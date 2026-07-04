// cuda_scan_large.hip — Large-array parallel prefix sum (Blelloch scan) benchmark (HIP)
//
// Work-efficient parallel prefix sum (exclusive scan) for large arrays.
// Three-phase hierarchical decomposition:
//   1. Block-level scan of each block
//   2. Scan of block sums
//   3. Add block sums back to each block
//
// Measures throughput in GB/s = 2 * N * sizeof(uint) / time_s / 1e9.
//
// Compiles with hipcc (AMD ROCm) or nvcc via HIP compatibility layer (CUDA).
//
// Usage:
//   ./cuda_scan_large [--size N]
//
//   --size N         Number of elements (default: 16777216 = 16M)
//
// Output (stdout): CSV row — cuda_scan_large,<N>,<time_ms>,<GBs>
// Output (stderr): human-readable results

#if defined(__NVCC__) && !__has_include(<hip/hip_runtime.h>)
#include <cuda_runtime.h>
#define hipMalloc               cudaMalloc
#define hipFree                 cudaFree
#define hipMemcpy               cudaMemcpy
#define hipMemcpyAsync          cudaMemcpyAsync
#define hipMemset               cudaMemset
#define hipMemsetAsync          cudaMemsetAsync
#define hipMemcpyHostToDevice   cudaMemcpyHostToDevice
#define hipMemcpyDeviceToHost   cudaMemcpyDeviceToHost
#define hipMemcpyDeviceToDevice cudaMemcpyDeviceToDevice
#define hipMemcpyToSymbol       cudaMemcpyToSymbol
#define hipDeviceSynchronize    cudaDeviceSynchronize
#define hipGetDevice            cudaGetDevice
#define hipGetDeviceProperties  cudaGetDeviceProperties
#define hipGetLastError         cudaGetLastError
#define hipGetErrorString       cudaGetErrorString
#define hipEvent_t              cudaEvent_t
#define hipEventCreate          cudaEventCreate
#define hipEventDestroy         cudaEventDestroy
#define hipEventRecord          cudaEventRecord
#define hipEventSynchronize     cudaEventSynchronize
#define hipEventElapsedTime     cudaEventElapsedTime
#define hipStream_t             cudaStream_t
#define hipStreamCreate         cudaStreamCreate
#define hipStreamDestroy        cudaStreamDestroy
#define hipStreamSynchronize    cudaStreamSynchronize
#define hipDeviceProp_t         cudaDeviceProp
#define hipError_t              cudaError_t
#define hipSuccess              cudaSuccess
#define hipHostMalloc           cudaMallocHost
#define hipHostFree             cudaFreeHost
#define hipHostMallocDefault    cudaHostAllocDefault
#define hipBlockIdx_x           blockIdx.x
#define hipBlockIdx_y           blockIdx.y
#define hipBlockIdx_z           blockIdx.z
#define hipThreadIdx_x          threadIdx.x
#define hipThreadIdx_y          threadIdx.y
#define hipThreadIdx_z          threadIdx.z
#define hipBlockDim_x           blockDim.x
#define hipBlockDim_y           blockDim.y
#define hipBlockDim_z           blockDim.z
#define hipGridDim_x            gridDim.x
#define hipGridDim_y            gridDim.y
#define hipGridDim_z            gridDim.z
#define __HIP_PLATFORM_AMD__    0
#define hipLaunchKernelGGL(kernel, grid, block, smem, stream, ...) \
    kernel<<<grid, block, smem, stream>>>(__VA_ARGS__)
#else
#include <hip/hip_runtime.h>
#endif
#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <cmath>
#include <vector>

// ---------------------------------------------------------------------------
// Error-checking macro
// ---------------------------------------------------------------------------

#define HIP_CHECK(cmd)                                                         \
    do {                                                                       \
        hipError_t _e = (cmd);                                                 \
        if (_e != hipSuccess) {                                                \
            fprintf(stderr, "HIP error %s at %s:%d\n",                        \
                    hipGetErrorString(_e), __FILE__, __LINE__);                \
            exit(1);                                                           \
        }                                                                      \
    } while (0)

