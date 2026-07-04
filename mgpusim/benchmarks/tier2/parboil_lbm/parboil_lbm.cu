// parboil_lbm.cu — Parboil LBM benchmark: Lattice Boltzmann Method (D3Q19)
//
// Simulates fluid flow using the D3Q19 Lattice Boltzmann Method on a
// regular 3D grid. The collide-stream kernel applies the BGK collision
// operator, computes equilibrium distributions, and streams to neighbor
// cells in a single fused pass. Bounce-back boundary conditions are
// applied at the grid boundaries.
//
// Native CUDA implementation.
//
// Output (stdout): JSON-lines
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
// D3Q19 velocity set
// ---------------------------------------------------------------------------

#define Q 19

// D3Q19 velocity vectors (ex, ey, ez) and weights
__constant__ int d_ex[Q] = { 0, 1,-1, 0, 0, 0, 0, 1,-1, 1,-1, 1,-1, 1,-1, 0, 0, 0, 0};
__constant__ int d_ey[Q] = { 0, 0, 0, 1,-1, 0, 0, 1, 1,-1,-1, 0, 0, 0, 0, 1,-1, 1,-1};
__constant__ int d_ez[Q] = { 0, 0, 0, 0, 0, 1,-1, 0, 0, 0, 0, 1, 1,-1,-1, 1, 1,-1,-1};

__constant__ float d_w[Q] = {
    1.0f/3.0f,                                         // 0: rest
    1.0f/18.0f, 1.0f/18.0f, 1.0f/18.0f,               // 1-3: face
    1.0f/18.0f, 1.0f/18.0f, 1.0f/18.0f,               // 4-6: face
    1.0f/36.0f, 1.0f/36.0f, 1.0f/36.0f, 1.0f/36.0f,   // 7-10: edge
    1.0f/36.0f, 1.0f/36.0f, 1.0f/36.0f, 1.0f/36.0f,   // 11-14: edge
    1.0f/36.0f, 1.0f/36.0f, 1.0f/36.0f, 1.0f/36.0f    // 15-18: edge
};

// Opposite direction for bounce-back
__constant__ int d_opp[Q] = {0, 2,1, 4,3, 6,5, 10,9,8,7, 14,13,12,11, 18,17,16,15};

// Host copies for CPU reference
static const int h_ex[Q] = { 0, 1,-1, 0, 0, 0, 0, 1,-1, 1,-1, 1,-1, 1,-1, 0, 0, 0, 0};
static const int h_ey[Q] = { 0, 0, 0, 1,-1, 0, 0, 1, 1,-1,-1, 0, 0, 0, 0, 1,-1, 1,-1};
static const int h_ez[Q] = { 0, 0, 0, 0, 0, 1,-1, 0, 0, 0, 0, 1, 1,-1,-1, 1, 1,-1,-1};
static const float h_w[Q] = {
    1.0f/3.0f,
    1.0f/18.0f, 1.0f/18.0f, 1.0f/18.0f,
    1.0f/18.0f, 1.0f/18.0f, 1.0f/18.0f,
    1.0f/36.0f, 1.0f/36.0f, 1.0f/36.0f, 1.0f/36.0f,
    1.0f/36.0f, 1.0f/36.0f, 1.0f/36.0f, 1.0f/36.0f,
    1.0f/36.0f, 1.0f/36.0f, 1.0f/36.0f, 1.0f/36.0f
};
static const int h_opp[Q] = {0, 2,1, 4,3, 6,5, 10,9,8,7, 14,13,12,11, 18,17,16,15};

// ---------------------------------------------------------------------------
// Kernel: Fused collide-stream with BGK collision and bounce-back boundary
//
// f_src: input distributions  [N*Q]
// f_dst: output distributions [N*Q]
// Each thread handles one lattice node.
// ---------------------------------------------------------------------------

