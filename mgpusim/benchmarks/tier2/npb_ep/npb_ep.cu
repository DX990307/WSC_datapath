// npb_ep.cu — NAS Parallel Benchmarks Embarrassingly Parallel (EP)
//
// Generates N pairs of Gaussian random deviates using Box-Muller transform
// with a per-thread linear congruential RNG, then counts results into 10
// annular bins.  Purely compute-bound workload.
//
// Native CUDA implementation.
//
// Usage:
//   ./npb_ep [--size N]
//
//   --size N         Number of Gaussian pairs (default: 16777216 = 2^24)
//
// Output (stdout): CSV row — npb_ep,<N>,<time_ms>,<Gpairs_per_sec>
// Output (stderr): human-readable results

#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <cmath>
#include <vector>
#include <cuda_runtime.h>

#ifndef M_PI
#define M_PI 3.14159265358979323846
#endif

// Number of annular bins
#define NUM_BINS 10

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

// ---------------------------------------------------------------------------
// LCG RNG — integer-based, works identically on CPU/GPU/Metal
//   seed = seed * 1103515245 + 12345  (ANSI C LCG, mod 2^32 by overflow)
// ---------------------------------------------------------------------------

__device__ __host__ static inline unsigned int lcg_next(unsigned int seed) {
    return seed * 1103515245u + 12345u;
}

// ---------------------------------------------------------------------------
// Kernel: EP — generate Gaussian pairs and bin them
//   Each thread generates one pair of uniform random numbers, applies
//   Box-Muller transform, and atomically increments the appropriate bin.
//   bins[NUM_BINS] is stored in global memory.
// ---------------------------------------------------------------------------

__global__ void ep_kernel(int N, unsigned long long* bins) {
    int idx = blockIdx.x * blockDim.x + threadIdx.x;
    if (idx >= N) return;

    // Per-thread seed derived from thread index
    unsigned int seed = (unsigned int)(idx + 1);
    // Advance RNG a few steps for decorrelation
    seed = lcg_next(seed);
    seed = lcg_next(seed);

    // Generate two uniform random numbers in (0, 1)
    seed = lcg_next(seed);
    float u1 = (float)seed / 4294967296.0f;
    seed = lcg_next(seed);
    float u2 = (float)seed / 4294967296.0f;

    // Avoid log(0)
    if (u1 < 1e-10f) u1 = 1e-10f;

    // Box-Muller transform
    float r = sqrtf(-2.0f * logf(u1));
    float theta = 2.0f * (float)M_PI * u2;
    float x1 = r * cosf(theta);
    float x2 = r * sinf(theta);

    // Compute distance squared
    float t = x1 * x1 + x2 * x2;

    // Bin index: floor(sqrt(t)), capped at NUM_BINS-1
    int bin = (int)sqrtf(t);
    if (bin >= NUM_BINS) bin = NUM_BINS - 1;
    if (bin < 0) bin = 0;

    atomicAdd(&bins[bin], 1ULL);
}

// ---------------------------------------------------------------------------
// CPU reference for verification
// ---------------------------------------------------------------------------

