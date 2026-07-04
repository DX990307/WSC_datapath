// tango_binomial_options.hip — Binomial option pricing (HIP)
//
// Cox-Ross-Rubinstein (CRR) binomial tree model for American put option
// pricing. Each threadblock processes one option, threads collaborate on
// backward induction through the tree using shared memory.
//
// Compiles with hipcc (AMD ROCm) or nvcc via HIP compatibility layer (CUDA).
//
// Usage:
//   ./tango_binomial_options [--options N] [--steps S]
//
//   --options N      Number of options (default: 512)
//   --steps S        Steps per option (default: 1024)
//
// Output (stdout): CSV row — tango_binomial_options,<N>x<steps>,<time_ms>,<opts/sec>
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
// Option parameters structure
// ---------------------------------------------------------------------------

struct OptionData {
    float S;      // stock price
    float K;      // strike price
    float T;      // time to expiration
    float r;      // risk-free rate
    float sigma;  // volatility
};

// ---------------------------------------------------------------------------
// Binomial option pricing kernel
//   One threadblock per option. Threads collaborate via shared memory to
//   perform backward induction through the binomial tree.
//   Uses stride loops so blockDim.x can be smaller than numSteps+1.
// ---------------------------------------------------------------------------

extern __shared__ float shared_values[];

__global__ void binomial_kernel(
    const OptionData* __restrict__ options,
    float*            __restrict__ prices,
    int               numSteps)
{
    int optIdx = blockIdx.x;
    int tid    = threadIdx.x;
    int bdim   = blockDim.x;

    OptionData opt = options[optIdx];

    // CRR parameters
    float dt = opt.T / (float)numSteps;
    float u  = expf(opt.sigma * sqrtf(dt));        // up factor
    float d  = 1.0f / u;                           // down factor
    float R  = expf(opt.r * dt);                   // risk-free growth
    float Rinv = 1.0f / R;
    float p  = (R - d) / (u - d);                  // risk-neutral probability
    float q  = 1.0f - p;

    int numNodes = numSteps + 1;

    // Step 1: Compute terminal payoffs (stride loop)
    for (int j = tid; j < numNodes; j += bdim) {
        float ST = opt.S * powf(u, (float)(2 * j - numSteps));
        float payoff = fmaxf(opt.K - ST, 0.0f);
        shared_values[j] = payoff;
    }
    __syncthreads();

    // Step 2: Backward induction
    for (int step = numSteps; step > 0; --step) {
        for (int j = tid; j < step; j += bdim) {
            // Expected value discounted
            float cont = Rinv * (p * shared_values[j + 1] + q * shared_values[j]);
            // American option: can exercise early
            float ST = opt.S * powf(u, (float)(2 * j - (step - 1)));
            float exercise = fmaxf(opt.K - ST, 0.0f);
            shared_values[j] = fmaxf(cont, exercise);
        }
        __syncthreads();
    }

    // Thread 0 writes the option price
    if (tid == 0) {
        prices[optIdx] = shared_values[0];
    }
}

// ---------------------------------------------------------------------------
// CPU reference: binomial option pricing
// ---------------------------------------------------------------------------

static float binomial_cpu(OptionData opt, int numSteps) {
    float dt   = opt.T / (float)numSteps;
    float u    = expf(opt.sigma * sqrtf(dt));
    float d    = 1.0f / u;
    float R    = expf(opt.r * dt);
    float Rinv = 1.0f / R;
    float p    = (R - d) / (u - d);
    float q    = 1.0f - p;

    std::vector<float> vals((size_t)(numSteps + 1));

    // Terminal payoffs
    for (int j = 0; j <= numSteps; ++j) {
        float ST = opt.S * powf(u, (float)(2 * j - numSteps));
        vals[j] = fmaxf(opt.K - ST, 0.0f);
    }

    // Backward induction
    for (int step = numSteps; step > 0; --step) {
        for (int j = 0; j < step; ++j) {
            float cont = Rinv * (p * vals[j + 1] + q * vals[j]);
            float ST = opt.S * powf(u, (float)(2 * j - (step - 1)));
            float exercise = fmaxf(opt.K - ST, 0.0f);
            vals[j] = fmaxf(cont, exercise);
        }
    }

    return vals[0];
}

// ---------------------------------------------------------------------------
// Simple deterministic pseudo-random in range [lo, hi]
// ---------------------------------------------------------------------------

