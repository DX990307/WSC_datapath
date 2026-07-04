// heteromark_fir.hip — HeteroMark FIR filter benchmark (HIP)
//
// 1D FIR (Finite Impulse Response) convolution:
//   output[i] = sum(coeff[k] * input[i - k]) for k = 0..NUM_TAPS-1
//
// Each thread computes one output sample. Filter coefficients are loaded
// into shared memory for performance.
//
// Compiles with hipcc (AMD ROCm) or nvcc via HIP compatibility layer (CUDA).
//
// Usage:
//   ./heteromark_fir [--size N]
//
//   --size N         Number of input samples (default: 1048576)
//
// Output (stdout): CSV row — heteromark_fir,<N>,<time_ms>,<GBs>
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
#include <vector>

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
// Constants
// ---------------------------------------------------------------------------

#define NUM_TAPS 128

// ---------------------------------------------------------------------------
// Kernel: FIR filter with shared memory for coefficients
//   Each thread computes one output sample.
//   Coefficients are loaded into shared memory once per block.
// ---------------------------------------------------------------------------

__global__ void fir_filter_kernel(const float* __restrict__ input,
                                   float* __restrict__ output,
                                   const float* __restrict__ coeff,
                                   int num_samples,
                                   int num_taps) {
    __shared__ float s_coeff[NUM_TAPS];

    // Cooperatively load coefficients into shared memory
    for (int t = threadIdx.x; t < num_taps; t += blockDim.x) {
        s_coeff[t] = coeff[t];
    }
    __syncthreads();

    int idx = blockIdx.x * blockDim.x + threadIdx.x;
    if (idx >= num_samples) return;

    float sum = 0.0f;
    for (int k = 0; k < num_taps; ++k) {
        int in_idx = idx - k;
        if (in_idx >= 0) {
            sum += s_coeff[k] * input[in_idx];
        }
    }

    output[idx] = sum;
}

// ---------------------------------------------------------------------------
// CPU reference: FIR filter
// ---------------------------------------------------------------------------

