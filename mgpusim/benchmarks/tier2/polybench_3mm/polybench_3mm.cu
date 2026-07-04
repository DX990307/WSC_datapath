// polybench_3mm.cu — PolyBench 3mm benchmark (CUDA, self-contained)
//
// Three chained matrix multiplications:
//   E = A * B  (NI×NK · NK×NJ → NI×NJ)
//   F = C * D  (NJ×NM · NM×NL → NJ×NL)
//   G = E * F  (NI×NJ · NJ×NL → NI×NL)
//
// Default matrix sizes: NI=NJ=NK=NL=NM=256 (all square 256×256)
// Reports GFLOPS = total_flops / time_s / 1e9
//   total_flops = 2*NI*NK*NJ + 2*NJ*NM*NL + 2*NI*NJ*NL
//
// Usage:
//   ./polybench_3mm [--size N]
//
//   --size N         Matrix dimension N (NxN for all matrices) (default: 256)
//
// Output (stdout): JSON-lines protocol
// Output (stderr): device info, timing details

#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <cmath>
#include <cfloat>
#include <vector>
#include <cuda_runtime.h>

// ---------------------------------------------------------------------------
// Error checking
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
// Timing helper
// ---------------------------------------------------------------------------

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
// GPU Kernels
// Each thread computes one output element (simple dot-product loop).
// Block: 16×16, Grid: ceil(cols/16) × ceil(rows/16)
// ---------------------------------------------------------------------------

// Kernel 1: E = A * B   (A: NI×NK, B: NK×NJ → E: NI×NJ)
__global__ void mm3_kernel1(const float* __restrict__ A,
                             const float* __restrict__ B,
                             float* __restrict__       E,
                             int NI, int NK, int NJ) {
    int j = blockIdx.x * blockDim.x + threadIdx.x;
    int i = blockIdx.y * blockDim.y + threadIdx.y;
    if (i < NI && j < NJ) {
        float sum = 0.0f;
        for (int k = 0; k < NK; k++)
            sum += A[i * NK + k] * B[k * NJ + j];
        E[i * NJ + j] = sum;
    }
}

// Kernel 2: F = C * D   (C: NJ×NM, D: NM×NL → F: NJ×NL)
__global__ void mm3_kernel2(const float* __restrict__ C,
                             const float* __restrict__ D,
                             float* __restrict__       F,
                             int NJ, int NM, int NL) {
    int l = blockIdx.x * blockDim.x + threadIdx.x;
    int j = blockIdx.y * blockDim.y + threadIdx.y;
    if (j < NJ && l < NL) {
        float sum = 0.0f;
        for (int m = 0; m < NM; m++)
            sum += C[j * NM + m] * D[m * NL + l];
        F[j * NL + l] = sum;
    }
}

