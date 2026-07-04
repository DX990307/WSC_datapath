// rodinia_srad.cu — Rodinia SRAD benchmark (CUDA, self-contained)
//
// Speckle-Reducing Anisotropic Diffusion (SRAD): iterative image smoothing
// using a Perona-Malik anisotropic diffusion scheme on a 2D float image.
//
// Two-phase stencil per iteration:
//   Phase 1 (srad1): Compute directional gradients (dN, dS, dW, dE) and the
//                    diffusion coefficient c for each pixel.
//   Phase 2 (srad2): Update the image J using the diffusion coefficients.
//
// Native CUDA implementation.
//
// Usage:
//   ./rodinia_srad [--image_size N] [--num_iterations K] [--block_size B]
//
//   --image_size N      Image dimension (N×N)              (default: 512)
//   --num_iterations K  SRAD iterations per timed launch   (default: 50)
//   --block_size B      Thread-block dimension (B×B)       (default: 16)
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
// SRAD Kernels
// ---------------------------------------------------------------------------

// Phase 1: Compute directional gradients and diffusion coefficient c.
//
//   dN[i] = J[iN*cols+col] - J[idx]   (north gradient)
//   dS[i] = J[iS*cols+col] - J[idx]   (south gradient)
//   dW[i] = J[row*cols+jW] - J[idx]   (west gradient)
//   dE[i] = J[row*cols+jE] - J[idx]   (east gradient)
//
//   G2   = (dN² + dS² + dW² + dE²) / Jc²
//   L    = (dN + dS + dW + dE) / Jc
//   qsqr = (0.5*G2 - (1/16)*L²) / (1 + 0.25*L)²
//   c[i] = 1 / (1 + (qsqr - q0sqr) / (q0sqr * (1 + q0sqr)))   [clamped 0..1]
//
__global__ void srad1(const float* __restrict__ J,
                      float* __restrict__ dN, float* __restrict__ dS,
                      float* __restrict__ dW, float* __restrict__ dE,
                      float* __restrict__ c,
                      int rows, int cols, float q0sqr) {
    int col = blockIdx.x * blockDim.x + threadIdx.x;
    int row = blockIdx.y * blockDim.y + threadIdx.y;

    if (row >= rows || col >= cols) return;

    int idx = row * cols + col;

    // Clamped neighbor indices
    int iN = (row > 0)        ? (row - 1) : 0;
    int iS = (row < rows - 1) ? (row + 1) : (rows - 1);
    int jW = (col > 0)        ? (col - 1) : 0;
    int jE = (col < cols - 1) ? (col + 1) : (cols - 1);

    float Jc = J[idx];

    float dn = J[iN * cols + col] - Jc;
    float ds = J[iS * cols + col] - Jc;
    float dw = J[row * cols + jW] - Jc;
    float de = J[row * cols + jE] - Jc;

    dN[idx] = dn;
    dS[idx] = ds;
    dW[idx] = dw;
    dE[idx] = de;

    float G2   = (dn*dn + ds*ds + dw*dw + de*de) / (Jc * Jc);
    float L    = (dn + ds + dw + de) / Jc;
    float num  = (0.5f * G2) - ((1.0f / 16.0f) * (L * L));
    float den  = 1.0f + (0.25f * L);
    float qsqr = num / (den * den);

    // Perona-Malik diffusion coefficient
    den = (qsqr - q0sqr) / (q0sqr * (1.0f + q0sqr));
    float ci = 1.0f / (1.0f + den);
    if (ci < 0.0f) ci = 0.0f;
    if (ci > 1.0f) ci = 1.0f;
    c[idx] = ci;
}

