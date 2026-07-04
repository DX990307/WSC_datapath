// shoc_fft.hip — SHOC FFT benchmark (HIP, self-contained)
//
// Cooley-Tukey radix-2 iterative FFT on N complex elements (float2).
// Measures throughput in GFLOPS = 5*N*log2(N) / time_s / 1e9.
//
// Compiles with hipcc (AMD ROCm) or nvcc via HIP compatibility layer (CUDA).
//
// Usage:
//   ./shoc_fft [--size N]
//
//   --size N         FFT size (must be power of 2, default: 1048576)
//
// Output (stdout): JSON-lines protocol
// Output (stderr): human-readable results

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

#ifndef M_PI
#define M_PI 3.14159265358979323846
#endif

// ---------------------------------------------------------------------------
// Error-checking macro
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
// Utility: check power of two and compute log2
// ---------------------------------------------------------------------------

static bool isPowerOfTwo(int n) { return n > 0 && (n & (n - 1)) == 0; }

static int ilog2(int n) {
    int r = 0;
    while ((1 << r) < n) ++r;
    return r;
}

// ---------------------------------------------------------------------------
// Kernel: bit-reversal permutation
// ---------------------------------------------------------------------------

__global__ void bit_reverse_kernel(float2* __restrict__ data, int N, int log2N) {
    int idx = blockIdx.x * blockDim.x + threadIdx.x;
    if (idx >= N) return;

    // Compute bit-reversed index
    int rev = 0;
    int temp = idx;
    for (int i = 0; i < log2N; ++i) {
        rev = (rev << 1) | (temp & 1);
        temp >>= 1;
    }

    // Swap only once per pair (rev > idx avoids double-swap)
    if (rev > idx) {
        float2 tmp = data[idx];
        data[idx] = data[rev];
        data[rev] = tmp;
    }
}

// ---------------------------------------------------------------------------
// Kernel: FFT butterfly for one stage
//   Each thread handles one butterfly operation.
//   Thread idx maps to a unique (top, bot) pair for the given stage.
// ---------------------------------------------------------------------------

__global__ void fft_butterfly_kernel(float2* __restrict__ data, int N, int stage) {
    int idx = blockIdx.x * blockDim.x + threadIdx.x;
    if (idx >= N / 2) return;

    int m      = 1 << (stage + 1);   // butterfly group size
    int half_m = 1 << stage;          // half of group size

    int group = idx / half_m;
    int pair  = idx % half_m;

    int top = group * m + pair;
    int bot = top + half_m;

    float angle = -2.0f * (float)M_PI * (float)pair / (float)m;
    float w_re  = cosf(angle);
    float w_im  = sinf(angle);

    float2 u = data[top];
    float2 v = data[bot];

    // Complex multiply: t = w * v
    float t_re = w_re * v.x - w_im * v.y;
    float t_im = w_re * v.y + w_im * v.x;

    data[top] = make_float2(u.x + t_re, u.y + t_im);
    data[bot] = make_float2(u.x - t_re, u.y - t_im);
}

// ---------------------------------------------------------------------------
// CPU reference FFT (iterative Cooley-Tukey) for verification
// ---------------------------------------------------------------------------

