// polybench_fdtd2d.hip — PolyBench FDTD-2D benchmark (HIP, self-contained)
//
// 2D Finite Difference Time Domain electromagnetic simulation.
// Three field arrays: ex[NX][NY], ey[NX][NY], hz[NX][NY]
//
// Per time step update equations (PolyBench standard):
//   ex[0][j]  = 0                                         (boundary)
//   ex[i][j] += 0.5*(hz[i][j] - hz[i-1][j])  for i >= 1
//   ey[i][0]  = 0                                         (boundary)
//   ey[i][j] += 0.5*(hz[i][j] - hz[i][j-1])  for j >= 1
//   hz[i][j] -= 0.7*(ex[i][j+1]-ex[i][j] + ey[i+1][j]-ey[i][j])
//                                              for i < NX-1, j < NY-1
//
// Compiles with hipcc (AMD ROCm) or nvcc via HIP compatibility layer (CUDA).
//
// Usage:
//   ./polybench_fdtd2d [--size N] [--tmax T]
//
//   --size N        Grid dimension (N×N)            (default: 512)
//   --tmax T        Time steps per benchmark iter   (default: 50)
//
// Output (stdout): CSV — kernel_name,problem_size,iterations,avg_ms,min_ms,max_ms,stddev_ms
// Output (stderr): Effective bandwidth in GB/s

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

struct BenchResult {
    const char* kernel_name;
    const char* problem_size;
    int         iterations;
    double      avg_ms;
    double      min_ms;
    double      max_ms;
    double      stddev_ms;
};


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

static int g_num_warmup = 5;

template <typename Func>
static BenchResult runBenchmark(const char* kernel_name,
                                const char* problem_size,
                                int         iterations,
                                Func        func) {
    BenchmarkTimer timer;

    // Warmup
    for (int w = 0; w < g_num_warmup; ++w) {
        func();
    }
    HIP_CHECK(hipDeviceSynchronize());

    std::vector<double> times(1);
    for (int i = 0; i < 1; ++i) {
        timer.record_start();
        func();
        timer.record_stop();
        times[i] = static_cast<double>(timer.elapsed_ms());
    }

    double sum = 0.0, mn = DBL_MAX, mx = 0.0;
    for (int i = 0; i < 1; ++i) {
        sum += times[i];
        if (times[i] < mn) mn = times[i];
        if (times[i] > mx) mx = times[i];
    }
    double avg = sum / 1;

    double variance = 0.0;
    for (int i = 0; i < 1; ++i) {
        double d = times[i] - avg;
        variance += d * d;
    }
    double stddev = (1 > 1) ? sqrt(variance / (1 - 1)) : 0.0;

    BenchResult r;
    r.kernel_name  = kernel_name;
    r.problem_size = problem_size;
    r.iterations   = iterations;
    r.avg_ms       = avg;
    r.min_ms       = mn;
    r.max_ms       = mx;
    r.stddev_ms    = stddev;
    return r;
}

// ---------------------------------------------------------------------------
// FDTD-2D kernels
// ---------------------------------------------------------------------------

// Kernel 1: Update ex
//   ex[0][j]  = 0                              for all j
//   ex[i][j] += 0.5*(hz[i][j] - hz[i-1][j])   for i >= 1, all j
__global__ void fdtd_update_ex(float* __restrict__ ex,
                                const float* __restrict__ hz,
                                int NX, int NY) {
    int j = blockIdx.x * blockDim.x + threadIdx.x;
    int i = blockIdx.y * blockDim.y + threadIdx.y;

    if (i >= NX || j >= NY) return;

    if (i == 0) {
        ex[0 * NY + j] = 0.0f;
    } else {
        ex[i * NY + j] += 0.5f * (hz[i * NY + j] - hz[(i - 1) * NY + j]);
    }
}

