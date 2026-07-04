// polybench_jacobi2d.cu — PolyBench Jacobi 2D stencil benchmark (CUDA, self-contained)
//
// Computes TSTEPS iterations of 2D Jacobi stencil on an N×N grid:
//   B[i][j] = (A[i-1][j] + A[i+1][j] + A[i][j-1] + A[i][j+1] + A[i][j]) / 5.0
//   then swap A and B (double-buffer)
//
// Only interior points (i=1..N-2, j=1..N-2) are updated; boundaries remain 0.
//
// Default: N=1024, TSTEPS=50
// Measures memory bandwidth in GB/s (read N×N + write N×N each step).
//
// Native CUDA implementation.
//
// Usage:
//   ./polybench_jacobi2d [--n N] [--tsteps T] [--trials K]
//
//   --n N        Grid size N×N           (default: 1024)
//   --tsteps T   Number of time steps    (default: 50)
//   --trials K   Number of timed trials  (default: 3)
//
// Output (stdout): CSV — jacobi2d,<N>,<TSTEPS>,<time_ms>,<GBs>
// Output (stderr): device info, timing details

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
// Kernel: one step of 2D Jacobi stencil
// ---------------------------------------------------------------------------
#define BLOCK_DIM 16

__global__ void jacobi2d_kernel(const float* __restrict__ A,
                                 float* __restrict__ B,
                                 int N)
{
    int i = blockIdx.y * blockDim.y + threadIdx.y + 1;  // interior row
    int j = blockIdx.x * blockDim.x + threadIdx.x + 1;  // interior col
    if (i >= N-1 || j >= N-1) return;
    B[i*N + j] = (A[(i-1)*N + j] + A[(i+1)*N + j] +
                  A[i*N + (j-1)] + A[i*N + (j+1)] +
                  A[i*N + j]) * 0.2f;
}

