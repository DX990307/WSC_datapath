// graph_pr.hip — Graph PageRank (iterative, pull-based) benchmark (HIP)
//
// Pull-based PageRank with CSC format on a random sparse graph.
// Each thread computes the new rank for one vertex by pulling from incoming
// neighbors.
//
// Compiles with hipcc (AMD ROCm) or nvcc via HIP compatibility layer (CUDA).
//
// Usage:
//   ./graph_pr [--vertices N] [--degree D] [--pr-iterations P]
//
//   --vertices N       Number of vertices (default: 131072)
//   --degree D         Average degree per vertex (default: 16)
//   --pr-iterations P  PageRank iterations per run (default: 20)
//
// Output (stdout): CSV row — graph_pr,<N_vertices>,<time_ms>,<GTEPS>
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
// Simple deterministic PRNG (xorshift32)
// ---------------------------------------------------------------------------

static uint32_t xorshift32(uint32_t& state) {
    state ^= state << 13;
    state ^= state >> 17;
    state ^= state << 5;
    return state;
}

// ---------------------------------------------------------------------------
// Generate random graph in CSC format (for pull-based PageRank)
// Also computes out-degree for each vertex.
// ---------------------------------------------------------------------------

static void generate_csc_graph(int N, int avg_degree, uint32_t seed,
                                std::vector<int>& col_offsets,
                                std::vector<int>& row_indices,
                                std::vector<int>& out_degree) {
    uint32_t rng = seed;

    // First generate edges as (src, dst) pairs
    // We generate avg_degree outgoing edges per vertex
    std::vector<std::vector<int>> incoming(N);
    out_degree.assign(N, 0);

    for (int u = 0; u < N; ++u) {
        int d = avg_degree / 2 + (int)(xorshift32(rng) % (avg_degree + 1));
        if (d < 1) d = 1;
        out_degree[u] = d;
        for (int e = 0; e < d; ++e) {
            int v = (int)(xorshift32(rng) % (uint32_t)N);
            incoming[v].push_back(u);
        }
    }

    // Build CSC from incoming lists
    col_offsets.resize(N + 1);
    col_offsets[0] = 0;
    for (int v = 0; v < N; ++v)
        col_offsets[v + 1] = col_offsets[v] + (int)incoming[v].size();

    int total_edges = col_offsets[N];
    row_indices.resize(total_edges);

    for (int v = 0; v < N; ++v) {
        int offset = col_offsets[v];
        for (int i = 0; i < (int)incoming[v].size(); ++i) {
            row_indices[offset + i] = incoming[v][i];
        }
    }
}

// ---------------------------------------------------------------------------
// Kernel: Pull-based PageRank iteration
// Each thread computes the new rank for one vertex
// ---------------------------------------------------------------------------

__global__ void pagerank_kernel(const int* __restrict__ col_offsets,
                                 const int* __restrict__ row_indices,
                                 const int* __restrict__ out_degree,
                                 const float* __restrict__ rank_in,
                                 float* __restrict__ rank_out,
                                 float damping,
                                 float base_rank,
                                 int N) {
    int v = blockIdx.x * blockDim.x + threadIdx.x;
    if (v >= N) return;

    int start = col_offsets[v];
    int end   = col_offsets[v + 1];

    float sum = 0.0f;
    for (int e = start; e < end; ++e) {
        int u = row_indices[e];
        int deg = out_degree[u];
        if (deg > 0)
            sum += rank_in[u] / (float)deg;
    }

    rank_out[v] = base_rank + damping * sum;
}

// ---------------------------------------------------------------------------
// CPU reference: PageRank
// ---------------------------------------------------------------------------

static void pagerank_cpu(int N, int pr_iters, float damping,
                          const std::vector<int>& col_offsets,
                          const std::vector<int>& row_indices,
                          const std::vector<int>& out_degree,
                          std::vector<float>& rank_out) {
    float base_rank = (1.0f - damping) / (float)N;
    std::vector<float> rank_a(N, 1.0f / (float)N);
    std::vector<float> rank_b(N);

    float* cur = rank_a.data();
    float* nxt = rank_b.data();

    for (int iter = 0; iter < pr_iters; ++iter) {
        for (int v = 0; v < N; ++v) {
            float sum = 0.0f;
            for (int e = col_offsets[v]; e < col_offsets[v + 1]; ++e) {
                int u = row_indices[e];
                if (out_degree[u] > 0)
                    sum += cur[u] / (float)out_degree[u];
            }
            nxt[v] = base_rank + damping * sum;
        }
        float* tmp = cur; cur = nxt; nxt = tmp;
    }

    rank_out.assign(cur, cur + N);
}

