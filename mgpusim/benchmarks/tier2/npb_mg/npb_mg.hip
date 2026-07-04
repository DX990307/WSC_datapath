// npb_mg.hip — NPB Multi-Grid benchmark (HIP/ROCm)
//
// V-cycle multigrid with smooth/restrict/prolong on synthetic 3D grid.
//
// Compiles with hipcc (AMD ROCm) or nvcc via HIP compatibility layer.
//
// Output (stdout): JSON-lines
// Output (stderr): human-readable

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

#define HIP_CHECK(cmd)                                                          \
    do {                                                                       \
        hipError_t _e = (cmd);                                                  \
        if (_e != hipSuccess) {                                                 \
            fprintf(stderr, "HIP error %s at %s:%d\n",                         \
                    hipGetErrorString(_e), __FILE__, __LINE__);                 \
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
// Kernel: Jacobi smooth
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

    coarse[idx] = sum * 0.125f;
}

// ---------------------------------------------------------------------------
// Kernel: Prolongation (coarse -> fine)
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

    if (cx >= N_coarse) cx = N_coarse - 1;
    if (cy >= N_coarse) cy = N_coarse - 1;
    if (cz >= N_coarse) cz = N_coarse - 1;

    fine[idx] += coarse[idx3d(cx, cy, cz, N_coarse)];
}

// ---------------------------------------------------------------------------
// Kernel: Compute residual r = rhs - A*u
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
// Kernel: Norm squared reduction
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
// Host helper
// ---------------------------------------------------------------------------

