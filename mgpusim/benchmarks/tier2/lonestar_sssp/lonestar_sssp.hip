// lonestar_sssp.hip — Single-Source Shortest Path (Bellman-Ford) benchmark (HIP)
//
// Iterative edge relaxation on a random sparse graph stored in CSR format.
// All edges are relaxed in parallel each iteration; repeats until no changes.
//
// Compiles with hipcc (AMD ROCm) or nvcc via HIP compatibility layer (CUDA).
//
// Usage:
//   ./lonestar_sssp [--vertices N] [--degree D] [--iterations I]
//
//   --vertices N     Number of vertices (default: 65536)
//   --degree D       Average degree per vertex (default: 16)
//   --iterations I   Timed iterations (default: 5)
//
// Output (stdout): CSV row — lonestar_sssp,<N_vertices>,<time_ms>,<Medges_per_sec>
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
#include <climits>

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
// Simple deterministic PRNG (xorshift32)
// ---------------------------------------------------------------------------

static uint32_t xorshift32(uint32_t& state) {
    state ^= state << 13;
    state ^= state >> 17;
    state ^= state << 5;
    return state;
}

// ---------------------------------------------------------------------------
// Generate random CSR graph
// ---------------------------------------------------------------------------

static void generate_csr_graph(int N, int avg_degree, uint32_t seed,
                                std::vector<int>& row_offsets,
                                std::vector<int>& col_indices,
                                std::vector<int>& weights) {
    uint32_t rng = seed;
    row_offsets.resize(N + 1);
    // First pass: determine edges per vertex
    std::vector<int> degrees(N);
    for (int i = 0; i < N; ++i) {
        // Poisson-like: use avg_degree +/- some variance
        int d = avg_degree / 2 + (int)(xorshift32(rng) % (avg_degree + 1));
        if (d < 1) d = 1;
        degrees[i] = d;
    }

    // Build row_offsets
    row_offsets[0] = 0;
    for (int i = 0; i < N; ++i)
        row_offsets[i + 1] = row_offsets[i] + degrees[i];

    int total_edges = row_offsets[N];
    col_indices.resize(total_edges);
    weights.resize(total_edges);

    // Fill edges
    for (int i = 0; i < N; ++i) {
        for (int e = row_offsets[i]; e < row_offsets[i + 1]; ++e) {
            col_indices[e] = (int)(xorshift32(rng) % (uint32_t)N);
            weights[e] = 1 + (int)(xorshift32(rng) % 100); // weight 1-100
        }
    }
}

// ---------------------------------------------------------------------------
// Kernel: Bellman-Ford edge relaxation
// Each thread processes one vertex's outgoing edges
// ---------------------------------------------------------------------------

__global__ void bellman_ford_kernel(const int* __restrict__ row_offsets,
                                    const int* __restrict__ col_indices,
                                    const int* __restrict__ weights,
                                    int* __restrict__ dist,
                                    int* __restrict__ changed,
                                    int N) {
    int u = blockIdx.x * blockDim.x + threadIdx.x;
    if (u >= N) return;

    int d_u = dist[u];
    if (d_u == INT_MAX) return; // unreachable, skip

    int start = row_offsets[u];
    int end   = row_offsets[u + 1];

    for (int e = start; e < end; ++e) {
        int v = col_indices[e];
        int w = weights[e];
        int new_dist = d_u + w;
        // Atomic min relaxation
        int old = atomicMin(&dist[v], new_dist);
        if (new_dist < old) {
            *changed = 1;
        }
    }
}

// ---------------------------------------------------------------------------
// CPU reference: Bellman-Ford
// ---------------------------------------------------------------------------

static int bellman_ford_cpu(int N, const std::vector<int>& row_offsets,
                             const std::vector<int>& col_indices,
                             const std::vector<int>& weights,
                             std::vector<int>& dist, int source) {
    dist.assign(N, INT_MAX);
    dist[source] = 0;
    int total_iters = 0;

    for (int iter = 0; iter < N; ++iter) {
        bool updated = false;
        for (int u = 0; u < N; ++u) {
            if (dist[u] == INT_MAX) continue;
            for (int e = row_offsets[u]; e < row_offsets[u + 1]; ++e) {
                int v = col_indices[e];
                int w = weights[e];
                if (dist[u] + w < dist[v]) {
                    dist[v] = dist[u] + w;
                    updated = true;
                }
            }
        }
        total_iters++;
        if (!updated) break;
    }
    return total_iters;
}

