/**
 * polybench_syr2k_metal.mm — Apple Metal host for the PolyBench SYR2K benchmark.
 *
 * Symmetric rank-2k update: C = alpha*A*B^T + alpha*B*A^T + beta*C
 * A, B are [N x M], C is [N x N] (single-precision)
 * Default: alpha=1.5, beta=1.2
 * Measures peak compute throughput in GFLOPS.
 *
 * Usage:
 *   ./polybench_syr2k [--size N]
 *
 * Output (stdout): JSON-lines
 * Output (stderr): device info, timing details
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

static double ticks_to_seconds(uint64_t ticks)
{
    static mach_timebase_info_data_t tb = {0, 0};
    if (tb.denom == 0) mach_timebase_info(&tb);
    return (double)ticks * (double)tb.numer / (double)tb.denom * 1e-9;
}

static double ticks_to_ms(uint64_t ticks)
{
    return ticks_to_seconds(ticks) * 1e3;
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

static float parseFloatParam(int argc, char** argv, const char* name, float defaultVal) {
    for (int i = 1; i < argc - 1; ++i) {
        if (strcmp(argv[i], name) == 0) {
            float v = (float)atof(argv[i + 1]);
            return v;
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

    int N     = parseIntParam(argc, argv, "--size", 1024);
    int M     = parseIntParam(argc, argv, "--inner_size", 1024);
    int block_size = 256;
    float alpha = parseFloatParam(argc, argv, "--alpha", 1.5f);
    float beta  = parseFloatParam(argc, argv, "--beta", 1.2f);

    // Environment variable overrides
    const char* env_val;
    env_val = getenv("BENCH_PARAM_matrix_size");
    if (env_val) N = atoi(env_val);
    env_val = getenv("BENCH_PARAM_inner_size");
    if (env_val) M = atoi(env_val);
    env_val = getenv("BENCH_PARAM_block_size");
    if (env_val) block_size = atoi(env_val);
    env_val = getenv("BENCH_PARAM_alpha");
    if (env_val) alpha = (float)atof(env_val);
    env_val = getenv("BENCH_PARAM_beta");
    if (env_val) beta = (float)atof(env_val);
    int num_warmup = 0;

    size_t bytes_AB = (size_t)N * M * sizeof(float);
    size_t bytes_C  = (size_t)N * N * sizeof(float);

    // -------------------------------------------------------------------
    // Metal device setup
    // -------------------------------------------------------------------
    id<MTLDevice> device = MTLCreateSystemDefaultDevice();
    if (!device) {
        fprintf(stderr, "Error: no Metal device found\n");
        return EXIT_FAILURE;
    }
    fprintf(stderr, "Device: %s\n", device.name.UTF8String);
    fprintf(stderr, "SYR2K: N=%d, M=%d  |  alpha=%.1f  beta=%.1f\n\n",
            N, M, alpha, beta);

    id<MTLCommandQueue> queue = [device newCommandQueue];

    // -------------------------------------------------------------------
    // Compile Metal shader from source file next to the binary
    // -------------------------------------------------------------------
    NSError *err = nil;
    id<MTLLibrary> library = nil;

    NSString *exePath = [[NSBundle mainBundle] executablePath];
    NSString *dir     = [exePath stringByDeletingLastPathComponent];
    NSString *srcPath = [dir stringByAppendingPathComponent:@"polybench_syr2k.metal"];

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
        fprintf(stderr, "Error: polybench_syr2k.metal not found at %s\n",
                srcPath.UTF8String);
        return EXIT_FAILURE;
    }

    id<MTLFunction> fn = [library newFunctionWithName:@"syr2k_kernel"];
    if (!fn) {
        fprintf(stderr, "Error: kernel 'syr2k_kernel' not found\n");
        return EXIT_FAILURE;
    }
    id<MTLComputePipelineState> pso =
        [device newComputePipelineStateWithFunction:fn error:&err];
    if (!pso) {
        fprintf(stderr, "Error creating pipeline: %s\n",
                err.localizedDescription.UTF8String);
        return EXIT_FAILURE;
    }

    // -------------------------------------------------------------------
    // Allocate Metal buffers (shared memory — accessible from CPU and GPU)
    // -------------------------------------------------------------------
    id<MTLBuffer> buf_A = [device newBufferWithLength:bytes_AB
                                              options:MTLResourceStorageModeShared];
    id<MTLBuffer> buf_B = [device newBufferWithLength:bytes_AB
                                              options:MTLResourceStorageModeShared];
    id<MTLBuffer> buf_C = [device newBufferWithLength:bytes_C
                                              options:MTLResourceStorageModeShared];

    // Keep a host copy of C for re-initialization between iterations
    float* h_C_backup = (float*)malloc(bytes_C);

    // Initialize A, B, C (PolyBench-style random initialization)
    float* h_A = (float*)buf_A.contents;
    float* h_B = (float*)buf_B.contents;
    float* h_C = (float*)buf_C.contents;

    srand(42);
    for (int i = 0; i < N * M; ++i) {
        h_A[i] = (float)(rand() % 100) / 10.0f;
        h_B[i] = (float)(rand() % 100) / 10.0f;
    }
    for (int i = 0; i < N * N; ++i) {
        h_C[i] = (float)(rand() % 100) / 10.0f;
        h_C_backup[i] = h_C[i];
    }

    // Kernel parameters: params = {N, M, 0, 0}, alpha_beta = {alpha, beta}
    uint32_t params_data[4] = { (uint32_t)N, (uint32_t)M, 0, 0 };
    float    ab_data[2]     = { alpha, beta };

    // Threadgroup (tile) size: 16×16 matches TILE in Metal shader
    NSUInteger tg_x = 16, tg_y = 16;
    NSUInteger grid_x = (NSUInteger)((N + 15) / 16) * 16;
    NSUInteger grid_y = (NSUInteger)((N + 15) / 16) * 16;

    auto dispatch_syr2k = [&]() {
        id<MTLCommandBuffer>         cb  = [queue commandBuffer];
        id<MTLComputeCommandEncoder> enc = [cb computeCommandEncoder];
        [enc setComputePipelineState:pso];
        [enc setBuffer:buf_A offset:0 atIndex:0];
        [enc setBuffer:buf_B offset:0 atIndex:1];
        [enc setBuffer:buf_C offset:0 atIndex:2];
        [enc setBytes:params_data length:sizeof(params_data) atIndex:3];
        [enc setBytes:ab_data     length:sizeof(ab_data)     atIndex:4];
        [enc dispatchThreads:MTLSizeMake(grid_x, grid_y, 1)
            threadsPerThreadgroup:MTLSizeMake(tg_x, tg_y, 1)];
        [enc endEncoding];
        [cb commit];
        [cb waitUntilCompleted];
    };

    // -------------------------------------------------------------------
    // Warmup (not measured)
    // -------------------------------------------------------------------
    for (int w = 0; w < num_warmup; ++w) {
        dispatch_syr2k();
    }

    // -------------------------------------------------------------------
    // Timed iterations
    // -------------------------------------------------------------------
    std::vector<double> times(1);
    for (int i = 0; i < 1; ++i) {
        // Restore C before each iteration
        memcpy(h_C, h_C_backup, bytes_C);
        uint64_t t0 = mach_absolute_time();
        dispatch_syr2k();
        uint64_t t1 = mach_absolute_time();
        times[i] = ticks_to_ms(t1 - t0);
    }

    // Compute statistics
    double sum_ms = 0.0, mn = DBL_MAX, mx = 0.0;
    for (int i = 0; i < 1; ++i) {
        sum_ms += times[i];
        if (times[i] < mn) mn = times[i];
        if (times[i] > mx) mx = times[i];
    }
    double avg_ms = sum_ms / 1;
    double variance = 0.0;
    for (int i = 0; i < 1; ++i) {
        double d = times[i] - avg_ms;
        variance += d * d;
    }
    double stddev = (1 > 1) ? sqrt(variance / (1 - 1)) : 0.0;

    // GFLOPS: 4*N*N*M (two rank-k products, each 2*N*N*M FLOPs)
    double gflops = 4.0 * (double)N * (double)N * (double)M
                    / (avg_ms * 1e-3) / 1e9;

    fprintf(stderr, "Performance: %.2f GFLOPS  (avg %.4f ms, min %.4f ms, max %.4f ms, stddev %.4f ms)\n",
            gflops, avg_ms, mn, mx, stddev);

    // JSON-lines output
    printf("{\"type\":\"kernel\",\"name\":\"syr2k_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"matrix_size\":%d,\"inner_size\":%d"
           "\"block_size\":%d,\"alpha\":%.2f,\"beta\":%.2f}}\n",
           avg_ms, N, M, block_size, alpha, beta);
    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"gflops\",\"value\":%.2f}]}\n",
           avg_ms, gflops);

    free(h_C_backup);
    return EXIT_SUCCESS;

    } // @autoreleasepool
}
