// rodinia_gaussian.hip — Rodinia Gaussian elimination benchmark (HIP, self-contained)
//
// Gaussian elimination with back-substitution to solve Ax=b for an NxN dense matrix.
// Derived from the Rodinia benchmark suite Gaussian workload.
//
// Two GPU kernels:
//   fan1: Compute multipliers m[i][k] = a[i][k] / a[k][k] for pivot row k
//   fan2: Update submatrix: a[i][j] -= m[i][k] * a[k][j], b[i] -= m[i][k] * b[k]
// Back-substitution is performed on the CPU after GPU forward elimination.
//
// Compiles with hipcc (AMD ROCm) or nvcc -x cu (CUDA).
//
// Usage:
//   ./rodinia_gaussian [--size N]
//
//   --size N         Matrix dimension N (N×N)  (default: 512)
//
// Output (stdout): CSV — gaussian,<N>,<time_ms>,<GFLOPS>
// Output (stderr): device info, timing details, verification result

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
// Kernel 1: fan1 — compute multipliers for pivot column t
//
// Each thread handles one row below the pivot:
//   m[(t+1+tid)*Size + t] = a[(t+1+tid)*Size + t] / a[t*Size + t]
// ---------------------------------------------------------------------------
__global__ void fan1(float* __restrict__ m,
                     const float* __restrict__ a,
                     int Size, int t)
{
    int tid = blockIdx.x * blockDim.x + threadIdx.x;
    if (tid < Size - 1 - t) {
        int row = t + 1 + tid;
        m[row * Size + t] = a[row * Size + t] / a[t * Size + t];
    }
}

// ---------------------------------------------------------------------------
// Kernel 2: fan2 — eliminate pivot column from submatrix below pivot row t
//
// Each (col, row) thread updates one element of the submatrix:
//   a[(t+1+row)*Size + (t+1+col)] -= m[(t+1+row)*Size + t] * a[t*Size + (t+1+col)]
// Thread with col==0 also updates b:
//   b[t+1+row] -= m[(t+1+row)*Size + t] * b[t]
// ---------------------------------------------------------------------------
__global__ void fan2(const float* __restrict__ m,
                     float* __restrict__ a,
                     float* __restrict__ b,
                     int Size, int t)
{
    int col = blockIdx.x * blockDim.x + threadIdx.x;  // relative column index
    int row = blockIdx.y * blockDim.y + threadIdx.y;  // relative row index
    int remaining = Size - t - 1;

    if (col < remaining && row < remaining) {
        int abs_row = t + 1 + row;
        int abs_col = t + 1 + col;
        a[abs_row * Size + abs_col] -= m[abs_row * Size + t] * a[t * Size + abs_col];
        if (col == 0) {
            b[abs_row] -= m[abs_row * Size + t] * b[t];
        }
    }
}