// ---------------------------------------------------------------------------
// GPU SSSP: run Bellman-Ford iterations
// ---------------------------------------------------------------------------

static int run_sssp_gpu(int N, int num_edges,
                         int* d_row_offsets, int* d_col_indices,
                         int* d_weights, int* d_dist, int* d_changed,
                         int source, int blkSize) {
    // Initialize distances on device
    std::vector<int> h_dist(N, INT_MAX);
    h_dist[source] = 0;
    HIP_CHECK(hipMemcpy(d_dist, h_dist.data(), N * sizeof(int), hipMemcpyHostToDevice));

    int blockSize = blkSize;
    int gridSize  = (N + blockSize - 1) / blockSize;
    int max_iters = N; // upper bound
    int total_iters = 0;

    for (int iter = 0; iter < max_iters; ++iter) {
        int h_changed = 0;
        HIP_CHECK(hipMemcpy(d_changed, &h_changed, sizeof(int), hipMemcpyHostToDevice));

        hipLaunchKernelGGL(bellman_ford_kernel, dim3(gridSize), dim3(blockSize),
                           0, 0, d_row_offsets, d_col_indices, d_weights,
                           d_dist, d_changed, N);
        HIP_CHECK(hipDeviceSynchronize());

        HIP_CHECK(hipMemcpy(&h_changed, d_changed, sizeof(int), hipMemcpyDeviceToHost));
        total_iters++;
        if (!h_changed) break;
    }
    return total_iters;
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------

int main(int argc, char** argv) {
    setvbuf(stdout, NULL, _IONBF, 0);

    // Defaults from params.json
    int vertices   = 65536;
    int degree     = 16;
    int iterations = 5;
    int block_size = 256;

    // Command-line fallback
    vertices   = parseIntParam(argc, argv, "--vertices", vertices);
    degree     = parseIntParam(argc, argv, "--degree", degree);
    iterations = parseIntParam(argc, argv, "--iterations", iterations);
    block_size = parseIntParam(argc, argv, "--block_size", block_size);

    // Environment variable overrides
    const char* env_val;
    env_val = getenv("BENCH_PARAM_vertices");
    if (env_val) vertices = atoi(env_val);
    env_val = getenv("BENCH_PARAM_degree");
    if (env_val) degree = atoi(env_val);
    env_val = getenv("BENCH_PARAM_iterations");
    if (env_val) iterations = atoi(env_val);
    env_val = getenv("BENCH_PARAM_block_size");
    if (env_val) block_size = atoi(env_val);
    int num_warmup = 5;

    int N       = vertices;
    int avg_deg = degree;
    int iters   = iterations;
    int source  = 0;

    // Generate CSR graph
    std::vector<int> row_offsets, col_indices, weights;
    generate_csr_graph(N, avg_deg, 42, row_offsets, col_indices, weights);
    int num_edges = row_offsets[N];

    // Print device info
    int device_id = 0;
    HIP_CHECK(hipGetDevice(&device_id));
    hipDeviceProp_t prop;
    HIP_CHECK(hipGetDeviceProperties(&prop, device_id));
    fprintf(stderr, "Device: %s (id=%d)\n", prop.name, device_id);
    fprintf(stderr, "SSSP (Bellman-Ford)  |  Vertices: %d  |  Edges: %d  |  "
            "Iterations: %d warmup + %d timed\n\n", N, num_edges, num_warmup, iters);

    // Allocate device memory
    int *d_row_offsets, *d_col_indices, *d_weights, *d_dist, *d_changed;
    HIP_CHECK(hipMalloc(&d_row_offsets, (N + 1) * sizeof(int)));
    HIP_CHECK(hipMalloc(&d_col_indices, num_edges * sizeof(int)));
    HIP_CHECK(hipMalloc(&d_weights, num_edges * sizeof(int)));
    HIP_CHECK(hipMalloc(&d_dist, N * sizeof(int)));
    HIP_CHECK(hipMalloc(&d_changed, sizeof(int)));

    // Copy graph structure to device
    HIP_CHECK(hipMemcpy(d_row_offsets, row_offsets.data(), (N + 1) * sizeof(int), hipMemcpyHostToDevice));
    HIP_CHECK(hipMemcpy(d_col_indices, col_indices.data(), num_edges * sizeof(int), hipMemcpyHostToDevice));
    HIP_CHECK(hipMemcpy(d_weights, weights.data(), num_edges * sizeof(int), hipMemcpyHostToDevice));

    // -------------------------------------------------------------------
    // Warmup
    // -------------------------------------------------------------------
    int bf_iters = 0;
    for (int w = 0; w < num_warmup; ++w) {
        bf_iters = run_sssp_gpu(N, num_edges, d_row_offsets, d_col_indices,
                                d_weights, d_dist, d_changed, source, block_size);
    }
    fprintf(stderr, "Bellman-Ford converged in %d iterations\n", bf_iters);

    // -------------------------------------------------------------------
    // Timed iterations
    // -------------------------------------------------------------------
    hipEvent_t evStart, evStop;
    HIP_CHECK(hipEventCreate(&evStart));
    HIP_CHECK(hipEventCreate(&evStop));

    std::vector<double> times((size_t)iters);
    std::vector<int> iter_counts((size_t)iters);
    for (int i = 0; i < iters; ++i) {
        HIP_CHECK(hipEventRecord(evStart, 0));
        iter_counts[i] = run_sssp_gpu(N, num_edges, d_row_offsets, d_col_indices,
                                       d_weights, d_dist, d_changed, source, block_size);
        HIP_CHECK(hipEventRecord(evStop, 0));
        HIP_CHECK(hipEventSynchronize(evStop));

        float ms = 0.0f;
        HIP_CHECK(hipEventElapsedTime(&ms, evStart, evStop));
        times[i] = (double)ms;
    }

    HIP_CHECK(hipEventDestroy(evStart));
    HIP_CHECK(hipEventDestroy(evStop));

    // Compute average
    double sum = 0.0;
    int total_bf_iters = 0;
    for (int i = 0; i < iters; ++i) {
        sum += times[i];
        total_bf_iters += iter_counts[i];
    }
    double avg_ms = sum / iters;
    double avg_bf_iters = (double)total_bf_iters / iters;

    // Medges/sec = N_edges * bf_iterations / time_s / 1e6
    double medges_per_sec = (double)num_edges * avg_bf_iters / (avg_ms * 1e-3) / 1e6;

    double total_ms = sum;

    // JSON-lines kernel event
    printf("{\"type\":\"kernel\",\"name\":\"bellman_ford_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"vertices\":%d,\"degree\":%d,\"iterations\":%d,"
           "\"block_size\":%d}}\n",
           avg_ms, vertices, degree, iterations, block_size);

    // JSON-lines summary event
    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"medges_per_sec\",\"value\":%.2f}]}\n",
           total_ms, medges_per_sec);

    // Human-readable output to stderr
    fprintf(stderr, "Average time: %.4f ms\n", avg_ms);
    fprintf(stderr, "BF iterations (avg): %.1f\n", avg_bf_iters);
    fprintf(stderr, "Throughput:   %.4f Medges/sec\n", medges_per_sec);

    // -------------------------------------------------------------------
    // Verification (compare GPU result with CPU reference)
    // -------------------------------------------------------------------
    {
        std::vector<int> h_gpu_dist(N);
        HIP_CHECK(hipMemcpy(h_gpu_dist.data(), d_dist, N * sizeof(int), hipMemcpyDeviceToHost));

        std::vector<int> h_cpu_dist;
        bellman_ford_cpu(N, row_offsets, col_indices, weights, h_cpu_dist, source);

        int verify_n = (N < 1024) ? N : 1024;
        int errors = 0;
        for (int i = 0; i < verify_n; ++i) {
            if (h_gpu_dist[i] != h_cpu_dist[i]) {
                if (errors < 10) {
                    fprintf(stderr, "Mismatch at vertex %d: GPU=%d CPU=%d\n",
                            i, h_gpu_dist[i], h_cpu_dist[i]);
                }
                errors++;
            }
        }
        if (errors > 0)
            fprintf(stderr, "FAIL: %d mismatches in first %d vertices\n",
                    errors, verify_n);
        else
            fprintf(stderr, "PASS\n");
    }

    HIP_CHECK(hipFree(d_row_offsets));
    HIP_CHECK(hipFree(d_col_indices));
    HIP_CHECK(hipFree(d_weights));
    HIP_CHECK(hipFree(d_dist));
    HIP_CHECK(hipFree(d_changed));

    return 0;
}
