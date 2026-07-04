// rodinia_hotspot3d.cu — Rodinia HotSpot3D benchmark (CUDA, self-contained)
//
// 3D stencil thermal simulation extending the 2D HotSpot benchmark.
// Each cell's temperature is updated based on its six neighbors (±x,±y,±z),
// local power density, and thermal resistances in an NxNxN grid.
//
// Native CUDA implementation.
//
// Usage:
//   ./rodinia_hotspot3d [--grid_size N] [--block_size B] [--num_iterations K]
//
// Output (stdout): JSON-lines
// Output (stderr): human-readable results

#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <cmath>
#include <cfloat>
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
// Argument parsing
// ---------------------------------------------------------------------------

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

static float parseFloatParam(int argc, char** argv, const char* name, float defaultVal) {
    for (int i = 1; i < argc - 1; ++i) {
        if (strcmp(argv[i], name) == 0) {
            float v = (float)atof(argv[i + 1]);
            if (v > 0.0f) return v;
            break;
        }
    }
    return defaultVal;
}

// ---------------------------------------------------------------------------
// Chip thermal constants (Rodinia defaults, extended to 3D)
// ---------------------------------------------------------------------------
#define CHIP_HEIGHT 0.016f
#define CHIP_WIDTH  0.016f
#define CHIP_DEPTH  0.016f
#define T_CHIP      0.0005f
#define K_SI        100.0f
#define C_SI        1.75e6f

