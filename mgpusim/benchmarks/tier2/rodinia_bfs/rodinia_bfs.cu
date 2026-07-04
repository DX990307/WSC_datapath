// rodinia_bfs.cu — Rodinia BFS benchmark (CUDA, self-contained)
//
// Breadth-First Search on a randomly-generated CSR (Compressed Sparse Row)
// graph. Two kernels per BFS level:
//   1. bfs_kernel  — for each frontier node, write cost to unvisited neighbors
//   2. bfs_update  — promote newly-discovered nodes into the next frontier
//
// Native CUDA implementation.
//
// Usage:
//   ./rodinia_bfs [--num_nodes N] [--edges_per_node E] [--block_size B]
//
//   --num_nodes N       Number of graph nodes            (default: 65536)
//   --edges_per_node E  Out-degree of each node          (default: 20)
//   --block_size B      GPU thread-block size            (default: 256)
//
// Output (stdout): CSV row —
//   kernel_name,problem_size,iterations,avg_ms,min_ms,max_ms,stddev_ms
// Output (stderr): Effective bandwidth in GB/s

#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <cmath>
#include <cfloat>
#include <vector>
#include <cuda_runtime.h>

// ---------------------------------------------------------------------------
// Inlined bench_common_cuda.h utilities
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

struct BenchResult {
    const char* kernel_name;
    const char* problem_size;
    int         iterations;
    double      avg_ms;
    double      min_ms;
    double      max_ms;
    double      stddev_ms;
};


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


struct BenchmarkTimer {
    cudaEvent_t start, stop;
    BenchmarkTimer() {
        CUDA_CHECK(cudaEventCreate(&start));
        CUDA_CHECK(cudaEventCreate(&stop));
    }
    ~BenchmarkTimer() {
        (void)cudaEventDestroy(start);
        (void)cudaEventDestroy(stop);
    }
    void record_start(cudaStream_t stream = 0) {
        CUDA_CHECK(cudaEventRecord(start, stream));
    }
    void record_stop(cudaStream_t stream = 0) {
        CUDA_CHECK(cudaEventRecord(stop, stream));
        CUDA_CHECK(cudaEventSynchronize(stop));
    }
    float elapsed_ms() const {
        float ms = 0.0f;
        CUDA_CHECK(cudaEventElapsedTime(&ms, start, stop));
        return ms;
    }
};

template <typename Func>
static BenchResult runBenchmark(const char* kernel_name,
                                const char* problem_size,
                                int         iterations,
                                int         num_warmup,
                                Func        func) {
    BenchmarkTimer timer;

    // Warmup
    for (int w = 0; w < num_warmup; ++w) {
        func();
        CUDA_CHECK(cudaDeviceSynchronize());
    }

    std::vector<double> times(1);
    for (int i = 0; i < 1; ++i) {
        timer.record_start();
        func();
        timer.record_stop();
        times[i] = static_cast<double>(timer.elapsed_ms());
    }

    double sum = 0.0, mn = DBL_MAX, mx = 0.0;
    for (int i = 0; i < 1; ++i) {
        sum += times[i];
        if (times[i] < mn) mn = times[i];
        if (times[i] > mx) mx = times[i];
    }
    double avg = sum / 1;

    double variance = 0.0;
    for (int i = 0; i < 1; ++i) {
        double d = times[i] - avg;
        variance += d * d;
    }
    double stddev = (1 > 1) ? sqrt(variance / (1 - 1)) : 0.0;

    BenchResult r;
    r.kernel_name  = kernel_name;
    r.problem_size = problem_size;
    r.iterations   = iterations;
    r.avg_ms       = avg;
    r.min_ms       = mn;
    r.max_ms       = mx;
    r.stddev_ms    = stddev;
    return r;
}

// ---------------------------------------------------------------------------
// BFS kernels
// ---------------------------------------------------------------------------

// Pass 1: for each frontier node, write cost+1 to unvisited neighbors
__global__ void bfs_kernel(const int* __restrict__ row_offsets,
                           const int* __restrict__ col_indices,
                           int*       cost,
                           const int* frontier,
                           int        num_nodes) {
    int tid = blockIdx.x * blockDim.x + threadIdx.x;
    if (tid < num_nodes && frontier[tid]) {
        int row_start = row_offsets[tid];
        int row_end   = row_offsets[tid + 1];
        int new_cost  = cost[tid] + 1;
        for (int e = row_start; e < row_end; ++e) {
            int nb = col_indices[e];
            if (cost[nb] < 0) {
                cost[nb] = new_cost;  // may race but harmless (same value)
            }
        }
    }
}

