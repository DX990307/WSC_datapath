/**
 * rodinia_backprop.hip — HIP/CUDA Backpropagation benchmark (Rodinia suite).
 *
 * Implements a two-layer fully-connected neural network forward + backward pass.
 * Architecture: INPUT_N → HIDDEN_N → OUTPUT_N
 *
 * Default sizes:
 *   INPUT_N  = 65536
 *   HIDDEN_N = 1024
 *   OUTPUT_N = 1
 *
 * Kernels:
 *   1. forward_hidden  — hidden[j] = sigmoid(sum_i input[i]*w1[i*HIDDEN_N+j] + b1[j])
 *   2. forward_output  — output[k] = sigmoid(sum_j hidden[j]*w2[j*OUTPUT_N+k] + b2[k])
 *   3. backward_output_delta — delta_out[k] = output[k]*(1-output[k])*(target[k]-output[k])
 *   4. backward_hidden_delta — delta_hid[j] = hidden[j]*(1-hidden[j])*sum_k(w2[j*OUTPUT_N+k]*delta_out[k])
 *   5. update_w1       — w1[i*HIDDEN_N+j] += lr * input[i] * delta_hid[j]
 *   6. update_w2       — w2[j*OUTPUT_N+k] += lr * hidden[j] * delta_out[k]
 *
 * Usage:
 *   ./rodinia_backprop [--input N] [--hidden H] [--output O]
 *
 * Output (stdout): CSV — backprop,<input_n>,<hidden_n>,<time_ms>,<GFLOPS>
 * Output (stderr): device info, timing details
 *
 * Build:
 *   make PLATFORM=cuda   # NVIDIA
 *   make PLATFORM=rocm   # AMD
 */

#ifdef __HIP__
  #include <hip/hip_runtime.h>
  #define GPU_API hip
  #define gpuMalloc       hipMalloc
  #define gpuFree         hipFree
  #define gpuMemcpy       hipMemcpy
  #define gpuMemcpyHtoD   hipMemcpyHostToDevice
  #define gpuMemcpyDtoH   hipMemcpyDeviceToHost
  #define gpuDeviceSynchronize hipDeviceSynchronize
  #define gpuEventCreate  hipEventCreate
  #define gpuEventRecord  hipEventRecord
  #define gpuEventSynchronize hipEventSynchronize
  #define gpuEventElapsedTime hipEventElapsedTime
  #define gpuEventDestroy hipEventDestroy
  typedef hipEvent_t gpuEvent_t;
  #define gpuGetDeviceProperties hipGetDeviceProperties
  typedef hipDeviceProp_t gpuDeviceProp_t;
#else
  #include <cuda_runtime.h>
  #define gpuMalloc       cudaMalloc
  #define gpuFree         cudaFree
  #define gpuMemcpy       cudaMemcpy
  #define gpuMemcpyHtoD   cudaMemcpyHostToDevice
  #define gpuMemcpyDtoH   cudaMemcpyDeviceToHost
  #define gpuDeviceSynchronize cudaDeviceSynchronize
  #define gpuEventCreate  cudaEventCreate
  #define gpuEventRecord  cudaEventRecord
  #define gpuEventSynchronize cudaEventSynchronize
  #define gpuEventElapsedTime cudaEventElapsedTime
  #define gpuEventDestroy cudaEventDestroy
  typedef cudaEvent_t gpuEvent_t;
  #define gpuGetDeviceProperties cudaGetDeviceProperties
  typedef cudaDeviceProp gpuDeviceProp_t;
#endif

#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <cmath>
#include <vector>

// ---------------------------------------------------------------------------
// Default network dimensions
// ---------------------------------------------------------------------------
#define DEFAULT_INPUT_N  65536
#define DEFAULT_HIDDEN_N 1024
#define DEFAULT_OUTPUT_N 1
#define DEFAULT_ITERS    5
#define LR               0.1f
#define BLOCK_SZ         256

// ---------------------------------------------------------------------------
// Device helpers
// ---------------------------------------------------------------------------
__device__ __forceinline__ float sigmoid_d(float x)
{
    return 1.0f / (1.0f + expf(-x));
}

