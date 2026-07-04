/**
 * lonestar_sssp_metal.mm — Apple Metal host for Bellman-Ford SSSP benchmark.
 *
 * Iterative edge relaxation on a random sparse graph stored in CSR format.
 * All edges are relaxed in parallel each iteration; repeats until no changes.
 *
 * Usage:
 *   ./lonestar_sssp [--vertices N] [--degree D] [--iterations I]
 *
 *   --vertices N     Number of vertices (default: 65536)
 *   --degree D       Average degree per vertex (default: 16)
 *   --iterations I   Timed iterations (default: 5)
 *
 * Output (stdout): CSV — lonestar_sssp,<N_vertices>,<time_ms>,<Medges_per_sec>
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
#include <climits>
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
// Generate random CSR graph
// ---------------------------------------------------------------------------

static void generate_csr_graph(int N, int avg_degree, uint32_t seed,
                                std::vector<int>& row_offsets,
                                std::vector<int>& col_indices,
                                std::vector<int>& weights) {
    uint32_t rng = seed;
    row_offsets.resize(N + 1);
    std::vector<int> degrees(N);
    for (int i = 0; i < N; ++i) {
        int d = avg_degree / 2 + (int)(xorshift32(rng) % (avg_degree + 1));
        if (d < 1) d = 1;
        degrees[i] = d;
    }

    row_offsets[0] = 0;
    for (int i = 0; i < N; ++i)
        row_offsets[i + 1] = row_offsets[i] + degrees[i];

    int total_edges = row_offsets[N];
    col_indices.resize(total_edges);
    weights.resize(total_edges);

    for (int i = 0; i < N; ++i) {
        for (int e = row_offsets[i]; e < row_offsets[i + 1]; ++e) {
            col_indices[e] = (int)(xorshift32(rng) % (uint32_t)N);
            weights[e] = 1 + (int)(xorshift32(rng) % 100);
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
// main
// ---------------------------------------------------------------------------

int main(int argc, char* argv[]) {
    @autoreleasepool {
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
    int num_warmup = 0;

    int N       = vertices;
    int avg_deg = degree;
    int iters   = iterations;
    int source  = 0;

    // Generate CSR graph
    std::vector<int> row_offsets, col_indices, weights;
    generate_csr_graph(N, avg_deg, 42, row_offsets, col_indices, weights);
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
    fprintf(stderr, "SSSP (Bellman-Ford)  |  Vertices: %d  |  Edges: %d  |  "
            "Iterations: %d warmup + %d timed\n\n", N, num_edges, num_warmup, iters);

    id<MTLCommandQueue> queue = [device newCommandQueue];

    // -------------------------------------------------------------------
    // Compile Metal shader
    // -------------------------------------------------------------------
    NSError* err = nil;
    id<MTLLibrary> library = nil;

    NSString* exePath = [[NSBundle mainBundle] executablePath];
    NSString* dir     = [exePath stringByDeletingLastPathComponent];
    NSString* srcPath = [dir stringByAppendingPathComponent:@"lonestar_sssp.metal"];

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
        fprintf(stderr, "Error: lonestar_sssp.metal not found at %s\n",
                srcPath.UTF8String);
        return EXIT_FAILURE;
    }

    id<MTLFunction> fnBF = [library newFunctionWithName:@"bellman_ford_kernel"];
    if (!fnBF) {
        fprintf(stderr, "Error: kernel function not found\n");
        return EXIT_FAILURE;
    }

    id<MTLComputePipelineState> psoBF =
        [device newComputePipelineStateWithFunction:fnBF error:&err];
    if (!psoBF) {
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
    id<MTLBuffer> bufWeights    = [device newBufferWithBytes:weights.data()
                                                     length:num_edges * sizeof(int)
                                                    options:MTLResourceStorageModeShared];
    id<MTLBuffer> bufDist       = [device newBufferWithLength:N * sizeof(int)
                                                     options:MTLResourceStorageModeShared];
    id<MTLBuffer> bufChanged    = [device newBufferWithLength:sizeof(int)
                                                     options:MTLResourceStorageModeShared];

    NSUInteger tgSize = (NSUInteger)block_size;
    NSUInteger gridN  = (NSUInteger)N;

    // Lambda: run SSSP
    auto run_sssp = [&]() -> int {
        // Initialize distances
        int* distPtr = (int*)bufDist.contents;
        for (int i = 0; i < N; ++i) distPtr[i] = INT_MAX;
        distPtr[source] = 0;

        int total_iters = 0;
        int max_iters = N;
        uint32_t paramN = (uint32_t)N;

        for (int iter = 0; iter < max_iters; ++iter) {
            int* changedPtr = (int*)bufChanged.contents;
            *changedPtr = 0;

            id<MTLCommandBuffer> cb = [queue commandBuffer];
            id<MTLComputeCommandEncoder> enc = [cb computeCommandEncoder];

            [enc setComputePipelineState:psoBF];
            [enc setBuffer:bufRowOffsets offset:0 atIndex:0];
            [enc setBuffer:bufColIndices offset:0 atIndex:1];
            [enc setBuffer:bufWeights    offset:0 atIndex:2];
            [enc setBuffer:bufDist       offset:0 atIndex:3];
            [enc setBuffer:bufChanged    offset:0 atIndex:4];
            [enc setBytes:&paramN length:sizeof(paramN) atIndex:5];
            [enc dispatchThreads:MTLSizeMake(gridN, 1, 1)
                threadsPerThreadgroup:MTLSizeMake(tgSize, 1, 1)];

            [enc endEncoding];
            [cb commit];
            [cb waitUntilCompleted];

            total_iters++;
            if (*changedPtr == 0) break;
        }
        return total_iters;
    };

    // -------------------------------------------------------------------
    // Warmup
    // -------------------------------------------------------------------
    int bf_iters = 0;
    for (int w = 0; w < num_warmup; ++w) {
        bf_iters = run_sssp();
    }
    fprintf(stderr, "Bellman-Ford converged in %d iterations\n", bf_iters);

    // -------------------------------------------------------------------
    // Timed iterations
    // -------------------------------------------------------------------
    std::vector<double> times((size_t)iters);
    std::vector<int> iter_counts((size_t)iters);
    for (int i = 0; i < iters; ++i) {
        uint64_t t0 = mach_absolute_time();
        iter_counts[i] = run_sssp();
        uint64_t t1 = mach_absolute_time();
        times[i] = ticks_to_ms(t1 - t0);
    }

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
        const int* gpuDist = (const int*)bufDist.contents;

        std::vector<int> h_cpu_dist;
        bellman_ford_cpu(N, row_offsets, col_indices, weights, h_cpu_dist, source);

        int verify_n = (N < 1024) ? N : 1024;
        int errors = 0;
        for (int i = 0; i < verify_n; ++i) {
            if (gpuDist[i] != h_cpu_dist[i]) {
                if (errors < 10) {
                    fprintf(stderr, "Mismatch at vertex %d: GPU=%d CPU=%d\n",
                            i, gpuDist[i], h_cpu_dist[i]);
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
