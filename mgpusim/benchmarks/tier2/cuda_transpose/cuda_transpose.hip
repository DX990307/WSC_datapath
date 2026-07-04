// cuda_transpose.hip — Shared-memory tiled matrix transpose benchmark (HIP)
//
// Two variants on a 4096×4096 single-precision matrix:
//   1. Naive transpose  — coalesced reads, strided writes
//   2. Optimized transpose — shared memory tile with +1 padding (bank-conflict-free)
//
// Compiles with hipcc (AMD ROCm) or nvcc via HIP compatibility layer (CUDA).
//
// Usage:
//   ./cuda_transpose [--size N]
//
//   --size N  Matrix dimension N (N×N)  (default: 4096)
//
// Output (stdout): CSV rows —
//   cuda_transpose,naive_<N>,<time_ms>,<GBs>
//   cuda_transpose,optimized_<N>,<time_ms>,<GBs>
//
// Output (stderr): human-readable results
//
// Effective bandwidth = 2 * N * N * sizeof(float) / time_s / 1e9

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

// ---------------------------------------------------------------------------
// Error checking
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

// ---------------------------------------------------------------------------
// Argument parsing
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// Constants — classic CUDA transpose pattern
// ---------------------------------------------------------------------------

#define TILE_DIM   32
#define BLOCK_ROWS  8

// ---------------------------------------------------------------------------
// Kernel 1: Naive transpose
// Each thread reads one element (coalesced) and writes one element (strided).
// ---------------------------------------------------------------------------

__global__ void transpose_naive(const float* __restrict__ idata,
                                float* __restrict__ odata,
                                int width, int height) {
    int xIndex = blockIdx.x * TILE_DIM + threadIdx.x;
    int yIndex = blockIdx.y * TILE_DIM + threadIdx.y;

    for (int j = 0; j < TILE_DIM; j += BLOCK_ROWS) {
        if (xIndex < width && (yIndex + j) < height) {
            odata[xIndex * height + (yIndex + j)] =
                idata[(yIndex + j) * width + xIndex];
        }
    }
}

// ---------------------------------------------------------------------------
// Kernel 2: Optimized transpose with shared memory (+1 padding)
// Coalesced read into shared memory, __syncthreads, coalesced write out.
// The +1 padding on the shared memory tile avoids bank conflicts.
// ---------------------------------------------------------------------------

__global__ void transpose_optimized(const float* __restrict__ idata,
                                    float* __restrict__ odata,
                                    int width, int height) {
    __shared__ float tile[TILE_DIM][TILE_DIM + 1];  // +1 padding

    int xIndex = blockIdx.x * TILE_DIM + threadIdx.x;
    int yIndex = blockIdx.y * TILE_DIM + threadIdx.y;

    // Load tile from global memory (coalesced reads)
    for (int j = 0; j < TILE_DIM; j += BLOCK_ROWS) {
        if (xIndex < width && (yIndex + j) < height) {
            tile[threadIdx.y + j][threadIdx.x] =
                idata[(yIndex + j) * width + xIndex];
        }
    }

    __syncthreads();

    // Write transposed tile to global memory (coalesced writes)
    // Swap block indices for transposed output position
    xIndex = blockIdx.y * TILE_DIM + threadIdx.x;
    yIndex = blockIdx.x * TILE_DIM + threadIdx.y;

    for (int j = 0; j < TILE_DIM; j += BLOCK_ROWS) {
        if (xIndex < height && (yIndex + j) < width) {
            odata[(yIndex + j) * height + xIndex] =
                tile[threadIdx.x][threadIdx.y + j];
        }
    }
}

// ---------------------------------------------------------------------------
// GPU timer
// ---------------------------------------------------------------------------

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
// Benchmark runner: 5 warmup + 5 timed iterations
// ---------------------------------------------------------------------------

static int WARMUP_ITERS = 5;
static const int TIMED_ITERS  = 5;

template <typename Func>
static double runTransposeBench(Func func) {
    BenchmarkTimer timer;

    // Warmup
    for (int i = 0; i < WARMUP_ITERS; ++i) {
        func();
    }
    HIP_CHECK(hipDeviceSynchronize());

    // Timed iterations
    double total_ms = 0.0;
    for (int i = 0; i < TIMED_ITERS; ++i) {
        timer.record_start();
        func();
        timer.record_stop();
        total_ms += static_cast<double>(timer.elapsed_ms());
    }
    return total_ms / TIMED_ITERS;  // average ms
}

// ---------------------------------------------------------------------------
// CPU reference transpose (for verification)
// ---------------------------------------------------------------------------

