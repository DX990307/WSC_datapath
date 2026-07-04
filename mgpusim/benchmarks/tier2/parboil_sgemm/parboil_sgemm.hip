// parboil_sgemm.hip — Parboil SGEMM benchmark (HIP, self-contained)
//
// Tiled single-precision GEMM: C = alpha*A*B + beta*C for NxN matrices.
// Measures compute throughput in GFLOPS.
//
// Derived from the Parboil benchmark suite SGEMM workload.
//
// Compiles with hipcc (AMD ROCm) or nvcc (CUDA via HIP compat layer).
//
// Usage:
//   ./parboil_sgemm [--size N]
//
//   --size N         Matrix dimension N (NxN)  (default: 1024)
//
// Output (stdout): parboil_sgemm,<N>,<GFLOPS>

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
// Inlined HIP utility macros
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

// ---------------------------------------------------------------------------
// HIP event timer
// ---------------------------------------------------------------------------

struct HipTimer {
    hipEvent_t start, stop;
    HipTimer() {
        HIP_CHECK(hipEventCreate(&start));
        HIP_CHECK(hipEventCreate(&stop));
    }
    ~HipTimer() {
        (void)hipEventDestroy(start);
        (void)hipEventDestroy(stop);
    }
    void begin(hipStream_t stream = 0) {
        HIP_CHECK(hipEventRecord(start, stream));
    }
    float end(hipStream_t stream = 0) {
        HIP_CHECK(hipEventRecord(stop, stream));
        HIP_CHECK(hipEventSynchronize(stop));
        float ms = 0.0f;
        HIP_CHECK(hipEventElapsedTime(&ms, start, stop));
        return ms;
    }
};

// ---------------------------------------------------------------------------
// Tiled SGEMM kernel (16x16 shared-memory tiles)
// ---------------------------------------------------------------------------

#define TILE 16

__global__ void sgemm_kernel(const float* __restrict__ A,
                              const float* __restrict__ B,
                              float* __restrict__ C,
                              int N, float alpha, float beta)
{
    __shared__ float tA[TILE][TILE];
    __shared__ float tB[TILE][TILE];

    int row = blockIdx.y * TILE + threadIdx.y;
    int col = blockIdx.x * TILE + threadIdx.x;

    float sum = 0.0f;
    int num_tiles = (N + TILE - 1) / TILE;

    for (int t = 0; t < num_tiles; ++t) {
        int aCol = t * TILE + threadIdx.x;
        int bRow = t * TILE + threadIdx.y;

        tA[threadIdx.y][threadIdx.x] = (row < N && aCol < N) ? A[row * N + aCol] : 0.0f;
        tB[threadIdx.y][threadIdx.x] = (bRow < N && col < N) ? B[bRow * N + col] : 0.0f;

        __syncthreads();

        for (int k = 0; k < TILE; ++k) {
            sum += tA[threadIdx.y][k] * tB[k][threadIdx.x];
        }

        __syncthreads();
    }

    if (row < N && col < N) {
        C[row * N + col] = alpha * sum + beta * C[row * N + col];
    }
}

// ---------------------------------------------------------------------------
// CPU reference for correctness check
// ---------------------------------------------------------------------------

