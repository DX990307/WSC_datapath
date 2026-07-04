// altis_cfd.hip — Altis CFD benchmark: Euler equations solver on unstructured mesh (HIP)
//
// Solves the compressible Euler equations on a synthetic unstructured mesh
// using a finite-volume method. Each thread processes one cell: computes
// fluxes from neighbor data and performs Runge-Kutta time integration.
//
// Compiles with hipcc (AMD ROCm) or nvcc via HIP compatibility layer (CUDA).
//
// Usage:
//   ./altis_cfd [--size N]
//
//   --size N         Number of mesh cells (default: 97000)
//
// Output (stdout): CSV row — altis_cfd,<N>,<time_ms>,<Mcells_per_sec>
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
// Constants for Euler equations
// ---------------------------------------------------------------------------

#define GAMMA 1.4f
#define GAMMA_M1 0.4f       // gamma - 1
#define NUM_NEIGHBORS 4
#define RK_STEPS 2          // 2-stage Runge-Kutta
#define DT 0.0001f          // Time step size

// ---------------------------------------------------------------------------
// Conserved variables per cell: [density, momentum_x, momentum_y, momentum_z, energy]
// We store these as 5 separate arrays (SoA) for better coalescing.
// ---------------------------------------------------------------------------

// Compute pressure from conserved variables
__device__ __host__ static inline float compute_pressure(float rho, float mx, float my,
                                                          float mz, float e) {
    float ke = 0.5f * (mx * mx + my * my + mz * mz) / fmaxf(rho, 1e-10f);
    return GAMMA_M1 * (e - ke);
}

// Compute speed of sound
__device__ __host__ static inline float compute_speed_of_sound(float rho, float p) {
    return sqrtf(fmaxf(GAMMA * p / fmaxf(rho, 1e-10f), 1e-10f));
}

// ---------------------------------------------------------------------------
// Kernel: compute flux contributions for each cell from neighbors
// Uses a simple Rusanov (local Lax-Friedrichs) flux scheme.
// ---------------------------------------------------------------------------

