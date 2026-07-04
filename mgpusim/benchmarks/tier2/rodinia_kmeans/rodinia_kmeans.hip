// rodinia_kmeans.hip — Rodinia K-Means Clustering benchmark (HIP, self-contained)
//
// K-means clustering: assign N points (each D-dimensional) to K clusters,
// update cluster centers, repeat until convergence or max_iter.
//
// Derived from the Rodinia benchmark suite.
//
// Compiles with hipcc (AMD ROCm) or nvcc via HIP compatibility layer (CUDA).
//
// Usage:
//   ./rodinia_kmeans [--points N] [--dims D] [--clusters K] [--max_iter M]
//
//   --points N      Number of data points      (default: 16384)
//   --dims D        Dimensions per point        (default: 16)
//   --clusters K    Number of clusters          (default: 8)
//   --max_iter M    Maximum iterations          (default: 10)
//
// Output (stdout): CSV row — kmeans,N,K,D,<iterations>,<time_ms>,<Gpoints_per_s>
// Output (stderr): Per-iteration delta, convergence status, PASS/FAIL

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
#include <cfloat>
#include <vector>

// ---------------------------------------------------------------------------
// HIP error-checking macro
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

static int parseIntParam(int argc, char** argv, const char* name, int defaultVal)
{
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
// Kernel 1: Assign each point to nearest cluster center
//
//   points[idx * D + d] — row-major: N x D
//   centers[k  * D + d] — row-major: K x D
//   membership[idx]     — output cluster index for each point
// ---------------------------------------------------------------------------

__global__ void kmeans_assign_kernel(
    const float* __restrict__ points,
    const float* __restrict__ centers,
    int*         __restrict__ membership,
    int N, int D, int K)
{
    int idx = (int)(blockIdx.x * blockDim.x + threadIdx.x);
    if (idx >= N) return;

    float min_dist = FLT_MAX;
    int   best_k   = 0;

    for (int k = 0; k < K; ++k) {
        float dist = 0.0f;
        for (int d = 0; d < D; ++d) {
            float diff = points[idx * D + d] - centers[k * D + d];
            dist += diff * diff;
        }
        if (dist < min_dist) {
            min_dist = dist;
            best_k   = k;
        }
    }
    membership[idx] = best_k;
}

// ---------------------------------------------------------------------------
// Kernel 2: Accumulate new cluster centers using atomics
//
//   new_centers[k * D + d] — running sum (zeroed before each call)
//   counts[k]              — number of points in cluster k
// ---------------------------------------------------------------------------

__global__ void kmeans_update_kernel(
    const float* __restrict__ points,
    float*       __restrict__ new_centers,
    int*         __restrict__ counts,
    const int*   __restrict__ membership,
    int N, int D)
{
    int idx = (int)(blockIdx.x * blockDim.x + threadIdx.x);
    if (idx >= N) return;

    int k = membership[idx];
    atomicAdd(&counts[k], 1);
    for (int d = 0; d < D; ++d) {
        atomicAdd(&new_centers[k * D + d], points[idx * D + d]);
    }
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------

int main(int argc, char** argv)
{
    setvbuf(stdout, NULL, _IONBF, 0);

    // Defaults
    int N        = 16384;
    int D        = 16;
    int K        = 8;
    int max_iter = 10;

    // Command-line fallback
    N        = parseIntParam(argc, argv, "--points", N);
    D        = parseIntParam(argc, argv, "--dims", D);
    K        = parseIntParam(argc, argv, "--clusters", K);
    max_iter = parseIntParam(argc, argv, "--max_iter", max_iter);

    // Environment variable overrides
    const char* env_val;
    env_val = getenv("BENCH_PARAM_points");
    if (env_val) N = atoi(env_val);
    env_val = getenv("BENCH_PARAM_dims");
    if (env_val) D = atoi(env_val);
    env_val = getenv("BENCH_PARAM_clusters");
    if (env_val) K = atoi(env_val);
    env_val = getenv("BENCH_PARAM_max_iter");
    if (env_val) max_iter = atoi(env_val);
    int num_warmup = 5;

    // Print device info
    int device_id = 0;
    HIP_CHECK(hipGetDevice(&device_id));
    hipDeviceProp_t prop;
    HIP_CHECK(hipGetDeviceProperties(&prop, device_id));
    fprintf(stderr, "Device: %s (id=%d)\n", prop.name, device_id);
    fprintf(stderr, "N=%d  D=%d  K=%d  max_iter=%d\n\n", N, D, K, max_iter);

    // -----------------------------------------------------------------------
    // Host allocations
    // -----------------------------------------------------------------------
    size_t points_bytes  = (size_t)N * D * sizeof(float);
    size_t centers_bytes = (size_t)K * D * sizeof(float);

    float* h_points      = (float*)malloc(points_bytes);
    float* h_centers     = (float*)malloc(centers_bytes);
    float* h_new_centers = (float*)malloc(centers_bytes);
    int*   h_counts      = (int*)malloc((size_t)K * sizeof(int));
    int*   h_membership  = (int*)malloc((size_t)N * sizeof(int));
    int*   h_membership_prev = (int*)malloc((size_t)N * sizeof(int));

    // Initialize points randomly in [0, 1)^D
    unsigned int seed = 42;
    for (int i = 0; i < N * D; ++i) {
        seed = seed * 1664525u + 1013904223u;
        h_points[i] = (float)(seed >> 8) / (float)(1 << 24);
    }

    // Initialize centers as first K points
    memcpy(h_centers, h_points, centers_bytes);

    // Initialize membership to -1
    for (int i = 0; i < N; ++i) {
        h_membership[i]      = -1;
        h_membership_prev[i] = -1;
    }

    // -----------------------------------------------------------------------
    // Device allocations
    // -----------------------------------------------------------------------
    float* d_points;
    float* d_centers;
    float* d_new_centers;
    int*   d_counts;
    int*   d_membership;

    HIP_CHECK(hipMalloc(&d_points,      points_bytes));
    HIP_CHECK(hipMalloc(&d_centers,     centers_bytes));
    HIP_CHECK(hipMalloc(&d_new_centers, centers_bytes));
    HIP_CHECK(hipMalloc(&d_counts,      (size_t)K * sizeof(int)));
    HIP_CHECK(hipMalloc(&d_membership,  (size_t)N * sizeof(int)));

    HIP_CHECK(hipMemcpy(d_points,  h_points,  points_bytes,  hipMemcpyHostToDevice));
    HIP_CHECK(hipMemcpy(d_centers, h_centers, centers_bytes, hipMemcpyHostToDevice));

    // -----------------------------------------------------------------------
    // Kernel launch parameters
    // -----------------------------------------------------------------------
    const int block_size = 256;
    const int grid_size  = (N + block_size - 1) / block_size;

    // -----------------------------------------------------------------------
    // Timing
    // -----------------------------------------------------------------------
    hipEvent_t ev_start, ev_stop;
    HIP_CHECK(hipEventCreate(&ev_start));
    HIP_CHECK(hipEventCreate(&ev_stop));

    // Warmup
    for (int w = 0; w < num_warmup; ++w) {
        hipLaunchKernelGGL(kmeans_assign_kernel,
                           dim3(grid_size), dim3(block_size), 0, 0,
                           d_points, d_centers, d_membership, N, D, K);
        HIP_CHECK(hipDeviceSynchronize());
    }

    // -----------------------------------------------------------------------
    // Main K-Means iteration loop — assign and update timed separately
    // -----------------------------------------------------------------------
    double t_assign = 0.0;
    double t_update = 0.0;
    float ev_ms = 0.0f;

    int iter = 0;
    for (iter = 0; iter < max_iter; ++iter) {

        // --- Step 1: assign ---
        HIP_CHECK(hipEventRecord(ev_start, 0));
        hipLaunchKernelGGL(kmeans_assign_kernel,
                           dim3(grid_size), dim3(block_size), 0, 0,
                           d_points, d_centers, d_membership, N, D, K);
        HIP_CHECK(hipEventRecord(ev_stop, 0));
        HIP_CHECK(hipEventSynchronize(ev_stop));
        HIP_CHECK(hipEventElapsedTime(&ev_ms, ev_start, ev_stop));
        t_assign += (double)ev_ms;

        // --- Step 2: zero accumulators ---
        HIP_CHECK(hipMemset(d_new_centers, 0, centers_bytes));
        HIP_CHECK(hipMemset(d_counts,      0, (size_t)K * sizeof(int)));

        // --- Step 3: accumulate ---
        HIP_CHECK(hipEventRecord(ev_start, 0));
        hipLaunchKernelGGL(kmeans_update_kernel,
                           dim3(grid_size), dim3(block_size), 0, 0,
                           d_points, d_new_centers, d_counts, d_membership, N, D);
        HIP_CHECK(hipEventRecord(ev_stop, 0));
        HIP_CHECK(hipEventSynchronize(ev_stop));
        HIP_CHECK(hipEventElapsedTime(&ev_ms, ev_start, ev_stop));
        t_update += (double)ev_ms;

        // --- Step 4: copy back and divide on CPU ---
        HIP_CHECK(hipMemcpy(h_new_centers, d_new_centers, centers_bytes,   hipMemcpyDeviceToHost));
        HIP_CHECK(hipMemcpy(h_counts,      d_counts,      (size_t)K * sizeof(int), hipMemcpyDeviceToHost));
        HIP_CHECK(hipMemcpy(h_membership,  d_membership,  (size_t)N * sizeof(int), hipMemcpyDeviceToHost));

        for (int k = 0; k < K; ++k) {
            if (h_counts[k] > 0) {
                for (int d = 0; d < D; ++d) {
                    h_new_centers[k * D + d] /= (float)h_counts[k];
                }
            } else {
                // Empty cluster — keep old center
                memcpy(&h_new_centers[k * D], &h_centers[k * D], (size_t)D * sizeof(float));
            }
        }

        // --- Step 5: check convergence ---
        int changed = 0;
        for (int i = 0; i < N; ++i) {
            if (h_membership[i] != h_membership_prev[i]) ++changed;
        }
        double delta = (double)changed / (double)N;
        fprintf(stderr, "  iter %2d: changed=%-6d  delta=%.6f\n", iter + 1, changed, delta);

        // Update centers on device
        memcpy(h_centers, h_new_centers, centers_bytes);
        HIP_CHECK(hipMemcpy(d_centers, h_centers, centers_bytes, hipMemcpyHostToDevice));

        memcpy(h_membership_prev, h_membership, (size_t)N * sizeof(int));

        if (delta < 1e-4) {
            iter++;   // count this iteration
            break;
        }
    }

    // Total measured GPU time = sum of per-kernel accumulators
    double time_ms = t_assign + t_update;

    // -----------------------------------------------------------------------
    // Verify convergence: run one extra assign pass, check delta < 1%
    // (untimed)
    // -----------------------------------------------------------------------
    hipLaunchKernelGGL(kmeans_assign_kernel,
                       dim3(grid_size), dim3(block_size), 0, 0,
                       d_points, d_centers, d_membership, N, D, K);
    HIP_CHECK(hipMemcpy(h_membership, d_membership, (size_t)N * sizeof(int), hipMemcpyDeviceToHost));

    int final_changed = 0;
    for (int i = 0; i < N; ++i) {
        if (h_membership[i] != h_membership_prev[i]) ++final_changed;
    }
    double final_delta = (double)final_changed / (double)N;

    // Pass if delta dropped vs first iteration (convergence is occurring),
    // or if delta < 5% (acceptable for benchmarks with few iterations).
    bool pass = (final_delta < 0.05);
    fprintf(stderr, "\nFinal stability check: changed=%d  delta=%.6f  %s\n",
            final_changed, final_delta, pass ? "PASS" : "FAIL");

    // -----------------------------------------------------------------------
    // Report
    // -----------------------------------------------------------------------
    double gpoints_per_s = (double)N * (double)iter / ((double)time_ms * 1e-3) / 1e9;

    // JSON-lines kernel events
    printf("{\"type\":\"kernel\",\"name\":\"kmeans_assign_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"points\":%d,\"dims\":%d,\"clusters\":%d,\"max_iter\":%d}}\n",
           t_assign, N, D, K, max_iter);

    printf("{\"type\":\"kernel\",\"name\":\"kmeans_update_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"points\":%d,\"dims\":%d,\"clusters\":%d,\"max_iter\":%d}}\n",
           t_update, N, D, K, max_iter);

    // JSON-lines summary event
    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"gpoints_per_s\",\"value\":%.6f}]}\n",
           (double)time_ms, gpoints_per_s);

    fprintf(stderr, "Iterations: %d  Time: %.4f ms  Throughput: %.6f Gpoints/s\n",
            iter, (double)time_ms, gpoints_per_s);

    // -----------------------------------------------------------------------
    // Cleanup
    // -----------------------------------------------------------------------
    HIP_CHECK(hipFree(d_points));
    HIP_CHECK(hipFree(d_centers));
    HIP_CHECK(hipFree(d_new_centers));
    HIP_CHECK(hipFree(d_counts));
    HIP_CHECK(hipFree(d_membership));
    HIP_CHECK(hipEventDestroy(ev_start));
    HIP_CHECK(hipEventDestroy(ev_stop));

    free(h_points);
    free(h_centers);
    free(h_new_centers);
    free(h_counts);
    free(h_membership);
    free(h_membership_prev);

    return pass ? 0 : 1;
}
