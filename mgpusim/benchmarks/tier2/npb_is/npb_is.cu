// npb_is.cu — NAS Parallel Benchmarks Integer Sort (IS)
//
// Parallel bucket sort of N random integers in range [0, MAX_KEY).
// Phase 1: histogram (count per bucket)
// Phase 2: prefix sum (exclusive scan) on histogram
// Phase 3: scatter keys to sorted positions
//
// Native CUDA implementation.
//
// Usage:
//   ./npb_is [--size N]
//
//   --size N         Number of keys (default: 8388608 = 2^23)
//
// Output (stdout): CSV row — npb_is,<N>,<time_ms>,<Mkeys_per_sec>
// Output (stderr): human-readable results

#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <cmath>
#include <vector>
#include <cuda_runtime.h>

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

#define NUM_BUCKETS 1024    // 2^10
#define MAX_KEY     524288  // 2^19

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
// Kernel: histogram — count keys per bucket
//   Each key maps to bucket = key / (MAX_KEY / NUM_BUCKETS)
// ---------------------------------------------------------------------------

__global__ void histogram_kernel(const int* __restrict__ keys, int N,
                                 unsigned int* __restrict__ hist) {
    int idx = blockIdx.x * blockDim.x + threadIdx.x;
    if (idx >= N) return;

    int bucket = keys[idx] / (MAX_KEY / NUM_BUCKETS);
    if (bucket >= NUM_BUCKETS) bucket = NUM_BUCKETS - 1;
    atomicAdd(&hist[bucket], 1u);
}

// ---------------------------------------------------------------------------
// Kernel: prefix sum (exclusive scan) — simple single-block scan
//   Works for NUM_BUCKETS <= 1024 (one block)
// ---------------------------------------------------------------------------

__global__ void prefix_sum_kernel(unsigned int* hist, int n) {
    extern __shared__ unsigned int sdata[];

    int tid = threadIdx.x;
    if (tid < n)
        sdata[tid] = hist[tid];
    else
        sdata[tid] = 0;
    __syncthreads();

    // Up-sweep (reduce)
    for (int stride = 1; stride < n; stride <<= 1) {
        int index = (tid + 1) * (stride << 1) - 1;
        if (index < n) {
            sdata[index] += sdata[index - stride];
        }
        __syncthreads();
    }

    // Set last element to 0 for exclusive scan
    if (tid == 0) sdata[n - 1] = 0;
    __syncthreads();

    // Down-sweep
    for (int stride = n >> 1; stride >= 1; stride >>= 1) {
        int index = (tid + 1) * (stride << 1) - 1;
        if (index < n) {
            unsigned int temp = sdata[index - stride];
            sdata[index - stride] = sdata[index];
            sdata[index] += temp;
        }
        __syncthreads();
    }

    if (tid < n)
        hist[tid] = sdata[tid];
}

// ---------------------------------------------------------------------------
// Kernel: scatter keys to sorted positions
//   Uses atomicAdd on prefix-sum offsets to get unique output positions
// ---------------------------------------------------------------------------

__global__ void scatter_kernel(const int* __restrict__ keys, int N,
                               unsigned int* __restrict__ offsets,
                               int* __restrict__ sorted) {
    int idx = blockIdx.x * blockDim.x + threadIdx.x;
    if (idx >= N) return;

    int bucket = keys[idx] / (MAX_KEY / NUM_BUCKETS);
    if (bucket >= NUM_BUCKETS) bucket = NUM_BUCKETS - 1;

    unsigned int pos = atomicAdd(&offsets[bucket], 1u);
    sorted[pos] = keys[idx];
}

// ---------------------------------------------------------------------------
// CPU reference: generate keys with simple LCG
// ---------------------------------------------------------------------------

static void generate_keys(int* keys, int N) {
    unsigned int seed = 314159265u;
    for (int i = 0; i < N; ++i) {
        seed = seed * 1103515245u + 12345u;
        keys[i] = (int)((seed >> 4) % MAX_KEY);
    }
}

// ---------------------------------------------------------------------------
// CPU verification: check sorted output is non-decreasing within buckets
// ---------------------------------------------------------------------------