__global__ void compute_flux_kernel(
    const float* __restrict__ rho,
    const float* __restrict__ mx,
    const float* __restrict__ my,
    const float* __restrict__ mz,
    const float* __restrict__ energy,
    const int*   __restrict__ neighbors,  // [N * NUM_NEIGHBORS]
    const float* __restrict__ normals,    // [N * NUM_NEIGHBORS * 3] (nx,ny,nz per face)
    const float* __restrict__ areas,      // [N * NUM_NEIGHBORS] face areas
    float* __restrict__ flux_rho,
    float* __restrict__ flux_mx,
    float* __restrict__ flux_my,
    float* __restrict__ flux_mz,
    float* __restrict__ flux_energy,
    int N)
{
    int idx = blockIdx.x * blockDim.x + threadIdx.x;
    if (idx >= N) return;

    float rho_i = rho[idx];
    float mx_i  = mx[idx];
    float my_i  = my[idx];
    float mz_i  = mz[idx];
    float e_i   = energy[idx];
    float p_i   = compute_pressure(rho_i, mx_i, my_i, mz_i, e_i);
    float a_i   = compute_speed_of_sound(rho_i, p_i);

    // Velocity components
    float inv_rho_i = 1.0f / fmaxf(rho_i, 1e-10f);
    float vx_i = mx_i * inv_rho_i;
    float vy_i = my_i * inv_rho_i;
    float vz_i = mz_i * inv_rho_i;

    float f_rho = 0.0f, f_mx = 0.0f, f_my = 0.0f, f_mz = 0.0f, f_e = 0.0f;

    for (int f = 0; f < NUM_NEIGHBORS; ++f) {
        int j = neighbors[idx * NUM_NEIGHBORS + f];
        int nbase = (idx * NUM_NEIGHBORS + f) * 3;
        float nx = normals[nbase + 0];
        float ny = normals[nbase + 1];
        float nz = normals[nbase + 2];
        float area = areas[idx * NUM_NEIGHBORS + f];

        float rho_j = rho[j];
        float mx_j  = mx[j];
        float my_j  = my[j];
        float mz_j  = mz[j];
        float e_j   = energy[j];
        float p_j   = compute_pressure(rho_j, mx_j, my_j, mz_j, e_j);
        float a_j   = compute_speed_of_sound(rho_j, p_j);

        float inv_rho_j = 1.0f / fmaxf(rho_j, 1e-10f);
        float vx_j = mx_j * inv_rho_j;
        float vy_j = my_j * inv_rho_j;
        float vz_j = mz_j * inv_rho_j;

        // Normal velocity
        float vn_i = vx_i * nx + vy_i * ny + vz_i * nz;
        float vn_j = vx_j * nx + vy_j * ny + vz_j * nz;

        // Physical fluxes in normal direction (left/right)
        // F_rho = rho * vn
        float f_rho_i = rho_i * vn_i;
        float f_rho_j = rho_j * vn_j;

        // F_mx = mx * vn + p * nx
        float f_mx_i = mx_i * vn_i + p_i * nx;
        float f_mx_j = mx_j * vn_j + p_j * nx;

        // F_my = my * vn + p * ny
        float f_my_i = my_i * vn_i + p_i * ny;
        float f_my_j = my_j * vn_j + p_j * ny;

        // F_mz = mz * vn + p * nz
        float f_mz_i = mz_i * vn_i + p_i * nz;
        float f_mz_j = mz_j * vn_j + p_j * nz;

        // F_e = (e + p) * vn
        float f_e_i = (e_i + p_i) * vn_i;
        float f_e_j = (e_j + p_j) * vn_j;

        // Rusanov dissipation: max wave speed
        float lambda = fmaxf(fabsf(vn_i) + a_i, fabsf(vn_j) + a_j);

        // Rusanov flux: F = 0.5*(F_L + F_R) - 0.5*lambda*(U_R - U_L)
        f_rho += area * (0.5f * (f_rho_i + f_rho_j) - 0.5f * lambda * (rho_j - rho_i));
        f_mx  += area * (0.5f * (f_mx_i  + f_mx_j)  - 0.5f * lambda * (mx_j  - mx_i));
        f_my  += area * (0.5f * (f_my_i  + f_my_j)  - 0.5f * lambda * (my_j  - my_i));
        f_mz  += area * (0.5f * (f_mz_i  + f_mz_j)  - 0.5f * lambda * (mz_j  - mz_i));
        f_e   += area * (0.5f * (f_e_i   + f_e_j)   - 0.5f * lambda * (e_j   - e_i));
    }

    flux_rho[idx]    = f_rho;
    flux_mx[idx]     = f_mx;
    flux_my[idx]     = f_my;
    flux_mz[idx]     = f_mz;
    flux_energy[idx] = f_e;
}

// ---------------------------------------------------------------------------
// Kernel: Runge-Kutta time update
// new_U = old_U - dt/volume * flux
// ---------------------------------------------------------------------------

__global__ void rk_update_kernel(
    float* __restrict__ rho,
    float* __restrict__ mx,
    float* __restrict__ my,
    float* __restrict__ mz,
    float* __restrict__ energy,
    const float* __restrict__ rho_old,
    const float* __restrict__ mx_old,
    const float* __restrict__ my_old,
    const float* __restrict__ mz_old,
    const float* __restrict__ energy_old,
    const float* __restrict__ flux_rho,
    const float* __restrict__ flux_mx,
    const float* __restrict__ flux_my,
    const float* __restrict__ flux_mz,
    const float* __restrict__ flux_energy,
    const float* __restrict__ volumes,
    float dt,
    float rk_coeff,
    int N)
{
    int idx = blockIdx.x * blockDim.x + threadIdx.x;
    if (idx >= N) return;

    float inv_vol = 1.0f / volumes[idx];
    float factor  = dt * inv_vol;

    // RK update: U_new = rk_coeff * U_old + (1 - rk_coeff) * (U_n - dt/vol * flux)
    float r = rho_old[idx]    - factor * flux_rho[idx];
    float x = mx_old[idx]     - factor * flux_mx[idx];
    float y = my_old[idx]     - factor * flux_my[idx];
    float z = mz_old[idx]     - factor * flux_mz[idx];
    float e = energy_old[idx] - factor * flux_energy[idx];

    rho[idx]    = rk_coeff * rho_old[idx]    + (1.0f - rk_coeff) * r;
    mx[idx]     = rk_coeff * mx_old[idx]     + (1.0f - rk_coeff) * x;
    my[idx]     = rk_coeff * my_old[idx]     + (1.0f - rk_coeff) * y;
    mz[idx]     = rk_coeff * mz_old[idx]     + (1.0f - rk_coeff) * z;
    energy[idx] = rk_coeff * energy_old[idx] + (1.0f - rk_coeff) * e;
}