__global__ void lbm_collide_stream_kernel(
    const float* __restrict__ f_src,
    float*       __restrict__ f_dst,
    int Nx, int Ny, int Nz,
    float omega)  // omega = 1/tau
{
    int idx = blockIdx.x * blockDim.x + threadIdx.x;
    int N = Nx * Ny * Nz;
    if (idx >= N) return;

    int iz = idx / (Nx * Ny);
    int iy = (idx / Nx) % Ny;
    int ix = idx % Nx;

    // Check if boundary node (bounce-back)
    bool is_boundary = (ix == 0 || ix == Nx-1 ||
                        iy == 0 || iy == Ny-1 ||
                        iz == 0 || iz == Nz-1);

    // Load distributions
    float f[Q];
    for (int q = 0; q < Q; ++q) {
        f[q] = f_src[q * N + idx];
    }

    // Compute macroscopic quantities: density and velocity
    float rho = 0.0f;
    float ux = 0.0f, uy = 0.0f, uz = 0.0f;
    for (int q = 0; q < Q; ++q) {
        rho += f[q];
        ux += f[q] * d_ex[q];
        uy += f[q] * d_ey[q];
        uz += f[q] * d_ez[q];
    }
    float inv_rho = 1.0f / fmaxf(rho, 1e-10f);
    ux *= inv_rho;
    uy *= inv_rho;
    uz *= inv_rho;

    // BGK collision: f_eq = w_q * rho * (1 + 3(e·u) + 4.5(e·u)^2 - 1.5 u^2)
    float u2 = ux * ux + uy * uy + uz * uz;
    float f_post[Q];
    for (int q = 0; q < Q; ++q) {
        float eu = (float)d_ex[q] * ux + (float)d_ey[q] * uy + (float)d_ez[q] * uz;
        float f_eq = d_w[q] * rho * (1.0f + 3.0f * eu + 4.5f * eu * eu - 1.5f * u2);
        f_post[q] = f[q] + omega * (f_eq - f[q]);
    }

    // Stream: write to neighbor cells
    for (int q = 0; q < Q; ++q) {
        int nx = ix + d_ex[q];
        int ny = iy + d_ey[q];
        int nz = iz + d_ez[q];

        if (is_boundary) {
            // Bounce-back: reflect to opposite direction at same node
            f_dst[d_opp[q] * N + idx] = f_post[q];
        } else if (nx >= 0 && nx < Nx && ny >= 0 && ny < Ny && nz >= 0 && nz < Nz) {
            int nidx = nz * Nx * Ny + ny * Nx + nx;
            f_dst[q * N + nidx] = f_post[q];
        } else {
            // Out of bounds: bounce-back
            f_dst[d_opp[q] * N + idx] = f_post[q];
        }
    }
}

// ---------------------------------------------------------------------------
// CPU reference: single-node collide (no streaming)
// ---------------------------------------------------------------------------

static void cpu_collide(
    const float* f_src, float* rho_out, float* ux_out, float* uy_out,
    float* uz_out, int idx, int N)
{
    float rho = 0.0f;
    float ux = 0.0f, uy = 0.0f, uz = 0.0f;
    for (int q = 0; q < Q; ++q) {
        float fq = f_src[q * N + idx];
        rho += fq;
        ux += fq * h_ex[q];
        uy += fq * h_ey[q];
        uz += fq * h_ez[q];
    }
    float inv_rho = 1.0f / fmaxf(rho, 1e-10f);
    *rho_out = rho;
    *ux_out  = ux * inv_rho;
    *uy_out  = uy * inv_rho;
    *uz_out  = uz * inv_rho;
}

// ---------------------------------------------------------------------------
// Simple PRNG
// ---------------------------------------------------------------------------

static inline unsigned int lcg_rand(unsigned int* state) {
    *state = *state * 1664525u + 1013904223u;
    return *state;
}

