// polybench_gramschmidt.cu — PolyBench Gram-Schmidt orthogonalization (CUDA, self-contained)
//
// Computes the QR factorization via Gram-Schmidt orthogonalization:
//   A = Q * R
// where A is M×N, Q is M×N (orthonormal columns), R is N×N (upper triangular).
// Derived from the PolyBench/GPU benchmark suite.
//
// GPU kernels (executed in a loop over k = 0 .. N-1):
//   gram_norm         : atomic-reduction of ||A[:,k]||^2 into nrm_buf
//   gram_norm_finish  : nrm_buf = sqrt(nrm_buf); R[k,k] = nrm_buf  (1 thread)
//   gram_normalize    : Q[:,k] = A[:,k] / nrm_buf                   (M threads)
//   gram_project      : for j>k: R[k,j]=dot(Q[:,k],A[:,j]); A[:,j]-=R[k,j]*Q[:,k]
//                       one thread per column j > k
//
// Default: M = N = 512
// Performance metric: GB/s = 3*M*N*sizeof(float) / time_s / 1e9
//
// Native CUDA implementation.
//
// Usage:
//   ./polybench_gramschmidt [--m M] [--n N] [--passes P]
//
//   --m M       Number of rows    (default: 512)
//   --n N       Number of columns (default: 512)
//   --passes P  Timed passes      (default: 3)
//
// Output (stdout): CSV — gramschmidt,<M>,<N>,<time_ms>,<GBs>
// Output (stderr): device info, timing details, orthogonality check

#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <cmath>
#include <cfloat>
#include <vector>
#include <cuda_runtime.h>

// ---------------------------------------------------------------------------
// Inlined utilities
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
// Kernel: accumulate ||A[:,k]||^2 via atomic add
// ---------------------------------------------------------------------------
__global__ void gram_norm(const float* __restrict__ A,
                           float* __restrict__ nrm_buf,
                           int M, int N, int k)
{
    int i = blockDim.x * blockIdx.x + threadIdx.x;
    if (i >= M) return;
    float val = A[i * N + k];
    atomicAdd(nrm_buf, val * val);
}

// ---------------------------------------------------------------------------
// Kernel: finalize norm — single thread computes sqrt, stores in R[k,k]
// ---------------------------------------------------------------------------
__global__ void gram_norm_finish(float* __restrict__ nrm_buf,
                                  float* __restrict__ R,
                                  int N, int k)
{
    float nrm = sqrtf(*nrm_buf);
    R[k * N + k] = nrm;
    *nrm_buf = nrm;  // overwrite so normalize kernel can read it
}

// ---------------------------------------------------------------------------
// Kernel: Q[:,k] = A[:,k] / nrm_buf[0]
// ---------------------------------------------------------------------------
__global__ void gram_normalize(const float* __restrict__ A,
                                float* __restrict__ Q,
                                const float* __restrict__ nrm_buf,
                                int M, int N, int k)
{
    int i = blockDim.x * blockIdx.x + threadIdx.x;
    if (i >= M) return;
    Q[i * N + k] = A[i * N + k] / nrm_buf[0];
}

// ---------------------------------------------------------------------------
// Kernel: update columns j = k+1..N-1
//   dot   = Q[:,k] · A[:,j]
//   R[k,j] = dot
//   A[:,j] -= dot * Q[:,k]
// One thread per column j > k.
// ---------------------------------------------------------------------------
__global__ void gram_project(float* __restrict__ A,
                              const float* __restrict__ Q,
                              float* __restrict__ R,
                              int M, int N, int k)
{
    int j = blockDim.x * blockIdx.x + threadIdx.x + k + 1;
    if (j >= N) return;

    float dot = 0.0f;
    for (int i = 0; i < M; i++)
        dot += Q[i * N + k] * A[i * N + j];
    R[k * N + j] = dot;
    for (int i = 0; i < M; i++)
        A[i * N + j] -= dot * Q[i * N + k];
}