// ---------------------------------------------------------------------------
// CPU reference: single-cell flux computation (for verification)
// ---------------------------------------------------------------------------

static void cpu_compute_flux(
    const float* rho, const float* mx, const float* my, const float* mz,
    const float* energy, const int* neighbors, const float* normals,
    const float* areas, float* flux_rho, float* flux_mx, float* flux_my,
    float* flux_mz, float* flux_energy, int idx)
{
    float rho_i = rho[idx], mx_i = mx[idx], my_i = my[idx];
    float mz_i  = mz[idx],  e_i  = energy[idx];
    float p_i = compute_pressure(rho_i, mx_i, my_i, mz_i, e_i);
    float a_i = compute_speed_of_sound(rho_i, p_i);

    float inv_rho_i = 1.0f / fmaxf(rho_i, 1e-10f);
    float vx_i = mx_i * inv_rho_i;
    float vy_i = my_i * inv_rho_i;
    float vz_i = mz_i * inv_rho_i;

    float f_rho = 0, f_mx = 0, f_my = 0, f_mz = 0, f_e = 0;

    for (int f = 0; f < NUM_NEIGHBORS; ++f) {
        int j = neighbors[idx * NUM_NEIGHBORS + f];
        int nbase = (idx * NUM_NEIGHBORS + f) * 3;
        float nx = normals[nbase], ny = normals[nbase+1], nz = normals[nbase+2];
        float area = areas[idx * NUM_NEIGHBORS + f];

        float rho_j = rho[j], mx_j = mx[j], my_j = my[j];
        float mz_j = mz[j], e_j = energy[j];
        float p_j = compute_pressure(rho_j, mx_j, my_j, mz_j, e_j);
        float a_j = compute_speed_of_sound(rho_j, p_j);

        float inv_rho_j = 1.0f / fmaxf(rho_j, 1e-10f);
        float vx_j = mx_j * inv_rho_j;
        float vy_j = my_j * inv_rho_j;
        float vz_j = mz_j * inv_rho_j;

        float vn_i = vx_i*nx + vy_i*ny + vz_i*nz;
        float vn_j = vx_j*nx + vy_j*ny + vz_j*nz;

        float f_rho_i = rho_i*vn_i, f_rho_j = rho_j*vn_j;
        float f_mx_i = mx_i*vn_i + p_i*nx, f_mx_j = mx_j*vn_j + p_j*nx;
        float f_my_i = my_i*vn_i + p_i*ny, f_my_j = my_j*vn_j + p_j*ny;
        float f_mz_i = mz_i*vn_i + p_i*nz, f_mz_j = mz_j*vn_j + p_j*nz;
        float f_e_i = (e_i + p_i)*vn_i, f_e_j = (e_j + p_j)*vn_j;

        float lambda = fmaxf(fabsf(vn_i) + a_i, fabsf(vn_j) + a_j);

        f_rho += area * (0.5f*(f_rho_i + f_rho_j) - 0.5f*lambda*(rho_j - rho_i));
        f_mx  += area * (0.5f*(f_mx_i  + f_mx_j)  - 0.5f*lambda*(mx_j  - mx_i));
        f_my  += area * (0.5f*(f_my_i  + f_my_j)  - 0.5f*lambda*(my_j  - my_i));
        f_mz  += area * (0.5f*(f_mz_i  + f_mz_j)  - 0.5f*lambda*(mz_j  - mz_i));
        f_e   += area * (0.5f*(f_e_i   + f_e_j)   - 0.5f*lambda*(e_j   - e_i));
    }

    flux_rho[idx]    = f_rho;
    flux_mx[idx]     = f_mx;
    flux_my[idx]     = f_my;
    flux_mz[idx]     = f_mz;
    flux_energy[idx] = f_e;
}

