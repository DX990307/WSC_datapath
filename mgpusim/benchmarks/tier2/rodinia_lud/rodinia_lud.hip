// rodinia_lud.hip — Rodinia LUD benchmark (HIP, self-contained)
//
// Blocked LU decomposition of a dense NxN matrix (no pivoting).
// Derived from the Rodinia LUD benchmark suite.
//
// Three GPU kernels:
//   lud_diagonal   — in-place LU factor of the diagonal 16×16 block
//   lud_perimeter  — forward/back-solve for row/column perimeter blocks
//   lud_internal   — Schur-complement update for interior blocks
//
// Default: N=512, BSIZE=16  (N must be divisible by BSIZE)
//
// Usage:
//   ./rodinia_lud [--size N]
//
// Output (stdout): CSV — lud,<N>,<time_ms>,<GFLOPS>
// Output (stderr): device info, per-iteration timing, verification result

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
#include <vector>

// ---------------------------------------------------------------------------
// Constants and error checking
// ---------------------------------------------------------------------------

#define BSIZE 16

#define HIP_CHECK(cmd)                                                        \
    do {                                                                      \
        hipError_t _e = (cmd);                                                \
        if (_e != hipSuccess) {                                               \
            fprintf(stderr, "HIP error %s at %s:%d\n",                       \
                    hipGetErrorString(_e), __FILE__, __LINE__);               \
            exit(1);                                                          \
        }                                                                     \
    } while (0)

// ---------------------------------------------------------------------------
// Kernels
// ---------------------------------------------------------------------------

// Factor the diagonal block at matrix position (offset*BSIZE, offset*BSIZE).
// Launch: <<<dim3(1), dim3(BSIZE,BSIZE)>>>
__global__ void lud_diagonal(float *a, int n, int offset)
{
    __shared__ float s[BSIZE][BSIZE];
    int tx = threadIdx.x, ty = threadIdx.y;

    s[ty][tx] = a[(offset * BSIZE + ty) * n + (offset * BSIZE + tx)];
    __syncthreads();

    for (int k = 0; k < BSIZE - 1; k++) {
        // Normalise column k below pivot  → L[i][k] = a[i][k] / a[k][k]
        if (ty > k && tx == k)
            s[ty][k] /= s[k][k];
        __syncthreads();
        // Schur complement on trailing sub-block
        if (ty > k && tx > k)
            s[ty][tx] -= s[ty][k] * s[k][tx];
        __syncthreads();
    }

    a[(offset * BSIZE + ty) * n + (offset * BSIZE + tx)] = s[ty][tx];
}

// Update perimeter blocks adjacent to the diagonal at step `offset`.
// First half of blocks (blockIdx.x < half): row panel (right of diagonal)
//   → forward-solve  L_kk * U_kj = A_kj
// Second half (blockIdx.x >= half):          column panel (below diagonal)
//   → back-solve     L_ik * U_kk = A_ik
// Launch: <<<dim3(2*half), dim3(BSIZE,BSIZE)>>>  where half = num_blocks-offset-1
__global__ void lud_perimeter(float *a, int n, int offset)
{
    __shared__ float dia [BSIZE][BSIZE];
    __shared__ float peri[BSIZE][BSIZE];

    int tx = threadIdx.x, ty = threadIdx.y;
    int half   = gridDim.x / 2;
    bool is_row = (blockIdx.x < half);
    int  idx    = is_row ? blockIdx.x : (blockIdx.x - half);
    int  blk    = offset + idx + 1;   // target block index

    // All threads in this block load the same diagonal block
    dia[ty][tx] = a[(offset * BSIZE + ty) * n + (offset * BSIZE + tx)];

    if (is_row)
        peri[ty][tx] = a[(offset * BSIZE + ty) * n + (blk * BSIZE + tx)];
    else
        peri[ty][tx] = a[(blk   * BSIZE + ty) * n + (offset * BSIZE + tx)];
    __syncthreads();

    // is_row is uniform across the entire thread block (depends only on blockIdx.x),
    // so __syncthreads() inside these branches is valid.
    if (is_row) {
        // Forward substitution: unit-lower-triangular L * peri = peri
        for (int k = 0; k < BSIZE - 1; k++) {
            if (ty > k)
                peri[ty][tx] -= dia[ty][k] * peri[k][tx];
            __syncthreads();
        }
        a[(offset * BSIZE + ty) * n + (blk * BSIZE + tx)] = peri[ty][tx];
    } else {
        // Back substitution: peri * U = peri  → find L column
        for (int k = 0; k < BSIZE; k++) {
            if (tx == k)
                peri[ty][k] /= dia[k][k];
            __syncthreads();
            if (tx > k)
                peri[ty][tx] -= peri[ty][k] * dia[k][tx];
            __syncthreads();
        }
        a[(blk * BSIZE + ty) * n + (offset * BSIZE + tx)] = peri[ty][tx];
    }
}