// ---------------------------------------------------------------------------
// 3D Hotspot stencil kernel
// ---------------------------------------------------------------------------
__global__ void hotspot3d_kernel(const float* __restrict__ temp_src,
                                  float* __restrict__ temp_dst,
                                  const float* __restrict__ power,
                                  int nx, int ny, int nz,
                                  float step_div_cap,
                                  float Rx_1, float Ry_1, float Rz_1,
                                  float Ra_1, float amb_temp) {
    int x = blockIdx.x * blockDim.x + threadIdx.x;
    int y = blockIdx.y * blockDim.y + threadIdx.y;
    int z = blockIdx.z * blockDim.z + threadIdx.z;

    if (x >= nx || y >= ny || z >= nz) return;

    int idx = z * ny * nx + y * nx + x;

    float tc = temp_src[idx];

    // ±x neighbors (clamped boundary)
    float txm = (x > 0)      ? temp_src[z * ny * nx + y * nx + (x - 1)] : tc;
    float txp = (x < nx - 1) ? temp_src[z * ny * nx + y * nx + (x + 1)] : tc;

    // ±y neighbors (clamped boundary)
    float tym = (y > 0)      ? temp_src[z * ny * nx + (y - 1) * nx + x] : tc;
    float typ = (y < ny - 1) ? temp_src[z * ny * nx + (y + 1) * nx + x] : tc;

    // ±z neighbors (clamped boundary)
    float tzm = (z > 0)      ? temp_src[(z - 1) * ny * nx + y * nx + x] : tc;
    float tzp = (z < nz - 1) ? temp_src[(z + 1) * ny * nx + y * nx + x] : tc;

    float delta = step_div_cap * (
        power[idx]
        + (txm + txp - 2.0f * tc) * Rx_1
        + (tym + typ - 2.0f * tc) * Ry_1
        + (tzm + tzp - 2.0f * tc) * Rz_1
        + (amb_temp - tc) * Ra_1
    );

    temp_dst[idx] = tc + delta;
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------
int main(int argc, char** argv) {
    setvbuf(stdout, NULL, _IONBF, 0);

    // Defaults from params.json
    int grid_size      = 64;
    int block_dim      = 8;
    int num_iterations = 10;
    float amb_temp     = 80.0f;

    // Command-line fallback
    grid_size      = parseIntParam(argc, argv, "--grid_size", grid_size);
    block_dim      = parseIntParam(argc, argv, "--block_size", block_dim);
    num_iterations = parseIntParam(argc, argv, "--num_iterations", num_iterations);
    amb_temp       = parseFloatParam(argc, argv, "--amb_temp", amb_temp);

    // Environment variable overrides
    const char* env_val;
    env_val = getenv("BENCH_PARAM_grid_size");
    if (env_val) grid_size = atoi(env_val);
    env_val = getenv("BENCH_PARAM_block_size");
    if (env_val) block_dim = atoi(env_val);
    env_val = getenv("BENCH_PARAM_num_iterations");
    if (env_val) num_iterations = atoi(env_val);
    env_val = getenv("BENCH_PARAM_amb_temp");
    if (env_val) amb_temp = (float)atof(env_val);
    int num_warmup = 0;

    int nx = grid_size, ny = grid_size, nz = grid_size;
    long long total_cells = (long long)nx * ny * nz;

    // Compute thermal parameters for 3D
    float dx = CHIP_WIDTH  / nx;
    float dy = CHIP_HEIGHT / ny;
    float dz = CHIP_DEPTH  / nz;

    float cap       = C_SI * T_CHIP * dx * dy;
    float Rx        = dx / (2.0f * K_SI * T_CHIP * dy);
    float Ry        = dy / (2.0f * K_SI * T_CHIP * dx);
    float Rz        = dz / (2.0f * K_SI * T_CHIP * dx);
    float Ra        = T_CHIP / (K_SI * dx * dy);
    float max_slope = K_SI / (0.5f * T_CHIP * C_SI);
    float step      = 0.001f / max_slope;
    float step_div_cap = step / cap;
    float Rx_1      = 1.0f / Rx;
    float Ry_1      = 1.0f / Ry;
    float Rz_1      = 1.0f / Rz;
    float Ra_1      = 1.0f / Ra;

    // Print device info
    int device_id = 0;
    CUDA_CHECK(cudaGetDevice(&device_id));
    cudaDeviceProp prop;
    CUDA_CHECK(cudaGetDeviceProperties(&prop, device_id));
    fprintf(stderr, "Device: %s (id=%d)\n", prop.name, device_id);
    fprintf(stderr, "Grid: %d×%d×%d  |  Block: %d³  |  Steps/launch: %d  |  "
            "Iterations: %d warmup + %d timed\n\n",
            nx, ny, nz, block_dim, num_iterations, num_warmup);

    // Host allocations
    size_t bytes = (size_t)total_cells * sizeof(float);
    float* h_temp  = (float*)malloc(bytes);
    float* h_power = (float*)malloc(bytes);

    // Generate synthetic temperature and power grids
    srand(42);
    for (long long i = 0; i < total_cells; ++i) {
        h_temp[i]  = amb_temp + (float)(rand() % 200) / 10.0f;
        h_power[i] = (float)(rand() % 100) / 500.0f;
    }

    // Device allocations
    float *d_temp_src, *d_temp_dst, *d_power;
    CUDA_CHECK(cudaMalloc(&d_temp_src, bytes));
    CUDA_CHECK(cudaMalloc(&d_temp_dst, bytes));
    CUDA_CHECK(cudaMalloc(&d_power,    bytes));

    CUDA_CHECK(cudaMemcpy(d_power, h_power, bytes, cudaMemcpyHostToDevice));

    dim3 block(block_dim, block_dim, block_dim);
    dim3 grid_dim((nx + block_dim - 1) / block_dim,
                  (ny + block_dim - 1) / block_dim,
                  (nz + block_dim - 1) / block_dim);

    // Lambda: run num_iterations stencil steps with ping-pong
    auto run_stencil = [&]() {
        CUDA_CHECK(cudaMemcpy(d_temp_src, h_temp, bytes, cudaMemcpyHostToDevice));

        float* src = d_temp_src;
        float* dst = d_temp_dst;

        for (int k = 0; k < num_iterations; ++k) {
            hotspot3d_kernel<<<grid_dim, block, 0, 0>>>(
                src, dst, d_power, nx, ny, nz,
                step_div_cap, Rx_1, Ry_1, Rz_1, Ra_1, amb_temp);
            float* tmp = src; src = dst; dst = tmp;
        }
    };

    // Warmup
    for (int w = 0; w < num_warmup; ++w) {
        run_stencil();
        CUDA_CHECK(cudaDeviceSynchronize());
    }

    // Timed iterations
    cudaEvent_t evStart, evStop;
    CUDA_CHECK(cudaEventCreate(&evStart));
    CUDA_CHECK(cudaEventCreate(&evStop));

    std::vector<double> times(1);
    for (int i = 0; i < 1; ++i) {
        CUDA_CHECK(cudaMemcpy(d_temp_src, h_temp, bytes, cudaMemcpyHostToDevice));

        float* src = d_temp_src;
        float* dst = d_temp_dst;

        CUDA_CHECK(cudaEventRecord(evStart, 0));
        for (int k = 0; k < num_iterations; ++k) {
            hotspot3d_kernel<<<grid_dim, block, 0, 0>>>(
                src, dst, d_power, nx, ny, nz,
                step_div_cap, Rx_1, Ry_1, Rz_1, Ra_1, amb_temp);
            float* tmp = src; src = dst; dst = tmp;
        }
        CUDA_CHECK(cudaEventRecord(evStop, 0));
        CUDA_CHECK(cudaEventSynchronize(evStop));

        float ms = 0.0f;
        CUDA_CHECK(cudaEventElapsedTime(&ms, evStart, evStop));
        times[i] = (double)ms;
    }

    CUDA_CHECK(cudaEventDestroy(evStart));
    CUDA_CHECK(cudaEventDestroy(evStop));

    // Compute statistics
    double sum = 0.0;
    for (int i = 0; i < 1; ++i) sum += times[i];
    double avg_ms = sum / 1;
    double total_ms = sum;

    // Effective bandwidth: 2 reads (temp_src, power) + 1 write (temp_dst) per step
    double bytes_per_step = 3.0 * (double)total_cells * sizeof(float);
    double total_bytes    = bytes_per_step * num_iterations;
    double bw_gb = total_bytes / (avg_ms * 1e-3) / 1e9;

    fprintf(stderr, "Effective bandwidth: %.2f GB/s  (avg %.4f ms, %d steps/iter)\n",
            bw_gb, avg_ms, num_iterations);

    // JSON-lines output
    printf("{\"type\":\"kernel\",\"name\":\"hotspot3d_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"grid_size\":%d,\"block_size\":%d,\"num_iterations\":%d,"
           "\"amb_temp\":%.1f}}\n",
           avg_ms, grid_size, block_dim, num_iterations, amb_temp);

    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"bandwidth_gbps\",\"value\":%.2f}]}\n",
           total_ms, bw_gb);

    // Cleanup
    CUDA_CHECK(cudaFree(d_temp_src));
    CUDA_CHECK(cudaFree(d_temp_dst));
    CUDA_CHECK(cudaFree(d_power));
    free(h_temp);
    free(h_power);

    return 0;
}
