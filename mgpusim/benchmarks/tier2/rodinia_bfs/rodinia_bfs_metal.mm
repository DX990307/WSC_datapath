/**
 * rodinia_bfs_metal.mm — Apple Metal host for the Rodinia BFS benchmark.
 *
 * Iterative Breadth-First Search on a randomly-generated CSR graph.
 * Two Metal compute kernels per BFS level:
 *   1. bfs_kernel  — propagate costs from frontier nodes to their neighbors
 *   2. bfs_update  — build the next frontier from newly-discovered nodes
 *
 * Usage:
 *   ./rodinia_bfs [--num_nodes N] [--edges_per_node E]
 *
 *   --num_nodes N       Number of graph nodes         (default: 65536)
 *   --edges_per_node E  Out-degree per node            (default: 20)
 *   --iterations I      Timed benchmark iterations    (default: 20)
 *
 * Output (stdout): CSV — kernel_name,problem_size,iterations,avg_ms,min_ms,max_ms,stddev_ms
 * Output (stderr): Effective bandwidth in GB/s
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
#include <cfloat>
#include <vector>
#include <mach/mach_time.h>

// ---------------------------------------------------------------------------
// Timing helpers
// ---------------------------------------------------------------------------

static double ticks_to_ms(uint64_t ticks)
{
    static mach_timebase_info_data_t tb = {0, 0};
    if (tb.denom == 0) mach_timebase_info(&tb);
    return (double)ticks * (double)tb.numer / (double)tb.denom * 1e-6;
}

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
// CSV output helpers
// ---------------------------------------------------------------------------

struct BenchResult {
    const char* kernel_name;
    const char* problem_size;
    int         iterations;
    double      avg_ms;
    double      min_ms;
    double      max_ms;
    double      stddev_ms;
};


// ---------------------------------------------------------------------------
// Graph generation (CSR, random)
// ---------------------------------------------------------------------------

static void generate_random_graph(int num_nodes, int edges_per_node,
                                  std::vector<int>& row_offsets,
                                  std::vector<int>& col_indices)
{
    int num_edges = num_nodes * edges_per_node;
    row_offsets.resize((size_t)(num_nodes + 1));
    col_indices.resize((size_t)num_edges);

    srand(42);
    int offset = 0;
    for (int i = 0; i < num_nodes; ++i) {
        row_offsets[i] = offset;
        for (int j = 0; j < edges_per_node; ++j) {
            col_indices[offset + j] = rand() % num_nodes;
        }
        offset += edges_per_node;
    }
    row_offsets[num_nodes] = offset;
}

// ---------------------------------------------------------------------------
// Run one complete BFS from node 0 and return wall time in ms
// ---------------------------------------------------------------------------

static double run_bfs_once(id<MTLDevice>               device,
                           id<MTLCommandQueue>          queue,
                           id<MTLComputePipelineState>  pso_kernel,
                           id<MTLComputePipelineState>  pso_update,
                           id<MTLBuffer>                buf_row_offsets,
                           id<MTLBuffer>                buf_col_indices,
                           id<MTLBuffer>                buf_cost,
                           id<MTLBuffer>                buf_frontier,
                           id<MTLBuffer>                buf_visited,
                           id<MTLBuffer>                buf_updated,
                           id<MTLBuffer>                buf_num_nodes,
                           int                          num_nodes,
                           NSUInteger                   threads_per_tg)
{
    // Re-initialise BFS state on CPU side (shared memory)
    int* cost    = (int*)buf_cost.contents;
    int* frontier = (int*)buf_frontier.contents;
    int* visited  = (int*)buf_visited.contents;
    for (int i = 0; i < num_nodes; ++i) {
        cost[i]     = -1;
        frontier[i] = 0;
        visited[i]  = 0;
    }
    cost[0]     = 0;
    frontier[0] = 1;
    visited[0]  = 1;

    int h_updated = 1;
    *(int*)buf_updated.contents = 0;

    NSUInteger total_threads = (NSUInteger)(((num_nodes + (int)threads_per_tg - 1)
                                            / (int)threads_per_tg) * (int)threads_per_tg);

    uint64_t t0 = mach_absolute_time();

    while (h_updated) {
        // ---- Pass 1: bfs_kernel ----
        h_updated = 0;
        *(int*)buf_updated.contents = 0;

        {
            id<MTLCommandBuffer>         cb  = [queue commandBuffer];
            id<MTLComputeCommandEncoder> enc = [cb computeCommandEncoder];
            [enc setComputePipelineState:pso_kernel];
            [enc setBuffer:buf_row_offsets offset:0 atIndex:0];
            [enc setBuffer:buf_col_indices offset:0 atIndex:1];
            [enc setBuffer:buf_cost        offset:0 atIndex:2];
            [enc setBuffer:buf_frontier    offset:0 atIndex:3];
            [enc setBuffer:buf_num_nodes   offset:0 atIndex:4];
            [enc dispatchThreads:MTLSizeMake(total_threads, 1, 1)
                threadsPerThreadgroup:MTLSizeMake(threads_per_tg, 1, 1)];
            [enc endEncoding];
            [cb commit];
            [cb waitUntilCompleted];
        }

        // ---- Pass 2: bfs_update ----
        *(int*)buf_updated.contents = 0;

        {
            id<MTLCommandBuffer>         cb  = [queue commandBuffer];
            id<MTLComputeCommandEncoder> enc = [cb computeCommandEncoder];
            [enc setComputePipelineState:pso_update];
            [enc setBuffer:buf_frontier  offset:0 atIndex:0];
            [enc setBuffer:buf_cost      offset:0 atIndex:1];
            [enc setBuffer:buf_visited   offset:0 atIndex:2];
            [enc setBuffer:buf_updated   offset:0 atIndex:3];
            [enc setBuffer:buf_num_nodes offset:0 atIndex:4];
            [enc dispatchThreads:MTLSizeMake(total_threads, 1, 1)
                threadsPerThreadgroup:MTLSizeMake(threads_per_tg, 1, 1)];
            [enc endEncoding];
            [cb commit];
            [cb waitUntilCompleted];
        }

        h_updated = *(int*)buf_updated.contents;
    }

    uint64_t t1 = mach_absolute_time();
    return ticks_to_ms(t1 - t0);
}

// ---------------------------------------------------------------------------
// main
// ---------------------------------------------------------------------------

int main(int argc, char* argv[])
{
    @autoreleasepool {
    setvbuf(stdout, NULL, _IONBF, 0);

    int num_nodes      = parseIntParam(argc, argv, "--num_nodes",      65536);
    int edges_per_node = parseIntParam(argc, argv, "--edges_per_node", 20);
    int block_size     = 256;

    const char* env_val;
    env_val = getenv("BENCH_PARAM_num_nodes");
    if (env_val) num_nodes = atoi(env_val);
    env_val = getenv("BENCH_PARAM_edges_per_node");
    if (env_val) edges_per_node = atoi(env_val);
    env_val = getenv("BENCH_PARAM_block_size");
    if (env_val) block_size = atoi(env_val);
    int num_warmup = 0;

    // Generate random CSR graph
    std::vector<int> row_offsets, col_indices;
    generate_random_graph(num_nodes, edges_per_node, row_offsets, col_indices);
    int num_edges = (int)col_indices.size();

    // -------------------------------------------------------------------
    // Metal device setup
    // -------------------------------------------------------------------
    id<MTLDevice> device = MTLCreateSystemDefaultDevice();
    if (!device) {
        fprintf(stderr, "Error: no Metal device found\n");
        return EXIT_FAILURE;
    }
    fprintf(stderr, "Device: %s\n", device.name.UTF8String);
    fprintf(stderr, "Nodes: %d  |  Edges: %d\n\n",
            num_nodes, num_edges);

    id<MTLCommandQueue> queue = [device newCommandQueue];

    // -------------------------------------------------------------------
    // Load Metal shader from source file (next to binary)
    // -------------------------------------------------------------------
    NSError *err = nil;

    NSString *exePath = [[NSBundle mainBundle] executablePath];
    NSString *dir     = [exePath stringByDeletingLastPathComponent];
    NSString *srcPath = [dir stringByAppendingPathComponent:@"rodinia_bfs.metal"];

    id<MTLLibrary> library = nil;
    if ([[NSFileManager defaultManager] fileExistsAtPath:srcPath]) {
        NSString *metalSrc = [NSString stringWithContentsOfFile:srcPath
                                                       encoding:NSUTF8StringEncoding
                                                          error:nil];
        MTLCompileOptions *opts = [MTLCompileOptions new];
        library = [device newLibraryWithSource:metalSrc options:opts error:&err];
        if (!library) {
            fprintf(stderr, "Metal compile error: %s\n",
                    err.localizedDescription.UTF8String);
            return EXIT_FAILURE;
        }
    } else {
        fprintf(stderr, "Error: rodinia_bfs.metal not found at %s\n",
                srcPath.UTF8String);
        return EXIT_FAILURE;
    }

    id<MTLFunction> fn_kernel = [library newFunctionWithName:@"bfs_kernel"];
    id<MTLFunction> fn_update = [library newFunctionWithName:@"bfs_update"];
    if (!fn_kernel || !fn_update) {
        fprintf(stderr, "Error: BFS kernel functions not found in Metal library\n");
        return EXIT_FAILURE;
    }

    id<MTLComputePipelineState> pso_kernel =
        [device newComputePipelineStateWithFunction:fn_kernel error:&err];
    id<MTLComputePipelineState> pso_update =
        [device newComputePipelineStateWithFunction:fn_update error:&err];
    if (!pso_kernel || !pso_update) {
        fprintf(stderr, "Error creating pipeline: %s\n",
                err.localizedDescription.UTF8String);
        return EXIT_FAILURE;
    }

    NSUInteger threads_per_tg = MIN((NSUInteger)256,
                                    pso_kernel.maxTotalThreadsPerThreadgroup);

    // -------------------------------------------------------------------
    // Allocate shared Metal buffers
    // -------------------------------------------------------------------
    size_t row_bytes  = (size_t)(num_nodes + 1) * sizeof(int);
    size_t edge_bytes = (size_t)num_edges        * sizeof(int);
    size_t node_bytes = (size_t)num_nodes        * sizeof(int);

    id<MTLBuffer> buf_row_offsets = [device newBufferWithLength:row_bytes
                                                        options:MTLResourceStorageModeShared];
    id<MTLBuffer> buf_col_indices = [device newBufferWithLength:edge_bytes
                                                        options:MTLResourceStorageModeShared];
    id<MTLBuffer> buf_cost        = [device newBufferWithLength:node_bytes
                                                        options:MTLResourceStorageModeShared];
    id<MTLBuffer> buf_frontier    = [device newBufferWithLength:node_bytes
                                                        options:MTLResourceStorageModeShared];
    id<MTLBuffer> buf_visited     = [device newBufferWithLength:node_bytes
                                                        options:MTLResourceStorageModeShared];
    id<MTLBuffer> buf_updated     = [device newBufferWithLength:sizeof(int)
                                                        options:MTLResourceStorageModeShared];
    id<MTLBuffer> buf_num_nodes   = [device newBufferWithLength:sizeof(uint32_t)
                                                        options:MTLResourceStorageModeShared];

    // Upload graph (constant)
    memcpy(buf_row_offsets.contents, row_offsets.data(), row_bytes);
    memcpy(buf_col_indices.contents, col_indices.data(), edge_bytes);
    *(uint32_t*)buf_num_nodes.contents = (uint32_t)num_nodes;

    // -------------------------------------------------------------------
    // Warmup
    // -------------------------------------------------------------------
    for (int w = 0; w < num_warmup; ++w) {
        run_bfs_once(device, queue, pso_kernel, pso_update,
                     buf_row_offsets, buf_col_indices,
                     buf_cost, buf_frontier, buf_visited, buf_updated, buf_num_nodes,
                     num_nodes, threads_per_tg);
    }

    // -------------------------------------------------------------------
    // Timed iterations
    // -------------------------------------------------------------------
    std::vector<double> times(1);
    for (int it = 0; it < 1; ++it) {
        times[it] = run_bfs_once(device, queue, pso_kernel, pso_update,
                                 buf_row_offsets, buf_col_indices,
                                 buf_cost, buf_frontier, buf_visited, buf_updated,
                                 buf_num_nodes, num_nodes, threads_per_tg);
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

    char problemSize[64];
    snprintf(problemSize, sizeof(problemSize), "%d", num_nodes);

    BenchResult r;
    r.kernel_name  = "rodinia_bfs";
    r.problem_size = problemSize;
    r.iterations = 1;
    r.avg_ms       = avg;
    r.min_ms       = mn;
    r.max_ms       = mx;
    r.stddev_ms    = stddev;

    // Bandwidth estimate: 2 × (row_offsets + col_indices + 3 × nodes) × sizeof(int)
    long long bytes_per_bfs =
        2LL * ((long long)(num_nodes + 1 + num_edges + 3 * num_nodes) * (int)sizeof(int));
    double bw_gb = (double)bytes_per_bfs / (avg * 1e-3) / 1e9;
    fprintf(stderr, "Effective bandwidth: %.2f GB/s  (avg %.4f ms/BFS)\n",
            bw_gb, avg);

    // JSON-lines output
    double edges_per_sec = (double)num_edges / (avg * 1e-3);
    printf("{\"type\":\"kernel\",\"name\":\"bfs_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"num_nodes\":%d,\"edges_per_node\":%d,\"block_size\":%d,"
           "}}\n",
           avg, num_nodes, edges_per_node, block_size);
    printf("{\"type\":\"kernel\",\"name\":\"bfs_update\",\"time_ms\":%.6f,"
           "\"params\":{\"num_nodes\":%d,\"edges_per_node\":%d,\"block_size\":%d,"
           "}}\n",
           avg, num_nodes, edges_per_node, block_size);
    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"bandwidth_gbps\",\"value\":%.2f},"
           "{\"name\":\"edges_per_sec\",\"value\":%.2f}]}\n",
           avg, bw_gb, edges_per_sec);

    return EXIT_SUCCESS;

    } // @autoreleasepool
}
