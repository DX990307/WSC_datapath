/**
 * graph_cc_metal.mm — Apple Metal host for Connected Components benchmark.
 *
 * Computes connected components on a random undirected sparse graph using
 * iterative label propagation. The graph is stored in CSR format.
 *
 * Output (stdout): JSON-lines protocol
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
// Metal params struct (must match shader)
// ---------------------------------------------------------------------------

struct CCParams {
    uint32_t N;
};

// ---------------------------------------------------------------------------
// main
// ---------------------------------------------------------------------------

int main(int argc, char* argv[]) {
    @autoreleasepool {
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

    // -------------------------------------------------------------------
    // Metal device setup
    // -------------------------------------------------------------------
    id<MTLDevice> device = MTLCreateSystemDefaultDevice();
    if (!device) {
        fprintf(stderr, "Error: no Metal device found\n");
        return EXIT_FAILURE;
    }
    fprintf(stderr, "Device: %s\n", device.name.UTF8String);
    fprintf(stderr, "Connected Components (label propagation)  |  "
            "Vertices: %d  |  Edges: %d  |  Avg degree: %d  |  "
            "Max iterations: %d  |  "
            "Benchmark iterations: 5 warmup + %d timed\n\n",
            N, num_edges, avg_degree, max_iterations, iters);

    id<MTLCommandQueue> queue = [device newCommandQueue];

    // -------------------------------------------------------------------
    // Compile Metal shader
    // -------------------------------------------------------------------
    NSError* err = nil;
    id<MTLLibrary> library = nil;

    NSString* exePath = [[NSBundle mainBundle] executablePath];
    NSString* dir     = [exePath stringByDeletingLastPathComponent];
    NSString* srcPath = [dir stringByAppendingPathComponent:@"graph_cc.metal"];

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
        fprintf(stderr, "Error: graph_cc.metal not found at %s\n",
                srcPath.UTF8String);
        return EXIT_FAILURE;
    }

    id<MTLFunction> fnCC = [library newFunctionWithName:@"cc_propagate_kernel"];
    if (!fnCC) {
        fprintf(stderr, "Error: kernel function not found\n");
        return EXIT_FAILURE;
    }

    id<MTLComputePipelineState> psoCC =
        [device newComputePipelineStateWithFunction:fnCC error:&err];
    if (!psoCC) {
        fprintf(stderr, "Error creating pipeline: %s\n",
                err.localizedDescription.UTF8String);
        return EXIT_FAILURE;
    }

    // -------------------------------------------------------------------
    // Allocate buffers
    // -------------------------------------------------------------------
    id<MTLBuffer> bufRowOffsets = [device newBufferWithBytes:row_offsets.data()
                                                      length:(N + 1) * sizeof(int)
                                                     options:MTLResourceStorageModeShared];
    id<MTLBuffer> bufColIndices = [device newBufferWithBytes:col_indices.data()
                                                      length:num_edges * sizeof(int)
                                                     options:MTLResourceStorageModeShared];
    id<MTLBuffer> bufLabels     = [device newBufferWithLength:N * sizeof(int)
                                                      options:MTLResourceStorageModeShared];
    id<MTLBuffer> bufChanged    = [device newBufferWithLength:sizeof(int)
                                                      options:MTLResourceStorageModeShared];

    NSUInteger tgSize = (NSUInteger)block_size;
    NSUInteger gridN  = (NSUInteger)N;

    CCParams params;
    params.N = (uint32_t)N;

    // Lambda: run CC propagation until convergence
    auto run_cc = [&]() -> int {
        // Initialize labels
        int* labelPtr = (int*)bufLabels.contents;
        for (int i = 0; i < N; ++i) labelPtr[i] = i;

        int iters_done = 0;

        for (int iter = 0; iter < max_iterations; ++iter) {
            // Reset changed flag
            int* changedPtr = (int*)bufChanged.contents;
            *changedPtr = 0;

            id<MTLCommandBuffer> cb = [queue commandBuffer];
            id<MTLComputeCommandEncoder> enc = [cb computeCommandEncoder];

            [enc setComputePipelineState:psoCC];
            [enc setBuffer:bufRowOffsets offset:0 atIndex:0];
            [enc setBuffer:bufColIndices offset:0 atIndex:1];
            [enc setBuffer:bufLabels     offset:0 atIndex:2];
            [enc setBuffer:bufChanged    offset:0 atIndex:3];
            [enc setBytes:&params length:sizeof(params) atIndex:4];
            [enc dispatchThreads:MTLSizeMake(gridN, 1, 1)
                threadsPerThreadgroup:MTLSizeMake(tgSize, 1, 1)];

            [enc endEncoding];
            [cb commit];
            [cb waitUntilCompleted];

            iters_done = iter + 1;
            changedPtr = (int*)bufChanged.contents;
            if (*changedPtr == 0) break;
        }

        return iters_done;
    };

    // -------------------------------------------------------------------
    // Warmup
    // -------------------------------------------------------------------
    for (int w = 0; w < num_warmup; ++w) {
        run_cc();
    }

    // -------------------------------------------------------------------
    // Timed iterations
    // -------------------------------------------------------------------
    std::vector<double> times((size_t)iters);
    int total_cc_iters = 0;
    for (int i = 0; i < iters; ++i) {
        uint64_t t0 = mach_absolute_time();
        int cc_iters = run_cc();
        uint64_t t1 = mach_absolute_time();
        times[i] = ticks_to_ms(t1 - t0);
        total_cc_iters += cc_iters;
    }

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
        run_cc();

        const int* gpuLabels = (const int*)bufLabels.contents;

        std::vector<int> h_cpu_labels;
        cc_cpu(N, max_iterations, row_offsets, col_indices, h_cpu_labels);

        int verify_n = (N < 1024) ? N : 1024;
        int errors = 0;

        for (int i = 0; i < verify_n; ++i) {
            if (gpuLabels[i] != h_cpu_labels[i]) {
                if (errors < 10) {
                    fprintf(stderr, "Mismatch at vertex %d: GPU=%d CPU=%d\n",
                            i, gpuLabels[i], h_cpu_labels[i]);
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

    return EXIT_SUCCESS;

    } // @autoreleasepool
}
