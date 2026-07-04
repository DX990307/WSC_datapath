// polybench_correlation.hip — PolyBench Correlation benchmark (HIP, self-contained)
//
// Compute correlation matrix from a data matrix (M samples x N features).
// Steps: mean, stddev, normalize, correlation (matmul-like).
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
// Kernel 1: Compute column means
// ---------------------------------------------------------------------------
__global__ void mean_kernel(const float* __restrict__ data,
                            float* __restrict__ mean,
                            int M, int N) {
    int j = blockIdx.x * blockDim.x + threadIdx.x;
    if (j >= N) return;

    float sum = 0.0f;
    for (int i = 0; i < M; ++i) {
        sum += data[i * N + j];
    }
    mean[j] = sum / (float)M;
}

// ---------------------------------------------------------------------------
// Kernel 2: Compute column standard deviations
// ---------------------------------------------------------------------------
__global__ void stddev_kernel(const float* __restrict__ data,
                              const float* __restrict__ mean,
                              float* __restrict__ stddev,
                              int M, int N) {
    int j = blockIdx.x * blockDim.x + threadIdx.x;
    if (j >= N) return;

    float m = mean[j];
    float sum = 0.0f;
    for (int i = 0; i < M; ++i) {
        float diff = data[i * N + j] - m;
        sum += diff * diff;
    }
    float s = sqrtf(sum / (float)M);
    stddev[j] = (s < 1e-12f) ? 1.0f : s;
}

// ---------------------------------------------------------------------------
// Kernel 3: Normalize data
// ---------------------------------------------------------------------------
__global__ void normalize_kernel(float* __restrict__ data,
                                 const float* __restrict__ mean,
                                 const float* __restrict__ stddev,
                                 int M, int N) {
    int idx = blockIdx.x * blockDim.x + threadIdx.x;
    if (idx >= M * N) return;

    int j = idx % N;
    float sqrt_m = sqrtf((float)M);
    data[idx] = (data[idx] - mean[j]) / (sqrt_m * stddev[j]);
}

// ---------------------------------------------------------------------------
// Kernel 4: Compute correlation matrix (tiled)
// ---------------------------------------------------------------------------
#define TILE_SIZE 16

