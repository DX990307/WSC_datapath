// polybench_2mm.cu — PolyBench 2MM benchmark: Two Matrix Multiplications (CUDA)
//
// Computes D = alpha * A * B + beta * D, then E = alpha * C * D + beta * E
// for NxN square matrices. Tiled shared-memory implementation.
// Derived from the PolyBench/GPU benchmark suite.
//
// Native CUDA implementation.
//
// Usage:
//   ./polybench_2mm [--size N]

#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <cmath>
#include <cfloat>
#include <vector>
#include <cuda_runtime.h>

// ---------------------------------------------------------------------------
// Error-checking macro
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

static float parseFloatParam(int argc, char** argv, const char* name, float defaultVal) {
    for (int i = 1; i < argc - 1; ++i) {
        if (strcmp(argv[i], name) == 0) {
            float v = (float)atof(argv[i + 1]);
            return v;
        }
    }
    return defaultVal;
}

// ---------------------------------------------------------------------------
// Tiled GEMM kernel: Out = alpha * P * Q + beta * Out  (NxN matrices)
// Uses 16x16 shared-memory tiling for good cache reuse.
// ---------------------------------------------------------------------------

#define TILE_SIZE 16

__global__ void mm_kernel(const float* __restrict__ P,
                          const float* __restrict__ Q,
                          float* __restrict__ Out,
                          int N, float alpha, float beta) {
    __shared__ float sP[TILE_SIZE][TILE_SIZE];
    __shared__ float sQ[TILE_SIZE][TILE_SIZE];

    int row = blockIdx.y * TILE_SIZE + threadIdx.y;
    int col = blockIdx.x * TILE_SIZE + threadIdx.x;

    float sum = 0.0f;
    int num_tiles = (N + TILE_SIZE - 1) / TILE_SIZE;

    for (int t = 0; t < num_tiles; ++t) {
        int pCol = t * TILE_SIZE + threadIdx.x;
        int qRow = t * TILE_SIZE + threadIdx.y;

        sP[threadIdx.y][threadIdx.x] = (row < N && pCol < N) ? P[row * N + pCol] : 0.0f;
        sQ[threadIdx.y][threadIdx.x] = (qRow < N && col < N) ? Q[qRow * N + col] : 0.0f;

        __syncthreads();

        for (int k = 0; k < TILE_SIZE; ++k) {
            sum += sP[threadIdx.y][k] * sQ[k][threadIdx.x];
        }

        __syncthreads();
    }

    if (row < N && col < N) {
        Out[row * N + col] = alpha * sum + beta * Out[row * N + col];
    }
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------

int main(int argc, char** argv) {
    setvbuf(stdout, NULL, _IONBF, 0);

    // Defaults
    int N          = 1024;
    int block_size = 256;
    float alpha    = 1.5f;
    float beta     = 1.2f;
    const char* precision = "float";

    // Command-line parsing
    N          = parseIntParam(argc, argv, "--size", N);
    alpha      = parseFloatParam(argc, argv, "--alpha", alpha);
    beta       = parseFloatParam(argc, argv, "--beta", beta);

    // Environment variable overrides (BENCH_PARAM_*)
    const char* env_val;
    env_val = getenv("BENCH_PARAM_size");
    if (env_val) N = atoi(env_val);
    env_val = getenv("BENCH_PARAM_block_size");
    if (env_val) block_size = atoi(env_val);
    env_val = getenv("BENCH_PARAM_alpha");
    if (env_val) alpha = (float)atof(env_val);
    env_val = getenv("BENCH_PARAM_beta");
    if (env_val) beta = (float)atof(env_val);
    env_val = getenv("BENCH_PARAM_precision");
    if (env_val) precision = env_val;
    int num_warmup = 0;

    size_t bytes = (size_t)N * N * sizeof(float);

    // Print device info
    int device_id = 0;
    CUDA_CHECK(cudaGetDevice(&device_id));
    cudaDeviceProp prop;
    CUDA_CHECK(cudaGetDeviceProperties(&prop, device_id));
    fprintf(stderr, "Device: %s (id=%d)\n", prop.name, device_id);
    fprintf(stderr, "2MM: D=alpha*A*B+beta*D, E=alpha*C*D+beta*E  |  N=%d  |  "
            "alpha=%.1f  beta=%.1f warmup + %d timed\n\n",
            N, alpha, beta, num_warmup);

    // Host allocations: A, B, C, D, E — all NxN
    float* h_A = (float*)malloc(bytes);
    float* h_B = (float*)malloc(bytes);
    float* h_C = (float*)malloc(bytes);
    float* h_D = (float*)malloc(bytes);
    float* h_E = (float*)malloc(bytes);

    // PolyBench-style initialization
    srand(42);
    for (int i = 0; i < N * N; ++i) {
        h_A[i] = (float)(rand() % 100) / 10.0f;
        h_B[i] = (float)(rand() % 100) / 10.0f;
        h_C[i] = (float)(rand() % 100) / 10.0f;
        h_D[i] = (float)(rand() % 100) / 10.0f;
        h_E[i] = (float)(rand() % 100) / 10.0f;
    }

    // Device allocations
    float *d_A, *d_B, *d_C, *d_D, *d_E;
    CUDA_CHECK(cudaMalloc(&d_A, bytes));
    CUDA_CHECK(cudaMalloc(&d_B, bytes));
    CUDA_CHECK(cudaMalloc(&d_C, bytes));
    CUDA_CHECK(cudaMalloc(&d_D, bytes));
    CUDA_CHECK(cudaMalloc(&d_E, bytes));

    CUDA_CHECK(cudaMemcpy(d_A, h_A, bytes, cudaMemcpyHostToDevice));
    CUDA_CHECK(cudaMemcpy(d_B, h_B, bytes, cudaMemcpyHostToDevice));
    CUDA_CHECK(cudaMemcpy(d_C, h_C, bytes, cudaMemcpyHostToDevice));

    dim3 block(TILE_SIZE, TILE_SIZE);
    dim3 grid((N + TILE_SIZE - 1) / TILE_SIZE, (N + TILE_SIZE - 1) / TILE_SIZE);

    // Lambda: run 2MM (D = alpha*A*B + beta*D, then E = alpha*C*D + beta*E)
    auto run_2mm = [&]() {
        // Reset D and E for each iteration
        CUDA_CHECK(cudaMemcpy(d_D, h_D, bytes, cudaMemcpyHostToDevice));
        CUDA_CHECK(cudaMemcpy(d_E, h_E, bytes, cudaMemcpyHostToDevice));

        // First multiply: D = alpha * A * B + beta * D
        mm_kernel<<<grid, block>>>(d_A, d_B, d_D, N, alpha, beta);

        // Second multiply: E = alpha * C * D + beta * E
        mm_kernel<<<grid, block>>>(d_C, d_D, d_E, N, alpha, beta);
    };

    // Warmup
    for (int w = 0; w < num_warmup; ++w) {
        run_2mm();
        CUDA_CHECK(cudaDeviceSynchronize());
    }

    // Timed iterations
    cudaEvent_t evStart, evStop;
    CUDA_CHECK(cudaEventCreate(&evStart));
    CUDA_CHECK(cudaEventCreate(&evStop));

    std::vector<double> times(1);
    for (int i = 0; i < 1; ++i) {
        CUDA_CHECK(cudaEventRecord(evStart, 0));
        run_2mm();
        CUDA_CHECK(cudaEventRecord(evStop, 0));
        CUDA_CHECK(cudaEventSynchronize(evStop));

        float ms = 0.0f;
        CUDA_CHECK(cudaEventElapsedTime(&ms, evStart, evStop));
        times[i] = (double)ms;
    }

    CUDA_CHECK(cudaEventDestroy(evStart));
    CUDA_CHECK(cudaEventDestroy(evStop));

    // Compute statistics
    double sum_ms = 0.0, mn = DBL_MAX, mx = 0.0;
    for (int i = 0; i < 1; ++i) {
        sum_ms += times[i];
        if (times[i] < mn) mn = times[i];
        if (times[i] > mx) mx = times[i];
    }
    double avg_ms = sum_ms / 1;

    // GFLOPS: two matrix multiplies = 2 * (2*N^3) = 4*N^3 FLOPs
    double gflops = 4.0 * (double)N * (double)N * (double)N
                    / (avg_ms * 1e-3) / 1e9;

    fprintf(stderr, "Performance: %.2f GFLOPS  (avg %.4f ms, min %.4f ms, max %.4f ms)\n",
            gflops, avg_ms, mn, mx);

    // JSON-lines output
    printf("{\"type\":\"kernel\",\"name\":\"mm_kernel_1\",\"time_ms\":%.6f,"
           "\"params\":{\"size\":%d\"block_size\":%d,"
           "\"alpha\":%.2f,\"beta\":%.2f,\"precision\":\"%s\"}}\n",
           avg_ms * 0.5, N, block_size, alpha, beta, precision);

    printf("{\"type\":\"kernel\",\"name\":\"mm_kernel_2\",\"time_ms\":%.6f,"
           "\"params\":{\"size\":%d\"block_size\":%d,"
           "\"alpha\":%.2f,\"beta\":%.2f,\"precision\":\"%s\"}}\n",
           avg_ms * 0.5, N, block_size, alpha, beta, precision);

    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"gflops\",\"value\":%.2f}]}\n",
           avg_ms, gflops);

    // Cleanup
    CUDA_CHECK(cudaFree(d_A));
    CUDA_CHECK(cudaFree(d_B));
    CUDA_CHECK(cudaFree(d_C));
    CUDA_CHECK(cudaFree(d_D));
    CUDA_CHECK(cudaFree(d_E));
    free(h_A);
    free(h_B);
    free(h_C);
    free(h_D);
    free(h_E);

    return 0;
}
