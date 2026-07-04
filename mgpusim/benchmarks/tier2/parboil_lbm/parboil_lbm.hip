// parboil_lbm.hip — Parboil LBM benchmark: Lattice Boltzmann Method D3Q19 (HIP)
//
// Simulates fluid flow using the D3Q19 Lattice Boltzmann Method on a
// regular 3D grid. The collide-stream kernel applies the BGK collision
// operator and streams to neighbor cells. Bounce-back boundary conditions
// are applied at grid boundaries.
//
// Compiles with hipcc (AMD ROCm) or nvcc via HIP compatibility layer (CUDA).
//
// Output (stdout): JSON-lines
// Output (stderr): human-readable results

#if defined(__NVCC__) && !__has_include(<hip/hip_runtime.h>)
#include <cuda_runtime.h>
#define hipMalloc               cudaMalloc
#define hipFree                 cudaFree
#define hipMemcpy               cudaMemcpy
#define hipMemset               cudaMemset
#define hipMemcpyHostToDevice   cudaMemcpyHostToDevice
#define hipMemcpyDeviceToHost   cudaMemcpyDeviceToHost
#define hipDeviceSynchronize    cudaDeviceSynchronize
#define hipGetDevice            cudaGetDevice
#define hipGetDeviceProperties  cudaGetDeviceProperties
#define hipGetErrorString       cudaGetErrorString
#define hipEvent_t              cudaEvent_t
#define hipEventCreate          cudaEventCreate
#define hipEventDestroy         cudaEventDestroy
#define hipEventRecord          cudaEventRecord
#define hipEventSynchronize     cudaEventSynchronize
#define hipEventElapsedTime     cudaEventElapsedTime
#define hipDeviceProp_t         cudaDeviceProp
#define hipError_t              cudaError_t
#define hipSuccess              cudaSuccess
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
// D3Q19 velocity set
// ---------------------------------------------------------------------------

#define Q 19

__constant__ int d_ex[Q] = { 0, 1,-1, 0, 0, 0, 0, 1,-1, 1,-1, 1,-1, 1,-1, 0, 0, 0, 0};
__constant__ int d_ey[Q] = { 0, 0, 0, 1,-1, 0, 0, 1, 1,-1,-1, 0, 0, 0, 0, 1,-1, 1,-1};
__constant__ int d_ez[Q] = { 0, 0, 0, 0, 0, 1,-1, 0, 0, 0, 0, 1, 1,-1,-1, 1, 1,-1,-1};

__constant__ float d_w[Q] = {
    1.0f/3.0f,
    1.0f/18.0f, 1.0f/18.0f, 1.0f/18.0f,
    1.0f/18.0f, 1.0f/18.0f, 1.0f/18.0f,
    1.0f/36.0f, 1.0f/36.0f, 1.0f/36.0f, 1.0f/36.0f,
    1.0f/36.0f, 1.0f/36.0f, 1.0f/36.0f, 1.0f/36.0f,
    1.0f/36.0f, 1.0f/36.0f, 1.0f/36.0f, 1.0f/36.0f
};

__constant__ int d_opp[Q] = {0, 2,1, 4,3, 6,5, 10,9,8,7, 14,13,12,11, 18,17,16,15};

// Host copies
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

// ---------------------------------------------------------------------------
// Kernel: Fused collide-stream with BGK collision and bounce-back
// ---------------------------------------------------------------------------

__global__ void lbm_collide_stream_kernel(
    const float* __restrict__ f_src,
    float*       __restrict__ f_dst,
    int Nx, int Ny, int Nz,
    float omega)
{
    int idx = blockIdx.x * blockDim.x + threadIdx.x;
    int N = Nx * Ny * Nz;
    if (idx >= N) return;

    int iz = idx / (Nx * Ny);
    int iy = (idx / Nx) % Ny;
    int ix = idx % Nx;

    bool is_boundary = (ix == 0 || ix == Nx-1 ||
                        iy == 0 || iy == Ny-1 ||
                        iz == 0 || iz == Nz-1);

    float f[Q];
    for (int q = 0; q < Q; ++q) {
        f[q] = f_src[q * N + idx];
    }

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

    float u2 = ux * ux + uy * uy + uz * uz;
    float f_post[Q];
    for (int q = 0; q < Q; ++q) {
        float eu = (float)d_ex[q] * ux + (float)d_ey[q] * uy + (float)d_ez[q] * uz;
        float f_eq = d_w[q] * rho * (1.0f + 3.0f * eu + 4.5f * eu * eu - 1.5f * u2);
        f_post[q] = f[q] + omega * (f_eq - f[q]);
    }

    for (int q = 0; q < Q; ++q) {
        int nx = ix + d_ex[q];
        int ny = iy + d_ey[q];
        int nz = iz + d_ez[q];

        if (is_boundary) {
            f_dst[d_opp[q] * N + idx] = f_post[q];
        } else if (nx >= 0 && nx < Nx && ny >= 0 && ny < Ny && nz >= 0 && nz < Nz) {
            int nidx = nz * Nx * Ny + ny * Nx + nx;
            f_dst[q * N + nidx] = f_post[q];
        } else {
            f_dst[d_opp[q] * N + idx] = f_post[q];
        }
    }
}

