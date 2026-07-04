/**
 * polybench_2mm_metal.mm — Apple Metal host for the PolyBench 2MM benchmark.
 *
 * Two matrix multiplications:
 *   D = alpha * A * B + beta * D
 *   E = alpha * C * D + beta * E
 * for NxN single-precision matrices.
 *
 * Usage:
 *   ./polybench_2mm [--size N]
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

static float parseFloatParam(int argc, char** argv, const char* name, float defaultVal) {
    for (int i = 1; i < argc - 1; ++i) {
        if (strcmp(argv[i], name) == 0) {
            return (float)atof(argv[i + 1]);
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

    int N          = 1024;
    int block_size = 256;
    float alpha    = 1.5f;
    float beta     = 1.2f;
    const char* precision = "float";

    N          = parseIntParam(argc, argv, "--size", N);
    alpha      = parseFloatParam(argc, argv, "--alpha", alpha);
    beta       = parseFloatParam(argc, argv, "--beta", beta);

    const char* env_val;
    env_val = getenv("BENCH_PARAM_size");
    if (env_val) N = atoi(env_val);
    env_val = getenv("BENCH_PARAM_block_size");
    if (env_val) block_size = atoi(env_val);
    env_val = getenv("BENCH_PARAM_alpha");
    if (env_val) alpha = (float)atof(env_val);
    env_val = getenv("BENCH_PARAM_beta");
    if (env_val) beta = (float)atof(env_val);
    env_val = getenv("BENCH_PARAM_precision");
    if (env_val) precision = env_val;
    int num_warmup = 0;

    size_t bytes = (size_t)N * N * sizeof(float);

    // -------------------------------------------------------------------
    // Metal device setup
    // -------------------------------------------------------------------
    id<MTLDevice> device = MTLCreateSystemDefaultDevice();
    if (!device) {
        fprintf(stderr, "Error: no Metal device found\n");
        return EXIT_FAILURE;
    }
    fprintf(stderr, "Device: %s\n", device.name.UTF8String);
    fprintf(stderr, "2MM: D=alpha*A*B+beta*D, E=alpha*C*D+beta*E  |  N=%d  |  "
            "alpha=%.1f  beta=%.1f warmup + %d timed\n\n",
            N, alpha, beta, num_warmup);

    id<MTLCommandQueue> queue = [device newCommandQueue];

    // -------------------------------------------------------------------
    // Compile Metal shader
    // -------------------------------------------------------------------
    NSError *err = nil;
    id<MTLLibrary> library = nil;

    NSString *exePath = [[NSBundle mainBundle] executablePath];
    NSString *dir     = [exePath stringByDeletingLastPathComponent];
    NSString *srcPath = [dir stringByAppendingPathComponent:@"polybench_2mm.metal"];

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
        fprintf(stderr, "Error: polybench_2mm.metal not found at %s\n",
                srcPath.UTF8String);
        return EXIT_FAILURE;
    }

    id<MTLFunction> fn = [library newFunctionWithName:@"mm_kernel"];
    if (!fn) {
        fprintf(stderr, "Error: kernel 'mm_kernel' not found\n");
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
    // Allocate Metal buffers
    // -------------------------------------------------------------------
    id<MTLBuffer> buf_A = [device newBufferWithLength:bytes
                                              options:MTLResourceStorageModeShared];
    id<MTLBuffer> buf_B = [device newBufferWithLength:bytes
                                              options:MTLResourceStorageModeShared];
    id<MTLBuffer> buf_C = [device newBufferWithLength:bytes
                                              options:MTLResourceStorageModeShared];
    id<MTLBuffer> buf_D = [device newBufferWithLength:bytes
                                              options:MTLResourceStorageModeShared];
    id<MTLBuffer> buf_E = [device newBufferWithLength:bytes
                                              options:MTLResourceStorageModeShared];

    // Keep backup copies for D and E to reset each iteration
    float* h_D_backup = (float*)malloc(bytes);
    float* h_E_backup = (float*)malloc(bytes);

    // Initialize matrices
    float* h_A = (float*)buf_A.contents;
    float* h_B = (float*)buf_B.contents;
    float* h_C = (float*)buf_C.contents;
    float* h_D = (float*)buf_D.contents;
    float* h_E = (float*)buf_E.contents;

    srand(42);
    for (int i = 0; i < N * N; ++i) {
        h_A[i] = (float)(rand() % 100) / 10.0f;
        h_B[i] = (float)(rand() % 100) / 10.0f;
        h_C[i] = (float)(rand() % 100) / 10.0f;
        h_D[i] = (float)(rand() % 100) / 10.0f;
        h_E[i] = (float)(rand() % 100) / 10.0f;
    }
    memcpy(h_D_backup, h_D, bytes);
    memcpy(h_E_backup, h_E, bytes);

    // Kernel parameters
    uint32_t params_data[4] = { (uint32_t)N, 0, 0, 0 };
    float    ab_data[2]     = { alpha, beta };

    NSUInteger tg_x = 16, tg_y = 16;
    NSUInteger grid_x = (NSUInteger)((N + 15) / 16) * 16;
    NSUInteger grid_y = (NSUInteger)((N + 15) / 16) * 16;

    // Dispatch one matrix multiply: Out = alpha * P * Q + beta * Out
    auto dispatch_mm = [&](id<MTLBuffer> bufP, id<MTLBuffer> bufQ,
                           id<MTLBuffer> bufOut) {
        id<MTLCommandBuffer>         cb  = [queue commandBuffer];
        id<MTLComputeCommandEncoder> enc = [cb computeCommandEncoder];
        [enc setComputePipelineState:pso];
        [enc setBuffer:bufP   offset:0 atIndex:0];
        [enc setBuffer:bufQ   offset:0 atIndex:1];
        [enc setBuffer:bufOut offset:0 atIndex:2];
        [enc setBytes:params_data length:sizeof(params_data) atIndex:3];
        [enc setBytes:ab_data     length:sizeof(ab_data)     atIndex:4];
        [enc dispatchThreads:MTLSizeMake(grid_x, grid_y, 1)
            threadsPerThreadgroup:MTLSizeMake(tg_x, tg_y, 1)];
        [enc endEncoding];
        [cb commit];
        [cb waitUntilCompleted];
    };

    auto run_2mm = [&]() {
        // Reset D and E
        memcpy(h_D, h_D_backup, bytes);
        memcpy(h_E, h_E_backup, bytes);

        // D = alpha * A * B + beta * D
        dispatch_mm(buf_A, buf_B, buf_D);
        // E = alpha * C * D + beta * E
        dispatch_mm(buf_C, buf_D, buf_E);
    };

    // -------------------------------------------------------------------
    // Warmup
    // -------------------------------------------------------------------
    for (int w = 0; w < num_warmup; ++w) {
        run_2mm();
    }

    // -------------------------------------------------------------------
    // Timed iterations
    // -------------------------------------------------------------------
    std::vector<double> times(1);
    for (int i = 0; i < 1; ++i) {
        uint64_t t0 = mach_absolute_time();
        run_2mm();
        uint64_t t1 = mach_absolute_time();
        times[i] = ticks_to_ms(t1 - t0);
    }

    double sum_ms = 0.0, mn = DBL_MAX, mx = 0.0;
    for (int i = 0; i < 1; ++i) {
        sum_ms += times[i];
        if (times[i] < mn) mn = times[i];
        if (times[i] > mx) mx = times[i];
    }
    double avg_ms = sum_ms / 1;

    double gflops = 4.0 * (double)N * (double)N * (double)N
                    / (avg_ms * 1e-3) / 1e9;

    fprintf(stderr, "Performance: %.2f GFLOPS  (avg %.4f ms, min %.4f ms, max %.4f ms)\n",
            gflops, avg_ms, mn, mx);

    printf("{\"type\":\"kernel\",\"name\":\"mm_kernel_1\",\"time_ms\":%.6f,"
           "\"params\":{\"size\":%d\"block_size\":%d,"
           "\"alpha\":%.2f,\"beta\":%.2f,\"precision\":\"%s\"}}\n",
           avg_ms * 0.5, N, block_size, alpha, beta, precision);

    printf("{\"type\":\"kernel\",\"name\":\"mm_kernel_2\",\"time_ms\":%.6f,"
           "\"params\":{\"size\":%d\"block_size\":%d,"
           "\"alpha\":%.2f,\"beta\":%.2f,\"precision\":\"%s\"}}\n",
           avg_ms * 0.5, N, block_size, alpha, beta, precision);

    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"gflops\",\"value\":%.2f}]}\n",
           avg_ms, gflops);

    free(h_D_backup);
    free(h_E_backup);

    return EXIT_SUCCESS;

    } // @autoreleasepool
}
