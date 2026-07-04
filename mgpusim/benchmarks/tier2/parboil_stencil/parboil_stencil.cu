// parboil_stencil.cu — Parboil 3D Jacobi Stencil benchmark (CUDA, self-contained)
//
// 7-point 3D stencil over a 64×64×64 grid using ping-pong buffers.
// Each kernel invocation performs one time-step; multiple time-steps per iteration.
//
// Stencil:
//   out[i] = c0*in[i] + c1*(in[i-1] + in[i+1] + in[i-nx] + in[i+nx]
//                           + in[i-nx*ny] + in[i+nx*ny])
//   c0 = 0.6,  c1 = (1-c0)/6 ≈ 0.0667
//
// Boundary cells (1-cell border) remain fixed.
//
// Native CUDA implementation.
//
// Usage:
//   ./parboil_stencil [--grid_dim N] [--num_timesteps T] [--iterations I]
//
//   --grid_dim N        Grid dimension (N×N×N)           (default: 64)
//   --num_timesteps T   Stencil time-steps per launch    (default: 10)
//   --iterations I      Timed benchmark iterations       (default: 5)
//
// Output (stdout): CSV row — kernel_name,problem_size,iterations,avg_ms,min_ms,max_ms,stddev_ms
// Output (stderr): GFLOPS and GB/s

#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <cmath>
#include <cfloat>
#include <vector>
#include <cuda_runtime.h>

// ---------------------------------------------------------------------------
// Inlined bench_common_cuda.h utilities
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

struct BenchResult {
    const char* kernel_name;
    const char* problem_size;
    int         iterations;
    double      avg_ms;
    double      min_ms;
    double      max_ms;
    double      stddev_ms;
};

static int parseIterations(int argc, char** argv) {
    int iters = 5;
    for (int i = 1; i < argc - 1; ++i) {
        if (strcmp(argv[i], "--iterations") == 0) {
            int v = atoi(argv[i + 1]);
            if (v >= 1) iters = v;
            break;
        }
    }
    return iters;
}

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

static int g_num_warmup = 0;

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
    CUDA_CHECK(cudaDeviceSynchronize());

    std::vector<double> times(iterations);
    for (int i = 0; i < iterations; ++i) {
        timer.record_start();
        func();
        timer.record_stop();
        times[i] = static_cast<double>(timer.elapsed_ms());
    }

    double sum = 0.0, mn = DBL_MAX, mx = 0.0;
    for (int i = 0; i < iterations; ++i) {
        sum += times[i];
        if (times[i] < mn) mn = times[i];
        if (times[i] > mx) mx = times[i];
    }
    double avg = sum / iterations;

    double variance = 0.0;
    for (int i = 0; i < iterations; ++i) {
        double d = times[i] - avg;
        variance += d * d;
    }
    double stddev = (iterations > 1) ? sqrt(variance / (iterations - 1)) : 0.0;

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
// 7-point 3D stencil kernel
// ---------------------------------------------------------------------------