// ---------------------------------------------------------------------------
// Kernels
// ---------------------------------------------------------------------------

/**
 * forward_hidden: compute activations for the hidden layer.
 * One thread per hidden unit j.
 */
__global__ void forward_hidden(const float* __restrict__ input,
                                const float* __restrict__ w1,
                                const float* __restrict__ b1,
                                float* __restrict__ hidden,
                                int input_n, int hidden_n)
{
    int j = blockIdx.x * blockDim.x + threadIdx.x;
    if (j >= hidden_n) return;
    float sum = b1[j];
    for (int i = 0; i < input_n; i++)
        sum += input[i] * w1[i * hidden_n + j];
    hidden[j] = sigmoid_d(sum);
}

/**
 * forward_output: compute activations for the output layer.
 * One thread per output unit k.
 */
__global__ void forward_output(const float* __restrict__ hidden,
                                const float* __restrict__ w2,
                                const float* __restrict__ b2,
                                float* __restrict__ output,
                                int hidden_n, int output_n)
{
    int k = blockIdx.x * blockDim.x + threadIdx.x;
    if (k >= output_n) return;
    float sum = b2[k];
    for (int j = 0; j < hidden_n; j++)
        sum += hidden[j] * w2[j * output_n + k];
    output[k] = sigmoid_d(sum);
}

/**
 * backward_output_delta: compute error signal at output layer.
 * delta_out[k] = output[k] * (1 - output[k]) * (target[k] - output[k])
 */
__global__ void backward_output_delta(const float* __restrict__ output,
                                       const float* __restrict__ target,
                                       float* __restrict__ delta_out,
                                       int output_n)
{
    int k = blockIdx.x * blockDim.x + threadIdx.x;
    if (k >= output_n) return;
    float o = output[k];
    delta_out[k] = o * (1.0f - o) * (target[k] - o);
}

/**
 * backward_hidden_delta: backpropagate error to hidden layer.
 * delta_hid[j] = hidden[j] * (1 - hidden[j]) * sum_k(w2[j*output_n+k] * delta_out[k])
 */
__global__ void backward_hidden_delta(const float* __restrict__ hidden,
                                       const float* __restrict__ w2,
                                       const float* __restrict__ delta_out,
                                       float* __restrict__ delta_hid,
                                       int hidden_n, int output_n)
{
    int j = blockIdx.x * blockDim.x + threadIdx.x;
    if (j >= hidden_n) return;
    float h = hidden[j];
    float sum = 0.0f;
    for (int k = 0; k < output_n; k++)
        sum += w2[j * output_n + k] * delta_out[k];
    delta_hid[j] = h * (1.0f - h) * sum;
}

/**
 * update_w1: update weights from input to hidden layer.
 * w1[i*hidden_n+j] += lr * input[i] * delta_hid[j]
 * 2D grid: x→i (input), y→j (hidden)
 */
__global__ void update_w1(float* __restrict__ w1,
                            const float* __restrict__ input,
                            const float* __restrict__ delta_hid,
                            int input_n, int hidden_n, float lr)
{
    int i = blockIdx.x * blockDim.x + threadIdx.x;
    int j = blockIdx.y * blockDim.y + threadIdx.y;
    if (i >= input_n || j >= hidden_n) return;
    w1[i * hidden_n + j] += lr * input[i] * delta_hid[j];
}

/**
 * update_w2: update weights from hidden to output layer.
 * w2[j*output_n+k] += lr * hidden[j] * delta_out[k]
 */
__global__ void update_w2(float* __restrict__ w2,
                            const float* __restrict__ hidden,
                            const float* __restrict__ delta_out,
                            int hidden_n, int output_n, float lr)
{
    int j = blockIdx.x * blockDim.x + threadIdx.x;
    if (j >= hidden_n) return;
    for (int k = 0; k < output_n; k++)
        w2[j * output_n + k] += lr * hidden[j] * delta_out[k];
}

