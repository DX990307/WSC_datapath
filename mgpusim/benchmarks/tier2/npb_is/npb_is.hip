// npb_is.hip — NAS Parallel Benchmarks Integer Sort (IS)
//
// Parallel bucket sort of N random integers in range [0, MAX_KEY).
// Phase 1: histogram (count per bucket)
// Phase 2: prefix sum (exclusive scan) on histogram
// Phase 3: scatter keys to sorted positions
//
// Compiles with hipcc (AMD ROCm) or nvcc via HIP compatibility layer (CUDA).
//
// Usage:
//   ./npb_is [--size N]
//
//   --size N         Number of keys (default: 8388608 = 2^23)
//
// Output (stdout): CSV row — npb_is,<N>,<time_ms>,<Mkeys_per_sec>
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
// Constants
// ---------------------------------------------------------------------------

#define NUM_BUCKETS 1024    // 2^10
#define MAX_KEY     524288  // 2^19

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
// Kernel: histogram — count keys per bucket
//   Each key maps to bucket = key / (MAX_KEY / NUM_BUCKETS)
// ---------------------------------------------------------------------------

__global__ void histogram_kernel(const int* __restrict__ keys, int N,
                                 unsigned int* __restrict__ hist) {
    int idx = blockIdx.x * blockDim.x + threadIdx.x;
    if (idx >= N) return;

    int bucket = keys[idx] / (MAX_KEY / NUM_BUCKETS);
    if (bucket >= NUM_BUCKETS) bucket = NUM_BUCKETS - 1;
    atomicAdd(&hist[bucket], 1u);
}

// ---------------------------------------------------------------------------
// Kernel: prefix sum (exclusive scan) — simple single-block scan
//   Works for NUM_BUCKETS <= 1024 (one block)
// ---------------------------------------------------------------------------

__global__ void prefix_sum_kernel(unsigned int* hist, int n) {
    extern __shared__ unsigned int sdata[];

    int tid = threadIdx.x;
    if (tid < n)
        sdata[tid] = hist[tid];
    else
        sdata[tid] = 0;
    __syncthreads();

    // Up-sweep (reduce)
    for (int stride = 1; stride < n; stride <<= 1) {
        int index = (tid + 1) * (stride << 1) - 1;
        if (index < n) {
            sdata[index] += sdata[index - stride];
        }
        __syncthreads();
    }

    // Set last element to 0 for exclusive scan
    if (tid == 0) sdata[n - 1] = 0;
    __syncthreads();

    // Down-sweep
    for (int stride = n >> 1; stride >= 1; stride >>= 1) {
        int index = (tid + 1) * (stride << 1) - 1;
        if (index < n) {
            unsigned int temp = sdata[index - stride];
            sdata[index - stride] = sdata[index];
            sdata[index] += temp;
        }
        __syncthreads();
    }

    if (tid < n)
        hist[tid] = sdata[tid];
}

// ---------------------------------------------------------------------------
// Kernel: scatter keys to sorted positions
//   Uses atomicAdd on prefix-sum offsets to get unique output positions
// ---------------------------------------------------------------------------

__global__ void scatter_kernel(const int* __restrict__ keys, int N,
                               unsigned int* __restrict__ offsets,
                               int* __restrict__ sorted) {
    int idx = blockIdx.x * blockDim.x + threadIdx.x;
    if (idx >= N) return;

    int bucket = keys[idx] / (MAX_KEY / NUM_BUCKETS);
    if (bucket >= NUM_BUCKETS) bucket = NUM_BUCKETS - 1;

    unsigned int pos = atomicAdd(&offsets[bucket], 1u);
    sorted[pos] = keys[idx];
}

// ---------------------------------------------------------------------------
// CPU reference: generate keys with simple LCG
// ---------------------------------------------------------------------------

static void generate_keys(int* keys, int N) {
    unsigned int seed = 314159265u;
    for (int i = 0; i < N; ++i) {
        seed = seed * 1103515245u + 12345u;
        keys[i] = (int)((seed >> 4) % MAX_KEY);
    }
}

// ---------------------------------------------------------------------------
// CPU verification: check sorted output is non-decreasing within buckets
// ---------------------------------------------------------------------------