// ---------------------------------------------------------------------------
// Simple PRNG for synthetic data generation
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

    // Defaults from params.json
    int N          = 97000;
    int block_size = 256;
    const char* precision = "float";

    // Command-line fallback
    N          = parseIntParam(argc, argv, "--size", N);

    // Environment variable overrides
    const char* env_val;
    env_val = getenv("BENCH_PARAM_size");
    if (env_val) N = atoi(env_val);
    env_val = getenv("BENCH_PARAM_block_size");
    if (env_val) block_size = atoi(env_val);
    env_val = getenv("BENCH_PARAM_precision");
    if (env_val) precision = env_val;
    int num_warmup = 5;


    // Print device info
    int device_id = 0;
    HIP_CHECK(hipGetDevice(&device_id));
    hipDeviceProp_t prop;
    HIP_CHECK(hipGetDeviceProperties(&prop, device_id));
    fprintf(stderr, "Device: %s (id=%d)\n", prop.name, device_id);
    fprintf(stderr, "CFD Euler solver  |  Cells: %d  |  Neighbors: %d  |  "
            "Iterations: 5 warmup + %d timed\n\n", N, NUM_NEIGHBORS);

    // -----------------------------------------------------------------------
    // Generate synthetic mesh data
    // -----------------------------------------------------------------------
    size_t cell_bytes    = (size_t)N * sizeof(float);
    size_t neigh_bytes   = (size_t)N * NUM_NEIGHBORS * sizeof(int);
    size_t normal_bytes  = (size_t)N * NUM_NEIGHBORS * 3 * sizeof(float);
    size_t area_bytes    = (size_t)N * NUM_NEIGHBORS * sizeof(float);

    // Host arrays
    std::vector<float> h_rho(N), h_mx(N), h_my(N), h_mz(N), h_energy(N);
    std::vector<float> h_volumes(N);
    std::vector<int>   h_neighbors(N * NUM_NEIGHBORS);
    std::vector<float> h_normals(N * NUM_NEIGHBORS * 3);
    std::vector<float> h_areas(N * NUM_NEIGHBORS);

    unsigned int seed = 42;

    // Initialize conserved variables (uniform flow with small perturbation)
    for (int i = 0; i < N; ++i) {
        h_rho[i]    = 1.0f + 0.01f * rand_float(&seed, -1.0f, 1.0f);
        h_mx[i]     = 0.5f + 0.01f * rand_float(&seed, -1.0f, 1.0f);
        h_my[i]     = 0.0f + 0.01f * rand_float(&seed, -1.0f, 1.0f);
        h_mz[i]     = 0.0f + 0.01f * rand_float(&seed, -1.0f, 1.0f);
        // energy = p/(gamma-1) + 0.5*rho*v^2
        float p = 1.0f / GAMMA;
        float ke = 0.5f * (h_mx[i]*h_mx[i] + h_my[i]*h_my[i] + h_mz[i]*h_mz[i]) / h_rho[i];
        h_energy[i] = p / GAMMA_M1 + ke;
        h_volumes[i] = 0.01f + 0.001f * rand_float(&seed, 0.0f, 1.0f);
    }

    // Generate random neighbor connectivity and face geometry
    for (int i = 0; i < N; ++i) {
        for (int f = 0; f < NUM_NEIGHBORS; ++f) {
            // Random neighbor (avoid self)
            int j;
            do {
                j = lcg_rand(&seed) % N;
            } while (j == i);
            h_neighbors[i * NUM_NEIGHBORS + f] = j;

            // Random outward normal (normalized)
            float nx = rand_float(&seed, -1.0f, 1.0f);
            float ny = rand_float(&seed, -1.0f, 1.0f);
            float nz = rand_float(&seed, -1.0f, 1.0f);
            float len = sqrtf(nx*nx + ny*ny + nz*nz);
            if (len < 1e-6f) { nx = 1.0f; ny = 0.0f; nz = 0.0f; len = 1.0f; }
            int base = (i * NUM_NEIGHBORS + f) * 3;
            h_normals[base + 0] = nx / len;
            h_normals[base + 1] = ny / len;
            h_normals[base + 2] = nz / len;

            h_areas[i * NUM_NEIGHBORS + f] = 0.001f + 0.0005f * rand_float(&seed, 0.0f, 1.0f);
        }
    }

    // -----------------------------------------------------------------------
    // Device allocations
    // -----------------------------------------------------------------------
    float *d_rho, *d_mx, *d_my, *d_mz, *d_energy;
    float *d_rho_old, *d_mx_old, *d_my_old, *d_mz_old, *d_energy_old;
    float *d_flux_rho, *d_flux_mx, *d_flux_my, *d_flux_mz, *d_flux_energy;
    float *d_volumes, *d_normals, *d_areas;
    int   *d_neighbors;

    HIP_CHECK(hipMalloc(&d_rho,         cell_bytes));
    HIP_CHECK(hipMalloc(&d_mx,          cell_bytes));
    HIP_CHECK(hipMalloc(&d_my,          cell_bytes));
    HIP_CHECK(hipMalloc(&d_mz,          cell_bytes));
    HIP_CHECK(hipMalloc(&d_energy,      cell_bytes));
    HIP_CHECK(hipMalloc(&d_rho_old,     cell_bytes));
    HIP_CHECK(hipMalloc(&d_mx_old,      cell_bytes));
    HIP_CHECK(hipMalloc(&d_my_old,      cell_bytes));
    HIP_CHECK(hipMalloc(&d_mz_old,      cell_bytes));
    HIP_CHECK(hipMalloc(&d_energy_old,  cell_bytes));
    HIP_CHECK(hipMalloc(&d_flux_rho,    cell_bytes));
    HIP_CHECK(hipMalloc(&d_flux_mx,     cell_bytes));
    HIP_CHECK(hipMalloc(&d_flux_my,     cell_bytes));
    HIP_CHECK(hipMalloc(&d_flux_mz,     cell_bytes));
    HIP_CHECK(hipMalloc(&d_flux_energy, cell_bytes));
    HIP_CHECK(hipMalloc(&d_volumes,     cell_bytes));
    HIP_CHECK(hipMalloc(&d_neighbors,   neigh_bytes));
    HIP_CHECK(hipMalloc(&d_normals,     normal_bytes));
    HIP_CHECK(hipMalloc(&d_areas,       area_bytes));

    // Copy mesh data (constant for all runs)
    HIP_CHECK(hipMemcpy(d_volumes,   h_volumes.data(),   cell_bytes,   hipMemcpyHostToDevice));
    HIP_CHECK(hipMemcpy(d_neighbors, h_neighbors.data(), neigh_bytes,  hipMemcpyHostToDevice));
    HIP_CHECK(hipMemcpy(d_normals,   h_normals.data(),   normal_bytes, hipMemcpyHostToDevice));
    HIP_CHECK(hipMemcpy(d_areas,     h_areas.data(),     area_bytes,   hipMemcpyHostToDevice));

    int blockSize = block_size;
    int gridSize  = (N + blockSize - 1) / blockSize;

    // RK coefficients for 2-stage scheme: stage 0 -> coeff=0, stage 1 -> coeff=0.5
    float rk_coeffs[RK_STEPS] = {0.0f, 0.5f};

    // Lambda: run one full RK time step.
    // When timing events are supplied, each kernel launch is wrapped in its own
    // event pair and its elapsed time is accumulated into the matching sum, so
    // each distinct kernel reports its OWN separately measured GPU time
    // (accumulated across the RK_STEPS stages).
    auto run_cfd_step = [&](hipEvent_t* evStart, hipEvent_t* evStop,
                            double* t_flux, double* t_rk) {
        // Save initial state
        HIP_CHECK(hipMemcpy(d_rho_old,    d_rho,    cell_bytes, hipMemcpyDeviceToDevice));
        HIP_CHECK(hipMemcpy(d_mx_old,     d_mx,     cell_bytes, hipMemcpyDeviceToDevice));
        HIP_CHECK(hipMemcpy(d_my_old,     d_my,     cell_bytes, hipMemcpyDeviceToDevice));
        HIP_CHECK(hipMemcpy(d_mz_old,     d_mz,     cell_bytes, hipMemcpyDeviceToDevice));
        HIP_CHECK(hipMemcpy(d_energy_old, d_energy, cell_bytes, hipMemcpyDeviceToDevice));

        for (int stage = 0; stage < RK_STEPS; ++stage) {
            // Compute fluxes
            if (evStart) HIP_CHECK(hipEventRecord(*evStart, 0));
            hipLaunchKernelGGL(compute_flux_kernel, dim3(gridSize), dim3(blockSize),
                               0, 0,
                               d_rho, d_mx, d_my, d_mz, d_energy,
                               d_neighbors, d_normals, d_areas,
                               d_flux_rho, d_flux_mx, d_flux_my, d_flux_mz, d_flux_energy,
                               N);
            if (evStart) {
                HIP_CHECK(hipEventRecord(*evStop, 0));
                HIP_CHECK(hipEventSynchronize(*evStop));
                float ms = 0.0f;
                HIP_CHECK(hipEventElapsedTime(&ms, *evStart, *evStop));
                *t_flux += (double)ms;
            }

            // RK update
            if (evStart) HIP_CHECK(hipEventRecord(*evStart, 0));
            hipLaunchKernelGGL(rk_update_kernel, dim3(gridSize), dim3(blockSize),
                               0, 0,
                               d_rho, d_mx, d_my, d_mz, d_energy,
                               d_rho_old, d_mx_old, d_my_old, d_mz_old, d_energy_old,
                               d_flux_rho, d_flux_mx, d_flux_my, d_flux_mz, d_flux_energy,
                               d_volumes, DT, rk_coeffs[stage], N);
            if (evStart) {
                HIP_CHECK(hipEventRecord(*evStop, 0));
                HIP_CHECK(hipEventSynchronize(*evStop));
                float ms = 0.0f;
                HIP_CHECK(hipEventElapsedTime(&ms, *evStart, *evStop));
                *t_rk += (double)ms;
            }
        }
    };

    // Lambda: upload initial conditions
    auto upload_initial = [&]() {
        HIP_CHECK(hipMemcpy(d_rho,    h_rho.data(),    cell_bytes, hipMemcpyHostToDevice));
        HIP_CHECK(hipMemcpy(d_mx,     h_mx.data(),     cell_bytes, hipMemcpyHostToDevice));
        HIP_CHECK(hipMemcpy(d_my,     h_my.data(),     cell_bytes, hipMemcpyHostToDevice));
        HIP_CHECK(hipMemcpy(d_mz,     h_mz.data(),     cell_bytes, hipMemcpyHostToDevice));
        HIP_CHECK(hipMemcpy(d_energy, h_energy.data(), cell_bytes, hipMemcpyHostToDevice));
    };

    // -----------------------------------------------------------------------
    // Warmup
    // -----------------------------------------------------------------------
    for (int w = 0; w < num_warmup; ++w) {
        upload_initial();
        run_cfd_step(nullptr, nullptr, nullptr, nullptr);  // untimed full sequence
        HIP_CHECK(hipDeviceSynchronize());
    }

    // -----------------------------------------------------------------------
    // Timed iterations — each kernel measured separately with its own
    // accumulator (summed across the RK stages within run_cfd_step).
    // -----------------------------------------------------------------------
    hipEvent_t evStart, evStop;
    HIP_CHECK(hipEventCreate(&evStart));
    HIP_CHECK(hipEventCreate(&evStop));

    double t_flux = 0.0;  // compute_flux_kernel
    double t_rk   = 0.0;  // rk_update_kernel
    for (int i = 0; i < 1; ++i) {
        upload_initial();
        run_cfd_step(&evStart, &evStop, &t_flux, &t_rk);
    }

    HIP_CHECK(hipEventDestroy(evStart));
    HIP_CHECK(hipEventDestroy(evStop));

    double total_ms = t_flux + t_rk;
    double avg_ms   = total_ms;

    // Mcells/sec = N / time_s / 1e6
    double mcells_per_sec = (double)N / (avg_ms * 1e-3) / 1e6;

    // JSON-lines kernel events (one per kernel, each with its OWN measured time)
    printf("{\"type\":\"kernel\",\"name\":\"compute_flux_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"size\":%d,\"block_size\":%d,"
           "\"precision\":\"%s\"}}\n",
           t_flux, N, block_size, precision);

    printf("{\"type\":\"kernel\",\"name\":\"rk_update_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"size\":%d,\"block_size\":%d,"
           "\"precision\":\"%s\"}}\n",
           t_rk, N, block_size, precision);

    // JSON-lines summary event
    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"mcells_per_sec\",\"value\":%.2f}]}\n",
           total_ms, mcells_per_sec);

    // Human-readable output to stderr
    fprintf(stderr, "Average time: %.4f ms  (flux=%.4f rk=%.4f)\n", avg_ms, t_flux, t_rk);
    fprintf(stderr, "Throughput:   %.4f Mcells/sec\n", mcells_per_sec);

    // -----------------------------------------------------------------------
    // Verification: compare GPU flux for first 256 cells with CPU reference
    // -----------------------------------------------------------------------
    {
        int verify_n = (N < 256) ? N : 256;

        // Re-upload initial state and compute one flux pass on GPU
        upload_initial();
        hipLaunchKernelGGL(compute_flux_kernel, dim3(gridSize), dim3(blockSize),
                           0, 0,
                           d_rho, d_mx, d_my, d_mz, d_energy,
                           d_neighbors, d_normals, d_areas,
                           d_flux_rho, d_flux_mx, d_flux_my, d_flux_mz, d_flux_energy,
                           N);
        HIP_CHECK(hipDeviceSynchronize());

        // Download GPU flux
        std::vector<float> gpu_flux_rho(N), gpu_flux_mx(N);
        HIP_CHECK(hipMemcpy(gpu_flux_rho.data(), d_flux_rho, cell_bytes, hipMemcpyDeviceToHost));
        HIP_CHECK(hipMemcpy(gpu_flux_mx.data(),  d_flux_mx,  cell_bytes, hipMemcpyDeviceToHost));

        // CPU reference flux
        std::vector<float> cpu_f_rho(verify_n), cpu_f_mx(verify_n);
        std::vector<float> cpu_f_my(verify_n), cpu_f_mz(verify_n), cpu_f_e(verify_n);

        for (int i = 0; i < verify_n; ++i) {
            cpu_compute_flux(h_rho.data(), h_mx.data(), h_my.data(), h_mz.data(),
                             h_energy.data(), h_neighbors.data(), h_normals.data(),
                             h_areas.data(), cpu_f_rho.data(), cpu_f_mx.data(),
                             cpu_f_my.data(), cpu_f_mz.data(), cpu_f_e.data(), i);
        }

        int errors = 0;
        for (int i = 0; i < verify_n; ++i) {
            float diff_rho = fabsf(gpu_flux_rho[i] - cpu_f_rho[i]);
            float diff_mx  = fabsf(gpu_flux_mx[i]  - cpu_f_mx[i]);
            float tol = 1e-3f * (fabsf(cpu_f_rho[i]) + fabsf(cpu_f_mx[i]) + 1e-6f);
            if (diff_rho > tol || diff_mx > tol) {
                if (errors < 10) {
                    fprintf(stderr, "Mismatch at cell %d: "
                            "GPU_rho=%.6f CPU_rho=%.6f  GPU_mx=%.6f CPU_mx=%.6f\n",
                            i, gpu_flux_rho[i], cpu_f_rho[i],
                            gpu_flux_mx[i], cpu_f_mx[i]);
                }
                errors++;
            }
        }

        if (errors > 0)
            fprintf(stderr, "FAIL: %d errors in first %d cells\n", errors, verify_n);
        else
            fprintf(stderr, "PASS\n");
    }

    // Cleanup
    HIP_CHECK(hipFree(d_rho));
    HIP_CHECK(hipFree(d_mx));
    HIP_CHECK(hipFree(d_my));
    HIP_CHECK(hipFree(d_mz));
    HIP_CHECK(hipFree(d_energy));
    HIP_CHECK(hipFree(d_rho_old));
    HIP_CHECK(hipFree(d_mx_old));
    HIP_CHECK(hipFree(d_my_old));
    HIP_CHECK(hipFree(d_mz_old));
    HIP_CHECK(hipFree(d_energy_old));
    HIP_CHECK(hipFree(d_flux_rho));
    HIP_CHECK(hipFree(d_flux_mx));
    HIP_CHECK(hipFree(d_flux_my));
    HIP_CHECK(hipFree(d_flux_mz));
    HIP_CHECK(hipFree(d_flux_energy));
    HIP_CHECK(hipFree(d_volumes));
    HIP_CHECK(hipFree(d_neighbors));
    HIP_CHECK(hipFree(d_normals));
    HIP_CHECK(hipFree(d_areas));

    return 0;
}
