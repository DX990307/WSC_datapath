// polybench_atax.hip — PolyBench ATAX benchmark (HIP, self-contained)
//
// Computes y = A^T * (A * x) where A is NX×NY, x is NY-vector, y is NY-vector.
// Derived from the PolyBench/GPU benchmark suite.
//
// Two kernels:
//   atax_kernel1: tmp[i] = sum_j A[i,j] * x[j]   (tmp = A * x)
//   atax_kernel2: y[j]   = sum_i A[i,j] * tmp[i]  (y = A^T * tmp)
//
// Default: NX = NY = 4096
// Measures memory bandwidth in GB/s (A is read twice per iteration).
//
// Compiles with hipcc (AMD ROCm) or nvcc -x cu (CUDA).
//
// Usage:
//   ./polybench_atax [--nx NX] [--ny NY]
//
//   --nx NX          Number of rows    (default: 4096)
//   --ny NY          Number of columns (default: 4096)
//
// Output (stdout): JSON-lines protocol
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
// Inlined bench_common_hip.h utilities
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
// Kernel 1: tmp = A * x
// Each thread computes one row: tmp[i] = sum_j A[i,j] * x[j]
// ---------------------------------------------------------------------------
__global__ void atax_kernel1(const float* __restrict__ A,
                              const float* __restrict__ x,
                              float* __restrict__ tmp,
                              int nx, int ny) {
    int i = blockDim.x * blockIdx.x + threadIdx.x;
    if (i < nx) {
        float sum = 0.0f;
        for (int j = 0; j < ny; j++)
            sum += A[i * ny + j] * x[j];
        tmp[i] = sum;
    }
}

// ---------------------------------------------------------------------------
// Kernel 2: y = A^T * tmp
// Each thread computes one column: y[j] = sum_i A[i,j] * tmp[i]
// ---------------------------------------------------------------------------
__global__ void atax_kernel2(const float* __restrict__ A,
                              const float* __restrict__ tmp,
                              float* __restrict__ y,
                              int nx, int ny) {
    int j = blockDim.x * blockIdx.x + threadIdx.x;
    if (j < ny) {
        float sum = 0.0f;
        for (int i = 0; i < nx; i++)
            sum += A[i * ny + j] * tmp[i];
        y[j] = sum;
    }
}