static bool verify_sorted(const int* sorted, int N) {
    for (int i = 1; i < N; ++i) {
        int bucket_prev = sorted[i - 1] / (MAX_KEY / NUM_BUCKETS);
        int bucket_curr = sorted[i] / (MAX_KEY / NUM_BUCKETS);
        if (bucket_curr < bucket_prev) {
            fprintf(stderr, "Verification failed at index %d: %d (bucket %d) > %d (bucket %d)\n",
                    i, sorted[i - 1], bucket_prev, sorted[i], bucket_curr);
            return false;
        }
    }
    return true;
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------

int main(int argc, char** argv) {
    setvbuf(stdout, NULL, _IONBF, 0);

    // Defaults from params.json
    int N          = 8388608;   // 2^23
    int block_size = 256;
    const char* verify = "true";

    // Command-line fallback
    N          = parseIntParam(argc, argv, "--size", N);

    // Environment variable overrides
    const char* env_val;
    env_val = getenv("BENCH_PARAM_size");
    if (env_val) N = atoi(env_val);
    env_val = getenv("BENCH_PARAM_block_size");
    if (env_val) block_size = atoi(env_val);
    env_val = getenv("BENCH_PARAM_verify");
    if (env_val) verify = env_val;
    int num_warmup = 5;


    // Print device info
    int device_id = 0;
    HIP_CHECK(hipGetDevice(&device_id));
    hipDeviceProp_t prop;
    HIP_CHECK(hipGetDeviceProperties(&prop, device_id));
    fprintf(stderr, "Device: %s (id=%d)\n", prop.name, device_id);
    fprintf(stderr, "IS keys: %d (2^%.0f)  |  Buckets: %d warmup + %d timed\n\n",
            N, log2((double)N), NUM_BUCKETS, num_warmup);

    size_t keysBytes = (size_t)N * sizeof(int);
    size_t histBytes = NUM_BUCKETS * sizeof(unsigned int);

    // Host allocation + key generation
    int* h_keys   = (int*)malloc(keysBytes);
    int* h_sorted = (int*)malloc(keysBytes);
    generate_keys(h_keys, N);

    // Device allocation
    int* d_keys;
    int* d_sorted;
    unsigned int* d_hist;
    unsigned int* d_offsets;  // copy of prefix-sum for scatter

    HIP_CHECK(hipMalloc(&d_keys, keysBytes));
    HIP_CHECK(hipMalloc(&d_sorted, keysBytes));
    HIP_CHECK(hipMalloc(&d_hist, histBytes));
    HIP_CHECK(hipMalloc(&d_offsets, histBytes));

    // Copy keys to device
    HIP_CHECK(hipMemcpy(d_keys, h_keys, keysBytes, hipMemcpyHostToDevice));

    // Kernel launch config
    int blockSize = block_size;
    int gridKeys  = (N + blockSize - 1) / blockSize;

    // Per-kernel timing events (used only when accumulators are supplied)
    hipEvent_t kStart, kStop;
    HIP_CHECK(hipEventCreate(&kStart));
    HIP_CHECK(hipEventCreate(&kStop));

    // Lambda: run full IS (histogram + prefix sum + scatter)
    // When accumulators are supplied, each phase launch is timed separately;
    // passing nullptr (warmup) runs untimed.
    auto run_is = [&](double* hist_acc, double* prefix_acc, double* scatter_acc) {
        // Clear histogram
        HIP_CHECK(hipMemset(d_hist, 0, histBytes));

        // Phase 1: histogram
        if (hist_acc) {
            HIP_CHECK(hipEventRecord(kStart, 0));
            hipLaunchKernelGGL(histogram_kernel, dim3(gridKeys), dim3(blockSize),
                               0, 0, d_keys, N, d_hist);
            HIP_CHECK(hipEventRecord(kStop, 0));
            HIP_CHECK(hipEventSynchronize(kStop));
            float _ms = 0.0f; HIP_CHECK(hipEventElapsedTime(&_ms, kStart, kStop));
            *hist_acc += (double)_ms;
        } else {
            hipLaunchKernelGGL(histogram_kernel, dim3(gridKeys), dim3(blockSize),
                               0, 0, d_keys, N, d_hist);
        }

        // Phase 2: prefix sum (exclusive scan on histogram)
        // Single-block scan, NUM_BUCKETS threads
        if (prefix_acc) {
            HIP_CHECK(hipEventRecord(kStart, 0));
            hipLaunchKernelGGL(prefix_sum_kernel, dim3(1), dim3(NUM_BUCKETS),
                               NUM_BUCKETS * sizeof(unsigned int), 0,
                               d_hist, NUM_BUCKETS);
            HIP_CHECK(hipEventRecord(kStop, 0));
            HIP_CHECK(hipEventSynchronize(kStop));
            float _ms = 0.0f; HIP_CHECK(hipEventElapsedTime(&_ms, kStart, kStop));
            *prefix_acc += (double)_ms;
        } else {
            hipLaunchKernelGGL(prefix_sum_kernel, dim3(1), dim3(NUM_BUCKETS),
                               NUM_BUCKETS * sizeof(unsigned int), 0,
                               d_hist, NUM_BUCKETS);
        }

        // Copy prefix sums to offsets (scatter will modify offsets)
        HIP_CHECK(hipMemcpy(d_offsets, d_hist, histBytes, hipMemcpyDeviceToDevice));

        // Phase 3: scatter
        if (scatter_acc) {
            HIP_CHECK(hipEventRecord(kStart, 0));
            hipLaunchKernelGGL(scatter_kernel, dim3(gridKeys), dim3(blockSize),
                               0, 0, d_keys, N, d_offsets, d_sorted);
            HIP_CHECK(hipEventRecord(kStop, 0));
            HIP_CHECK(hipEventSynchronize(kStop));
            float _ms = 0.0f; HIP_CHECK(hipEventElapsedTime(&_ms, kStart, kStop));
            *scatter_acc += (double)_ms;
        } else {
            hipLaunchKernelGGL(scatter_kernel, dim3(gridKeys), dim3(blockSize),
                               0, 0, d_keys, N, d_offsets, d_sorted);
        }
    };

    // -------------------------------------------------------------------
    // Warmup
    // -------------------------------------------------------------------
    for (int w = 0; w < num_warmup; ++w) {
        run_is(nullptr, nullptr, nullptr);
        HIP_CHECK(hipDeviceSynchronize());
    }

    // -------------------------------------------------------------------
    // Timed iterations — each phase timed into its own accumulator
    // -------------------------------------------------------------------
    double hist_ms = 0.0, prefix_ms = 0.0, scatter_ms = 0.0;
    for (int i = 0; i < 1; ++i) {
        run_is(&hist_ms, &prefix_ms, &scatter_ms);
    }

    HIP_CHECK(hipEventDestroy(kStart));
    HIP_CHECK(hipEventDestroy(kStop));

    double total_ms = hist_ms + prefix_ms + scatter_ms;
    double avg_ms = total_ms;

    // M keys/sec = N / time_s / 1e6
    double mkeys_sec = (double)N / (avg_ms * 1e-3) / 1e6;

    // JSON-lines kernel events (one per kernel)
    printf("{\"type\":\"kernel\",\"name\":\"histogram_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"size\":%d,\"block_size\":%d,"
           "\"verify\":\"%s\"}}\n",
           hist_ms, N, block_size, verify);

    printf("{\"type\":\"kernel\",\"name\":\"prefix_sum_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"size\":%d,\"block_size\":%d,"
           "\"verify\":\"%s\"}}\n",
           prefix_ms, N, block_size, verify);

    printf("{\"type\":\"kernel\",\"name\":\"scatter_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"size\":%d,\"block_size\":%d,"
           "\"verify\":\"%s\"}}\n",
           scatter_ms, N, block_size, verify);

    // JSON-lines summary event
    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"mkeys_per_sec\",\"value\":%.2f}]}\n",
           total_ms, mkeys_sec);

    // Human-readable output to stderr
    fprintf(stderr, "Average time: %.4f ms\n", avg_ms);
    fprintf(stderr, "Performance:  %.4f Mkeys/sec\n", mkeys_sec);

    // -------------------------------------------------------------------
    // Verification: check sorted output is bucket-ordered
    // -------------------------------------------------------------------
    HIP_CHECK(hipMemcpy(h_sorted, d_sorted, keysBytes, hipMemcpyDeviceToHost));

    if (verify_sorted(h_sorted, N))
        fprintf(stderr, "PASS\n");
    else
        fprintf(stderr, "FAIL\n");

    HIP_CHECK(hipFree(d_keys));
    HIP_CHECK(hipFree(d_sorted));
    HIP_CHECK(hipFree(d_hist));
    HIP_CHECK(hipFree(d_offsets));
    free(h_keys);
    free(h_sorted);

    return 0;
}
