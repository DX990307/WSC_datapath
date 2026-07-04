// chai_hsto.cu — Histogram benchmark (Chai collaborative computing pattern)
//
// Computes a 256-bin histogram of N random bytes using shared memory
// privatization and global reduction.
//
// Native CUDA implementation.
//
// Usage:
//   ./chai_hsto [--size N]
//
//   --size N         Number of input bytes (default: 16777216 = 16M)
//
// Output (stdout): CSV — chai_hsto,<N>,<time_ms>,<GBs>
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

#define NUM_BINS 256
#define BLOCK_SIZE 256

// ---------------------------------------------------------------------------
// Kernel: histogram with shared memory privatization
//
// Each block builds a private histogram in shared memory, then atomically
// adds it to the global histogram.
// ---------------------------------------------------------------------------

__global__ void histogram_kernel(
    const unsigned char* __restrict__ data,
    unsigned int* __restrict__ histo,
    int N)
{
    // Shared memory histogram for this block
    __shared__ unsigned int s_histo[NUM_BINS];

    int tid = threadIdx.x;

    // Initialize shared histogram to zero
    if (tid < NUM_BINS) {
        s_histo[tid] = 0;
    }
    __syncthreads();

    // Each thread processes multiple elements (grid-stride loop)
    int idx = blockIdx.x * blockDim.x + threadIdx.x;
    int stride = blockDim.x * gridDim.x;

    for (int i = idx; i < N; i += stride) {
        atomicAdd(&s_histo[data[i]], 1u);
    }
    __syncthreads();

    // Reduce shared histogram to global histogram
    if (tid < NUM_BINS) {
        if (s_histo[tid] > 0) {
            atomicAdd(&histo[tid], s_histo[tid]);
        }
    }
}

// ---------------------------------------------------------------------------
// CPU reference: histogram
// ---------------------------------------------------------------------------

static void histogram_cpu(const unsigned char* data, unsigned int* histo, int N) {
    memset(histo, 0, NUM_BINS * sizeof(unsigned int));
    for (int i = 0; i < N; ++i) {
        histo[data[i]]++;
    }
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------

int main(int argc, char** argv) {
    setvbuf(stdout, NULL, _IONBF, 0);

    // Defaults from params.json
    int size       = 16777216;
    int num_bins   = NUM_BINS;
    int block_size = BLOCK_SIZE;

    // Command-line fallback
    size       = parseIntParam(argc, argv, "--size", size);
    num_bins   = parseIntParam(argc, argv, "--num_bins", num_bins);
    block_size = parseIntParam(argc, argv, "--block_size", block_size);

    // Environment variable overrides
    const char* env_val;
    env_val = getenv("BENCH_PARAM_size");
    if (env_val) size = atoi(env_val);
    env_val = getenv("BENCH_PARAM_num_bins");
    if (env_val) num_bins = atoi(env_val);
    env_val = getenv("BENCH_PARAM_block_size");
    if (env_val) block_size = atoi(env_val);
    int num_warmup = 0;

    int N     = size;

    size_t data_bytes  = (size_t)N * sizeof(unsigned char);
    size_t histo_bytes = num_bins * sizeof(unsigned int);

    // Print device info
    int device_id = 0;
    CUDA_CHECK(cudaGetDevice(&device_id));
    cudaDeviceProp prop;
    CUDA_CHECK(cudaGetDeviceProperties(&prop, device_id));
    fprintf(stderr, "Device: %s (id=%d)\n", prop.name, device_id);
    fprintf(stderr, "Histogram  |  N: %d (%.2f MB)  |  Bins: %d  |  "
            "Iterations: %d warmup + %d timed\n\n",
            N, (double)data_bytes / (1024.0 * 1024.0), num_bins, num_warmup);

    // Generate random input data (deterministic seed)
    unsigned char* h_data = (unsigned char*)malloc(data_bytes);
    unsigned int seed = 12345;
    for (int i = 0; i < N; ++i) {
        seed = seed * 1103515245u + 12345u;
        h_data[i] = (unsigned char)((seed >> 16) & 0xFF);
    }

    // Host histogram
    unsigned int* h_histo = (unsigned int*)malloc(histo_bytes);

    // Device allocation
    unsigned char* d_data;
    unsigned int* d_histo;
    CUDA_CHECK(cudaMalloc(&d_data, data_bytes));
    CUDA_CHECK(cudaMalloc(&d_histo, histo_bytes));

    // Copy input data to device
    CUDA_CHECK(cudaMemcpy(d_data, h_data, data_bytes, cudaMemcpyHostToDevice));

    // Kernel launch config
    int blockSize = block_size;
    int gridSize  = (N + blockSize - 1) / blockSize;
    // Cap grid size to avoid too many blocks
    if (gridSize > 1024) gridSize = 1024;

    auto run_histo = [&]() {
        CUDA_CHECK(cudaMemset(d_histo, 0, histo_bytes));
        histogram_kernel<<<dim3(gridSize), dim3(blockSize), 0, 0>>>(d_data, d_histo, N);
    };

    // -------------------------------------------------------------------
    // Warmup
    // -------------------------------------------------------------------
    for (int w = 0; w < num_warmup; ++w) {
        run_histo();
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
        run_histo();
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

    // GB/s = N * sizeof(uint8_t) / time_s / 1e9
    double gbs = (double)N * 1.0 / (avg_ms * 1e-3) / 1e9;

    double total_ms = sum;

    // JSON-lines kernel event
    printf("{\"type\":\"kernel\",\"name\":\"histogram_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"size\":%d\"num_bins\":%d,"
           "\"block_size\":%d}}\n",
           avg_ms, size, num_bins, block_size);

    // JSON-lines summary event
    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"throughput_gbps\",\"value\":%.2f}]}\n",
           total_ms, gbs);

    // Human-readable output to stderr
    fprintf(stderr, "Average time: %.4f ms\n", avg_ms);
    fprintf(stderr, "Throughput:   %.4f GB/s\n", gbs);

    // -------------------------------------------------------------------
    // Verification (compare GPU histogram against CPU reference)
    // -------------------------------------------------------------------
    {
        CUDA_CHECK(cudaMemcpy(h_histo, d_histo, histo_bytes, cudaMemcpyDeviceToHost));

        std::vector<unsigned int> cpu_histo(num_bins);
        histogram_cpu(h_data, cpu_histo.data(), N);

        int errors = 0;
        for (int b = 0; b < num_bins; ++b) {
            if (h_histo[b] != cpu_histo[b]) {
                if (errors < 10) {
                    fprintf(stderr, "Mismatch at bin %d: GPU=%u CPU=%u\n",
                            b, h_histo[b], cpu_histo[b]);
                }
                errors++;
            }
        }

        if (errors > 0)
            fprintf(stderr, "FAIL: %d bin errors out of %d bins\n",
                    errors, num_bins);
        else
            fprintf(stderr, "PASS\n");
    }

    CUDA_CHECK(cudaFree(d_data));
    CUDA_CHECK(cudaFree(d_histo));
    free(h_data);
    free(h_histo);

    return 0;
}
