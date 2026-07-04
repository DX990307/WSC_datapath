// rodinia_hotspot.cu — Rodinia Hotspot benchmark (CUDA, self-contained)
//
// Iterative 2D stencil thermal simulation for chip temperature estimation.
// Each cell's temperature is updated based on its four neighbors (N/S/E/W),
// the local power density, and thermal resistances.
//
// Native CUDA implementation.
//
// Usage:
//   ./rodinia_hotspot [--grid_size N] [--block_size B] [--num_iterations K]
//
//   --grid_size N       Grid dimension (N×N)         (default: 512)
//   --block_size B      Thread-block dimension (B×B) (default: 16)
//   --num_iterations K  Stencil time-steps per timed launch (default: 10)
//
// Output (stdout): CSV row — kernel_name,problem_size,iterations,avg_ms,min_ms,max_ms,stddev_ms
// Output (stderr): Effective bandwidth in GB/s

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

template <typename Func>
static BenchResult runBenchmark(const char* kernel_name,
                                const char* problem_size,
                                int         iterations,
                                int         num_warmup,
                                Func        func) {
    BenchmarkTimer timer;

    // Warmup
    for (int w = 0; w < num_warmup; ++w) {
        func();
        CUDA_CHECK(cudaDeviceSynchronize());
    }

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
// Chip thermal constants (Rodinia defaults)
// ---------------------------------------------------------------------------
#define AMB_TEMP    80.0f
#define CHIP_HEIGHT 0.016f
#define CHIP_WIDTH  0.016f
#define T_CHIP      0.0005f
#define K_SI        100.0f
#define C_SI        1.75e6f

// ---------------------------------------------------------------------------
// Hotspot stencil kernel
// ---------------------------------------------------------------------------
__global__ void hotspot_kernel(const float* __restrict__ temp_src,
                                float* __restrict__ temp_dst,
                                const float* __restrict__ power,
                                int grid_cols, int grid_rows,
                                float step_div_cap,
                                float Rx_1, float Ry_1, float Rz_1) {
    int col = blockIdx.x * blockDim.x + threadIdx.x;
    int row = blockIdx.y * blockDim.y + threadIdx.y;

    if (col < grid_cols && row < grid_rows) {
        int idx = row * grid_cols + col;

        float temp_c = temp_src[idx];
        float temp_n = (row > 0)             ? temp_src[(row - 1) * grid_cols + col] : temp_c;
        float temp_s = (row < grid_rows - 1) ? temp_src[(row + 1) * grid_cols + col] : temp_c;
        float temp_w = (col > 0)             ? temp_src[row * grid_cols + (col - 1)] : temp_c;
        float temp_e = (col < grid_cols - 1) ? temp_src[row * grid_cols + (col + 1)] : temp_c;

        float delta = step_div_cap *
            (power[idx]
             + (temp_n + temp_s - 2.0f * temp_c) * Ry_1
             + (temp_w + temp_e - 2.0f * temp_c) * Rx_1
             + (AMB_TEMP - temp_c) * Rz_1);

        temp_dst[idx] = temp_c + delta;
    }
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------
int main(int argc, char** argv) {
    setvbuf(stdout, NULL, _IONBF, 0);

    int grid_size      = parseIntParam(argc, argv, "--grid_size",      512);
    int block_dim      = parseIntParam(argc, argv, "--block_size",      16);
    int num_iterations = parseIntParam(argc, argv, "--num_iterations",  10);

    const char* env_val;
    env_val = getenv("BENCH_PARAM_grid_size");
    if (env_val) grid_size = atoi(env_val);
    env_val = getenv("BENCH_PARAM_block_size");
    if (env_val) block_dim = atoi(env_val);
    env_val = getenv("BENCH_PARAM_num_iterations");
    if (env_val) num_iterations = atoi(env_val);
    int num_warmup = 0;

    int grid_rows  = grid_size;
    int grid_cols  = grid_size;
    int total_cells = grid_rows * grid_cols;

    // Compute thermal parameters
    float grid_height = CHIP_HEIGHT / grid_rows;
    float grid_width  = CHIP_WIDTH  / grid_cols;
    float cap         = C_SI * T_CHIP * grid_height * grid_width;
    float Rx          = grid_width  / (2.0f * K_SI * T_CHIP * grid_height);
    float Ry          = grid_height / (2.0f * K_SI * T_CHIP * grid_width);
    float Rz          = T_CHIP / (K_SI * grid_height * grid_width);
    float max_slope   = K_SI / (0.5f * T_CHIP * C_SI);
    float step        = 0.001f / max_slope;
    float step_div_cap = step / cap;
    float Rx_1        = 1.0f / Rx;
    float Ry_1        = 1.0f / Ry;
    float Rz_1        = 1.0f / Rz;

    // Print device info
    int device_id = 0;
    CUDA_CHECK(cudaGetDevice(&device_id));
    cudaDeviceProp prop;
    CUDA_CHECK(cudaGetDeviceProperties(&prop, device_id));
    fprintf(stderr, "Device: %s (id=%d)\n", prop.name, device_id);
    fprintf(stderr, "Grid: %d×%d  |  Block: %d×%d  |  Steps/launch: %d\n\n",
            grid_rows, grid_cols, block_dim, block_dim, num_iterations);

    // Host allocations
    float* h_temp  = (float*)malloc(total_cells * sizeof(float));
    float* h_power = (float*)malloc(total_cells * sizeof(float));

    // Generate synthetic temperature and power grids
    srand(42);
    for (int i = 0; i < total_cells; ++i) {
        h_temp[i]  = AMB_TEMP + (float)(rand() % 200) / 10.0f;
        h_power[i] = (float)(rand() % 100) / 500.0f;
    }

    // Device allocations
    float *d_temp_src, *d_temp_dst, *d_power;
    CUDA_CHECK(cudaMalloc(&d_temp_src, total_cells * sizeof(float)));
    CUDA_CHECK(cudaMalloc(&d_temp_dst, total_cells * sizeof(float)));
    CUDA_CHECK(cudaMalloc(&d_power,    total_cells * sizeof(float)));

    CUDA_CHECK(cudaMemcpy(d_power, h_power, total_cells * sizeof(float), cudaMemcpyHostToDevice));

    dim3 block(block_dim, block_dim);
    dim3 grid((grid_cols + block_dim - 1) / block_dim,
              (grid_rows + block_dim - 1) / block_dim);

    char problemSize[64];
    snprintf(problemSize, sizeof(problemSize), "%d", grid_size);

    BenchResult r = runBenchmark(
        "hotspot", problemSize, 1, num_warmup, [&]() {
            // Reset temperature for each benchmark iteration
            CUDA_CHECK(cudaMemcpy(d_temp_src, h_temp,
                                total_cells * sizeof(float), cudaMemcpyHostToDevice));

            float* src = d_temp_src;
            float* dst = d_temp_dst;

            for (int k = 0; k < num_iterations; ++k) {
                hotspot_kernel<<<grid, block, 0, 0>>>(src, dst, d_power, grid_cols, grid_rows, step_div_cap, Rx_1, Ry_1, Rz_1);
                // Ping-pong buffers
                float* tmp = src;
                src = dst;
                dst = tmp;
            }
            CUDA_CHECK(cudaDeviceSynchronize());
        });

    // Effective bandwidth per stencil step: 2 reads (temp_src, power) + 1 write (temp_dst)
    double bytes_per_step = 3.0 * (double)total_cells * sizeof(float);
    double total_bytes    = bytes_per_step * num_iterations;
    double bw_gb = total_bytes / (r.avg_ms * 1e-3) / 1e9;
    fprintf(stderr, "Effective bandwidth: %.2f GB/s  (avg %.4f ms, %d steps/iter)\n",
            bw_gb, r.avg_ms, num_iterations);

    // JSON-lines output
    printf("{\"type\":\"kernel\",\"name\":\"hotspot_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"grid_size\":%d,\"block_size\":%d,\"num_iterations\":%d,"
           "}}\n",
           r.avg_ms, grid_size, block_dim, num_iterations);
    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"bandwidth_gbps\",\"value\":%.2f}]}\n",
           r.avg_ms, bw_gb);

    CUDA_CHECK(cudaFree(d_temp_src));
    CUDA_CHECK(cudaFree(d_temp_dst));
    CUDA_CHECK(cudaFree(d_power));
    free(h_temp);
    free(h_power);

    return 0;
}