// Schur-complement update for interior block (row_blk, col_blk).
// A[row_blk][col_blk] -= L[row_blk][offset] * U[offset][col_blk]
// Launch: <<<dim3(intern, intern), dim3(BSIZE,BSIZE)>>>  where intern = num_blocks-offset-1
__global__ void lud_internal(float *a, int n, int offset)
{
    __shared__ float peri_row[BSIZE][BSIZE];  // U[offset, col_blk]
    __shared__ float peri_col[BSIZE][BSIZE];  // L[row_blk, offset]

    int tx = threadIdx.x, ty = threadIdx.y;
    int col_blk = offset + blockIdx.x + 1;
    int row_blk = offset + blockIdx.y + 1;

    peri_row[ty][tx] = a[(offset  * BSIZE + ty) * n + (col_blk * BSIZE + tx)];
    peri_col[ty][tx] = a[(row_blk * BSIZE + ty) * n + (offset  * BSIZE + tx)];
    __syncthreads();

    float sum = 0.0f;
    for (int k = 0; k < BSIZE; k++)
        sum += peri_col[ty][k] * peri_row[k][tx];
    a[(row_blk * BSIZE + ty) * n + (col_blk * BSIZE + tx)] -= sum;
}

// ---------------------------------------------------------------------------
// Host helpers
// ---------------------------------------------------------------------------

// Diagonally-dominant random matrix → LU without pivoting is numerically stable
static void init_matrix(float *a, int n)
{
    srand(42);
    for (int i = 0; i < n; i++) {
        float row_sum = 0.0f;
        for (int j = 0; j < n; j++) {
            a[i * n + j] = (float)(rand() % 10 + 1) * 0.1f;
            if (i != j) row_sum += fabsf(a[i * n + j]);
        }
        a[i * n + i] = row_sum + 1.0f;  // ensure diagonal dominance
    }
}

