// shoc_reduction.hip — SHOC Reduction benchmark (HIP, self-contained)
//
// Parallel sum reduction: reduces an array of floats to a single sum using
// tree-based shared-memory reduction. Derived from the SHOC benchmark suite.
//
// Parameters are read from BENCH_PARAM_* environment variables,
// with command-line --flags as fallback.
//
// Output (stdout): JSON-lines protocol
// Output (stderr): Human-readable diagnostics

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
#include <cfloat>
#include <vector>

// ---------------------------------------------------------------------------
// Inlined bench_common_hip.h utilities
// ---------------------------------------------------------------------------

// Error-checking macro
#define HIP_CHECK(cmd)                                                         \
    do {                                                                       \
        hipError_t _e = (cmd);                                                 \
        if (_e != hipSuccess) {                                                \
            fprintf(stderr, "HIP error %s at %s:%d\n",                        \
                    hipGetErrorString(_e), __FILE__, __LINE__);                \
            exit(1);                                                           \
        }                                                                      \
    } while (0)

// Parse --<name> N from argv (returns defaultVal if not found)
static int parseIntParam(int argc, char** argv, const char* name,
                         int defaultVal) {
    for (int i = 1; i < argc - 1; ++i) {
        if (strcmp(argv[i], name) == 0) {
            int v = atoi(argv[i + 1]);
            if (v > 0) return v;
            break;
        }
    }
    return defaultVal;
}

static const char* parseStrParam(int argc, char** argv, const char* name,
                                 const char* defaultVal) {
    for (int i = 1; i < argc - 1; ++i) {
        if (strcmp(argv[i], name) == 0) {
            return argv[i + 1];
        }
    }
    return defaultVal;
}

// HIP-event-based timer
struct BenchmarkTimer {
    hipEvent_t start, stop;
    BenchmarkTimer() {
        HIP_CHECK(hipEventCreate(&start));
        HIP_CHECK(hipEventCreate(&stop));
    }
    ~BenchmarkTimer() {
        (void)hipEventDestroy(start);
        (void)hipEventDestroy(stop);
    }
    void record_start(hipStream_t stream = 0) {
        HIP_CHECK(hipEventRecord(start, stream));
    }
    void record_stop(hipStream_t stream = 0) {
        HIP_CHECK(hipEventRecord(stop, stream));
        HIP_CHECK(hipEventSynchronize(stop));
    }
    float elapsed_ms() const {
        float ms = 0.0f;
        HIP_CHECK(hipEventElapsedTime(&ms, start, stop));
        return ms;
    }
};

// ---------------------------------------------------------------------------
// Reduction kernel — shared-memory tree reduction
// ---------------------------------------------------------------------------
__global__ void reduce_kernel(const float* __restrict__ input,
                               float* __restrict__ output,
                               int N)
{
    extern __shared__ float sdata[];

    unsigned int tid = threadIdx.x;
    unsigned int i   = blockIdx.x * blockDim.x * 2 + threadIdx.x;

    float val = 0.0f;
    if (i < (unsigned int)N)
        val = input[i];
    if (i + blockDim.x < (unsigned int)N)
        val += input[i + blockDim.x];
    sdata[tid] = val;
    __syncthreads();

    for (unsigned int s = blockDim.x / 2; s > 0; s >>= 1) {
        if (tid < s) {
            sdata[tid] += sdata[tid + s];
        }
        __syncthreads();
    }

    if (tid == 0) {
        output[blockIdx.x] = sdata[0];
    }
}

// ---------------------------------------------------------------------------
// Two-pass GPU reduction
// ---------------------------------------------------------------------------
static void gpu_reduce(const float* d_input, float* d_partial,
                       float* d_result, int N, int block_size)
{
    int elems_per_block = block_size * 2;
    int num_blocks      = (N + elems_per_block - 1) / elems_per_block;
    int shared_bytes    = block_size * sizeof(float);

    hipLaunchKernelGGL(reduce_kernel, dim3(num_blocks), dim3(block_size),
                       shared_bytes, 0,
                       d_input, d_partial, N);

    if (num_blocks == 1) {
        HIP_CHECK(hipMemcpy(d_result, d_partial, sizeof(float),
                             hipMemcpyDeviceToDevice));
        return;
    }

    int remaining = num_blocks;
    float* d_src  = d_partial;
    float* d_dst  = nullptr;

    float* d_temp = nullptr;
    bool   allocated_temp = false;

    while (remaining > 1) {
        int next_blocks  = (remaining + elems_per_block - 1) / elems_per_block;

        if (next_blocks == 1) {
            hipLaunchKernelGGL(reduce_kernel, dim3(1), dim3(block_size),
                               shared_bytes, 0,
                               d_src, d_result, remaining);
            remaining = 1;
        } else {
            if (!allocated_temp) {
                HIP_CHECK(hipMalloc(&d_temp, next_blocks * sizeof(float)));
                allocated_temp = true;
            }
            hipLaunchKernelGGL(reduce_kernel, dim3(next_blocks), dim3(block_size),
                               shared_bytes, 0,
                               d_src, d_temp, remaining);
            d_src     = d_temp;
            remaining = next_blocks;
        }
    }

    if (allocated_temp) {
        HIP_CHECK(hipFree(d_temp));
    }
}

