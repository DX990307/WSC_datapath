// shoc_spmv.hip — SHOC CSR Sparse Matrix-Vector Multiply benchmark (HIP, self-contained)
//
// Computes: y = A * x
//   where A is a synthetic CSR sparse matrix with exactly nnz_per_row non-zeros per row.
//
// Parameters are read from BENCH_PARAM_* environment variables,
// with command-line --flags as fallback.
//
// Output (stdout): JSON-lines protocol
// Output (stderr): Human-readable diagnostics

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
// Inlined utilities
// ---------------------------------------------------------------------------

#define HIP_CHECK(cmd)                                                          \
    do {                                                                        \
        hipError_t _e = (cmd);                                                  \
        if (_e != hipSuccess) {                                                 \
            fprintf(stderr, "HIP error %s at %s:%d\n",                         \
                    hipGetErrorString(_e), __FILE__, __LINE__);                 \
            exit(1);                                                            \
        }                                                                       \
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
// CSR SpMV kernel: each thread handles one row
// ---------------------------------------------------------------------------

__global__ void spmv_csr_kernel(
    const int*   __restrict__ row_ptr,
    const int*   __restrict__ col_idx,
    const float* __restrict__ values,
    const float* __restrict__ x,
    float*       __restrict__ y,
    int num_rows)
{
    int row = blockIdx.x * blockDim.x + threadIdx.x;
    if (row >= num_rows) return;
    float sum = 0.0f;
    int start = row_ptr[row];
    int end   = row_ptr[row + 1];
    for (int k = start; k < end; ++k)
        sum += values[k] * x[col_idx[k]];
    y[row] = sum;
}

// ---------------------------------------------------------------------------
// CPU reference
// ---------------------------------------------------------------------------

static void cpu_spmv(const int* row_ptr, const int* col_idx,
                     const float* values, const float* x,
                     float* y, int num_rows)
{
    for (int row = 0; row < num_rows; ++row) {
        float sum = 0.0f;
        for (int k = row_ptr[row]; k < row_ptr[row + 1]; ++k)
            sum += values[k] * x[col_idx[k]];
        y[row] = sum;
    }
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------

int main(int argc, char** argv)
{
    setvbuf(stdout, NULL, _IONBF, 0);

    // Defaults
    int num_rows    = 16384;
    int nnz_per_row = 32;
    const char* precision = "float";

    // Command-line fallback
    num_rows    = parseIntParam(argc, argv, "--num_rows", num_rows);
    nnz_per_row = parseIntParam(argc, argv, "--nnz_per_row", nnz_per_row);
    precision   = parseStrParam(argc, argv, "--precision", precision);

    // Environment variable overrides
    const char* env_val;
    env_val = getenv("BENCH_PARAM_num_rows");
    if (env_val) num_rows = atoi(env_val);
    env_val = getenv("BENCH_PARAM_nnz_per_row");
    if (env_val) nnz_per_row = atoi(env_val);
    env_val = getenv("BENCH_PARAM_precision");
    if (env_val) precision = env_val;
    int num_warmup = 5;

    int num_cols   = num_rows;
    int nnz        = num_rows * nnz_per_row;
    int BLOCK_SIZE = 256;

    // Allocate host arrays
    int*   h_row_ptr = (int*)  malloc((size_t)(num_rows + 1) * sizeof(int));
    int*   h_col_idx = (int*)  malloc((size_t)nnz            * sizeof(int));
    float* h_values  = (float*)malloc((size_t)nnz            * sizeof(float));
    float* h_x       = (float*)malloc((size_t)num_rows       * sizeof(float));
    float* h_y       = (float*)malloc((size_t)num_rows       * sizeof(float));
    float* h_y_ref   = (float*)malloc((size_t)num_rows       * sizeof(float));

    // Build CSR matrix
    srand(42);
    for (int i = 0; i <= num_rows; ++i)
        h_row_ptr[i] = i * nnz_per_row;

    for (int k = 0; k < nnz; ++k) {
        h_col_idx[k] = rand() % num_cols;
        h_values[k]  = (float)rand() / (float)RAND_MAX;
    }
    for (int i = 0; i < num_rows; ++i)
        h_x[i] = (float)rand() / (float)RAND_MAX;

    // Print device info
    int device_id = 0;
    HIP_CHECK(hipGetDevice(&device_id));
    hipDeviceProp_t prop;
    HIP_CHECK(hipGetDeviceProperties(&prop, device_id));
    fprintf(stderr, "Device: %s (id=%d)\n", prop.name, device_id);
    fprintf(stderr, "Rows: %d  |  NNZ_per_row: %d  |  Total NNZ: %d\n\n",
            num_rows, nnz_per_row, nnz);

    // Device allocations
    int*   d_row_ptr;
    int*   d_col_idx;
    float* d_values;
    float* d_x;
    float* d_y;
    HIP_CHECK(hipMalloc(&d_row_ptr, (size_t)(num_rows + 1) * sizeof(int)));
    HIP_CHECK(hipMalloc(&d_col_idx, (size_t)nnz            * sizeof(int)));
    HIP_CHECK(hipMalloc(&d_values,  (size_t)nnz            * sizeof(float)));
    HIP_CHECK(hipMalloc(&d_x,       (size_t)num_rows       * sizeof(float)));
    HIP_CHECK(hipMalloc(&d_y,       (size_t)num_rows       * sizeof(float)));

    HIP_CHECK(hipMemcpy(d_row_ptr, h_row_ptr,
                        (size_t)(num_rows + 1) * sizeof(int), hipMemcpyHostToDevice));
    HIP_CHECK(hipMemcpy(d_col_idx, h_col_idx,
                        (size_t)nnz * sizeof(int), hipMemcpyHostToDevice));
    HIP_CHECK(hipMemcpy(d_values, h_values,
                        (size_t)nnz * sizeof(float), hipMemcpyHostToDevice));
    HIP_CHECK(hipMemcpy(d_x, h_x,
                        (size_t)num_rows * sizeof(float), hipMemcpyHostToDevice));

    int grid_size = (num_rows + BLOCK_SIZE - 1) / BLOCK_SIZE;

    BenchmarkTimer timer;

    // Warmup
    for (int w = 0; w < num_warmup; ++w) {
        hipLaunchKernelGGL(spmv_csr_kernel, grid_size, BLOCK_SIZE, 0, 0,
                           d_row_ptr, d_col_idx, d_values, d_x, d_y, num_rows);
        HIP_CHECK(hipDeviceSynchronize());
    }

    // Timed iterations
    std::vector<double> times(1);
    for (int iter = 0; iter < 1; ++iter) {
        timer.record_start();
        hipLaunchKernelGGL(spmv_csr_kernel, grid_size, BLOCK_SIZE, 0, 0,
                           d_row_ptr, d_col_idx, d_values, d_x, d_y, num_rows);
        timer.record_stop();
        times[iter] = static_cast<double>(timer.elapsed_ms());
        fprintf(stderr, "  iter %d: %.4f ms\n", iter, times[iter]);
    }

    // Copy result back
    HIP_CHECK(hipMemcpy(h_y, d_y, (size_t)num_rows * sizeof(float), hipMemcpyDeviceToHost));

    // CPU reference
    cpu_spmv(h_row_ptr, h_col_idx, h_values, h_x, h_y_ref, num_rows);

    bool pass = true;
    int check_n = 32;
    for (int i = 0; i < check_n && i < num_rows; ++i) {
        float ref = h_y_ref[i];
        float rel = fabsf(h_y[i] - ref) / (fabsf(ref) + 1e-6f);
        if (rel > 1e-4f) {
            fprintf(stderr, "MISMATCH y[%d]: gpu=%.6f cpu=%.6f rel=%.4e\n",
                    i, h_y[i], ref, rel);
            pass = false;
        }
    }
    fprintf(stderr, "Correctness check (first %d elements): %s\n\n",
            check_n, pass ? "PASS" : "FAIL");

    // Statistics
    double sum_ms = 0.0, mn = DBL_MAX, mx = 0.0;
    for (int i = 0; i < 1; ++i) {
        sum_ms += times[i];
        if (times[i] < mn) mn = times[i];
        if (times[i] > mx) mx = times[i];
    }
    double avg_ms = sum_ms / 1;
    double total_ms = sum_ms;

    // GB/s and GFLOPS
    double data_bytes = (double)nnz * (sizeof(float) + sizeof(int))
                 + (double)(num_rows + 1) * sizeof(int)
                 + (double)num_rows * sizeof(float)
                 + (double)num_rows * sizeof(float);
    double gbps   = data_bytes / (avg_ms * 1e-3) / 1e9;
    double gflops = 2.0 * (double)nnz / (avg_ms * 1e-3) / 1e9;

    fprintf(stderr, "GB/s: %.2f  GFLOPS: %.2f  (avg %.4f ms, min %.4f ms, max %.4f ms)\n",
            gbps, gflops, avg_ms, mn, mx);

    // JSON-lines kernel event
    printf("{\"type\":\"kernel\",\"name\":\"spmv_csr_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"num_rows\":%d,\"nnz_per_row\":%d,"
           "\"precision\":\"%s\"}}\n",
           avg_ms, num_rows, nnz_per_row, precision);

    // JSON-lines summary event
    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"bandwidth_gbps\",\"value\":%.2f},"
           "{\"name\":\"gflops\",\"value\":%.2f}]}\n",
           total_ms, gbps, gflops);

    // Cleanup
    HIP_CHECK(hipFree(d_row_ptr));
    HIP_CHECK(hipFree(d_col_idx));
    HIP_CHECK(hipFree(d_values));
    HIP_CHECK(hipFree(d_x));
    HIP_CHECK(hipFree(d_y));
    free(h_row_ptr);
    free(h_col_idx);
    free(h_values);
    free(h_x);
    free(h_y);
    free(h_y_ref);

    return pass ? EXIT_SUCCESS : EXIT_FAILURE;
}
