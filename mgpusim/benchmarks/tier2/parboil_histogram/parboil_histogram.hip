// parboil_histogram.hip — Parboil Histogram benchmark (HIP, self-contained)
//
// Computes a 256-bin histogram of N random uint32 values using atomic operations.
// Each element is mapped to a bin via: bin = element & 0xFF (lower 8 bits).
//
// Algorithm:
//   - One GPU kernel: each thread reads one element, computes bin, atomicAdd to hist
//   - 256 threads per block, ceil(N/256) blocks
//   - Runs 5 iterations (histogram reset to 0 before each), reports average time
//   - CPU verify: compare all 256 bins exactly
//
// Default: N = 16*1024*1024 = 16777216 elements
//
// Compiles with hipcc (AMD ROCm) or nvcc -x cu (CUDA).
//
// Usage:
//   ./parboil_histogram [--n N]
//
// Output (stdout): CSV — histogram,<N>,<time_ms>,<GBs>
// Output (stderr): device info, timing details, PASS/FAIL

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
#include <cstdint>
#include <vector>

// ---------------------------------------------------------------------------
// Inlined utilities
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
// Constants
// ---------------------------------------------------------------------------

#define NUM_BINS   256
#define BLOCK_SIZE 256

// ---------------------------------------------------------------------------
// GPU kernel: each thread reads one element and atomically increments the bin
// ---------------------------------------------------------------------------
__global__ void histogram_kernel(const uint32_t* __restrict__ data,
                                  uint32_t* __restrict__ hist,
                                  uint32_t n)
{
    uint32_t idx = blockIdx.x * blockDim.x + threadIdx.x;
    if (idx >= n) return;
    uint32_t bin = data[idx] & 0xFF;
    atomicAdd(&hist[bin], 1u);
}

