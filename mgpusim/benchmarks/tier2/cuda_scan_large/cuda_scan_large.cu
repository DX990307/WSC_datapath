// cuda_scan_large.cu — Large-array parallel prefix sum (Blelloch scan) benchmark
//
// Work-efficient parallel prefix sum (exclusive scan) for large arrays.
// Three-phase hierarchical decomposition:
//   1. Block-level scan of each block
//   2. Scan of block sums
//   3. Add block sums back to each block
//
// Measures throughput in GB/s = 2 * N * sizeof(uint) / time_s / 1e9.
//
// Native CUDA implementation.
//
// Usage:
//   ./cuda_scan_large [--size N]
//
//   --size N         Number of elements (default: 16777216 = 16M)
//
// Output (stdout): CSV row — cuda_scan_large,<N>,<time_ms>,<GBs>
// Output (stderr): human-readable results

#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <cmath>
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

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

#define BLOCK_SIZE 256
#define ELEMENTS_PER_BLOCK (2 * BLOCK_SIZE)

// ---------------------------------------------------------------------------
// Kernel: Blelloch work-efficient scan (per-block)
//   Each block processes ELEMENTS_PER_BLOCK elements.
//   Stores the block total in block_sums[blockIdx.x] before zeroing the root.
// ---------------------------------------------------------------------------

__global__ void scan_block_kernel(
    unsigned int* __restrict__ data,
    unsigned int* __restrict__ block_sums,
    int N)
{
    __shared__ unsigned int temp[ELEMENTS_PER_BLOCK];

    int tid = threadIdx.x;
    int blockOffset = blockIdx.x * ELEMENTS_PER_BLOCK;

    // Load input into shared memory
    int ai = tid;
    int bi = tid + BLOCK_SIZE;
    int ga = blockOffset + ai;
    int gb = blockOffset + bi;

    temp[ai] = (ga < N) ? data[ga] : 0;
    temp[bi] = (gb < N) ? data[gb] : 0;

    // Up-sweep (reduce) phase
    int offset = 1;
    for (int d = ELEMENTS_PER_BLOCK >> 1; d > 0; d >>= 1) {
        __syncthreads();
        if (tid < d) {
            int ai2 = offset * (2 * tid + 1) - 1;
            int bi2 = offset * (2 * tid + 2) - 1;
            temp[bi2] += temp[ai2];
        }
        offset <<= 1;
    }

    __syncthreads();

    // Store block sum and clear last element
    if (tid == 0) {
        if (block_sums != nullptr) {
            block_sums[blockIdx.x] = temp[ELEMENTS_PER_BLOCK - 1];
        }
        temp[ELEMENTS_PER_BLOCK - 1] = 0;
    }

    // Down-sweep phase
    for (int d = 1; d < ELEMENTS_PER_BLOCK; d <<= 1) {
        offset >>= 1;
        __syncthreads();
        if (tid < d) {
            int ai2 = offset * (2 * tid + 1) - 1;
            int bi2 = offset * (2 * tid + 2) - 1;
            unsigned int t = temp[ai2];
            temp[ai2] = temp[bi2];
            temp[bi2] += t;
        }
    }

    __syncthreads();

    // Write results back
    if (ga < N) data[ga] = temp[ai];
    if (gb < N) data[gb] = temp[bi];
}

// ---------------------------------------------------------------------------
// Kernel: add block sums to each element in the block
// ---------------------------------------------------------------------------

__global__ void add_block_sums_kernel(
    unsigned int* __restrict__ data,
    const unsigned int* __restrict__ block_sums,
    int N)
{
    int idx = blockIdx.x * ELEMENTS_PER_BLOCK + threadIdx.x;
    unsigned int val = block_sums[blockIdx.x];

    if (idx < N)
        data[idx] += val;
    if (idx + BLOCK_SIZE < N)
        data[idx + BLOCK_SIZE] += val;
}

// ---------------------------------------------------------------------------
// Recursive scan for large arrays
// ---------------------------------------------------------------------------

static void scan_recursive(unsigned int* d_data, int N) {
    int numBlocks = (N + ELEMENTS_PER_BLOCK - 1) / ELEMENTS_PER_BLOCK;

    unsigned int* d_block_sums = nullptr;
    if (numBlocks > 1) {
        CUDA_CHECK(cudaMalloc(&d_block_sums, numBlocks * sizeof(unsigned int)));
    }

    // Phase 1: Block-level scan
    scan_block_kernel<<<dim3(numBlocks), dim3(BLOCK_SIZE), 0, 0>>>(d_data, d_block_sums, N);

    if (numBlocks > 1) {
        // Phase 2: Recursively scan the block sums
        scan_recursive(d_block_sums, numBlocks);

        // Phase 3: Add scanned block sums back to each block
        add_block_sums_kernel<<<dim3(numBlocks), dim3(BLOCK_SIZE), 0, 0>>>(d_data, d_block_sums, N);

        CUDA_CHECK(cudaFree(d_block_sums));
    }
}

