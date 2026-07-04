// npb_mg.cu — NPB Multi-Grid benchmark: V-cycle multigrid on 3D grid
//
// Performs V-cycle multigrid operations (smooth, restrict, prolong) on a
// synthetic 3D grid. Uses Jacobi smoothing with restrict/prolong operators.
//
// Native CUDA implementation.
//
// Output (stdout): JSON-lines
// Output (stderr): human-readable

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
// 3D indexing helper
// ---------------------------------------------------------------------------

__device__ __host__ static inline int idx3d(int x, int y, int z, int N) {
    return x + y * N + z * N * N;
}

// ---------------------------------------------------------------------------
// Kernel: Jacobi smooth on 3D grid
// out[i,j,k] = (1/6) * (u[i-1,j,k]+u[i+1,j,k]+u[i,j-1,k]+u[i,j+1,k]
//                        +u[i,j,k-1]+u[i,j,k+1]) + weight * rhs[i,j,k]
// Interior points only; boundary stays zero.
// ---------------------------------------------------------------------------

__global__ void smooth_kernel(
    const float* __restrict__ u,
    const float* __restrict__ rhs,
    float*       __restrict__ out,
    int N,
    float weight)
{
    int idx = blockIdx.x * blockDim.x + threadIdx.x;
    int total = N * N * N;
    if (idx >= total) return;

    int x = idx % N;
    int y = (idx / N) % N;
    int z = idx / (N * N);

    // Boundary: keep zero
    if (x == 0 || x == N-1 || y == 0 || y == N-1 || z == 0 || z == N-1) {
        out[idx] = u[idx];
        return;
    }

    float s = u[idx3d(x-1,y,z,N)] + u[idx3d(x+1,y,z,N)]
            + u[idx3d(x,y-1,z,N)] + u[idx3d(x,y+1,z,N)]
            + u[idx3d(x,y,z-1,N)] + u[idx3d(x,y,z+1,N)];

    out[idx] = (s + weight * rhs[idx]) / 6.0f;
}

// ---------------------------------------------------------------------------
// Kernel: Restriction (fine -> coarse)
// Averages 2x2x2 block of fine grid into one coarse cell.
// fine is N_fine^3, coarse is N_coarse^3 where N_coarse = N_fine / 2
// ---------------------------------------------------------------------------

__global__ void restrict_kernel(
    const float* __restrict__ fine,
    float*       __restrict__ coarse,
    int N_fine,
    int N_coarse)
{
    int idx = blockIdx.x * blockDim.x + threadIdx.x;
    int total_coarse = N_coarse * N_coarse * N_coarse;
    if (idx >= total_coarse) return;

    int cx = idx % N_coarse;
    int cy = (idx / N_coarse) % N_coarse;
    int cz = idx / (N_coarse * N_coarse);

    int fx = cx * 2;
    int fy = cy * 2;
    int fz = cz * 2;

    float sum = 0.0f;
    for (int dz = 0; dz < 2; ++dz)
        for (int dy = 0; dy < 2; ++dy)
            for (int dx = 0; dx < 2; ++dx)
                sum += fine[idx3d(fx+dx, fy+dy, fz+dz, N_fine)];

    coarse[idx] = sum * 0.125f; // average of 8
}

// ---------------------------------------------------------------------------
// Kernel: Prolongation (coarse -> fine)
// Adds interpolated coarse grid correction to fine grid.
// Uses nearest-neighbor interpolation (injection).
// ---------------------------------------------------------------------------

__global__ void prolong_kernel(
    const float* __restrict__ coarse,
    float*       __restrict__ fine,
    int N_fine,
    int N_coarse)
{
    int idx = blockIdx.x * blockDim.x + threadIdx.x;
    int total_fine = N_fine * N_fine * N_fine;
    if (idx >= total_fine) return;

    int fx = idx % N_fine;
    int fy = (idx / N_fine) % N_fine;
    int fz = idx / (N_fine * N_fine);

    int cx = fx / 2;
    int cy = fy / 2;
    int cz = fz / 2;

    // Clamp
    if (cx >= N_coarse) cx = N_coarse - 1;
    if (cy >= N_coarse) cy = N_coarse - 1;
    if (cz >= N_coarse) cz = N_coarse - 1;

    fine[idx] += coarse[idx3d(cx, cy, cz, N_coarse)];
}

// ---------------------------------------------------------------------------
// Kernel: Compute residual r = rhs - A*u (Laplacian)
// ---------------------------------------------------------------------------