// ---------------------------------------------------------------------------
// CPU reference for correctness check
// ---------------------------------------------------------------------------
static void cpu_atax(const float* A, const float* x, float* y_ref, int nx, int ny) {
    // tmp = A * x
    std::vector<float> tmp(nx, 0.0f);
    for (int i = 0; i < nx; i++)
        for (int j = 0; j < ny; j++)
            tmp[i] += A[i * ny + j] * x[j];
    // y = A^T * tmp
    for (int j = 0; j < ny; j++) {
        y_ref[j] = 0.0f;
        for (int i = 0; i < nx; i++)
            y_ref[j] += A[i * ny + j] * tmp[i];
    }
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------
int main(int argc, char** argv) {
    setvbuf(stdout, NULL, _IONBF, 0);

    int nx    = parseIntParam(argc, argv, "--nx", 4096);
    int ny    = parseIntParam(argc, argv, "--ny", 4096);
    int block_size = 256;

    const char* env_val;
    env_val = getenv("BENCH_PARAM_nx");
    if (env_val) nx = atoi(env_val);
    env_val = getenv("BENCH_PARAM_ny");
    if (env_val) ny = atoi(env_val);
    env_val = getenv("BENCH_PARAM_block_size");
    if (env_val) block_size = atoi(env_val);
    int num_warmup = 5;

    size_t bytes_A   = (size_t)nx * ny * sizeof(float);
    size_t bytes_x   = (size_t)ny * sizeof(float);
    size_t bytes_tmp = (size_t)nx * sizeof(float);
    size_t bytes_y   = (size_t)ny * sizeof(float);

    // Print device info
    int device_id = 0;
    HIP_CHECK(hipGetDevice(&device_id));
    hipDeviceProp_t prop;
    HIP_CHECK(hipGetDeviceProperties(&prop, device_id));
    fprintf(stderr, "Device: %s (id=%d)\n", prop.name, device_id);
    fprintf(stderr, "Matrix size: %d×%d\n\n", nx, ny);

    // Host allocations
    float* h_A = (float*)malloc(bytes_A);
    float* h_x = (float*)malloc(bytes_x);
    float* h_y = (float*)malloc(bytes_y);

    // Initialize (random floats)
    srand(42);
    for (int i = 0; i < nx * ny; ++i)
        h_A[i] = (float)(rand() % 100) / 10.0f;
    for (int j = 0; j < ny; ++j)
        h_x[j] = (float)(rand() % 100) / 10.0f;

    // Device allocations
    float *d_A, *d_x, *d_tmp, *d_y;
    HIP_CHECK(hipMalloc(&d_A,   bytes_A));
    HIP_CHECK(hipMalloc(&d_x,   bytes_x));
    HIP_CHECK(hipMalloc(&d_tmp, bytes_tmp));
    HIP_CHECK(hipMalloc(&d_y,   bytes_y));

    HIP_CHECK(hipMemcpy(d_A, h_A, bytes_A, hipMemcpyHostToDevice));
    HIP_CHECK(hipMemcpy(d_x, h_x, bytes_x, hipMemcpyHostToDevice));

    int grid1 = (nx + block_size - 1) / block_size;
    int grid2 = (ny + block_size - 1) / block_size;

    BenchmarkTimer timer;

    // Warmup
    for (int w = 0; w < num_warmup; ++w) {
        hipLaunchKernelGGL(atax_kernel1, grid1, block_size, 0, 0, d_A, d_x, d_tmp, nx, ny);
        hipLaunchKernelGGL(atax_kernel2, grid2, block_size, 0, 0, d_A, d_tmp, d_y, nx, ny);
        HIP_CHECK(hipDeviceSynchronize());
    }

    // Timed iterations — time each kernel separately
    std::vector<double> k1_times(1), k2_times(1);
    for (int i = 0; i < 1; ++i) {
        timer.record_start();
        hipLaunchKernelGGL(atax_kernel1, grid1, block_size, 0, 0, d_A, d_x, d_tmp, nx, ny);
        timer.record_stop();
        k1_times[i] = static_cast<double>(timer.elapsed_ms());

        timer.record_start();
        hipLaunchKernelGGL(atax_kernel2, grid2, block_size, 0, 0, d_A, d_tmp, d_y, nx, ny);
        timer.record_stop();
        k2_times[i] = static_cast<double>(timer.elapsed_ms());
    }

    // Copy result back for correctness check
    HIP_CHECK(hipMemcpy(h_y, d_y, bytes_y, hipMemcpyDeviceToHost));

    // Correctness check against CPU reference (small subset)
    int check_n = (ny < 8) ? ny : 8;
    std::vector<float> y_ref(ny);
    cpu_atax(h_A, h_x, y_ref.data(), nx, ny);
    bool pass = true;
    for (int j = 0; j < check_n; ++j) {
        float rel = fabsf(h_y[j] - y_ref[j]) / (fabsf(y_ref[j]) + 1e-6f);
        if (rel > 1e-3f) {
            fprintf(stderr, "MISMATCH at y[%d]: gpu=%.6f cpu=%.6f rel=%.4e\n",
                    j, h_y[j], y_ref[j], rel);
            pass = false;
        }
    }
    fprintf(stderr, "Correctness check (first %d elements): %s\n\n",
            check_n, pass ? "PASS" : "FAIL");

    // Compute statistics
    double k1_sum = 0.0, k2_sum = 0.0;
    for (int i = 0; i < 1; ++i) {
        k1_sum += k1_times[i];
        k2_sum += k2_times[i];
    }
    double k1_avg = k1_sum / 1;
    double k2_avg = k2_sum / 1;
    double total_avg_ms = k1_avg + k2_avg;

    // GB/s: A is read twice (kernel1 + kernel2), each read is NX*NY*4 bytes
    double gb = 2.0 * (double)nx * (double)ny * sizeof(float) / 1e9;
    double gbps = gb / (total_avg_ms * 1e-3);

    fprintf(stderr, "Bandwidth: %.2f GB/s  (total avg %.4f ms: k1=%.4f k2=%.4f)\n",
            gbps, total_avg_ms, k1_avg, k2_avg);

    // JSON-lines output
    printf("{\"type\":\"kernel\",\"name\":\"atax_kernel1\",\"time_ms\":%.6f,"
           "\"params\":{\"nx\":%d,\"ny\":%d,\"block_size\":%d}}\n",
           k1_avg, nx, ny, block_size);
    printf("{\"type\":\"kernel\",\"name\":\"atax_kernel2\",\"time_ms\":%.6f,"
           "\"params\":{\"nx\":%d,\"ny\":%d,\"block_size\":%d}}\n",
           k2_avg, nx, ny, block_size);
    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"bandwidth_gbps\",\"value\":%.2f}]}\n",
           total_avg_ms, gbps);

    // Cleanup
    HIP_CHECK(hipFree(d_A));
    HIP_CHECK(hipFree(d_x));
    HIP_CHECK(hipFree(d_tmp));
    HIP_CHECK(hipFree(d_y));
    free(h_A);
    free(h_x);
    free(h_y);

    return 0;
}
