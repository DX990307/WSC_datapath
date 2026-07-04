// tango_blackscholes.cu — Black-Scholes European option pricing (CUDA)
//
// Computes call and put option prices for N options using the
// Black-Scholes closed-form formula with a polynomial approximation
// of the cumulative normal distribution.
//
// Native CUDA implementation.
//
// Usage:
//   ./tango_blackscholes [--size N]
//
//   --size N         Number of options (default: 4194304 = 4M)
//
// Output (stdout): CSV row — tango_blackscholes,<N>,<time_ms>,<Moptions_per_sec>
// Output (stderr): human-readable results

#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <cmath>
#include <cfloat>
#include <vector>
#include <cuda_runtime.h>

#ifndef M_PI
#define M_PI 3.14159265358979323846
#endif

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
// Cumulative normal distribution (polynomial approximation)
// Abramowitz & Stegun 26.2.17
// ---------------------------------------------------------------------------

__device__ __host__ inline float cnd(float d) {
    const float A1 = 0.31938153f;
    const float A2 = -0.356563782f;
    const float A3 = 1.781477937f;
    const float A4 = -1.821255978f;
    const float A5 = 1.330274429f;
    const float RSQRT2PI = 0.39894228040143267793994605993438f;

    float K = 1.0f / (1.0f + 0.2316419f * fabsf(d));
    float cnd_val = RSQRT2PI * expf(-0.5f * d * d) *
                    (K * (A1 + K * (A2 + K * (A3 + K * (A4 + K * A5)))));

    if (d > 0.0f) cnd_val = 1.0f - cnd_val;
    return cnd_val;
}

// ---------------------------------------------------------------------------
// Black-Scholes kernel: each thread prices one option (call + put)
// ---------------------------------------------------------------------------

__global__ void blackscholes_kernel(
    const float* __restrict__ S,       // stock price
    const float* __restrict__ K,       // strike price
    const float* __restrict__ T,       // time to expiration
    const float* __restrict__ sigma,   // volatility
    float        r,                    // risk-free rate
    float*       callPrice,
    float*       putPrice,
    int          N)
{
    int idx = blockIdx.x * blockDim.x + threadIdx.x;
    if (idx >= N) return;

    float s  = S[idx];
    float k  = K[idx];
    float t  = T[idx];
    float v  = sigma[idx];

    float sqrtT  = sqrtf(t);
    float d1     = (logf(s / k) + (r + 0.5f * v * v) * t) / (v * sqrtT);
    float d2     = d1 - v * sqrtT;

    float expRT  = expf(-r * t);
    float cnd_d1 = cnd(d1);
    float cnd_d2 = cnd(d2);

    callPrice[idx] = s * cnd_d1 - k * expRT * cnd_d2;
    putPrice[idx]  = k * expRT * (1.0f - cnd_d2) - s * (1.0f - cnd_d1);
}

// ---------------------------------------------------------------------------
// CPU reference for verification
// ---------------------------------------------------------------------------

static float cnd_cpu(float d) {
    const float A1 = 0.31938153f;
    const float A2 = -0.356563782f;
    const float A3 = 1.781477937f;
    const float A4 = -1.821255978f;
    const float A5 = 1.330274429f;
    const float RSQRT2PI = 0.39894228040143267793994605993438f;

    float K = 1.0f / (1.0f + 0.2316419f * fabsf(d));
    float cnd_val = RSQRT2PI * expf(-0.5f * d * d) *
                    (K * (A1 + K * (A2 + K * (A3 + K * (A4 + K * A5)))));

    if (d > 0.0f) cnd_val = 1.0f - cnd_val;
    return cnd_val;
}

