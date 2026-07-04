/**
 * graph_tc_metal.mm — Apple Metal host for Triangle Counting benchmark.
 *
 * Counts triangles in an undirected graph using sorted adjacency list
 * intersection on the GPU. Each thread processes one directed edge (u, v)
 * with u < v and counts common neighbors.
 *
 * Usage:
 *   ./graph_tc [N=16384] [D=32]
 *
 *   N=<vertices>     Number of vertices (default: 16384)
 *   D=<avg_degree>   Average degree (default: 32)
 *
 * Output (stdout): CSV — graph_tc,<N>,<time_ms>,<triangles_per_sec>
 * Output (stderr): human-readable results
 *
 * Build:
 *   make PLATFORM=metal
 */

#import <Foundation/Foundation.h>
#import <Metal/Metal.h>

#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <cmath>
#include <vector>
#include <algorithm>
#include <set>
#include <mach/mach_time.h>

// ---------------------------------------------------------------------------
// Timing helpers
// ---------------------------------------------------------------------------

static double ticks_to_ms(uint64_t ticks) {
    static mach_timebase_info_data_t tb = {0, 0};
    if (tb.denom == 0) mach_timebase_info(&tb);
    return (double)ticks * (double)tb.numer / (double)tb.denom * 1e-6;
}

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
// CPU reference: triangle counting
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
            if (v <= u) continue;
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
    return total;
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------

