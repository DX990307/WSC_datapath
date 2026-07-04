// shoc_stencil2d.hip — SHOC 9-point 2D stencil benchmark (HIP, self-contained)
//
// Applies a 9-point star stencil iteratively on an N×N grid:
//
//   out[i][j] = 0.5  * in[i][j]                                   (center)
//             + 0.1  * (in[i-1][j] + in[i+1][j] +                (N/S/E/W)
//                       in[i][j-1] + in[i][j+1])
//             + 0.025 * (in[i-1][j-1] + in[i-1][j+1] +           (diagonals)
//                        in[i+1][j-1] + in[i+1][j+1])
//
// Interior cells only: rows/columns 1 .. N-2.
// Boundaries (row 0, row N-1, col 0, col N-1) remain fixed at 0.
// Uses ping-pong buffers: in_buf and out_buf are swapped each step.
//
// Default: N=2048, ITERATIONS=5
// Measures effective memory bandwidth in GB/s.
// Runs 3 timed trials, reports average.
//
// Compiles with hipcc (AMD ROCm) or nvcc -x cu (CUDA).
//
// Usage:
//   ./shoc_stencil2d [--n N] [--iterations I] [--trials T]
//
//   --n N            Grid dimension (N×N)           (default: 2048)
//   --iterations I   Stencil steps per trial         (default: 5)
//   --trials T       Number of timed trials          (default: 3)
//
// Output (stdout): CSV — stencil2d,<N>,<ITERATIONS>,<time_ms>,<GBs>
// Output (stderr): device info, timing details, correctness check

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
// Utilities
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

static int parseIntParam(int argc, char** argv, const char* name, int defVal) {
    for (int i = 1; i < argc - 1; ++i) {
        if (strcmp(argv[i], name) == 0) {
            int v = atoi(argv[i + 1]);
            if (v > 0) return v;
            break;
        }
    }
    return defVal;
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
    void record_start(hipStream_t s = 0) { HIP_CHECK(hipEventRecord(start, s)); }
    void record_stop (hipStream_t s = 0) {
        HIP_CHECK(hipEventRecord(stop, s));
        HIP_CHECK(hipEventSynchronize(stop));
    }
    float elapsed_ms() const {
        float ms = 0.0f;
        HIP_CHECK(hipEventElapsedTime(&ms, start, stop));
        return ms;
    }
};

// ---------------------------------------------------------------------------
// Stencil kernel — 9-point star stencil, 16×16 thread blocks
// ---------------------------------------------------------------------------

#define BLOCK_DIM 16

__global__ void stencil2d_kernel(const float* __restrict__ in,
                                  float* __restrict__ out,
                                  int N)
{
    // Map thread to interior cell (1-indexed)
    int i = blockIdx.y * blockDim.y + threadIdx.y + 1;  // row
    int j = blockIdx.x * blockDim.x + threadIdx.x + 1;  // col
    if (i >= N - 1 || j >= N - 1) return;

    float center = in[i * N + j];
    float north  = in[(i - 1) * N + j];
    float south  = in[(i + 1) * N + j];
    float west   = in[i * N + (j - 1)];
    float east   = in[i * N + (j + 1)];
    float nw     = in[(i - 1) * N + (j - 1)];
    float ne     = in[(i - 1) * N + (j + 1)];
    float sw     = in[(i + 1) * N + (j - 1)];
    float se     = in[(i + 1) * N + (j + 1)];

    out[i * N + j] = 0.5f   * center
                   + 0.1f   * (north + south + west + east)
                   + 0.025f * (nw + ne + sw + se);
}

// ---------------------------------------------------------------------------
// CPU reference (single iteration, for correctness check)
// ---------------------------------------------------------------------------

static void cpu_stencil2d(const float* in, float* out, int N)
{
    for (int i = 1; i < N - 1; ++i) {
        for (int j = 1; j < N - 1; ++j) {
            float center = in[i * N + j];
            float north  = in[(i - 1) * N + j];
            float south  = in[(i + 1) * N + j];
            float west   = in[i * N + (j - 1)];
            float east   = in[i * N + (j + 1)];
            float nw     = in[(i - 1) * N + (j - 1)];
            float ne     = in[(i - 1) * N + (j + 1)];
            float sw     = in[(i + 1) * N + (j - 1)];
            float se     = in[(i + 1) * N + (j + 1)];

            out[i * N + j] = 0.5f   * center
                           + 0.1f   * (north + south + west + east)
                           + 0.025f * (nw + ne + sw + se);
        }
    }
}

