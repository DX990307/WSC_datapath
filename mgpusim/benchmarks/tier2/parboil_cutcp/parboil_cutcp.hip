// parboil_cutcp.hip — Parboil CUTCP benchmark: Coulombic potential with cutoff (HIP)
//
// Direct summation of electrostatic charge interactions on a 3D grid.
// Each thread computes the potential at one grid point by iterating over
// all atoms within a cutoff radius.
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
// Constants
// ---------------------------------------------------------------------------

#define GRID_DIM_SIDE 64

// ---------------------------------------------------------------------------
// Kernel: compute Coulombic potential at each grid point
// ---------------------------------------------------------------------------

__global__ void cutcp_kernel(
    const float4* __restrict__ atoms,
    float*        __restrict__ potential,
    int           num_atoms,
    int           grid_side,
    float         grid_spacing,
    float         cutoff,
    float         cutoff2)
{
    int gid = blockIdx.x * blockDim.x + threadIdx.x;
    int total_points = grid_side * grid_side * grid_side;
    if (gid >= total_points) return;

    int gz = gid / (grid_side * grid_side);
    int gy = (gid / grid_side) % grid_side;
    int gx = gid % grid_side;

    float px = gx * grid_spacing;
    float py = gy * grid_spacing;
    float pz = gz * grid_spacing;

    float pot = 0.0f;

    for (int i = 0; i < num_atoms; ++i) {
        float4 atom = atoms[i];
        float dx = px - atom.x;
        float dy = py - atom.y;
        float dz = pz - atom.z;
        float r2 = dx * dx + dy * dy + dz * dz;

        if (r2 < cutoff2 && r2 > 1e-12f) {
            float r = sqrtf(r2);
            pot += atom.w / r;
        }
    }

    potential[gid] = pot;
}

// ---------------------------------------------------------------------------
// CPU reference
// ---------------------------------------------------------------------------