// Kernel 2: Update ey
//   ey[i][0]  = 0                              for all i
//   ey[i][j] += 0.5*(hz[i][j] - hz[i][j-1])   for all i, j >= 1
__global__ void fdtd_update_ey(float* __restrict__ ey,
                                const float* __restrict__ hz,
                                int NX, int NY) {
    int j = blockIdx.x * blockDim.x + threadIdx.x;
    int i = blockIdx.y * blockDim.y + threadIdx.y;

    if (i >= NX || j >= NY) return;

    if (j == 0) {
        ey[i * NY + 0] = 0.0f;
    } else {
        ey[i * NY + j] += 0.5f * (hz[i * NY + j] - hz[i * NY + (j - 1)]);
    }
}

// Kernel 3: Update hz
//   hz[i][j] -= 0.7*(ex[i][j+1]-ex[i][j] + ey[i+1][j]-ey[i][j])
//   for i < NX-1, j < NY-1
__global__ void fdtd_update_hz(const float* __restrict__ ex,
                                const float* __restrict__ ey,
                                float* __restrict__ hz,
                                int NX, int NY) {
    int j = blockIdx.x * blockDim.x + threadIdx.x;
    int i = blockIdx.y * blockDim.y + threadIdx.y;

    if (i >= NX - 1 || j >= NY - 1) return;

    hz[i * NY + j] -= 0.7f * (ex[i * NY + (j + 1)] - ex[i * NY + j] +
                               ey[(i + 1) * NY + j] - ey[i * NY + j]);
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------
int main(int argc, char** argv) {
    setvbuf(stdout, NULL, _IONBF, 0);

    int N    = parseIntParam(argc, argv, "--size", 512);
    int TMAX = parseIntParam(argc, argv, "--tmax",  50);
    int block_size = parseIntParam(argc, argv, "--block_size", 256);

    const char* env_val;
    env_val = getenv("BENCH_PARAM_size");
    if (env_val) N = atoi(env_val);
    env_val = getenv("BENCH_PARAM_tmax");
    if (env_val) TMAX = atoi(env_val);
    env_val = getenv("BENCH_PARAM_block_size");
    if (env_val) block_size = atoi(env_val);
    int num_warmup = 5;

    int NX = N, NY = N;
    size_t grid_bytes = (size_t)NX * NY * sizeof(float);

    // Print device info
    int device_id = 0;
    HIP_CHECK(hipGetDevice(&device_id));
    hipDeviceProp_t prop;
    HIP_CHECK(hipGetDeviceProperties(&prop, device_id));
    fprintf(stderr, "Device: %s (id=%d)\n", prop.name, device_id);
    fprintf(stderr, "Grid: %d×%d  |  TMAX: %d\n\n",
            NX, NY, TMAX);

    // Host allocations and initialization
    float* h_ex = (float*)malloc(grid_bytes);
    float* h_ey = (float*)malloc(grid_bytes);
    float* h_hz = (float*)malloc(grid_bytes);

    srand(42);
    for (int i = 0; i < NX * NY; i++) h_ex[i] = (float)(rand() % 100) / 10.0f;
    for (int i = 0; i < NX * NY; i++) h_ey[i] = (float)(rand() % 100) / 10.0f;
    for (int i = 0; i < NX * NY; i++) h_hz[i] = (float)(rand() % 100) / 10.0f;

    // Device allocations
    float *d_ex, *d_ey, *d_hz;
    HIP_CHECK(hipMalloc(&d_ex, grid_bytes));
    HIP_CHECK(hipMalloc(&d_ey, grid_bytes));
    HIP_CHECK(hipMalloc(&d_hz, grid_bytes));

    // Kernel launch configuration
    dim3 block(16, 16);
    dim3 grid2d((NY + block.x - 1) / block.x, (NX + block.y - 1) / block.y);

    char problemSize[64];
    snprintf(problemSize, sizeof(problemSize), "%dx%d", NX, NY);

    g_num_warmup = num_warmup;
    (void)problemSize;

    BenchmarkTimer timer;

    // Warmup — run the full kernel sequence, untimed
    for (int w = 0; w < g_num_warmup; ++w) {
        HIP_CHECK(hipMemcpy(d_ex, h_ex, grid_bytes, hipMemcpyHostToDevice));
        HIP_CHECK(hipMemcpy(d_ey, h_ey, grid_bytes, hipMemcpyHostToDevice));
        HIP_CHECK(hipMemcpy(d_hz, h_hz, grid_bytes, hipMemcpyHostToDevice));

        for (int t = 0; t < TMAX; ++t) {
            hipLaunchKernelGGL(fdtd_update_ex, grid2d, block, 0, 0,
                               d_ex, d_hz, NX, NY);
            hipLaunchKernelGGL(fdtd_update_ey, grid2d, block, 0, 0,
                               d_ey, d_hz, NX, NY);
            hipLaunchKernelGGL(fdtd_update_hz, grid2d, block, 0, 0,
                               d_ex, d_ey, d_hz, NX, NY);
        }
    }
    HIP_CHECK(hipDeviceSynchronize());

    // Timed region — accumulate each kernel's GPU time across the TMAX steps
    double t_ex = 0.0, t_ey = 0.0, t_hz = 0.0;

    // Reset device arrays from host for reproducibility
    HIP_CHECK(hipMemcpy(d_ex, h_ex, grid_bytes, hipMemcpyHostToDevice));
    HIP_CHECK(hipMemcpy(d_ey, h_ey, grid_bytes, hipMemcpyHostToDevice));
    HIP_CHECK(hipMemcpy(d_hz, h_hz, grid_bytes, hipMemcpyHostToDevice));

    for (int t = 0; t < TMAX; ++t) {
        timer.record_start();
        hipLaunchKernelGGL(fdtd_update_ex, grid2d, block, 0, 0,
                           d_ex, d_hz, NX, NY);
        timer.record_stop();
        t_ex += static_cast<double>(timer.elapsed_ms());

        timer.record_start();
        hipLaunchKernelGGL(fdtd_update_ey, grid2d, block, 0, 0,
                           d_ey, d_hz, NX, NY);
        timer.record_stop();
        t_ey += static_cast<double>(timer.elapsed_ms());

        timer.record_start();
        hipLaunchKernelGGL(fdtd_update_hz, grid2d, block, 0, 0,
                           d_ex, d_ey, d_hz, NX, NY);
        timer.record_stop();
        t_hz += static_cast<double>(timer.elapsed_ms());
    }
    HIP_CHECK(hipDeviceSynchronize());

    double total_ms = t_ex + t_ey + t_hz;

    // Effective bandwidth: 7*NX*NY*sizeof(float) bytes/step across ex/ey/hz updates
    double bytes_per_step = 7.0 * (double)NX * NY * sizeof(float);
    double total_bytes    = bytes_per_step * TMAX;
    double bw_gb = total_bytes / (total_ms * 1e-3) / 1e9;
    fprintf(stderr, "Effective bandwidth: %.2f GB/s  (total %.4f ms: ex=%.4f ey=%.4f hz=%.4f, %d steps/iter)\n",
            bw_gb, total_ms, t_ex, t_ey, t_hz, TMAX);

    // JSON-lines output
    printf("{\"type\":\"kernel\",\"name\":\"fdtd_update_ex\",\"time_ms\":%.6f,"
           "\"params\":{\"size\":%d,\"tmax\":%d,\"block_size\":%d}}\n",
           t_ex, N, TMAX, block_size);
    printf("{\"type\":\"kernel\",\"name\":\"fdtd_update_ey\",\"time_ms\":%.6f,"
           "\"params\":{\"size\":%d,\"tmax\":%d,\"block_size\":%d}}\n",
           t_ey, N, TMAX, block_size);
    printf("{\"type\":\"kernel\",\"name\":\"fdtd_update_hz\",\"time_ms\":%.6f,"
           "\"params\":{\"size\":%d,\"tmax\":%d,\"block_size\":%d}}\n",
           t_hz, N, TMAX, block_size);
    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"bandwidth_gbps\",\"value\":%.2f}]}\n",
           total_ms, bw_gb);

    HIP_CHECK(hipFree(d_ex));
    HIP_CHECK(hipFree(d_ey));
    HIP_CHECK(hipFree(d_hz));
    free(h_ex); free(h_ey); free(h_hz);

    return 0;
}