// ---------------------------------------------------------------------------
// CPU reference histogram for verification
// ---------------------------------------------------------------------------
static void cpu_histogram(const uint32_t* data, uint32_t* hist, uint32_t n)
{
    memset(hist, 0, NUM_BINS * sizeof(uint32_t));
    for (uint32_t i = 0; i < n; ++i) {
        hist[data[i] & 0xFF]++;
    }
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------
int main(int argc, char** argv)
{
    setvbuf(stdout, NULL, _IONBF, 0);

    // Default values from params.json
    uint32_t n     = 16 * 1024 * 1024;
    int      num_bins = 256;
    int      block_size = 256;

    // Command-line args as fallback
    n     = (uint32_t)parseIntParam(argc, argv, "--n", (int)n);
    num_bins = parseIntParam(argc, argv, "--num_bins", num_bins);
    block_size = parseIntParam(argc, argv, "--block_size", block_size);

    // Env vars override
    const char* env_val;
    env_val = getenv("BENCH_PARAM_n");
    if (env_val) n = (uint32_t)atoi(env_val);
    env_val = getenv("BENCH_PARAM_num_bins");
    if (env_val) num_bins = atoi(env_val);
    env_val = getenv("BENCH_PARAM_block_size");
    if (env_val) block_size = atoi(env_val);
    int num_warmup = 5;

    size_t bytes_data = (size_t)n * sizeof(uint32_t);
    size_t bytes_hist = (size_t)NUM_BINS * sizeof(uint32_t);

    // Print device info
    int device_id = 0;
    HIP_CHECK(hipGetDevice(&device_id));
    hipDeviceProp_t prop;
    HIP_CHECK(hipGetDeviceProperties(&prop, device_id));
    fprintf(stderr, "Device: %s (id=%d)\n", prop.name, device_id);
    fprintf(stderr, "N = %u elements  |  Bins = %d  |  Iterations = %d\n\n",
            n, NUM_BINS);

    // Host allocations
    uint32_t* h_data = (uint32_t*)malloc(bytes_data);
    uint32_t* h_hist = (uint32_t*)malloc(bytes_hist);
    uint32_t* h_ref  = (uint32_t*)malloc(bytes_hist);

    // Initialize data with pseudo-random uint32 values
    srand(42);
    for (uint32_t i = 0; i < n; ++i) {
        // Combine multiple rand() calls to fill 32 bits
        h_data[i] = ((uint32_t)rand() ^ ((uint32_t)rand() << 15) ^ ((uint32_t)rand() << 30));
    }

    // Device allocations
    uint32_t *d_data, *d_hist;
    HIP_CHECK(hipMalloc(&d_data, bytes_data));
    HIP_CHECK(hipMalloc(&d_hist, bytes_hist));

    // Copy input data to device (done once, reused across iterations)
    HIP_CHECK(hipMemcpy(d_data, h_data, bytes_data, hipMemcpyHostToDevice));

    uint32_t blocks = (n + BLOCK_SIZE - 1) / BLOCK_SIZE;

    BenchmarkTimer timer;

    // Warmup
    for (int w = 0; w < num_warmup; ++w) {
        HIP_CHECK(hipMemset(d_hist, 0, bytes_hist));
        hipLaunchKernelGGL(histogram_kernel, blocks, BLOCK_SIZE, 0, 0, d_data, d_hist, n);
        HIP_CHECK(hipDeviceSynchronize());
    }

    // Timed iterations
    std::vector<double> times(1);
    for (int iter = 0; iter < 1; ++iter) {
        // Reset histogram before each iteration
        HIP_CHECK(hipMemset(d_hist, 0, bytes_hist));
        timer.record_start();
        hipLaunchKernelGGL(histogram_kernel, blocks, BLOCK_SIZE, 0, 0, d_data, d_hist, n);
        timer.record_stop();
        times[iter] = static_cast<double>(timer.elapsed_ms());
    }

    // Copy result histogram back
    HIP_CHECK(hipMemcpy(h_hist, d_hist, bytes_hist, hipMemcpyDeviceToHost));

    // CPU reference verification
    cpu_histogram(h_data, h_ref, n);

    bool pass = true;
    for (int b = 0; b < NUM_BINS; ++b) {
        if (h_hist[b] != h_ref[b]) {
            fprintf(stderr, "MISMATCH bin[%d]: gpu=%u cpu=%u\n", b, h_hist[b], h_ref[b]);
            pass = false;
        }
    }
    fprintf(stderr, "Correctness check (all %d bins): %s\n\n",
            NUM_BINS, pass ? "PASS" : "FAIL");

    // Compute statistics
    double sum_ms = 0.0, mn = DBL_MAX, mx = 0.0;
    for (int i = 0; i < 1; ++i) {
        sum_ms += times[i];
        if (times[i] < mn) mn = times[i];
        if (times[i] > mx) mx = times[i];
    }
    double avg_ms = sum_ms / 1;

    double variance = 0.0;
    for (int i = 0; i < 1; ++i) {
        double d = times[i] - avg_ms;
        variance += d * d;
    }
    double stddev = (1 > 1) ? sqrt(variance / (1 - 1)) : 0.0;

    // GB/s: N * sizeof(uint32) bytes read
    double gb        = (double)n * sizeof(uint32_t) / 1.0e9;
    double gbps      = gb / (avg_ms * 1e-3);
    double melems_s  = (double)n / (avg_ms * 1e-3) / 1.0e6;

    fprintf(stderr, "Bandwidth:     %.2f GB/s\n", gbps);
    fprintf(stderr, "Throughput:    %.2f Melements/s\n", melems_s);
    fprintf(stderr, "Timing:        avg %.4f ms  min %.4f ms  max %.4f ms  stddev %.4f ms\n",
            avg_ms, mn, mx, stddev);

    // JSON-lines output
    printf("{\"type\":\"kernel\",\"name\":\"histogram_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"n\":%u,\"num_bins\":%d,"
           "\"block_size\":%d}}\n",
           avg_ms, n, num_bins, block_size);

    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"bandwidth_gbps\",\"value\":%.2f}]}\n",
           sum_ms, gbps);

    // Cleanup
    HIP_CHECK(hipFree(d_data));
    HIP_CHECK(hipFree(d_hist));
    free(h_data);
    free(h_hist);
    free(h_ref);

    return 0;
}