// ---------------------------------------------------------------------------
// Argument parsing
// ---------------------------------------------------------------------------

static int parseIntParam(int argc, char** argv, const char* name, int defaultVal) {
    for (int i = 1; i < argc - 1; ++i) {
        if (strcmp(argv[i], name) == 0) {
            int v = atoi(argv[i + 1]);
            if (v > 0) return v;
            break;
        }
    }
    return defaultVal;
}

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

#define BLOCK_SIZE 256
#define ELEMENTS_PER_BLOCK (2 * BLOCK_SIZE)

// ---------------------------------------------------------------------------
// Kernel: Blelloch work-efficient scan (per-block)
//   Each block processes ELEMENTS_PER_BLOCK elements.
//   Stores the block total in block_sums[blockIdx.x] before zeroing the root.
// ---------------------------------------------------------------------------

__global__ void scan_block_kernel(
    unsigned int* __restrict__ data,
    unsigned int* __restrict__ block_sums,
    int N)
{
    __shared__ unsigned int temp[ELEMENTS_PER_BLOCK];

    int tid = threadIdx.x;
    int blockOffset = blockIdx.x * ELEMENTS_PER_BLOCK;

    // Load input into shared memory
    int ai = tid;
    int bi = tid + BLOCK_SIZE;
    int ga = blockOffset + ai;
    int gb = blockOffset + bi;

    temp[ai] = (ga < N) ? data[ga] : 0;
    temp[bi] = (gb < N) ? data[gb] : 0;

    // Up-sweep (reduce) phase
    int offset = 1;
    for (int d = ELEMENTS_PER_BLOCK >> 1; d > 0; d >>= 1) {
        __syncthreads();
        if (tid < d) {
            int ai2 = offset * (2 * tid + 1) - 1;
            int bi2 = offset * (2 * tid + 2) - 1;
            temp[bi2] += temp[ai2];
        }
        offset <<= 1;
    }

    __syncthreads();

    // Store block sum and clear last element
    if (tid == 0) {
        if (block_sums != nullptr) {
            block_sums[blockIdx.x] = temp[ELEMENTS_PER_BLOCK - 1];
        }
        temp[ELEMENTS_PER_BLOCK - 1] = 0;
    }

    // Down-sweep phase
    for (int d = 1; d < ELEMENTS_PER_BLOCK; d <<= 1) {
        offset >>= 1;
        __syncthreads();
        if (tid < d) {
            int ai2 = offset * (2 * tid + 1) - 1;
            int bi2 = offset * (2 * tid + 2) - 1;
            unsigned int t = temp[ai2];
            temp[ai2] = temp[bi2];
            temp[bi2] += t;
        }
    }

    __syncthreads();

    // Write results back
    if (ga < N) data[ga] = temp[ai];
    if (gb < N) data[gb] = temp[bi];
}

// ---------------------------------------------------------------------------
// Kernel: add block sums to each element in the block
// ---------------------------------------------------------------------------

__global__ void add_block_sums_kernel(
    unsigned int* __restrict__ data,
    const unsigned int* __restrict__ block_sums,
    int N)
{
    int idx = blockIdx.x * ELEMENTS_PER_BLOCK + threadIdx.x;
    unsigned int val = block_sums[blockIdx.x];

    if (idx < N)
        data[idx] += val;
    if (idx + BLOCK_SIZE < N)
        data[idx + BLOCK_SIZE] += val;
}

// ---------------------------------------------------------------------------
// Recursive scan for large arrays
// ---------------------------------------------------------------------------