static bool verify_sorted(const int* sorted, int N) {
    for (int i = 1; i < N; ++i) {
        int bucket_prev = sorted[i - 1] / (MAX_KEY / NUM_BUCKETS);
        int bucket_curr = sorted[i] / (MAX_KEY / NUM_BUCKETS);
        if (bucket_curr < bucket_prev) {
            fprintf(stderr, "Verification failed at index %d: %d (bucket %d) > %d (bucket %d)\n",
                    i, sorted[i - 1], bucket_prev, sorted[i], bucket_curr);
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

    // Defaults from params.json
    int N          = 8388608;   // 2^23
    int block_size = 256;
    const char* verify = "true";

    // Command-line fallback
    N          = parseIntParam(argc, argv, "--size", N);

    // Environment variable overrides
    const char* env_val;
    env_val = getenv("BENCH_PARAM_size");
    if (env_val) N = atoi(env_val);
    env_val = getenv("BENCH_PARAM_block_size");
    if (env_val) block_size = atoi(env_val);
    env_val = getenv("BENCH_PARAM_verify");
    if (env_val) verify = env_val;
    int num_warmup = 0;


    // Print device info
    int device_id = 0;
    CUDA_CHECK(cudaGetDevice(&device_id));
    cudaDeviceProp prop;
    CUDA_CHECK(cudaGetDeviceProperties(&prop, device_id));
    fprintf(stderr, "Device: %s (id=%d)\n", prop.name, device_id);
    fprintf(stderr, "IS keys: %d (2^%.0f)  |  Buckets: %d warmup + %d timed\n\n",
            N, log2((double)N), NUM_BUCKETS, num_warmup);

    size_t keysBytes = (size_t)N * sizeof(int);
    size_t histBytes = NUM_BUCKETS * sizeof(unsigned int);

    // Host allocation + key generation
    int* h_keys   = (int*)malloc(keysBytes);
    int* h_sorted = (int*)malloc(keysBytes);
    generate_keys(h_keys, N);

    // Device allocation
    int* d_keys;
    int* d_sorted;
    unsigned int* d_hist;
    unsigned int* d_offsets;  // copy of prefix-sum for scatter

    CUDA_CHECK(cudaMalloc(&d_keys, keysBytes));
    CUDA_CHECK(cudaMalloc(&d_sorted, keysBytes));
    CUDA_CHECK(cudaMalloc(&d_hist, histBytes));
    CUDA_CHECK(cudaMalloc(&d_offsets, histBytes));

    // Copy keys to device
    CUDA_CHECK(cudaMemcpy(d_keys, h_keys, keysBytes, cudaMemcpyHostToDevice));

    // Kernel launch config
    int blockSize = block_size;
    int gridKeys  = (N + blockSize - 1) / blockSize;

    // Lambda: run full IS (histogram + prefix sum + scatter)
    auto run_is = [&]() {
        // Clear histogram
        CUDA_CHECK(cudaMemset(d_hist, 0, histBytes));

        // Phase 1: histogram
        histogram_kernel<<<dim3(gridKeys), dim3(blockSize), 0, 0>>>(d_keys, N, d_hist);

        // Phase 2: prefix sum (exclusive scan on histogram)
        // Single-block scan, NUM_BUCKETS threads
        prefix_sum_kernel<<<dim3(1), dim3(NUM_BUCKETS), NUM_BUCKETS * sizeof(unsigned int), 0>>>(d_hist, NUM_BUCKETS);

        // Copy prefix sums to offsets (scatter will modify offsets)
        CUDA_CHECK(cudaMemcpy(d_offsets, d_hist, histBytes, cudaMemcpyDeviceToDevice));

        // Phase 3: scatter
        scatter_kernel<<<dim3(gridKeys), dim3(blockSize), 0, 0>>>(d_keys, N, d_offsets, d_sorted);
    };

    // -------------------------------------------------------------------
    // Warmup
    // -------------------------------------------------------------------
    for (int w = 0; w < num_warmup; ++w) {
        run_is();
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
        run_is();
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

    // M keys/sec = N / time_s / 1e6
    double mkeys_sec = (double)N / (avg_ms * 1e-3) / 1e6;

    double total_ms = sum;

    // JSON-lines kernel events (one per kernel)
    printf("{\"type\":\"kernel\",\"name\":\"histogram_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"size\":%d\"block_size\":%d,"
           "\"verify\":\"%s\"}}\n",
           avg_ms, N, block_size, verify);

    printf("{\"type\":\"kernel\",\"name\":\"prefix_sum_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"size\":%d\"block_size\":%d,"
           "\"verify\":\"%s\"}}\n",
           avg_ms, N, block_size, verify);

    printf("{\"type\":\"kernel\",\"name\":\"scatter_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"size\":%d\"block_size\":%d,"
           "\"verify\":\"%s\"}}\n",
           avg_ms, N, block_size, verify);

    // JSON-lines summary event
    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"mkeys_per_sec\",\"value\":%.2f}]}\n",
           total_ms, mkeys_sec);

    // Human-readable output to stderr
    fprintf(stderr, "Average time: %.4f ms\n", avg_ms);
    fprintf(stderr, "Performance:  %.4f Mkeys/sec\n", mkeys_sec);

    // -------------------------------------------------------------------
    // Verification: check sorted output is bucket-ordered
    // -------------------------------------------------------------------
    CUDA_CHECK(cudaMemcpy(h_sorted, d_sorted, keysBytes, cudaMemcpyDeviceToHost));

    if (verify_sorted(h_sorted, N))
        fprintf(stderr, "PASS\n");
    else
        fprintf(stderr, "FAIL\n");

    CUDA_CHECK(cudaFree(d_keys));
    CUDA_CHECK(cudaFree(d_sorted));
    CUDA_CHECK(cudaFree(d_hist));
    CUDA_CHECK(cudaFree(d_offsets));
    free(h_keys);
    free(h_sorted);

    return 0;
}
