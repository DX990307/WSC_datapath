/**
 * heteromark_pagerank_metal.mm — Apple Metal host for the PageRank benchmark.
 *
 * Iterative PageRank (power method) on a synthetic Erdős–Rényi graph in CSR
 * format.  PR[v] = (1-d)/N + d * sum(PR[u]/degree[u]) for all u→v.
 *
 * Usage:
 *   ./heteromark_pagerank [--vertices N] [--pr-iterations P]
 *
 *   --vertices N       Number of vertices (default: 65536)
 *   --iterations I     Timed iterations (default: 5)
 *   --pr-iterations P  PageRank iterations per run (default: 20)
 *
 * Output (stdout): CSV — heteromark_pagerank,<N_vertices>,<time_ms>,<edges_per_sec>
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
// Generate synthetic Erdős–Rényi graph in CSR format
// ---------------------------------------------------------------------------

static void generateCSR(int N, int avgDegree, uint32_t seed,
                         std::vector<int>& rowPtr,
                         std::vector<int>& colIdx,
                         std::vector<int>& outDegree) {
    std::vector<std::vector<int>> adj(N);
    uint32_t rng = seed;

    for (int u = 0; u < N; ++u) {
        int deg = avgDegree;
        for (int d = 0; d < deg; ++d) {
            int v = (int)(xorshift32(rng) % (uint32_t)N);
            adj[v].push_back(u);
        }
    }

    outDegree.resize(N);
    for (int u = 0; u < N; ++u) {
        outDegree[u] = avgDegree;
    }

    rowPtr.resize(N + 1);
    rowPtr[0] = 0;
    for (int v = 0; v < N; ++v) {
        rowPtr[v + 1] = rowPtr[v] + (int)adj[v].size();
    }

    int nnz = rowPtr[N];
    colIdx.resize(nnz);
    for (int v = 0; v < N; ++v) {
        int offset = rowPtr[v];
        for (int j = 0; j < (int)adj[v].size(); ++j) {
            colIdx[offset + j] = adj[v][j];
        }
    }
}

// ---------------------------------------------------------------------------
// CPU reference PageRank for verification
// ---------------------------------------------------------------------------

static void pagerank_cpu(const std::vector<int>& rowPtr,
                          const std::vector<int>& colIdx,
                          const std::vector<int>& outDegree,
                          std::vector<float>& pr,
                          int N, int prIters, float damping) {
    std::vector<float> prNew(N);
    for (int i = 0; i < N; ++i) pr[i] = 1.0f / (float)N;

    for (int iter = 0; iter < prIters; ++iter) {
        for (int v = 0; v < N; ++v) {
            float sum = 0.0f;
            for (int e = rowPtr[v]; e < rowPtr[v + 1]; ++e) {
                int u = colIdx[e];
                sum += pr[u] / (float)outDegree[u];
            }
            prNew[v] = (1.0f - damping) / (float)N + damping * sum;
        }
        pr.swap(prNew);
    }
}

// ---------------------------------------------------------------------------
// Params struct matching Metal shader
// ---------------------------------------------------------------------------

struct PageRankParams {
    uint32_t N;
    float    damping;
};

// ---------------------------------------------------------------------------
// main
// ---------------------------------------------------------------------------

static double parseDoubleParam(int argc, char** argv, const char* name, double defaultVal) {
    for (int i = 1; i < argc - 1; ++i) {
        if (strcmp(argv[i], name) == 0) {
            double v = atof(argv[i + 1]);
            return v;
        }
    }
    return defaultVal;
}

int main(int argc, char* argv[]) {
    @autoreleasepool {
    setvbuf(stdout, NULL, _IONBF, 0);

    // Defaults from params.json
    int vertices     = 65536;
    int pr_iterations = 20;
    double damping_factor = 0.85;

    // Command-line fallback
    vertices      = parseIntParam(argc, argv, "--vertices", vertices);
    pr_iterations = parseIntParam(argc, argv, "--pr-iterations", pr_iterations);
    damping_factor = parseDoubleParam(argc, argv, "--damping_factor", damping_factor);

    // Environment variable overrides
    const char* env_val;
    env_val = getenv("BENCH_PARAM_vertices");
    if (env_val) vertices = atoi(env_val);
    env_val = getenv("BENCH_PARAM_pr_iterations");
    if (env_val) pr_iterations = atoi(env_val);
    env_val = getenv("BENCH_PARAM_damping_factor");
    if (env_val) damping_factor = atof(env_val);
    int num_warmup = 0;

    int N       = vertices;
    int prIters = pr_iterations;
    float damping = (float)damping_factor;

    // -------------------------------------------------------------------
    // Metal device setup
    // -------------------------------------------------------------------
    id<MTLDevice> device = MTLCreateSystemDefaultDevice();
    if (!device) {
        fprintf(stderr, "Error: no Metal device found\n");
        return EXIT_FAILURE;
    }
    fprintf(stderr, "Device: %s\n", device.name.UTF8String);
    fprintf(stderr, "Vertices: %d  |  PR iterations: %d  |  Warmup: %d\n\n",
            N, prIters, num_warmup);

    id<MTLCommandQueue> queue = [device newCommandQueue];

    // -------------------------------------------------------------------
    // Compile Metal shader
    // -------------------------------------------------------------------
    NSError* err = nil;
    id<MTLLibrary> library = nil;

    NSString* exePath = [[NSBundle mainBundle] executablePath];
    NSString* dir     = [exePath stringByDeletingLastPathComponent];
    NSString* srcPath = [dir stringByAppendingPathComponent:@"heteromark_pagerank.metal"];

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
        fprintf(stderr, "Error: heteromark_pagerank.metal not found at %s\n",
                srcPath.UTF8String);
        return EXIT_FAILURE;
    }

    id<MTLFunction> fnPageRank = [library newFunctionWithName:@"pagerank_kernel"];
    if (!fnPageRank) {
        fprintf(stderr, "Error: pagerank_kernel function not found\n");
        return EXIT_FAILURE;
    }

    id<MTLComputePipelineState> psoPageRank =
        [device newComputePipelineStateWithFunction:fnPageRank error:&err];
    if (!psoPageRank) {
        fprintf(stderr, "Error creating pagerank pipeline: %s\n",
                err.localizedDescription.UTF8String);
        return EXIT_FAILURE;
    }

    // -------------------------------------------------------------------
    // Generate graph
    // -------------------------------------------------------------------
    std::vector<int> rowPtr, colIdx, outDegree;
    generateCSR(N, 16, 42, rowPtr, colIdx, outDegree);
    int numEdges = rowPtr[N];
    fprintf(stderr, "Graph: %d vertices, %d edges (avg in-degree: %.1f)\n",
            N, numEdges, (double)numEdges / N);

    // -------------------------------------------------------------------
    // Allocate Metal buffers
    // -------------------------------------------------------------------
    id<MTLBuffer> bufRowPtr = [device newBufferWithBytes:rowPtr.data()
                                                  length:(N + 1) * sizeof(int)
                                                 options:MTLResourceStorageModeShared];
    id<MTLBuffer> bufColIdx = [device newBufferWithBytes:colIdx.data()
                                                  length:numEdges * sizeof(int)
                                                 options:MTLResourceStorageModeShared];
    id<MTLBuffer> bufOutDegree = [device newBufferWithBytes:outDegree.data()
                                                     length:N * sizeof(int)
                                                    options:MTLResourceStorageModeShared];
    id<MTLBuffer> bufPrOld = [device newBufferWithLength:N * sizeof(float)
                                                 options:MTLResourceStorageModeShared];
    id<MTLBuffer> bufPrNew = [device newBufferWithLength:N * sizeof(float)
                                                 options:MTLResourceStorageModeShared];

    NSUInteger tgSize = 256;
    NSUInteger gridN  = (NSUInteger)N;

    PageRankParams params;
    params.N       = (uint32_t)N;
    params.damping = damping;

    // Lambda: run full PageRank
    auto run_pagerank = [&]() {
        // Initialize PR to 1/N
        float* prPtr = (float*)bufPrOld.contents;
        for (int i = 0; i < N; ++i) prPtr[i] = 1.0f / (float)N;

        id<MTLBuffer> curOld = bufPrOld;
        id<MTLBuffer> curNew = bufPrNew;

        for (int it = 0; it < prIters; ++it) {
            id<MTLCommandBuffer> cb = [queue commandBuffer];
            id<MTLComputeCommandEncoder> enc = [cb computeCommandEncoder];

            [enc setComputePipelineState:psoPageRank];
            [enc setBuffer:bufRowPtr    offset:0 atIndex:0];
            [enc setBuffer:bufColIdx    offset:0 atIndex:1];
            [enc setBuffer:bufOutDegree offset:0 atIndex:2];
            [enc setBuffer:curOld       offset:0 atIndex:3];
            [enc setBuffer:curNew       offset:0 atIndex:4];
            [enc setBytes:&params       length:sizeof(params) atIndex:5];

            [enc dispatchThreads:MTLSizeMake(gridN, 1, 1)
                threadsPerThreadgroup:MTLSizeMake(tgSize, 1, 1)];

            [enc endEncoding];
            [cb commit];
            [cb waitUntilCompleted];

            // Swap buffers
            id<MTLBuffer> tmp = curOld;
            curOld = curNew;
            curNew = tmp;
        }

        // After prIters, result is in curOld; copy to bufPrOld if needed
        if (curOld != bufPrOld) {
            memcpy(bufPrOld.contents, curOld.contents, N * sizeof(float));
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

    // edges_per_sec = numEdges * prIters / time_s
    double edges_per_sec = (double)numEdges * (double)prIters / (avg_ms * 1e-3);

    double total_ms = sum;

    // JSON-lines kernel event
    printf("{\"type\":\"kernel\",\"name\":\"pagerank_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"vertices\":%d,\"pr_iterations\":%d"
           "\"damping_factor\":%.6f}}\n",
           avg_ms, vertices, pr_iterations, damping_factor);

    // JSON-lines summary event
    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"edges_per_sec\",\"value\":%.2f}]}\n",
           total_ms, edges_per_sec);

    // Human-readable output to stderr
    fprintf(stderr, "Average time: %.4f ms\n", avg_ms);
    fprintf(stderr, "Throughput:   %.4e edges/sec\n", edges_per_sec);

    // -------------------------------------------------------------------
    // Verification
    // -------------------------------------------------------------------
    const float* gpuPr = (const float*)bufPrOld.contents;

    std::vector<float> refPr(N);
    pagerank_cpu(rowPtr, colIdx, outDegree, refPr, N, prIters, damping);

    int errors = 0;
    for (int v = 0; v < N; ++v) {
        float diff = fabsf(gpuPr[v] - refPr[v]);
        float tol  = 1e-4f * fabsf(refPr[v]) + 1e-7f;
        if (diff > tol) {
            if (errors < 10) {
                fprintf(stderr, "Mismatch at v=%d: GPU=%.8f CPU=%.8f\n",
                        v, gpuPr[v], refPr[v]);
            }
            errors++;
        }
    }
    if (errors > 0)
        fprintf(stderr, "FAIL: %d errors out of %d vertices\n", errors, N);
    else
        fprintf(stderr, "PASS\n");

    return EXIT_SUCCESS;

    } // @autoreleasepool
}