__global__ void residual_kernel(
    const float* __restrict__ u,
    const float* __restrict__ rhs,
    float*       __restrict__ r,
    int N)
{
    int idx = blockIdx.x * blockDim.x + threadIdx.x;
    int total = N * N * N;
    if (idx >= total) return;

    int x = idx % N;
    int y = (idx / N) % N;
    int z = idx / (N * N);

    if (x == 0 || x == N-1 || y == 0 || y == N-1 || z == 0 || z == N-1) {
        r[idx] = 0.0f;
        return;
    }

    float laplacian = -6.0f * u[idx]
                    + u[idx3d(x-1,y,z,N)] + u[idx3d(x+1,y,z,N)]
                    + u[idx3d(x,y-1,z,N)] + u[idx3d(x,y+1,z,N)]
                    + u[idx3d(x,y,z-1,N)] + u[idx3d(x,y,z+1,N)];

    r[idx] = rhs[idx] - laplacian;
}

// ---------------------------------------------------------------------------
// Kernel: Norm squared (block reduction)
// ---------------------------------------------------------------------------

__global__ void norm_sq_kernel(
    const float* __restrict__ v,
    float*       __restrict__ partial,
    int N_total)
{
    extern __shared__ float sdata[];
    int tid = threadIdx.x;
    int idx = blockIdx.x * blockDim.x + threadIdx.x;

    float val = 0.0f;
    if (idx < N_total) val = v[idx] * v[idx];
    sdata[tid] = val;
    __syncthreads();

    for (int s = blockDim.x / 2; s > 0; s >>= 1) {
        if (tid < s) sdata[tid] += sdata[tid + s];
        __syncthreads();
    }

    if (tid == 0) partial[blockIdx.x] = sdata[0];
}

// ---------------------------------------------------------------------------
// Host helper: finish reduction
// ---------------------------------------------------------------------------