// ---------------------------------------------------------------------------
// CPU reference
// ---------------------------------------------------------------------------
static double reduction_cpu(const float* data, int N) {
    double sum = 0.0;
    for (int i = 0; i < N; ++i) {
        sum += (double)data[i];
    }
    return sum;
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------
int main(int argc, char** argv)
{
    setvbuf(stdout, NULL, _IONBF, 0);

    // Defaults
    int size       = 4194304;
    int block_size = 256;
    const char* precision = "float";

    // Command-line fallback
    size       = parseIntParam(argc, argv, "--size", size);
    block_size = parseIntParam(argc, argv, "--block_size", block_size);
    precision  = parseStrParam(argc, argv, "--precision", precision);

    // Environment variable overrides
    const char* env_val;
    env_val = getenv("BENCH_PARAM_size");
    if (env_val) size = atoi(env_val);
    env_val = getenv("BENCH_PARAM_block_size");
    if (env_val) block_size = atoi(env_val);
    env_val = getenv("BENCH_PARAM_precision");
    if (env_val) precision = env_val;
    int num_warmup = 5;

    int N     = size;
    size_t bytesIn = (size_t)N * sizeof(float);

    // Print device info
    int device_id = 0;
    HIP_CHECK(hipGetDevice(&device_id));
    hipDeviceProp_t prop;
    HIP_CHECK(hipGetDeviceProperties(&prop, device_id));
    fprintf(stderr, "Device: %s (id=%d)\n", prop.name, device_id);
    fprintf(stderr, "Array size: %d floats (%.1f MiB)\n",
            N, (double)bytesIn / (1024.0 * 1024.0));
    fprintf(stderr, "Block size: %d\n\n", block_size);

    // Host allocation
    float* h_input = (float*)malloc(bytesIn);

    // Initialize with a known pattern
    for (int i = 0; i < N; ++i) {
        h_input[i] = (float)(i % 1000) * 0.001f;
    }

    // Device allocations
    int elems_per_block = block_size * 2;
    int num_blocks      = (N + elems_per_block - 1) / elems_per_block;

    float *d_input, *d_partial, *d_result;
    HIP_CHECK(hipMalloc(&d_input,   bytesIn));
    HIP_CHECK(hipMalloc(&d_partial, (size_t)num_blocks * sizeof(float)));
    HIP_CHECK(hipMalloc(&d_result,  sizeof(float)));

    HIP_CHECK(hipMemcpy(d_input, h_input, bytesIn, hipMemcpyHostToDevice));

    // Warmup
    for (int w = 0; w < num_warmup; ++w) {
        gpu_reduce(d_input, d_partial, d_result, N, block_size);
        HIP_CHECK(hipDeviceSynchronize());
    }

    // Timed iterations
    BenchmarkTimer timer;
    std::vector<double> times(1);
    for (int i = 0; i < 1; ++i) {
        timer.record_start();
        gpu_reduce(d_input, d_partial, d_result, N, block_size);
        timer.record_stop();
        times[i] = static_cast<double>(timer.elapsed_ms());
    }

    // Statistics
    double sum_t = 0.0, mn = DBL_MAX, mx = 0.0;
    for (int i = 0; i < 1; ++i) {
        sum_t += times[i];
        if (times[i] < mn) mn = times[i];
        if (times[i] > mx) mx = times[i];
    }
    double avg_ms = sum_t / 1;
    double total_ms = sum_t;

    // Effective bandwidth: 2 * N * sizeof(float) (read + result write)
    double bandwidth_gb = 2.0 * (double)N * sizeof(float)
                          / (avg_ms * 1e-3) / 1e9;

    // JSON-lines kernel event
    printf("{\"type\":\"kernel\",\"name\":\"reduce_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"size\":%d,\"block_size\":%d,"
           "\"precision\":\"%s\"}}\n",
           avg_ms, size, block_size, precision);

    // JSON-lines summary event
    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"bandwidth_gbps\",\"value\":%.2f}]}\n",
           total_ms, bandwidth_gb);

    fprintf(stderr, "Effective bandwidth: %.2f GB/s  (avg %.4f ms)\n",
            bandwidth_gb, avg_ms);

    // Verify
    float gpu_result = 0.0f;
    HIP_CHECK(hipMemcpy(&gpu_result, d_result, sizeof(float),
                         hipMemcpyDeviceToHost));
    double cpu_result = reduction_cpu(h_input, N);

    double rel_err = fabs((double)gpu_result - cpu_result)
                     / (fabs(cpu_result) + 1e-10);
    fprintf(stderr, "GPU sum: %f, CPU sum: %f, relative error: %e\n",
            gpu_result, cpu_result, rel_err);

    if (rel_err < 1e-3) {
        fprintf(stderr, "PASS\n");
    } else {
        fprintf(stderr, "FAIL: relative error too large\n");
    }

    // Cleanup
    HIP_CHECK(hipFree(d_input));
    HIP_CHECK(hipFree(d_partial));
    HIP_CHECK(hipFree(d_result));
    free(h_input);

    return (rel_err >= 1e-3) ? 1 : 0;
}