static void blackscholes_cpu(
    const float* S, const float* K, const float* T, const float* sigma,
    float r, float* callPrice, float* putPrice, int N)
{
    for (int i = 0; i < N; ++i) {
        float s     = S[i];
        float k     = K[i];
        float t     = T[i];
        float v     = sigma[i];
        float sqrtT = sqrtf(t);
        float d1    = (logf(s / k) + (r + 0.5f * v * v) * t) / (v * sqrtT);
        float d2    = d1 - v * sqrtT;
        float expRT = expf(-r * t);
        float cd1   = cnd_cpu(d1);
        float cd2   = cnd_cpu(d2);
        callPrice[i] = s * cd1 - k * expRT * cd2;
        putPrice[i]  = k * expRT * (1.0f - cd2) - s * (1.0f - cd1);
    }
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
    int size       = 4194304;
    int block_size = 256;
    const char* precision = "float";

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
    int num_warmup = 0;

    int N     = size;

    const float r = 0.02f;  // risk-free rate

    size_t bytes = (size_t)N * sizeof(float);

    // Print device info
    int device_id = 0;
    CUDA_CHECK(cudaGetDevice(&device_id));
    cudaDeviceProp prop;
    CUDA_CHECK(cudaGetDeviceProperties(&prop, device_id));
    fprintf(stderr, "Device: %s (id=%d)\n", prop.name, device_id);
    fprintf(stderr, "Options: %d  |  Iterations: 5 warmup + %d timed\n\n",
            N);

    // Host allocation + initialization
    float* h_S     = (float*)malloc(bytes);
    float* h_K     = (float*)malloc(bytes);
    float* h_T     = (float*)malloc(bytes);
    float* h_sigma = (float*)malloc(bytes);
    float* h_call  = (float*)malloc(bytes);
    float* h_put   = (float*)malloc(bytes);

    unsigned seed = 42u;
    for (int i = 0; i < N; ++i) {
        h_S[i]     = randRange(seed, 5.0f, 200.0f);
        h_K[i]     = randRange(seed, 1.0f, 300.0f);
        h_T[i]     = randRange(seed, 0.25f, 10.0f);
        h_sigma[i] = randRange(seed, 0.1f, 1.0f);
    }

    // Device allocation
    float *d_S, *d_K, *d_T, *d_sigma, *d_call, *d_put;
    CUDA_CHECK(cudaMalloc(&d_S,     bytes));
    CUDA_CHECK(cudaMalloc(&d_K,     bytes));
    CUDA_CHECK(cudaMalloc(&d_T,     bytes));
    CUDA_CHECK(cudaMalloc(&d_sigma, bytes));
    CUDA_CHECK(cudaMalloc(&d_call,  bytes));
    CUDA_CHECK(cudaMalloc(&d_put,   bytes));

    CUDA_CHECK(cudaMemcpy(d_S,     h_S,     bytes, cudaMemcpyHostToDevice));
    CUDA_CHECK(cudaMemcpy(d_K,     h_K,     bytes, cudaMemcpyHostToDevice));
    CUDA_CHECK(cudaMemcpy(d_T,     h_T,     bytes, cudaMemcpyHostToDevice));
    CUDA_CHECK(cudaMemcpy(d_sigma, h_sigma, bytes, cudaMemcpyHostToDevice));

    // Kernel launch config
    int blockSize = block_size;
    int gridSize  = (N + blockSize - 1) / blockSize;

    // -------------------------------------------------------------------
    // Warmup
    // -------------------------------------------------------------------
    for (int w = 0; w < num_warmup; ++w) {
        blackscholes_kernel<<<dim3(gridSize), dim3(blockSize), 0, 0>>>(d_S, d_K, d_T, d_sigma, r, d_call, d_put, N);
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
        blackscholes_kernel<<<dim3(gridSize), dim3(blockSize), 0, 0>>>(d_S, d_K, d_T, d_sigma, r, d_call, d_put, N);
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

    // M options/sec = N / time_s / 1e6
    double mopts = (double)N / (avg_ms * 1e-3) / 1e6;

    double total_ms = sum;

    // JSON-lines kernel event
    printf("{\"type\":\"kernel\",\"name\":\"blackscholes_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"size\":%d\"block_size\":%d,"
           "\"precision\":\"%s\"}}\n",
           avg_ms, size, block_size, precision);

    // JSON-lines summary event
    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"moptions_per_sec\",\"value\":%.2f}]}\n",
           total_ms, mopts);

    // Human-readable output to stderr
    fprintf(stderr, "Average time: %.4f ms\n", avg_ms);
    fprintf(stderr, "Performance:  %.4f M options/sec\n", mopts);

    // -------------------------------------------------------------------
    // Verification
    // -------------------------------------------------------------------
    CUDA_CHECK(cudaMemcpy(h_call, d_call, bytes, cudaMemcpyDeviceToHost));
    CUDA_CHECK(cudaMemcpy(h_put,  d_put,  bytes, cudaMemcpyDeviceToHost));

    // CPU reference (verify first 10000 elements)
    int verifyN = (N < 10000) ? N : 10000;
    float* ref_call = (float*)malloc((size_t)verifyN * sizeof(float));
    float* ref_put  = (float*)malloc((size_t)verifyN * sizeof(float));
    blackscholes_cpu(h_S, h_K, h_T, h_sigma, r, ref_call, ref_put, verifyN);

    int errors = 0;
    for (int i = 0; i < verifyN; ++i) {
        float tol = 1e-3f * (fabsf(ref_call[i]) + fabsf(ref_put[i])) + 1e-5f;
        if (fabsf(h_call[i] - ref_call[i]) > tol ||
            fabsf(h_put[i]  - ref_put[i])  > tol) {
            if (errors < 10) {
                fprintf(stderr,
                    "Mismatch at %d: GPU call=%.6f put=%.6f, CPU call=%.6f put=%.6f\n",
                    i, h_call[i], h_put[i], ref_call[i], ref_put[i]);
            }
            errors++;
        }
    }

    if (errors > 0)
        fprintf(stderr, "FAIL: %d errors out of %d verified\n", errors, verifyN);
    else
        fprintf(stderr, "PASS\n");

    free(ref_call);
    free(ref_put);

    CUDA_CHECK(cudaFree(d_S));
    CUDA_CHECK(cudaFree(d_K));
    CUDA_CHECK(cudaFree(d_T));
    CUDA_CHECK(cudaFree(d_sigma));
    CUDA_CHECK(cudaFree(d_call));
    CUDA_CHECK(cudaFree(d_put));
    free(h_S);
    free(h_K);
    free(h_T);
    free(h_sigma);
    free(h_call);
    free(h_put);

    return 0;
}
