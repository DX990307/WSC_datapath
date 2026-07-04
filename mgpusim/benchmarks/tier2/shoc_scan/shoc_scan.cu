// shoc_scan.cu — SHOC Scan benchmark (CUDA, self-contained)
//
// Exclusive prefix scan (sum): output[i] = sum(input[0..i-1])
// Uses a two-phase work-efficient Blelloch scan with multi-block support via
// recursive block-sum propagation.  Derived from the SHOC benchmark suite.
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

#define CUDA_CHECK(cmd)                                                         \
    do {                                                                       \
        cudaError_t _e = (cmd);                                                 \
        if (_e != cudaSuccess) {                                                \
            fprintf(stderr, "CUDA error %s at %s:%d\n",                        \
                    cudaGetErrorString(_e), __FILE__, __LINE__);                \
            exit(1);                                                           \
        }                                                                      \
    } while (0)

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
// Scan kernels
// ---------------------------------------------------------------------------

#define MAX_BLOCK_SIZE 512

__global__ void scan_block_kernel(float* __restrict__ output,
                                  const float* __restrict__ input,
                                  float* __restrict__ block_sums,
                                  int N) {
    extern __shared__ float temp[];

    int tid          = threadIdx.x;
    int block_offset = blockIdx.x * (blockDim.x * 2);
    int ai           = tid;
    int bi           = tid + blockDim.x;

    temp[ai] = (block_offset + ai < N) ? input[block_offset + ai] : 0.0f;
    temp[bi] = (block_offset + bi < N) ? input[block_offset + bi] : 0.0f;

    int n = blockDim.x * 2;

    // Up-sweep (reduce)
    int offset = 1;
    for (int d = n >> 1; d > 0; d >>= 1) {
        __syncthreads();
        if (tid < d) {
            int ai2 = offset * (2 * tid + 1) - 1;
            int bi2 = offset * (2 * tid + 2) - 1;
            temp[bi2] += temp[ai2];
        }
        offset <<= 1;
    }

    __syncthreads();
    if (tid == 0) {
        if (block_sums != nullptr) {
            block_sums[blockIdx.x] = temp[n - 1];
        }
        temp[n - 1] = 0.0f;
    }

    // Down-sweep
    for (int d = 1; d < n; d <<= 1) {
        offset >>= 1;
        __syncthreads();
        if (tid < d) {
            int ai2 = offset * (2 * tid + 1) - 1;
            int bi2 = offset * (2 * tid + 2) - 1;
            float t   = temp[ai2];
            temp[ai2] = temp[bi2];
            temp[bi2] += t;
        }
    }

    __syncthreads();

    if (block_offset + ai < N) output[block_offset + ai] = temp[ai];
    if (block_offset + bi < N) output[block_offset + bi] = temp[bi];
}

__global__ void add_block_sums_kernel(float* __restrict__ data,
                                      const float* __restrict__ block_sums,
                                      int N) {
    if (blockIdx.x == 0) return;

    float sum   = block_sums[blockIdx.x];
    int   base  = blockIdx.x * (blockDim.x * 2);
    int   idx_a = base + threadIdx.x;
    int   idx_b = base + threadIdx.x + blockDim.x;

    if (idx_a < N) data[idx_a] += sum;
    if (idx_b < N) data[idx_b] += sum;
}

// ---------------------------------------------------------------------------
// Recursive multi-block scan
// ---------------------------------------------------------------------------

static void scan_recursive(float* d_output, const float* d_input, int N,
                            int block_size) {
    int elements_per_block = block_size * 2;
    int num_blocks         = (N + elements_per_block - 1) / elements_per_block;
    size_t shared_mem      = (size_t)elements_per_block * sizeof(float);

    float* d_block_sums = nullptr;
    if (num_blocks > 1) {
        CUDA_CHECK(cudaMalloc(&d_block_sums, (size_t)num_blocks * sizeof(float)));
    }

    scan_block_kernel<<<dim3(num_blocks), dim3(block_size), shared_mem, 0>>>(d_output, d_input, d_block_sums, N);

    if (num_blocks > 1) {
        float* d_scanned_block_sums;
        CUDA_CHECK(cudaMalloc(&d_scanned_block_sums,
                             (size_t)num_blocks * sizeof(float)));

        scan_recursive(d_scanned_block_sums, d_block_sums, num_blocks,
                       block_size);

        add_block_sums_kernel<<<dim3(num_blocks), dim3(block_size), 0, 0>>>(d_output, d_scanned_block_sums, N);

        CUDA_CHECK(cudaFree(d_scanned_block_sums));
        CUDA_CHECK(cudaFree(d_block_sums));
    }
}

