// npb_cg.hip — NPB Conjugate Gradient benchmark (HIP/ROCm)
//
// Sparse iterative CG solver: SpMV (CSR format) + dot products + AXPY
//
// Compiles with hipcc (AMD ROCm) or nvcc via HIP compatibility layer.
//
// Output (stdout): JSON-lines
// Output (stderr): human-readable

#if defined(__NVCC__) && !__has_include(<hip/hip_runtime.h>)
#include <cuda_runtime.h>
#define hipMalloc               cudaMalloc
#define hipFree                 cudaFree
#define hipMemcpy               cudaMemcpy
#define hipMemcpyHostToDevice   cudaMemcpyHostToDevice
#define hipMemcpyDeviceToHost   cudaMemcpyDeviceToHost
#define hipMemcpyDeviceToDevice cudaMemcpyDeviceToDevice
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
#define hipDeviceProp_t         cudaDeviceProp
#define hipError_t              cudaError_t
#define hipSuccess              cudaSuccess
#else
#include <hip/hip_runtime.h>
#endif

#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <cmath>
#include <vector>

// ---------------------------------------------------------------------------
// Error-checking macro
// ---------------------------------------------------------------------------

#define HIP_CHECK(cmd)                                                          \
    do {                                                                       \
        hipError_t _e = (cmd);                                                  \
        if (_e != hipSuccess) {                                                 \
            fprintf(stderr, "HIP error %s at %s:%d\n",                         \
                    hipGetErrorString(_e), __FILE__, __LINE__);                 \
            exit(1);                                                           \
        }                                                                      \
    } while (0)

// ---------------------------------------------------------------------------
// Kernel: Sparse Matrix-Vector Multiply (CSR format)
// ---------------------------------------------------------------------------

__global__ void spmv_kernel(
    const int*   __restrict__ row_ptr,
    const int*   __restrict__ col_idx,
    const float* __restrict__ values,
    const float* __restrict__ x,
    float*       __restrict__ y,
    int N)
{
    int row = blockIdx.x * blockDim.x + threadIdx.x;
    if (row >= N) return;

    float sum = 0.0f;
    int start = row_ptr[row];
    int end   = row_ptr[row + 1];
    for (int j = start; j < end; ++j) {
        sum += values[j] * x[col_idx[j]];
    }
    y[row] = sum;
}

// ---------------------------------------------------------------------------
// Kernel: Dot product using block-level reduction
// ---------------------------------------------------------------------------

__global__ void dot_product_kernel(
    const float* __restrict__ a,
    const float* __restrict__ b,
    float*       __restrict__ partial,
    int N)
{
    extern __shared__ float sdata[];
    int tid = threadIdx.x;
    int idx = blockIdx.x * blockDim.x + threadIdx.x;

    float val = 0.0f;
    if (idx < N) val = a[idx] * b[idx];
    sdata[tid] = val;
    __syncthreads();

    for (int s = blockDim.x / 2; s > 0; s >>= 1) {
        if (tid < s) sdata[tid] += sdata[tid + s];
        __syncthreads();
    }

    if (tid == 0) partial[blockIdx.x] = sdata[0];
}

// ---------------------------------------------------------------------------
// Kernel: AXPY  y = alpha * x + y
// ---------------------------------------------------------------------------

__global__ void axpy_kernel(
    float        alpha,
    const float* __restrict__ x,
    float*       __restrict__ y,
    int N)
{
    int idx = blockIdx.x * blockDim.x + threadIdx.x;
    if (idx < N) {
        y[idx] = alpha * x[idx] + y[idx];
    }
}

// ---------------------------------------------------------------------------
// Kernel: Scale  y = alpha * x
// ---------------------------------------------------------------------------

__global__ void scale_kernel(
    float        alpha,
    const float* __restrict__ x,
    float*       __restrict__ y,
    int N)
{
    int idx = blockIdx.x * blockDim.x + threadIdx.x;
    if (idx < N) {
        y[idx] = alpha * x[idx];
    }
}

// ---------------------------------------------------------------------------
// Host helper: finish dot product
// ---------------------------------------------------------------------------

static float finish_dot(float* d_partial, int numBlocks) {
    std::vector<float> h_partial(numBlocks);
    HIP_CHECK(hipMemcpy(h_partial.data(), d_partial,
                          numBlocks * sizeof(float), hipMemcpyDeviceToHost));
    float sum = 0.0f;
    for (int i = 0; i < numBlocks; ++i) sum += h_partial[i];
    return sum;
}

// ---------------------------------------------------------------------------
// Simple PRNG
// ---------------------------------------------------------------------------