// ---------------------------------------------------------------------------
// Argument parsing
// ---------------------------------------------------------------------------
static int parseIntParam(int argc, char** argv, const char* name, int def)
{
    for (int i = 1; i < argc - 1; i++) {
        if (strcmp(argv[i], name) == 0) {
            int v = atoi(argv[i + 1]);
            if (v > 0) return v;
        }
    }
    return def;
}

// ---------------------------------------------------------------------------
// main
// ---------------------------------------------------------------------------
int main(int argc, char* argv[])
{
    setvbuf(stdout, NULL, _IONBF, 0);

    // Defaults
    int input_n  = DEFAULT_INPUT_N;
    int hidden_n = DEFAULT_HIDDEN_N;
    int output_n = DEFAULT_OUTPUT_N;

    // Command-line fallback
    input_n  = parseIntParam(argc, argv, "--input", input_n);
    hidden_n = parseIntParam(argc, argv, "--hidden", hidden_n);
    output_n = parseIntParam(argc, argv, "--output", output_n);

    // Environment variable overrides
    const char* env_val;
    env_val = getenv("BENCH_PARAM_input");
    if (env_val) input_n = atoi(env_val);
    env_val = getenv("BENCH_PARAM_hidden");
    if (env_val) hidden_n = atoi(env_val);
    env_val = getenv("BENCH_PARAM_output");
    if (env_val) output_n = atoi(env_val);
    int num_warmup = 5;

    // Print device info
    gpuDeviceProp_t prop;
    gpuGetDeviceProperties(&prop, 0);
    fprintf(stderr, "Device: %s\n", prop.name);
    fprintf(stderr, "Network: %d → %d → %d\n\n",
            input_n, hidden_n, output_n);

    // -----------------------------------------------------------------------
    // Host allocation and initialization
    // -----------------------------------------------------------------------
    size_t sz_w1  = (size_t)input_n  * hidden_n * sizeof(float);
    size_t sz_w2  = (size_t)hidden_n * output_n  * sizeof(float);
    size_t sz_in  = (size_t)input_n              * sizeof(float);
    size_t sz_hid = (size_t)hidden_n             * sizeof(float);
    size_t sz_out = (size_t)output_n             * sizeof(float);

    std::vector<float> h_input(input_n),  h_w1(input_n * hidden_n),
                       h_w2(hidden_n * output_n),
                       h_b1(hidden_n, 0.0f), h_b2(output_n, 0.0f),
                       h_target(output_n, 1.0f);

    srand(42);
    for (int i = 0; i < input_n; i++)
        h_input[i] = (float)(i % 256) / 256.0f;
    for (size_t i = 0; i < h_w1.size(); i++)
        h_w1[i] = ((float)rand() / RAND_MAX - 0.5f) * 0.2f;   // [-0.1, 0.1]
    for (size_t i = 0; i < h_w2.size(); i++)
        h_w2[i] = ((float)rand() / RAND_MAX - 0.5f) * 0.2f;

    // -----------------------------------------------------------------------
    // Device allocation
    // -----------------------------------------------------------------------
    float *d_input, *d_w1, *d_b1, *d_hidden;
    float *d_w2,    *d_b2, *d_output;
    float *d_target, *d_delta_out, *d_delta_hid;

    gpuMalloc((void**)&d_input,     sz_in);
    gpuMalloc((void**)&d_w1,        sz_w1);
    gpuMalloc((void**)&d_b1,        sz_hid);
    gpuMalloc((void**)&d_hidden,    sz_hid);
    gpuMalloc((void**)&d_w2,        sz_w2);
    gpuMalloc((void**)&d_b2,        sz_out);
    gpuMalloc((void**)&d_output,    sz_out);
    gpuMalloc((void**)&d_target,    sz_out);
    gpuMalloc((void**)&d_delta_out, sz_out);
    gpuMalloc((void**)&d_delta_hid, sz_hid);

    gpuMemcpy(d_input,  h_input.data(),  sz_in,  gpuMemcpyHtoD);
    gpuMemcpy(d_w1,     h_w1.data(),     sz_w1,  gpuMemcpyHtoD);
    gpuMemcpy(d_b1,     h_b1.data(),     sz_hid, gpuMemcpyHtoD);
    gpuMemcpy(d_w2,     h_w2.data(),     sz_w2,  gpuMemcpyHtoD);
    gpuMemcpy(d_b2,     h_b2.data(),     sz_out, gpuMemcpyHtoD);
    gpuMemcpy(d_target, h_target.data(), sz_out, gpuMemcpyHtoD);

    // -----------------------------------------------------------------------
    // Kernel launch configs
    // -----------------------------------------------------------------------
    // 1D kernels
    int grid_hid = (hidden_n + BLOCK_SZ - 1) / BLOCK_SZ;
    int grid_out = (output_n + BLOCK_SZ - 1) / BLOCK_SZ;

    // 2D kernel for update_w1
    dim3 block2d(16, 16);
    dim3 grid2d((input_n  + 15) / 16,
                (hidden_n + 15) / 16);

    // Per-kernel timing events and accumulators
    gpuEvent_t ev_start, ev_stop;
    gpuEventCreate(&ev_start);
    gpuEventCreate(&ev_stop);

    double t_forward_hidden        = 0.0;
    double t_forward_output        = 0.0;
    double t_backward_output_delta = 0.0;
    double t_backward_hidden_delta = 0.0;
    double t_update_w1             = 0.0;
    double t_update_w2             = 0.0;

    // Lambda: one forward + backward pass.
    // When timed==true, each kernel launch is wrapped in its own event pair
    // and its elapsed time is added to that kernel's accumulator.
    auto run_epoch = [&](bool timed) {
        float ms = 0.0f;

        // --- Forward ---
        if (timed) gpuEventRecord(ev_start, 0);
        forward_hidden<<<grid_hid, BLOCK_SZ>>>(
            d_input, d_w1, d_b1, d_hidden, input_n, hidden_n);
        if (timed) {
            gpuEventRecord(ev_stop, 0);
            gpuEventSynchronize(ev_stop);
            gpuEventElapsedTime(&ms, ev_start, ev_stop);
            t_forward_hidden += ms;
        }

        if (timed) gpuEventRecord(ev_start, 0);
        forward_output<<<grid_out, BLOCK_SZ>>>(
            d_hidden, d_w2, d_b2, d_output, hidden_n, output_n);
        if (timed) {
            gpuEventRecord(ev_stop, 0);
            gpuEventSynchronize(ev_stop);
            gpuEventElapsedTime(&ms, ev_start, ev_stop);
            t_forward_output += ms;
        }

        // --- Backward ---
        if (timed) gpuEventRecord(ev_start, 0);
        backward_output_delta<<<grid_out, BLOCK_SZ>>>(
            d_output, d_target, d_delta_out, output_n);
        if (timed) {
            gpuEventRecord(ev_stop, 0);
            gpuEventSynchronize(ev_stop);
            gpuEventElapsedTime(&ms, ev_start, ev_stop);
            t_backward_output_delta += ms;
        }

        if (timed) gpuEventRecord(ev_start, 0);
        backward_hidden_delta<<<grid_hid, BLOCK_SZ>>>(
            d_hidden, d_w2, d_delta_out, d_delta_hid, hidden_n, output_n);
        if (timed) {
            gpuEventRecord(ev_stop, 0);
            gpuEventSynchronize(ev_stop);
            gpuEventElapsedTime(&ms, ev_start, ev_stop);
            t_backward_hidden_delta += ms;
        }

        if (timed) gpuEventRecord(ev_start, 0);
        update_w1<<<grid2d, block2d>>>(
            d_w1, d_input, d_delta_hid, input_n, hidden_n, LR);
        if (timed) {
            gpuEventRecord(ev_stop, 0);
            gpuEventSynchronize(ev_stop);
            gpuEventElapsedTime(&ms, ev_start, ev_stop);
            t_update_w1 += ms;
        }

        if (timed) gpuEventRecord(ev_start, 0);
        update_w2<<<grid_hid, BLOCK_SZ>>>(
            d_w2, d_hidden, d_delta_out, hidden_n, output_n, LR);
        if (timed) {
            gpuEventRecord(ev_stop, 0);
            gpuEventSynchronize(ev_stop);
            gpuEventElapsedTime(&ms, ev_start, ev_stop);
            t_update_w2 += ms;
        }
    };

    // -----------------------------------------------------------------------
    // Warmup (full kernel sequence, untimed)
    // -----------------------------------------------------------------------
    for (int w = 0; w < num_warmup; ++w) {
        run_epoch(false);
        gpuDeviceSynchronize();
    }

    // -----------------------------------------------------------------------
    // Timed iterations — each kernel timed separately into its accumulator
    // -----------------------------------------------------------------------
    for (int it = 0; it < 1; it++) {
        run_epoch(true);
    }

    gpuEventDestroy(ev_start);
    gpuEventDestroy(ev_stop);

    // -----------------------------------------------------------------------
    // Statistics
    // -----------------------------------------------------------------------
    double sum_ms = t_forward_hidden + t_forward_output +
                    t_backward_output_delta + t_backward_hidden_delta +
                    t_update_w1 + t_update_w2;
    double avg_ms = sum_ms;

    // GFLOPS: approximate (forward ≈ 2*INPUT*HIDDEN + 2*HIDDEN*OUTPUT,
    //                       backward ≈ same)
    double flops = 4.0 * (double)input_n * hidden_n +
                   4.0 * (double)hidden_n * output_n;
    double gflops = flops / (avg_ms * 1e-3) / 1e9;

    fprintf(stderr, "GFLOPS: %.2f  (total %.4f ms)\n",
            gflops, avg_ms);

    // JSON-lines kernel events (one per kernel in params.json)
    printf("{\"type\":\"kernel\",\"name\":\"forward_hidden\",\"time_ms\":%.6f,"
           "\"params\":{\"input\":%d,\"hidden\":%d,\"output\":%d}}\n",
           t_forward_hidden, input_n, hidden_n, output_n);
    printf("{\"type\":\"kernel\",\"name\":\"forward_output\",\"time_ms\":%.6f,"
           "\"params\":{\"input\":%d,\"hidden\":%d,\"output\":%d}}\n",
           t_forward_output, input_n, hidden_n, output_n);
    printf("{\"type\":\"kernel\",\"name\":\"backward_output_delta\",\"time_ms\":%.6f,"
           "\"params\":{\"input\":%d,\"hidden\":%d,\"output\":%d}}\n",
           t_backward_output_delta, input_n, hidden_n, output_n);
    printf("{\"type\":\"kernel\",\"name\":\"backward_hidden_delta\",\"time_ms\":%.6f,"
           "\"params\":{\"input\":%d,\"hidden\":%d,\"output\":%d}}\n",
           t_backward_hidden_delta, input_n, hidden_n, output_n);
    printf("{\"type\":\"kernel\",\"name\":\"update_w1\",\"time_ms\":%.6f,"
           "\"params\":{\"input\":%d,\"hidden\":%d,\"output\":%d}}\n",
           t_update_w1, input_n, hidden_n, output_n);
    printf("{\"type\":\"kernel\",\"name\":\"update_w2\",\"time_ms\":%.6f,"
           "\"params\":{\"input\":%d,\"hidden\":%d,\"output\":%d}}\n",
           t_update_w2, input_n, hidden_n, output_n);

    // JSON-lines summary event
    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"gflops\",\"value\":%.2f}]}\n",
           sum_ms, gflops);

    // -----------------------------------------------------------------------
    // Cleanup
    // -----------------------------------------------------------------------
    gpuFree(d_input);
    gpuFree(d_w1);
    gpuFree(d_b1);
    gpuFree(d_hidden);
    gpuFree(d_w2);
    gpuFree(d_b2);
    gpuFree(d_output);
    gpuFree(d_target);
    gpuFree(d_delta_out);
    gpuFree(d_delta_hid);

    return 0;
}
