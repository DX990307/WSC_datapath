// graph_tc.hip — Triangle Counting benchmark (HIP)
//
// Counts triangles in an undirected graph using sorted adjacency list
// intersection. Each thread processes one edge (u, v) and counts common
// neighbors of u and v via a merge-based intersection.
//
// Compiles with hipcc (AMD ROCm) or nvcc via HIP compatibility layer (CUDA).
//
// Usage:
//   ./graph_tc [N=16384] [D=32]
//
//   N=<vertices>     Number of vertices (default: 16384)
//   D=<avg_degree>   Average degree (default: 32)
//
// Output (stdout): CSV row — graph_tc,<N>,<time_ms>,<triangles_per_sec>
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
#include <algorithm>
#include <set>

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
// Argument parsing (key=value style)
// ---------------------------------------------------------------------------

static int parseIntParam(int argc, char** argv, const char* name, int defaultVal) {
    size_t nlen = strlen(name);
    for (int i = 1; i < argc; ++i) {
        if (strncmp(argv[i], name, nlen) == 0) {
            int v = atoi(argv[i] + nlen);
            if (v > 0) return v;
        }
    }
    return defaultVal;
}

// ---------------------------------------------------------------------------
// Kernel: Count triangles via sorted-list intersection
// Each thread handles one edge (u, v) with u < v, and counts
// the number of common neighbors w with w < u (to avoid triple-counting).
// ---------------------------------------------------------------------------

__global__ void triangle_count_kernel(
    const int* __restrict__ row_ptr,
    const int* __restrict__ col_idx,
    long long* __restrict__ edge_counts,
    int num_edges,
    const int* __restrict__ edge_src,
    const int* __restrict__ edge_dst)
{
    int eid = blockIdx.x * blockDim.x + threadIdx.x;
    if (eid >= num_edges) return;

    int u = edge_src[eid];
    int v = edge_dst[eid];

    // Intersect sorted neighbor lists of u and v
    int u_start = row_ptr[u], u_end = row_ptr[u + 1];
    int v_start = row_ptr[v], v_end = row_ptr[v + 1];

    long long count = 0;
    int i = u_start, j = v_start;
    while (i < u_end && j < v_end) {
        int nu = col_idx[i];
        int nv = col_idx[j];
        if (nu == nv) {
            count++;
            i++;
            j++;
        } else if (nu < nv) {
            i++;
        } else {
            j++;
        }
    }

    edge_counts[eid] = count;
}

// ---------------------------------------------------------------------------
// CPU reference: triangle counting via sorted list intersection
// ---------------------------------------------------------------------------

static long long triangle_count_cpu(
    const std::vector<int>& row_ptr,
    const std::vector<int>& col_idx,
    int N)
{
    long long total = 0;
    for (int u = 0; u < N; ++u) {
        for (int idx = row_ptr[u]; idx < row_ptr[u + 1]; ++idx) {
            int v = col_idx[idx];
            if (v <= u) continue; // only process edges u < v
            // Intersect neighbors of u and v
            int ui = row_ptr[u], ue = row_ptr[u + 1];
            int vi = row_ptr[v], ve = row_ptr[v + 1];
            while (ui < ue && vi < ve) {
                if (col_idx[ui] == col_idx[vi]) {
                    total++;
                    ui++;
                    vi++;
                } else if (col_idx[ui] < col_idx[vi]) {
                    ui++;
                } else {
                    vi++;
                }
            }
        }
    }
    // Each triangle is counted once per edge (u,v) with u<v where w is a common neighbor.
    // But each triangle has 3 edges, so we've counted each triangle 3 times.
    // Actually, for each edge (u,v) with u<v, we count ALL common neighbors (both less than u and greater than v).
    // Each triangle (a,b,c) with a<b<c gets counted once on edge (a,b) for neighbor c,
    // once on edge (a,c) for neighbor b, and once on edge (b,c) for neighbor a.
    // So total / 3 would be wrong. Actually each triangle is counted exactly 3 times.
    // Wait, let's reconsider. For triangle (a,b,c):
    //   - Edge (a,b): common neighbor c is found => count++
    //   - Edge (a,c): common neighbor b is found => count++
    //   - Edge (b,c): common neighbor a is found => count++
    // So total counts each triangle 3 times. Divide by 3.
    // Actually no: we process only edges u<v and count ALL common neighbors.
    // Each triangle has exactly 3 directed edges u<v, and is counted once per each.
    // So divide by 3.
    // Actually that's wrong too. For triangle (a<b<c), edge (a,b) finds common neighbor c,
    // edge (a,c) finds common neighbor b, edge (b,c) finds common neighbor a.
    // That's 3 counts per triangle. So total / 3.
    // BUT WAIT: we're not dividing in the GPU version either, we return raw count.
    // Let's just return raw total and compare.
    return total;
}