int main(int argc, char* argv[]) {
    @autoreleasepool {
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
    int num_warmup = 0;

    int N = vertices;
    int D = avg_degree;

    // -------------------------------------------------------------------
    // Metal device setup
    // -------------------------------------------------------------------
    id<MTLDevice> device = MTLCreateSystemDefaultDevice();
    if (!device) {
        fprintf(stderr, "Error: no Metal device found\n");
        return EXIT_FAILURE;
    }
    fprintf(stderr, "Device: %s\n", device.name.UTF8String);
    fprintf(stderr, "Triangle Counting  |  Vertices: %d  |  Avg Degree: %d  |  "
            "Iterations: %d warmup + 5 timed\n\n", N, D, num_warmup);

    id<MTLCommandQueue> queue = [device newCommandQueue];

    // -------------------------------------------------------------------
    // Compile Metal shader
    // -------------------------------------------------------------------
    NSError* err = nil;
    NSString* exePath = [[NSBundle mainBundle] executablePath];
    NSString* dir     = [exePath stringByDeletingLastPathComponent];
    NSString* srcPath = [dir stringByAppendingPathComponent:@"graph_tc.metal"];

    id<MTLLibrary> library = nil;
    if ([[NSFileManager defaultManager] fileExistsAtPath:srcPath]) {
        NSString* metalSrc = [NSString stringWithContentsOfFile:srcPath
                                                       encoding:NSUTF8StringEncoding
                                                          error:nil];
        MTLCompileOptions* opts = [MTLCompileOptions new];
        library = [device newLibraryWithSource:metalSrc options:opts error:&err];
        if (!library) {
            fprintf(stderr, "Metal compile error: %s\n",
                    err.localizedDescription.UTF8String);
            return EXIT_FAILURE;
        }
    } else {
        fprintf(stderr, "Error: graph_tc.metal not found at %s\n",
                srcPath.UTF8String);
        return EXIT_FAILURE;
    }

    id<MTLFunction> fnTC = [library newFunctionWithName:@"triangle_count_kernel"];
    if (!fnTC) {
        fprintf(stderr, "Error: kernel function not found\n");
        return EXIT_FAILURE;
    }

    id<MTLComputePipelineState> psoTC =
        [device newComputePipelineStateWithFunction:fnTC error:&err];
    if (!psoTC) {
        fprintf(stderr, "Error creating pipeline: %s\n",
                err.localizedDescription.UTF8String);
        return EXIT_FAILURE;
    }

    // -------------------------------------------------------------------
    // Generate graph
    // -------------------------------------------------------------------
    fprintf(stderr, "Generating random graph...\n");
    std::vector<int> row_ptr, col_idx;
    generate_graph(N, D, row_ptr, col_idx);
    int num_nnz = (int)col_idx.size();
    fprintf(stderr, "Graph: %d vertices, %d directed edges (CSR nnz)\n", N, num_nnz);

    // Build edge list (u < v only)
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

    // -------------------------------------------------------------------
    // Allocate Metal buffers
    // -------------------------------------------------------------------
    id<MTLBuffer> bufRowPtr = [device newBufferWithBytes:row_ptr.data()
                                                  length:(N + 1) * sizeof(int)
                                                 options:MTLResourceStorageModeShared];
    id<MTLBuffer> bufColIdx = [device newBufferWithBytes:col_idx.data()
                                                  length:num_nnz * sizeof(int)
                                                 options:MTLResourceStorageModeShared];
    id<MTLBuffer> bufEdgeCounts = [device newBufferWithLength:num_edges * sizeof(int)
                                                      options:MTLResourceStorageModeShared];
    id<MTLBuffer> bufEdgeSrc = [device newBufferWithBytes:edge_src.data()
                                                   length:num_edges * sizeof(int)
                                                  options:MTLResourceStorageModeShared];
    id<MTLBuffer> bufEdgeDst = [device newBufferWithBytes:edge_dst.data()
                                                   length:num_edges * sizeof(int)
                                                  options:MTLResourceStorageModeShared];

    NSUInteger tgSize = (NSUInteger)block_size;

    auto run_tc = [&]() {
        memset(bufEdgeCounts.contents, 0, num_edges * sizeof(int));

        uint32_t ne = (uint32_t)num_edges;

        id<MTLCommandBuffer> cb = [queue commandBuffer];
        id<MTLComputeCommandEncoder> enc = [cb computeCommandEncoder];

        [enc setComputePipelineState:psoTC];
        [enc setBuffer:bufRowPtr     offset:0 atIndex:0];
        [enc setBuffer:bufColIdx     offset:0 atIndex:1];
        [enc setBuffer:bufEdgeCounts offset:0 atIndex:2];
        [enc setBuffer:bufEdgeSrc    offset:0 atIndex:3];
        [enc setBuffer:bufEdgeDst    offset:0 atIndex:4];
        [enc setBytes:&ne length:sizeof(ne) atIndex:5];
        [enc dispatchThreads:MTLSizeMake((NSUInteger)num_edges, 1, 1)
            threadsPerThreadgroup:MTLSizeMake(tgSize, 1, 1)];

        [enc endEncoding];
        [cb commit];
        [cb waitUntilCompleted];
    };

    // -------------------------------------------------------------------
    // Warmup
    // -------------------------------------------------------------------
    for (int w = 0; w < num_warmup; ++w) {
        run_tc();
    }

    // -------------------------------------------------------------------
    // Timed iterations
    // -------------------------------------------------------------------
    std::vector<double> times(1);
    for (int i = 0; i < 1; ++i) {
        uint64_t t0 = mach_absolute_time();
        run_tc();
        uint64_t t1 = mach_absolute_time();
        times[i] = ticks_to_ms(t1 - t0);
    }

    // Sum up triangle counts
    int* h_counts = (int*)bufEdgeCounts.contents;
    long long gpu_total = 0;
    for (int i = 0; i < num_edges; ++i) {
        gpu_total += (long long)h_counts[i];
    }
    long long num_triangles = gpu_total / 3;

    double sum = 0.0;
    for (int i = 0; i < 1; ++i) sum += times[i];
    double avg_ms = sum / 1;

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
    // Verification
    // -------------------------------------------------------------------
    if (strcmp(verify, "skip") != 0) {
        fprintf(stderr, "Running CPU verification...\n");
        long long cpu_raw = triangle_count_cpu(row_ptr, col_idx, N);
        long long cpu_triangles = cpu_raw / 3;

        if (num_triangles == cpu_triangles) {
            fprintf(stderr, "PASS (CPU=%lld, GPU=%lld triangles)\n",
                    cpu_triangles, num_triangles);
        } else {
            fprintf(stderr, "FAIL (CPU=%lld, GPU=%lld triangles)\n",
                    cpu_triangles, num_triangles);
        }
    }

    return EXIT_SUCCESS;

    } // @autoreleasepool
}