// ---------------------------------------------------------------------------
// CPU reference
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

    int grid_dim     = 64;
    int block_size   = 128;
    int num_timesteps = 100;
    float tau        = 0.7f;
    int iterations   = 5;

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
    int num_warmup = 5;

    int iters = iterations;
    float omega = 1.0f / tau;
    int Nx = grid_dim, Ny = grid_dim, Nz = grid_dim;
    int N = Nx * Ny * Nz;

    int device_id = 0;
    HIP_CHECK(hipGetDevice(&device_id));
    hipDeviceProp_t prop;
    HIP_CHECK(hipGetDeviceProperties(&prop, device_id));
    fprintf(stderr, "Device: %s (id=%d)\n", prop.name, device_id);
    fprintf(stderr, "LBM D3Q19  |  Grid: %dx%dx%d = %d  |  Timesteps: %d  |  "
            "Tau: %.2f  |  Iterations: 5 warmup + %d timed\n\n",
            Nx, Ny, Nz, N, num_timesteps, tau, iters);

    // Initialize equilibrium distributions
    size_t f_bytes = (size_t)Q * N * sizeof(float);
    std::vector<float> h_f(Q * N);

    unsigned int seed = 42;
    for (int idx = 0; idx < N; ++idx) {
        float rho = 1.0f + 0.001f * rand_float(&seed, -1.0f, 1.0f);
        for (int q = 0; q < Q; ++q) {
            h_f[q * N + idx] = h_w[q] * rho;
        }
    }

    float *d_f_src, *d_f_dst;
    HIP_CHECK(hipMalloc(&d_f_src, f_bytes));
    HIP_CHECK(hipMalloc(&d_f_dst, f_bytes));

    int gridSize = (N + block_size - 1) / block_size;

    auto run_lbm = [&]() {
        for (int t = 0; t < num_timesteps; ++t) {
            hipLaunchKernelGGL(lbm_collide_stream_kernel,
                               dim3(gridSize), dim3(block_size), 0, 0,
                               d_f_src, d_f_dst, Nx, Ny, Nz, omega);
            float* tmp = d_f_src;
            d_f_src = d_f_dst;
            d_f_dst = tmp;
        }
    };

    auto upload_initial = [&]() {
        HIP_CHECK(hipMemcpy(d_f_src, h_f.data(), f_bytes, hipMemcpyHostToDevice));
        HIP_CHECK(hipMemset(d_f_dst, 0, f_bytes));
    };

    // Warmup
    for (int w = 0; w < num_warmup; ++w) {
        upload_initial();
        run_lbm();
        HIP_CHECK(hipDeviceSynchronize());
    }

    // Timed iterations
    hipEvent_t evStart, evStop;
    HIP_CHECK(hipEventCreate(&evStart));
    HIP_CHECK(hipEventCreate(&evStop));

    std::vector<double> times((size_t)iters);
    for (int i = 0; i < iters; ++i) {
        upload_initial();

        HIP_CHECK(hipEventRecord(evStart, 0));
        run_lbm();
        HIP_CHECK(hipEventRecord(evStop, 0));
        HIP_CHECK(hipEventSynchronize(evStop));

        float ms = 0.0f;
        HIP_CHECK(hipEventElapsedTime(&ms, evStart, evStop));
        times[i] = (double)ms;
    }

    HIP_CHECK(hipEventDestroy(evStart));
    HIP_CHECK(hipEventDestroy(evStop));

    double sum = 0.0;
    for (int i = 0; i < iters; ++i) sum += times[i];
    double avg_ms = sum / iters;
    double total_ms = sum;

    double mlups = (double)N * num_timesteps / (avg_ms * 1e-3) / 1e6;

    printf("{\"type\":\"kernel\",\"name\":\"lbm_collide_stream_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"grid_dim\":%d,\"iterations\":%d,\"block_size\":%d,"
           "\"num_timesteps\":%d,\"tau\":%.2f}}\n",
           avg_ms, grid_dim, iters, block_size, num_timesteps, tau);

    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"mlups\",\"value\":%.2f}]}\n",
           total_ms, mlups);

    fprintf(stderr, "Average time: %.4f ms\n", avg_ms);
    fprintf(stderr, "Throughput:   %.2f MLUPS\n", mlups);

    // Verification
    {
        std::vector<float> h_f_out(Q * N);
        HIP_CHECK(hipMemcpy(h_f_out.data(), d_f_src, f_bytes, hipMemcpyDeviceToHost));

        int verify_n = 128;
        int errors = 0;
        unsigned int vseed = 123;

        for (int v = 0; v < verify_n; ++v) {
            int ix = 1 + (lcg_rand(&vseed) % (Nx - 2));
            int iy = 1 + (lcg_rand(&vseed) % (Ny - 2));
            int iz = 1 + (lcg_rand(&vseed) % (Nz - 2));
            int idx = iz * Nx * Ny + iy * Nx + ix;

            float rho, ux, uy, uz;
            cpu_collide(h_f_out.data(), &rho, &ux, &uy, &uz, idx, N);

            if (rho < 0.5f || rho > 2.0f || std::isnan(rho)) {
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

    HIP_CHECK(hipFree(d_f_src));
    HIP_CHECK(hipFree(d_f_dst));

    return 0;
}