// ---------------------------------------------------------------------------
// CPU reference for correctness check
// ---------------------------------------------------------------------------
static void cpu_jacobi2d(const float* A, float* B, int N) {
    for (int i = 1; i < N-1; ++i) {
        for (int j = 1; j < N-1; ++j) {
            B[i*N + j] = (A[(i-1)*N + j] + A[(i+1)*N + j] +
                          A[i*N + (j-1)] + A[i*N + (j+1)] +
                          A[i*N + j]) * 0.2f;
        }
    }
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------
int main(int argc, char** argv) {
    setvbuf(stdout, NULL, _IONBF, 0);

    int N      = parseIntParam(argc, argv, "--n",      1024);
    int TSTEPS = parseIntParam(argc, argv, "--tsteps", 50);
    int trials = parseIntParam(argc, argv, "--trials", 3);
    int block_size_param = parseIntParam(argc, argv, "--block_size", 256);

    const char* env_val;
    env_val = getenv("BENCH_PARAM_n");
    if (env_val) N = atoi(env_val);
    env_val = getenv("BENCH_PARAM_tsteps");
    if (env_val) TSTEPS = atoi(env_val);
    env_val = getenv("BENCH_PARAM_trials");
    if (env_val) trials = atoi(env_val);
    env_val = getenv("BENCH_PARAM_block_size");
    if (env_val) block_size_param = atoi(env_val);
    int num_warmup = 0;

    size_t bytes = (size_t)N * N * sizeof(float);

    // Print device info
    int device_id = 0;
    CUDA_CHECK(cudaGetDevice(&device_id));
    cudaDeviceProp prop;
    CUDA_CHECK(cudaGetDeviceProperties(&prop, device_id));
    fprintf(stderr, "Device: %s (id=%d)\n", prop.name, device_id);
    fprintf(stderr, "Grid: %d×%d  |  TSTEPS: %d  |  Trials: %d\n\n",
            N, N, TSTEPS, trials);

    // Host allocations
    std::vector<float> h_A((size_t)N * N, 0.0f);
    std::vector<float> h_B((size_t)N * N, 0.0f);

    // Initialize with random floats (interior only; boundaries stay 0)
    srand(42);
    for (int i = 1; i < N-1; ++i)
        for (int j = 1; j < N-1; ++j)
            h_A[i*N + j] = (float)(rand() % 100) / 10.0f;

    // Device allocations
    float *d_A, *d_B;
    CUDA_CHECK(cudaMalloc(&d_A, bytes));
    CUDA_CHECK(cudaMalloc(&d_B, bytes));

    // Copy A to device (B starts zeroed)
    CUDA_CHECK(cudaMemcpy(d_A, h_A.data(), bytes, cudaMemcpyHostToDevice));
    CUDA_CHECK(cudaMemset(d_B, 0, bytes));

    dim3 block(BLOCK_DIM, BLOCK_DIM);
    dim3 grid((N-2 + BLOCK_DIM - 1) / BLOCK_DIM,
              (N-2 + BLOCK_DIM - 1) / BLOCK_DIM);

    // Warmup (not measured)
    for (int w = 0; w < num_warmup; ++w) {
        float *src = d_A, *dst = d_B;
        for (int t = 0; t < TSTEPS; ++t) {
            jacobi2d_kernel<<<grid, block, 0, 0>>>(src, dst, N);
            float* tmp = src; src = dst; dst = tmp;
        }
        CUDA_CHECK(cudaDeviceSynchronize());
        // Reset to initial state
        CUDA_CHECK(cudaMemcpy(d_A, h_A.data(), bytes, cudaMemcpyHostToDevice));
        CUDA_CHECK(cudaMemset(d_B, 0, bytes));
    }

    // Timed trials
    BenchmarkTimer timer;
    std::vector<double> times((size_t)trials);
    for (int tr = 0; tr < trials; ++tr) {
        // Reset for each trial
        CUDA_CHECK(cudaMemcpy(d_A, h_A.data(), bytes, cudaMemcpyHostToDevice));
        CUDA_CHECK(cudaMemset(d_B, 0, bytes));

        float *src = d_A, *dst = d_B;
        timer.record_start();
        for (int t = 0; t < TSTEPS; ++t) {
            jacobi2d_kernel<<<grid, block, 0, 0>>>(src, dst, N);
            float* tmp = src; src = dst; dst = tmp;
        }
        timer.record_stop();
        times[tr] = static_cast<double>(timer.elapsed_ms());
    }

    // Copy final result back (src points to the latest output after swaps)
    // After TSTEPS swaps: if TSTEPS is even, src=d_A holds result; if odd, src=d_B
    float *final_src = (TSTEPS % 2 == 0) ? d_A : d_B;
    CUDA_CHECK(cudaMemcpy(h_B.data(), final_src, bytes, cudaMemcpyDeviceToHost));

    // Correctness check: run CPU for TSTEPS on small 64×64 grid
    {
        int CN = 64;
        std::vector<float> cA((size_t)CN * CN, 0.0f);
        std::vector<float> cB((size_t)CN * CN, 0.0f);
        std::vector<float> cA_gpu_init((size_t)CN * CN, 0.0f);

        srand(42);
        for (int i = 1; i < CN-1; ++i)
            for (int j = 1; j < CN-1; ++j)
                cA[i*CN + j] = (float)(rand() % 100) / 10.0f;
        cA_gpu_init = cA;

        // Run CPU TSTEPS
        for (int t = 0; t < TSTEPS; ++t) {
            cpu_jacobi2d(cA.data(), cB.data(), CN);
            cA.swap(cB);
        }
        // cA now holds CPU result

        // Run GPU on CN×CN grid
        size_t cbytes = (size_t)CN * CN * sizeof(float);
        float *cd_A, *cd_B;
        CUDA_CHECK(cudaMalloc(&cd_A, cbytes));
        CUDA_CHECK(cudaMalloc(&cd_B, cbytes));
        CUDA_CHECK(cudaMemcpy(cd_A, cA_gpu_init.data(), cbytes, cudaMemcpyHostToDevice));
        CUDA_CHECK(cudaMemset(cd_B, 0, cbytes));

        dim3 cblock(BLOCK_DIM, BLOCK_DIM);
        dim3 cgrid((CN-2 + BLOCK_DIM - 1) / BLOCK_DIM,
                   (CN-2 + BLOCK_DIM - 1) / BLOCK_DIM);

        float *csrc = cd_A, *cdst = cd_B;
        for (int t = 0; t < TSTEPS; ++t) {
            jacobi2d_kernel<<<cgrid, cblock, 0, 0>>>(csrc, cdst, CN);
            float* tmp = csrc; csrc = cdst; cdst = tmp;
        }
        CUDA_CHECK(cudaDeviceSynchronize());

        std::vector<float> gpu_result((size_t)CN * CN);
        float *cfinal = (TSTEPS % 2 == 0) ? cd_A : cd_B;
        CUDA_CHECK(cudaMemcpy(gpu_result.data(), cfinal, cbytes, cudaMemcpyDeviceToHost));

        bool pass = true;
        float max_rel = 0.0f;
        for (int i = 1; i < CN-1 && pass; ++i) {
            for (int j = 1; j < CN-1; ++j) {
                float ref = cA[i*CN + j];
                float gpu = gpu_result[i*CN + j];
                float rel = fabsf(gpu - ref) / (fabsf(ref) + 1e-6f);
                if (rel > max_rel) max_rel = rel;
                if (rel > 1e-3f) {
                    fprintf(stderr, "MISMATCH [%d][%d]: gpu=%.6f cpu=%.6f rel=%.4e\n",
                            i, j, gpu, ref, rel);
                    pass = false;
                    break;
                }
            }
        }
        fprintf(stderr, "Correctness check (CPU vs GPU, %d×%d, %d steps): %s (max_rel=%.2e)\n\n",
                CN, CN, TSTEPS, pass ? "PASS" : "FAIL", max_rel);

        CUDA_CHECK(cudaFree(cd_A));
        CUDA_CHECK(cudaFree(cd_B));
    }

    // Statistics
    double sum_ms = 0.0, mn = DBL_MAX, mx = 0.0;
    for (int i = 0; i < trials; ++i) {
        sum_ms += times[i];
        if (times[i] < mn) mn = times[i];
        if (times[i] > mx) mx = times[i];
    }
    double avg_ms = sum_ms / trials;

    double variance = 0.0;
    for (int i = 0; i < trials; ++i) {
        double d = times[i] - avg_ms;
        variance += d * d;
    }
    double stddev = (trials > 1) ? sqrt(variance / (trials - 1)) : 0.0;

    // GB/s: read N×N + write N×N per step, TSTEPS steps
    double gb   = (double)TSTEPS * 2.0 * (double)N * (double)N * sizeof(float) / 1e9;
    double gbps = gb / (avg_ms * 1e-3);

    fprintf(stderr, "Bandwidth: %.2f GB/s  (avg %.4f ms, min %.4f ms, max %.4f ms, stddev %.4f ms)\n",
            gbps, avg_ms, mn, mx, stddev);

    // JSON-lines output
    printf("{\"type\":\"kernel\",\"name\":\"jacobi2d_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"n\":%d,\"tsteps\":%d,\"trials\":%d,\"block_size\":%d}}\n",
           avg_ms, N, TSTEPS, trials, block_size_param);
    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"bandwidth_gbps\",\"value\":%.2f}]}\n",
           avg_ms, gbps);

    // Cleanup
    CUDA_CHECK(cudaFree(d_A));
    CUDA_CHECK(cudaFree(d_B));

    return 0;
}