// Kernel 3: G = E * F   (E: NI×NJ, F: NJ×NL → G: NI×NL)
__global__ void mm3_kernel3(const float* __restrict__ E,
                             const float* __restrict__ F,
                             float* __restrict__       G,
                             int NI, int NJ, int NL) {
    int l = blockIdx.x * blockDim.x + threadIdx.x;
    int i = blockIdx.y * blockDim.y + threadIdx.y;
    if (i < NI && l < NL) {
        float sum = 0.0f;
        for (int j = 0; j < NJ; j++)
            sum += E[i * NJ + j] * F[j * NL + l];
        G[i * NL + l] = sum;
    }
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------

int main(int argc, char** argv) {
    setvbuf(stdout, NULL, _IONBF, 0);

    int N     = parseIntParam(argc, argv, "--size", 256);
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

    // All matrices are N×N
    const int NI = N, NJ = N, NK = N, NL = N, NM = N;

    // Print device info
    int device_id = 0;
    CUDA_CHECK(cudaGetDevice(&device_id));
    cudaDeviceProp prop;
    CUDA_CHECK(cudaGetDeviceProperties(&prop, device_id));
    fprintf(stderr, "Device: %s (id=%d)\n", prop.name, device_id);
    fprintf(stderr, "Matrix size: %d×%d (all)\n\n",
            N, N);

    // Buffer sizes
    size_t sA = (size_t)NI * NK * sizeof(float); // A: NI×NK
    size_t sB = (size_t)NK * NJ * sizeof(float); // B: NK×NJ
    size_t sC = (size_t)NJ * NM * sizeof(float); // C: NJ×NM
    size_t sD = (size_t)NM * NL * sizeof(float); // D: NM×NL
    size_t sE = (size_t)NI * NJ * sizeof(float); // E: NI×NJ
    size_t sF = (size_t)NJ * NL * sizeof(float); // F: NJ×NL
    size_t sG = (size_t)NI * NL * sizeof(float); // G: NI×NL

    // Host allocations — inputs only (A, B, C, D)
    float* h_A = (float*)malloc(sA);
    float* h_B = (float*)malloc(sB);
    float* h_C = (float*)malloc(sC);
    float* h_D = (float*)malloc(sD);

    // Initialize with random floats
    srand(42);
    for (int i = 0; i < NI * NK; ++i) h_A[i] = (float)(rand() % 100) / 10.0f;
    for (int i = 0; i < NK * NJ; ++i) h_B[i] = (float)(rand() % 100) / 10.0f;
    for (int i = 0; i < NJ * NM; ++i) h_C[i] = (float)(rand() % 100) / 10.0f;
    for (int i = 0; i < NM * NL; ++i) h_D[i] = (float)(rand() % 100) / 10.0f;

    // Device allocations
    float *d_A, *d_B, *d_C, *d_D, *d_E, *d_F, *d_G;
    CUDA_CHECK(cudaMalloc(&d_A, sA));
    CUDA_CHECK(cudaMalloc(&d_B, sB));
    CUDA_CHECK(cudaMalloc(&d_C, sC));
    CUDA_CHECK(cudaMalloc(&d_D, sD));
    CUDA_CHECK(cudaMalloc(&d_E, sE));
    CUDA_CHECK(cudaMalloc(&d_F, sF));
    CUDA_CHECK(cudaMalloc(&d_G, sG));

    CUDA_CHECK(cudaMemcpy(d_A, h_A, sA, cudaMemcpyHostToDevice));
    CUDA_CHECK(cudaMemcpy(d_B, h_B, sB, cudaMemcpyHostToDevice));
    CUDA_CHECK(cudaMemcpy(d_C, h_C, sC, cudaMemcpyHostToDevice));
    CUDA_CHECK(cudaMemcpy(d_D, h_D, sD, cudaMemcpyHostToDevice));

    // Kernel launch configuration (16×16 blocks)
    dim3 block(16, 16);
    // Kernel 1: E = A*B  grid over (NJ, NI)
    dim3 grid1((NJ + 15) / 16, (NI + 15) / 16);
    // Kernel 2: F = C*D  grid over (NL, NJ)
    dim3 grid2((NL + 15) / 16, (NJ + 15) / 16);
    // Kernel 3: G = E*F  grid over (NL, NI)
    dim3 grid3((NL + 15) / 16, (NI + 15) / 16);

    BenchmarkTimer timer;

    // Warmup
    for (int w = 0; w < num_warmup; ++w) {
        mm3_kernel1<<<grid1, block, 0, 0>>>(d_A, d_B, d_E, NI, NK, NJ);
        mm3_kernel2<<<grid2, block, 0, 0>>>(d_C, d_D, d_F, NJ, NM, NL);
        mm3_kernel3<<<grid3, block, 0, 0>>>(d_E, d_F, d_G, NI, NJ, NL);
        CUDA_CHECK(cudaDeviceSynchronize());
    }

    // Timed iterations — time each kernel separately
    std::vector<double> k1_times(1), k2_times(1), k3_times(1);
    for (int iter = 0; iter < 1; ++iter) {
        timer.record_start();
        mm3_kernel1<<<grid1, block, 0, 0>>>(d_A, d_B, d_E, NI, NK, NJ);
        timer.record_stop();
        k1_times[iter] = static_cast<double>(timer.elapsed_ms());

        timer.record_start();
        mm3_kernel2<<<grid2, block, 0, 0>>>(d_C, d_D, d_F, NJ, NM, NL);
        timer.record_stop();
        k2_times[iter] = static_cast<double>(timer.elapsed_ms());

        timer.record_start();
        mm3_kernel3<<<grid3, block, 0, 0>>>(d_E, d_F, d_G, NI, NJ, NL);
        timer.record_stop();
        k3_times[iter] = static_cast<double>(timer.elapsed_ms());
    }

    // Compute statistics
    double k1_sum = 0.0, k2_sum = 0.0, k3_sum = 0.0;
    for (int i = 0; i < 1; ++i) {
        k1_sum += k1_times[i];
        k2_sum += k2_times[i];
        k3_sum += k3_times[i];
    }
    double k1_avg = k1_sum / 1;
    double k2_avg = k2_sum / 1;
    double k3_avg = k3_sum / 1;
    double total_avg_ms = k1_avg + k2_avg + k3_avg;

    // total_flops = 2*NI*NK*NJ + 2*NJ*NM*NL + 2*NI*NJ*NL
    double total_flops = 2.0 * ((double)NI * NK * NJ +
                                (double)NJ * NM * NL +
                                (double)NI * NJ * NL);
    double gflops = total_flops / (total_avg_ms * 1e-3) / 1e9;

    fprintf(stderr, "Performance: %.2f GFLOPS  (total avg %.4f ms: k1=%.4f k2=%.4f k3=%.4f)\n",
            gflops, total_avg_ms, k1_avg, k2_avg, k3_avg);

    // JSON-lines output
    printf("{\"type\":\"kernel\",\"name\":\"mm3_kernel1\",\"time_ms\":%.6f,"
           "\"params\":{\"size\":%d\"block_size\":%d,\"precision\":\"%s\"}}\n",
           k1_avg, N, block_size, precision);
    printf("{\"type\":\"kernel\",\"name\":\"mm3_kernel2\",\"time_ms\":%.6f,"
           "\"params\":{\"size\":%d\"block_size\":%d,\"precision\":\"%s\"}}\n",
           k2_avg, N, block_size, precision);
    printf("{\"type\":\"kernel\",\"name\":\"mm3_kernel3\",\"time_ms\":%.6f,"
           "\"params\":{\"size\":%d\"block_size\":%d,\"precision\":\"%s\"}}\n",
           k3_avg, N, block_size, precision);
    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"gflops\",\"value\":%.2f}]}\n",
           total_avg_ms, gflops);

    // Cleanup
    CUDA_CHECK(cudaFree(d_A));
    CUDA_CHECK(cudaFree(d_B));
    CUDA_CHECK(cudaFree(d_C));
    CUDA_CHECK(cudaFree(d_D));
    CUDA_CHECK(cudaFree(d_E));
    CUDA_CHECK(cudaFree(d_F));
    CUDA_CHECK(cudaFree(d_G));
    free(h_A);
    free(h_B);
    free(h_C);
    free(h_D);

    return 0;
}