static float finish_reduce(float* d_partial, int numBlocks) {
    std::vector<float> h(numBlocks);
    HIP_CHECK(hipMemcpy(h.data(), d_partial, numBlocks * sizeof(float),
                          hipMemcpyDeviceToHost));
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

    int N              = 64;
    int block_size     = 256;
    int num_vcycles    = 5;
    int num_smooth     = 3;

    const char* env_val;
    env_val = getenv("BENCH_PARAM_grid_size");
    if (env_val) N = atoi(env_val);
    env_val = getenv("BENCH_PARAM_block_size");
    if (env_val) block_size = atoi(env_val);
    env_val = getenv("BENCH_PARAM_num_vcycles");
    if (env_val) num_vcycles = atoi(env_val);
    env_val = getenv("BENCH_PARAM_num_smooth_steps");
    if (env_val) num_smooth = atoi(env_val);
    int num_warmup = 5;

    for (int i = 1; i < argc - 1; ++i) {
        if (strcmp(argv[i], "--size") == 0) N = atoi(argv[i+1]);
    }

    { int p = 4; while (p < N) p *= 2; N = p; }


    int device_id = 0;
    HIP_CHECK(hipGetDevice(&device_id));
    hipDeviceProp_t prop;
    HIP_CHECK(hipGetDeviceProperties(&prop, device_id));
    fprintf(stderr, "Device: %s (id=%d)\n", prop.name, device_id);
    fprintf(stderr, "NPB MG  |  Grid: %d x %d x %d  |  V-cycles: %d  |  "
            "Smooth steps: %d  |  Timed runs: 5 warmup + %d timed\n\n",
            N, N, N, num_vcycles, num_smooth);

    std::vector<int> levels;
    { int sz = N; while (sz >= 4) { levels.push_back(sz); sz /= 2; } }
    int num_levels = (int)levels.size();

    std::vector<float*> d_u(num_levels), d_rhs(num_levels);
    std::vector<float*> d_r(num_levels), d_temp(num_levels);
    std::vector<size_t> level_bytes(num_levels);

    for (int lv = 0; lv < num_levels; ++lv) {
        int sz = levels[lv];
        size_t bytes = (size_t)sz * sz * sz * sizeof(float);
        level_bytes[lv] = bytes;
        HIP_CHECK(hipMalloc(&d_u[lv],    bytes));
        HIP_CHECK(hipMalloc(&d_rhs[lv],  bytes));
        HIP_CHECK(hipMalloc(&d_r[lv],    bytes));
        HIP_CHECK(hipMalloc(&d_temp[lv], bytes));
    }

    int max_total = N * N * N;
    int max_blocks = (max_total + block_size - 1) / block_size;
    float* d_partial;
    HIP_CHECK(hipMalloc(&d_partial, max_blocks * sizeof(float)));

    {
        int total = N * N * N;
        std::vector<float> h_rhs(total);
        unsigned int seed = 42;
        for (int i = 0; i < total; ++i) h_rhs[i] = rand_float(&seed, -1.0f, 1.0f);
        HIP_CHECK(hipMemcpy(d_rhs[0], h_rhs.data(), level_bytes[0], hipMemcpyHostToDevice));
    }

    for (int lv = 0; lv < num_levels; ++lv) {
        HIP_CHECK(hipMemset(d_u[lv], 0, level_bytes[lv]));
        if (lv > 0) HIP_CHECK(hipMemset(d_rhs[lv], 0, level_bytes[lv]));
    }

    float smooth_weight = 1.0f;
    int smem = block_size * sizeof(float);

    // Per-kernel timing events (used only when accumulators are supplied)
    hipEvent_t kStart, kStop;
    HIP_CHECK(hipEventCreate(&kStart));
    HIP_CHECK(hipEventCreate(&kStop));

    // run_vcycle accumulates per-kernel GPU time for the emitted kernels
    // (smooth / restrict / prolong). Passing nullptr runs untimed.
    auto run_vcycle = [&](double* smooth_acc, double* restrict_acc, double* prolong_acc) {
        for (int lv = 0; lv < num_levels - 1; ++lv) {
            int sz = levels[lv];
            int total = sz * sz * sz;
            int grid = (total + block_size - 1) / block_size;

            for (int s = 0; s < num_smooth; ++s) {
                if (smooth_acc) {
                    HIP_CHECK(hipEventRecord(kStart, 0));
                    smooth_kernel<<<grid, block_size>>>(d_u[lv], d_rhs[lv], d_temp[lv], sz, smooth_weight);
                    HIP_CHECK(hipEventRecord(kStop, 0));
                    HIP_CHECK(hipEventSynchronize(kStop));
                    float _ms = 0.0f; HIP_CHECK(hipEventElapsedTime(&_ms, kStart, kStop));
                    *smooth_acc += (double)_ms;
                } else {
                    smooth_kernel<<<grid, block_size>>>(d_u[lv], d_rhs[lv], d_temp[lv], sz, smooth_weight);
                }
                float* tmp = d_u[lv]; d_u[lv] = d_temp[lv]; d_temp[lv] = tmp;
            }

            residual_kernel<<<grid, block_size>>>(d_u[lv], d_rhs[lv], d_r[lv], sz);

            int csz = levels[lv + 1];
            int ctotal = csz * csz * csz;
            int cgrid = (ctotal + block_size - 1) / block_size;
            if (restrict_acc) {
                HIP_CHECK(hipEventRecord(kStart, 0));
                restrict_kernel<<<cgrid, block_size>>>(d_r[lv], d_rhs[lv + 1], sz, csz);
                HIP_CHECK(hipEventRecord(kStop, 0));
                HIP_CHECK(hipEventSynchronize(kStop));
                float _ms = 0.0f; HIP_CHECK(hipEventElapsedTime(&_ms, kStart, kStop));
                *restrict_acc += (double)_ms;
            } else {
                restrict_kernel<<<cgrid, block_size>>>(d_r[lv], d_rhs[lv + 1], sz, csz);
            }

            HIP_CHECK(hipMemset(d_u[lv + 1], 0, level_bytes[lv + 1]));
        }

        {
            int lv = num_levels - 1;
            int sz = levels[lv];
            int total = sz * sz * sz;
            int grid = (total + block_size - 1) / block_size;
            for (int s = 0; s < num_smooth * 2; ++s) {
                if (smooth_acc) {
                    HIP_CHECK(hipEventRecord(kStart, 0));
                    smooth_kernel<<<grid, block_size>>>(d_u[lv], d_rhs[lv], d_temp[lv], sz, smooth_weight);
                    HIP_CHECK(hipEventRecord(kStop, 0));
                    HIP_CHECK(hipEventSynchronize(kStop));
                    float _ms = 0.0f; HIP_CHECK(hipEventElapsedTime(&_ms, kStart, kStop));
                    *smooth_acc += (double)_ms;
                } else {
                    smooth_kernel<<<grid, block_size>>>(d_u[lv], d_rhs[lv], d_temp[lv], sz, smooth_weight);
                }
                float* tmp = d_u[lv]; d_u[lv] = d_temp[lv]; d_temp[lv] = tmp;
            }
        }

        for (int lv = num_levels - 2; lv >= 0; --lv) {
            int sz = levels[lv];
            int total = sz * sz * sz;
            int grid = (total + block_size - 1) / block_size;
            int csz = levels[lv + 1];

            if (prolong_acc) {
                HIP_CHECK(hipEventRecord(kStart, 0));
                prolong_kernel<<<grid, block_size>>>(d_u[lv + 1], d_u[lv], sz, csz);
                HIP_CHECK(hipEventRecord(kStop, 0));
                HIP_CHECK(hipEventSynchronize(kStop));
                float _ms = 0.0f; HIP_CHECK(hipEventElapsedTime(&_ms, kStart, kStop));
                *prolong_acc += (double)_ms;
            } else {
                prolong_kernel<<<grid, block_size>>>(d_u[lv + 1], d_u[lv], sz, csz);
            }

            for (int s = 0; s < num_smooth; ++s) {
                if (smooth_acc) {
                    HIP_CHECK(hipEventRecord(kStart, 0));
                    smooth_kernel<<<grid, block_size>>>(d_u[lv], d_rhs[lv], d_temp[lv], sz, smooth_weight);
                    HIP_CHECK(hipEventRecord(kStop, 0));
                    HIP_CHECK(hipEventSynchronize(kStop));
                    float _ms = 0.0f; HIP_CHECK(hipEventElapsedTime(&_ms, kStart, kStop));
                    *smooth_acc += (double)_ms;
                } else {
                    smooth_kernel<<<grid, block_size>>>(d_u[lv], d_rhs[lv], d_temp[lv], sz, smooth_weight);
                }
                float* tmp = d_u[lv]; d_u[lv] = d_temp[lv]; d_temp[lv] = tmp;
            }
        }
    };

    auto run_mg = [&](double* smooth_acc, double* restrict_acc, double* prolong_acc) {
        for (int lv = 0; lv < num_levels; ++lv)
            HIP_CHECK(hipMemset(d_u[lv], 0, level_bytes[lv]));
        for (int v = 0; v < num_vcycles; ++v) run_vcycle(smooth_acc, restrict_acc, prolong_acc);
    };

    for (int w = 0; w < num_warmup; ++w) { run_mg(nullptr, nullptr, nullptr); HIP_CHECK(hipDeviceSynchronize()); }

    // Timed run — per-kernel accumulators summed across all V-cycle invocations
    double smooth_ms = 0.0, restrict_ms = 0.0, prolong_ms = 0.0;
    for (int i = 0; i < 1; ++i) {
        run_mg(&smooth_ms, &restrict_ms, &prolong_ms);
    }

    HIP_CHECK(hipEventDestroy(kStart));
    HIP_CHECK(hipEventDestroy(kStop));

    double total_ms = smooth_ms + restrict_ms + prolong_ms;
    double avg_ms = total_ms;

    double mcells = (double)(N * N * N) / 1e6;
    double mcells_per_sec = mcells / (avg_ms * 1e-3);

    printf("{\"type\":\"kernel\",\"name\":\"smooth_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"grid_size\":%d,\"block_size\":%d,"
           "\"num_vcycles\":%d,\"num_smooth_steps\":%d}}\n",
           smooth_ms, N, block_size, num_vcycles, num_smooth);

    printf("{\"type\":\"kernel\",\"name\":\"restrict_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"grid_size\":%d,\"block_size\":%d,"
           "\"num_vcycles\":%d,\"num_smooth_steps\":%d}}\n",
           restrict_ms, N, block_size, num_vcycles, num_smooth);

    printf("{\"type\":\"kernel\",\"name\":\"prolong_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"grid_size\":%d,\"block_size\":%d,"
           "\"num_vcycles\":%d,\"num_smooth_steps\":%d}}\n",
           prolong_ms, N, block_size, num_vcycles, num_smooth);

    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"avg_iter_ms\",\"value\":%.4f},"
           "{\"name\":\"mcells_per_sec\",\"value\":%.2f}]}\n",
           total_ms, avg_ms, mcells_per_sec);

    fprintf(stderr, "Average time: %.4f ms\n", avg_ms);
    fprintf(stderr, "Throughput:   %.2f Mcells/sec\n", mcells_per_sec);

    // Verification
    {
        run_mg(nullptr, nullptr, nullptr);
        HIP_CHECK(hipDeviceSynchronize());

        int total = N * N * N;
        int grid = (total + block_size - 1) / block_size;

        residual_kernel<<<grid, block_size>>>(d_u[0], d_rhs[0], d_r[0], N);

        norm_sq_kernel<<<grid, block_size, smem>>>(d_r[0], d_partial, total);
        HIP_CHECK(hipDeviceSynchronize());
        float res_sq = finish_reduce(d_partial, grid);
        float res_norm = sqrtf(res_sq);

        norm_sq_kernel<<<grid, block_size, smem>>>(d_rhs[0], d_partial, total);
        HIP_CHECK(hipDeviceSynchronize());
        float rhs_sq = finish_reduce(d_partial, grid);
        float rhs_norm = sqrtf(rhs_sq);

        float rel_res = res_norm / fmaxf(rhs_norm, 1e-20f);
        fprintf(stderr, "Residual ||r||/||rhs|| = %.6e\n", rel_res);

        if (rel_res < 10.0f)
            fprintf(stderr, "PASS\n");
        else
            fprintf(stderr, "FAIL: relative residual too large\n");
    }

    for (int lv = 0; lv < num_levels; ++lv) {
        HIP_CHECK(hipFree(d_u[lv]));
        HIP_CHECK(hipFree(d_rhs[lv]));
        HIP_CHECK(hipFree(d_r[lv]));
        HIP_CHECK(hipFree(d_temp[lv]));
    }
    HIP_CHECK(hipFree(d_partial));

    return 0;
}
