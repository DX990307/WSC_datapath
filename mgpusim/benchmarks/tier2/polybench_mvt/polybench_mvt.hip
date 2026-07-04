// polybench_mvt.hip — PolyBench MVT benchmark (HIP, self-contained)
//
// Computes:
//   x1 = A * y1   (matrix-vector product)
//   x2 = A^T * y2 (transpose matrix-vector product)
// where A is N×N, x1, x2, y1, y2 are N-vectors.
//
// Derived from the PolyBench/GPU benchmark suite.
//
// Two kernels:
//   mvt_kernel1: x1[i] = sum_j A[i,j] * y1[j]   (one row per thread)
//   mvt_kernel2: x2[j] = sum_i A[i,j] * y2[i]   (one column per thread)
//
// Default: N=4096
// Measures memory bandwidth in GB/s (A is read twice per iteration).
//
// Compiles with hipcc (AMD ROCm) or nvcc -x cu (CUDA).
//
// Usage:
//   ./polybench_mvt [--n N]
//
//   --n N            Matrix dimension (default: 4096)
//
// Output (stdout): CSV — mvt,<N>,<time_ms>,<GB/s>
// Output (stderr): device info, timing details

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
// Inlined HIP utilities
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
// Kernel 1: x1 = A * y1
// Each thread computes one row: x1[i] = sum_j A[i,j] * y1[j]
// ---------------------------------------------------------------------------
__global__ void mvt_kernel1(const float* __restrict__ A,
                              const float* __restrict__ y1,
                              float* __restrict__ x1,
                              int n) {
    int i = blockDim.x * blockIdx.x + threadIdx.x;
    if (i >= n) return;
    float sum = 0.0f;
    for (int j = 0; j < n; j++)
        sum += A[i * n + j] * y1[j];
    x1[i] = sum;
}

// ---------------------------------------------------------------------------
// Kernel 2: x2 = A^T * y2
// Each thread computes one column: x2[j] = sum_i A[i,j] * y2[i]
// ---------------------------------------------------------------------------
__global__ void mvt_kernel2(const float* __restrict__ A,
                              const float* __restrict__ y2,
                              float* __restrict__ x2,
                              int n) {
    int j = blockDim.x * blockIdx.x + threadIdx.x;
    if (j >= n) return;
    float sum = 0.0f;
    for (int i = 0; i < n; i++)
        sum += A[i * n + j] * y2[i];
    x2[j] = sum;
}

