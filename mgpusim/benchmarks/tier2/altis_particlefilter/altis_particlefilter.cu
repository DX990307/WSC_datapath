// altis_particlefilter.cu — Altis Particle Filter benchmark
//
// Sequential Importance Resampling (SIR) particle filter for Monte Carlo
// localization on a 2D grid. Tracks an object over multiple time steps.
//
// Kernel 1: update particle positions (random walk)
// Kernel 2: compute weights (Gaussian likelihood)
// Kernel 3: resample (prefix sum + scatter)
//
// Native CUDA implementation.
//
// Usage:
//   ./altis_particlefilter [--size N]
//
//   --size N         Number of particles (default: 100000)
//
// Output (stdout): CSV row — altis_particlefilter,<N>,<time_ms>,<particles_per_sec>
// Output (stderr): human-readable results

#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <cmath>
#include <vector>
#include <cuda_runtime.h>

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
// Constants
// ---------------------------------------------------------------------------

#define GRID_SIZE    128
#define TIME_STEPS   10
#define SIGMA_OBS    10.0f    // Observation noise std dev
#define SIGMA_MOVE   2.0f     // Movement noise std dev

// ---------------------------------------------------------------------------
// Simple GPU-side PRNG (xorshift32)
// ---------------------------------------------------------------------------

__device__ static unsigned int gpu_xorshift(unsigned int s) {
    s ^= s << 13;
    s ^= s >> 17;
    s ^= s << 5;
    return s;
}

__device__ static float gpu_rand_uniform(unsigned int* state) {
    *state = gpu_xorshift(*state);
    return (float)(*state & 0xFFFFFF) / 16777216.0f;
}

__device__ static float gpu_rand_normal(unsigned int* state) {
    // Box-Muller transform
    float u1 = gpu_rand_uniform(state);
    float u2 = gpu_rand_uniform(state);
    u1 = fmaxf(u1, 1e-10f);
    return sqrtf(-2.0f * logf(u1)) * cosf(2.0f * 3.14159265f * u2);
}

// ---------------------------------------------------------------------------
// Kernel 1: Update particle positions (random walk)
// ---------------------------------------------------------------------------

__global__ void update_particles_kernel(
    float* __restrict__ x_pos,
    float* __restrict__ y_pos,
    unsigned int* __restrict__ rng_states,
    int N)
{
    int idx = blockIdx.x * blockDim.x + threadIdx.x;
    if (idx >= N) return;

    unsigned int rng = rng_states[idx];

    // Random walk
    float dx = SIGMA_MOVE * gpu_rand_normal(&rng);
    float dy = SIGMA_MOVE * gpu_rand_normal(&rng);

    float nx = x_pos[idx] + dx;
    float ny = y_pos[idx] + dy;

    // Clamp to grid boundaries
    nx = fminf(fmaxf(nx, 0.0f), (float)(GRID_SIZE - 1));
    ny = fminf(fmaxf(ny, 0.0f), (float)(GRID_SIZE - 1));

    x_pos[idx] = nx;
    y_pos[idx] = ny;
    rng_states[idx] = rng;
}

// ---------------------------------------------------------------------------
// Kernel 2: Compute weights (Gaussian likelihood)
// ---------------------------------------------------------------------------

__global__ void compute_weights_kernel(
    const float* __restrict__ x_pos,
    const float* __restrict__ y_pos,
    float* __restrict__ weights,
    float obs_x, float obs_y,
    int N)
{
    int idx = blockIdx.x * blockDim.x + threadIdx.x;
    if (idx >= N) return;

    float dx = x_pos[idx] - obs_x;
    float dy = y_pos[idx] - obs_y;
    float dist2 = dx * dx + dy * dy;

    // Gaussian likelihood: exp(-dist^2 / (2 * sigma^2))
    float w = expf(-dist2 / (2.0f * SIGMA_OBS * SIGMA_OBS));
    weights[idx] = w;
}

// ---------------------------------------------------------------------------
// Kernel 3a: Parallel prefix sum (Blelloch scan) for weight normalization
// Simple work-efficient scan within blocks.
// ---------------------------------------------------------------------------