// ---------------------------------------------------------------------------
// CPU back-substitution: solve upper-triangular system Ux = b
// ---------------------------------------------------------------------------
static void backSubstitution(const float* a, const float* b, float* x, int N)
{
    for (int i = N - 1; i >= 0; --i) {
        double sum = b[i];
        for (int j = i + 1; j < N; ++j) {
            sum -= (double)a[i * N + j] * (double)x[j];
        }
        x[i] = (float)(sum / (double)a[i * N + i]);
    }
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------
int main(int argc, char** argv)
{
    setvbuf(stdout, NULL, _IONBF, 0);

    // Defaults
    int N     = 512;
    int block_size_param = 256;
    const char* verify = "true";

    // Command-line fallback
    N     = parseIntParam(argc, argv, "--size", N);

    // Environment variable overrides
    const char* env_val;
    env_val = getenv("BENCH_PARAM_size");
    if (env_val) N = atoi(env_val);
    env_val = getenv("BENCH_PARAM_block_size");
    if (env_val) block_size_param = atoi(env_val);
    env_val = getenv("BENCH_PARAM_verify");
    if (env_val) verify = env_val;
    int num_warmup = 5;

    size_t bytes_a = (size_t)N * N * sizeof(float);
    size_t bytes_b = (size_t)N * sizeof(float);

    // Print device info
    int device_id = 0;
    HIP_CHECK(hipGetDevice(&device_id));
    hipDeviceProp_t prop;
    HIP_CHECK(hipGetDeviceProperties(&prop, device_id));
    fprintf(stderr, "Device: %s (id=%d)\n", prop.name, device_id);
    fprintf(stderr, "Matrix size: %d×%d\n\n", N, N);

    // Host allocations
    float* h_a      = (float*)malloc(bytes_a);
    float* h_b      = (float*)malloc(bytes_b);
    float* h_x      = (float*)malloc(bytes_b);
    float* h_a_orig = (float*)malloc(bytes_a);
    float* h_b_orig = (float*)malloc(bytes_b);

    // Initialize: diagonally dominant matrix for numerical stability (no pivoting needed)
    // A_ii = N (large diagonal), A_ij = random [0, 0.9] for i!=j
    // b_i = random [1.0, 1.9]
    srand(42);
    for (int i = 0; i < N * N; ++i) {
        h_a[i] = (float)(rand() % 10) / 10.0f;
    }
    for (int i = 0; i < N; ++i) {
        h_a[i * N + i] += (float)N;  // ensure diagonal dominance
    }
    for (int i = 0; i < N; ++i) {
        h_b[i] = (float)(rand() % 10) / 10.0f + 1.0f;
    }

    // Save originals for correctness verification
    memcpy(h_a_orig, h_a, bytes_a);
    memcpy(h_b_orig, h_b, bytes_b);

    // Device allocations
    float *d_a, *d_b, *d_m;
    HIP_CHECK(hipMalloc(&d_a, bytes_a));
    HIP_CHECK(hipMalloc(&d_b, bytes_b));
    HIP_CHECK(hipMalloc(&d_m, bytes_a));  // multiplier matrix (same size as A)

    // Kernel launch configurations
    const int BLOCK1D = 256;
    const int BLOCK2D = 16;

    // Warmup
    for (int w = 0; w < num_warmup; ++w) {
        HIP_CHECK(hipMemcpy(d_a, h_a, bytes_a, hipMemcpyHostToDevice));
        HIP_CHECK(hipMemcpy(d_b, h_b, bytes_b, hipMemcpyHostToDevice));
        HIP_CHECK(hipMemset(d_m, 0, bytes_a));

        for (int t = 0; t < N - 1; ++t) {
            int remaining = N - t - 1;
            // fan1
            int grid1 = (remaining + BLOCK1D - 1) / BLOCK1D;
            hipLaunchKernelGGL(fan1, grid1, BLOCK1D, 0, 0, d_m, d_a, N, t);
            // fan2
            dim3 block2(BLOCK2D, BLOCK2D);
            dim3 grid2((remaining + BLOCK2D - 1) / BLOCK2D,
                       (remaining + BLOCK2D - 1) / BLOCK2D);
            hipLaunchKernelGGL(fan2, grid2, block2, 0, 0, d_m, d_a, d_b, N, t);
        }
        HIP_CHECK(hipDeviceSynchronize());
    }

    // Timed iterations — fan1 and fan2 timed separately into their own accumulators
    double t_fan1 = 0.0;
    double t_fan2 = 0.0;
    BenchmarkTimer timer;

    for (int iter = 0; iter < 1; ++iter) {
        // Reset device data from original (outside timing)
        HIP_CHECK(hipMemcpy(d_a, h_a, bytes_a, hipMemcpyHostToDevice));
        HIP_CHECK(hipMemcpy(d_b, h_b, bytes_b, hipMemcpyHostToDevice));
        HIP_CHECK(hipMemset(d_m, 0, bytes_a));

        // Time the forward elimination kernel loop, per kernel
        for (int t = 0; t < N - 1; ++t) {
            int remaining = N - t - 1;
            // fan1: compute multipliers
            int grid1 = (remaining + BLOCK1D - 1) / BLOCK1D;
            timer.record_start();
            hipLaunchKernelGGL(fan1, grid1, BLOCK1D, 0, 0, d_m, d_a, N, t);
            timer.record_stop();
            t_fan1 += (double)timer.elapsed_ms();
            // fan2: update submatrix
            dim3 block2(BLOCK2D, BLOCK2D);
            dim3 grid2((remaining + BLOCK2D - 1) / BLOCK2D,
                       (remaining + BLOCK2D - 1) / BLOCK2D);
            timer.record_start();
            hipLaunchKernelGGL(fan2, grid2, block2, 0, 0, d_m, d_a, d_b, N, t);
            timer.record_stop();
            t_fan2 += (double)timer.elapsed_ms();
        }
    }

    // Copy final result from device
    HIP_CHECK(hipMemcpy(h_a, d_a, bytes_a, hipMemcpyDeviceToHost));
    HIP_CHECK(hipMemcpy(h_b, d_b, bytes_b, hipMemcpyDeviceToHost));

    // CPU back-substitution on the factored system
    backSubstitution(h_a, h_b, h_x, N);

    // Correctness check: compute ||A_orig * x - b_orig|| / ||b_orig||
    double norm_res = 0.0, norm_b = 0.0;
    for (int i = 0; i < N; ++i) {
        double res = -(double)h_b_orig[i];
        for (int j = 0; j < N; ++j) {
            res += (double)h_a_orig[i * N + j] * (double)h_x[j];
        }
        norm_res += res * res;
        norm_b   += (double)h_b_orig[i] * (double)h_b_orig[i];
    }
    double rel_err = sqrt(norm_res / (norm_b + 1e-30));
    bool pass = (rel_err < 1e-4);
    fprintf(stderr, "Verification: rel_err = %.2e — %s\n\n",
            rel_err, pass ? "PASS" : "FAIL");

    // Compute statistics — total is the sum of the per-kernel accumulators
    double total_ms = t_fan1 + t_fan2;
    double avg_ms = total_ms;

    // GFLOPS: 2/3 * N^3 floating-point ops for forward elimination
    double flops  = (2.0 / 3.0) * (double)N * (double)N * (double)N;
    double gflops = flops / (avg_ms * 1e-3) / 1e9;

    fprintf(stderr, "Performance: %.2f GFLOPS  (total %.4f ms: fan1=%.4f fan2=%.4f)\n",
            gflops, total_ms, t_fan1, t_fan2);

    // JSON-lines kernel events
    printf("{\"type\":\"kernel\",\"name\":\"fan1\",\"time_ms\":%.6f,"
           "\"params\":{\"size\":%d,\"block_size\":%d,\"verify\":\"%s\"}}\n",
           t_fan1, N, block_size_param, verify);

    printf("{\"type\":\"kernel\",\"name\":\"fan2\",\"time_ms\":%.6f,"
           "\"params\":{\"size\":%d,\"block_size\":%d,\"verify\":\"%s\"}}\n",
           t_fan2, N, block_size_param, verify);

    // JSON-lines summary event
    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"gflops\",\"value\":%.2f}]}\n",
           total_ms, gflops);

    // Cleanup
    HIP_CHECK(hipFree(d_a));
    HIP_CHECK(hipFree(d_b));
    HIP_CHECK(hipFree(d_m));
    free(h_a);
    free(h_b);
    free(h_x);
    free(h_a_orig);
    free(h_b_orig);

    return pass ? 0 : 1;
}
