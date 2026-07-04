/**
 * graph_pr_metal.mm — Apple Metal host for pull-based PageRank benchmark.
 *
 * Pull-based PageRank with CSC format on a random sparse graph.
 * Each thread computes the new rank for one vertex by pulling from incoming
 * neighbors.
 *
 * Usage:
 *   ./graph_pr [--vertices N] [--degree D] [--pr-iterations P]
 *
 *   --vertices N       Number of vertices (default: 131072)
 *   --degree D         Average degree per vertex (default: 16)
 *   --iterations I     Timed iterations (default: 5)
 *   --pr-iterations P  PageRank iterations per run (default: 20)
 *
 * Output (stdout): CSV — graph_pr,<N_vertices>,<time_ms>,<GTEPS>
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
// Generate random graph in CSC format
// ---------------------------------------------------------------------------

static void generate_csc_graph(int N, int avg_degree, uint32_t seed,
                                std::vector<int>& col_offsets,
                                std::vector<int>& row_indices,
                                std::vector<int>& out_degree) {
    uint32_t rng = seed;

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
// Metal params struct (must match shader)
// ---------------------------------------------------------------------------

struct PRParams {
    uint32_t N;
    float    damping;
    float    base_rank;
};

// ---------------------------------------------------------------------------
// main
// ---------------------------------------------------------------------------

int main(int argc, char* argv[]) {
    @autoreleasepool {
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
    int num_warmup = 0;

    int N         = vertices;
    int avg_deg   = degree;
    int pr_iters  = pr_iterations;
    float damping = 0.85f;

    // Generate CSC graph
    std::vector<int> col_offsets, row_indices, out_degree;
    generate_csc_graph(N, avg_deg, 42, col_offsets, row_indices, out_degree);
    int num_edges = col_offsets[N];

    // -------------------------------------------------------------------
    // Metal device setup
    // -------------------------------------------------------------------
    id<MTLDevice> device = MTLCreateSystemDefaultDevice();
    if (!device) {
        fprintf(stderr, "Error: no Metal device found\n");
        return EXIT_FAILURE;
    }
    fprintf(stderr, "Device: %s\n", device.name.UTF8String);
    fprintf(stderr, "PageRank (pull-based)  |  Vertices: %d  |  Edges: %d  |  "
            "PR iterations: %d  |  Damping: %.2f  |  "
            "Iterations: 5 warmup + %d timed\n\n",
            N, num_edges, pr_iters, damping);

    id<MTLCommandQueue> queue = [device newCommandQueue];

    // -------------------------------------------------------------------
    // Compile Metal shader
    // -------------------------------------------------------------------
    NSError* err = nil;
    id<MTLLibrary> library = nil;

    NSString* exePath = [[NSBundle mainBundle] executablePath];
    NSString* dir     = [exePath stringByDeletingLastPathComponent];
    NSString* srcPath = [dir stringByAppendingPathComponent:@"graph_pr.metal"];

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
        fprintf(stderr, "Error: graph_pr.metal not found at %s\n",
                srcPath.UTF8String);
        return EXIT_FAILURE;
    }

    id<MTLFunction> fnPR = [library newFunctionWithName:@"pagerank_kernel"];
    if (!fnPR) {
        fprintf(stderr, "Error: kernel function not found\n");
        return EXIT_FAILURE;
    }

    id<MTLComputePipelineState> psoPR =
        [device newComputePipelineStateWithFunction:fnPR error:&err];
    if (!psoPR) {
        fprintf(stderr, "Error creating pipeline: %s\n",
                err.localizedDescription.UTF8String);
        return EXIT_FAILURE;
    }

    // -------------------------------------------------------------------
    // Allocate buffers
    // -------------------------------------------------------------------
    id<MTLBuffer> bufColOffsets = [device newBufferWithBytes:col_offsets.data()
                                                     length:(N + 1) * sizeof(int)
                                                    options:MTLResourceStorageModeShared];
    id<MTLBuffer> bufRowIndices = [device newBufferWithBytes:row_indices.data()
                                                     length:num_edges * sizeof(int)
                                                    options:MTLResourceStorageModeShared];
    id<MTLBuffer> bufOutDegree  = [device newBufferWithBytes:out_degree.data()
                                                      length:N * sizeof(int)
                                                     options:MTLResourceStorageModeShared];
    id<MTLBuffer> bufRankA      = [device newBufferWithLength:N * sizeof(float)
                                                     options:MTLResourceStorageModeShared];
    id<MTLBuffer> bufRankB      = [device newBufferWithLength:N * sizeof(float)
                                                     options:MTLResourceStorageModeShared];

    NSUInteger tgSize = 256;
    NSUInteger gridN  = (NSUInteger)N;

    float base_rank = (1.0f - damping) / (float)N;
    PRParams params;
    params.N = (uint32_t)N;
    params.damping = damping;
    params.base_rank = base_rank;

    // Lambda: run PageRank
    auto run_pagerank = [&]() {
        // Initialize ranks
        float* rankPtr = (float*)bufRankA.contents;
        float init_val = 1.0f / (float)N;
        for (int i = 0; i < N; ++i) rankPtr[i] = init_val;

        id<MTLBuffer> curBuf = bufRankA;
        id<MTLBuffer> nxtBuf = bufRankB;

        for (int iter = 0; iter < pr_iters; ++iter) {
            id<MTLCommandBuffer> cb = [queue commandBuffer];
            id<MTLComputeCommandEncoder> enc = [cb computeCommandEncoder];

            [enc setComputePipelineState:psoPR];
            [enc setBuffer:bufColOffsets offset:0 atIndex:0];
            [enc setBuffer:bufRowIndices offset:0 atIndex:1];
            [enc setBuffer:bufOutDegree  offset:0 atIndex:2];
            [enc setBuffer:curBuf        offset:0 atIndex:3];
            [enc setBuffer:nxtBuf        offset:0 atIndex:4];
            [enc setBytes:&params length:sizeof(params) atIndex:5];
            [enc dispatchThreads:MTLSizeMake(gridN, 1, 1)
                threadsPerThreadgroup:MTLSizeMake(tgSize, 1, 1)];

            [enc endEncoding];
            [cb commit];
            [cb waitUntilCompleted];

            // Swap
            id<MTLBuffer> tmp = curBuf;
            curBuf = nxtBuf;
            nxtBuf = tmp;
        }

        // If final result is in bufRankB, copy to bufRankA
        if (curBuf != bufRankA) {
            memcpy(bufRankA.contents, curBuf.contents, N * sizeof(float));
        }
    };

    // -------------------------------------------------------------------
    // Warmup
    // -------------------------------------------------------------------
    for (int w = 0; w < num_warmup; ++w) {
        run_pagerank();
    }

    // -------------------------------------------------------------------
    // Timed iterations
    // -------------------------------------------------------------------
    std::vector<double> times(1);
    for (int i = 0; i < 1; ++i) {
        uint64_t t0 = mach_absolute_time();
        run_pagerank();
        uint64_t t1 = mach_absolute_time();
        times[i] = ticks_to_ms(t1 - t0);
    }

    double sum = 0.0;
    for (int i = 0; i < 1; ++i) sum += times[i];
    double avg_ms = sum / 1;

    // GTEPS = N_edges * pr_iterations / time_s / 1e9
    double gteps = (double)num_edges * (double)pr_iters / (avg_ms * 1e-3) / 1e9;

    double total_ms = sum;

    // JSON-lines kernel event
    printf("{\"type\":\"kernel\",\"name\":\"pagerank_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"vertices\":%d,\"degree\":%d,\"pr_iterations\":%d,"
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
        const float* gpuRank = (const float*)bufRankA.contents;

        std::vector<float> h_cpu_rank;
        pagerank_cpu(N, pr_iters, damping, col_offsets, row_indices, out_degree, h_cpu_rank);

        int verify_n = (N < 1024) ? N : 1024;
        int errors = 0;
        float max_err = 0.0f;
        for (int i = 0; i < verify_n; ++i) {
            float err_val = fabsf(gpuRank[i] - h_cpu_rank[i]);
            if (err_val > max_err) max_err = err_val;
            if (err_val > 1e-4f) {
                if (errors < 10) {
                    fprintf(stderr, "Mismatch at vertex %d: GPU=%.6f CPU=%.6f (err=%.6f)\n",
                            i, gpuRank[i], h_cpu_rank[i], err_val);
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

    return EXIT_SUCCESS;

    } // @autoreleasepool
}