__global__ void correlation_kernel(const float* __restrict__ data,
                                   float* __restrict__ corr,
                                   int M, int N) {
    __shared__ float sA[TILE_SIZE][TILE_SIZE];
    __shared__ float sB[TILE_SIZE][TILE_SIZE];

    int row = blockIdx.y * TILE_SIZE + threadIdx.y;
    int col = blockIdx.x * TILE_SIZE + threadIdx.x;

    float sum = 0.0f;
    int num_tiles = (M + TILE_SIZE - 1) / TILE_SIZE;

    for (int t = 0; t < num_tiles; ++t) {
        int k_a = t * TILE_SIZE + threadIdx.x;
        int k_b = t * TILE_SIZE + threadIdx.y;

        sA[threadIdx.y][threadIdx.x] = (row < N && k_a < M) ? data[k_a * N + row] : 0.0f;
        sB[threadIdx.y][threadIdx.x] = (k_b < M && col < N) ? data[k_b * N + col] : 0.0f;

        __syncthreads();

        for (int k = 0; k < TILE_SIZE; ++k) {
            sum += sA[threadIdx.y][k] * sB[k][threadIdx.x];
        }

        __syncthreads();
    }

    if (row < N && col < N) {
        if (row == col)
            corr[row * N + col] = 1.0f;
        else
            corr[row * N + col] = sum;
    }
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------
int main(int argc, char** argv) {
    setvbuf(stdout, NULL, _IONBF, 0);

    int N          = parseIntParam(argc, argv, "--size", 1024);
    int block_size = parseIntParam(argc, argv, "--block_size", 256);
    const char* precision = "float";

    const char* env_val;
    env_val = getenv("BENCH_PARAM_size");
    if (env_val) N = atoi(env_val);
    env_val = getenv("BENCH_PARAM_block_size");
    if (env_val) block_size = atoi(env_val);
    env_val = getenv("BENCH_PARAM_precision");
    if (env_val) precision = env_val;
    int num_warmup = 5;

    int M = N;

    size_t data_bytes = (size_t)M * N * sizeof(float);
    size_t vec_bytes  = (size_t)N * sizeof(float);
    size_t corr_bytes = (size_t)N * N * sizeof(float);

    // Print device info
    int device_id = 0;
    HIP_CHECK(hipGetDevice(&device_id));
    hipDeviceProp_t prop;
    HIP_CHECK(hipGetDeviceProperties(&prop, device_id));
    fprintf(stderr, "Device: %s (id=%d)\n", prop.name, device_id);
    fprintf(stderr, "Data matrix: %dx%d\n\n", M, N);

    // Host allocations
    float* h_data = (float*)malloc(data_bytes);

    srand(42);
    for (size_t i = 0; i < (size_t)M * N; ++i)
        h_data[i] = (float)(rand() % 1000) / 100.0f;

    // Device allocations
    float *d_data, *d_mean, *d_stddev, *d_corr;
    HIP_CHECK(hipMalloc(&d_data,   data_bytes));
    HIP_CHECK(hipMalloc(&d_mean,   vec_bytes));
    HIP_CHECK(hipMalloc(&d_stddev, vec_bytes));
    HIP_CHECK(hipMalloc(&d_corr,   corr_bytes));

    BenchmarkTimer timer;

    int threads1d  = block_size;
    int blocks_n   = (N + threads1d - 1) / threads1d;
    int blocks_mn  = (M * N + threads1d - 1) / threads1d;

    dim3 corr_block(TILE_SIZE, TILE_SIZE);
    dim3 corr_grid((N + TILE_SIZE - 1) / TILE_SIZE, (N + TILE_SIZE - 1) / TILE_SIZE);

    // Warmup
    for (int w = 0; w < num_warmup; ++w) {
        HIP_CHECK(hipMemcpy(d_data, h_data, data_bytes, hipMemcpyHostToDevice));
        hipLaunchKernelGGL(mean_kernel, dim3(blocks_n), dim3(threads1d), 0, 0,
                           d_data, d_mean, M, N);
        hipLaunchKernelGGL(stddev_kernel, dim3(blocks_n), dim3(threads1d), 0, 0,
                           d_data, d_mean, d_stddev, M, N);
        hipLaunchKernelGGL(normalize_kernel, dim3(blocks_mn), dim3(threads1d), 0, 0,
                           d_data, d_mean, d_stddev, M, N);
        hipLaunchKernelGGL(correlation_kernel, corr_grid, corr_block, 0, 0,
                           d_data, d_corr, M, N);
    }
    HIP_CHECK(hipDeviceSynchronize());

    // Timed iterations
    std::vector<double> t_mean(1), t_stddev(1), t_norm(1), t_corr(1);

    for (int it = 0; it < 1; ++it) {
        HIP_CHECK(hipMemcpy(d_data, h_data, data_bytes, hipMemcpyHostToDevice));

        timer.record_start();
        hipLaunchKernelGGL(mean_kernel, dim3(blocks_n), dim3(threads1d), 0, 0,
                           d_data, d_mean, M, N);
        timer.record_stop();
        t_mean[it] = (double)timer.elapsed_ms();

        timer.record_start();
        hipLaunchKernelGGL(stddev_kernel, dim3(blocks_n), dim3(threads1d), 0, 0,
                           d_data, d_mean, d_stddev, M, N);
        timer.record_stop();
        t_stddev[it] = (double)timer.elapsed_ms();

        timer.record_start();
        hipLaunchKernelGGL(normalize_kernel, dim3(blocks_mn), dim3(threads1d), 0, 0,
                           d_data, d_mean, d_stddev, M, N);
        timer.record_stop();
        t_norm[it] = (double)timer.elapsed_ms();

        timer.record_start();
        hipLaunchKernelGGL(correlation_kernel, corr_grid, corr_block, 0, 0,
                           d_data, d_corr, M, N);
        timer.record_stop();
        t_corr[it] = (double)timer.elapsed_ms();
    }

    auto avg = [](const std::vector<double>& v) {
        double s = 0; for (auto x : v) s += x; return s / v.size();
    };

    double avg_mean   = avg(t_mean);
    double avg_stddev = avg(t_stddev);
    double avg_norm   = avg(t_norm);
    double avg_corr   = avg(t_corr);
    double total_ms   = avg_mean + avg_stddev + avg_norm + avg_corr;

    fprintf(stderr, "Kernel timings (avg ms): mean=%.4f  stddev=%.4f  normalize=%.4f  correlation=%.4f\n",
            avg_mean, avg_stddev, avg_norm, avg_corr);
    fprintf(stderr, "Total: %.4f ms\n", total_ms);

    // JSON-lines output
    printf("{\"type\":\"kernel\",\"name\":\"mean_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"size\":%d,\"block_size\":%d,"
           "\"precision\":\"%s\"}}\n",
           avg_mean, N, block_size, precision);
    printf("{\"type\":\"kernel\",\"name\":\"stddev_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"size\":%d,\"block_size\":%d,"
           "\"precision\":\"%s\"}}\n",
           avg_stddev, N, block_size, precision);
    printf("{\"type\":\"kernel\",\"name\":\"normalize_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"size\":%d,\"block_size\":%d,"
           "\"precision\":\"%s\"}}\n",
           avg_norm, N, block_size, precision);
    printf("{\"type\":\"kernel\",\"name\":\"correlation_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"size\":%d,\"block_size\":%d,"
           "\"precision\":\"%s\"}}\n",
           avg_corr, N, block_size, precision);
    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[]}\n",
           total_ms);

    // Cleanup
    HIP_CHECK(hipFree(d_data));
    HIP_CHECK(hipFree(d_mean));
    HIP_CHECK(hipFree(d_stddev));
    HIP_CHECK(hipFree(d_corr));
    free(h_data);

    return 0;
}
