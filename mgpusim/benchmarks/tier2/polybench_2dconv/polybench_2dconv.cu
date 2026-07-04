// polybench_2dconv.cu — PolyBench 2D Convolution benchmark (CUDA, self-contained)
//
// Applies a fixed 3×3 kernel to an NI×NJ matrix A, producing output B.
// PolyBench standard kernel (c coefficients are fixed):
//
//   c = {{0.8, 0.2, 0.3},
//        {0.2, 0.7, 0.4},
//        {0.1, 0.2, 0.5}}
//
//   B[i][j] = sum_{k1=0}^{2} sum_{k2=0}^{2} c[k1][k2] * A[i+k1-1][j+k2-1]
//             for 1 <= i < NI-1, 1 <= j < NJ-1
//
// Each GPU thread computes one output element (one 3×3 dot product).
//
// Native CUDA implementation.
//
// Usage:
//   ./polybench_2dconv [--size N]
//
//   --size N        Matrix dimension N×N   (default: 2048)
//
// Output (stdout): JSON-lines protocol
// Output (stderr): GFLOPS

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
// PolyBench 2D Convolution kernel
// ---------------------------------------------------------------------------

__global__ void convolution2D_kernel(const float* __restrict__ A,
                                     float*       __restrict__ B,
                                     int NI, int NJ) {
    int j = (int)(blockIdx.x * blockDim.x + threadIdx.x);
    int i = (int)(blockIdx.y * blockDim.y + threadIdx.y);

    if (i < 1 || i >= NI - 1 || j < 1 || j >= NJ - 1)
        return;

    // PolyBench fixed coefficients
    const float c00 = 0.8f, c01 = 0.2f, c02 = 0.3f;
    const float c10 = 0.2f, c11 = 0.7f, c12 = 0.4f;
    const float c20 = 0.1f, c21 = 0.2f, c22 = 0.5f;

    B[i * NJ + j] =
        c00 * A[(i - 1) * NJ + (j - 1)] +
        c01 * A[(i - 1) * NJ +  j     ] +
        c02 * A[(i - 1) * NJ + (j + 1)] +
        c10 * A[ i      * NJ + (j - 1)] +
        c11 * A[ i      * NJ +  j     ] +
        c12 * A[ i      * NJ + (j + 1)] +
        c20 * A[(i + 1) * NJ + (j - 1)] +
        c21 * A[(i + 1) * NJ +  j     ] +
        c22 * A[(i + 1) * NJ + (j + 1)];
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------
int main(int argc, char** argv) {
    setvbuf(stdout, NULL, _IONBF, 0);

    int N     = parseIntParam(argc, argv, "--size", 2048);
    int block_size = 256;
    const char* precision = "float";

    const char* env_val;
    env_val = getenv("BENCH_PARAM_size");
    if (env_val) N = atoi(env_val);
    env_val = getenv("BENCH_PARAM_block_size");
    if (env_val) block_size = atoi(env_val);
    env_val = getenv("BENCH_PARAM_precision");
    if (env_val) precision = env_val;
    int num_warmup = 0;

    int NI = N, NJ = N;
    size_t matSize = (size_t)NI * NJ * sizeof(float);

    // Print device info
    int device_id = 0;
    CUDA_CHECK(cudaGetDevice(&device_id));
    cudaDeviceProp prop;
    CUDA_CHECK(cudaGetDeviceProperties(&prop, device_id));
    fprintf(stderr, "Device: %s (id=%d)\n", prop.name, device_id);
    fprintf(stderr, "Matrix: %d×%d\n\n", NI, NJ);

    // Host allocations
    float* h_A = (float*)malloc(matSize);
    if (!h_A) { fprintf(stderr, "malloc failed\n"); return 1; }

    srand(42);
    for (int i = 0; i < NI * NJ; i++)
        h_A[i] = (float)(rand() % 100) / 10.0f;

    // Device allocations
    float *d_A, *d_B;
    CUDA_CHECK(cudaMalloc(&d_A, matSize));
    CUDA_CHECK(cudaMalloc(&d_B, matSize));

    CUDA_CHECK(cudaMemcpy(d_A, h_A, matSize, cudaMemcpyHostToDevice));
    CUDA_CHECK(cudaMemset(d_B, 0, matSize));

    dim3 block(16, 16);
    dim3 grid((NJ + block.x - 1) / block.x, (NI + block.y - 1) / block.y);

    BenchmarkTimer timer;

    // Warmup
    for (int w = 0; w < num_warmup; ++w) {
        convolution2D_kernel<<<grid, block, 0, 0>>>(d_A, d_B, NI, NJ);
        CUDA_CHECK(cudaDeviceSynchronize());
    }

    // Timed iterations
    std::vector<double> times(1);
    for (int i = 0; i < 1; ++i) {
        timer.record_start();
        convolution2D_kernel<<<grid, block, 0, 0>>>(d_A, d_B, NI, NJ);
        timer.record_stop();
        times[i] = static_cast<double>(timer.elapsed_ms());
    }

    double sum = 0.0, mn = DBL_MAX, mx = 0.0;
    for (int i = 0; i < 1; ++i) {
        sum += times[i];
        if (times[i] < mn) mn = times[i];
        if (times[i] > mx) mx = times[i];
    }
    double avg = sum / 1;

    double variance = 0.0;
    for (int i = 0; i < 1; ++i) {
        double d = times[i] - avg;
        variance += d * d;
    }
    double stddev = (1 > 1) ? sqrt(variance / (1 - 1)) : 0.0;

    // GFLOPS: each interior output element = 9 MACs = 18 FLOPs
    long long interior = (long long)(NI - 2) * (NJ - 2);
    double gflops = 2.0 * 9.0 * (double)interior / (avg * 1e-3) / 1e9;

    fprintf(stderr, "GFLOPS: %.2f  (avg %.4f ms)\n", gflops, avg);

    // JSON-lines output
    printf("{\"type\":\"kernel\",\"name\":\"convolution2D_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"size\":%d\"block_size\":%d,\"precision\":\"%s\"}}\n",
           avg, N, block_size, precision);
    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"gflops\",\"value\":%.2f}]}\n",
           avg, gflops);

    CUDA_CHECK(cudaFree(d_A));
    CUDA_CHECK(cudaFree(d_B));
    free(h_A);

    return 0;
}
