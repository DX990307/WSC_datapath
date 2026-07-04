// shoc_devicememory.cu — SHOC DeviceMemory benchmark (CUDA, self-contained)
//
// Device global memory bandwidth benchmark: read-only, write-only, and
// read-write patterns.  Measures GB/s for each access pattern.
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
// Kernels
// ---------------------------------------------------------------------------

__global__ void readOnly_kernel(const float* __restrict__ input,
                                float* __restrict__ output,
                                int N) {
    int gid = blockIdx.x * blockDim.x + threadIdx.x;
    int totalThreads = gridDim.x * blockDim.x;

    float sum = 0.0f;
    for (int i = gid; i < N; i += totalThreads) {
        sum += input[i];
    }
    if (gid < N) {
        output[gid] = sum;
    }
}

__global__ void writeOnly_kernel(float* __restrict__ output,
                                 int N) {
    int gid = blockIdx.x * blockDim.x + threadIdx.x;
    int totalThreads = gridDim.x * blockDim.x;

    for (int i = gid; i < N; i += totalThreads) {
        output[i] = (float)i * 0.001f;
    }
}

__global__ void readWrite_kernel(float* __restrict__ data,
                                 int N) {
    int gid = blockIdx.x * blockDim.x + threadIdx.x;
    int totalThreads = gridDim.x * blockDim.x;

    for (int i = gid; i < N; i += totalThreads) {
        data[i] = data[i] * 1.0001f;
    }
}

// ---------------------------------------------------------------------------
// Helper: run benchmark for one kernel
// ---------------------------------------------------------------------------
struct KernelResult {
    double avg_ms;
    double total_ms;
};

static int g_num_warmup = 0;