static void transpose_cpu(const float* in, float* out, int width, int height) {
    for (int y = 0; y < height; ++y) {
        for (int x = 0; x < width; ++x) {
            out[x * height + y] = in[y * width + x];
        }
    }
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------

int main(int argc, char** argv) {
    setvbuf(stdout, NULL, _IONBF, 0);

    // Default values from params.json
    int N          = 4096;
    int block_size = 256;
    int tile_dim   = 32;

    // Command-line args as fallback
    N          = parseIntParam(argc, argv, "--size", N);
    block_size = parseIntParam(argc, argv, "--block_size", block_size);
    tile_dim   = parseIntParam(argc, argv, "--tile_dim", tile_dim);

    // Env vars override
    const char* env_val;
    env_val = getenv("BENCH_PARAM_size");
    if (env_val) N = atoi(env_val);
    env_val = getenv("BENCH_PARAM_block_size");
    if (env_val) block_size = atoi(env_val);
    env_val = getenv("BENCH_PARAM_tile_dim");
    if (env_val) tile_dim = atoi(env_val);

    size_t num_elems = (size_t)N * N;
    size_t bytes     = num_elems * sizeof(float);

    // Print device info to stderr
    int device_id = 0;
    HIP_CHECK(hipGetDevice(&device_id));
    hipDeviceProp_t prop;
    HIP_CHECK(hipGetDeviceProperties(&prop, device_id));
    fprintf(stderr, "Device: %s (id=%d)\n", prop.name, device_id);
    fprintf(stderr, "Matrix size: %d×%d  |  Warmup: %d  |  Timed: %d\n\n",
            N, N, WARMUP_ITERS, TIMED_ITERS);

    // Host allocations
    float* h_idata = (float*)malloc(bytes);
    float* h_odata = (float*)malloc(bytes);
    float* h_ref   = (float*)malloc(bytes);

    // Initialize input matrix
    for (size_t i = 0; i < num_elems; ++i) {
        h_idata[i] = (float)(i % 1000) * 0.001f;
    }

    // Device allocations
    float *d_idata, *d_odata;
    HIP_CHECK(hipMalloc(&d_idata, bytes));
    HIP_CHECK(hipMalloc(&d_odata, bytes));
    HIP_CHECK(hipMemcpy(d_idata, h_idata, bytes, hipMemcpyHostToDevice));

    dim3 block(TILE_DIM, BLOCK_ROWS);
    dim3 grid((N + TILE_DIM - 1) / TILE_DIM, (N + TILE_DIM - 1) / TILE_DIM);

    // Effective bandwidth: read + write = 2 * N * N * sizeof(float)
    double data_bytes = 2.0 * (double)N * (double)N * sizeof(float);

    // -----------------------------------------------------------------------
    // Variant 1: Naive transpose
    // -----------------------------------------------------------------------
    HIP_CHECK(hipMemset(d_odata, 0, bytes));

    double naive_ms = runTransposeBench([&]() {
        hipLaunchKernelGGL(transpose_naive, grid, block, 0, 0,
                           d_idata, d_odata, N, N);
    });

    double naive_gbs = data_bytes / (naive_ms * 1e-3) / 1e9;

    fprintf(stderr, "Naive transpose:     avg %.4f ms  |  %.2f GB/s\n",
            naive_ms, naive_gbs);

    // Verify naive transpose
    HIP_CHECK(hipMemcpy(h_odata, d_odata, bytes, hipMemcpyDeviceToHost));
    transpose_cpu(h_idata, h_ref, N, N);

    int naive_errors = 0;
    for (int i = 0; i < 1000 && i < (int)num_elems; ++i) {
        if (fabsf(h_odata[i] - h_ref[i]) > 1e-5f) {
            if (naive_errors < 5) {
                fprintf(stderr, "  Naive mismatch at %d: got %f, expected %f\n",
                        i, h_odata[i], h_ref[i]);
            }
            naive_errors++;
        }
    }
    fprintf(stderr, "  Verification: %s\n",
            (naive_errors == 0) ? "PASS" : "FAIL");

    // -----------------------------------------------------------------------
    // Variant 2: Optimized transpose (shared memory + bank-conflict-free)
    // -----------------------------------------------------------------------
    HIP_CHECK(hipMemset(d_odata, 0, bytes));

    double opt_ms = runTransposeBench([&]() {
        hipLaunchKernelGGL(transpose_optimized, grid, block, 0, 0,
                           d_idata, d_odata, N, N);
    });

    double opt_gbs = data_bytes / (opt_ms * 1e-3) / 1e9;

    fprintf(stderr, "Optimized transpose: avg %.4f ms  |  %.2f GB/s\n",
            opt_ms, opt_gbs);

    // Verify optimized transpose
    HIP_CHECK(hipMemcpy(h_odata, d_odata, bytes, hipMemcpyDeviceToHost));

    int opt_errors = 0;
    for (int i = 0; i < 1000 && i < (int)num_elems; ++i) {
        if (fabsf(h_odata[i] - h_ref[i]) > 1e-5f) {
            if (opt_errors < 5) {
                fprintf(stderr, "  Optimized mismatch at %d: got %f, expected %f\n",
                        i, h_odata[i], h_ref[i]);
            }
            opt_errors++;
        }
    }
    fprintf(stderr, "  Verification: %s\n",
            (opt_errors == 0) ? "PASS" : "FAIL");

    // -----------------------------------------------------------------------
    // JSON-lines output to stdout
    // -----------------------------------------------------------------------
    double total_ms = naive_ms * TIMED_ITERS + opt_ms * TIMED_ITERS;

    printf("{\"type\":\"kernel\",\"name\":\"transpose_naive\",\"time_ms\":%.6f,"
           "\"params\":{\"size\":%d,\"block_size\":%d,"
           "\"tile_dim\":%d}}\n",
           naive_ms, N, block_size, tile_dim);

    printf("{\"type\":\"kernel\",\"name\":\"transpose_optimized\",\"time_ms\":%.6f,"
           "\"params\":{\"size\":%d,\"block_size\":%d,"
           "\"tile_dim\":%d}}\n",
           opt_ms, N, block_size, tile_dim);

    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"naive_bandwidth_gbps\",\"value\":%.2f},"
           "{\"name\":\"optimized_bandwidth_gbps\",\"value\":%.2f}]}\n",
           total_ms, naive_gbs, opt_gbs);

    // -----------------------------------------------------------------------
    // Cleanup
    // -----------------------------------------------------------------------
    HIP_CHECK(hipFree(d_idata));
    HIP_CHECK(hipFree(d_odata));
    free(h_idata);
    free(h_odata);
    free(h_ref);

    return 0;
}
