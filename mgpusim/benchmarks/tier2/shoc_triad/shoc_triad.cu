// shoc_triad.cu — SHOC Triad benchmark (CUDA, self-contained)
//
// Stream Triad: a[i] = b[i] + scalar * c[i]
// Classic memory bandwidth benchmark derived from the SHOC suite.
//
// Native CUDA implementation.
//
// Parameters are read from BENCH_PARAM_* environment variables,
// with command-line --flags as fallback.
//
// Output (stdout): JSON-lines protocol
// Output (stderr): Human-readable diagnostics

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

// Error-checking macro
#define CUDA_CHECK(cmd)                                                         \
    do {                                                                       \
        cudaError_t _e = (cmd);                                                 \
        if (_e != cudaSuccess) {                                                \
            fprintf(stderr, "CUDA error %s at %s:%d\n",                        \
                    cudaGetErrorString(_e), __FILE__, __LINE__);                \
            exit(1);                                                           \
        }                                                                      \
    } while (0)

// Parse --<name> N from argv (returns defaultVal if not found)
static int parseIntParam(int argc, char** argv, const char* name,
                         int defaultVal) {
    for (int i = 1; i < argc - 1; ++i) {
        if (strcmp(argv[i], name) == 0) {
            int v = atoi(argv[i + 1]);
            if (v > 0) return v;
            break;
        }
    }
    return defaultVal;
}

static const char* parseStrParam(int argc, char** argv, const char* name,
                                 const char* defaultVal) {
    for (int i = 1; i < argc - 1; ++i) {
        if (strcmp(argv[i], name) == 0) {
            return argv[i + 1];
        }
    }
    return defaultVal;
}

// CUDA-event-based timer
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
// Triad kernel: a[i] = b[i] + scalar * c[i]
// ---------------------------------------------------------------------------
__global__ void triad_kernel(float* __restrict__ a,
                              const float* __restrict__ b,
                              const float* __restrict__ c,
                              float scalar, int N) {
    int i = blockIdx.x * blockDim.x + threadIdx.x;
    if (i < N) {
        a[i] = b[i] + scalar * c[i];
    }
}

