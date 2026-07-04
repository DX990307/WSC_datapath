// shoc_sort.hip — SHOC Sort benchmark (HIP, self-contained)
//
// Bitonic sort on a large array of random unsigned integers.
// O(n log^2 n) compare-and-swap network.
//
// Parameters are read from BENCH_PARAM_* environment variables,
// with command-line --flags as fallback.
//
// Output (stdout): JSON-lines protocol
// Output (stderr): Human-readable diagnostics

#if defined(__NVCC__) && !__has_include(<hip/hip_runtime.h>)
#include <cuda_runtime.h>
#define hipMalloc               cudaMalloc
#define hipFree                 cudaFree
#define hipMemcpy               cudaMemcpy
#define hipMemcpyAsync          cudaMemcpyAsync
#define hipMemset               cudaMemset
#define hipMemsetAsync          cudaMemsetAsync
#define hipMemcpyHostToDevice   cudaMemcpyHostToDevice
#define hipMemcpyDeviceToHost   cudaMemcpyDeviceToHost
#define hipMemcpyDeviceToDevice cudaMemcpyDeviceToDevice
#define hipMemcpyToSymbol       cudaMemcpyToSymbol
#define hipDeviceSynchronize    cudaDeviceSynchronize
#define hipGetDevice            cudaGetDevice
#define hipGetDeviceProperties  cudaGetDeviceProperties
#define hipGetLastError         cudaGetLastError
#define hipGetErrorString       cudaGetErrorString
#define hipEvent_t              cudaEvent_t
#define hipEventCreate          cudaEventCreate
#define hipEventDestroy         cudaEventDestroy
#define hipEventRecord          cudaEventRecord
#define hipEventSynchronize     cudaEventSynchronize
#define hipEventElapsedTime     cudaEventElapsedTime
#define hipStream_t             cudaStream_t
#define hipStreamCreate         cudaStreamCreate
#define hipStreamDestroy        cudaStreamDestroy
#define hipStreamSynchronize    cudaStreamSynchronize
#define hipDeviceProp_t         cudaDeviceProp
#define hipError_t              cudaError_t
#define hipSuccess              cudaSuccess
#define hipHostMalloc           cudaMallocHost
#define hipHostFree             cudaFreeHost
#define hipHostMallocDefault    cudaHostAllocDefault
#define hipBlockIdx_x           blockIdx.x
#define hipBlockIdx_y           blockIdx.y
#define hipBlockIdx_z           blockIdx.z
#define hipThreadIdx_x          threadIdx.x
#define hipThreadIdx_y          threadIdx.y
#define hipThreadIdx_z          threadIdx.z
#define hipBlockDim_x           blockDim.x
#define hipBlockDim_y           blockDim.y
#define hipBlockDim_z           blockDim.z
#define hipGridDim_x            gridDim.x
#define hipGridDim_y            gridDim.y
#define hipGridDim_z            gridDim.z
#define __HIP_PLATFORM_AMD__    0
#define hipLaunchKernelGGL(kernel, grid, block, smem, stream, ...) \
    kernel<<<grid, block, smem, stream>>>(__VA_ARGS__)
#else
#include <hip/hip_runtime.h>
#endif
#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <cmath>
#include <cfloat>
#include <vector>
#include <algorithm>

// ---------------------------------------------------------------------------
// Inlined HIP utilities
// ---------------------------------------------------------------------------

#define HIP_CHECK(cmd)                                                         \
    do {                                                                       \
        hipError_t _e = (cmd);                                                 \
        if (_e != hipSuccess) {                                                \
            fprintf(stderr, "HIP error %s at %s:%d\n",                        \
                    hipGetErrorString(_e), __FILE__, __LINE__);                \
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
    hipEvent_t start, stop;
    BenchmarkTimer() {
        HIP_CHECK(hipEventCreate(&start));
        HIP_CHECK(hipEventCreate(&stop));
    }
    ~BenchmarkTimer() {
        (void)hipEventDestroy(start);
        (void)hipEventDestroy(stop);
    }
    void record_start(hipStream_t stream = 0) {
        HIP_CHECK(hipEventRecord(start, stream));
    }
    void record_stop(hipStream_t stream = 0) {
        HIP_CHECK(hipEventRecord(stop, stream));
        HIP_CHECK(hipEventSynchronize(stop));
    }
    float elapsed_ms() const {
        float ms = 0.0f;
        HIP_CHECK(hipEventElapsedTime(&ms, start, stop));
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
            hipLaunchKernelGGL(bitonic_step, dim3(grid), dim3(block_size),
                               0, 0, d_data, j, k);
        }
    }
    HIP_CHECK(hipDeviceSynchronize());
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
    int num_warmup = 5;

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
    HIP_CHECK(hipGetDevice(&device_id));
    hipDeviceProp_t prop;
    HIP_CHECK(hipGetDeviceProperties(&prop, device_id));
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
    HIP_CHECK(hipMalloc(&d_data, bytes));

    BenchmarkTimer timer;

    // Warmup
    for (int w = 0; w < num_warmup; ++w) {
        HIP_CHECK(hipMemcpy(d_data, h_orig, bytes, hipMemcpyHostToDevice));
        bitonic_sort_gpu(d_data, N, block_size);
    }

    // Timed iterations
    std::vector<double> times(1);
    for (int i = 0; i < 1; ++i) {
        HIP_CHECK(hipMemcpy(d_data, h_orig, bytes, hipMemcpyHostToDevice));
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
    HIP_CHECK(hipMemcpy(h_data, d_data, bytes, hipMemcpyDeviceToHost));
    if (verify_sorted(h_data, N)) {
        fprintf(stderr, "Correctness: PASS\n");
    } else {
        fprintf(stderr, "Correctness: FAIL\n");
    }

    // JSON-lines kernel event
    printf("{\"type\":\"kernel\",\"name\":\"bitonic_step\",\"time_ms\":%.6f,"
           "\"params\":{\"size\":%d,\"block_size\":%d,"
           "\"precision\":\"%s\"}}\n",
           avg_ms, size, block_size, precision);

    // JSON-lines summary event
    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"melements_per_sec\",\"value\":%.2f}]}\n",
           total_ms, melems_per_sec);

    // Cleanup
    HIP_CHECK(hipFree(d_data));
    free(h_data);
    free(h_orig);

    return 0;
}