static void fir_cpu(const float* input, float* output, const float* coeff,
                     int num_samples, int num_taps) {
    for (int i = 0; i < num_samples; ++i) {
        float sum = 0.0f;
        for (int k = 0; k < num_taps; ++k) {
            int in_idx = i - k;
            if (in_idx >= 0) {
                sum += coeff[k] * input[in_idx];
            }
        }
        output[i] = sum;
    }
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------

int main(int argc, char** argv) {
    setvbuf(stdout, NULL, _IONBF, 0);

    // Defaults from params.json
    int size       = 1048576;
    int num_taps   = 64;
    int block_size = 256;

    // Command-line fallback
    size       = parseIntParam(argc, argv, "--size", size);
    num_taps   = parseIntParam(argc, argv, "--num_taps", num_taps);
    block_size = parseIntParam(argc, argv, "--block_size", block_size);

    // Environment variable overrides
    const char* env_val;
    env_val = getenv("BENCH_PARAM_size");
    if (env_val) size = atoi(env_val);
    env_val = getenv("BENCH_PARAM_num_taps");
    if (env_val) num_taps = atoi(env_val);
    env_val = getenv("BENCH_PARAM_block_size");
    if (env_val) block_size = atoi(env_val);
    int num_warmup = 5;

    int N     = size;
    int taps  = num_taps;

    size_t input_bytes  = (size_t)N * sizeof(float);
    size_t output_bytes = (size_t)N * sizeof(float);
    size_t coeff_bytes  = (size_t)taps * sizeof(float);

    // Print device info
    int device_id = 0;
    HIP_CHECK(hipGetDevice(&device_id));
    hipDeviceProp_t prop;
    HIP_CHECK(hipGetDeviceProperties(&prop, device_id));
    fprintf(stderr, "Device: %s (id=%d)\n", prop.name, device_id);
    fprintf(stderr, "FIR filter  |  Samples: %d  |  Taps: %d  |  "
            "Iterations: %d warmup + %d timed\n\n",
            N, taps, num_warmup);

    // Host allocation + initialization
    float* h_input  = (float*)malloc(input_bytes);
    float* h_output = (float*)malloc(output_bytes);
    float* h_coeff  = (float*)malloc(coeff_bytes);

    // Initialize input with a simple pattern
    for (int i = 0; i < N; ++i) {
        h_input[i] = sinf((float)i * 0.01f) + 0.5f;
    }

    // Initialize filter coefficients (simple low-pass-like)
    float coeff_sum = 0.0f;
    for (int k = 0; k < taps; ++k) {
        h_coeff[k] = 1.0f / (float)(k + 1);
        coeff_sum += h_coeff[k];
    }
    // Normalize coefficients
    for (int k = 0; k < taps; ++k) {
        h_coeff[k] /= coeff_sum;
    }

    // Device allocation
    float* d_input;
    float* d_output;
    float* d_coeff;
    HIP_CHECK(hipMalloc(&d_input, input_bytes));
    HIP_CHECK(hipMalloc(&d_output, output_bytes));
    HIP_CHECK(hipMalloc(&d_coeff, coeff_bytes));

    // Copy input and coefficients to device
    HIP_CHECK(hipMemcpy(d_input, h_input, input_bytes, hipMemcpyHostToDevice));
    HIP_CHECK(hipMemcpy(d_coeff, h_coeff, coeff_bytes, hipMemcpyHostToDevice));

    // Kernel launch config
    int blockSize = block_size;
    int gridSize  = (N + blockSize - 1) / blockSize;

    // Lambda: run FIR filter
    auto run_fir = [&]() {
        hipLaunchKernelGGL(fir_filter_kernel, dim3(gridSize), dim3(blockSize),
                           0, 0, d_input, d_output, d_coeff, N, taps);
    };

    // -------------------------------------------------------------------
    // Warmup
    // -------------------------------------------------------------------
    for (int w = 0; w < num_warmup; ++w) {
        run_fir();
        HIP_CHECK(hipDeviceSynchronize());
    }

    // -------------------------------------------------------------------
    // Timed iterations
    // -------------------------------------------------------------------
    hipEvent_t evStart, evStop;
    HIP_CHECK(hipEventCreate(&evStart));
    HIP_CHECK(hipEventCreate(&evStop));

    std::vector<double> times(1);
    for (int i = 0; i < 1; ++i) {
        HIP_CHECK(hipEventRecord(evStart, 0));
        run_fir();
        HIP_CHECK(hipEventRecord(evStop, 0));
        HIP_CHECK(hipEventSynchronize(evStop));

        float ms = 0.0f;
        HIP_CHECK(hipEventElapsedTime(&ms, evStart, evStop));
        times[i] = (double)ms;
    }

    HIP_CHECK(hipEventDestroy(evStart));
    HIP_CHECK(hipEventDestroy(evStop));

    // Compute average time
    double sum = 0.0;
    for (int i = 0; i < 1; ++i) sum += times[i];
    double avg_ms = sum / 1;

    // GB/s = (input_size + output_size) * sizeof(float) / time_s / 1e9
    double total_bytes = (double)(input_bytes + output_bytes);
    double gbs = total_bytes / (avg_ms * 1e-3) / 1e9;

    double total_ms = sum;

    // JSON-lines kernel event
    printf("{\"type\":\"kernel\",\"name\":\"fir_filter_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"size\":%d,\"num_taps\":%d,"
           "\"block_size\":%d}}\n",
           avg_ms, size, num_taps, block_size);

    // JSON-lines summary event
    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"throughput_gbps\",\"value\":%.2f}]}\n",
           total_ms, gbs);

    // Human-readable output to stderr
    fprintf(stderr, "Average time: %.4f ms\n", avg_ms);
    fprintf(stderr, "Throughput:   %.4f GB/s\n", gbs);

    // -------------------------------------------------------------------
    // Verification (compare first 1024 samples with CPU reference)
    // -------------------------------------------------------------------
    {
        int verify_n = (N < 1024) ? N : 1024;
        HIP_CHECK(hipMemcpy(h_output, d_output, output_bytes, hipMemcpyDeviceToHost));

        float* h_ref = (float*)malloc(output_bytes);
        fir_cpu(h_input, h_ref, h_coeff, verify_n, taps);

        int errors = 0;
        for (int i = 0; i < verify_n; ++i) {
            float diff = fabsf(h_output[i] - h_ref[i]);
            float tol = 1e-4f * fabsf(h_ref[i]) + 1e-6f;
            if (diff > tol) {
                if (errors < 10) {
                    fprintf(stderr, "Mismatch at %d: GPU=%.6f CPU=%.6f diff=%.2e\n",
                            i, h_output[i], h_ref[i], diff);
                }
                errors++;
            }
        }
        if (errors > 0)
            fprintf(stderr, "FAIL: %d errors in first %d samples\n",
                    errors, verify_n);
        else
            fprintf(stderr, "PASS\n");

        free(h_ref);
    }

    HIP_CHECK(hipFree(d_input));
    HIP_CHECK(hipFree(d_output));
    HIP_CHECK(hipFree(d_coeff));
    free(h_input);
    free(h_output);
    free(h_coeff);

    return 0;
}