// ---------------------------------------------------------------------------
// Correctness check on a small grid
// ---------------------------------------------------------------------------

static bool correctness_check()
{
    const int NC = 64;
    size_t bytes = (size_t)NC * NC * sizeof(float);

    std::vector<float> h_in(NC * NC, 0.0f);
    std::vector<float> h_out_cpu(NC * NC, 0.0f);
    std::vector<float> h_out_gpu(NC * NC, 0.0f);

    srand(42);
    for (int i = 0; i < NC * NC; ++i)
        h_in[i] = (float)(rand() % 100) / 10.0f;
    // Keep boundaries at 0
    for (int j = 0; j < NC; ++j) h_in[0 * NC + j] = 0.0f;
    for (int j = 0; j < NC; ++j) h_in[(NC - 1) * NC + j] = 0.0f;
    for (int i = 0; i < NC; ++i) h_in[i * NC + 0] = 0.0f;
    for (int i = 0; i < NC; ++i) h_in[i * NC + (NC - 1)] = 0.0f;

    // CPU reference
    cpu_stencil2d(h_in.data(), h_out_cpu.data(), NC);

    // GPU
    float *d_in, *d_out;
    HIP_CHECK(hipMalloc(&d_in,  bytes));
    HIP_CHECK(hipMalloc(&d_out, bytes));
    HIP_CHECK(hipMemcpy(d_in,  h_in.data(),      bytes, hipMemcpyHostToDevice));
    HIP_CHECK(hipMemset(d_out, 0, bytes));

    dim3 block(BLOCK_DIM, BLOCK_DIM);
    dim3 grid((NC - 2 + BLOCK_DIM - 1) / BLOCK_DIM,
              (NC - 2 + BLOCK_DIM - 1) / BLOCK_DIM);
    hipLaunchKernelGGL(stencil2d_kernel, grid, block, 0, 0, d_in, d_out, NC);
    HIP_CHECK(hipDeviceSynchronize());
    HIP_CHECK(hipMemcpy(h_out_gpu.data(), d_out, bytes, hipMemcpyDeviceToHost));

    HIP_CHECK(hipFree(d_in));
    HIP_CHECK(hipFree(d_out));

    bool pass = true;
    for (int i = 1; i < NC - 1; ++i) {
        for (int j = 1; j < NC - 1; ++j) {
            float ref = h_out_cpu[i * NC + j];
            float gpu = h_out_gpu[i * NC + j];
            float rel = fabsf(gpu - ref) / (fabsf(ref) + 1e-6f);
            if (rel > 1e-4f) {
                fprintf(stderr, "MISMATCH at [%d][%d]: gpu=%.6f cpu=%.6f rel=%.4e\n",
                        i, j, gpu, ref, rel);
                pass = false;
                if (!pass) goto done; // report first mismatch only
            }
        }
    }
done:
    return pass;
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------

int main(int argc, char** argv)
{
    setvbuf(stdout, NULL, _IONBF, 0);

    int N      = parseIntParam(argc, argv, "--n",          2048);
    int iters  = parseIntParam(argc, argv, "--iterations", 5);
    int trials = parseIntParam(argc, argv, "--trials",     3);
    int block_size = 16;

    const char* env_val;
    env_val = getenv("BENCH_PARAM_n");
    if (env_val) N = atoi(env_val);
    env_val = getenv("BENCH_PARAM_iterations");
    if (env_val) iters = atoi(env_val);
    env_val = getenv("BENCH_PARAM_trials");
    if (env_val) trials = atoi(env_val);
    env_val = getenv("BENCH_PARAM_block_size");
    if (env_val) block_size = atoi(env_val);
    int num_warmup = 5;

    // Print device info
    int device_id = 0;
    HIP_CHECK(hipGetDevice(&device_id));
    hipDeviceProp_t prop;
    HIP_CHECK(hipGetDeviceProperties(&prop, device_id));
    fprintf(stderr, "Device: %s (id=%d)\n", prop.name, device_id);
    fprintf(stderr, "Grid: %d×%d  |  Iterations/trial: %d  |  Trials: %d\n\n",
            N, N, iters, trials);

    // Correctness check on 64×64 grid
    bool ok = correctness_check();
    fprintf(stderr, "Correctness check (64×64, 1 iteration): %s\n\n",
            ok ? "PASS" : "FAIL");

    // Allocate host and device buffers for full run
    size_t bytes = (size_t)N * N * sizeof(float);
    std::vector<float> h_data(N * N, 0.0f);

    // Initialize interior with random data
    srand(123);
    for (int i = 1; i < N - 1; ++i)
        for (int j = 1; j < N - 1; ++j)
            h_data[i * N + j] = (float)(rand() % 100) / 10.0f;

    float *d_buf0, *d_buf1;
    HIP_CHECK(hipMalloc(&d_buf0, bytes));
    HIP_CHECK(hipMalloc(&d_buf1, bytes));
    HIP_CHECK(hipMemcpy(d_buf0, h_data.data(), bytes, hipMemcpyHostToDevice));
    HIP_CHECK(hipMemset(d_buf1, 0, bytes));

    // Kernel launch config
    dim3 block(block_size, block_size);
    dim3 grid((N - 2 + block_size - 1) / block_size,
              (N - 2 + block_size - 1) / block_size);

    BenchmarkTimer timer;

    // Warmup
    for (int w = 0; w < num_warmup; ++w) {
        float *in_ptr = d_buf0, *out_ptr = d_buf1;
        for (int it = 0; it < iters; ++it) {
            hipLaunchKernelGGL(stencil2d_kernel, grid, block, 0, 0,
                               in_ptr, out_ptr, N);
            float* tmp = in_ptr; in_ptr = out_ptr; out_ptr = tmp;
        }
        HIP_CHECK(hipDeviceSynchronize());
    }

    // Timed trials
    std::vector<double> trial_times((size_t)trials);
    for (int t = 0; t < trials; ++t) {
        // Reset to original data each trial for reproducibility
        HIP_CHECK(hipMemcpy(d_buf0, h_data.data(), bytes, hipMemcpyHostToDevice));
        HIP_CHECK(hipMemset(d_buf1, 0, bytes));

        float *in_ptr = d_buf0, *out_ptr = d_buf1;

        timer.record_start();
        for (int it = 0; it < iters; ++it) {
            hipLaunchKernelGGL(stencil2d_kernel, grid, block, 0, 0,
                               in_ptr, out_ptr, N);
            float* tmp = in_ptr; in_ptr = out_ptr; out_ptr = tmp;
        }
        timer.record_stop();
        trial_times[t] = (double)timer.elapsed_ms();
    }

    // Statistics
    double sum_ms = 0.0, mn_ms = DBL_MAX, mx_ms = 0.0;
    for (int t = 0; t < trials; ++t) {
        sum_ms += trial_times[t];
        if (trial_times[t] < mn_ms) mn_ms = trial_times[t];
        if (trial_times[t] > mx_ms) mx_ms = trial_times[t];
    }
    double avg_ms = sum_ms / trials;
    double variance = 0.0;
    for (int t = 0; t < trials; ++t) {
        double d = trial_times[t] - avg_ms;
        variance += d * d;
    }
    double stddev = (trials > 1) ? sqrt(variance / (trials - 1)) : 0.0;

    // GB/s: each iteration reads N*N floats and writes (N-2)*(N-2) floats
    // approximate as 2 * N * N * sizeof(float) per iteration
    double gb   = (double)iters * 2.0 * (double)N * (double)N * sizeof(float) / 1e9;
    double gbps = gb / (avg_ms * 1e-3);

    fprintf(stderr, "Bandwidth: %.2f GB/s  (avg %.4f ms, min %.4f ms, max %.4f ms, stddev %.4f ms)\n",
            gbps, avg_ms, mn_ms, mx_ms, stddev);

    // JSON-lines output
    printf("{\"type\":\"kernel\",\"name\":\"stencil2d_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"n\":%d,\"iterations\":%d,\"trials\":%d,\"block_size\":%d}}\n",
           avg_ms, N, iters, trials, block_size);
    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"bandwidth_gbps\",\"value\":%.2f}]}\n",
           avg_ms, gbps);

    // Cleanup
    HIP_CHECK(hipFree(d_buf0));
    HIP_CHECK(hipFree(d_buf1));

    return 0;
}