__global__ void prefix_sum_kernel(
    float* __restrict__ data,
    float* __restrict__ block_sums,
    int N)
{
    extern __shared__ float sdata[];

    int tid = threadIdx.x;
    int gid = blockIdx.x * blockDim.x * 2 + threadIdx.x;

    // Load data into shared memory
    sdata[tid]              = (gid < N)              ? data[gid]              : 0.0f;
    sdata[tid + blockDim.x] = (gid + blockDim.x < N) ? data[gid + blockDim.x] : 0.0f;

    int n = blockDim.x * 2;

    // Up-sweep (reduce)
    for (int stride = 1; stride < n; stride *= 2) {
        int index = (tid + 1) * stride * 2 - 1;
        if (index < n)
            sdata[index] += sdata[index - stride];
        __syncthreads();
    }

    // Save block total and clear last element
    if (tid == 0) {
        if (block_sums) block_sums[blockIdx.x] = sdata[n - 1];
        sdata[n - 1] = 0.0f;
    }
    __syncthreads();

    // Down-sweep
    for (int stride = n / 2; stride >= 1; stride /= 2) {
        int index = (tid + 1) * stride * 2 - 1;
        if (index < n) {
            float t = sdata[index - stride];
            sdata[index - stride] = sdata[index];
            sdata[index] += t;
        }
        __syncthreads();
    }

    // Write results (exclusive prefix sum)
    if (gid < N)              data[gid]              = sdata[tid];
    if (gid + blockDim.x < N) data[gid + blockDim.x] = sdata[tid + blockDim.x];
}

__global__ void add_block_sums_kernel(
    float* __restrict__ data,
    const float* __restrict__ block_sums,
    int N)
{
    int gid = blockIdx.x * blockDim.x * 2 + threadIdx.x;
    float val = block_sums[blockIdx.x];

    if (gid < N)              data[gid]              += val;
    if (gid + blockDim.x < N) data[gid + blockDim.x] += val;
}

// ---------------------------------------------------------------------------
// Kernel 3b: Systematic resampling
// ---------------------------------------------------------------------------

__global__ void resample_kernel(
    const float* __restrict__ cdf,         // prefix sum of normalized weights
    const float* __restrict__ x_pos_in,
    const float* __restrict__ y_pos_in,
    float* __restrict__ x_pos_out,
    float* __restrict__ y_pos_out,
    unsigned int* __restrict__ rng_states,
    float total_weight,
    int N)
{
    int idx = blockIdx.x * blockDim.x + threadIdx.x;
    if (idx >= N) return;

    // Systematic resampling: evenly spaced points with random offset
    unsigned int rng = rng_states[idx];
    float u0 = gpu_rand_uniform(&rng);
    rng_states[idx] = rng;

    float target = (((float)idx + u0) / (float)N) * total_weight;

    // Binary search in CDF
    int lo = 0, hi = N - 1;
    while (lo < hi) {
        int mid = (lo + hi) / 2;
        if (cdf[mid] < target)
            lo = mid + 1;
        else
            hi = mid;
    }

    // Clamp to valid range
    lo = min(lo, N - 1);
    lo = max(lo, 0);

    x_pos_out[idx] = x_pos_in[lo];
    y_pos_out[idx] = y_pos_in[lo];
}

// ---------------------------------------------------------------------------
// Host prefix sum (for small arrays / block sums)
// ---------------------------------------------------------------------------

static void host_prefix_sum(float* data, int n) {
    float acc = 0.0f;
    for (int i = 0; i < n; ++i) {
        float tmp = data[i];
        data[i] = acc;
        acc += tmp;
    }
}

// ---------------------------------------------------------------------------
// CPU reference particle filter (for verification)
// ---------------------------------------------------------------------------

static inline unsigned int cpu_xorshift(unsigned int s) {
    s ^= s << 13;
    s ^= s >> 17;
    s ^= s << 5;
    return s;
}

static inline float cpu_rand_uniform(unsigned int* state) {
    *state = cpu_xorshift(*state);
    return (float)(*state & 0xFFFFFF) / 16777216.0f;
}

static inline float cpu_rand_normal(unsigned int* state) {
    float u1 = cpu_rand_uniform(state);
    float u2 = cpu_rand_uniform(state);
    u1 = fmaxf(u1, 1e-10f);
    return sqrtf(-2.0f * logf(u1)) * cosf(2.0f * 3.14159265f * u2);
}

