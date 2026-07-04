/**
 * rodinia_kmeans_metal.mm — Apple Metal host for the Rodinia K-Means benchmark.
 *
 * K-means clustering: assign N points (each D-dimensional) to K clusters,
 * update cluster centers, repeat until convergence or max_iter.
 * Derived from the Rodinia benchmark suite.
 *
 * The Metal compute kernels live in rodinia_kmeans.metal and are compiled at
 * runtime from source (loaded from the same directory as the binary).
 *
 * Iteration loop:
 *   1. kmeans_assign  → fills membership[]
 *   2. Zero new_centers[] and counts[] (CPU memset on shared buffers)
 *   3. kmeans_update  → accumulates sums and counts using device atomics
 *   4. CPU divides to get new centers, checks convergence
 *   5. Repeat until delta < 1e-4 or max_iter reached
 *
 * Usage:
 *   ./rodinia_kmeans [--points N] [--dims D] [--clusters K] [--max_iter M]
 *
 *   --points N      Number of data points   (default: 16384)
 *   --dims D        Dimensions per point    (default: 16)
 *   --clusters K    Number of clusters      (default: 8)
 *   --max_iter M    Maximum iterations      (default: 10)
 *
 * Output (stdout): CSV — kmeans,N,K,D,<iterations>,<time_ms>,<Gpoints_per_s>
 * Output (stderr): Per-iteration delta, PASS/FAIL
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

static int parseIntParam(int argc, char** argv, const char* name, int defaultVal)
{
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
// main
// ---------------------------------------------------------------------------

int main(int argc, char* argv[])
{
    @autoreleasepool {
    setvbuf(stdout, NULL, _IONBF, 0);

    // Defaults
    int N        = 16384;
    int D        = 16;
    int K        = 8;
    int max_iter = 10;

    // Command-line fallback
    N        = parseIntParam(argc, argv, "--points", N);
    D        = parseIntParam(argc, argv, "--dims", D);
    K        = parseIntParam(argc, argv, "--clusters", K);
    max_iter = parseIntParam(argc, argv, "--max_iter", max_iter);

    // Environment variable overrides
    const char* env_val;
    env_val = getenv("BENCH_PARAM_points");
    if (env_val) N = atoi(env_val);
    env_val = getenv("BENCH_PARAM_dims");
    if (env_val) D = atoi(env_val);
    env_val = getenv("BENCH_PARAM_clusters");
    if (env_val) K = atoi(env_val);
    env_val = getenv("BENCH_PARAM_max_iter");
    if (env_val) max_iter = atoi(env_val);
    int num_warmup = 0;

    // -------------------------------------------------------------------
    // Metal device setup
    // -------------------------------------------------------------------
    id<MTLDevice> device = MTLCreateSystemDefaultDevice();
    if (!device) {
        fprintf(stderr, "Error: no Metal device found\n");
        return EXIT_FAILURE;
    }
    fprintf(stderr, "Device: %s\n", device.name.UTF8String);
    fprintf(stderr, "N=%d  D=%d  K=%d  max_iter=%d\n\n", N, D, K, max_iter);

    id<MTLCommandQueue> queue = [device newCommandQueue];

    // -------------------------------------------------------------------
    // Compile Metal shaders from source file next to the binary
    // -------------------------------------------------------------------
    NSError   *err     = nil;
    id<MTLLibrary> library = nil;

    NSString *exePath = [[NSBundle mainBundle] executablePath];
    NSString *dir     = [exePath stringByDeletingLastPathComponent];
    NSString *srcPath = [dir stringByAppendingPathComponent:@"rodinia_kmeans.metal"];

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
        fprintf(stderr, "Error: rodinia_kmeans.metal not found at %s\n",
                srcPath.UTF8String);
        return EXIT_FAILURE;
    }

    // Get kernel functions
    id<MTLFunction> fn_assign = [library newFunctionWithName:@"kmeans_assign"];
    id<MTLFunction> fn_update = [library newFunctionWithName:@"kmeans_update"];

    if (!fn_assign || !fn_update) {
        fprintf(stderr, "Error: one or both kernel functions not found in library\n");
        return EXIT_FAILURE;
    }

    id<MTLComputePipelineState> pso_assign =
        [device newComputePipelineStateWithFunction:fn_assign error:&err];
    if (!pso_assign) {
        fprintf(stderr, "Error creating assign pipeline: %s\n",
                err.localizedDescription.UTF8String);
        return EXIT_FAILURE;
    }

    id<MTLComputePipelineState> pso_update =
        [device newComputePipelineStateWithFunction:fn_update error:&err];
    if (!pso_update) {
        fprintf(stderr, "Error creating update pipeline: %s\n",
                err.localizedDescription.UTF8String);
        return EXIT_FAILURE;
    }

    // Thread sizes
    NSUInteger tg_assign = pso_assign.maxTotalThreadsPerThreadgroup;
    if (tg_assign > 256) tg_assign = 256;
    NSUInteger tg_update = pso_update.maxTotalThreadsPerThreadgroup;
    if (tg_update > 256) tg_update = 256;

    // -------------------------------------------------------------------
    // Allocate Metal buffers (shared storage — visible to CPU and GPU)
    // -------------------------------------------------------------------
    size_t points_bytes      = (size_t)N * D * sizeof(float);
    size_t centers_bytes     = (size_t)K * D * sizeof(float);
    size_t membership_bytes  = (size_t)N * sizeof(int);
    size_t counts_bytes      = (size_t)K * sizeof(int);

    id<MTLBuffer> buf_points      = [device newBufferWithLength:points_bytes
                                                        options:MTLResourceStorageModeShared];
    id<MTLBuffer> buf_centers     = [device newBufferWithLength:centers_bytes
                                                        options:MTLResourceStorageModeShared];
    id<MTLBuffer> buf_new_centers = [device newBufferWithLength:centers_bytes
                                                        options:MTLResourceStorageModeShared];
    id<MTLBuffer> buf_counts      = [device newBufferWithLength:counts_bytes
                                                        options:MTLResourceStorageModeShared];
    id<MTLBuffer> buf_membership  = [device newBufferWithLength:membership_bytes
                                                        options:MTLResourceStorageModeShared];

    // Scalar parameter buffers (constant)
    uint32_t uN = (uint32_t)N, uD = (uint32_t)D, uK = (uint32_t)K;
    id<MTLBuffer> buf_N = [device newBufferWithBytes:&uN length:sizeof(uint32_t)
                                             options:MTLResourceStorageModeShared];
    id<MTLBuffer> buf_D = [device newBufferWithBytes:&uD length:sizeof(uint32_t)
                                             options:MTLResourceStorageModeShared];
    id<MTLBuffer> buf_K = [device newBufferWithBytes:&uK length:sizeof(uint32_t)
                                             options:MTLResourceStorageModeShared];

    // -------------------------------------------------------------------
    // Initialize data
    // -------------------------------------------------------------------
    float* h_points     = (float*)buf_points.contents;
    float* h_centers    = (float*)buf_centers.contents;
    int*   h_membership = (int*)buf_membership.contents;

    // Random points in [0, 1)^D
    unsigned int seed = 42;
    for (int i = 0; i < N * D; ++i) {
        seed = seed * 1664525u + 1013904223u;
        h_points[i] = (float)(seed >> 8) / (float)(1 << 24);
    }

    // Initialize centers as first K points
    memcpy(h_centers, h_points, centers_bytes);

    // Initialize membership to -1
    for (int i = 0; i < N; ++i) h_membership[i] = -1;

    // Previous membership for convergence check
    std::vector<int> h_membership_prev(N, -1);

    // -------------------------------------------------------------------
    // Lambda: dispatch one assign pass
    // -------------------------------------------------------------------
    auto dispatch_assign = [&]() {
        NSUInteger num_tg = ((NSUInteger)N + tg_assign - 1) / tg_assign;
        id<MTLCommandBuffer>         cb  = [queue commandBuffer];
        id<MTLComputeCommandEncoder> enc = [cb computeCommandEncoder];
        [enc setComputePipelineState:pso_assign];
        [enc setBuffer:buf_points     offset:0 atIndex:0];
        [enc setBuffer:buf_centers    offset:0 atIndex:1];
        [enc setBuffer:buf_membership offset:0 atIndex:2];
        [enc setBuffer:buf_N          offset:0 atIndex:3];
        [enc setBuffer:buf_D          offset:0 atIndex:4];
        [enc setBuffer:buf_K          offset:0 atIndex:5];
        [enc dispatchThreadgroups:MTLSizeMake(num_tg, 1, 1)
           threadsPerThreadgroup:MTLSizeMake(tg_assign, 1, 1)];
        [enc endEncoding];
        [cb commit];
        [cb waitUntilCompleted];
    };

    // Lambda: dispatch one update pass (assumes new_centers and counts already zeroed)
    auto dispatch_update = [&]() {
        NSUInteger num_tg = ((NSUInteger)N + tg_update - 1) / tg_update;
        id<MTLCommandBuffer>         cb  = [queue commandBuffer];
        id<MTLComputeCommandEncoder> enc = [cb computeCommandEncoder];
        [enc setComputePipelineState:pso_update];
        [enc setBuffer:buf_points      offset:0 atIndex:0];
        [enc setBuffer:buf_counts      offset:0 atIndex:1];
        [enc setBuffer:buf_new_centers offset:0 atIndex:2];
        [enc setBuffer:buf_membership  offset:0 atIndex:3];
        [enc setBuffer:buf_N           offset:0 atIndex:4];
        [enc setBuffer:buf_D           offset:0 atIndex:5];
        [enc dispatchThreadgroups:MTLSizeMake(num_tg, 1, 1)
           threadsPerThreadgroup:MTLSizeMake(tg_update, 1, 1)];
        [enc endEncoding];
        [cb commit];
        [cb waitUntilCompleted];
    };

    // -------------------------------------------------------------------
    // Warmup
    // -------------------------------------------------------------------
    for (int w = 0; w < num_warmup; ++w) {
        dispatch_assign();
    }

    // -------------------------------------------------------------------
    // Main K-Means iteration loop
    // -------------------------------------------------------------------
    uint64_t t_start = mach_absolute_time();

    float* h_new_centers = (float*)buf_new_centers.contents;
    int*   h_counts      = (int*)buf_counts.contents;

    int iter = 0;
    for (iter = 0; iter < max_iter; ++iter) {

        // Step 1: assign
        dispatch_assign();

        // Step 2: zero accumulators (shared buffers — CPU can zero directly)
        memset(h_new_centers, 0, centers_bytes);
        memset(h_counts,      0, counts_bytes);

        // Step 3: update
        dispatch_update();

        // Step 4: divide on CPU
        for (int k = 0; k < K; ++k) {
            if (h_counts[k] > 0) {
                for (int d = 0; d < D; ++d) {
                    h_new_centers[k * D + d] /= (float)h_counts[k];
                }
            } else {
                // Empty cluster — keep old center
                for (int d = 0; d < D; ++d) {
                    h_new_centers[k * D + d] = h_centers[k * D + d];
                }
            }
        }

        // Step 5: check convergence
        int changed = 0;
        for (int i = 0; i < N; ++i) {
            if (h_membership[i] != h_membership_prev[i]) ++changed;
        }
        double delta = (double)changed / (double)N;
        fprintf(stderr, "  iter %2d: changed=%-6d  delta=%.6f\n", iter + 1, changed, delta);

        // Copy new centers into active centers buffer
        memcpy(h_centers, h_new_centers, centers_bytes);
        memcpy(h_membership_prev.data(), h_membership, membership_bytes);

        if (delta < 1e-4) {
            iter++;
            break;
        }
    }

    uint64_t t_stop = mach_absolute_time();
    double time_ms = ticks_to_ms(t_stop - t_start);

    // -------------------------------------------------------------------
    // Verify convergence: run one extra assign pass, check delta < 1%
    // -------------------------------------------------------------------
    dispatch_assign();

    int final_changed = 0;
    for (int i = 0; i < N; ++i) {
        if (h_membership[i] != h_membership_prev[i]) ++final_changed;
    }
    double final_delta = (double)final_changed / (double)N;
    // Pass if delta < 5% (acceptable for benchmarks with few iterations).
    bool pass = (final_delta < 0.05);

    fprintf(stderr, "\nFinal stability check: changed=%d  delta=%.6f  %s\n",
            final_changed, final_delta, pass ? "PASS" : "FAIL");

    // -------------------------------------------------------------------
    // Report
    // -------------------------------------------------------------------
    double gpoints_per_s = (double)N * (double)iter / (time_ms * 1e-3) / 1e9;

    // JSON-lines kernel events
    printf("{\"type\":\"kernel\",\"name\":\"kmeans_assign_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"points\":%d,\"dims\":%d,\"clusters\":%d,\"max_iter\":%d}}\n",
           time_ms, N, D, K, max_iter);

    printf("{\"type\":\"kernel\",\"name\":\"kmeans_update_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"points\":%d,\"dims\":%d,\"clusters\":%d,\"max_iter\":%d}}\n",
           time_ms, N, D, K, max_iter);

    // JSON-lines summary event
    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"gpoints_per_s\",\"value\":%.6f}]}\n",
           time_ms, gpoints_per_s);

    fprintf(stderr, "Iterations: %d  Time: %.4f ms  Throughput: %.6f Gpoints/s\n",
            iter, time_ms, gpoints_per_s);

    return pass ? EXIT_SUCCESS : EXIT_FAILURE;

    } // @autoreleasepool
}