// ---------------------------------------------------------------------------
// CPU reference for verification
// ---------------------------------------------------------------------------
static void triad_cpu(float* a, const float* b, const float* c,
                      float scalar, int N) {
    for (int i = 0; i < N; ++i) {
        a[i] = b[i] + scalar * c[i];
    }
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------
int main(int argc, char** argv) {
    setvbuf(stdout, NULL, _IONBF, 0);

    // Defaults
    int array_size  = 4194304;
    int block_size  = 256;
    const char* precision = "float";

    // Command-line fallback
    array_size = parseIntParam(argc, argv, "--array_size", array_size);
    block_size = parseIntParam(argc, argv, "--block_size", block_size);
    precision  = parseStrParam(argc, argv, "--precision", precision);

    // Environment variable overrides
    const char* env_val;
    env_val = getenv("BENCH_PARAM_array_size");
    if (env_val) array_size = atoi(env_val);
    env_val = getenv("BENCH_PARAM_block_size");
    if (env_val) block_size = atoi(env_val);
    env_val = getenv("BENCH_PARAM_precision");
    if (env_val) precision = env_val;
    int num_warmup = 0;

    int N = array_size;
    size_t bytes  = (size_t)N * sizeof(float);
    float  scalar = 1.75f;

    // Print device info
    int device_id = 0;
    CUDA_CHECK(cudaGetDevice(&device_id));
    cudaDeviceProp prop;
    CUDA_CHECK(cudaGetDeviceProperties(&prop, device_id));
    fprintf(stderr, "Device: %s (id=%d)\n", prop.name, device_id);
    fprintf(stderr, "Array size: %d floats (%.1f MiB)\n",
            N, (double)bytes / (1024.0 * 1024.0));
    fprintf(stderr, "Block size: %d\n\n", block_size);

    // Host allocations
    float* h_a   = (float*)malloc(bytes);
    float* h_b   = (float*)malloc(bytes);
    float* h_c   = (float*)malloc(bytes);
    float* h_ref = (float*)malloc(bytes);

    // Initialize inputs
    for (int i = 0; i < N; ++i) {
        h_b[i] = (float)(i % 1000) * 0.001f;
        h_c[i] = (float)((i + 37) % 1000) * 0.001f;
    }

    // Device allocations
    float *d_a, *d_b, *d_c;
    CUDA_CHECK(cudaMalloc(&d_a, bytes));
    CUDA_CHECK(cudaMalloc(&d_b, bytes));
    CUDA_CHECK(cudaMalloc(&d_c, bytes));

    CUDA_CHECK(cudaMemcpy(d_b, h_b, bytes, cudaMemcpyHostToDevice));
    CUDA_CHECK(cudaMemcpy(d_c, h_c, bytes, cudaMemcpyHostToDevice));

    int grid = (N + block_size - 1) / block_size;

    // Warmup
    for (int w = 0; w < num_warmup; ++w) {
        triad_kernel<<<dim3(grid), dim3(block_size), 0, 0>>>(d_a, d_b, d_c, scalar, N);
        CUDA_CHECK(cudaDeviceSynchronize());
    }

    // Timed iterations
    BenchmarkTimer timer;
    std::vector<double> times(1);
    for (int i = 0; i < 1; ++i) {
        timer.record_start();
        triad_kernel<<<dim3(grid), dim3(block_size), 0, 0>>>(d_a, d_b, d_c, scalar, N);
        timer.record_stop();
        times[i] = static_cast<double>(timer.elapsed_ms());
    }

    // Statistics
    double sum = 0.0, mn = DBL_MAX, mx = 0.0;
    for (int i = 0; i < 1; ++i) {
        sum += times[i];
        if (times[i] < mn) mn = times[i];
        if (times[i] > mx) mx = times[i];
    }
    double avg_ms = sum / 1;

    // Effective bandwidth: 2 reads (b, c) + 1 write (a) = 3 * N * sizeof(float)
    double bandwidth_gb = 3.0 * (double)N * sizeof(float)
                          / (avg_ms * 1e-3) / 1e9;
    double total_ms = sum;

    // JSON-lines kernel event
    printf("{\"type\":\"kernel\",\"name\":\"triad_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"array_size\":%d,\"block_size\":%d"
           "\"precision\":\"%s\"}}\n",
           avg_ms, array_size, block_size, precision);

    // JSON-lines summary event
    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"bandwidth_gbps\",\"value\":%.2f}]}\n",
           total_ms, bandwidth_gb);

    fprintf(stderr, "Effective bandwidth: %.2f GB/s  (avg %.4f ms)\n",
            bandwidth_gb, avg_ms);

    // Verify correctness
    CUDA_CHECK(cudaMemcpy(h_a, d_a, bytes, cudaMemcpyDeviceToHost));
    triad_cpu(h_ref, h_b, h_c, scalar, N);

    int errors = 0;
    for (int i = 0; i < N; ++i) {
        float diff = fabsf(h_a[i] - h_ref[i]);
        if (diff > 1e-5f * fabsf(h_ref[i]) + 1e-6f) {
            if (errors < 10) {
                fprintf(stderr, "Mismatch at %d: got %.8f, expected %.8f\n",
                        i, h_a[i], h_ref[i]);
            }
            errors++;
        }
    }
    if (errors > 0) {
        fprintf(stderr, "FAIL: %d mismatches\n", errors);
    } else {
        fprintf(stderr, "PASS\n");
    }

    // Cleanup
    CUDA_CHECK(cudaFree(d_a));
    CUDA_CHECK(cudaFree(d_b));
    CUDA_CHECK(cudaFree(d_c));
    free(h_a);
    free(h_b);
    free(h_c);
    free(h_ref);

    return (errors > 0) ? 1 : 0;
}