static void cpu_particle_filter_step(
    float* x_pos, float* y_pos, unsigned int* rng,
    float obs_x, float obs_y, float* weights, float* cdf,
    float* x_tmp, float* y_tmp, int N)
{
    // Update positions
    for (int i = 0; i < N; ++i) {
        float dx = SIGMA_MOVE * cpu_rand_normal(&rng[i]);
        float dy = SIGMA_MOVE * cpu_rand_normal(&rng[i]);
        x_pos[i] = fminf(fmaxf(x_pos[i] + dx, 0.0f), (float)(GRID_SIZE - 1));
        y_pos[i] = fminf(fmaxf(y_pos[i] + dy, 0.0f), (float)(GRID_SIZE - 1));
    }

    // Compute weights
    for (int i = 0; i < N; ++i) {
        float dx = x_pos[i] - obs_x;
        float dy = y_pos[i] - obs_y;
        float dist2 = dx * dx + dy * dy;
        weights[i] = expf(-dist2 / (2.0f * SIGMA_OBS * SIGMA_OBS));
    }

    // Exclusive prefix sum for CDF
    float total = 0.0f;
    for (int i = 0; i < N; ++i) {
        cdf[i] = total;
        total += weights[i];
    }

    // Resample
    for (int i = 0; i < N; ++i) {
        float u0 = cpu_rand_uniform(&rng[i]);
        float target = ((float)i + u0) / (float)N * total;

        int lo = 0, hi = N - 1;
        while (lo < hi) {
            int mid = (lo + hi) / 2;
            if (cdf[mid] < target) lo = mid + 1;
            else hi = mid;
        }
        if (lo >= N) lo = N - 1;
        if (lo < 0) lo = 0;

        x_tmp[i] = x_pos[lo];
        y_tmp[i] = y_pos[lo];
    }

    memcpy(x_pos, x_tmp, N * sizeof(float));
    memcpy(y_pos, y_tmp, N * sizeof(float));
}

// ---------------------------------------------------------------------------
// Simple PRNG for initialization
// ---------------------------------------------------------------------------