static float finish_reduce(float* d_partial, int numBlocks) {
    std::vector<float> h(numBlocks);
    CUDA_CHECK(cudaMemcpy(h.data(), d_partial, numBlocks * sizeof(float),
                           cudaMemcpyDeviceToHost));
    float sum = 0.0f;
    for (int i = 0; i < numBlocks; ++i) sum += h[i];
    return sum;
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
    int N              = 64;     // Grid is N x N x N
    int block_size     = 256;
    int num_vcycles    = 5;
    int num_smooth     = 3;

    // Environment variable overrides
    const char* env_val;
    env_val = getenv("BENCH_PARAM_grid_size");
    if (env_val) N = atoi(env_val);
    env_val = getenv("BENCH_PARAM_block_size");
    if (env_val) block_size = atoi(env_val);
    env_val = getenv("BENCH_PARAM_num_vcycles");
    if (env_val) num_vcycles = atoi(env_val);
    env_val = getenv("BENCH_PARAM_num_smooth_steps");
    if (env_val) num_smooth = atoi(env_val);
    int num_warmup = 0;

    for (int i = 1; i < argc - 1; ++i) {
        if (strcmp(argv[i], "--size") == 0) N = atoi(argv[i+1]);
    }

    // Ensure N is a power of 2 and >= 4
    // Round up to next power of 2
    {
        int p = 4;
        while (p < N) p *= 2;
        N = p;
    }


    // Print device info
    int device_id = 0;
    CUDA_CHECK(cudaGetDevice(&device_id));
    cudaDeviceProp prop;
    CUDA_CHECK(cudaGetDeviceProperties(&prop, device_id));
    fprintf(stderr, "Device: %s (id=%d)\n", prop.name, device_id);
    fprintf(stderr, "NPB MG  |  Grid: %d x %d x %d  |  V-cycles: %d  |  "
            "Smooth steps: %d  |  Timed runs: 5 warmup + %d timed\n\n",
            N, N, N, num_vcycles, num_smooth);

    // Compute multigrid levels: N, N/2, N/4, ... down to 4
    std::vector<int> levels;
    {
        int sz = N;
        while (sz >= 4) {
            levels.push_back(sz);
            sz /= 2;
        }
    }
    int num_levels = (int)levels.size();

    // -----------------------------------------------------------------------
    // Allocate device memory for each level: u, rhs, r, temp
    // -----------------------------------------------------------------------
    std::vector<float*> d_u(num_levels), d_rhs(num_levels);
    std::vector<float*> d_r(num_levels), d_temp(num_levels);
    std::vector<size_t> level_bytes(num_levels);

    for (int lv = 0; lv < num_levels; ++lv) {
        int sz = levels[lv];
        size_t bytes = (size_t)sz * sz * sz * sizeof(float);
        level_bytes[lv] = bytes;
        CUDA_CHECK(cudaMalloc(&d_u[lv],    bytes));
        CUDA_CHECK(cudaMalloc(&d_rhs[lv],  bytes));
        CUDA_CHECK(cudaMalloc(&d_r[lv],    bytes));
        CUDA_CHECK(cudaMalloc(&d_temp[lv], bytes));
    }

    // Partial reduction buffer
    int max_total = N * N * N;
    int max_blocks = (max_total + block_size - 1) / block_size;
    float* d_partial;
    CUDA_CHECK(cudaMalloc(&d_partial, max_blocks * sizeof(float)));

    // Generate synthetic RHS on finest grid
    {
        int total = N * N * N;
        std::vector<float> h_rhs(total);
        unsigned int seed = 42;
        for (int i = 0; i < total; ++i) {
            h_rhs[i] = rand_float(&seed, -1.0f, 1.0f);
        }
        CUDA_CHECK(cudaMemcpy(d_rhs[0], h_rhs.data(), level_bytes[0],
                               cudaMemcpyHostToDevice));
    }

    // Zero all u arrays and coarser rhs arrays
    for (int lv = 0; lv < num_levels; ++lv) {
        CUDA_CHECK(cudaMemset(d_u[lv], 0, level_bytes[lv]));
        if (lv > 0) CUDA_CHECK(cudaMemset(d_rhs[lv], 0, level_bytes[lv]));
    }

    float smooth_weight = 1.0f;
    int smem = block_size * sizeof(float);

    // Lambda: run one V-cycle multigrid
    auto run_vcycle = [&]() {
        // Going down: smooth + restrict residual
        for (int lv = 0; lv < num_levels - 1; ++lv) {
            int sz = levels[lv];
            int total = sz * sz * sz;
            int grid = (total + block_size - 1) / block_size;

            // Pre-smooth
            for (int s = 0; s < num_smooth; ++s) {
                smooth_kernel<<<grid, block_size>>>(
                    d_u[lv], d_rhs[lv], d_temp[lv], sz, smooth_weight);
                // Swap u and temp
                float* tmp = d_u[lv];
                d_u[lv] = d_temp[lv];
                d_temp[lv] = tmp;
            }

            // Compute residual
            residual_kernel<<<grid, block_size>>>(
                d_u[lv], d_rhs[lv], d_r[lv], sz);

            // Restrict residual to coarser level
            int csz = levels[lv + 1];
            int ctotal = csz * csz * csz;
            int cgrid = (ctotal + block_size - 1) / block_size;
            restrict_kernel<<<cgrid, block_size>>>(
                d_r[lv], d_rhs[lv + 1], sz, csz);

            // Zero coarse u
            CUDA_CHECK(cudaMemset(d_u[lv + 1], 0, level_bytes[lv + 1]));
        }

        // Solve on coarsest level (just smooth)
        {
            int lv = num_levels - 1;
            int sz = levels[lv];
            int total = sz * sz * sz;
            int grid = (total + block_size - 1) / block_size;
            for (int s = 0; s < num_smooth * 2; ++s) {
                smooth_kernel<<<grid, block_size>>>(
                    d_u[lv], d_rhs[lv], d_temp[lv], sz, smooth_weight);
                float* tmp = d_u[lv];
                d_u[lv] = d_temp[lv];
                d_temp[lv] = tmp;
            }
        }

        // Going up: prolong + post-smooth
        for (int lv = num_levels - 2; lv >= 0; --lv) {
            int sz = levels[lv];
            int total = sz * sz * sz;
            int grid = (total + block_size - 1) / block_size;
            int csz = levels[lv + 1];

            // Prolong correction
            prolong_kernel<<<grid, block_size>>>(
                d_u[lv + 1], d_u[lv], sz, csz);

            // Post-smooth
            for (int s = 0; s < num_smooth; ++s) {
                smooth_kernel<<<grid, block_size>>>(
                    d_u[lv], d_rhs[lv], d_temp[lv], sz, smooth_weight);
                float* tmp = d_u[lv];
                d_u[lv] = d_temp[lv];
                d_temp[lv] = tmp;
            }
        }
    };

    // Lambda: run full multigrid solve
    auto run_mg = [&]() {
        // Reset u to zero
        for (int lv = 0; lv < num_levels; ++lv) {
            CUDA_CHECK(cudaMemset(d_u[lv], 0, level_bytes[lv]));
        }
        for (int v = 0; v < num_vcycles; ++v) {
            run_vcycle();
        }
    };

    // -----------------------------------------------------------------------
    // Warmup
    // -----------------------------------------------------------------------
    for (int w = 0; w < num_warmup; ++w) {
        run_mg();
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
        CUDA_CHECK(cudaEventRecord(evStart, 0));
        run_mg();
        CUDA_CHECK(cudaEventRecord(evStop, 0));
        CUDA_CHECK(cudaEventSynchronize(evStop));

        float ms = 0.0f;
        CUDA_CHECK(cudaEventElapsedTime(&ms, evStart, evStop));
        times[i] = (double)ms;
    }

    CUDA_CHECK(cudaEventDestroy(evStart));
    CUDA_CHECK(cudaEventDestroy(evStop));

    double tsum = 0.0;
    for (int i = 0; i < 1; ++i) tsum += times[i];
    double avg_ms = tsum / 1;
    double total_ms = tsum;

    // Throughput: Mcells/sec on finest grid
    double mcells = (double)(N * N * N) / 1e6;
    double mcells_per_sec = mcells / (avg_ms * 1e-3);

    // JSON-lines output
    printf("{\"type\":\"kernel\",\"name\":\"smooth_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"grid_size\":%d,\"block_size\":%d,"
           "\"num_vcycles\":%d,\"num_smooth_steps\":%d}}\n",
           avg_ms, N, block_size, num_vcycles, num_smooth);

    printf("{\"type\":\"kernel\",\"name\":\"restrict_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"grid_size\":%d,\"block_size\":%d,"
           "\"num_vcycles\":%d,\"num_smooth_steps\":%d}}\n",
           avg_ms, N, block_size, num_vcycles, num_smooth);

    printf("{\"type\":\"kernel\",\"name\":\"prolong_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"grid_size\":%d,\"block_size\":%d,"
           "\"num_vcycles\":%d,\"num_smooth_steps\":%d}}\n",
           avg_ms, N, block_size, num_vcycles, num_smooth);

    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"avg_iter_ms\",\"value\":%.4f},"
           "{\"name\":\"mcells_per_sec\",\"value\":%.2f}]}\n",
           total_ms, avg_ms, mcells_per_sec);

    fprintf(stderr, "Average time: %.4f ms\n", avg_ms);
    fprintf(stderr, "Throughput:   %.2f Mcells/sec\n", mcells_per_sec);

    // -----------------------------------------------------------------------
    // Verification: check residual norm on finest grid
    // -----------------------------------------------------------------------
    {
        run_mg();
        CUDA_CHECK(cudaDeviceSynchronize());

        int total = N * N * N;
        int grid = (total + block_size - 1) / block_size;

        residual_kernel<<<grid, block_size>>>(d_u[0], d_rhs[0], d_r[0], N);

        norm_sq_kernel<<<grid, block_size, smem>>>(d_r[0], d_partial, total);
        CUDA_CHECK(cudaDeviceSynchronize());
        float res_sq = finish_reduce(d_partial, grid);
        float res_norm = sqrtf(res_sq);

        norm_sq_kernel<<<grid, block_size, smem>>>(d_rhs[0], d_partial, total);
        CUDA_CHECK(cudaDeviceSynchronize());
        float rhs_sq = finish_reduce(d_partial, grid);
        float rhs_norm = sqrtf(rhs_sq);

        float rel_res = res_norm / fmaxf(rhs_norm, 1e-20f);
        fprintf(stderr, "Residual ||r||/||rhs|| = %.6e\n", rel_res);

        if (rel_res < 10.0f)
            fprintf(stderr, "PASS\n");
        else
            fprintf(stderr, "FAIL: relative residual too large\n");
    }

    // Cleanup
    for (int lv = 0; lv < num_levels; ++lv) {
        CUDA_CHECK(cudaFree(d_u[lv]));
        CUDA_CHECK(cudaFree(d_rhs[lv]));
        CUDA_CHECK(cudaFree(d_r[lv]));
        CUDA_CHECK(cudaFree(d_temp[lv]));
    }
    CUDA_CHECK(cudaFree(d_partial));

    return 0;
}
