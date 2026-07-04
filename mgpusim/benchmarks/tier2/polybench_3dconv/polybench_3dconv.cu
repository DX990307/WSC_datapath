// polybench_3dconv.cu — PolyBench 3D Convolution benchmark (CUDA, self-contained)
//
// 3D convolution over an NxNxN volume with a small 3D filter (e.g., 3x3x3).
// Each output point is the weighted sum of its neighborhood.
// Derived from the PolyBench/GPU benchmark suite.
//
// Native CUDA implementation.
//
// Usage:
//   ./polybench_3dconv [--size N]
//
// Output (stdout): JSON-lines
// Output (stderr): device info, timing details

#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <cmath>
#include <cfloat>
#include <vector>
#include <cuda_runtime.h>

// ---------------------------------------------------------------------------
// Inlined error check macro
// ---------------------------------------------------------------------------

#define CUDA_CHECK(cmd)                                                         \
    do {                                                                       \
        cudaError_t _e = (cmd);                                                 \
        if (_e != cudaSuccess) {                                                \
            fprintf(stderr, "CUDA error %s at %s:%d\n",                        \
                    cudaGetErrorString(_e), __FILE__, __LINE__);                \
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
    cudaEvent_t start, stop;
    BenchmarkTimer() {
        CUDA_CHECK(cudaEventCreate(&start));
        CUDA_CHECK(cudaEventCreate(&stop));
    }
    ~BenchmarkTimer() {
        (void)cudaEventDestroy(start);
        (void)cudaEventDestroy(stop);
    }
    void record_start(cudaStream_t stream = 0) {
        CUDA_CHECK(cudaEventRecord(start, stream));
    }
    void record_stop(cudaStream_t stream = 0) {
        CUDA_CHECK(cudaEventRecord(stop, stream));
        CUDA_CHECK(cudaEventSynchronize(stop));
    }
    float elapsed_ms() const {
        float ms = 0.0f;
        CUDA_CHECK(cudaEventElapsedTime(&ms, start, stop));
        return ms;
    }
};

// ---------------------------------------------------------------------------
// 3D Convolution Kernel
// For each output point (i,j,k), compute weighted sum over the filter window.
// Filter is centered at (i,j,k) with radius = filter_size/2.
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
    int num_warmup = 0;

    size_t vol_elems   = (size_t)N * N * N;
    size_t vol_bytes   = vol_elems * sizeof(float);
    size_t filt_elems  = (size_t)filter_size * filter_size * filter_size;
    size_t filt_bytes  = filt_elems * sizeof(float);

    // Print device info
    int device_id = 0;
    CUDA_CHECK(cudaGetDevice(&device_id));
    cudaDeviceProp prop;
    CUDA_CHECK(cudaGetDeviceProperties(&prop, device_id));
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
    // Normalised filter weights
    float filt_sum = 0.0f;
    for (size_t i = 0; i < filt_elems; ++i) {
        h_filter[i] = (float)(rand() % 10 + 1);
        filt_sum += h_filter[i];
    }
    for (size_t i = 0; i < filt_elems; ++i)
        h_filter[i] /= filt_sum;

    // Device allocations
    float *d_input, *d_filter, *d_output;
    CUDA_CHECK(cudaMalloc(&d_input,  vol_bytes));
    CUDA_CHECK(cudaMalloc(&d_filter, filt_bytes));
    CUDA_CHECK(cudaMalloc(&d_output, vol_bytes));

    CUDA_CHECK(cudaMemcpy(d_input,  h_input,  vol_bytes,  cudaMemcpyHostToDevice));
    CUDA_CHECK(cudaMemcpy(d_filter, h_filter, filt_bytes, cudaMemcpyHostToDevice));

    dim3 block(block_size, block_size, block_size);
    dim3 grid((N + block_size - 1) / block_size,
              (N + block_size - 1) / block_size,
              (N + block_size - 1) / block_size);

    BenchmarkTimer timer;

    // Warmup
    for (int w = 0; w < num_warmup; ++w) {
        conv3d_kernel<<<grid, block>>>(d_input, d_filter, d_output, N, filter_size);
    }
    CUDA_CHECK(cudaDeviceSynchronize());

    // Timed iterations
    std::vector<double> times(1);
    for (int i = 0; i < 1; ++i) {
        timer.record_start();
        conv3d_kernel<<<grid, block>>>(d_input, d_filter, d_output, N, filter_size);
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

    // GFLOPS: each output voxel does filter_size^3 multiply-adds = 2*filter_size^3 FLOPs
    double flops = 2.0 * (double)vol_elems * (double)filt_elems;
    double gflops = flops / (avg_ms * 1e-3) / 1e9;

    fprintf(stderr, "Performance: %.2f GFLOPS  (avg %.4f ms, min %.4f ms, max %.4f ms, stddev %.4f ms)\n",
            gflops, avg_ms, mn, mx, stddev);

    // JSON-lines output
    printf("{\"type\":\"kernel\",\"name\":\"conv3d_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"size\":%d\"block_size\":%d,"
           "\"filter_size\":%d}}\n",
           avg_ms, N, block_size, filter_size);
    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"gflops\",\"value\":%.2f}]}\n",
           avg_ms, gflops);

    // Cleanup
    CUDA_CHECK(cudaFree(d_input));
    CUDA_CHECK(cudaFree(d_filter));
    CUDA_CHECK(cudaFree(d_output));
    free(h_input);
    free(h_filter);
    free(h_output);

    return 0;
}
