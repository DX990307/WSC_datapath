// graph_cc.hip — Graph Connected Components via label propagation (HIP)
//
// Computes connected components on a random undirected sparse graph using
// iterative label propagation. The graph is stored in CSR format. Each vertex
// starts with its own ID as label and iteratively updates to the minimum label
// among itself and its neighbors until convergence.
//
// Compiles with hipcc (AMD ROCm) or nvcc via HIP compatibility layer (CUDA).
//
// Output (stdout): JSON-lines protocol
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
#include <algorithm>

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
// Simple deterministic PRNG (xorshift32)
// ---------------------------------------------------------------------------

static uint32_t xorshift32(uint32_t& state) {
    state ^= state << 13;
    state ^= state >> 17;
    state ^= state << 5;
    return state;
}

// ---------------------------------------------------------------------------
// Generate random undirected graph in CSR format
// ---------------------------------------------------------------------------

static void generate_csr_graph(int N, int avg_degree, uint32_t seed,
                                std::vector<int>& row_offsets,
                                std::vector<int>& col_indices) {
    uint32_t rng = seed;

    std::vector<std::vector<int>> adj(N);

    for (int u = 0; u < N; ++u) {
        int d = avg_degree / 2 + (int)(xorshift32(rng) % (avg_degree + 1));
        if (d < 1) d = 1;
        for (int e = 0; e < d; ++e) {
            int v = (int)(xorshift32(rng) % (uint32_t)N);
            if (v != u) {
                adj[u].push_back(v);
                adj[v].push_back(u);
            }
        }
    }

    row_offsets.resize(N + 1);
    row_offsets[0] = 0;
    for (int u = 0; u < N; ++u) {
        std::sort(adj[u].begin(), adj[u].end());
        adj[u].erase(std::unique(adj[u].begin(), adj[u].end()), adj[u].end());
        row_offsets[u + 1] = row_offsets[u] + (int)adj[u].size();
    }

    int total_edges = row_offsets[N];
    col_indices.resize(total_edges);

    for (int u = 0; u < N; ++u) {
        int offset = row_offsets[u];
        for (int i = 0; i < (int)adj[u].size(); ++i) {
            col_indices[offset + i] = adj[u][i];
        }
    }
}

// ---------------------------------------------------------------------------
// Kernel: CC label propagation
// ---------------------------------------------------------------------------

__global__ void cc_propagate_kernel(const int* __restrict__ row_offsets,
                                     const int* __restrict__ col_indices,
                                     int* __restrict__ labels,
                                     int* __restrict__ changed,
                                     int N) {
    int v = blockIdx.x * blockDim.x + threadIdx.x;
    if (v >= N) return;

    int my_label = labels[v];
    int new_label = my_label;

    int start = row_offsets[v];
    int end   = row_offsets[v + 1];

    for (int e = start; e < end; ++e) {
        int u = col_indices[e];
        int neighbor_label = labels[u];
        if (neighbor_label < new_label) {
            new_label = neighbor_label;
        }
    }

    if (new_label < my_label) {
        labels[v] = new_label;
        *changed = 1;
    }
}

// ---------------------------------------------------------------------------
// CPU reference: CC via label propagation
// ---------------------------------------------------------------------------

static void cc_cpu(int N, int max_iters,
                   const std::vector<int>& row_offsets,
                   const std::vector<int>& col_indices,
                   std::vector<int>& labels) {
    labels.resize(N);
    for (int i = 0; i < N; ++i) labels[i] = i;

    for (int iter = 0; iter < max_iters; ++iter) {
        bool any_changed = false;
        for (int v = 0; v < N; ++v) {
            int new_label = labels[v];
            for (int e = row_offsets[v]; e < row_offsets[v + 1]; ++e) {
                int u = col_indices[e];
                if (labels[u] < new_label) {
                    new_label = labels[u];
                }
            }
            if (new_label < labels[v]) {
                labels[v] = new_label;
                any_changed = true;
            }
        }
        if (!any_changed) break;
    }
}

// ---------------------------------------------------------------------------
// GPU CC: run propagation until convergence or max_iters
// ---------------------------------------------------------------------------

