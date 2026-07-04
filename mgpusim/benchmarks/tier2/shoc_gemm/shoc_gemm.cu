// shoc_gemm.cu — SHOC GEMM benchmark (CUDA, self-contained)
//
// Dense matrix multiply: C = alpha*A*B + beta*C for NxN square matrices of floats.
// Measures peak compute throughput in GFLOPS.
//
// Parameters are read from BENCH_PARAM_* environment variables,
// with command-line --flags as fallback.
//
// Output (stdout): JSON-lines protocol
// Output (stderr): Human-readable diagnostics

#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <cmath>
#include <cfloat>
#include <vector>
#include <cuda_runtime.h>

// ---------------------------------------------------------------------------
// Inlined bench_common_cuda.h utilities
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

static const char* parseStrParam(int argc, char** argv, const char* name,
                                 const char* defaultVal) {
    for (int i = 1; i < argc - 1; ++i) {
        if (strcmp(argv[i], name) == 0) {
            return argv[i + 1];
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
// GEMM kernel
// ---------------------------------------------------------------------------
#define TILE_SIZE 16

__global__ void gemm_kernel(const float* __restrict__ A,
                             const float* __restrict__ B,
                             float* __restrict__ C,
                             int M, int N, int K,
                             float alpha, float beta) {
    __shared__ float sA[TILE_SIZE][TILE_SIZE];
    __shared__ float sB[TILE_SIZE][TILE_SIZE];

    int row = blockIdx.y * TILE_SIZE + threadIdx.y;
    int col = blockIdx.x * TILE_SIZE + threadIdx.x;

    float sum = 0.0f;
    int num_tiles = (K + TILE_SIZE - 1) / TILE_SIZE;

    for (int t = 0; t < num_tiles; ++t) {
        int aCol = t * TILE_SIZE + threadIdx.x;
        int bRow = t * TILE_SIZE + threadIdx.y;

        sA[threadIdx.y][threadIdx.x] = (row < M && aCol < K) ? A[row * K + aCol] : 0.0f;
        sB[threadIdx.y][threadIdx.x] = (bRow < K && col < N) ? B[bRow * N + col] : 0.0f;

        __syncthreads();

        for (int k = 0; k < TILE_SIZE; ++k) {
            sum += sA[threadIdx.y][k] * sB[k][threadIdx.x];
        }

        __syncthreads();
    }

    if (row < M && col < N) {
        C[row * N + col] = alpha * sum + beta * C[row * N + col];
    }
}

// ---------------------------------------------------------------------------
// CPU reference
// ---------------------------------------------------------------------------
static void gemm_cpu(const float* A, const float* B, float* C,
                     int M, int N, int K, float alpha, float beta) {
    for (int i = 0; i < M; ++i) {
        for (int j = 0; j < N; ++j) {
            float s = 0.0f;
            for (int k = 0; k < K; ++k) {
                s += A[i * K + k] * B[k * N + j];
            }
            C[i * N + j] = alpha * s + beta * C[i * N + j];
        }
    }
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------
int main(int argc, char** argv) {
    setvbuf(stdout, NULL, _IONBF, 0);

    // Defaults
    int size       = 512;
    int block_size = 16;
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
    int num_warmup = 0;

    int M = size, N = size, K = size;
    float alpha = 1.0f, beta = 0.0f;

    size_t bytesA = (size_t)M * K * sizeof(float);
    size_t bytesB = (size_t)K * N * sizeof(float);
    size_t bytesC = (size_t)M * N * sizeof(float);

    // Print device info
    int device_id = 0;
    CUDA_CHECK(cudaGetDevice(&device_id));
    cudaDeviceProp prop;
    CUDA_CHECK(cudaGetDeviceProperties(&prop, device_id));
    fprintf(stderr, "Device: %s (id=%d)\n", prop.name, device_id);
    fprintf(stderr, "Matrix size: %d×%d\n\n", size, size);

    // Host allocations
    float* h_A   = (float*)malloc(bytesA);
    float* h_B   = (float*)malloc(bytesB);
    float* h_C   = (float*)malloc(bytesC);
    float* h_ref = (float*)malloc(bytesC);

    for (int i = 0; i < M * K; ++i)
        h_A[i] = (float)(i % 1000) * 0.001f;
    for (int i = 0; i < K * N; ++i)
        h_B[i] = (float)((i + 37) % 1000) * 0.001f;
    for (int i = 0; i < M * N; ++i) {
        h_C[i]   = 0.0f;
        h_ref[i] = 0.0f;
    }

    // Device allocations
    float *d_A, *d_B, *d_C;
    CUDA_CHECK(cudaMalloc(&d_A, bytesA));
    CUDA_CHECK(cudaMalloc(&d_B, bytesB));
    CUDA_CHECK(cudaMalloc(&d_C, bytesC));

    CUDA_CHECK(cudaMemcpy(d_A, h_A, bytesA, cudaMemcpyHostToDevice));
    CUDA_CHECK(cudaMemcpy(d_B, h_B, bytesB, cudaMemcpyHostToDevice));
    CUDA_CHECK(cudaMemcpy(d_C, h_C, bytesC, cudaMemcpyHostToDevice));

    dim3 block(TILE_SIZE, TILE_SIZE);
    dim3 grid((N + TILE_SIZE - 1) / TILE_SIZE, (M + TILE_SIZE - 1) / TILE_SIZE);

    // Warmup
    for (int w = 0; w < num_warmup; ++w) {
        gemm_kernel<<<grid, block, 0, 0>>>(d_A, d_B, d_C, M, N, K, alpha, beta);
        CUDA_CHECK(cudaDeviceSynchronize());
    }

    // Timed iterations
    BenchmarkTimer timer;
    std::vector<double> times(1);
    for (int i = 0; i < 1; ++i) {
        timer.record_start();
        gemm_kernel<<<grid, block, 0, 0>>>(d_A, d_B, d_C, M, N, K, alpha, beta);
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

    // GFLOPS: 2 * M * N * K
    double gflops = 2.0 * (double)M * (double)N * (double)K
                    / (avg_ms * 1e-3) / 1e9;

    // JSON-lines kernel event
    printf("{\"type\":\"kernel\",\"name\":\"gemm_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"size\":%d,\"block_size\":%d"
           "\"precision\":\"%s\"}}\n",
           avg_ms, size, block_size, precision);

    // JSON-lines summary event
    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"gflops\",\"value\":%.2f}]}\n",
           total_ms, gflops);

    fprintf(stderr, "Performance: %.2f GFLOPS  (avg %.4f ms)\n", gflops, avg_ms);

    // Verify correctness (only for small sizes)
    if (size <= 512) {
        CUDA_CHECK(cudaMemcpy(h_C, d_C, bytesC, cudaMemcpyDeviceToHost));
        gemm_cpu(h_A, h_B, h_ref, M, N, K, alpha, beta);

        int errors = 0;
        for (int i = 0; i < M * N; ++i) {
            float diff = fabsf(h_C[i] - h_ref[i]);
            if (diff > 1e-3f * fabsf(h_ref[i]) + 1e-5f) {
                if (errors < 10) {
                    fprintf(stderr, "Mismatch at %d: got %f, expected %f\n",
                            i, h_C[i], h_ref[i]);
                }
                errors++;
            }
        }
        if (errors > 0) {
            fprintf(stderr, "FAIL: %d errors\n", errors);
        } else {
            fprintf(stderr, "PASS\n");
        }
    } else {
        fprintf(stderr, "Skipping verification for large size\n");
    }

    CUDA_CHECK(cudaFree(d_A));
    CUDA_CHECK(cudaFree(d_B));
    CUDA_CHECK(cudaFree(d_C));
    free(h_A);
    free(h_B);
    free(h_C);
    free(h_ref);

    return 0;
}