// ---------------------------------------------------------------------------
// Graph generation: random undirected graph with sorted adjacency lists
// ---------------------------------------------------------------------------

static void generate_graph(int N, int avg_degree,
                           std::vector<int>& row_ptr,
                           std::vector<int>& col_idx)
{
    long long target_edges = (long long)N * avg_degree / 2;
    std::set<std::pair<int,int>> edge_set;

    srand(42);
    while ((long long)edge_set.size() < target_edges) {
        int u = rand() % N;
        int v = rand() % N;
        if (u == v) continue;
        if (u > v) std::swap(u, v);
        edge_set.insert({u, v});
    }

    // Build adjacency lists
    std::vector<std::vector<int>> adj(N);
    for (auto& e : edge_set) {
        adj[e.first].push_back(e.second);
        adj[e.second].push_back(e.first);
    }

    // Sort each adjacency list
    for (int i = 0; i < N; ++i) {
        std::sort(adj[i].begin(), adj[i].end());
    }

    // Build CSR
    row_ptr.resize(N + 1);
    row_ptr[0] = 0;
    for (int i = 0; i < N; ++i) {
        row_ptr[i + 1] = row_ptr[i] + (int)adj[i].size();
    }
    col_idx.resize(row_ptr[N]);
    for (int i = 0; i < N; ++i) {
        for (int j = 0; j < (int)adj[i].size(); ++j) {
            col_idx[row_ptr[i] + j] = adj[i][j];
        }
    }
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------

int main(int argc, char** argv) {
    setvbuf(stdout, NULL, _IONBF, 0);

    // Defaults from params.json
    int vertices   = 16384;
    int avg_degree = 32;
    int block_size = 256;
    const char* verify = "true";

    // Command-line fallback
    vertices   = parseIntParam(argc, argv, "N=", vertices);
    avg_degree = parseIntParam(argc, argv, "D=", avg_degree);

    // Environment variable overrides
    const char* env_val;
    env_val = getenv("BENCH_PARAM_vertices");
    if (env_val) vertices = atoi(env_val);
    env_val = getenv("BENCH_PARAM_avg_degree");
    if (env_val) avg_degree = atoi(env_val);
    env_val = getenv("BENCH_PARAM_block_size");
    if (env_val) block_size = atoi(env_val);
    env_val = getenv("BENCH_PARAM_verify");
    if (env_val) verify = env_val;
    int num_warmup = 5;

    int N = vertices;
    int D = avg_degree;

    // Print device info
    int device_id = 0;
    HIP_CHECK(hipGetDevice(&device_id));
    hipDeviceProp_t prop;
    HIP_CHECK(hipGetDeviceProperties(&prop, device_id));
    fprintf(stderr, "Device: %s (id=%d)\n", prop.name, device_id);
    fprintf(stderr, "Triangle Counting  |  Vertices: %d  |  Avg Degree: %d  |  "
            "Iterations: %d warmup + 5 timed\n\n", N, D, num_warmup);

    // Generate graph
    fprintf(stderr, "Generating random graph...\n");
    std::vector<int> row_ptr, col_idx;
    generate_graph(N, D, row_ptr, col_idx);
    int num_nnz = (int)col_idx.size();
    fprintf(stderr, "Graph: %d vertices, %d directed edges (CSR nnz)\n", N, num_nnz);

    // Build edge list for directed edges (u < v) only
    std::vector<int> edge_src, edge_dst;
    for (int u = 0; u < N; ++u) {
        for (int idx = row_ptr[u]; idx < row_ptr[u + 1]; ++idx) {
            int v = col_idx[idx];
            if (v > u) {
                edge_src.push_back(u);
                edge_dst.push_back(v);
            }
        }
    }
    int num_edges = (int)edge_src.size();
    fprintf(stderr, "Undirected edges (u<v): %d\n", num_edges);

    // Allocate device memory
    int* d_row_ptr;
    int* d_col_idx;
    int* d_edge_src;
    int* d_edge_dst;
    long long* d_edge_counts;

    HIP_CHECK(hipMalloc(&d_row_ptr, (N + 1) * sizeof(int)));
    HIP_CHECK(hipMalloc(&d_col_idx, num_nnz * sizeof(int)));
    HIP_CHECK(hipMalloc(&d_edge_src, num_edges * sizeof(int)));
    HIP_CHECK(hipMalloc(&d_edge_dst, num_edges * sizeof(int)));
    HIP_CHECK(hipMalloc(&d_edge_counts, num_edges * sizeof(long long)));

    HIP_CHECK(hipMemcpy(d_row_ptr, row_ptr.data(), (N + 1) * sizeof(int), hipMemcpyHostToDevice));
    HIP_CHECK(hipMemcpy(d_col_idx, col_idx.data(), num_nnz * sizeof(int), hipMemcpyHostToDevice));
    HIP_CHECK(hipMemcpy(d_edge_src, edge_src.data(), num_edges * sizeof(int), hipMemcpyHostToDevice));
    HIP_CHECK(hipMemcpy(d_edge_dst, edge_dst.data(), num_edges * sizeof(int), hipMemcpyHostToDevice));

    int blockSize = block_size;
    int gridSize = (num_edges + blockSize - 1) / blockSize;

    auto run_tc = [&]() {
        HIP_CHECK(hipMemset(d_edge_counts, 0, num_edges * sizeof(long long)));
        hipLaunchKernelGGL(triangle_count_kernel, dim3(gridSize), dim3(blockSize),
                           0, 0, d_row_ptr, d_col_idx, d_edge_counts,
                           num_edges, d_edge_src, d_edge_dst);
    };

    // -------------------------------------------------------------------
    // Warmup
    // -------------------------------------------------------------------
    for (int w = 0; w < num_warmup; ++w) {
        run_tc();
        HIP_CHECK(hipDeviceSynchronize());
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
        run_tc();
        HIP_CHECK(hipEventRecord(evStop, 0));
        HIP_CHECK(hipEventSynchronize(evStop));

        float ms = 0.0f;
        HIP_CHECK(hipEventElapsedTime(&ms, evStart, evStop));
        times[i] = (double)ms;
    }

    HIP_CHECK(hipEventDestroy(evStart));
    HIP_CHECK(hipEventDestroy(evStop));

    // Sum up triangle counts from GPU
    std::vector<long long> h_edge_counts(num_edges);
    HIP_CHECK(hipMemcpy(h_edge_counts.data(), d_edge_counts,
                         num_edges * sizeof(long long), hipMemcpyDeviceToHost));

    long long gpu_total = 0;
    for (int i = 0; i < num_edges; ++i) {
        gpu_total += h_edge_counts[i];
    }
    // Each triangle (a,b,c) with a<b<c: edge (a,b) counts c, edge (a,c) counts b,
    // edge (b,c) counts a. So each triangle is counted 3 times. But common neighbors
    // include ALL neighbors, not just those forming ordered triples.
    // Actually for edge (u,v) with u<v, we count ALL common neighbors w.
    // w could be < u, between u and v, or > v.
    // Triangle (w,u,v) is counted on edge (u,v) for neighbor w.
    // The same triangle is also counted on edge (w,u) [if w<u] for neighbor v, and on edge (w,v) for neighbor u.
    // So each triangle is counted exactly 3 times across all edges.
    long long num_triangles = gpu_total / 3;

    // Compute average time
    double sum = 0.0;
    for (int i = 0; i < 1; ++i) sum += times[i];
    double avg_ms = sum / 1;

    // triangles_per_sec = num_triangles / time_s
    double tri_per_sec = (double)num_triangles / (avg_ms * 1e-3);

    double total_ms = sum;

    // JSON-lines kernel event
    printf("{\"type\":\"kernel\",\"name\":\"triangle_count_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"vertices\":%d,\"avg_degree\":%d,\"block_size\":%d,"
           "\"verify\":\"%s\"}}\n",
           avg_ms, vertices, avg_degree, block_size, verify);

    // JSON-lines summary event
    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"triangles_per_sec\",\"value\":%.2f}]}\n",
           total_ms, tri_per_sec);

    // Human-readable output to stderr
    fprintf(stderr, "Triangles found:  %lld\n", num_triangles);
    fprintf(stderr, "Average time:     %.4f ms\n", avg_ms);
    fprintf(stderr, "Throughput:       %.4f triangles/sec\n", tri_per_sec);

    // -------------------------------------------------------------------
    // Verification: compare against CPU reference on small subset
    // -------------------------------------------------------------------
    if (strcmp(verify, "skip") != 0) {
        fprintf(stderr, "Running CPU verification...\n");
        long long cpu_raw = triangle_count_cpu(row_ptr, col_idx, N);
        long long cpu_triangles = cpu_raw / 3;

        if (num_triangles == cpu_triangles) {
            fprintf(stderr, "PASS (CPU=%lld, GPU=%lld triangles)\n",
                    cpu_triangles, num_triangles);
        } else {
            fprintf(stderr, "FAIL (CPU=%lld, GPU=%lld triangles, raw GPU=%lld, raw CPU=%lld)\n",
                    cpu_triangles, num_triangles, gpu_total, cpu_raw);
        }
    }

    HIP_CHECK(hipFree(d_row_ptr));
    HIP_CHECK(hipFree(d_col_idx));
    HIP_CHECK(hipFree(d_edge_src));
    HIP_CHECK(hipFree(d_edge_dst));
    HIP_CHECK(hipFree(d_edge_counts));

    return 0;
}