// When accumulators are supplied, each kernel launch is timed separately and
// summed across ALL recursion levels into the corresponding accumulator.
// Passing nullptr (warmup) runs untimed.
static void scan_recursive(unsigned int* d_data, int N,
                           double* block_acc, double* add_acc,
                           hipEvent_t kStart, hipEvent_t kStop) {
    int numBlocks = (N + ELEMENTS_PER_BLOCK - 1) / ELEMENTS_PER_BLOCK;

    unsigned int* d_block_sums = nullptr;
    if (numBlocks > 1) {
        HIP_CHECK(hipMalloc(&d_block_sums, numBlocks * sizeof(unsigned int)));
    }

    // Phase 1: Block-level scan
    if (block_acc) {
        HIP_CHECK(hipEventRecord(kStart, 0));
        hipLaunchKernelGGL(scan_block_kernel,
                           dim3(numBlocks), dim3(BLOCK_SIZE), 0, 0,
                           d_data, d_block_sums, N);
        HIP_CHECK(hipEventRecord(kStop, 0));
        HIP_CHECK(hipEventSynchronize(kStop));
        float _ms = 0.0f; HIP_CHECK(hipEventElapsedTime(&_ms, kStart, kStop));
        *block_acc += (double)_ms;
    } else {
        hipLaunchKernelGGL(scan_block_kernel,
                           dim3(numBlocks), dim3(BLOCK_SIZE), 0, 0,
                           d_data, d_block_sums, N);
    }

    if (numBlocks > 1) {
        // Phase 2: Recursively scan the block sums
        scan_recursive(d_block_sums, numBlocks, block_acc, add_acc, kStart, kStop);

        // Phase 3: Add scanned block sums back to each block
        if (add_acc) {
            HIP_CHECK(hipEventRecord(kStart, 0));
            hipLaunchKernelGGL(add_block_sums_kernel,
                               dim3(numBlocks), dim3(BLOCK_SIZE), 0, 0,
                               d_data, d_block_sums, N);
            HIP_CHECK(hipEventRecord(kStop, 0));
            HIP_CHECK(hipEventSynchronize(kStop));
            float _ms = 0.0f; HIP_CHECK(hipEventElapsedTime(&_ms, kStart, kStop));
            *add_acc += (double)_ms;
        } else {
            hipLaunchKernelGGL(add_block_sums_kernel,
                               dim3(numBlocks), dim3(BLOCK_SIZE), 0, 0,
                               d_data, d_block_sums, N);
        }

        HIP_CHECK(hipFree(d_block_sums));
    }
}

// ---------------------------------------------------------------------------
// CPU reference: exclusive prefix sum
// ---------------------------------------------------------------------------

