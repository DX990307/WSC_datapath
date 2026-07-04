// polybench_2mm.hip — PolyBench 2MM benchmark: Two Matrix Multiplications (HIP)
//
// Computes D = alpha * A * B + beta * D, then E = alpha * C * D + beta * E
// for NxN square matrices. Tiled shared-memory implementation.
//
// Compiles with hipcc (AMD ROCm) or nvcc via HIP compatibility layer (CUDA).

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

    int N          = 1024;
    int block_size = 256;
    float alpha    = 1.5f;
    float beta     = 1.2f;
    const char* precision = "float";

    N          = parseIntParam(argc, argv, "--size", N);
    alpha      = parseFloatParam(argc, argv, "--alpha", alpha);
    beta       = parseFloatParam(argc, argv, "--beta", beta);

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
    int num_warmup = 5;

    size_t bytes = (size_t)N * N * sizeof(float);

    // Print device info
    int device_id = 0;
    HIP_CHECK(hipGetDevice(&device_id));
    hipDeviceProp_t prop;
    HIP_CHECK(hipGetDeviceProperties(&prop, device_id));
    fprintf(stderr, "Device: %s (id=%d)\n", prop.name, device_id);
    fprintf(stderr, "2MM: D=alpha*A*B+beta*D, E=alpha*C*D+beta*E  |  N=%d  |  "
            "alpha=%.1f  beta=%.1f warmup + %d timed\n\n",
            N, alpha, beta, num_warmup);

    // Host allocations
    float* h_A = (float*)malloc(bytes);
    float* h_B = (float*)malloc(bytes);
    float* h_C = (float*)malloc(bytes);
    float* h_D = (float*)malloc(bytes);
    float* h_E = (float*)malloc(bytes);

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
    HIP_CHECK(hipMalloc(&d_A, bytes));
    HIP_CHECK(hipMalloc(&d_B, bytes));
    HIP_CHECK(hipMalloc(&d_C, bytes));
    HIP_CHECK(hipMalloc(&d_D, bytes));
    HIP_CHECK(hipMalloc(&d_E, bytes));

    HIP_CHECK(hipMemcpy(d_A, h_A, bytes, hipMemcpyHostToDevice));
    HIP_CHECK(hipMemcpy(d_B, h_B, bytes, hipMemcpyHostToDevice));
    HIP_CHECK(hipMemcpy(d_C, h_C, bytes, hipMemcpyHostToDevice));

    dim3 block(TILE_SIZE, TILE_SIZE);
    dim3 grid((N + TILE_SIZE - 1) / TILE_SIZE, (N + TILE_SIZE - 1) / TILE_SIZE);

    auto run_2mm = [&]() {
        HIP_CHECK(hipMemcpy(d_D, h_D, bytes, hipMemcpyHostToDevice));
        HIP_CHECK(hipMemcpy(d_E, h_E, bytes, hipMemcpyHostToDevice));
        hipLaunchKernelGGL(mm_kernel, grid, block, 0, 0,
                           d_A, d_B, d_D, N, alpha, beta);
        hipLaunchKernelGGL(mm_kernel, grid, block, 0, 0,
                           d_C, d_D, d_E, N, alpha, beta);
    };

    // Warmup
    for (int w = 0; w < num_warmup; ++w) {
        run_2mm();
        HIP_CHECK(hipDeviceSynchronize());
    }

    // Timed iterations — time each mm_kernel launch separately
    hipEvent_t evStart, evStop;
    HIP_CHECK(hipEventCreate(&evStart));
    HIP_CHECK(hipEventCreate(&evStop));

    double t_mm1 = 0.0, t_mm2 = 0.0;
    for (int i = 0; i < 1; ++i) {
        // Restore D and E inputs (outside timing) before each timed pass
        HIP_CHECK(hipMemcpy(d_D, h_D, bytes, hipMemcpyHostToDevice));
        HIP_CHECK(hipMemcpy(d_E, h_E, bytes, hipMemcpyHostToDevice));

        // mm_kernel #1: D = alpha*A*B + beta*D
        HIP_CHECK(hipEventRecord(evStart, 0));
        hipLaunchKernelGGL(mm_kernel, grid, block, 0, 0,
                           d_A, d_B, d_D, N, alpha, beta);
        HIP_CHECK(hipEventRecord(evStop, 0));
        HIP_CHECK(hipEventSynchronize(evStop));
        float ms1 = 0.0f;
        HIP_CHECK(hipEventElapsedTime(&ms1, evStart, evStop));
        t_mm1 += (double)ms1;

        // mm_kernel #2: E = alpha*C*D + beta*E
        HIP_CHECK(hipEventRecord(evStart, 0));
        hipLaunchKernelGGL(mm_kernel, grid, block, 0, 0,
                           d_C, d_D, d_E, N, alpha, beta);
        HIP_CHECK(hipEventRecord(evStop, 0));
        HIP_CHECK(hipEventSynchronize(evStop));
        float ms2 = 0.0f;
        HIP_CHECK(hipEventElapsedTime(&ms2, evStart, evStop));
        t_mm2 += (double)ms2;
    }

    HIP_CHECK(hipEventDestroy(evStart));
    HIP_CHECK(hipEventDestroy(evStop));

    double avg_ms = t_mm1 + t_mm2;

    double gflops = 4.0 * (double)N * (double)N * (double)N
                    / (avg_ms * 1e-3) / 1e9;

    fprintf(stderr, "Performance: %.2f GFLOPS  (total %.4f ms: mm1=%.4f mm2=%.4f)\n",
            gflops, avg_ms, t_mm1, t_mm2);

    printf("{\"type\":\"kernel\",\"name\":\"mm_kernel_1\",\"time_ms\":%.6f,"
           "\"params\":{\"size\":%d,\"block_size\":%d,"
           "\"alpha\":%.2f,\"beta\":%.2f,\"precision\":\"%s\"}}\n",
           t_mm1, N, block_size, alpha, beta, precision);

    printf("{\"type\":\"kernel\",\"name\":\"mm_kernel_2\",\"time_ms\":%.6f,"
           "\"params\":{\"size\":%d,\"block_size\":%d,"
           "\"alpha\":%.2f,\"beta\":%.2f,\"precision\":\"%s\"}}\n",
           t_mm2, N, block_size, alpha, beta, precision);

    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"gflops\",\"value\":%.2f}]}\n",
           avg_ms, gflops);

    HIP_CHECK(hipFree(d_A));
    HIP_CHECK(hipFree(d_B));
    HIP_CHECK(hipFree(d_C));
    HIP_CHECK(hipFree(d_D));
    HIP_CHECK(hipFree(d_E));
    free(h_A);
    free(h_B);
    free(h_C);
    free(h_D);
    free(h_E);

    return 0;
}