template <typename Func>
static KernelResult benchKernel(int iters, Func func) {
    BenchmarkTimer timer;

    // Warmup
    for (int w = 0; w < g_num_warmup; ++w) {
        func();
    }
    CUDA_CHECK(cudaDeviceSynchronize());

    std::vector<double> times(1);
    for (int i = 0; i < 1; ++i) {
        timer.record_start();
        func();
        timer.record_stop();
        times[i] = static_cast<double>(timer.elapsed_ms());
    }

    double sum = 0.0;
    for (int i = 0; i < 1; ++i) sum += times[i];
    double avg = sum / 1;

    KernelResult r;
    r.avg_ms = avg;
    r.total_ms = sum;
    return r;
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------
int main(int argc, char** argv) {
    setvbuf(stdout, NULL, _IONBF, 0);

    // Defaults
    int array_size_mb = 64;
    int block_size    = 256;
    const char* transfer_mode = "readwrite";

    // Command-line fallback
    array_size_mb = parseIntParam(argc, argv, "--array_size_mb", array_size_mb);
    block_size    = parseIntParam(argc, argv, "--block_size", block_size);
    transfer_mode = parseStrParam(argc, argv, "--transfer_mode", transfer_mode);

    // Environment variable overrides
    const char* env_val;
    env_val = getenv("BENCH_PARAM_array_size_mb");
    if (env_val) array_size_mb = atoi(env_val);
    env_val = getenv("BENCH_PARAM_block_size");
    if (env_val) block_size = atoi(env_val);
    env_val = getenv("BENCH_PARAM_transfer_mode");
    if (env_val) transfer_mode = env_val;
    int num_warmup = 0;

    size_t N     = (size_t)array_size_mb * 1024 * 1024 / sizeof(float);
    size_t bytes = N * sizeof(float);

    // Print device info
    int device_id = 0;
    CUDA_CHECK(cudaGetDevice(&device_id));
    cudaDeviceProp prop;
    CUDA_CHECK(cudaGetDeviceProperties(&prop, device_id));
    fprintf(stderr, "Device: %s (id=%d)\n", prop.name, device_id);
    fprintf(stderr, "Array size: %d MB (%zu floats)  |  Block size: %d\n\n",
            array_size_mb, N, block_size);

    // Device allocations
    float *d_input, *d_output;
    CUDA_CHECK(cudaMalloc(&d_input,  bytes));
    CUDA_CHECK(cudaMalloc(&d_output, bytes));

    // Initialize device input
    float* h_init = (float*)malloc(bytes);
    for (size_t i = 0; i < N; ++i)
        h_init[i] = (float)(i % 1000) * 0.001f;
    CUDA_CHECK(cudaMemcpy(d_input, h_init, bytes, cudaMemcpyHostToDevice));
    free(h_init);

    int num_blocks = (int)((N + block_size - 1) / block_size);
    if (num_blocks > 65535) num_blocks = 65535;

    g_num_warmup = num_warmup;
    double total_all_ms = 0.0;

    // Determine which kernels to run based on transfer_mode
    bool run_read  = (strcmp(transfer_mode, "read") == 0 || strcmp(transfer_mode, "readwrite") == 0);
    bool run_write = (strcmp(transfer_mode, "write") == 0 || strcmp(transfer_mode, "readwrite") == 0);
    bool run_rw    = (strcmp(transfer_mode, "readwrite") == 0);

    // --- Read-only bandwidth ---
    if (run_read || strcmp(transfer_mode, "readwrite") == 0) {
        KernelResult kr = benchKernel(1, [&]() {
            readOnly_kernel<<<dim3(num_blocks), dim3(block_size), 0, 0>>>(d_input, d_output, (int)N);
        });
        double read_bw = (double)bytes / (kr.avg_ms * 1e-3) / 1e9;
        total_all_ms += kr.total_ms;

        printf("{\"type\":\"kernel\",\"name\":\"readOnly_kernel\",\"time_ms\":%.6f,"
               "\"params\":{\"array_size_mb\":%d,\"block_size\":%d"
               "\"transfer_mode\":\"%s\"}}\n",
               kr.avg_ms, array_size_mb, block_size, transfer_mode);

        fprintf(stderr, "Read-only bandwidth:  %.2f GB/s  (%d MB)\n", read_bw, array_size_mb);
    }

    // --- Write-only bandwidth ---
    if (run_write || strcmp(transfer_mode, "readwrite") == 0) {
        KernelResult kr = benchKernel(1, [&]() {
            writeOnly_kernel<<<dim3(num_blocks), dim3(block_size), 0, 0>>>(d_output, (int)N);
        });
        double write_bw = (double)bytes / (kr.avg_ms * 1e-3) / 1e9;
        total_all_ms += kr.total_ms;

        printf("{\"type\":\"kernel\",\"name\":\"writeOnly_kernel\",\"time_ms\":%.6f,"
               "\"params\":{\"array_size_mb\":%d,\"block_size\":%d"
               "\"transfer_mode\":\"%s\"}}\n",
               kr.avg_ms, array_size_mb, block_size, transfer_mode);

        fprintf(stderr, "Write-only bandwidth: %.2f GB/s  (%d MB)\n", write_bw, array_size_mb);
    }

    // --- Read-write bandwidth ---
    if (run_rw || strcmp(transfer_mode, "readwrite") == 0) {
        // Re-initialize d_input
        {
            float* h_rw = (float*)malloc(bytes);
            for (size_t i = 0; i < N; ++i) h_rw[i] = (float)(i % 1000) * 0.001f;
            CUDA_CHECK(cudaMemcpy(d_input, h_rw, bytes, cudaMemcpyHostToDevice));
            free(h_rw);
        }
        KernelResult kr = benchKernel(1, [&]() {
            readWrite_kernel<<<dim3(num_blocks), dim3(block_size), 0, 0>>>(d_input, (int)N);
        });
        double rw_bw = 2.0 * (double)bytes / (kr.avg_ms * 1e-3) / 1e9;
        total_all_ms += kr.total_ms;

        printf("{\"type\":\"kernel\",\"name\":\"readWrite_kernel\",\"time_ms\":%.6f,"
               "\"params\":{\"array_size_mb\":%d,\"block_size\":%d"
               "\"transfer_mode\":\"%s\"}}\n",
               kr.avg_ms, array_size_mb, block_size, transfer_mode);

        fprintf(stderr, "Read-write bandwidth: %.2f GB/s  (%d MB)\n", rw_bw, array_size_mb);
    }

    // JSON-lines summary event
    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[]}\n",
           total_all_ms);

    CUDA_CHECK(cudaFree(d_input));
    CUDA_CHECK(cudaFree(d_output));

    return 0;
}