// ---------------------------------------------------------------------------
// CPU reference: exclusive prefix sum
// ---------------------------------------------------------------------------
static void scan_cpu(float* output, const float* input, int N) {
    output[0] = 0.0f;
    for (int i = 1; i < N; ++i) {
        output[i] = output[i - 1] + input[i - 1];
    }
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------
int main(int argc, char** argv) {
    setvbuf(stdout, NULL, _IONBF, 0);

    // Defaults
    int array_size = 1048576;
    int block_size = 256;
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

    int N     = array_size;

    // Clamp block_size
    if (block_size > MAX_BLOCK_SIZE) block_size = MAX_BLOCK_SIZE;

    // Pad N to next multiple of elements_per_block
    int elements_per_block = block_size * 2;
    int N_padded = ((N + elements_per_block - 1) / elements_per_block)
                   * elements_per_block;

    size_t bytes         = (size_t)N_padded * sizeof(float);
    size_t bytes_orig    = (size_t)N        * sizeof(float);

    // Print device info to stderr
    int device_id = 0;
    CUDA_CHECK(cudaGetDevice(&device_id));
    cudaDeviceProp prop;
    CUDA_CHECK(cudaGetDeviceProperties(&prop, device_id));
    fprintf(stderr, "Device: %s (id=%d)\n", prop.name, device_id);
    fprintf(stderr, "Array size: %d floats (%.1f MiB, padded to %d)\n",
            N, (double)bytes_orig / (1024.0 * 1024.0), N_padded);
    fprintf(stderr, "Block size: %d\n\n", block_size);

    // Host allocations
    float* h_input  = (float*)calloc(N_padded, sizeof(float));
    float* h_output = (float*)malloc(bytes_orig);
    float* h_ref    = (float*)malloc(bytes_orig);

    for (int i = 0; i < N; ++i) {
        h_input[i] = (float)(i % 7) + 1.0f;
    }

    // Device allocations
    float *d_input, *d_output;
    CUDA_CHECK(cudaMalloc(&d_input,  bytes));
    CUDA_CHECK(cudaMalloc(&d_output, bytes));

    CUDA_CHECK(cudaMemcpy(d_input, h_input, bytes, cudaMemcpyHostToDevice));

    // Warmup
    for (int w = 0; w < num_warmup; ++w) {
        scan_recursive(d_output, d_input, N_padded, block_size);
        CUDA_CHECK(cudaDeviceSynchronize());
    }

    // Timed iterations
    BenchmarkTimer timer;
    std::vector<double> times(1);
    for (int i = 0; i < 1; ++i) {
        timer.record_start();
        scan_recursive(d_output, d_input, N_padded, block_size);
        timer.record_stop();
        times[i] = static_cast<double>(timer.elapsed_ms());
    }

    // Statistics
    double sum_t = 0.0, mn = DBL_MAX, mx = 0.0;
    for (int i = 0; i < 1; ++i) {
        sum_t += times[i];
        if (times[i] < mn) mn = times[i];
        if (times[i] > mx) mx = times[i];
    }
    double avg_ms = sum_t / 1;
    double total_ms = sum_t;

    // Effective bandwidth: 1 read + 1 write = 2 × N × sizeof(float)
    double bandwidth_gb = 2.0 * (double)N * sizeof(float)
                          / (avg_ms * 1e-3) / 1e9;

    // JSON-lines: one event per kernel listed in params.json
    printf("{\"type\":\"kernel\",\"name\":\"scan_block_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"array_size\":%d,\"block_size\":%d"
           "\"precision\":\"%s\"}}\n",
           avg_ms, array_size, block_size, precision);

    printf("{\"type\":\"kernel\",\"name\":\"add_block_sums_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"array_size\":%d,\"block_size\":%d"
           "\"precision\":\"%s\"}}\n",
           avg_ms, array_size, block_size, precision);

    // JSON-lines summary event
    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"bandwidth_gbps\",\"value\":%.2f}]}\n",
           total_ms, bandwidth_gb);

    fprintf(stderr, "Effective bandwidth: %.2f GB/s  (avg %.4f ms)\n",
            bandwidth_gb, avg_ms);

    // ---------------------------------------------------------------------------
    // Verification
    // ---------------------------------------------------------------------------
    scan_recursive(d_output, d_input, N_padded, block_size);
    CUDA_CHECK(cudaDeviceSynchronize());
    CUDA_CHECK(cudaMemcpy(h_output, d_output, bytes_orig, cudaMemcpyDeviceToHost));

    scan_cpu(h_ref, h_input, N);

    int errors = 0;
    for (int i = 0; i < N; ++i) {
        float diff = fabsf(h_output[i] - h_ref[i]);
        float tol  = 1e-3f * fabsf(h_ref[i]) + 1e-5f;
        if (diff > tol) {
            if (errors < 10) {
                fprintf(stderr, "Mismatch at %d: got %.6f, expected %.6f\n",
                        i, h_output[i], h_ref[i]);
            }
            errors++;
        }
    }
    if (errors > 0) {
        fprintf(stderr, "FAIL: %d errors out of %d\n", errors, N);
    } else {
        fprintf(stderr, "PASS\n");
    }

    // Cleanup
    CUDA_CHECK(cudaFree(d_input));
    CUDA_CHECK(cudaFree(d_output));
    free(h_input);
    free(h_output);
    free(h_ref);

    return (errors > 0) ? 1 : 0;
}