static void prefix_sum_cpu(const unsigned int* input, unsigned int* output, int N) {
    unsigned int sum = 0;
    for (int i = 0; i < N; ++i) {
        output[i] = sum;
        sum += input[i];
    }
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------

static const char* parseStrParam(int argc, char** argv, const char* name,
                                 const char* defaultVal) {
    for (int i = 1; i < argc - 1; ++i) {
        if (strcmp(argv[i], name) == 0) {
            return argv[i + 1];
        }
    }
    return defaultVal;
}

int main(int argc, char** argv) {
    setvbuf(stdout, NULL, _IONBF, 0);

    // Defaults from params.json
    int size       = 16777216;
    int block_size = 256;
    const char* verify = "true";

    // Command-line fallback
    size       = parseIntParam(argc, argv, "--size", size);
    block_size = parseIntParam(argc, argv, "--block_size", block_size);
    verify     = parseStrParam(argc, argv, "--verify", verify);

    // Environment variable overrides
    const char* env_val;
    env_val = getenv("BENCH_PARAM_size");
    if (env_val) size = atoi(env_val);
    env_val = getenv("BENCH_PARAM_block_size");
    if (env_val) block_size = atoi(env_val);
    env_val = getenv("BENCH_PARAM_verify");
    if (env_val) verify = env_val;
    int num_warmup = 5;

    int N     = size;

    size_t bytes = (size_t)N * sizeof(unsigned int);

    // Print device info
    int device_id = 0;
    HIP_CHECK(hipGetDevice(&device_id));
    hipDeviceProp_t prop;
    HIP_CHECK(hipGetDeviceProperties(&prop, device_id));
    fprintf(stderr, "Device: %s (id=%d)\n", prop.name, device_id);
    fprintf(stderr, "Scan size: %d warmup + %d timed\n\n", N, num_warmup);

    // Host allocation + initialization
    unsigned int* h_input  = (unsigned int*)malloc(bytes);
    unsigned int* h_output = (unsigned int*)malloc(bytes);
    for (int i = 0; i < N; ++i) {
        h_input[i] = (unsigned int)(i % 10);  // small values to avoid overflow
    }

    // Device allocation
    unsigned int* d_data;
    HIP_CHECK(hipMalloc(&d_data, bytes));

    // Per-kernel timing events (used only when accumulators are supplied)
    hipEvent_t kStart, kStop;
    HIP_CHECK(hipEventCreate(&kStart));
    HIP_CHECK(hipEventCreate(&kStop));

    // Lambda: run full scan. When accumulators are supplied, each kernel type
    // is timed and summed across all recursion levels; nullptr runs untimed.
    auto run_scan = [&](double* block_acc, double* add_acc) {
        HIP_CHECK(hipMemcpy(d_data, h_input, bytes, hipMemcpyHostToDevice));
        scan_recursive(d_data, N, block_acc, add_acc, kStart, kStop);
    };

    // -------------------------------------------------------------------
    // Warmup
    // -------------------------------------------------------------------
    for (int w = 0; w < num_warmup; ++w) {
        run_scan(nullptr, nullptr);
        HIP_CHECK(hipDeviceSynchronize());
    }

    // -------------------------------------------------------------------
    // Timed iterations — each kernel type summed across all recursion levels
    // -------------------------------------------------------------------
    double block_ms = 0.0, add_ms = 0.0;
    for (int i = 0; i < 1; ++i) {
        run_scan(&block_ms, &add_ms);
    }

    HIP_CHECK(hipEventDestroy(kStart));
    HIP_CHECK(hipEventDestroy(kStop));

    double total_ms = block_ms + add_ms;
    double avg_ms = total_ms;

    // GB/s = 2 * N * sizeof(uint) / time_s / 1e9
    double data_bytes = 2.0 * (double)N * sizeof(unsigned int);
    double gbs = data_bytes / (avg_ms * 1e-3) / 1e9;

    // JSON-lines kernel events
    printf("{\"type\":\"kernel\",\"name\":\"scan_block_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"size\":%d,\"block_size\":%d,"
           "\"verify\":\"%s\"}}\n",
           block_ms, size, block_size, verify);

    printf("{\"type\":\"kernel\",\"name\":\"add_block_sums_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"size\":%d,\"block_size\":%d,"
           "\"verify\":\"%s\"}}\n",
           add_ms, size, block_size, verify);

    // JSON-lines summary event
    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"throughput_gbps\",\"value\":%.2f}]}\n",
           total_ms, gbs);

    // Human-readable output to stderr
    fprintf(stderr, "Average time: %.4f ms\n", avg_ms);
    fprintf(stderr, "Throughput:   %.4f GB/s\n", gbs);

    // -------------------------------------------------------------------
    // Verification
    // -------------------------------------------------------------------
    {
        HIP_CHECK(hipMemcpy(h_output, d_data, bytes, hipMemcpyDeviceToHost));

        // CPU reference
        unsigned int* h_ref = (unsigned int*)malloc(bytes);
        prefix_sum_cpu(h_input, h_ref, N);

        int errors = 0;
        for (int i = 0; i < N; ++i) {
            if (h_output[i] != h_ref[i]) {
                if (errors < 10) {
                    fprintf(stderr, "Mismatch at %d: GPU=%u CPU=%u\n",
                            i, h_output[i], h_ref[i]);
                }
                errors++;
            }
        }

        if (errors > 0)
            fprintf(stderr, "FAIL: %d errors out of %d\n", errors, N);
        else
            fprintf(stderr, "PASS\n");

        free(h_ref);
    }

    HIP_CHECK(hipFree(d_data));
    free(h_input);
    free(h_output);

    return 0;
}