// Pass 2: promote newly-discovered nodes into next frontier; signal if any
__global__ void bfs_update(int*       frontier,
                           const int* cost,
                           int*       visited,
                           int*       updated,
                           int        num_nodes) {
    int tid = blockIdx.x * blockDim.x + threadIdx.x;
    if (tid < num_nodes) {
        int was_visited = visited[tid];
        frontier[tid] = 0;
        if (!was_visited && cost[tid] >= 0) {
            visited[tid]  = 1;
            frontier[tid] = 1;
            *updated = 1;
        }
    }
}

// ---------------------------------------------------------------------------
// Graph generation (CSR, random)
// ---------------------------------------------------------------------------

static void generate_random_graph(int num_nodes, int edges_per_node,
                                  int** h_row_offsets, int** h_col_indices,
                                  int*  num_edges_out) {
    int num_edges = num_nodes * edges_per_node;
    *h_row_offsets = (int*)malloc((size_t)(num_nodes + 1) * sizeof(int));
    *h_col_indices = (int*)malloc((size_t)num_edges * sizeof(int));

    srand(42);
    int offset = 0;
    for (int i = 0; i < num_nodes; ++i) {
        (*h_row_offsets)[i] = offset;
        for (int j = 0; j < edges_per_node; ++j) {
            (*h_col_indices)[offset + j] = rand() % num_nodes;
        }
        offset += edges_per_node;
    }
    (*h_row_offsets)[num_nodes] = offset;
    *num_edges_out = num_edges;
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------

int main(int argc, char** argv) {
    setvbuf(stdout, NULL, _IONBF, 0);

    int num_nodes      = parseIntParam(argc, argv, "--num_nodes",      65536);
    int edges_per_node = parseIntParam(argc, argv, "--edges_per_node", 20);
    int block_size     = parseIntParam(argc, argv, "--block_size",     256);

    const char* env_val;
    env_val = getenv("BENCH_PARAM_num_nodes");
    if (env_val) num_nodes = atoi(env_val);
    env_val = getenv("BENCH_PARAM_edges_per_node");
    if (env_val) edges_per_node = atoi(env_val);
    env_val = getenv("BENCH_PARAM_block_size");
    if (env_val) block_size = atoi(env_val);
    int num_warmup = 0;

    // Generate random graph
    int* h_row_offsets = nullptr;
    int* h_col_indices = nullptr;
    int  num_edges     = 0;
    generate_random_graph(num_nodes, edges_per_node,
                          &h_row_offsets, &h_col_indices, &num_edges);

    // Host arrays for BFS state
    int* h_cost     = (int*)malloc((size_t)num_nodes * sizeof(int));
    int* h_frontier = (int*)malloc((size_t)num_nodes * sizeof(int));
    int* h_visited  = (int*)malloc((size_t)num_nodes * sizeof(int));

    // Device allocations
    int *d_row_offsets, *d_col_indices;
    int *d_cost, *d_frontier, *d_visited, *d_updated;

    CUDA_CHECK(cudaMalloc(&d_row_offsets, (size_t)(num_nodes + 1) * sizeof(int)));
    CUDA_CHECK(cudaMalloc(&d_col_indices, (size_t)num_edges        * sizeof(int)));
    CUDA_CHECK(cudaMalloc(&d_cost,        (size_t)num_nodes        * sizeof(int)));
    CUDA_CHECK(cudaMalloc(&d_frontier,    (size_t)num_nodes        * sizeof(int)));
    CUDA_CHECK(cudaMalloc(&d_visited,     (size_t)num_nodes        * sizeof(int)));
    CUDA_CHECK(cudaMalloc(&d_updated,     sizeof(int)));

    // Copy graph structure to device (constant across iterations)
    CUDA_CHECK(cudaMemcpy(d_row_offsets, h_row_offsets,
                        (size_t)(num_nodes + 1) * sizeof(int),
                        cudaMemcpyHostToDevice));
    CUDA_CHECK(cudaMemcpy(d_col_indices, h_col_indices,
                        (size_t)num_edges * sizeof(int),
                        cudaMemcpyHostToDevice));

    int device_id = 0;
    CUDA_CHECK(cudaGetDevice(&device_id));
    cudaDeviceProp prop;
    CUDA_CHECK(cudaGetDeviceProperties(&prop, device_id));
    fprintf(stderr, "Device: %s (id=%d)\n", prop.name, device_id);
    fprintf(stderr, "Nodes: %d  |  Edges: %d  |  Block: %d\n\n",
            num_nodes, num_edges, block_size);

    dim3 block(block_size);
    dim3 grid((num_nodes + block_size - 1) / block_size);

    char problemSize[64];
    snprintf(problemSize, sizeof(problemSize), "%d", num_nodes);

    // Bandwidth accounting:
    //   Each BFS iteration reads row_offsets (num_nodes+1 ints),
    //   col_indices (num_edges ints), cost (num_nodes ints),
    //   frontier (num_nodes ints), visited (num_nodes ints)
    //   and writes cost (num_edges worst-case), frontier, visited, updated.
    // We estimate conservatively as 2 * (row_offsets + col_indices + 3*nodes)
    long long bytes_per_bfs =
        2LL * ((long long)(num_nodes + 1 + num_edges + 3 * num_nodes) * sizeof(int));

    BenchResult r = runBenchmark(
        "rodinia_bfs", problemSize, 1, num_warmup, [&]() {
        // Re-initialize BFS state for each timed run
        for (int i = 0; i < num_nodes; ++i) {
            h_cost[i]     = -1;
            h_frontier[i] = 0;
            h_visited[i]  = 0;
        }
        h_cost[0]     = 0;
        h_frontier[0] = 1;
        h_visited[0]  = 1;

        CUDA_CHECK(cudaMemcpy(d_cost,     h_cost,     (size_t)num_nodes * sizeof(int), cudaMemcpyHostToDevice));
        CUDA_CHECK(cudaMemcpy(d_frontier, h_frontier, (size_t)num_nodes * sizeof(int), cudaMemcpyHostToDevice));
        CUDA_CHECK(cudaMemcpy(d_visited,  h_visited,  (size_t)num_nodes * sizeof(int), cudaMemcpyHostToDevice));

        int h_updated = 1;
        while (h_updated) {
            h_updated = 0;
            CUDA_CHECK(cudaMemcpy(d_updated, &h_updated, sizeof(int), cudaMemcpyHostToDevice));

            bfs_kernel<<<grid, block, 0, 0>>>(d_row_offsets, d_col_indices, d_cost, d_frontier, num_nodes);
            CUDA_CHECK(cudaDeviceSynchronize());

            h_updated = 0;
            CUDA_CHECK(cudaMemcpy(d_updated, &h_updated, sizeof(int), cudaMemcpyHostToDevice));

            bfs_update<<<grid, block, 0, 0>>>(d_frontier, d_cost, d_visited, d_updated, num_nodes);
            CUDA_CHECK(cudaDeviceSynchronize());

            CUDA_CHECK(cudaMemcpy(&h_updated, d_updated, sizeof(int), cudaMemcpyDeviceToHost));
        }
    });

    double bw_gb = (double)bytes_per_bfs / (r.avg_ms * 1e-3) / 1e9;
    fprintf(stderr, "Effective bandwidth: %.2f GB/s  (avg %.4f ms/BFS)\n",
            bw_gb, r.avg_ms);

    // JSON-lines output
    double edges_per_sec = (double)num_edges / (r.avg_ms * 1e-3);
    printf("{\"type\":\"kernel\",\"name\":\"bfs_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"num_nodes\":%d,\"edges_per_node\":%d,\"block_size\":%d,"
           "}}\n",
           r.avg_ms, num_nodes, edges_per_node, block_size);
    printf("{\"type\":\"kernel\",\"name\":\"bfs_update\",\"time_ms\":%.6f,"
           "\"params\":{\"num_nodes\":%d,\"edges_per_node\":%d,\"block_size\":%d,"
           "}}\n",
           r.avg_ms, num_nodes, edges_per_node, block_size);
    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"bandwidth_gbps\",\"value\":%.2f},"
           "{\"name\":\"edges_per_sec\",\"value\":%.2f}]}\n",
           r.avg_ms, bw_gb, edges_per_sec);

    // Cleanup
    CUDA_CHECK(cudaFree(d_row_offsets));
    CUDA_CHECK(cudaFree(d_col_indices));
    CUDA_CHECK(cudaFree(d_cost));
    CUDA_CHECK(cudaFree(d_frontier));
    CUDA_CHECK(cudaFree(d_visited));
    CUDA_CHECK(cudaFree(d_updated));
    free(h_row_offsets);
    free(h_col_indices);
    free(h_cost);
    free(h_frontier);
    free(h_visited);

    return 0;
}
