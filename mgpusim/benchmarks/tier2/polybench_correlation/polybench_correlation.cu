// polybench_correlation.cu — PolyBench Correlation benchmark (CUDA, self-contained)
//
// Compute correlation matrix from a data matrix (M samples x N features).
// Steps: mean, stddev, normalize, correlation (matmul-like).
// Derived from the PolyBench/GPU benchmark suite.
//
// Default: M = N = 1024
//
// Native CUDA implementation.
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
// Kernel 1: Compute column means
// mean[j] = sum_i(data[i][j]) / M
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
// stddev[j] = sqrt(sum_i((data[i][j] - mean[j])^2) / M)
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
    // Prevent division by zero
    stddev[j] = (s < 1e-12f) ? 1.0f : s;
}

// ---------------------------------------------------------------------------
// Kernel 3: Normalize data
// data[i][j] = (data[i][j] - mean[j]) / (sqrt(M) * stddev[j])
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
// Kernel 4: Compute correlation matrix
// corr[i][j] = sum_k(data[k][i] * data[k][j]) for i,j in [0, N)
// The diagonal is set to 1.0.
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

        // data is MxN, we want data^T * data => (NxM) * (MxN) = NxN
        // data^T[row][k] = data[k][row]
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
    int num_warmup = 0;

    int M = N; // square data matrix

    size_t data_bytes = (size_t)M * N * sizeof(float);
    size_t vec_bytes  = (size_t)N * sizeof(float);
    size_t corr_bytes = (size_t)N * N * sizeof(float);

    // Print device info
    int device_id = 0;
    CUDA_CHECK(cudaGetDevice(&device_id));
    cudaDeviceProp prop;
    CUDA_CHECK(cudaGetDeviceProperties(&prop, device_id));
    fprintf(stderr, "Device: %s (id=%d)\n", prop.name, device_id);
    fprintf(stderr, "Data matrix: %dx%d\n\n", M, N);

    // Host allocations
    float* h_data = (float*)malloc(data_bytes);

    // Synthetic data init
    srand(42);
    for (size_t i = 0; i < (size_t)M * N; ++i)
        h_data[i] = (float)(rand() % 1000) / 100.0f;

    // Device allocations
    float *d_data, *d_mean, *d_stddev, *d_corr;
    CUDA_CHECK(cudaMalloc(&d_data,   data_bytes));
    CUDA_CHECK(cudaMalloc(&d_mean,   vec_bytes));
    CUDA_CHECK(cudaMalloc(&d_stddev, vec_bytes));
    CUDA_CHECK(cudaMalloc(&d_corr,   corr_bytes));

    BenchmarkTimer timer;

    // Grid/block configs
    int threads1d  = block_size;
    int blocks_n   = (N + threads1d - 1) / threads1d;
    int blocks_mn  = (M * N + threads1d - 1) / threads1d;

    dim3 corr_block(TILE_SIZE, TILE_SIZE);
    dim3 corr_grid((N + TILE_SIZE - 1) / TILE_SIZE, (N + TILE_SIZE - 1) / TILE_SIZE);

    // Warmup
    for (int w = 0; w < num_warmup; ++w) {
        CUDA_CHECK(cudaMemcpy(d_data, h_data, data_bytes, cudaMemcpyHostToDevice));
        mean_kernel<<<blocks_n, threads1d>>>(d_data, d_mean, M, N);
        stddev_kernel<<<blocks_n, threads1d>>>(d_data, d_mean, d_stddev, M, N);
        normalize_kernel<<<blocks_mn, threads1d>>>(d_data, d_mean, d_stddev, M, N);
        correlation_kernel<<<corr_grid, corr_block>>>(d_data, d_corr, M, N);
    }
    CUDA_CHECK(cudaDeviceSynchronize());

    // Timed iterations — measure each kernel separately
    std::vector<double> t_mean(1), t_stddev(1), t_norm(1), t_corr(1);

    for (int it = 0; it < 1; ++it) {
        // Re-upload original data (normalize modifies in-place)
        CUDA_CHECK(cudaMemcpy(d_data, h_data, data_bytes, cudaMemcpyHostToDevice));

        timer.record_start();
        mean_kernel<<<blocks_n, threads1d>>>(d_data, d_mean, M, N);
        timer.record_stop();
        t_mean[it] = (double)timer.elapsed_ms();

        timer.record_start();
        stddev_kernel<<<blocks_n, threads1d>>>(d_data, d_mean, d_stddev, M, N);
        timer.record_stop();
        t_stddev[it] = (double)timer.elapsed_ms();

        timer.record_start();
        normalize_kernel<<<blocks_mn, threads1d>>>(d_data, d_mean, d_stddev, M, N);
        timer.record_stop();
        t_norm[it] = (double)timer.elapsed_ms();

        timer.record_start();
        correlation_kernel<<<corr_grid, corr_block>>>(d_data, d_corr, M, N);
        timer.record_stop();
        t_corr[it] = (double)timer.elapsed_ms();
    }

    // Compute averages
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

    // JSON-lines output — one line per kernel
    printf("{\"type\":\"kernel\",\"name\":\"mean_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"size\":%d\"block_size\":%d,"
           "\"precision\":\"%s\"}}\n",
           avg_mean, N, block_size, precision);
    printf("{\"type\":\"kernel\",\"name\":\"stddev_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"size\":%d\"block_size\":%d,"
           "\"precision\":\"%s\"}}\n",
           avg_stddev, N, block_size, precision);
    printf("{\"type\":\"kernel\",\"name\":\"normalize_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"size\":%d\"block_size\":%d,"
           "\"precision\":\"%s\"}}\n",
           avg_norm, N, block_size, precision);
    printf("{\"type\":\"kernel\",\"name\":\"correlation_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"size\":%d\"block_size\":%d,"
           "\"precision\":\"%s\"}}\n",
           avg_corr, N, block_size, precision);
    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[]}\n",
           total_ms);

    // Cleanup
    CUDA_CHECK(cudaFree(d_data));
    CUDA_CHECK(cudaFree(d_mean));
    CUDA_CHECK(cudaFree(d_stddev));
    CUDA_CHECK(cudaFree(d_corr));
    free(h_data);

    return 0;
}
