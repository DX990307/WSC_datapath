// graph_cc.cu — Graph Connected Components via label propagation benchmark
//
// Computes connected components on a random undirected sparse graph using
// iterative label propagation. The graph is stored in CSR format. Each vertex
// starts with its own ID as label and iteratively updates to the minimum label
// among itself and its neighbors until convergence.
//
// Native CUDA implementation.
//
// Output (stdout): JSON-lines protocol
// Output (stderr): human-readable results

#include <cstdio>
#include <cstdint>
#include <cstdlib>
#include <cstring>
#include <cmath>
#include <vector>
#include <algorithm>
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

    // Generate adjacency lists (undirected: add both directions)
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

    // Build CSR
    row_offsets.resize(N + 1);
    row_offsets[0] = 0;
    for (int u = 0; u < N; ++u) {
        // Sort and deduplicate neighbors
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
// Kernel: CC label propagation — each thread propagates min label to neighbors
// Uses "hooking" approach: for each vertex, set label to min of own label
// and all neighbor labels.
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
// GPU CC: run propagation iterations until convergence or max_iters
// Returns number of iterations performed.
// ---------------------------------------------------------------------------

static int run_cc_gpu(int N, int max_iters, int block_size,
                      int* d_row_offsets, int* d_col_indices,
                      int* d_labels, int* d_changed) {
    // Initialize labels: each vertex = its own ID
    std::vector<int> h_init(N);
    for (int i = 0; i < N; ++i) h_init[i] = i;
    CUDA_CHECK(cudaMemcpy(d_labels, h_init.data(), N * sizeof(int), cudaMemcpyHostToDevice));

    int gridSize = (N + block_size - 1) / block_size;
    int h_changed = 0;
    int iters_done = 0;

    for (int iter = 0; iter < max_iters; ++iter) {
        // Reset changed flag
        h_changed = 0;
        CUDA_CHECK(cudaMemcpy(d_changed, &h_changed, sizeof(int), cudaMemcpyHostToDevice));

        cc_propagate_kernel<<<dim3(gridSize), dim3(block_size), 0, 0>>>(
            d_row_offsets, d_col_indices, d_labels, d_changed, N);
        CUDA_CHECK(cudaDeviceSynchronize());

        // Check convergence
        CUDA_CHECK(cudaMemcpy(&h_changed, d_changed, sizeof(int), cudaMemcpyDeviceToHost));
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
    int num_warmup = 0;

    int N     = num_vertices;
    int iters = iterations;

    // Generate CSR graph
    std::vector<int> row_offsets, col_indices;
    generate_csr_graph(N, avg_degree, 42, row_offsets, col_indices);
    int num_edges = row_offsets[N];

    // Print device info
    int device_id = 0;
    CUDA_CHECK(cudaGetDevice(&device_id));
    cudaDeviceProp prop;
    CUDA_CHECK(cudaGetDeviceProperties(&prop, device_id));
    fprintf(stderr, "Device: %s (id=%d)\n", prop.name, device_id);
    fprintf(stderr, "Connected Components (label propagation)  |  "
            "Vertices: %d  |  Edges: %d  |  Avg degree: %d  |  "
            "Max iterations: %d  |  "
            "Benchmark iterations: %d warmup + %d timed\n\n",
            N, num_edges, avg_degree, max_iterations, num_warmup, iters);

    // Allocate device memory
    int *d_row_offsets, *d_col_indices, *d_labels, *d_changed;
    CUDA_CHECK(cudaMalloc(&d_row_offsets, (N + 1) * sizeof(int)));
    CUDA_CHECK(cudaMalloc(&d_col_indices, num_edges * sizeof(int)));
    CUDA_CHECK(cudaMalloc(&d_labels, N * sizeof(int)));
    CUDA_CHECK(cudaMalloc(&d_changed, sizeof(int)));

    // Copy graph structure to device
    CUDA_CHECK(cudaMemcpy(d_row_offsets, row_offsets.data(), (N + 1) * sizeof(int), cudaMemcpyHostToDevice));
    CUDA_CHECK(cudaMemcpy(d_col_indices, col_indices.data(), num_edges * sizeof(int), cudaMemcpyHostToDevice));

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
    cudaEvent_t evStart, evStop;
    CUDA_CHECK(cudaEventCreate(&evStart));
    CUDA_CHECK(cudaEventCreate(&evStop));

    std::vector<double> times((size_t)iters);
    int total_cc_iters = 0;
    for (int i = 0; i < iters; ++i) {
        CUDA_CHECK(cudaEventRecord(evStart, 0));
        int cc_iters = run_cc_gpu(N, max_iterations, block_size,
                                   d_row_offsets, d_col_indices, d_labels, d_changed);
        CUDA_CHECK(cudaEventRecord(evStop, 0));
        CUDA_CHECK(cudaEventSynchronize(evStop));

        float ms = 0.0f;
        CUDA_CHECK(cudaEventElapsedTime(&ms, evStart, evStop));
        times[i] = (double)ms;
        total_cc_iters += cc_iters;
    }

    CUDA_CHECK(cudaEventDestroy(evStart));
    CUDA_CHECK(cudaEventDestroy(evStop));

    // Compute average
    double sum = 0.0;
    for (int i = 0; i < iters; ++i) sum += times[i];
    double avg_ms = sum / iters;
    double avg_cc_iters = (double)total_cc_iters / iters;

    // MTEPS = N_edges * avg_cc_iters / time_s / 1e6
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

    // Human-readable output to stderr
    fprintf(stderr, "Average time: %.4f ms\n", avg_ms);
    fprintf(stderr, "Avg CC iterations: %.1f\n", avg_cc_iters);
    fprintf(stderr, "Throughput:   %.2f MTEPS\n", mteps);

    // -------------------------------------------------------------------
    // Verification (compare GPU result with CPU reference)
    // -------------------------------------------------------------------
    {
        // Run one more time on GPU
        run_cc_gpu(N, max_iterations, block_size,
                   d_row_offsets, d_col_indices, d_labels, d_changed);

        std::vector<int> h_gpu_labels(N);
        CUDA_CHECK(cudaMemcpy(h_gpu_labels.data(), d_labels, N * sizeof(int), cudaMemcpyDeviceToHost));

        std::vector<int> h_cpu_labels;
        cc_cpu(N, max_iterations, row_offsets, col_indices, h_cpu_labels);

        // For CC, labels may differ but connected components structure must match.
        // We verify by checking that for each pair of vertices in the same CPU
        // component, they're also in the same GPU component and vice versa.
        int verify_n = (N < 1024) ? N : 1024;
        int errors = 0;

        // Simple check: labels should match since we use the same algorithm
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

    CUDA_CHECK(cudaFree(d_row_offsets));
    CUDA_CHECK(cudaFree(d_col_indices));
    CUDA_CHECK(cudaFree(d_labels));
    CUDA_CHECK(cudaFree(d_changed));

    return 0;
}