static void ep_cpu(int N, unsigned long long* bins) {
    for (int i = 0; i < NUM_BINS; ++i) bins[i] = 0;

    for (int idx = 0; idx < N; ++idx) {
        unsigned int seed = (unsigned int)(idx + 1);
        seed = lcg_next(seed);
        seed = lcg_next(seed);

        seed = lcg_next(seed);
        float u1 = (float)seed / 4294967296.0f;
        seed = lcg_next(seed);
        float u2 = (float)seed / 4294967296.0f;

        if (u1 < 1e-10f) u1 = 1e-10f;

        float r = sqrtf(-2.0f * logf(u1));
        float theta = 2.0f * (float)M_PI * u2;
        float x1 = r * cosf(theta);
        float x2 = r * sinf(theta);

        float t = x1 * x1 + x2 * x2;

        int bin = (int)sqrtf(t);
        if (bin >= NUM_BINS) bin = NUM_BINS - 1;
        if (bin < 0) bin = 0;

        bins[bin] += 1;
    }
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------

int main(int argc, char** argv) {
    setvbuf(stdout, NULL, _IONBF, 0);

    // Defaults from params.json
    int N          = 16777216;  // 2^24
    int block_size = 256;
    int seed_param = 271828183;

    // Command-line fallback
    N          = parseIntParam(argc, argv, "--size", N);

    // Environment variable overrides
    const char* env_val;
    env_val = getenv("BENCH_PARAM_size");
    if (env_val) N = atoi(env_val);
    env_val = getenv("BENCH_PARAM_block_size");
    if (env_val) block_size = atoi(env_val);
    env_val = getenv("BENCH_PARAM_seed");
    if (env_val) seed_param = atoi(env_val);
    int num_warmup = 0;


    // Print device info
    int device_id = 0;
    CUDA_CHECK(cudaGetDevice(&device_id));
    cudaDeviceProp prop;
    CUDA_CHECK(cudaGetDeviceProperties(&prop, device_id));
    fprintf(stderr, "Device: %s (id=%d)\n", prop.name, device_id);
    fprintf(stderr, "EP pairs: %d (2^%.0f) warmup + %d timed\n\n",
            N, log2((double)N), num_warmup);

    // Device allocation for bins
    unsigned long long* d_bins;
    CUDA_CHECK(cudaMalloc(&d_bins, NUM_BINS * sizeof(unsigned long long)));

    // Kernel launch config
    int blockSize = block_size;
    int gridSize  = (N + blockSize - 1) / blockSize;

    // Lambda: run EP kernel
    auto run_ep = [&]() {
        CUDA_CHECK(cudaMemset(d_bins, 0, NUM_BINS * sizeof(unsigned long long)));
        ep_kernel<<<dim3(gridSize), dim3(blockSize), 0, 0>>>(N, d_bins);
    };

    // -------------------------------------------------------------------
    // Warmup
    // -------------------------------------------------------------------
    for (int w = 0; w < num_warmup; ++w) {
        run_ep();
        CUDA_CHECK(cudaDeviceSynchronize());
    }

    // -------------------------------------------------------------------
    // Timed iterations
    // -------------------------------------------------------------------
    cudaEvent_t evStart, evStop;
    CUDA_CHECK(cudaEventCreate(&evStart));
    CUDA_CHECK(cudaEventCreate(&evStop));

    std::vector<double> times(1);
    for (int i = 0; i < 1; ++i) {
        CUDA_CHECK(cudaEventRecord(evStart, 0));
        run_ep();
        CUDA_CHECK(cudaEventRecord(evStop, 0));
        CUDA_CHECK(cudaEventSynchronize(evStop));

        float ms = 0.0f;
        CUDA_CHECK(cudaEventElapsedTime(&ms, evStart, evStop));
        times[i] = (double)ms;
    }

    CUDA_CHECK(cudaEventDestroy(evStart));
    CUDA_CHECK(cudaEventDestroy(evStop));

    // Compute average time
    double sum = 0.0;
    for (int i = 0; i < 1; ++i) sum += times[i];
    double avg_ms = sum / 1;

    // G pairs/sec = N / time_s / 1e9
    double gpairs_sec = (double)N / (avg_ms * 1e-3) / 1e9;

    double total_ms = sum;

    // JSON-lines kernel event
    printf("{\"type\":\"kernel\",\"name\":\"ep_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"size\":%d\"block_size\":%d,"
           "\"seed\":%d}}\n",
           avg_ms, N, block_size, seed_param);

    // JSON-lines summary event
    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"gpairs_per_sec\",\"value\":%.2f}]}\n",
           total_ms, gpairs_sec);

    // Human-readable output to stderr
    fprintf(stderr, "Average time: %.4f ms\n", avg_ms);
    fprintf(stderr, "Performance:  %.4f Gpairs/sec\n", gpairs_sec);

    // -------------------------------------------------------------------
    // Verification: compare GPU bins against CPU reference
    // -------------------------------------------------------------------
    unsigned long long h_bins[NUM_BINS];
    CUDA_CHECK(cudaMemcpy(h_bins, d_bins, NUM_BINS * sizeof(unsigned long long),
                        cudaMemcpyDeviceToHost));

    // Use a small N for verification to keep CPU time reasonable
    int verifyN = (N <= 1048576) ? N : 1048576;

    // Re-run GPU with verifyN if different
    unsigned long long h_verify_gpu[NUM_BINS];
    if (verifyN != N) {
        int vGridSize = (verifyN + blockSize - 1) / blockSize;
        CUDA_CHECK(cudaMemset(d_bins, 0, NUM_BINS * sizeof(unsigned long long)));
        ep_kernel<<<dim3(vGridSize), dim3(blockSize), 0, 0>>>(verifyN, d_bins);
        CUDA_CHECK(cudaDeviceSynchronize());
        CUDA_CHECK(cudaMemcpy(h_verify_gpu, d_bins, NUM_BINS * sizeof(unsigned long long),
                            cudaMemcpyDeviceToHost));
    } else {
        for (int i = 0; i < NUM_BINS; ++i) h_verify_gpu[i] = h_bins[i];
    }

    unsigned long long cpu_bins[NUM_BINS];
    ep_cpu(verifyN, cpu_bins);

    fprintf(stderr, "\nBin counts (GPU vs CPU, N=%d):\n", verifyN);
    int errors = 0;
    for (int i = 0; i < NUM_BINS; ++i) {
        fprintf(stderr, "  Bin %d: GPU=%llu  CPU=%llu\n", i, h_verify_gpu[i], cpu_bins[i]);
        if (h_verify_gpu[i] != cpu_bins[i]) errors++;
    }

    if (errors > 0)
        fprintf(stderr, "FAIL: %d bin mismatches\n", errors);
    else
        fprintf(stderr, "PASS\n");

    CUDA_CHECK(cudaFree(d_bins));

    return 0;
}
