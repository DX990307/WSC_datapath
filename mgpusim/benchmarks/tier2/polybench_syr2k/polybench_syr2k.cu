// polybench_syr2k.cu — PolyBench SYR2K benchmark (CUDA, self-contained)
//
// Symmetric rank-2k update: C = alpha*A*B^T + alpha*B*A^T + beta*C
// A and B are NxM matrices, C is NxN symmetric matrix.
// Derived from the PolyBench/GPU benchmark suite.
//
// Default: N=1024, M=1024, alpha=1.5, beta=1.2
// Measures peak compute throughput in GFLOPS.
//
// Native CUDA implementation with tiled shared-memory kernel.
//
// Usage:
//   ./polybench_syr2k [--size N]
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
// Inlined error-checking macro
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

static float parseFloatParam(int argc, char** argv, const char* name, float defaultVal) {
    for (int i = 1; i < argc - 1; ++i) {
        if (strcmp(argv[i], name) == 0) {
            float v = (float)atof(argv[i + 1]);
            return v;
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
// SYR2K kernel — tiled shared-memory implementation
// C = alpha*A*B^T + alpha*B*A^T + beta*C
// A is [N x M], B is [N x M], C is [N x N]
//
// C[row][col] = alpha * sum_k(A[row][k]*B[col][k])
//             + alpha * sum_k(B[row][k]*A[col][k])
//             + beta  * C[row][col]
// ---------------------------------------------------------------------------
#define TILE_SIZE 16

__global__ void syr2k_kernel(const float* __restrict__ A,
                              const float* __restrict__ B,
                              float* __restrict__ C,
                              int N, int M,
                              float alpha, float beta) {
    __shared__ float sA_row[TILE_SIZE][TILE_SIZE];
    __shared__ float sB_row[TILE_SIZE][TILE_SIZE];
    __shared__ float sA_col[TILE_SIZE][TILE_SIZE];
    __shared__ float sB_col[TILE_SIZE][TILE_SIZE];

    int row = blockIdx.y * TILE_SIZE + threadIdx.y;
    int col = blockIdx.x * TILE_SIZE + threadIdx.x;

    float sum = 0.0f;
    int num_tiles = (M + TILE_SIZE - 1) / TILE_SIZE;

    for (int t = 0; t < num_tiles; ++t) {
        int k = t * TILE_SIZE + threadIdx.x;

        // Load A and B rows for the row-block (rows bidy*TILE .. bidy*TILE+TILE-1)
        sA_row[threadIdx.y][threadIdx.x] = (row < N && k < M) ? A[row * M + k] : 0.0f;
        sB_row[threadIdx.y][threadIdx.x] = (row < N && k < M) ? B[row * M + k] : 0.0f;

        // Load A and B rows for the col-block (rows bidx*TILE .. bidx*TILE+TILE-1)
        int col_row = blockIdx.x * TILE_SIZE + threadIdx.y;
        int k2 = t * TILE_SIZE + threadIdx.x;
        sA_col[threadIdx.y][threadIdx.x] = (col_row < N && k2 < M) ? A[col_row * M + k2] : 0.0f;
        sB_col[threadIdx.y][threadIdx.x] = (col_row < N && k2 < M) ? B[col_row * M + k2] : 0.0f;

        __syncthreads();

        for (int kk = 0; kk < TILE_SIZE; ++kk) {
            // A*B^T contribution: A[row][k] * B[col][k]
            // B*A^T contribution: B[row][k] * A[col][k]
            sum += sA_row[threadIdx.y][kk] * sB_col[threadIdx.x][kk]
                 + sB_row[threadIdx.y][kk] * sA_col[threadIdx.x][kk];
        }

        __syncthreads();
    }

    if (row < N && col < N) {
        C[row * N + col] = alpha * sum + beta * C[row * N + col];
    }
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------
int main(int argc, char** argv) {
    setvbuf(stdout, NULL, _IONBF, 0);

    int N     = parseIntParam(argc, argv, "--size", 1024);
    int M     = parseIntParam(argc, argv, "--inner_size", 1024);
    int block_size = parseIntParam(argc, argv, "--block_size", 256);
    float alpha = parseFloatParam(argc, argv, "--alpha", 1.5f);
    float beta  = parseFloatParam(argc, argv, "--beta", 1.2f);

    // Environment variable overrides
    const char* env_val;
    env_val = getenv("BENCH_PARAM_matrix_size");
    if (env_val) N = atoi(env_val);
    env_val = getenv("BENCH_PARAM_inner_size");
    if (env_val) M = atoi(env_val);
    env_val = getenv("BENCH_PARAM_block_size");
    if (env_val) block_size = atoi(env_val);
    env_val = getenv("BENCH_PARAM_alpha");
    if (env_val) alpha = (float)atof(env_val);
    env_val = getenv("BENCH_PARAM_beta");
    if (env_val) beta = (float)atof(env_val);
    int num_warmup = 0;

    size_t bytes_AB = (size_t)N * M * sizeof(float);
    size_t bytes_C  = (size_t)N * N * sizeof(float);

    // Print device info
    int device_id = 0;
    CUDA_CHECK(cudaGetDevice(&device_id));
    cudaDeviceProp prop;
    CUDA_CHECK(cudaGetDeviceProperties(&prop, device_id));
    fprintf(stderr, "Device: %s (id=%d)\n", prop.name, device_id);
    fprintf(stderr, "SYR2K: N=%d, M=%d  |  alpha=%.1f  beta=%.1f\n\n",
            N, M, alpha, beta);

    // Host allocations
    float* h_A = (float*)malloc(bytes_AB);
    float* h_B = (float*)malloc(bytes_AB);
    float* h_C = (float*)malloc(bytes_C);

    // PolyBench-style initialization
    srand(42);
    for (int i = 0; i < N * M; ++i) {
        h_A[i] = (float)(rand() % 100) / 10.0f;
        h_B[i] = (float)(rand() % 100) / 10.0f;
    }
    for (int i = 0; i < N * N; ++i) {
        h_C[i] = (float)(rand() % 100) / 10.0f;
    }

    // Device allocations
    float *d_A, *d_B, *d_C;
    CUDA_CHECK(cudaMalloc(&d_A, bytes_AB));
    CUDA_CHECK(cudaMalloc(&d_B, bytes_AB));
    CUDA_CHECK(cudaMalloc(&d_C, bytes_C));

    CUDA_CHECK(cudaMemcpy(d_A, h_A, bytes_AB, cudaMemcpyHostToDevice));
    CUDA_CHECK(cudaMemcpy(d_B, h_B, bytes_AB, cudaMemcpyHostToDevice));
    CUDA_CHECK(cudaMemcpy(d_C, h_C, bytes_C, cudaMemcpyHostToDevice));

    dim3 block(TILE_SIZE, TILE_SIZE);
    dim3 grid((N + TILE_SIZE - 1) / TILE_SIZE, (N + TILE_SIZE - 1) / TILE_SIZE);

    BenchmarkTimer timer;

    // Warmup (not measured)
    for (int w = 0; w < num_warmup; ++w) {
        syr2k_kernel<<<grid, block>>>(d_A, d_B, d_C, N, M, alpha, beta);
    }
    CUDA_CHECK(cudaDeviceSynchronize());

    // Re-upload C for consistent timed runs
    CUDA_CHECK(cudaMemcpy(d_C, h_C, bytes_C, cudaMemcpyHostToDevice));

    // Timed iterations (5 by default)
    std::vector<double> times(1);
    for (int i = 0; i < 1; ++i) {
        CUDA_CHECK(cudaMemcpy(d_C, h_C, bytes_C, cudaMemcpyHostToDevice));
        timer.record_start();
        syr2k_kernel<<<grid, block>>>(d_A, d_B, d_C, N, M, alpha, beta);
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

    // GFLOPS: 4*N*N*M (two rank-k products, each 2*N*N*M FLOPs)
    double gflops = 4.0 * (double)N * (double)N * (double)M
                    / (avg_ms * 1e-3) / 1e9;

    fprintf(stderr, "Performance: %.2f GFLOPS  (avg %.4f ms, min %.4f ms, max %.4f ms, stddev %.4f ms)\n",
            gflops, avg_ms, mn, mx, stddev);

    // JSON-lines output
    printf("{\"type\":\"kernel\",\"name\":\"syr2k_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"matrix_size\":%d,\"inner_size\":%d"
           "\"block_size\":%d,\"alpha\":%.2f,\"beta\":%.2f}}\n",
           avg_ms, N, M, block_size, alpha, beta);
    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"gflops\",\"value\":%.2f}]}\n",
           avg_ms, gflops);

    // Cleanup
    CUDA_CHECK(cudaFree(d_A));
    CUDA_CHECK(cudaFree(d_B));
    CUDA_CHECK(cudaFree(d_C));
    free(h_A);
    free(h_B);
    free(h_C);

    return 0;
}