static inline unsigned int lcg_rand(unsigned int* state) {
    *state = *state * 1664525u + 1013904223u;
    return *state;
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------

int main(int argc, char** argv) {
    setvbuf(stdout, NULL, _IONBF, 0);

    int N              = 50000;
    int block_size     = 256;
    int max_cg_iters   = 25;
    int nonzeros_per_row = 7;
    int iterations     = 5;

    const char* env_val;
    env_val = getenv("BENCH_PARAM_matrix_size");
    if (env_val) N = atoi(env_val);
    env_val = getenv("BENCH_PARAM_block_size");
    if (env_val) block_size = atoi(env_val);
    env_val = getenv("BENCH_PARAM_max_cg_iterations");
    if (env_val) max_cg_iters = atoi(env_val);
    env_val = getenv("BENCH_PARAM_nonzeros_per_row");
    if (env_val) nonzeros_per_row = atoi(env_val);
    env_val = getenv("BENCH_PARAM_iterations");
    if (env_val) iterations = atoi(env_val);
    int num_warmup = 5;

    for (int i = 1; i < argc - 1; ++i) {
        if (strcmp(argv[i], "--size") == 0) N = atoi(argv[i+1]);
        if (strcmp(argv[i], "--iterations") == 0) iterations = atoi(argv[i+1]);
    }

    int iters = iterations;

    int device_id = 0;
    HIP_CHECK(hipGetDevice(&device_id));
    hipDeviceProp_t prop;
    HIP_CHECK(hipGetDeviceProperties(&prop, device_id));
    fprintf(stderr, "Device: %s (id=%d)\n", prop.name, device_id);
    fprintf(stderr, "NPB CG  |  Matrix: %d x %d  |  NNZ/row: %d  |  "
            "CG iters: %d  |  Timed runs: 5 warmup + %d timed\n\n",
            N, N, nonzeros_per_row, max_cg_iters, iters);

    // Generate synthetic sparse matrix in CSR format
    int nnz_per_row = nonzeros_per_row;
    long long total_nnz = (long long)N * nnz_per_row;

    std::vector<int>   h_row_ptr(N + 1);
    std::vector<int>   h_col_idx(total_nnz);
    std::vector<float> h_values(total_nnz);
    std::vector<float> h_x(N);
    std::vector<float> h_b(N);

    unsigned int seed = 42;

    for (int i = 0; i < N; ++i) {
        h_row_ptr[i] = i * nnz_per_row;
    }
    h_row_ptr[N] = (int)total_nnz;

    for (int i = 0; i < N; ++i) {
        int base = i * nnz_per_row;
        h_col_idx[base] = i;
        h_values[base]  = (float)(nnz_per_row + 1);
        for (int k = 1; k < nnz_per_row; ++k) {
            int col;
            do { col = lcg_rand(&seed) % N; } while (col == i);
            h_col_idx[base + k] = col;
            h_values[base + k]  = -1.0f;
        }
    }

    for (int i = 0; i < N; ++i) {
        h_x[i] = 0.0f;
        h_b[i] = 1.0f;
    }

    // Device allocations
    int *d_row_ptr, *d_col_idx;
    float *d_values, *d_x, *d_b;
    float *d_r, *d_p, *d_Ap;
    float *d_partial;

    size_t ptr_bytes = (N + 1) * sizeof(int);
    size_t idx_bytes = total_nnz * sizeof(int);
    size_t val_bytes = total_nnz * sizeof(float);
    size_t vec_bytes = N * sizeof(float);

    int gridSize  = (N + block_size - 1) / block_size;
    int numBlocks = gridSize;

    HIP_CHECK(hipMalloc(&d_row_ptr, ptr_bytes));
    HIP_CHECK(hipMalloc(&d_col_idx, idx_bytes));
    HIP_CHECK(hipMalloc(&d_values,  val_bytes));
    HIP_CHECK(hipMalloc(&d_x,       vec_bytes));
    HIP_CHECK(hipMalloc(&d_b,       vec_bytes));
    HIP_CHECK(hipMalloc(&d_r,       vec_bytes));
    HIP_CHECK(hipMalloc(&d_p,       vec_bytes));
    HIP_CHECK(hipMalloc(&d_Ap,      vec_bytes));
    HIP_CHECK(hipMalloc(&d_partial, numBlocks * sizeof(float)));

    HIP_CHECK(hipMemcpy(d_row_ptr, h_row_ptr.data(), ptr_bytes, hipMemcpyHostToDevice));
    HIP_CHECK(hipMemcpy(d_col_idx, h_col_idx.data(), idx_bytes, hipMemcpyHostToDevice));
    HIP_CHECK(hipMemcpy(d_values,  h_values.data(),  val_bytes, hipMemcpyHostToDevice));

    int smem = block_size * sizeof(float);

    // Per-kernel timing events (used only when accumulators are supplied)
    hipEvent_t kStart, kStop;
    HIP_CHECK(hipEventCreate(&kStart));
    HIP_CHECK(hipEventCreate(&kStop));

    // run_cg accumulates per-kernel GPU time into the supplied accumulators.
    // Passing nullptr (warmup / verification) runs the kernels untimed.
    auto run_cg = [&](double* spmv_acc, double* dot_acc, double* axpy_acc) {
        HIP_CHECK(hipMemcpy(d_x, h_x.data(), vec_bytes, hipMemcpyHostToDevice));
        HIP_CHECK(hipMemcpy(d_b, h_b.data(), vec_bytes, hipMemcpyHostToDevice));
        HIP_CHECK(hipMemcpy(d_r, d_b, vec_bytes, hipMemcpyDeviceToDevice));
        HIP_CHECK(hipMemcpy(d_p, d_r, vec_bytes, hipMemcpyDeviceToDevice));

        if (dot_acc) {
            HIP_CHECK(hipEventRecord(kStart, 0));
            dot_product_kernel<<<numBlocks, block_size, smem>>>(d_r, d_r, d_partial, N);
            HIP_CHECK(hipEventRecord(kStop, 0));
            HIP_CHECK(hipEventSynchronize(kStop));
            float _ms = 0.0f; HIP_CHECK(hipEventElapsedTime(&_ms, kStart, kStop));
            *dot_acc += (double)_ms;
        } else {
            dot_product_kernel<<<numBlocks, block_size, smem>>>(d_r, d_r, d_partial, N);
        }
        float rr = finish_dot(d_partial, numBlocks);

        for (int iter = 0; iter < max_cg_iters; ++iter) {
            if (spmv_acc) {
                HIP_CHECK(hipEventRecord(kStart, 0));
                spmv_kernel<<<gridSize, block_size>>>(d_row_ptr, d_col_idx, d_values, d_p, d_Ap, N);
                HIP_CHECK(hipEventRecord(kStop, 0));
                HIP_CHECK(hipEventSynchronize(kStop));
                float _ms = 0.0f; HIP_CHECK(hipEventElapsedTime(&_ms, kStart, kStop));
                *spmv_acc += (double)_ms;
            } else {
                spmv_kernel<<<gridSize, block_size>>>(d_row_ptr, d_col_idx, d_values, d_p, d_Ap, N);
            }

            if (dot_acc) {
                HIP_CHECK(hipEventRecord(kStart, 0));
                dot_product_kernel<<<numBlocks, block_size, smem>>>(d_p, d_Ap, d_partial, N);
                HIP_CHECK(hipEventRecord(kStop, 0));
                HIP_CHECK(hipEventSynchronize(kStop));
                float _ms = 0.0f; HIP_CHECK(hipEventElapsedTime(&_ms, kStart, kStop));
                *dot_acc += (double)_ms;
            } else {
                dot_product_kernel<<<numBlocks, block_size, smem>>>(d_p, d_Ap, d_partial, N);
            }
            float pAp = finish_dot(d_partial, numBlocks);
            float alpha = rr / fmaxf(pAp, 1e-20f);

            if (axpy_acc) {
                HIP_CHECK(hipEventRecord(kStart, 0));
                axpy_kernel<<<gridSize, block_size>>>(alpha, d_p, d_x, N);
                axpy_kernel<<<gridSize, block_size>>>(-alpha, d_Ap, d_r, N);
                HIP_CHECK(hipEventRecord(kStop, 0));
                HIP_CHECK(hipEventSynchronize(kStop));
                float _ms = 0.0f; HIP_CHECK(hipEventElapsedTime(&_ms, kStart, kStop));
                *axpy_acc += (double)_ms;
            } else {
                axpy_kernel<<<gridSize, block_size>>>(alpha, d_p, d_x, N);
                axpy_kernel<<<gridSize, block_size>>>(-alpha, d_Ap, d_r, N);
            }

            if (dot_acc) {
                HIP_CHECK(hipEventRecord(kStart, 0));
                dot_product_kernel<<<numBlocks, block_size, smem>>>(d_r, d_r, d_partial, N);
                HIP_CHECK(hipEventRecord(kStop, 0));
                HIP_CHECK(hipEventSynchronize(kStop));
                float _ms = 0.0f; HIP_CHECK(hipEventElapsedTime(&_ms, kStart, kStop));
                *dot_acc += (double)_ms;
            } else {
                dot_product_kernel<<<numBlocks, block_size, smem>>>(d_r, d_r, d_partial, N);
            }
            float rr_new = finish_dot(d_partial, numBlocks);

            float beta = rr_new / fmaxf(rr, 1e-20f);
            scale_kernel<<<gridSize, block_size>>>(beta, d_p, d_p, N);

            if (axpy_acc) {
                HIP_CHECK(hipEventRecord(kStart, 0));
                axpy_kernel<<<gridSize, block_size>>>(1.0f, d_r, d_p, N);
                HIP_CHECK(hipEventRecord(kStop, 0));
                HIP_CHECK(hipEventSynchronize(kStop));
                float _ms = 0.0f; HIP_CHECK(hipEventElapsedTime(&_ms, kStart, kStop));
                *axpy_acc += (double)_ms;
            } else {
                axpy_kernel<<<gridSize, block_size>>>(1.0f, d_r, d_p, N);
            }

            rr = rr_new;
        }
    };

    // Warmup
    for (int w = 0; w < num_warmup; ++w) {
        run_cg(nullptr, nullptr, nullptr);
        HIP_CHECK(hipDeviceSynchronize());
    }

    // Timed iterations — per-kernel accumulators summed across CG iterations
    double spmv_ms = 0.0, dot_ms = 0.0, axpy_ms = 0.0;
    for (int i = 0; i < iters; ++i) {
        run_cg(&spmv_ms, &dot_ms, &axpy_ms);
    }

    HIP_CHECK(hipEventDestroy(kStart));
    HIP_CHECK(hipEventDestroy(kStop));

    double total_ms = spmv_ms + dot_ms + axpy_ms;
    double avg_ms = total_ms / iters;

    long long spmv_bytes = ((long long)(N+1)*4 + total_nnz*4 + total_nnz*4 + (long long)N*4 + (long long)N*4);
    double spmv_bw_gb = (double)spmv_bytes * max_cg_iters / (avg_ms * 1e-3) / 1e9;

    printf("{\"type\":\"kernel\",\"name\":\"spmv_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"matrix_size\":%d,\"block_size\":%d,"
           "\"max_cg_iterations\":%d,\"nonzeros_per_row\":%d,\"iterations\":%d}}\n",
           spmv_ms, N, block_size, max_cg_iters, nonzeros_per_row, iters);

    printf("{\"type\":\"kernel\",\"name\":\"dot_product_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"matrix_size\":%d,\"block_size\":%d,"
           "\"max_cg_iterations\":%d,\"nonzeros_per_row\":%d,\"iterations\":%d}}\n",
           dot_ms, N, block_size, max_cg_iters, nonzeros_per_row, iters);

    printf("{\"type\":\"kernel\",\"name\":\"axpy_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"matrix_size\":%d,\"block_size\":%d,"
           "\"max_cg_iterations\":%d,\"nonzeros_per_row\":%d,\"iterations\":%d}}\n",
           axpy_ms, N, block_size, max_cg_iters, nonzeros_per_row, iters);

    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"avg_iter_ms\",\"value\":%.4f},"
           "{\"name\":\"spmv_bw_gb_s\",\"value\":%.2f}]}\n",
           total_ms, avg_ms, spmv_bw_gb);

    fprintf(stderr, "Average time: %.4f ms\n", avg_ms);
    fprintf(stderr, "SpMV bandwidth: %.2f GB/s\n", spmv_bw_gb);

    // Verification
    {
        run_cg(nullptr, nullptr, nullptr);
        HIP_CHECK(hipDeviceSynchronize());

        spmv_kernel<<<gridSize, block_size>>>(d_row_ptr, d_col_idx, d_values, d_x, d_Ap, N);
        HIP_CHECK(hipMemcpy(d_r, d_b, vec_bytes, hipMemcpyDeviceToDevice));
        axpy_kernel<<<gridSize, block_size>>>(-1.0f, d_Ap, d_r, N);

        dot_product_kernel<<<numBlocks, block_size, smem>>>(d_r, d_r, d_partial, N);
        float res_norm = sqrtf(finish_dot(d_partial, numBlocks));

        dot_product_kernel<<<numBlocks, block_size, smem>>>(d_b, d_b, d_partial, N);
        float b_norm = sqrtf(finish_dot(d_partial, numBlocks));

        float rel_res = res_norm / fmaxf(b_norm, 1e-20f);
        fprintf(stderr, "Residual ||b-Ax||/||b|| = %.6e\n", rel_res);

        if (rel_res < 1.0f)
            fprintf(stderr, "PASS\n");
        else
            fprintf(stderr, "FAIL: relative residual too large\n");
    }

    HIP_CHECK(hipFree(d_row_ptr));
    HIP_CHECK(hipFree(d_col_idx));
    HIP_CHECK(hipFree(d_values));
    HIP_CHECK(hipFree(d_x));
    HIP_CHECK(hipFree(d_b));
    HIP_CHECK(hipFree(d_r));
    HIP_CHECK(hipFree(d_p));
    HIP_CHECK(hipFree(d_Ap));
    HIP_CHECK(hipFree(d_partial));

    return 0;
}