static float randRange(unsigned& seed, float lo, float hi) {
    seed = seed * 1103515245u + 12345u;
    float t = (float)(seed & 0x7fffffffu) / (float)0x7fffffffu;
    return lo + t * (hi - lo);
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------

int main(int argc, char** argv) {
    setvbuf(stdout, NULL, _IONBF, 0);

    // Defaults from params.json
    int numOptions = 512;
    int numSteps   = 1024;
    const char* precision = "float";

    // Command-line fallback
    numOptions = parseIntParam(argc, argv, "--options", numOptions);
    numSteps   = parseIntParam(argc, argv, "--steps",   numSteps);

    // Environment variable overrides
    const char* env_val;
    env_val = getenv("BENCH_PARAM_options");
    if (env_val) numOptions = atoi(env_val);
    env_val = getenv("BENCH_PARAM_steps");
    if (env_val) numSteps = atoi(env_val);
    env_val = getenv("BENCH_PARAM_precision");
    if (env_val) precision = env_val;
    int num_warmup = 5;


    // Shared memory must hold (numSteps+1) floats; clamp to reasonable limit
    if (numSteps > 4096) {
        fprintf(stderr, "Warning: clamping steps to 4096 (shared memory limit)\n");
        numSteps = 4096;
    }

    size_t optBytes   = (size_t)numOptions * sizeof(OptionData);
    size_t priceBytes = (size_t)numOptions * sizeof(float);

    // Print device info
    int device_id = 0;
    HIP_CHECK(hipGetDevice(&device_id));
    hipDeviceProp_t prop;
    HIP_CHECK(hipGetDeviceProperties(&prop, device_id));
    fprintf(stderr, "Device: %s (id=%d)\n", prop.name, device_id);
    fprintf(stderr, "Options: %d  |  Steps: %d  |  Iterations: 5 warmup + %d timed\n\n",
            numOptions, numSteps);

    // Host allocation + initialization
    OptionData* h_options = (OptionData*)malloc(optBytes);
    float*      h_prices  = (float*)malloc(priceBytes);

    unsigned seed = 42u;
    for (int i = 0; i < numOptions; ++i) {
        h_options[i].S     = randRange(seed, 5.0f, 200.0f);
        h_options[i].K     = randRange(seed, 1.0f, 300.0f);
        h_options[i].T     = randRange(seed, 0.25f, 10.0f);
        h_options[i].r     = 0.02f;
        h_options[i].sigma = randRange(seed, 0.1f, 1.0f);
    }

    // Device allocation
    OptionData* d_options;
    float*      d_prices;
    HIP_CHECK(hipMalloc(&d_options, optBytes));
    HIP_CHECK(hipMalloc(&d_prices,  priceBytes));
    HIP_CHECK(hipMemcpy(d_options, h_options, optBytes, hipMemcpyHostToDevice));

    // Kernel launch config: one block per option
    // Clamp blockSize to 1024 (HIP/CUDA max), kernel uses stride loops
    int blockSize = (numSteps + 1 > 1024) ? 1024 : (numSteps + 1);
    int gridSize  = numOptions;
    size_t sharedBytes = (size_t)(numSteps + 1) * sizeof(float);

    // -------------------------------------------------------------------
    // Warmup
    // -------------------------------------------------------------------
    for (int w = 0; w < num_warmup; ++w) {
        hipLaunchKernelGGL(binomial_kernel, dim3(gridSize), dim3(blockSize),
                           sharedBytes, 0, d_options, d_prices, numSteps);
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
        hipLaunchKernelGGL(binomial_kernel, dim3(gridSize), dim3(blockSize),
                           sharedBytes, 0, d_options, d_prices, numSteps);
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

    // options/sec = numOptions / time_s
    double opts_per_sec = (double)numOptions / (avg_ms * 1e-3);

    double total_ms = sum;

    // JSON-lines kernel event
    printf("{\"type\":\"kernel\",\"name\":\"binomial_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"options\":%d,\"steps\":%d,"
           "\"precision\":\"%s\"}}\n",
           avg_ms, numOptions, numSteps, precision);

    // JSON-lines summary event
    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"options_per_sec\",\"value\":%.2f}]}\n",
           total_ms, opts_per_sec);

    // Human-readable output to stderr
    fprintf(stderr, "Average time: %.4f ms\n", avg_ms);
    fprintf(stderr, "Performance:  %.4f options/sec\n", opts_per_sec);

    // -------------------------------------------------------------------
    // Verification
    // -------------------------------------------------------------------
    HIP_CHECK(hipMemcpy(h_prices, d_prices, priceBytes, hipMemcpyDeviceToHost));

    int verifyN = (numOptions < 10) ? numOptions : 10;
    int errors = 0;
    for (int i = 0; i < verifyN; ++i) {
        float ref = binomial_cpu(h_options[i], numSteps);
        float tol = 1e-2f * fabsf(ref) + 1e-4f;
        if (fabsf(h_prices[i] - ref) > tol) {
            fprintf(stderr, "Mismatch at option %d: GPU=%.6f CPU=%.6f (diff=%.6f)\n",
                    i, h_prices[i], ref, fabsf(h_prices[i] - ref));
            errors++;
        }
    }

    if (errors > 0)
        fprintf(stderr, "FAIL: %d errors out of %d verified\n", errors, verifyN);
    else
        fprintf(stderr, "PASS\n");

    HIP_CHECK(hipFree(d_options));
    HIP_CHECK(hipFree(d_prices));
    free(h_options);
    free(h_prices);

    return 0;
}
