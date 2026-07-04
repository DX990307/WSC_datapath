// shoc_sort.cu — SHOC Sort benchmark (CUDA, self-contained)
//
// Bitonic sort on a large array of random unsigned integers.
// O(n log^2 n) compare-and-swap network.
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
#include <algorithm>
#include <cuda_runtime.h>

// ---------------------------------------------------------------------------
// Inlined CUDA utilities
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

static const char* parseStrParam(int argc, char** argv, const char* name,
                                 const char* defaultVal) {
    for (int i = 1; i < argc - 1; ++i) {
        if (strcmp(argv[i], name) == 0) {
            return argv[i + 1];
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
// Bitonic sort kernel
// ---------------------------------------------------------------------------
__global__ void bitonic_step(unsigned int* data, int j, int k) {
    int i = (int)(threadIdx.x + blockIdx.x * blockDim.x);
    int ixj = i ^ j;

    if (ixj > i) {
        bool ascending = ((i & k) == 0);
        unsigned int a = data[i];
        unsigned int b = data[ixj];
        if (ascending ? (a > b) : (a < b)) {
            data[i]   = b;
            data[ixj] = a;
        }
    }
}

// ---------------------------------------------------------------------------
// GPU bitonic sort driver
// ---------------------------------------------------------------------------
static void bitonic_sort_gpu(unsigned int* d_data, int N, int block_size) {
    int grid = (N + block_size - 1) / block_size;
    for (int k = 2; k <= N; k <<= 1) {
        for (int j = k >> 1; j > 0; j >>= 1) {
            bitonic_step<<<dim3(grid), dim3(block_size), 0, 0>>>(d_data, j, k);
        }
    }
    CUDA_CHECK(cudaDeviceSynchronize());
}

// ---------------------------------------------------------------------------
// Verify
// ---------------------------------------------------------------------------
static bool verify_sorted(const unsigned int* arr, int N) {
    for (int i = 1; i < N; ++i) {
        if (arr[i] < arr[i - 1]) {
            fprintf(stderr, "Mismatch at %d: arr[%d]=%u > arr[%d]=%u\n",
                    i, i - 1, arr[i - 1], i, arr[i]);
            return false;
        }
    }
    return true;
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------
int main(int argc, char** argv) {
    setvbuf(stdout, NULL, _IONBF, 0);

    // Defaults
    int size       = 1 << 22; // 4M
    int block_size = 256;
    const char* precision = "uint";

    // Command-line fallback
    size       = parseIntParam(argc, argv, "--size", size);
    block_size = parseIntParam(argc, argv, "--block_size", block_size);
    precision  = parseStrParam(argc, argv, "--precision", precision);

    // Environment variable overrides
    const char* env_val;
    env_val = getenv("BENCH_PARAM_size");
    if (env_val) size = atoi(env_val);
    env_val = getenv("BENCH_PARAM_block_size");
    if (env_val) block_size = atoi(env_val);
    env_val = getenv("BENCH_PARAM_precision");
    if (env_val) precision = env_val;
    int num_warmup = 0;

    int N     = size;

    // Round N up to next power of 2
    {
        int p = 1;
        while (p < N) p <<= 1;
        if (p != N) {
            fprintf(stderr, "Warning: N=%d is not a power of 2, rounding up to %d\n", N, p);
            N = p;
        }
    }

    size_t bytes = (size_t)N * sizeof(unsigned int);

    // Print device info
    int device_id = 0;
    CUDA_CHECK(cudaGetDevice(&device_id));
    cudaDeviceProp prop;
    CUDA_CHECK(cudaGetDeviceProperties(&prop, device_id));
    fprintf(stderr, "Device: %s (id=%d)\n", prop.name, device_id);
    fprintf(stderr, "Array size: %d elements (%.1f MB)\n\n",
            N, (double)bytes / (1024.0 * 1024.0));

    // Host allocation and initialization
    unsigned int* h_data = (unsigned int*)malloc(bytes);
    unsigned int* h_orig = (unsigned int*)malloc(bytes);
    if (!h_data || !h_orig) {
        fprintf(stderr, "Host malloc failed\n");
        return 1;
    }

    srand(42);
    for (int i = 0; i < N; ++i) {
        h_orig[i] = (unsigned int)rand();
    }

    // Device allocation
    unsigned int* d_data;
    CUDA_CHECK(cudaMalloc(&d_data, bytes));

    BenchmarkTimer timer;

    // Warmup
    for (int w = 0; w < num_warmup; ++w) {
        CUDA_CHECK(cudaMemcpy(d_data, h_orig, bytes, cudaMemcpyHostToDevice));
        bitonic_sort_gpu(d_data, N, block_size);
    }

    // Timed iterations
    std::vector<double> times(1);
    for (int i = 0; i < 1; ++i) {
        CUDA_CHECK(cudaMemcpy(d_data, h_orig, bytes, cudaMemcpyHostToDevice));
        timer.record_start();
        bitonic_sort_gpu(d_data, N, block_size);
        timer.record_stop();
        times[i] = static_cast<double>(timer.elapsed_ms());
        fprintf(stderr, "  Iter %d: %.4f ms\n", i + 1, times[i]);
    }

    // Statistics
    double sum_ms = 0.0, mn = DBL_MAX, mx = 0.0;
    for (int i = 0; i < 1; ++i) {
        sum_ms += times[i];
        if (times[i] < mn) mn = times[i];
        if (times[i] > mx) mx = times[i];
    }
    double avg_ms = sum_ms / 1;
    double total_ms = sum_ms;

    double melems_per_sec = (double)N / (avg_ms * 1e-3) / 1e6;

    fprintf(stderr, "Throughput: %.2f Melements/s  (avg %.4f ms, min %.4f ms, max %.4f ms)\n",
            melems_per_sec, avg_ms, mn, mx);

    // Correctness check
    CUDA_CHECK(cudaMemcpy(h_data, d_data, bytes, cudaMemcpyDeviceToHost));
    if (verify_sorted(h_data, N)) {
        fprintf(stderr, "Correctness: PASS\n");
    } else {
        fprintf(stderr, "Correctness: FAIL\n");
    }

    // JSON-lines kernel event
    printf("{\"type\":\"kernel\",\"name\":\"bitonic_step\",\"time_ms\":%.6f,"
           "\"params\":{\"size\":%d\"block_size\":%d,"
           "\"precision\":\"%s\"}}\n",
           avg_ms, size, block_size, precision);

    // JSON-lines summary event
    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"melements_per_sec\",\"value\":%.2f}]}\n",
           total_ms, melems_per_sec);

    // Cleanup
    CUDA_CHECK(cudaFree(d_data));
    free(h_data);
    free(h_orig);

    return 0;
}
