// polybench_3dconv.hip — PolyBench 3D Convolution benchmark (HIP, self-contained)
//
// 3D convolution over an NxNxN volume with a small 3D filter (e.g., 3x3x3).
// Each output point is the weighted sum of its neighborhood.
// Derived from the PolyBench/GPU benchmark suite.
//
// Compiles with hipcc (AMD ROCm) or nvcc via HIP compatibility layer (CUDA).
//
// Output (stdout): JSON-lines
// Output (stderr): device info, timing details

#if defined(__NVCC__) && !__has_include(<hip/hip_runtime.h>)
#include <cuda_runtime.h>
#define hipMalloc               cudaMalloc
#define hipFree                 cudaFree
#define hipMemcpy               cudaMemcpy
#define hipMemcpyHostToDevice   cudaMemcpyHostToDevice
#define hipMemcpyDeviceToHost   cudaMemcpyDeviceToHost
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
#define hipDeviceProp_t         cudaDeviceProp
#define hipError_t              cudaError_t
#define hipSuccess              cudaSuccess
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
// Inlined error check macro
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
// Argument parsing helpers
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
// 3D Convolution Kernel
// ---------------------------------------------------------------------------

__global__ void conv3d_kernel(const float* __restrict__ input,
                              const float* __restrict__ filter,
                              float* __restrict__ output,
                              int N, int filter_size) {
    int k = blockIdx.x * blockDim.x + threadIdx.x;
    int j = blockIdx.y * blockDim.y + threadIdx.y;
    int i = blockIdx.z * blockDim.z + threadIdx.z;

    if (i >= N || j >= N || k >= N) return;

    int half = filter_size / 2;
    float sum = 0.0f;

    for (int fi = 0; fi < filter_size; ++fi) {
        int ii = i - half + fi;
        if (ii < 0 || ii >= N) continue;
        for (int fj = 0; fj < filter_size; ++fj) {
            int jj = j - half + fj;
            if (jj < 0 || jj >= N) continue;
            for (int fk = 0; fk < filter_size; ++fk) {
                int kk = k - half + fk;
                if (kk < 0 || kk >= N) continue;
                sum += input[((size_t)ii * N + jj) * N + kk]
                     * filter[((size_t)fi * filter_size + fj) * filter_size + fk];
            }
        }
    }

    output[((size_t)i * N + j) * N + k] = sum;
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------
int main(int argc, char** argv) {
    setvbuf(stdout, NULL, _IONBF, 0);

    int N           = parseIntParam(argc, argv, "--size", 128);
    int block_size  = parseIntParam(argc, argv, "--block_size", 8);
    int filter_size = parseIntParam(argc, argv, "--filter_size", 3);

    const char* env_val;
    env_val = getenv("BENCH_PARAM_size");
    if (env_val) N = atoi(env_val);
    env_val = getenv("BENCH_PARAM_block_size");
    if (env_val) block_size = atoi(env_val);
    env_val = getenv("BENCH_PARAM_filter_size");
    if (env_val) filter_size = atoi(env_val);
    int num_warmup = 5;

    size_t vol_elems   = (size_t)N * N * N;
    size_t vol_bytes   = vol_elems * sizeof(float);
    size_t filt_elems  = (size_t)filter_size * filter_size * filter_size;
    size_t filt_bytes  = filt_elems * sizeof(float);

    // Print device info
    int device_id = 0;
    HIP_CHECK(hipGetDevice(&device_id));
    hipDeviceProp_t prop;
    HIP_CHECK(hipGetDeviceProperties(&prop, device_id));
    fprintf(stderr, "Device: %s (id=%d)\n", prop.name, device_id);
    fprintf(stderr, "Volume: %dx%dx%d  |  Filter: %dx%dx%d\n\n",
            N, N, N, filter_size, filter_size, filter_size);

    // Host allocations
    float* h_input  = (float*)malloc(vol_bytes);
    float* h_filter = (float*)malloc(filt_bytes);
    float* h_output = (float*)malloc(vol_bytes);

    // Synthetic data init
    srand(42);
    for (size_t i = 0; i < vol_elems; ++i)
        h_input[i] = (float)(rand() % 100) / 100.0f;
    float filt_sum = 0.0f;
    for (size_t i = 0; i < filt_elems; ++i) {
        h_filter[i] = (float)(rand() % 10 + 1);
        filt_sum += h_filter[i];
    }
    for (size_t i = 0; i < filt_elems; ++i)
        h_filter[i] /= filt_sum;

    // Device allocations
    float *d_input, *d_filter, *d_output;
    HIP_CHECK(hipMalloc(&d_input,  vol_bytes));
    HIP_CHECK(hipMalloc(&d_filter, filt_bytes));
    HIP_CHECK(hipMalloc(&d_output, vol_bytes));

    HIP_CHECK(hipMemcpy(d_input,  h_input,  vol_bytes,  hipMemcpyHostToDevice));
    HIP_CHECK(hipMemcpy(d_filter, h_filter, filt_bytes, hipMemcpyHostToDevice));

    dim3 block(block_size, block_size, block_size);
    dim3 grid((N + block_size - 1) / block_size,
              (N + block_size - 1) / block_size,
              (N + block_size - 1) / block_size);

    BenchmarkTimer timer;

    // Warmup
    for (int w = 0; w < num_warmup; ++w) {
        hipLaunchKernelGGL(conv3d_kernel, grid, block, 0, 0,
                           d_input, d_filter, d_output, N, filter_size);
    }
    HIP_CHECK(hipDeviceSynchronize());

    // Timed iterations
    std::vector<double> times(1);
    for (int i = 0; i < 1; ++i) {
        timer.record_start();
        hipLaunchKernelGGL(conv3d_kernel, grid, block, 0, 0,
                           d_input, d_filter, d_output, N, filter_size);
        timer.record_stop();
        times[i] = static_cast<double>(timer.elapsed_ms());
    }

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

    double flops = 2.0 * (double)vol_elems * (double)filt_elems;
    double gflops = flops / (avg_ms * 1e-3) / 1e9;

    fprintf(stderr, "Performance: %.2f GFLOPS  (avg %.4f ms, min %.4f ms, max %.4f ms, stddev %.4f ms)\n",
            gflops, avg_ms, mn, mx, stddev);

    // JSON-lines output
    printf("{\"type\":\"kernel\",\"name\":\"conv3d_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"size\":%d,\"block_size\":%d,"
           "\"filter_size\":%d}}\n",
           avg_ms, N, block_size, filter_size);
    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"gflops\",\"value\":%.2f}]}\n",
           avg_ms, gflops);

    // Cleanup
    HIP_CHECK(hipFree(d_input));
    HIP_CHECK(hipFree(d_filter));
    HIP_CHECK(hipFree(d_output));
    free(h_input);
    free(h_filter);
    free(h_output);

    return 0;
}