static inline float rand_float(unsigned int* state, float lo, float hi) {
    return lo + (float)(lcg_rand(state) & 0xFFFF) / 65535.0f * (hi - lo);
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------

int main(int argc, char** argv) {
    setvbuf(stdout, NULL, _IONBF, 0);

    // Defaults
    int grid_dim     = 64;
    int block_size   = 128;
    int num_timesteps = 100;
    float tau        = 0.7f;
    int iterations   = 5;

    // Environment variable overrides
    const char* env_val;
    env_val = getenv("BENCH_PARAM_grid_dim");
    if (env_val) grid_dim = atoi(env_val);
    env_val = getenv("BENCH_PARAM_block_size");
    if (env_val) block_size = atoi(env_val);
    env_val = getenv("BENCH_PARAM_num_timesteps");
    if (env_val) num_timesteps = atoi(env_val);
    env_val = getenv("BENCH_PARAM_tau");
    if (env_val) tau = (float)atof(env_val);
    env_val = getenv("BENCH_PARAM_iterations");
    if (env_val) iterations = atoi(env_val);
    int num_warmup = 0;

    int iters = iterations;
    float omega = 1.0f / tau;
    int Nx = grid_dim, Ny = grid_dim, Nz = grid_dim;
    int N = Nx * Ny * Nz;

    // Print device info
    int device_id = 0;
    CUDA_CHECK(cudaGetDevice(&device_id));
    cudaDeviceProp prop;
    CUDA_CHECK(cudaGetDeviceProperties(&prop, device_id));
    fprintf(stderr, "Device: %s (id=%d)\n", prop.name, device_id);
    fprintf(stderr, "LBM D3Q19  |  Grid: %dx%dx%d = %d  |  Timesteps: %d  |  "
            "Tau: %.2f  |  Iterations: 5 warmup + %d timed\n\n",
            Nx, Ny, Nz, N, num_timesteps, tau, iters);

    // -----------------------------------------------------------------------
    // Initialize distribution functions with equilibrium (rho=1, u=0)
    // plus small perturbation
    // -----------------------------------------------------------------------
    size_t f_bytes = (size_t)Q * N * sizeof(float);
    std::vector<float> h_f(Q * N);

    unsigned int seed = 42;
    for (int idx = 0; idx < N; ++idx) {
        float rho = 1.0f + 0.001f * rand_float(&seed, -1.0f, 1.0f);
        for (int q = 0; q < Q; ++q) {
            h_f[q * N + idx] = h_w[q] * rho;
        }
    }

    // -----------------------------------------------------------------------
    // Device allocations (double-buffered)
    // -----------------------------------------------------------------------
    float *d_f_src, *d_f_dst;

    CUDA_CHECK(cudaMalloc(&d_f_src, f_bytes));
    CUDA_CHECK(cudaMalloc(&d_f_dst, f_bytes));

    int gridSize = (N + block_size - 1) / block_size;

    // Lambda: run num_timesteps LBM steps
    auto run_lbm = [&]() {
        for (int t = 0; t < num_timesteps; ++t) {
            lbm_collide_stream_kernel<<<gridSize, block_size>>>(
                d_f_src, d_f_dst, Nx, Ny, Nz, omega);

            // Swap buffers
            float* tmp = d_f_src;
            d_f_src = d_f_dst;
            d_f_dst = tmp;
        }
    };

    // Lambda: upload initial conditions
    auto upload_initial = [&]() {
        CUDA_CHECK(cudaMemcpy(d_f_src, h_f.data(), f_bytes, cudaMemcpyHostToDevice));
        CUDA_CHECK(cudaMemset(d_f_dst, 0, f_bytes));
    };

    // -----------------------------------------------------------------------
    // Warmup
    // -----------------------------------------------------------------------
    for (int w = 0; w < num_warmup; ++w) {
        upload_initial();
        run_lbm();
        CUDA_CHECK(cudaDeviceSynchronize());
    }

    // -----------------------------------------------------------------------
    // Timed iterations
    // -----------------------------------------------------------------------
    cudaEvent_t evStart, evStop;
    CUDA_CHECK(cudaEventCreate(&evStart));
    CUDA_CHECK(cudaEventCreate(&evStop));

    std::vector<double> times((size_t)iters);
    for (int i = 0; i < iters; ++i) {
        upload_initial();

        CUDA_CHECK(cudaEventRecord(evStart, 0));
        run_lbm();
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
    for (int i = 0; i < iters; ++i) sum += times[i];
    double avg_ms = sum / iters;
    double total_ms = sum;

    // MLUPS = million lattice updates per second
    double mlups = (double)N * num_timesteps / (avg_ms * 1e-3) / 1e6;

    // JSON-lines output
    printf("{\"type\":\"kernel\",\"name\":\"lbm_collide_stream_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"grid_dim\":%d,\"iterations\":%d,\"block_size\":%d,"
           "\"num_timesteps\":%d,\"tau\":%.2f}}\n",
           avg_ms, grid_dim, iters, block_size, num_timesteps, tau);

    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"mlups\",\"value\":%.2f}]}\n",
           total_ms, mlups);

    // Human-readable output
    fprintf(stderr, "Average time: %.4f ms\n", avg_ms);
    fprintf(stderr, "Throughput:   %.2f MLUPS\n", mlups);

    // -----------------------------------------------------------------------
    // Verification: check density conservation on interior nodes
    // -----------------------------------------------------------------------
    {
        std::vector<float> h_f_out(Q * N);
        CUDA_CHECK(cudaMemcpy(h_f_out.data(), d_f_src, f_bytes, cudaMemcpyDeviceToHost));

        int verify_n = 128;
        int errors = 0;
        unsigned int vseed = 123;

        for (int v = 0; v < verify_n; ++v) {
            // Pick interior nodes only
            int ix = 1 + (lcg_rand(&vseed) % (Nx - 2));
            int iy = 1 + (lcg_rand(&vseed) % (Ny - 2));
            int iz = 1 + (lcg_rand(&vseed) % (Nz - 2));
            int idx = iz * Nx * Ny + iy * Nx + ix;

            float rho, ux, uy, uz;
            cpu_collide(h_f_out.data(), &rho, &ux, &uy, &uz, idx, N);

            // After LBM steps, density should remain close to 1.0
            // (conservation for simple initialization)
            if (rho < 0.5f || rho > 2.0f || isnan(rho)) {
                if (errors < 10) {
                    fprintf(stderr, "Suspicious density at (%d,%d,%d): rho=%.6f\n",
                            ix, iy, iz, rho);
                }
                errors++;
            }
        }

        if (errors > 0)
            fprintf(stderr, "FAIL: %d suspicious nodes in %d verified\n", errors, verify_n);
        else
            fprintf(stderr, "PASS\n");
    }

    // Cleanup
    CUDA_CHECK(cudaFree(d_f_src));
    CUDA_CHECK(cudaFree(d_f_dst));

    return 0;
}