// ---------------------------------------------------------------------------
// CPU reference for correctness check
// ---------------------------------------------------------------------------
static void cpu_mvt(const float* A, const float* y1, const float* y2,
                    float* x1_ref, float* x2_ref, int n) {
    // x1 = A * y1
    for (int i = 0; i < n; i++) {
        x1_ref[i] = 0.0f;
        for (int j = 0; j < n; j++)
            x1_ref[i] += A[i * n + j] * y1[j];
    }
    // x2 = A^T * y2
    for (int j = 0; j < n; j++) {
        x2_ref[j] = 0.0f;
        for (int i = 0; i < n; i++)
            x2_ref[j] += A[i * n + j] * y2[i];
    }
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------
int main(int argc, char** argv) {
    setvbuf(stdout, NULL, _IONBF, 0);

    int n     = parseIntParam(argc, argv, "--n", 4096);
    int block_size_param = parseIntParam(argc, argv, "--block_size", 256);
    const char* precision = "float";

    const char* env_val;
    env_val = getenv("BENCH_PARAM_n");
    if (env_val) n = atoi(env_val);
    env_val = getenv("BENCH_PARAM_block_size");
    if (env_val) block_size_param = atoi(env_val);
    env_val = getenv("BENCH_PARAM_precision");
    if (env_val) precision = env_val;
    int num_warmup = 5;

    size_t bytes_A  = (size_t)n * n * sizeof(float);
    size_t bytes_v  = (size_t)n * sizeof(float);

    // Print device info
    int device_id = 0;
    HIP_CHECK(hipGetDevice(&device_id));
    hipDeviceProp_t prop;
    HIP_CHECK(hipGetDeviceProperties(&prop, device_id));
    fprintf(stderr, "Device: %s (id=%d)\n", prop.name, device_id);
    fprintf(stderr, "Matrix size: %d×%d\n\n", n, n);

    // Host allocations
    float* h_A  = (float*)malloc(bytes_A);
    float* h_y1 = (float*)malloc(bytes_v);
    float* h_y2 = (float*)malloc(bytes_v);
    float* h_x1 = (float*)malloc(bytes_v);
    float* h_x2 = (float*)malloc(bytes_v);

    // Initialize with random floats
    srand(42);
    for (int i = 0; i < n * n; ++i)
        h_A[i] = (float)(rand() % 100) / 10.0f;
    for (int j = 0; j < n; ++j) {
        h_y1[j] = (float)(rand() % 100) / 10.0f;
        h_y2[j] = (float)(rand() % 100) / 10.0f;
        h_x1[j] = 0.0f;
        h_x2[j] = 0.0f;
    }

    // Device allocations
    float *d_A, *d_y1, *d_y2, *d_x1, *d_x2;
    HIP_CHECK(hipMalloc(&d_A,  bytes_A));
    HIP_CHECK(hipMalloc(&d_y1, bytes_v));
    HIP_CHECK(hipMalloc(&d_y2, bytes_v));
    HIP_CHECK(hipMalloc(&d_x1, bytes_v));
    HIP_CHECK(hipMalloc(&d_x2, bytes_v));

    HIP_CHECK(hipMemcpy(d_A,  h_A,  bytes_A, hipMemcpyHostToDevice));
    HIP_CHECK(hipMemcpy(d_y1, h_y1, bytes_v, hipMemcpyHostToDevice));
    HIP_CHECK(hipMemcpy(d_y2, h_y2, bytes_v, hipMemcpyHostToDevice));

    // Initialize output vectors to zero on device
    HIP_CHECK(hipMemset(d_x1, 0, bytes_v));
    HIP_CHECK(hipMemset(d_x2, 0, bytes_v));

    int block_size = 256;
    int grid       = (n + block_size - 1) / block_size;

    BenchmarkTimer timer;

    // Warmup
    for (int w = 0; w < num_warmup; ++w) {
        hipLaunchKernelGGL(mvt_kernel1, grid, block_size, 0, 0, d_A, d_y1, d_x1, n);
        hipLaunchKernelGGL(mvt_kernel2, grid, block_size, 0, 0, d_A, d_y2, d_x2, n);
        HIP_CHECK(hipDeviceSynchronize());
    }

    // Timed iterations — time each kernel separately
    double t_k1 = 0.0, t_k2 = 0.0;
    for (int it = 0; it < 1; ++it) {
        timer.record_start();
        hipLaunchKernelGGL(mvt_kernel1, grid, block_size, 0, 0, d_A, d_y1, d_x1, n);
        timer.record_stop();
        t_k1 += static_cast<double>(timer.elapsed_ms());

        timer.record_start();
        hipLaunchKernelGGL(mvt_kernel2, grid, block_size, 0, 0, d_A, d_y2, d_x2, n);
        timer.record_stop();
        t_k2 += static_cast<double>(timer.elapsed_ms());
    }

    // Copy results back for correctness check
    HIP_CHECK(hipMemcpy(h_x1, d_x1, bytes_v, hipMemcpyDeviceToHost));
    HIP_CHECK(hipMemcpy(h_x2, d_x2, bytes_v, hipMemcpyDeviceToHost));

    // Correctness check against CPU reference (small subset)
    int check_n = (n < 8) ? n : 8;
    std::vector<float> x1_ref(n), x2_ref(n);
    cpu_mvt(h_A, h_y1, h_y2, x1_ref.data(), x2_ref.data(), n);

    bool pass = true;
    for (int i = 0; i < check_n; ++i) {
        float rel1 = fabsf(h_x1[i] - x1_ref[i]) / (fabsf(x1_ref[i]) + 1e-6f);
        if (rel1 > 1e-3f) {
            fprintf(stderr, "MISMATCH x1[%d]: gpu=%.6f cpu=%.6f rel=%.4e\n",
                    i, h_x1[i], x1_ref[i], rel1);
            pass = false;
        }
        float rel2 = fabsf(h_x2[i] - x2_ref[i]) / (fabsf(x2_ref[i]) + 1e-6f);
        if (rel2 > 1e-3f) {
            fprintf(stderr, "MISMATCH x2[%d]: gpu=%.6f cpu=%.6f rel=%.4e\n",
                    i, h_x2[i], x2_ref[i], rel2);
            pass = false;
        }
    }
    fprintf(stderr, "Correctness check (first %d elements): %s\n\n",
            check_n, pass ? "PASS" : "FAIL");

    // Compute statistics
    double avg_ms = t_k1 + t_k2;

    // GB/s: A is read twice (kernel1 + kernel2), each read is N*N*4 bytes
    double gb   = 2.0 * (double)n * (double)n * sizeof(float) / 1e9;
    double gbps = gb / (avg_ms * 1e-3);

    fprintf(stderr, "Bandwidth: %.2f GB/s  (total %.4f ms: k1=%.4f k2=%.4f)\n",
            gbps, avg_ms, t_k1, t_k2);

    // JSON-lines output
    printf("{\"type\":\"kernel\",\"name\":\"mvt_kernel1\",\"time_ms\":%.6f,"
           "\"params\":{\"n\":%d,\"block_size\":%d,"
           "\"precision\":\"%s\"}}\n",
           t_k1, n, block_size_param, precision);
    printf("{\"type\":\"kernel\",\"name\":\"mvt_kernel2\",\"time_ms\":%.6f,"
           "\"params\":{\"n\":%d,\"block_size\":%d,"
           "\"precision\":\"%s\"}}\n",
           t_k2, n, block_size_param, precision);
    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"bandwidth_gbps\",\"value\":%.2f}]}\n",
           avg_ms, gbps);

    // Cleanup
    HIP_CHECK(hipFree(d_A));
    HIP_CHECK(hipFree(d_y1));
    HIP_CHECK(hipFree(d_y2));
    HIP_CHECK(hipFree(d_x1));
    HIP_CHECK(hipFree(d_x2));
    free(h_A);
    free(h_y1);
    free(h_y2);
    free(h_x1);
    free(h_x2);

    return 0;
}