static int run_cc_gpu(int N, int max_iters, int block_size,
                      int* d_row_offsets, int* d_col_indices,
                      int* d_labels, int* d_changed) {
    std::vector<int> h_init(N);
    for (int i = 0; i < N; ++i) h_init[i] = i;
    HIP_CHECK(hipMemcpy(d_labels, h_init.data(), N * sizeof(int), hipMemcpyHostToDevice));

    int gridSize = (N + block_size - 1) / block_size;
    int h_changed = 0;
    int iters_done = 0;

    for (int iter = 0; iter < max_iters; ++iter) {
        h_changed = 0;
        HIP_CHECK(hipMemcpy(d_changed, &h_changed, sizeof(int), hipMemcpyHostToDevice));

        hipLaunchKernelGGL(cc_propagate_kernel, dim3(gridSize), dim3(block_size),
                           0, 0, d_row_offsets, d_col_indices, d_labels, d_changed, N);
        HIP_CHECK(hipDeviceSynchronize());

        HIP_CHECK(hipMemcpy(&h_changed, d_changed, sizeof(int), hipMemcpyDeviceToHost));
        iters_done = iter + 1;
        if (h_changed == 0) break;
    }

    return iters_done;
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------

int main(int argc, char** argv) {
    setvbuf(stdout, NULL, _IONBF, 0);

    // Defaults from params.json
    int num_vertices   = 100000;
    int avg_degree     = 16;
    int max_iterations = 100;
    int block_size     = 256;
    int iterations     = 5;

    // Environment variable overrides
    const char* env_val;
    env_val = getenv("BENCH_PARAM_num_vertices");
    if (env_val) num_vertices = atoi(env_val);
    env_val = getenv("BENCH_PARAM_avg_degree");
    if (env_val) avg_degree = atoi(env_val);
    env_val = getenv("BENCH_PARAM_max_iterations");
    if (env_val) max_iterations = atoi(env_val);
    env_val = getenv("BENCH_PARAM_block_size");
    if (env_val) block_size = atoi(env_val);
    env_val = getenv("BENCH_PARAM_iterations");
    if (env_val) iterations = atoi(env_val);
    int num_warmup = 5;

    int N     = num_vertices;
    int iters = iterations;

    // Generate CSR graph
    std::vector<int> row_offsets, col_indices;
    generate_csr_graph(N, avg_degree, 42, row_offsets, col_indices);
    int num_edges = row_offsets[N];

    // Print device info
    int device_id = 0;
    HIP_CHECK(hipGetDevice(&device_id));
    hipDeviceProp_t prop;
    HIP_CHECK(hipGetDeviceProperties(&prop, device_id));
    fprintf(stderr, "Device: %s (id=%d)\n", prop.name, device_id);
    fprintf(stderr, "Connected Components (label propagation)  |  "
            "Vertices: %d  |  Edges: %d  |  Avg degree: %d  |  "
            "Max iterations: %d  |  "
            "Benchmark iterations: 5 warmup + %d timed\n\n",
            N, num_edges, avg_degree, max_iterations, iters);

    // Allocate device memory
    int *d_row_offsets, *d_col_indices, *d_labels, *d_changed;
    HIP_CHECK(hipMalloc(&d_row_offsets, (N + 1) * sizeof(int)));
    HIP_CHECK(hipMalloc(&d_col_indices, num_edges * sizeof(int)));
    HIP_CHECK(hipMalloc(&d_labels, N * sizeof(int)));
    HIP_CHECK(hipMalloc(&d_changed, sizeof(int)));

    HIP_CHECK(hipMemcpy(d_row_offsets, row_offsets.data(), (N + 1) * sizeof(int), hipMemcpyHostToDevice));
    HIP_CHECK(hipMemcpy(d_col_indices, col_indices.data(), num_edges * sizeof(int), hipMemcpyHostToDevice));

    // -------------------------------------------------------------------
    // Warmup
    // -------------------------------------------------------------------
    for (int w = 0; w < num_warmup; ++w) {
        run_cc_gpu(N, max_iterations, block_size,
                   d_row_offsets, d_col_indices, d_labels, d_changed);
    }

    // -------------------------------------------------------------------
    // Timed iterations
    // -------------------------------------------------------------------
    hipEvent_t evStart, evStop;
    HIP_CHECK(hipEventCreate(&evStart));
    HIP_CHECK(hipEventCreate(&evStop));

    std::vector<double> times((size_t)iters);
    int total_cc_iters = 0;
    for (int i = 0; i < iters; ++i) {
        HIP_CHECK(hipEventRecord(evStart, 0));
        int cc_iters = run_cc_gpu(N, max_iterations, block_size,
                                   d_row_offsets, d_col_indices, d_labels, d_changed);
        HIP_CHECK(hipEventRecord(evStop, 0));
        HIP_CHECK(hipEventSynchronize(evStop));

        float ms = 0.0f;
        HIP_CHECK(hipEventElapsedTime(&ms, evStart, evStop));
        times[i] = (double)ms;
        total_cc_iters += cc_iters;
    }

    HIP_CHECK(hipEventDestroy(evStart));
    HIP_CHECK(hipEventDestroy(evStop));

    double sum = 0.0;
    for (int i = 0; i < iters; ++i) sum += times[i];
    double avg_ms = sum / iters;
    double avg_cc_iters = (double)total_cc_iters / iters;

    double mteps = (double)num_edges * avg_cc_iters / (avg_ms * 1e-3) / 1e6;

    double total_ms = sum;

    // JSON-lines kernel event
    printf("{\"type\":\"kernel\",\"name\":\"cc_propagate_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"num_vertices\":%d,\"avg_degree\":%d,"
           "\"max_iterations\":%d,\"block_size\":%d,\"iterations\":%d}}\n",
           avg_ms, num_vertices, avg_degree, max_iterations, block_size, iterations);

    // JSON-lines summary event
    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"mteps\",\"value\":%.2f},"
           "{\"name\":\"avg_cc_iterations\",\"value\":%.1f}]}\n",
           total_ms, mteps, avg_cc_iters);

    fprintf(stderr, "Average time: %.4f ms\n", avg_ms);
    fprintf(stderr, "Avg CC iterations: %.1f\n", avg_cc_iters);
    fprintf(stderr, "Throughput:   %.2f MTEPS\n", mteps);

    // -------------------------------------------------------------------
    // Verification
    // -------------------------------------------------------------------
    {
        run_cc_gpu(N, max_iterations, block_size,
                   d_row_offsets, d_col_indices, d_labels, d_changed);

        std::vector<int> h_gpu_labels(N);
        HIP_CHECK(hipMemcpy(h_gpu_labels.data(), d_labels, N * sizeof(int), hipMemcpyDeviceToHost));

        std::vector<int> h_cpu_labels;
        cc_cpu(N, max_iterations, row_offsets, col_indices, h_cpu_labels);

        int verify_n = (N < 1024) ? N : 1024;
        int errors = 0;

        for (int i = 0; i < verify_n; ++i) {
            if (h_gpu_labels[i] != h_cpu_labels[i]) {
                if (errors < 10) {
                    fprintf(stderr, "Mismatch at vertex %d: GPU=%d CPU=%d\n",
                            i, h_gpu_labels[i], h_cpu_labels[i]);
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
    HIP_CHECK(hipFree(d_labels));
    HIP_CHECK(hipFree(d_changed));

    return 0;
}