static float cpu_potential(
    const float* atoms_x, const float* atoms_y,
    const float* atoms_z, const float* atoms_q,
    int num_atoms, float px, float py, float pz,
    float cutoff2)
{
    float pot = 0.0f;
    for (int i = 0; i < num_atoms; ++i) {
        float dx = px - atoms_x[i];
        float dy = py - atoms_y[i];
        float dz = pz - atoms_z[i];
        float r2 = dx * dx + dy * dy + dz * dz;
        if (r2 < cutoff2 && r2 > 1e-12f) {
            pot += atoms_q[i] / sqrtf(r2);
        }
    }
    return pot;
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

    int num_atoms    = 10000;
    int block_size   = 128;
    float grid_spacing = 0.5f;
    float cutoff     = 12.0f;

    const char* env_val;
    env_val = getenv("BENCH_PARAM_num_atoms");
    if (env_val) num_atoms = atoi(env_val);
    env_val = getenv("BENCH_PARAM_block_size");
    if (env_val) block_size = atoi(env_val);
    env_val = getenv("BENCH_PARAM_grid_spacing");
    if (env_val) grid_spacing = (float)atof(env_val);
    env_val = getenv("BENCH_PARAM_cutoff_radius");
    if (env_val) cutoff = (float)atof(env_val);
    int num_warmup = 5;

    float cutoff2 = cutoff * cutoff;
    int grid_side = GRID_DIM_SIDE;
    int total_grid_points = grid_side * grid_side * grid_side;
    float domain_size = grid_side * grid_spacing;

    int device_id = 0;
    HIP_CHECK(hipGetDevice(&device_id));
    hipDeviceProp_t prop;
    HIP_CHECK(hipGetDeviceProperties(&prop, device_id));
    fprintf(stderr, "Device: %s (id=%d)\n", prop.name, device_id);
    fprintf(stderr, "CUTCP  |  Atoms: %d  |  Grid: %d^3 = %d  |  Cutoff: %.1f  |  "
            "Spacing: %.2f  |  Iterations: 5 warmup + %d timed\n\n",
            num_atoms, grid_side, total_grid_points, cutoff, grid_spacing);

    // Generate synthetic atom data
    std::vector<float> h_atoms_x(num_atoms), h_atoms_y(num_atoms);
    std::vector<float> h_atoms_z(num_atoms), h_atoms_q(num_atoms);
    std::vector<float4> h_atoms(num_atoms);

    unsigned int seed = 42;
    for (int i = 0; i < num_atoms; ++i) {
        float x = rand_float(&seed, 0.0f, domain_size);
        float y = rand_float(&seed, 0.0f, domain_size);
        float z = rand_float(&seed, 0.0f, domain_size);
        float q = rand_float(&seed, -1.0f, 1.0f);
        h_atoms_x[i] = x;
        h_atoms_y[i] = y;
        h_atoms_z[i] = z;
        h_atoms_q[i] = q;
        h_atoms[i] = make_float4(x, y, z, q);
    }

    size_t atom_bytes = (size_t)num_atoms * sizeof(float4);
    size_t grid_bytes = (size_t)total_grid_points * sizeof(float);

    float4* d_atoms;
    float*  d_potential;

    HIP_CHECK(hipMalloc(&d_atoms,     atom_bytes));
    HIP_CHECK(hipMalloc(&d_potential,  grid_bytes));
    HIP_CHECK(hipMemcpy(d_atoms, h_atoms.data(), atom_bytes, hipMemcpyHostToDevice));

    int gridSize = (total_grid_points + block_size - 1) / block_size;

    auto run_cutcp = [&]() {
        hipLaunchKernelGGL(cutcp_kernel, dim3(gridSize), dim3(block_size),
                           0, 0,
                           d_atoms, d_potential, num_atoms,
                           grid_side, grid_spacing, cutoff, cutoff2);
    };

    // Warmup
    for (int w = 0; w < num_warmup; ++w) {
        run_cutcp();
        HIP_CHECK(hipDeviceSynchronize());
    }

    // Timed iterations
    hipEvent_t evStart, evStop;
    HIP_CHECK(hipEventCreate(&evStart));
    HIP_CHECK(hipEventCreate(&evStop));

    std::vector<double> times(1);
    for (int i = 0; i < 1; ++i) {
        HIP_CHECK(hipEventRecord(evStart, 0));
        run_cutcp();
        HIP_CHECK(hipEventRecord(evStop, 0));
        HIP_CHECK(hipEventSynchronize(evStop));

        float ms = 0.0f;
        HIP_CHECK(hipEventElapsedTime(&ms, evStart, evStop));
        times[i] = (double)ms;
    }

    HIP_CHECK(hipEventDestroy(evStart));
    HIP_CHECK(hipEventDestroy(evStop));

    double sum = 0.0;
    for (int i = 0; i < 1; ++i) sum += times[i];
    double avg_ms = sum / 1;
    double total_ms = sum;

    double interactions = (double)num_atoms * (double)total_grid_points;
    double ginteractions_per_sec = interactions / (avg_ms * 1e-3) / 1e9;

    printf("{\"type\":\"kernel\",\"name\":\"cutcp_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"num_atoms\":%d,\"block_size\":%d,"
           "\"grid_spacing\":%.2f,\"cutoff_radius\":%.1f}}\n",
           avg_ms, num_atoms, block_size, grid_spacing, cutoff);

    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"ginteractions_per_sec\",\"value\":%.4f}]}\n",
           total_ms, ginteractions_per_sec);

    fprintf(stderr, "Average time: %.4f ms\n", avg_ms);
    fprintf(stderr, "Throughput:   %.4f Ginteractions/sec\n", ginteractions_per_sec);

    // Verification
    {
        std::vector<float> h_potential(total_grid_points);
        HIP_CHECK(hipMemcpy(h_potential.data(), d_potential, grid_bytes,
                            hipMemcpyDeviceToHost));

        int verify_n = 64;
        int errors = 0;
        unsigned int vseed = 123;
        for (int v = 0; v < verify_n; ++v) {
            int idx = lcg_rand(&vseed) % total_grid_points;
            int gz = idx / (grid_side * grid_side);
            int gy = (idx / grid_side) % grid_side;
            int gx = idx % grid_side;
            float px = gx * grid_spacing;
            float py = gy * grid_spacing;
            float pz = gz * grid_spacing;

            float cpu_pot = cpu_potential(
                h_atoms_x.data(), h_atoms_y.data(),
                h_atoms_z.data(), h_atoms_q.data(),
                num_atoms, px, py, pz, cutoff2);

            float gpu_pot = h_potential[idx];
            float diff = fabsf(gpu_pot - cpu_pot);
            float tol = 1e-3f * (fabsf(cpu_pot) + 1e-6f);
            if (diff > tol) {
                if (errors < 10) {
                    fprintf(stderr, "Mismatch at grid[%d] (%d,%d,%d): "
                            "GPU=%.6f CPU=%.6f diff=%.6f\n",
                            idx, gx, gy, gz, gpu_pot, cpu_pot, diff);
                }
                errors++;
            }
        }

        if (errors > 0)
            fprintf(stderr, "FAIL: %d errors in %d verified points\n", errors, verify_n);
        else
            fprintf(stderr, "PASS\n");
    }

    HIP_CHECK(hipFree(d_atoms));
    HIP_CHECK(hipFree(d_potential));

    return 0;
}
