// rodinia_lavamd.hip — Rodinia LavaMD benchmark (HIP, self-contained)
//
// Short-range molecular dynamics with cell-list decomposition.
// Computes Lennard-Jones type particle interactions within neighboring cells
// (26 neighbors + self = 27 cells per box) on an NxNxN grid of boxes.
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
#define hipMemcpyDeviceToDevice cudaMemcpyDeviceToDevice
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
// LJ potential constants
// ---------------------------------------------------------------------------
#define LJ_A   2.0f
#define LJ_B   1.0f
#define BOX_SIZE 10.0f

// ---------------------------------------------------------------------------
// LavaMD kernel
// ---------------------------------------------------------------------------
__global__ void lavamd_kernel(
    const float* __restrict__ pos_x,
    const float* __restrict__ pos_y,
    const float* __restrict__ pos_z,
    float* __restrict__ force_x,
    float* __restrict__ force_y,
    float* __restrict__ force_z,
    float* __restrict__ energy_out,
    const int*   __restrict__ neighbor_list,
    const int*   __restrict__ neighbor_count,
    int particles_per_box,
    int total_boxes)
{
    int box_id = blockIdx.x;
    if (box_id >= total_boxes) return;

    int tid = threadIdx.x;
    int base_i = box_id * particles_per_box;

    for (int p = tid; p < particles_per_box; p += blockDim.x) {
        int i = base_i + p;
        float px = pos_x[i];
        float py = pos_y[i];
        float pz = pos_z[i];

        float fx = 0.0f, fy = 0.0f, fz = 0.0f;
        float pe = 0.0f;

        int n_neighbors = neighbor_count[box_id];

        for (int n = 0; n < n_neighbors; ++n) {
            int nbox = neighbor_list[box_id * 27 + n];
            int base_j = nbox * particles_per_box;

            for (int q = 0; q < particles_per_box; ++q) {
                int j = base_j + q;

                float dx = px - pos_x[j];
                float dy = py - pos_y[j];
                float dz = pz - pos_z[j];

                float r2 = dx * dx + dy * dy + dz * dz;

                if (r2 > 1e-10f) {
                    float r2inv = 1.0f / r2;
                    float r6inv = r2inv * r2inv * r2inv;

                    float force = r2inv * r6inv * (LJ_A * r6inv - LJ_B);
                    pe += r6inv * (LJ_A * r6inv - LJ_B);

                    fx += force * dx;
                    fy += force * dy;
                    fz += force * dz;
                }
            }
        }

        force_x[i]    = fx;
        force_y[i]    = fy;
        force_z[i]    = fz;
        energy_out[i] = pe;
    }
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

    int num_boxes         = 10;
    int block_size        = 128;
    int particles_per_box = 100;

    num_boxes         = parseIntParam(argc, argv, "--num_boxes", num_boxes);
    block_size        = parseIntParam(argc, argv, "--block_size", block_size);
    particles_per_box = parseIntParam(argc, argv, "--particles_per_box", particles_per_box);

    const char* env_val;
    env_val = getenv("BENCH_PARAM_num_boxes");
    if (env_val) num_boxes = atoi(env_val);
    env_val = getenv("BENCH_PARAM_block_size");
    if (env_val) block_size = atoi(env_val);
    env_val = getenv("BENCH_PARAM_particles_per_box");
    if (env_val) particles_per_box = atoi(env_val);
    int num_warmup = 5;

    int nb = num_boxes;
    int total_boxes     = nb * nb * nb;
    long long total_particles = (long long)total_boxes * particles_per_box;

    int device_id = 0;
    HIP_CHECK(hipGetDevice(&device_id));
    hipDeviceProp_t prop;
    HIP_CHECK(hipGetDeviceProperties(&prop, device_id));
    fprintf(stderr, "Device: %s (id=%d)\n", prop.name, device_id);
    fprintf(stderr, "Boxes: %d×%d×%d (%d total)  |  Particles/box: %d  |  "
            "Iterations: %d warmup + %d timed\n\n",
            nb, nb, nb, total_boxes, particles_per_box, num_warmup);

    size_t pos_bytes    = (size_t)total_particles * sizeof(float);
    size_t force_bytes  = (size_t)total_particles * sizeof(float);
    size_t neigh_bytes  = (size_t)total_boxes * 27 * sizeof(int);
    size_t ncount_bytes = (size_t)total_boxes * sizeof(int);

    std::vector<float> h_pos_x(total_particles);
    std::vector<float> h_pos_y(total_particles);
    std::vector<float> h_pos_z(total_particles);
    std::vector<int>   h_neighbor_list(total_boxes * 27, 0);
    std::vector<int>   h_neighbor_count(total_boxes, 0);

    unsigned int seed = 42;

    for (int bz = 0; bz < nb; ++bz) {
        for (int by = 0; by < nb; ++by) {
            for (int bx = 0; bx < nb; ++bx) {
                int box_id = bz * nb * nb + by * nb + bx;
                int base = box_id * particles_per_box;

                for (int p = 0; p < particles_per_box; ++p) {
                    h_pos_x[base + p] = bx * BOX_SIZE + rand_float(&seed, 0.5f, BOX_SIZE - 0.5f);
                    h_pos_y[base + p] = by * BOX_SIZE + rand_float(&seed, 0.5f, BOX_SIZE - 0.5f);
                    h_pos_z[base + p] = bz * BOX_SIZE + rand_float(&seed, 0.5f, BOX_SIZE - 0.5f);
                }

                int count = 0;
                for (int dz = -1; dz <= 1; ++dz) {
                    for (int dy = -1; dy <= 1; ++dy) {
                        for (int dx = -1; dx <= 1; ++dx) {
                            int nnx = bx + dx;
                            int nny = by + dy;
                            int nnz = bz + dz;
                            if (nnx >= 0 && nnx < nb && nny >= 0 && nny < nb && nnz >= 0 && nnz < nb) {
                                int nid = nnz * nb * nb + nny * nb + nnx;
                                h_neighbor_list[box_id * 27 + count] = nid;
                                count++;
                            }
                        }
                    }
                }
                h_neighbor_count[box_id] = count;
            }
        }
    }

    float *d_pos_x, *d_pos_y, *d_pos_z;
    float *d_force_x, *d_force_y, *d_force_z, *d_energy;
    int   *d_neighbor_list, *d_neighbor_count;

    HIP_CHECK(hipMalloc(&d_pos_x,          pos_bytes));
    HIP_CHECK(hipMalloc(&d_pos_y,          pos_bytes));
    HIP_CHECK(hipMalloc(&d_pos_z,          pos_bytes));
    HIP_CHECK(hipMalloc(&d_force_x,        force_bytes));
    HIP_CHECK(hipMalloc(&d_force_y,        force_bytes));
    HIP_CHECK(hipMalloc(&d_force_z,        force_bytes));
    HIP_CHECK(hipMalloc(&d_energy,         force_bytes));
    HIP_CHECK(hipMalloc(&d_neighbor_list,  neigh_bytes));
    HIP_CHECK(hipMalloc(&d_neighbor_count, ncount_bytes));

    HIP_CHECK(hipMemcpy(d_pos_x, h_pos_x.data(), pos_bytes, hipMemcpyHostToDevice));
    HIP_CHECK(hipMemcpy(d_pos_y, h_pos_y.data(), pos_bytes, hipMemcpyHostToDevice));
    HIP_CHECK(hipMemcpy(d_pos_z, h_pos_z.data(), pos_bytes, hipMemcpyHostToDevice));
    HIP_CHECK(hipMemcpy(d_neighbor_list,  h_neighbor_list.data(),  neigh_bytes,  hipMemcpyHostToDevice));
    HIP_CHECK(hipMemcpy(d_neighbor_count, h_neighbor_count.data(), ncount_bytes, hipMemcpyHostToDevice));

    dim3 grid(total_boxes);
    dim3 block(block_size);

    for (int w = 0; w < num_warmup; ++w) {
        HIP_CHECK(hipMemset(d_force_x, 0, force_bytes));
        HIP_CHECK(hipMemset(d_force_y, 0, force_bytes));
        HIP_CHECK(hipMemset(d_force_z, 0, force_bytes));
        HIP_CHECK(hipMemset(d_energy,  0, force_bytes));

        hipLaunchKernelGGL(lavamd_kernel, grid, block, 0, 0,
            d_pos_x, d_pos_y, d_pos_z,
            d_force_x, d_force_y, d_force_z, d_energy,
            d_neighbor_list, d_neighbor_count,
            particles_per_box, total_boxes);
        HIP_CHECK(hipDeviceSynchronize());
    }

    hipEvent_t evStart, evStop;
    HIP_CHECK(hipEventCreate(&evStart));
    HIP_CHECK(hipEventCreate(&evStop));

    std::vector<double> times(1);
    for (int i = 0; i < 1; ++i) {
        HIP_CHECK(hipMemset(d_force_x, 0, force_bytes));
        HIP_CHECK(hipMemset(d_force_y, 0, force_bytes));
        HIP_CHECK(hipMemset(d_force_z, 0, force_bytes));
        HIP_CHECK(hipMemset(d_energy,  0, force_bytes));

        HIP_CHECK(hipEventRecord(evStart, 0));
        hipLaunchKernelGGL(lavamd_kernel, grid, block, 0, 0,
            d_pos_x, d_pos_y, d_pos_z,
            d_force_x, d_force_y, d_force_z, d_energy,
            d_neighbor_list, d_neighbor_count,
            particles_per_box, total_boxes);
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

    double avg_neighbors = 0;
    for (int i = 0; i < total_boxes; ++i) avg_neighbors += h_neighbor_count[i];
    avg_neighbors /= total_boxes;
    double flops = (double)total_particles * avg_neighbors * particles_per_box * 20.0;
    double gflops = flops / (avg_ms * 1e-3) / 1e9;

    fprintf(stderr, "Average time: %.4f ms  |  Throughput: %.2f GFLOPS\n", avg_ms, gflops);

    printf("{\"type\":\"kernel\",\"name\":\"lavamd_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"num_boxes\":%d,\"block_size\":%d,"
           "\"particles_per_box\":%d}}\n",
           avg_ms, num_boxes, block_size, particles_per_box);

    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"gflops\",\"value\":%.2f}]}\n",
           total_ms, gflops);

    HIP_CHECK(hipFree(d_pos_x));
    HIP_CHECK(hipFree(d_pos_y));
    HIP_CHECK(hipFree(d_pos_z));
    HIP_CHECK(hipFree(d_force_x));
    HIP_CHECK(hipFree(d_force_y));
    HIP_CHECK(hipFree(d_force_z));
    HIP_CHECK(hipFree(d_energy));
    HIP_CHECK(hipFree(d_neighbor_list));
    HIP_CHECK(hipFree(d_neighbor_count));

    return 0;
}