static inline unsigned int lcg_rand(unsigned int* state) {
    *state = *state * 1664525u + 1013904223u;
    return *state;
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------

int main(int argc, char** argv) {
    setvbuf(stdout, NULL, _IONBF, 0);

    // Defaults from params.json
    int N          = 100000;
    const char* num_particles_ratio = "0.5";
    int seed_param = 42;

    // Command-line fallback
    N          = parseIntParam(argc, argv, "--size", N);

    // Environment variable overrides
    const char* env_val;
    env_val = getenv("BENCH_PARAM_size");
    if (env_val) N = atoi(env_val);
    env_val = getenv("BENCH_PARAM_num_particles_ratio");
    if (env_val) num_particles_ratio = env_val;
    env_val = getenv("BENCH_PARAM_seed");
    if (env_val) seed_param = atoi(env_val);
    int num_warmup = 0;


    // Print device info
    int device_id = 0;
    CUDA_CHECK(cudaGetDevice(&device_id));
    cudaDeviceProp prop;
    CUDA_CHECK(cudaGetDeviceProperties(&prop, device_id));
    fprintf(stderr, "Device: %s (id=%d)\n", prop.name, device_id);
    fprintf(stderr, "Particle Filter  |  Particles: %d  |  Grid: %dx%d  |  "
            "Steps: %d warmup + %d timed\n\n",
            N, GRID_SIZE, GRID_SIZE, TIME_STEPS, num_warmup);

    size_t float_bytes = (size_t)N * sizeof(float);
    size_t uint_bytes  = (size_t)N * sizeof(unsigned int);

    // -----------------------------------------------------------------------
    // Host data initialization
    // -----------------------------------------------------------------------
    std::vector<float> h_x(N), h_y(N);
    std::vector<unsigned int> h_rng(N);

    unsigned int init_seed = 12345;
    for (int i = 0; i < N; ++i) {
        h_x[i] = (float)(GRID_SIZE / 2) + ((float)(lcg_rand(&init_seed) & 0xFFFF) / 65535.0f - 0.5f) * 10.0f;
        h_y[i] = (float)(GRID_SIZE / 2) + ((float)(lcg_rand(&init_seed) & 0xFFFF) / 65535.0f - 0.5f) * 10.0f;
        h_rng[i] = lcg_rand(&init_seed) | 1u;  // ensure non-zero
    }

    // Generate synthetic observations (target moving in a circle)
    float obs_x[TIME_STEPS], obs_y[TIME_STEPS];
    for (int t = 0; t < TIME_STEPS; ++t) {
        float angle = 2.0f * 3.14159265f * t / TIME_STEPS;
        obs_x[t] = GRID_SIZE / 2.0f + 20.0f * cosf(angle);
        obs_y[t] = GRID_SIZE / 2.0f + 20.0f * sinf(angle);
    }

    // -----------------------------------------------------------------------
    // Device allocations
    // -----------------------------------------------------------------------
    float *d_x, *d_y, *d_x2, *d_y2;
    float *d_weights, *d_cdf;
    unsigned int *d_rng;

    CUDA_CHECK(cudaMalloc(&d_x,       float_bytes));
    CUDA_CHECK(cudaMalloc(&d_y,       float_bytes));
    CUDA_CHECK(cudaMalloc(&d_x2,      float_bytes));
    CUDA_CHECK(cudaMalloc(&d_y2,      float_bytes));
    CUDA_CHECK(cudaMalloc(&d_weights, float_bytes));
    CUDA_CHECK(cudaMalloc(&d_cdf,     float_bytes));
    CUDA_CHECK(cudaMalloc(&d_rng,     uint_bytes));

    // Prefix sum block sums
    int scanBlock = 256;
    int scanElements = scanBlock * 2;
    int numScanBlocks = (N + scanElements - 1) / scanElements;
    size_t blockSumBytes = numScanBlocks * sizeof(float);
    float *d_block_sums;
    CUDA_CHECK(cudaMalloc(&d_block_sums, blockSumBytes));

    int blockSize = 256;
    int gridSize  = (N + blockSize - 1) / blockSize;

    // Lambda: upload initial state
    auto upload_initial = [&]() {
        CUDA_CHECK(cudaMemcpy(d_x,   h_x.data(),   float_bytes, cudaMemcpyHostToDevice));
        CUDA_CHECK(cudaMemcpy(d_y,   h_y.data(),   float_bytes, cudaMemcpyHostToDevice));
        CUDA_CHECK(cudaMemcpy(d_rng, h_rng.data(), uint_bytes,  cudaMemcpyHostToDevice));
    };

    // Lambda: run full particle filter over all time steps
    auto run_pf = [&]() {
        for (int t = 0; t < TIME_STEPS; ++t) {
            // Kernel 1: update positions
            update_particles_kernel<<<dim3(gridSize), dim3(blockSize), 0, 0>>>(d_x, d_y, d_rng, N);

            // Kernel 2: compute weights
            compute_weights_kernel<<<dim3(gridSize), dim3(blockSize), 0, 0>>>(d_x, d_y, d_weights, obs_x[t], obs_y[t], N);

            // Copy weights to CDF for prefix sum
            CUDA_CHECK(cudaMemcpy(d_cdf, d_weights, float_bytes, cudaMemcpyDeviceToDevice));

            // Prefix sum (in-place on d_cdf)
            prefix_sum_kernel<<<dim3(numScanBlocks), dim3(scanBlock), scanElements * sizeof(float), 0>>>(d_cdf, d_block_sums, N);

            // Fix up block sums on host (small array)
            if (numScanBlocks > 1) {
                std::vector<float> h_bsums(numScanBlocks);
                CUDA_CHECK(cudaMemcpy(h_bsums.data(), d_block_sums, blockSumBytes,
                                     cudaMemcpyDeviceToHost));
                host_prefix_sum(h_bsums.data(), numScanBlocks);
                CUDA_CHECK(cudaMemcpy(d_block_sums, h_bsums.data(), blockSumBytes,
                                     cudaMemcpyHostToDevice));

                add_block_sums_kernel<<<dim3(numScanBlocks), dim3(scanBlock), 0, 0>>>(d_cdf, d_block_sums, N);
            }

            // Get total weight (sum of all weights)
            float total_weight = 0.0f;
            {
                // total = cdf[N-1] + weights[N-1]
                float last_cdf, last_w;
                CUDA_CHECK(cudaMemcpy(&last_cdf, d_cdf + N - 1, sizeof(float),
                                     cudaMemcpyDeviceToHost));
                CUDA_CHECK(cudaMemcpy(&last_w, d_weights + N - 1, sizeof(float),
                                     cudaMemcpyDeviceToHost));
                total_weight = last_cdf + last_w;
            }

            if (total_weight < 1e-30f) total_weight = 1.0f;

            // Kernel 3: resample
            resample_kernel<<<dim3(gridSize), dim3(blockSize), 0, 0>>>(d_cdf, d_x, d_y, d_x2, d_y2, d_rng, total_weight, N);

            // Swap buffers
            float* tmp;
            tmp = d_x; d_x = d_x2; d_x2 = tmp;
            tmp = d_y; d_y = d_y2; d_y2 = tmp;
        }
    };

    // -----------------------------------------------------------------------
    // Warmup
    // -----------------------------------------------------------------------
    for (int w = 0; w < num_warmup; ++w) {
        upload_initial();
        run_pf();
        CUDA_CHECK(cudaDeviceSynchronize());
    }

    // -----------------------------------------------------------------------
    // Timed iterations
    // -----------------------------------------------------------------------
    cudaEvent_t evStart, evStop;
    CUDA_CHECK(cudaEventCreate(&evStart));
    CUDA_CHECK(cudaEventCreate(&evStop));

    std::vector<double> times(1);
    for (int i = 0; i < 1; ++i) {
        upload_initial();

        CUDA_CHECK(cudaEventRecord(evStart, 0));
        run_pf();
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

    // particles/sec = N * TIME_STEPS / time_s
    double particles_per_sec = (double)N * TIME_STEPS / (avg_ms * 1e-3);

    double total_ms = sum;

    // JSON-lines kernel events (one per kernel)
    printf("{\"type\":\"kernel\",\"name\":\"update_particles_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"size\":%d"
           "\"num_particles_ratio\":\"%s\",\"seed\":%d}}\n",
           avg_ms, N, num_particles_ratio, seed_param);

    printf("{\"type\":\"kernel\",\"name\":\"compute_weights_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"size\":%d"
           "\"num_particles_ratio\":\"%s\",\"seed\":%d}}\n",
           avg_ms, N, num_particles_ratio, seed_param);

    printf("{\"type\":\"kernel\",\"name\":\"prefix_sum_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"size\":%d"
           "\"num_particles_ratio\":\"%s\",\"seed\":%d}}\n",
           avg_ms, N, num_particles_ratio, seed_param);

    printf("{\"type\":\"kernel\",\"name\":\"add_block_sums_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"size\":%d"
           "\"num_particles_ratio\":\"%s\",\"seed\":%d}}\n",
           avg_ms, N, num_particles_ratio, seed_param);

    printf("{\"type\":\"kernel\",\"name\":\"resample_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"size\":%d"
           "\"num_particles_ratio\":\"%s\",\"seed\":%d}}\n",
           avg_ms, N, num_particles_ratio, seed_param);

    // JSON-lines summary event
    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"particles_per_sec\",\"value\":%.2f}]}\n",
           total_ms, particles_per_sec);

    // Human-readable output to stderr
    fprintf(stderr, "Average time: %.4f ms\n", avg_ms);
    fprintf(stderr, "Throughput:   %.4f particles/sec\n", particles_per_sec);

    // -----------------------------------------------------------------------
    // Verification: run CPU reference and compare mean positions
    // -----------------------------------------------------------------------
    {
        int verify_n = (N < 10000) ? N : 10000;

        // CPU reference
        std::vector<float> cpu_x(verify_n), cpu_y(verify_n);
        std::vector<unsigned int> cpu_rng(verify_n);
        std::vector<float> cpu_w(verify_n), cpu_cdf(verify_n);
        std::vector<float> cpu_xt(verify_n), cpu_yt(verify_n);

        for (int i = 0; i < verify_n; ++i) {
            cpu_x[i]   = h_x[i];
            cpu_y[i]   = h_y[i];
            cpu_rng[i] = h_rng[i];
        }

        for (int t = 0; t < TIME_STEPS; ++t) {
            cpu_particle_filter_step(cpu_x.data(), cpu_y.data(), cpu_rng.data(),
                                      obs_x[t], obs_y[t], cpu_w.data(), cpu_cdf.data(),
                                      cpu_xt.data(), cpu_yt.data(), verify_n);
        }

        // GPU reference (same subset)
        CUDA_CHECK(cudaMemcpy(d_x, h_x.data(), verify_n * sizeof(float), cudaMemcpyHostToDevice));
        CUDA_CHECK(cudaMemcpy(d_y, h_y.data(), verify_n * sizeof(float), cudaMemcpyHostToDevice));
        CUDA_CHECK(cudaMemcpy(d_rng, h_rng.data(), verify_n * sizeof(unsigned int), cudaMemcpyHostToDevice));

        // Run GPU on verify_n particles
        int vGridSize = (verify_n + blockSize - 1) / blockSize;
        int vScanBlocks = (verify_n + scanElements - 1) / scanElements;
        size_t vBlockSumBytes = vScanBlocks * sizeof(float);
        float* d_vblock_sums;
        CUDA_CHECK(cudaMalloc(&d_vblock_sums, vBlockSumBytes));

        for (int t = 0; t < TIME_STEPS; ++t) {
            update_particles_kernel<<<dim3(vGridSize), dim3(blockSize), 0, 0>>>(d_x, d_y, d_rng, verify_n);
            compute_weights_kernel<<<dim3(vGridSize), dim3(blockSize), 0, 0>>>(d_x, d_y, d_weights, obs_x[t], obs_y[t], verify_n);
            CUDA_CHECK(cudaMemcpy(d_cdf, d_weights, verify_n * sizeof(float), cudaMemcpyDeviceToDevice));

            prefix_sum_kernel<<<dim3(vScanBlocks), dim3(scanBlock), scanElements * sizeof(float), 0>>>(d_cdf, d_vblock_sums, verify_n);

            if (vScanBlocks > 1) {
                std::vector<float> h_bsums(vScanBlocks);
                CUDA_CHECK(cudaMemcpy(h_bsums.data(), d_vblock_sums, vBlockSumBytes,
                                     cudaMemcpyDeviceToHost));
                host_prefix_sum(h_bsums.data(), vScanBlocks);
                CUDA_CHECK(cudaMemcpy(d_vblock_sums, h_bsums.data(), vBlockSumBytes,
                                     cudaMemcpyHostToDevice));
                add_block_sums_kernel<<<dim3(vScanBlocks), dim3(scanBlock), 0, 0>>>(d_cdf, d_vblock_sums, verify_n);
            }

            float total_weight = 0.0f;
            {
                float last_cdf, last_w;
                CUDA_CHECK(cudaMemcpy(&last_cdf, d_cdf + verify_n - 1, sizeof(float),
                                     cudaMemcpyDeviceToHost));
                CUDA_CHECK(cudaMemcpy(&last_w, d_weights + verify_n - 1, sizeof(float),
                                     cudaMemcpyDeviceToHost));
                total_weight = last_cdf + last_w;
            }
            if (total_weight < 1e-30f) total_weight = 1.0f;

            resample_kernel<<<dim3(vGridSize), dim3(blockSize), 0, 0>>>(d_cdf, d_x, d_y, d_x2, d_y2, d_rng, total_weight, verify_n);

            float* tmp;
            tmp = d_x; d_x = d_x2; d_x2 = tmp;
            tmp = d_y; d_y = d_y2; d_y2 = tmp;
        }
        CUDA_CHECK(cudaDeviceSynchronize());

        // Compare mean x/y positions
        std::vector<float> gpu_x(verify_n), gpu_y(verify_n);
        CUDA_CHECK(cudaMemcpy(gpu_x.data(), d_x, verify_n * sizeof(float), cudaMemcpyDeviceToHost));
        CUDA_CHECK(cudaMemcpy(gpu_y.data(), d_y, verify_n * sizeof(float), cudaMemcpyDeviceToHost));

        double gpu_mean_x = 0, gpu_mean_y = 0;
        double cpu_mean_x = 0, cpu_mean_y = 0;
        for (int i = 0; i < verify_n; ++i) {
            gpu_mean_x += gpu_x[i];
            gpu_mean_y += gpu_y[i];
            cpu_mean_x += cpu_x[i];
            cpu_mean_y += cpu_y[i];
        }
        gpu_mean_x /= verify_n; gpu_mean_y /= verify_n;
        cpu_mean_x /= verify_n; cpu_mean_y /= verify_n;

        fprintf(stderr, "GPU mean: (%.4f, %.4f)  CPU mean: (%.4f, %.4f)\n",
                gpu_mean_x, gpu_mean_y, cpu_mean_x, cpu_mean_y);

        // Both should converge to similar region (near observations)
        // Allow generous tolerance since particle filters are stochastic
        double diff = sqrt((gpu_mean_x - cpu_mean_x) * (gpu_mean_x - cpu_mean_x) +
                           (gpu_mean_y - cpu_mean_y) * (gpu_mean_y - cpu_mean_y));

        if (diff < 20.0) {
            fprintf(stderr, "PASS\n");
        } else {
            fprintf(stderr, "FAIL: mean position difference %.4f exceeds tolerance\n", diff);
        }

        CUDA_CHECK(cudaFree(d_vblock_sums));
    }

    // Cleanup
    CUDA_CHECK(cudaFree(d_x));
    CUDA_CHECK(cudaFree(d_y));
    CUDA_CHECK(cudaFree(d_x2));
    CUDA_CHECK(cudaFree(d_y2));
    CUDA_CHECK(cudaFree(d_weights));
    CUDA_CHECK(cudaFree(d_cdf));
    CUDA_CHECK(cudaFree(d_rng));
    CUDA_CHECK(cudaFree(d_block_sums));

    return 0;
}