// ---------------------------------------------------------------------------
// CPU reference: exclusive prefix sum
// ---------------------------------------------------------------------------

static void prefix_sum_cpu(const unsigned int* input, unsigned int* output, int N) {
    unsigned int sum = 0;
    for (int i = 0; i < N; ++i) {
        output[i] = sum;
        sum += input[i];
    }
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------

static const char* parseStrParam(int argc, char** argv, const char* name,
                                 const char* defaultVal) {
    for (int i = 1; i < argc - 1; ++i) {
        if (strcmp(argv[i], name) == 0) {
            return argv[i + 1];
        }
    }
    return defaultVal;
}

int main(int argc, char** argv) {
    setvbuf(stdout, NULL, _IONBF, 0);

    // Defaults from params.json
    int size       = 16777216;
    int block_size = 256;
    const char* verify = "true";

    // Command-line fallback
    size       = parseIntParam(argc, argv, "--size", size);
    block_size = parseIntParam(argc, argv, "--block_size", block_size);
    verify     = parseStrParam(argc, argv, "--verify", verify);

    // Environment variable overrides
    const char* env_val;
    env_val = getenv("BENCH_PARAM_size");
    if (env_val) size = atoi(env_val);
    env_val = getenv("BENCH_PARAM_block_size");
    if (env_val) block_size = atoi(env_val);
    env_val = getenv("BENCH_PARAM_verify");
    if (env_val) verify = env_val;
    int num_warmup = 0;

    int N     = size;

    size_t bytes = (size_t)N * sizeof(unsigned int);

    // Print device info
    int device_id = 0;
    CUDA_CHECK(cudaGetDevice(&device_id));
    cudaDeviceProp prop;
    CUDA_CHECK(cudaGetDeviceProperties(&prop, device_id));
    fprintf(stderr, "Device: %s (id=%d)\n", prop.name, device_id);
    fprintf(stderr, "Scan size: %d warmup + %d timed\n\n", N, num_warmup);

    // Host allocation + initialization
    unsigned int* h_input  = (unsigned int*)malloc(bytes);
    unsigned int* h_output = (unsigned int*)malloc(bytes);
    for (int i = 0; i < N; ++i) {
        h_input[i] = (unsigned int)(i % 10);  // small values to avoid overflow
    }

    // Device allocation
    unsigned int* d_data;
    CUDA_CHECK(cudaMalloc(&d_data, bytes));

    // Lambda: run full scan
    auto run_scan = [&]() {
        CUDA_CHECK(cudaMemcpy(d_data, h_input, bytes, cudaMemcpyHostToDevice));
        scan_recursive(d_data, N);
    };

    // -------------------------------------------------------------------
    // Warmup
    // -------------------------------------------------------------------
    for (int w = 0; w < num_warmup; ++w) {
        run_scan();
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
        run_scan();
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

    // GB/s = 2 * N * sizeof(uint) / time_s / 1e9
    double data_bytes = 2.0 * (double)N * sizeof(unsigned int);
    double gbs = data_bytes / (avg_ms * 1e-3) / 1e9;

    double total_ms = sum;

    // JSON-lines kernel events
    printf("{\"type\":\"kernel\",\"name\":\"scan_block_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"size\":%d\"block_size\":%d,"
           "\"verify\":\"%s\"}}\n",
           avg_ms, size, block_size, verify);

    printf("{\"type\":\"kernel\",\"name\":\"add_block_sums_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"size\":%d\"block_size\":%d,"
           "\"verify\":\"%s\"}}\n",
           avg_ms, size, block_size, verify);

    // JSON-lines summary event
    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"throughput_gbps\",\"value\":%.2f}]}\n",
           total_ms, gbs);

    // Human-readable output to stderr
    fprintf(stderr, "Average time: %.4f ms\n", avg_ms);
    fprintf(stderr, "Throughput:   %.4f GB/s\n", gbs);

    // -------------------------------------------------------------------
    // Verification
    // -------------------------------------------------------------------
    {
        CUDA_CHECK(cudaMemcpy(h_output, d_data, bytes, cudaMemcpyDeviceToHost));

        // CPU reference
        unsigned int* h_ref = (unsigned int*)malloc(bytes);
        prefix_sum_cpu(h_input, h_ref, N);

        int errors = 0;
        for (int i = 0; i < N; ++i) {
            if (h_output[i] != h_ref[i]) {
                if (errors < 10) {
                    fprintf(stderr, "Mismatch at %d: GPU=%u CPU=%u\n",
                            i, h_output[i], h_ref[i]);
                }
                errors++;
            }
        }

        if (errors > 0)
            fprintf(stderr, "FAIL: %d errors out of %d\n", errors, N);
        else
            fprintf(stderr, "PASS\n");

        free(h_ref);
    }

    CUDA_CHECK(cudaFree(d_data));
    free(h_input);
    free(h_output);

    return 0;
}