// Phase 2: Update image using the diffusion coefficients.
//
//   cN = c[idx]          cS = c[iS*cols+col]
//   cW = c[idx]          cE = c[row*cols+jE]
//   D  = cN*dN + cS*dS + cW*dW + cE*dE
//   J[idx] += 0.25 * lambda * D
//
__global__ void srad2(float* __restrict__ J,
                      const float* __restrict__ dN, const float* __restrict__ dS,
                      const float* __restrict__ dW, const float* __restrict__ dE,
                      const float* __restrict__ c,
                      int rows, int cols, float lambda) {
    int col = blockIdx.x * blockDim.x + threadIdx.x;
    int row = blockIdx.y * blockDim.y + threadIdx.y;

    if (row >= rows || col >= cols) return;

    int idx = row * cols + col;

    // South and East neighbor diffusion coefficients
    int iS = (row < rows - 1) ? (row + 1) : (rows - 1);
    int jE = (col < cols - 1) ? (col + 1) : (cols - 1);

    float cN = c[idx];
    float cS = c[iS * cols + col];
    float cW = c[idx];
    float cE = c[row * cols + jE];

    float D = cN * dN[idx] + cS * dS[idx] + cW * dW[idx] + cE * dE[idx];

    J[idx] += 0.25f * lambda * D;
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------

int main(int argc, char** argv) {
    setvbuf(stdout, NULL, _IONBF, 0);

    // Defaults
    int image_size  = 512;
    int num_iters   = 50;
    int block_dim   = 16;

    // Command-line fallback
    image_size = parseIntParam(argc, argv, "--image_size", image_size);
    num_iters  = parseIntParam(argc, argv, "--num_iterations", num_iters);
    block_dim  = parseIntParam(argc, argv, "--block_size", block_dim);

    float lambda = 0.25f;
    for (int i = 1; i < argc - 1; ++i) {
        if (strcmp(argv[i], "--lambda") == 0) {
            lambda = (float)atof(argv[i + 1]);
            break;
        }
    }

    // Environment variable overrides
    const char* env_val;
    env_val = getenv("BENCH_PARAM_image_size");
    if (env_val) image_size = atoi(env_val);
    env_val = getenv("BENCH_PARAM_num_iterations");
    if (env_val) num_iters = atoi(env_val);
    env_val = getenv("BENCH_PARAM_block_size");
    if (env_val) block_dim = atoi(env_val);
    int num_warmup = 0;

    int rows = image_size;
    int cols = image_size;
    int size = rows * cols;

    // Speckle noise parameter (variance of uniform noise)
    float q0sqr = 0.05f;

    // Print device info
    int device_id = 0;
    CUDA_CHECK(cudaGetDevice(&device_id));
    cudaDeviceProp prop;
    CUDA_CHECK(cudaGetDeviceProperties(&prop, device_id));
    fprintf(stderr, "Device: %s (id=%d)\n", prop.name, device_id);
    fprintf(stderr, "Image: %d×%d  |  SRAD iters/launch: %d\n\n",
            rows, cols, num_iters);

    // Host allocation — random float image in [0, 1]
    float* h_J      = (float*)malloc(size * sizeof(float));
    float* h_J_orig = (float*)malloc(size * sizeof(float));
    srand(42);
    for (int i = 0; i < size; ++i) {
        h_J[i] = (float)rand() / (float)RAND_MAX;
    }
    memcpy(h_J_orig, h_J, size * sizeof(float));

    // Device allocations
    float *d_J, *d_dN, *d_dS, *d_dW, *d_dE, *d_c;
    CUDA_CHECK(cudaMalloc(&d_J,  size * sizeof(float)));
    CUDA_CHECK(cudaMalloc(&d_dN, size * sizeof(float)));
    CUDA_CHECK(cudaMalloc(&d_dS, size * sizeof(float)));
    CUDA_CHECK(cudaMalloc(&d_dW, size * sizeof(float)));
    CUDA_CHECK(cudaMalloc(&d_dE, size * sizeof(float)));
    CUDA_CHECK(cudaMalloc(&d_c,  size * sizeof(float)));

    dim3 block(block_dim, block_dim);
    dim3 grid((cols + block_dim - 1) / block_dim,
              (rows + block_dim - 1) / block_dim);

    char problemSize[64];
    snprintf(problemSize, sizeof(problemSize), "%dx%d", rows, cols);

    BenchResult r = runBenchmark(
        "rodinia_srad", problemSize, 1, num_warmup, [&]() {
            // Reset image to original before each timed run
            CUDA_CHECK(cudaMemcpy(d_J, h_J_orig, size * sizeof(float),
                                cudaMemcpyHostToDevice));

            for (int k = 0; k < num_iters; ++k) {
                srad1<<<grid, block, 0, 0>>>(d_J, d_dN, d_dS, d_dW, d_dE, d_c, rows, cols, q0sqr);
                srad2<<<grid, block, 0, 0>>>(d_J, d_dN, d_dS, d_dW, d_dE, d_c, rows, cols, lambda);
            }
            CUDA_CHECK(cudaDeviceSynchronize());
        });

    // Effective bandwidth
    double bytes_per_iter = 13.0 * (double)size * sizeof(float);
    double total_bytes    = bytes_per_iter * num_iters;
    double bw_gb = total_bytes / (r.avg_ms * 1e-3) / 1e9;
    double total_ms = r.avg_ms;

    // JSON-lines kernel events
    printf("{\"type\":\"kernel\",\"name\":\"srad1\",\"time_ms\":%.6f,"
           "\"params\":{\"image_size\":%d,\"num_iterations\":%d,\"block_size\":%d}}\n",
           r.avg_ms, image_size, num_iters, block_dim);

    printf("{\"type\":\"kernel\",\"name\":\"srad2\",\"time_ms\":%.6f,"
           "\"params\":{\"image_size\":%d,\"num_iterations\":%d,\"block_size\":%d}}\n",
           r.avg_ms, image_size, num_iters, block_dim);

    // JSON-lines summary event
    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"effective_bandwidth_gbps\",\"value\":%.2f}]}\n",
           total_ms, bw_gb);

    fprintf(stderr, "Effective bandwidth: %.2f GB/s  (avg %.4f ms, %d SRAD iters/launch)\n",
            bw_gb, r.avg_ms, num_iters);

    CUDA_CHECK(cudaFree(d_J));
    CUDA_CHECK(cudaFree(d_dN));
    CUDA_CHECK(cudaFree(d_dS));
    CUDA_CHECK(cudaFree(d_dW));
    CUDA_CHECK(cudaFree(d_dE));
    CUDA_CHECK(cudaFree(d_c));
    free(h_J);
    free(h_J_orig);

    return 0;
}