// ---------------------------------------------------------------------------
// GPU PageRank: run pr_iters iterations
// ---------------------------------------------------------------------------

static void run_pagerank_gpu(int N, int pr_iters, float damping,
                              int* d_col_offsets, int* d_row_indices,
                              int* d_out_degree,
                              float* d_rank_a, float* d_rank_b) {
    float base_rank = (1.0f - damping) / (float)N;

    // Initialize ranks
    std::vector<float> h_init(N, 1.0f / (float)N);
    HIP_CHECK(hipMemcpy(d_rank_a, h_init.data(), N * sizeof(float), hipMemcpyHostToDevice));

    int blockSize = 256;
    int gridSize  = (N + blockSize - 1) / blockSize;

    float* cur = d_rank_a;
    float* nxt = d_rank_b;

    for (int iter = 0; iter < pr_iters; ++iter) {
        hipLaunchKernelGGL(pagerank_kernel, dim3(gridSize), dim3(blockSize),
                           0, 0, d_col_offsets, d_row_indices, d_out_degree,
                           cur, nxt, damping, base_rank, N);
        float* tmp = cur; cur = nxt; nxt = tmp;
    }
    HIP_CHECK(hipDeviceSynchronize());

    // If final result is in d_rank_b, copy to d_rank_a
    if (cur != d_rank_a) {
        HIP_CHECK(hipMemcpy(d_rank_a, cur, N * sizeof(float), hipMemcpyDeviceToDevice));
    }
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------

int main(int argc, char** argv) {
    setvbuf(stdout, NULL, _IONBF, 0);

    // Defaults from params.json
    int vertices      = 131072;
    int degree        = 16;
    int pr_iterations = 20;

    // Command-line fallback
    vertices      = parseIntParam(argc, argv, "--vertices", vertices);
    degree        = parseIntParam(argc, argv, "--degree", degree);
    pr_iterations = parseIntParam(argc, argv, "--pr-iterations", pr_iterations);

    // Environment variable overrides
    const char* env_val;
    env_val = getenv("BENCH_PARAM_vertices");
    if (env_val) vertices = atoi(env_val);
    env_val = getenv("BENCH_PARAM_degree");
    if (env_val) degree = atoi(env_val);
    env_val = getenv("BENCH_PARAM_pr_iterations");
    if (env_val) pr_iterations = atoi(env_val);
    int num_warmup = 5;

    int N         = vertices;
    int avg_deg   = degree;
    int pr_iters  = pr_iterations;
    float damping = 0.85f;

    // Generate CSC graph
    std::vector<int> col_offsets, row_indices, out_degree;
    generate_csc_graph(N, avg_deg, 42, col_offsets, row_indices, out_degree);
    int num_edges = col_offsets[N];

    // Print device info
    int device_id = 0;
    HIP_CHECK(hipGetDevice(&device_id));
    hipDeviceProp_t prop;
    HIP_CHECK(hipGetDeviceProperties(&prop, device_id));
    fprintf(stderr, "Device: %s (id=%d)\n", prop.name, device_id);
    fprintf(stderr, "PageRank (pull-based)  |  Vertices: %d  |  Edges: %d  |  "
            "PR iterations: %d  |  Damping: %.2f  |  "
            "Iterations: 5 warmup + %d timed\n\n",
            N, num_edges, pr_iters, damping);

    // Allocate device memory
    int *d_col_offsets, *d_row_indices, *d_out_degree;
    float *d_rank_a, *d_rank_b;
    HIP_CHECK(hipMalloc(&d_col_offsets, (N + 1) * sizeof(int)));
    HIP_CHECK(hipMalloc(&d_row_indices, num_edges * sizeof(int)));
    HIP_CHECK(hipMalloc(&d_out_degree, N * sizeof(int)));
    HIP_CHECK(hipMalloc(&d_rank_a, N * sizeof(float)));
    HIP_CHECK(hipMalloc(&d_rank_b, N * sizeof(float)));

    // Copy graph structure to device
    HIP_CHECK(hipMemcpy(d_col_offsets, col_offsets.data(), (N + 1) * sizeof(int), hipMemcpyHostToDevice));
    HIP_CHECK(hipMemcpy(d_row_indices, row_indices.data(), num_edges * sizeof(int), hipMemcpyHostToDevice));
    HIP_CHECK(hipMemcpy(d_out_degree, out_degree.data(), N * sizeof(int), hipMemcpyHostToDevice));

    // -------------------------------------------------------------------
    // Warmup
    // -------------------------------------------------------------------
    for (int w = 0; w < num_warmup; ++w) {
        run_pagerank_gpu(N, pr_iters, damping, d_col_offsets, d_row_indices,
                         d_out_degree, d_rank_a, d_rank_b);
    }

    // -------------------------------------------------------------------
    // Timed iterations
    // -------------------------------------------------------------------
    hipEvent_t evStart, evStop;
    HIP_CHECK(hipEventCreate(&evStart));
    HIP_CHECK(hipEventCreate(&evStop));

    std::vector<double> times(1);
    for (int i = 0; i < 1; ++i) {
        HIP_CHECK(hipEventRecord(evStart, 0));
        run_pagerank_gpu(N, pr_iters, damping, d_col_offsets, d_row_indices,
                         d_out_degree, d_rank_a, d_rank_b);
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
    for (int i = 0; i < 1; ++i) sum += times[i];
    double avg_ms = sum / 1;

    // GTEPS = N_edges * pr_iterations / time_s / 1e9
    double gteps = (double)num_edges * (double)pr_iters / (avg_ms * 1e-3) / 1e9;

    double total_ms = sum;

    // JSON-lines kernel event
    printf("{\"type\":\"kernel\",\"name\":\"pagerank_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"vertices\":%d,\"degree\":%d,\"pr_iterations\":%d"
           "}}\n",
           avg_ms, vertices, degree, pr_iterations);

    // JSON-lines summary event
    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"gteps\",\"value\":%.2f}]}\n",
           total_ms, gteps);

    // Human-readable output to stderr
    fprintf(stderr, "Average time: %.4f ms\n", avg_ms);
    fprintf(stderr, "Throughput:   %.4f GTEPS\n", gteps);

    // -------------------------------------------------------------------
    // Verification (compare GPU result with CPU reference)
    // -------------------------------------------------------------------
    {
        std::vector<float> h_gpu_rank(N);
        HIP_CHECK(hipMemcpy(h_gpu_rank.data(), d_rank_a, N * sizeof(float), hipMemcpyDeviceToHost));

        std::vector<float> h_cpu_rank;
        pagerank_cpu(N, pr_iters, damping, col_offsets, row_indices, out_degree, h_cpu_rank);

        int verify_n = (N < 1024) ? N : 1024;
        int errors = 0;
        float max_err = 0.0f;
        for (int i = 0; i < verify_n; ++i) {
            float err = fabsf(h_gpu_rank[i] - h_cpu_rank[i]);
            if (err > max_err) max_err = err;
            if (err > 1e-4f) {
                if (errors < 10) {
                    fprintf(stderr, "Mismatch at vertex %d: GPU=%.6f CPU=%.6f (err=%.6f)\n",
                            i, h_gpu_rank[i], h_cpu_rank[i], err);
                }
                errors++;
            }
        }
        fprintf(stderr, "Max error (first %d vertices): %.6e\n", verify_n, max_err);
        if (errors > 0)
            fprintf(stderr, "FAIL: %d mismatches in first %d vertices\n",
                    errors, verify_n);
        else
            fprintf(stderr, "PASS\n");
    }

    HIP_CHECK(hipFree(d_col_offsets));
    HIP_CHECK(hipFree(d_row_indices));
    HIP_CHECK(hipFree(d_out_degree));
    HIP_CHECK(hipFree(d_rank_a));
    HIP_CHECK(hipFree(d_rank_b));

    return 0;
}