// ---------------------------------------------------------------------------
// CPU orthogonality reference check: verify Q^T * Q ≈ I
// Checks first `check` columns (up to min(8, N))
// ---------------------------------------------------------------------------
static bool check_orthogonality(const float* Q, int M, int N)
{
    int check = (N < 8) ? N : 8;
    bool pass = true;
    for (int ci = 0; ci < check; ci++) {
        for (int cj = 0; cj < check; cj++) {
            float dot = 0.0f;
            for (int i = 0; i < M; i++)
                dot += Q[i * N + ci] * Q[i * N + cj];
            float expected = (ci == cj) ? 1.0f : 0.0f;
            if (fabsf(dot - expected) > 0.01f) {
                fprintf(stderr,
                        "ORTH FAIL: dot(Q[:,%d], Q[:,%d]) = %.6f  (expected %.1f)\n",
                        ci, cj, dot, expected);
                pass = false;
            }
        }
    }
    return pass;
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------
int main(int argc, char** argv)
{
    setvbuf(stdout, NULL, _IONBF, 0);

    int M      = parseIntParam(argc, argv, "--m", 512);
    int N      = parseIntParam(argc, argv, "--n", 512);
    int passes = parseIntParam(argc, argv, "--passes", 3);
    int block_size_param = parseIntParam(argc, argv, "--block_size", 256);

    const char* env_val;
    env_val = getenv("BENCH_PARAM_m");
    if (env_val) M = atoi(env_val);
    env_val = getenv("BENCH_PARAM_n");
    if (env_val) N = atoi(env_val);
    env_val = getenv("BENCH_PARAM_passes");
    if (env_val) passes = atoi(env_val);
    env_val = getenv("BENCH_PARAM_block_size");
    if (env_val) block_size_param = atoi(env_val);

    size_t bytes_A = (size_t)M * N * sizeof(float);
    size_t bytes_Q = (size_t)M * N * sizeof(float);
    size_t bytes_R = (size_t)N * N * sizeof(float);
    size_t bytes_1 = sizeof(float);

    // Print device info
    int device_id = 0;
    CUDA_CHECK(cudaGetDevice(&device_id));
    cudaDeviceProp prop;
    CUDA_CHECK(cudaGetDeviceProperties(&prop, device_id));
    fprintf(stderr, "Device: %s (id=%d)\n", prop.name, device_id);
    fprintf(stderr, "Matrix: %d×%d  |  Passes: %d\n\n", M, N, passes);

    // Host allocations
    float* h_A_orig = (float*)malloc(bytes_A);
    float* h_Q      = (float*)malloc(bytes_Q);

    if (!h_A_orig || !h_Q) {
        fprintf(stderr, "Error: host malloc failed\n");
        return 1;
    }

    // Initialize A with random floats in [0,1) to ensure full column rank
    srand(42);
    for (int i = 0; i < M * N; ++i)
        h_A_orig[i] = (float)rand() / (float)RAND_MAX;

    // Device allocations
    float *d_A, *d_Q, *d_R, *d_nrm_buf;
    CUDA_CHECK(cudaMalloc(&d_A,       bytes_A));
    CUDA_CHECK(cudaMalloc(&d_Q,       bytes_Q));
    CUDA_CHECK(cudaMalloc(&d_R,       bytes_R));
    CUDA_CHECK(cudaMalloc(&d_nrm_buf, bytes_1));

    const int block_size = 256;
    const int grid_M = (M + block_size - 1) / block_size;

    BenchmarkTimer timer;
    std::vector<double> times((size_t)passes);

    for (int pass = 0; pass < passes; ++pass) {
        // Restore A from original, zero Q and R (outside timing)
        CUDA_CHECK(cudaMemcpy(d_A, h_A_orig, bytes_A, cudaMemcpyHostToDevice));
        CUDA_CHECK(cudaMemset(d_Q, 0, bytes_Q));
        CUDA_CHECK(cudaMemset(d_R, 0, bytes_R));
        CUDA_CHECK(cudaDeviceSynchronize());

        // ----- Timed region -----
        timer.record_start();

        for (int k = 0; k < N; ++k) {
            // Step 1: zero nrm_buf, then accumulate ||A[:,k]||^2
            CUDA_CHECK(cudaMemsetAsync(d_nrm_buf, 0, sizeof(float), 0));
            gram_norm<<<grid_M, block_size, 0, 0>>>(d_A, d_nrm_buf, M, N, k);

            // Step 2: finalize norm → R[k,k]
            gram_norm_finish<<<1, 1, 0, 0>>>(d_nrm_buf, d_R, N, k);

            // Step 3: normalize column k
            gram_normalize<<<grid_M, block_size, 0, 0>>>(d_A, d_Q, d_nrm_buf, M, N, k);

            // Step 4: project remaining columns (if any)
            if (k + 1 < N) {
                int remaining   = N - k - 1;
                int grid_proj   = (remaining + block_size - 1) / block_size;
                gram_project<<<grid_proj, block_size, 0, 0>>>(d_A, d_Q, d_R, M, N, k);
            }
        }

        timer.record_stop();
        // ----- End timed region -----

        times[pass] = static_cast<double>(timer.elapsed_ms());
        fprintf(stderr, "  pass %d: %.4f ms\n", pass + 1, times[pass]);
    }

    // Copy Q back for orthogonality check
    CUDA_CHECK(cudaMemcpy(h_Q, d_Q, bytes_Q, cudaMemcpyDeviceToHost));

    // Orthogonality check
    bool orth_ok = check_orthogonality(h_Q, M, N);
    fprintf(stderr, "Orthogonality check (first %d columns): %s\n\n",
            (N < 8 ? N : 8), orth_ok ? "PASS" : "FAIL");

    // Statistics
    double sum_ms = 0.0, mn = DBL_MAX, mx = 0.0;
    for (int i = 0; i < passes; ++i) {
        sum_ms += times[i];
        if (times[i] < mn) mn = times[i];
        if (times[i] > mx) mx = times[i];
    }
    double avg_ms = sum_ms / passes;

    double variance = 0.0;
    for (int i = 0; i < passes; ++i) {
        double d = times[i] - avg_ms;
        variance += d * d;
    }
    double stddev = (passes > 1) ? sqrt(variance / (passes - 1)) : 0.0;

    // GB/s: 3 * M*N*sizeof(float) per pass (read A, read Q, write A for project)
    double gb   = 3.0 * (double)M * (double)N * sizeof(float) / 1e9;
    double gbps = gb / (avg_ms * 1e-3);

    fprintf(stderr,
            "Bandwidth: %.2f GB/s  (avg %.4f ms, min %.4f ms, max %.4f ms, stddev %.4f ms)\n",
            gbps, avg_ms, mn, mx, stddev);

    // JSON-lines output
    printf("{\"type\":\"kernel\",\"name\":\"gram_norm\",\"time_ms\":%.6f,"
           "\"params\":{\"m\":%d,\"n\":%d,\"passes\":%d,\"block_size\":%d}}\n",
           avg_ms, M, N, passes, block_size_param);
    printf("{\"type\":\"kernel\",\"name\":\"gram_norm_finish\",\"time_ms\":%.6f,"
           "\"params\":{\"m\":%d,\"n\":%d,\"passes\":%d,\"block_size\":%d}}\n",
           avg_ms, M, N, passes, block_size_param);
    printf("{\"type\":\"kernel\",\"name\":\"gram_normalize\",\"time_ms\":%.6f,"
           "\"params\":{\"m\":%d,\"n\":%d,\"passes\":%d,\"block_size\":%d}}\n",
           avg_ms, M, N, passes, block_size_param);
    printf("{\"type\":\"kernel\",\"name\":\"gram_project\",\"time_ms\":%.6f,"
           "\"params\":{\"m\":%d,\"n\":%d,\"passes\":%d,\"block_size\":%d}}\n",
           avg_ms, M, N, passes, block_size_param);
    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"bandwidth_gbps\",\"value\":%.2f}]}\n",
           avg_ms, gbps);

    // Cleanup
    CUDA_CHECK(cudaFree(d_A));
    CUDA_CHECK(cudaFree(d_Q));
    CUDA_CHECK(cudaFree(d_R));
    CUDA_CHECK(cudaFree(d_nrm_buf));
    free(h_A_orig);
    free(h_Q);

    return 0;
}