__global__ void stencil3d(const float* __restrict__ in,
                          float*       __restrict__ out,
                          int nx, int ny, int nz,
                          float c0, float c1) {
    int ix = blockIdx.x * blockDim.x + threadIdx.x;
    int iy = blockIdx.y * blockDim.y + threadIdx.y;
    int iz = blockIdx.z;

    // Only compute interior points (skip 1-cell boundary)
    if (ix >= 1 && ix < nx - 1 &&
        iy >= 1 && iy < ny - 1 &&
        iz >= 1 && iz < nz - 1) {
        int idx = iz * ny * nx + iy * nx + ix;
        out[idx] = c0 * in[idx]
                 + c1 * (in[idx - 1]       // x-1
                       + in[idx + 1]       // x+1
                       + in[idx - nx]      // y-1
                       + in[idx + nx]      // y+1
                       + in[idx - ny*nx]   // z-1
                       + in[idx + ny*nx]); // z+1
    }
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------

int main(int argc, char** argv) {
    setvbuf(stdout, NULL, _IONBF, 0);

    // Default values from params.json
    int grid_dim      = 64;
    int num_timesteps = 10;
    int iters         = 5;
    int block_size    = 256;

    // Command-line args as fallback
    grid_dim      = parseIntParam(argc, argv, "--grid_dim",      grid_dim);
    num_timesteps = parseIntParam(argc, argv, "--num_timesteps", num_timesteps);
    iters         = parseIterations(argc, argv);
    block_size    = parseIntParam(argc, argv, "--block_size",    block_size);

    // Env vars override
    const char* env_val;
    env_val = getenv("BENCH_PARAM_grid_dim");
    if (env_val) grid_dim = atoi(env_val);
    env_val = getenv("BENCH_PARAM_num_timesteps");
    if (env_val) num_timesteps = atoi(env_val);
    env_val = getenv("BENCH_PARAM_iterations");
    if (env_val) iters = atoi(env_val);
    env_val = getenv("BENCH_PARAM_block_size");
    if (env_val) block_size = atoi(env_val);
    int num_warmup = 0;

    int nx = grid_dim;
    int ny = grid_dim;
    int nz = grid_dim;
    int total = nx * ny * nz;

    float c0 = 0.6f;
    float c1 = (1.0f - c0) / 6.0f;

    // Print device info
    int device_id = 0;
    CUDA_CHECK(cudaGetDevice(&device_id));
    cudaDeviceProp prop;
    CUDA_CHECK(cudaGetDeviceProperties(&prop, device_id));
    fprintf(stderr, "Device: %s (id=%d)\n", prop.name, device_id);
    fprintf(stderr, "Grid: %d×%d×%d  |  Timesteps/launch: %d  |  Iterations: %d\n\n",
            nx, ny, nz, num_timesteps, iters);

    // Host allocation and initialization
    float* h_data = (float*)malloc(total * sizeof(float));
    srand(42);
    for (int i = 0; i < total; ++i) {
        h_data[i] = (float)(rand() % 1000) / 1000.0f;
    }

    // Device allocations (ping-pong buffers)
    float *d_A, *d_B;
    CUDA_CHECK(cudaMalloc(&d_A, total * sizeof(float)));
    CUDA_CHECK(cudaMalloc(&d_B, total * sizeof(float)));
    CUDA_CHECK(cudaMemcpy(d_A, h_data, total * sizeof(float), cudaMemcpyHostToDevice));
    CUDA_CHECK(cudaMemcpy(d_B, h_data, total * sizeof(float), cudaMemcpyHostToDevice));

    // Kernel launch configuration
    dim3 block(16, 16, 1);
    dim3 grid((nx + 15) / 16, (ny + 15) / 16, nz);

    char problemSize[64];
    snprintf(problemSize, sizeof(problemSize), "%dx%dx%d", nx, ny, nz);

    g_num_warmup = num_warmup;
    BenchResult r = runBenchmark(
        "stencil3d", problemSize, iters, [&]() {
            float* src = d_A;
            float* dst = d_B;
            for (int t = 0; t < num_timesteps; ++t) {
                stencil3d<<<grid, block>>>(src, dst, nx, ny, nz, c0, c1);
                // Ping-pong
                float* tmp = src; src = dst; dst = tmp;
            }
            CUDA_CHECK(cudaDeviceSynchronize());
        });

    // Performance metrics
    long long interior_cells = (long long)(nx - 2) * (ny - 2) * (nz - 2);
    double gflops = 8.0 * (double)interior_cells * num_timesteps
                    / (r.avg_ms * 1e-3) / 1e9;
    double gb_s = 8.0 * sizeof(float) * (double)interior_cells * num_timesteps
                  / (r.avg_ms * 1e-3) / 1e9;

    // JSON-lines output
    printf("{\"type\":\"kernel\",\"name\":\"stencil3d\",\"time_ms\":%.6f,"
           "\"params\":{\"grid_dim\":%d,\"num_timesteps\":%d,\"iterations\":%d,"
           "\"block_size\":%d}}\n",
           r.avg_ms, grid_dim, num_timesteps, iters, block_size);

    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"gflops\",\"value\":%.2f},"
           "{\"name\":\"bandwidth_gbps\",\"value\":%.2f}]}\n",
           r.avg_ms * iters, gflops, gb_s);

    fprintf(stderr, "GFLOPS: %.2f  |  GB/s: %.2f  (avg %.4f ms, %d timesteps/iter)\n",
            gflops, gb_s, r.avg_ms, num_timesteps);

    CUDA_CHECK(cudaFree(d_A));
    CUDA_CHECK(cudaFree(d_B));
    free(h_data);

    return 0;
}