static void sgemm_cpu(const float* A, const float* B, float* C,
                      int N, float alpha, float beta) {
    for (int i = 0; i < N; ++i) {
        for (int j = 0; j < N; ++j) {
            float s = 0.0f;
            for (int k = 0; k < N; ++k) {
                s += A[i * N + k] * B[k * N + j];
            }
            C[i * N + j] = alpha * s + beta * C[i * N + j];
        }
    }
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------

int main(int argc, char** argv)
{
    setvbuf(stdout, NULL, _IONBF, 0);

    // Default values from params.json
    int N     = 1024;
    int block_size = 256;
    const char* precision = "float";

    // Command-line args as fallback
    N     = parseIntParam(argc, argv, "--size", N);
    block_size = parseIntParam(argc, argv, "--block_size", block_size);

    // Env vars override
    const char* env_val;
    env_val = getenv("BENCH_PARAM_size");
    if (env_val) N = atoi(env_val);
    env_val = getenv("BENCH_PARAM_block_size");
    if (env_val) block_size = atoi(env_val);
    env_val = getenv("BENCH_PARAM_precision");
    if (env_val) precision = env_val;
    int num_warmup = 5;

    float alpha = 1.5f;
    float beta  = 1.2f;

    size_t bytes = (size_t)N * N * sizeof(float);

    // Print device info
    int device_id = 0;
    HIP_CHECK(hipGetDevice(&device_id));
    hipDeviceProp_t prop;
    HIP_CHECK(hipGetDeviceProperties(&prop, device_id));
    fprintf(stderr, "Device: %s (id=%d)\n", prop.name, device_id);
    fprintf(stderr, "Matrix size: %d×%d  |  alpha=%.1f  beta=%.1f\n\n",
            N, N, alpha, beta);

    // Host allocations
    float* h_A   = (float*)malloc(bytes);
    float* h_B   = (float*)malloc(bytes);
    float* h_C   = (float*)malloc(bytes);
    float* h_ref = (float*)malloc(bytes);

    // Initialize inputs
    for (int i = 0; i < N * N; ++i)
        h_A[i] = (float)(i % 1000) * 0.001f;
    for (int i = 0; i < N * N; ++i)
        h_B[i] = (float)((i + 37) % 1000) * 0.001f;
    for (int i = 0; i < N * N; ++i) {
        h_C[i]   = (float)(i % 100) * 0.01f;
        h_ref[i] = h_C[i];
    }

    // Device allocations
    float *d_A, *d_B, *d_C;
    HIP_CHECK(hipMalloc(&d_A, bytes));
    HIP_CHECK(hipMalloc(&d_B, bytes));
    HIP_CHECK(hipMalloc(&d_C, bytes));

    HIP_CHECK(hipMemcpy(d_A, h_A, bytes, hipMemcpyHostToDevice));
    HIP_CHECK(hipMemcpy(d_B, h_B, bytes, hipMemcpyHostToDevice));
    HIP_CHECK(hipMemcpy(d_C, h_C, bytes, hipMemcpyHostToDevice));

    dim3 block(TILE, TILE);
    dim3 grid((N + TILE - 1) / TILE, (N + TILE - 1) / TILE);

    // Warmup
    for (int w = 0; w < num_warmup; ++w) {
        hipLaunchKernelGGL(sgemm_kernel, grid, block, 0, 0,
                           d_A, d_B, d_C, N, alpha, beta);
        HIP_CHECK(hipDeviceSynchronize());
        HIP_CHECK(hipMemcpy(d_C, h_C, bytes, hipMemcpyHostToDevice));
    }

    // Timed iterations
    HipTimer timer;
    std::vector<double> times(1);

    for (int i = 0; i < 1; ++i) {
        // Reset C to initial state so beta accumulation is consistent
        HIP_CHECK(hipMemcpy(d_C, h_C, bytes, hipMemcpyHostToDevice));
        timer.begin();
        hipLaunchKernelGGL(sgemm_kernel, grid, block, 0, 0,
                           d_A, d_B, d_C, N, alpha, beta);
        float ms = timer.end();
        times[i] = (double)ms;
    }

    // Compute statistics
    double sum_t = 0.0, mn = DBL_MAX, mx = 0.0;
    for (int i = 0; i < 1; ++i) {
        sum_t += times[i];
        if (times[i] < mn) mn = times[i];
        if (times[i] > mx) mx = times[i];
    }
    double avg_ms = sum_t / 1;

    // GFLOPS: 2*N*N*N multiply-adds
    double gflops = 2.0 * (double)N * (double)N * (double)N
                    / (avg_ms * 1e-3) / 1e9;

    // JSON-lines output
    printf("{\"type\":\"kernel\",\"name\":\"sgemm_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"size\":%d,\"block_size\":%d,"
           "\"precision\":\"%s\"}}\n",
           avg_ms, N, block_size, precision);

    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"gflops\",\"value\":%.2f}]}\n",
           sum_t, gflops);

    // Human-readable stderr
    fprintf(stderr, "Performance: %.2f GFLOPS  (avg %.4f ms, min %.4f ms, max %.4f ms)\n",
            gflops, avg_ms, mn, mx);

    // Correctness check (only for small N to keep runtime short)
    if (N <= 256) {
        HIP_CHECK(hipMemcpy(h_C, d_C, bytes, hipMemcpyDeviceToHost));
        sgemm_cpu(h_A, h_B, h_ref, N, alpha, beta);

        int errors = 0;
        for (int i = 0; i < N * N; ++i) {
            float diff = fabsf(h_C[i] - h_ref[i]);
            if (diff > 1e-3f * fabsf(h_ref[i]) + 1e-4f) {
                if (errors < 10) {
                    fprintf(stderr, "Mismatch at %d: got %f, expected %f\n",
                            i, h_C[i], h_ref[i]);
                }
                errors++;
            }
        }
        if (errors > 0)
            fprintf(stderr, "FAIL: %d errors\n", errors);
        else
            fprintf(stderr, "PASS\n");
    } else {
        fprintf(stderr, "Skipping verification for large size\n");
    }

    HIP_CHECK(hipFree(d_A));
    HIP_CHECK(hipFree(d_B));
    HIP_CHECK(hipFree(d_C));
    free(h_A);
    free(h_B);
    free(h_C);
    free(h_ref);

    return 0;
}