static void fft_cpu(float2* data, int N) {
    int log2N = ilog2(N);

    // Bit-reversal permutation
    for (int i = 0; i < N; ++i) {
        int rev = 0, temp = i;
        for (int b = 0; b < log2N; ++b) {
            rev = (rev << 1) | (temp & 1);
            temp >>= 1;
        }
        if (rev > i) {
            float2 tmp = data[i];
            data[i] = data[rev];
            data[rev] = tmp;
        }
    }

    // Butterfly stages
    for (int s = 0; s < log2N; ++s) {
        int m      = 1 << (s + 1);
        int half_m = 1 << s;
        for (int k = 0; k < N; k += m) {
            for (int j = 0; j < half_m; ++j) {
                float angle = -2.0f * (float)M_PI * (float)j / (float)m;
                float w_re = cosf(angle);
                float w_im = sinf(angle);

                float2 u = data[k + j];
                float2 v = data[k + j + half_m];

                float t_re = w_re * v.x - w_im * v.y;
                float t_im = w_re * v.y + w_im * v.x;

                data[k + j].x          = u.x + t_re;
                data[k + j].y          = u.y + t_im;
                data[k + j + half_m].x = u.x - t_re;
                data[k + j + half_m].y = u.y - t_im;
            }
        }
    }
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------

int main(int argc, char** argv) {
    setvbuf(stdout, NULL, _IONBF, 0);

    int N     = parseIntParam(argc, argv, "--size", 1048576);
    const char* precision = "float";
    int batch_count = 1;

    const char* env_val;
    env_val = getenv("BENCH_PARAM_size");
    if (env_val) N = atoi(env_val);
    env_val = getenv("BENCH_PARAM_precision");
    if (env_val) precision = env_val;
    env_val = getenv("BENCH_PARAM_batch_count");
    if (env_val) batch_count = atoi(env_val);
    int num_warmup = 5;

    if (!isPowerOfTwo(N)) {
        fprintf(stderr, "Error: N=%d must be a power of 2\n", N);
        return 1;
    }

    int log2N = ilog2(N);

    size_t bytes = (size_t)N * sizeof(float2);

    // Print device info
    int device_id = 0;
    HIP_CHECK(hipGetDevice(&device_id));
    hipDeviceProp_t prop;
    HIP_CHECK(hipGetDeviceProperties(&prop, device_id));
    fprintf(stderr, "Device: %s (id=%d)\n", prop.name, device_id);
    fprintf(stderr, "FFT size: %d (2^%d) warmup + %d timed\n\n",
            N, log2N, num_warmup);

    // Host allocation + initialization
    float2* h_data = (float2*)malloc(bytes);
    float2* h_orig = (float2*)malloc(bytes);  // keep original for re-init
    for (int i = 0; i < N; ++i) {
        h_data[i].x = (float)(i % 1024) * 0.001f;
        h_data[i].y = 0.0f;
        h_orig[i]   = h_data[i];
    }

    // Device allocation
    float2* d_data;
    HIP_CHECK(hipMalloc(&d_data, bytes));

    // Kernel launch config
    int blockSize = 256;
    int gridBitRev    = (N       + blockSize - 1) / blockSize;
    int gridButterfly = (N / 2   + blockSize - 1) / blockSize;

    // Lambda: run a full FFT (bit-reversal + all butterfly stages)
    auto run_fft = [&]() {
        hipLaunchKernelGGL(bit_reverse_kernel, dim3(gridBitRev), dim3(blockSize),
                           0, 0, d_data, N, log2N);
        for (int s = 0; s < log2N; ++s) {
            hipLaunchKernelGGL(fft_butterfly_kernel, dim3(gridButterfly),
                               dim3(blockSize), 0, 0, d_data, N, s);
        }
    };

    // -------------------------------------------------------------------
    // Warmup
    // -------------------------------------------------------------------
    for (int w = 0; w < num_warmup; ++w) {
        HIP_CHECK(hipMemcpy(d_data, h_orig, bytes, hipMemcpyHostToDevice));
        run_fft();
        HIP_CHECK(hipDeviceSynchronize());
    }

    // -------------------------------------------------------------------
    // Timed iterations — time bit_reverse and butterfly separately
    // -------------------------------------------------------------------
    hipEvent_t evStart, evStop;
    HIP_CHECK(hipEventCreate(&evStart));
    HIP_CHECK(hipEventCreate(&evStop));

    std::vector<double> br_times(1);
    std::vector<double> bf_times(1);
    for (int i = 0; i < 1; ++i) {
        HIP_CHECK(hipMemcpy(d_data, h_orig, bytes, hipMemcpyHostToDevice));

        // Time bit-reverse kernel
        HIP_CHECK(hipEventRecord(evStart, 0));
        hipLaunchKernelGGL(bit_reverse_kernel, dim3(gridBitRev), dim3(blockSize),
                           0, 0, d_data, N, log2N);
        HIP_CHECK(hipEventRecord(evStop, 0));
        HIP_CHECK(hipEventSynchronize(evStop));
        float ms = 0.0f;
        HIP_CHECK(hipEventElapsedTime(&ms, evStart, evStop));
        br_times[i] = (double)ms;

        // Time all butterfly stages
        HIP_CHECK(hipEventRecord(evStart, 0));
        for (int s = 0; s < log2N; ++s) {
            hipLaunchKernelGGL(fft_butterfly_kernel, dim3(gridButterfly),
                               dim3(blockSize), 0, 0, d_data, N, s);
        }
        HIP_CHECK(hipEventRecord(evStop, 0));
        HIP_CHECK(hipEventSynchronize(evStop));
        HIP_CHECK(hipEventElapsedTime(&ms, evStart, evStop));
        bf_times[i] = (double)ms;
    }

    HIP_CHECK(hipEventDestroy(evStart));
    HIP_CHECK(hipEventDestroy(evStop));

    // Compute average times
    double br_sum = 0.0, bf_sum = 0.0;
    for (int i = 0; i < 1; ++i) {
        br_sum += br_times[i];
        bf_sum += bf_times[i];
    }
    double br_avg_ms = br_sum / 1;
    double bf_avg_ms = bf_sum / 1;
    double total_avg_ms = br_avg_ms + bf_avg_ms;

    // GFLOPS = 5 * N * log2(N) / time_s / 1e9
    double flops  = 5.0 * (double)N * (double)log2N;
    double gflops = flops / (total_avg_ms * 1e-3) / 1e9;

    // JSON-lines output
    printf("{\"type\":\"kernel\",\"name\":\"bit_reverse_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"size\":%d,\"precision\":\"%s\",\"batch_count\":%d}}\n",
           br_avg_ms, N, precision, batch_count);
    printf("{\"type\":\"kernel\",\"name\":\"fft_butterfly_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"size\":%d,\"precision\":\"%s\",\"batch_count\":%d}}\n",
           bf_avg_ms, N, precision, batch_count);
    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"gflops\",\"value\":%.4f}]}\n",
           total_avg_ms, gflops);

    // Human-readable output to stderr
    fprintf(stderr, "Average time: %.4f ms (bit_reverse: %.4f ms, butterfly: %.4f ms)\n",
            total_avg_ms, br_avg_ms, bf_avg_ms);
    fprintf(stderr, "Performance:  %.4f GFLOPS\n", gflops);

    // -------------------------------------------------------------------
    // Verification (only for small sizes)
    // -------------------------------------------------------------------
    if (N <= 8192) {
        HIP_CHECK(hipMemcpy(h_data, d_data, bytes, hipMemcpyDeviceToHost));

        // CPU reference
        float2* h_ref = (float2*)malloc(bytes);
        memcpy(h_ref, h_orig, bytes);
        fft_cpu(h_ref, N);

        int errors = 0;
        for (int i = 0; i < N; ++i) {
            float diff_re = fabsf(h_data[i].x - h_ref[i].x);
            float diff_im = fabsf(h_data[i].y - h_ref[i].y);
            float tol = 1e-2f * (fabsf(h_ref[i].x) + fabsf(h_ref[i].y)) + 1e-4f;
            if (diff_re > tol || diff_im > tol) {
                if (errors < 10) {
                    fprintf(stderr, "Mismatch at %d: GPU=(%.6f,%.6f) CPU=(%.6f,%.6f)\n",
                            i, h_data[i].x, h_data[i].y,
                            h_ref[i].x, h_ref[i].y);
                }
                errors++;
            }
        }
        if (errors > 0)
            fprintf(stderr, "FAIL: %d errors out of %d\n", errors, N);
        else
            fprintf(stderr, "PASS\n");

        free(h_ref);
    } else {
        // Parseval's check: sum|X|^2 should equal N * sum|x|^2
        HIP_CHECK(hipMemcpy(h_data, d_data, bytes, hipMemcpyDeviceToHost));

        double energy_time = 0.0;
        for (int i = 0; i < N; ++i)
            energy_time += (double)h_orig[i].x * h_orig[i].x
                         + (double)h_orig[i].y * h_orig[i].y;

        double energy_freq = 0.0;
        for (int i = 0; i < N; ++i)
            energy_freq += (double)h_data[i].x * h_data[i].x
                         + (double)h_data[i].y * h_data[i].y;

        double ratio = energy_freq / ((double)N * energy_time);
        fprintf(stderr, "Parseval ratio: %.6f (expected ~1.0)\n", ratio);
        if (fabs(ratio - 1.0) < 0.01)
            fprintf(stderr, "PASS (Parseval)\n");
        else
            fprintf(stderr, "FAIL (Parseval): ratio = %.6f\n", ratio);
    }

    HIP_CHECK(hipFree(d_data));
    free(h_data);
    free(h_orig);

    return 0;
}
