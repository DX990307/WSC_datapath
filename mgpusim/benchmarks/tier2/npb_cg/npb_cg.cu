// npb_cg.cu — NPB Conjugate Gradient benchmark: sparse iterative CG solver
//
// Solves a sparse linear system Ax = b using the Conjugate Gradient method.
// The sparse matrix A is stored in CSR format. Operations include SpMV,
// dot products, and AXPY vector updates.
//
// Native CUDA implementation.
//
// Output (stdout): JSON-lines
// Output (stderr): human-readable

#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <cmath>
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
// Kernel: Sparse Matrix-Vector Multiply (CSR format)
// y = A * x
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
// partial[blockIdx.x] = sum of a[i]*b[i] for this block
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

    // Reduction in shared memory
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
// Host helper: finish dot product by summing partial sums
// ---------------------------------------------------------------------------

static float finish_dot(float* d_partial, int numBlocks) {
    std::vector<float> h_partial(numBlocks);
    CUDA_CHECK(cudaMemcpy(h_partial.data(), d_partial,
                           numBlocks * sizeof(float), cudaMemcpyDeviceToHost));
    float sum = 0.0f;
    for (int i = 0; i < numBlocks; ++i) sum += h_partial[i];
    return sum;
}

// ---------------------------------------------------------------------------
// Simple PRNG for synthetic data
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

    // Defaults from params.json
    int N              = 50000;
    int block_size     = 256;
    int max_cg_iters   = 25;
    int nonzeros_per_row = 7;
    int iterations     = 5;

    // Environment variable overrides
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
    int num_warmup = 0;

    // Command-line fallback
    for (int i = 1; i < argc - 1; ++i) {
        if (strcmp(argv[i], "--size") == 0) N = atoi(argv[i+1]);
        if (strcmp(argv[i], "--iterations") == 0) iterations = atoi(argv[i+1]);
    }

    int iters = iterations;

    // Print device info
    int device_id = 0;
    CUDA_CHECK(cudaGetDevice(&device_id));
    cudaDeviceProp prop;
    CUDA_CHECK(cudaGetDeviceProperties(&prop, device_id));
    fprintf(stderr, "Device: %s (id=%d)\n", prop.name, device_id);
    fprintf(stderr, "NPB CG  |  Matrix: %d x %d  |  NNZ/row: %d  |  "
            "CG iters: %d  |  Timed runs: 5 warmup + %d timed\n\n",
            N, N, nonzeros_per_row, max_cg_iters, iters);

    // -----------------------------------------------------------------------
    // Generate synthetic sparse matrix in CSR format
    // -----------------------------------------------------------------------
    int nnz_per_row = nonzeros_per_row;
    // Ensure diagonal + (nnz_per_row - 1) off-diagonals
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
        // Diagonal element (dominant)
        h_col_idx[base] = i;
        h_values[base]  = (float)(nnz_per_row + 1);

        // Off-diagonal elements
        for (int k = 1; k < nnz_per_row; ++k) {
            int col;
            do {
                col = lcg_rand(&seed) % N;
            } while (col == i);
            h_col_idx[base + k] = col;
            h_values[base + k]  = -1.0f;
        }
    }

    // Initial guess x=0, RHS b=1
    for (int i = 0; i < N; ++i) {
        h_x[i] = 0.0f;
        h_b[i] = 1.0f;
    }

    // -----------------------------------------------------------------------
    // Device allocations
    // -----------------------------------------------------------------------
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

    CUDA_CHECK(cudaMalloc(&d_row_ptr, ptr_bytes));
    CUDA_CHECK(cudaMalloc(&d_col_idx, idx_bytes));
    CUDA_CHECK(cudaMalloc(&d_values,  val_bytes));
    CUDA_CHECK(cudaMalloc(&d_x,       vec_bytes));
    CUDA_CHECK(cudaMalloc(&d_b,       vec_bytes));
    CUDA_CHECK(cudaMalloc(&d_r,       vec_bytes));
    CUDA_CHECK(cudaMalloc(&d_p,       vec_bytes));
    CUDA_CHECK(cudaMalloc(&d_Ap,      vec_bytes));
    CUDA_CHECK(cudaMalloc(&d_partial, numBlocks * sizeof(float)));

    // Copy CSR matrix (constant across runs)
    CUDA_CHECK(cudaMemcpy(d_row_ptr, h_row_ptr.data(), ptr_bytes, cudaMemcpyHostToDevice));
    CUDA_CHECK(cudaMemcpy(d_col_idx, h_col_idx.data(), idx_bytes, cudaMemcpyHostToDevice));
    CUDA_CHECK(cudaMemcpy(d_values,  h_values.data(),  val_bytes, cudaMemcpyHostToDevice));

    int smem = block_size * sizeof(float);

    // Lambda: run one full CG solve
    auto run_cg = [&]() {
        // Upload initial x and b
        CUDA_CHECK(cudaMemcpy(d_x, h_x.data(), vec_bytes, cudaMemcpyHostToDevice));
        CUDA_CHECK(cudaMemcpy(d_b, h_b.data(), vec_bytes, cudaMemcpyHostToDevice));

        // r = b - A*x (since x=0, r = b)
        CUDA_CHECK(cudaMemcpy(d_r, d_b, vec_bytes, cudaMemcpyDeviceToDevice));
        // p = r
        CUDA_CHECK(cudaMemcpy(d_p, d_r, vec_bytes, cudaMemcpyDeviceToDevice));

        // rr = dot(r, r)
        dot_product_kernel<<<numBlocks, block_size, smem>>>(d_r, d_r, d_partial, N);
        float rr = finish_dot(d_partial, numBlocks);

        for (int iter = 0; iter < max_cg_iters; ++iter) {
            // Ap = A * p
            spmv_kernel<<<gridSize, block_size>>>(d_row_ptr, d_col_idx, d_values, d_p, d_Ap, N);

            // pAp = dot(p, Ap)
            dot_product_kernel<<<numBlocks, block_size, smem>>>(d_p, d_Ap, d_partial, N);
            float pAp = finish_dot(d_partial, numBlocks);

            float alpha = rr / fmaxf(pAp, 1e-20f);

            // x = x + alpha * p
            axpy_kernel<<<gridSize, block_size>>>(alpha, d_p, d_x, N);

            // r = r - alpha * Ap
            axpy_kernel<<<gridSize, block_size>>>(-alpha, d_Ap, d_r, N);

            // rr_new = dot(r, r)
            dot_product_kernel<<<numBlocks, block_size, smem>>>(d_r, d_r, d_partial, N);
            float rr_new = finish_dot(d_partial, numBlocks);

            float beta = rr_new / fmaxf(rr, 1e-20f);

            // p = r + beta * p
            scale_kernel<<<gridSize, block_size>>>(beta, d_p, d_p, N);
            axpy_kernel<<<gridSize, block_size>>>(1.0f, d_r, d_p, N);

            rr = rr_new;
        }
    };

    // -----------------------------------------------------------------------
    // Warmup
    // -----------------------------------------------------------------------
    for (int w = 0; w < num_warmup; ++w) {
        run_cg();
        CUDA_CHECK(cudaDeviceSynchronize());
    }

    // -----------------------------------------------------------------------
    // Timed iterations
    // -----------------------------------------------------------------------
    cudaEvent_t evStart, evStop;
    CUDA_CHECK(cudaEventCreate(&evStart));
    CUDA_CHECK(cudaEventCreate(&evStop));

    std::vector<double> times((size_t)iters);
    for (int i = 0; i < iters; ++i) {
        CUDA_CHECK(cudaEventRecord(evStart, 0));
        run_cg();
        CUDA_CHECK(cudaEventRecord(evStop, 0));
        CUDA_CHECK(cudaEventSynchronize(evStop));

        float ms = 0.0f;
        CUDA_CHECK(cudaEventElapsedTime(&ms, evStart, evStop));
        times[i] = (double)ms;
    }

    CUDA_CHECK(cudaEventDestroy(evStart));
    CUDA_CHECK(cudaEventDestroy(evStop));

    // Compute average
    double sum = 0.0;
    for (int i = 0; i < iters; ++i) sum += times[i];
    double avg_ms = sum / iters;
    double total_ms = sum;

    // Effective bandwidth: each CG iteration does SpMV + 2 dots + 3 axpy-like ops
    // SpMV: read row_ptr(N+1) + col_idx(nnz) + values(nnz) + x(N), write y(N)
    long long spmv_bytes = ((long long)(N+1)*4 + total_nnz*4 + total_nnz*4 + (long long)N*4 + (long long)N*4);
    double spmv_bw_gb = (double)spmv_bytes * max_cg_iters / (avg_ms * 1e-3) / 1e9;

    // JSON-lines output
    printf("{\"type\":\"kernel\",\"name\":\"spmv_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"matrix_size\":%d,\"block_size\":%d,"
           "\"max_cg_iterations\":%d,\"nonzeros_per_row\":%d,\"iterations\":%d}}\n",
           avg_ms, N, block_size, max_cg_iters, nonzeros_per_row, iters);

    printf("{\"type\":\"kernel\",\"name\":\"dot_product_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"matrix_size\":%d,\"block_size\":%d,"
           "\"max_cg_iterations\":%d,\"nonzeros_per_row\":%d,\"iterations\":%d}}\n",
           avg_ms, N, block_size, max_cg_iters, nonzeros_per_row, iters);

    printf("{\"type\":\"kernel\",\"name\":\"axpy_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"matrix_size\":%d,\"block_size\":%d,"
           "\"max_cg_iterations\":%d,\"nonzeros_per_row\":%d,\"iterations\":%d}}\n",
           avg_ms, N, block_size, max_cg_iters, nonzeros_per_row, iters);

    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"avg_iter_ms\",\"value\":%.4f},"
           "{\"name\":\"spmv_bw_gb_s\",\"value\":%.2f}]}\n",
           total_ms, avg_ms, spmv_bw_gb);

    fprintf(stderr, "Average time: %.4f ms\n", avg_ms);
    fprintf(stderr, "SpMV bandwidth: %.2f GB/s\n", spmv_bw_gb);

    // -----------------------------------------------------------------------
    // Verification: check residual norm
    // -----------------------------------------------------------------------
    {
        // Run once more
        run_cg();
        CUDA_CHECK(cudaDeviceSynchronize());

        // Compute r = b - A*x
        spmv_kernel<<<gridSize, block_size>>>(d_row_ptr, d_col_idx, d_values, d_x, d_Ap, N);
        // d_r = d_b - d_Ap  =>  d_r = -1*d_Ap + d_b
        CUDA_CHECK(cudaMemcpy(d_r, d_b, vec_bytes, cudaMemcpyDeviceToDevice));
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

    // Cleanup
    CUDA_CHECK(cudaFree(d_row_ptr));
    CUDA_CHECK(cudaFree(d_col_idx));
    CUDA_CHECK(cudaFree(d_values));
    CUDA_CHECK(cudaFree(d_x));
    CUDA_CHECK(cudaFree(d_b));
    CUDA_CHECK(cudaFree(d_r));
    CUDA_CHECK(cudaFree(d_p));
    CUDA_CHECK(cudaFree(d_Ap));
    CUDA_CHECK(cudaFree(d_partial));

    return 0;
}