// Verify: reconstruct A = L*U from the in-place result and compare with original.
// In-place storage: lower-triangular (i>j) → L (unit diagonal implicit)
//                   upper-triangular (i<=j) → U
static bool verify(const float *lu, const float *a_orig, int n)
{
    double err = 0.0, norm_a = 0.0;
    for (int i = 0; i < n; i++) {
        for (int j = 0; j < n; j++) {
            double sum = 0.0;
            int lim = (i < j) ? i : j;   // min(i, j)
            for (int k = 0; k <= lim; k++) {
                double l = (k == i) ? 1.0 : (double)lu[i * n + k];
                double u = (double)lu[k * n + j];
                sum += l * u;
            }
            double diff = (double)a_orig[i * n + j] - sum;
            err    += diff * diff;
            norm_a += (double)a_orig[i * n + j] * (double)a_orig[i * n + j];
        }
    }
    double rel = sqrt(err / norm_a);
    fprintf(stderr, "Verification: ||A - LU||_F / ||A||_F = %.6e  %s\n",
            rel, (rel < 1e-4) ? "(PASS)" : "(FAIL)");
    return rel < 1e-4;
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------
int main(int argc, char **argv)
{
    setvbuf(stdout, NULL, _IONBF, 0);

    // Defaults
    int N     = 512;
    int block_size_param = 256;
    const char* verify_mode = "true";

    // Command-line fallback
    for (int i = 1; i < argc - 1; i++) {
        if (strcmp(argv[i], "--size")       == 0) N     = atoi(argv[i + 1]);
    }

    // Environment variable overrides
    const char* env_val;
    env_val = getenv("BENCH_PARAM_size");
    if (env_val) N = atoi(env_val);
    env_val = getenv("BENCH_PARAM_block_size");
    if (env_val) block_size_param = atoi(env_val);
    env_val = getenv("BENCH_PARAM_verify");
    if (env_val) verify_mode = env_val;
    int num_warmup = 5;
    if (N % BSIZE != 0) {
        fprintf(stderr, "Error: N=%d must be divisible by BSIZE=%d\n", N, BSIZE);
        return 1;
    }

    int    num_blocks = N / BSIZE;
    size_t bytes      = (size_t)N * N * sizeof(float);

    // Device info
    int dev = 0;
    HIP_CHECK(hipGetDevice(&dev));
    hipDeviceProp_t prop;
    HIP_CHECK(hipGetDeviceProperties(&prop, dev));
    fprintf(stderr, "Device: %s\n", prop.name);
    fprintf(stderr, "Matrix: %dx%d  BSIZE=%d  num_blocks=%d\n\n",
            N, N, BSIZE, num_blocks);

    // Host allocations
    float *h_a      = (float*)malloc(bytes);
    float *h_a_orig = (float*)malloc(bytes);
    float *h_lu     = (float*)malloc(bytes);

    init_matrix(h_a, N);
    memcpy(h_a_orig, h_a, bytes);

    // Device allocation
    float *d_a;
    HIP_CHECK(hipMalloc(&d_a, bytes));

    // Events for timing
    hipEvent_t ev_start, ev_stop;
    HIP_CHECK(hipEventCreate(&ev_start));
    HIP_CHECK(hipEventCreate(&ev_stop));

    dim3 block(BSIZE, BSIZE);

    // Per-kernel timing accumulators (filled only when `timed` is true).
    double t_diagonal = 0.0, t_perimeter = 0.0, t_internal = 0.0;

    auto run_lud = [&](bool timed) {
        for (int k = 0; k < num_blocks; k++) {
            if (timed) {
                HIP_CHECK(hipEventRecord(ev_start));
                hipLaunchKernelGGL(lud_diagonal, dim3(1), block, 0, 0, d_a, N, k);
                HIP_CHECK(hipEventRecord(ev_stop));
                HIP_CHECK(hipEventSynchronize(ev_stop));
                float ms = 0.0f;
                HIP_CHECK(hipEventElapsedTime(&ms, ev_start, ev_stop));
                t_diagonal += (double)ms;
            } else {
                hipLaunchKernelGGL(lud_diagonal, dim3(1), block, 0, 0, d_a, N, k);
            }
            int peri  = 2 * (num_blocks - k - 1);
            int intern =     num_blocks - k - 1;
            if (peri > 0) {
                if (timed) {
                    HIP_CHECK(hipEventRecord(ev_start));
                    hipLaunchKernelGGL(lud_perimeter, dim3(peri),         block, 0, 0, d_a, N, k);
                    HIP_CHECK(hipEventRecord(ev_stop));
                    HIP_CHECK(hipEventSynchronize(ev_stop));
                    float ms = 0.0f;
                    HIP_CHECK(hipEventElapsedTime(&ms, ev_start, ev_stop));
                    t_perimeter += (double)ms;

                    HIP_CHECK(hipEventRecord(ev_start));
                    hipLaunchKernelGGL(lud_internal,  dim3(intern,intern), block, 0, 0, d_a, N, k);
                    HIP_CHECK(hipEventRecord(ev_stop));
                    HIP_CHECK(hipEventSynchronize(ev_stop));
                    ms = 0.0f;
                    HIP_CHECK(hipEventElapsedTime(&ms, ev_start, ev_stop));
                    t_internal += (double)ms;
                } else {
                    hipLaunchKernelGGL(lud_perimeter, dim3(peri),         block, 0, 0, d_a, N, k);
                    hipLaunchKernelGGL(lud_internal,  dim3(intern,intern), block, 0, 0, d_a, N, k);
                }
            }
        }
    };

    // Warmup
    for (int w = 0; w < num_warmup; ++w) {
        HIP_CHECK(hipMemcpy(d_a, h_a, bytes, hipMemcpyHostToDevice));
        run_lud(false);
        HIP_CHECK(hipDeviceSynchronize());
    }
    // Correctness verification (one extra run)
    HIP_CHECK(hipMemcpy(d_a, h_a, bytes, hipMemcpyHostToDevice));
    run_lud(false);
    HIP_CHECK(hipDeviceSynchronize());
    HIP_CHECK(hipMemcpy(h_lu, d_a, bytes, hipMemcpyDeviceToHost));

    verify(h_lu, h_a_orig, N);

    // Timed iterations — each kernel timed separately via its own event pair.
    for (int it = 0; it < 1; it++) {
        HIP_CHECK(hipMemcpy(d_a, h_a, bytes, hipMemcpyHostToDevice));
        run_lud(true);
    }

    // Statistics
    double sum_ms = t_diagonal + t_perimeter + t_internal;
    double avg_ms = sum_ms / 1;
    double gflops = (2.0 / 3.0) * (double)N * (double)N * (double)N
                    / (avg_ms * 1e-3) / 1e9;

    fprintf(stderr, "\nAverage: %.4f ms  GFLOPS: %.2f"
                    "  (diagonal=%.4f perimeter=%.4f internal=%.4f)\n",
            avg_ms, gflops, t_diagonal, t_perimeter, t_internal);

    // JSON-lines kernel events
    printf("{\"type\":\"kernel\",\"name\":\"lud_diagonal\",\"time_ms\":%.6f,"
           "\"params\":{\"size\":%d,\"block_size\":%d,\"verify\":\"%s\"}}\n",
           t_diagonal, N, block_size_param, verify_mode);

    printf("{\"type\":\"kernel\",\"name\":\"lud_perimeter\",\"time_ms\":%.6f,"
           "\"params\":{\"size\":%d,\"block_size\":%d,\"verify\":\"%s\"}}\n",
           t_perimeter, N, block_size_param, verify_mode);

    printf("{\"type\":\"kernel\",\"name\":\"lud_internal\",\"time_ms\":%.6f,"
           "\"params\":{\"size\":%d,\"block_size\":%d,\"verify\":\"%s\"}}\n",
           t_internal, N, block_size_param, verify_mode);

    // JSON-lines summary event
    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"gflops\",\"value\":%.2f}]}\n",
           sum_ms, gflops);

    // Cleanup
    HIP_CHECK(hipFree(d_a));
    HIP_CHECK(hipEventDestroy(ev_start));
    HIP_CHECK(hipEventDestroy(ev_stop));
    free(h_a);
    free(h_a_orig);
    free(h_lu);

    return 0;
}
