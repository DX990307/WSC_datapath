/**
 * polybench_mvt_metal.mm — Apple Metal host for the PolyBench MVT benchmark.
 *
 * Computes:
 *   x1 = A * y1   (matrix-vector product)
 *   x2 = A^T * y2 (transpose matrix-vector product)
 * where A is N×N and x1, x2, y1, y2 are N-vectors.
 *
 * Measures memory bandwidth in GB/s (A read twice per iteration).
 *
 * Usage:
 *   ./polybench_mvt [--n N]
 *
 *   --n N            Matrix dimension (default: 4096)
 *   --iterations I   Timed iterations (default: 5)
 *
 * Output (stdout): CSV — mvt,<N>,<time_ms>,<GB/s>
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

// ---------------------------------------------------------------------------
// CPU reference for correctness check
// ---------------------------------------------------------------------------

static void cpu_mvt(const float* A, const float* y1, const float* y2,
                    float* x1_ref, float* x2_ref, int n)
{
    // x1 = A * y1
    for (int i = 0; i < n; i++) {
        x1_ref[i] = 0.0f;
        for (int j = 0; j < n; j++)
            x1_ref[i] += A[i * n + j] * y1[j];
    }
    // x2 = A^T * y2
    for (int j = 0; j < n; j++) {
        x2_ref[j] = 0.0f;
        for (int i = 0; i < n; i++)
            x2_ref[j] += A[i * n + j] * y2[i];
    }
}

// ---------------------------------------------------------------------------
// main
// ---------------------------------------------------------------------------

int main(int argc, char* argv[])
{
    @autoreleasepool {
    setvbuf(stdout, NULL, _IONBF, 0);

    int n     = parseIntParam(argc, argv, "--n", 4096);
    int block_size_param = 256;
    const char* precision = "float";

    const char* env_val;
    env_val = getenv("BENCH_PARAM_n");
    if (env_val) n = atoi(env_val);
    env_val = getenv("BENCH_PARAM_block_size");
    if (env_val) block_size_param = atoi(env_val);
    env_val = getenv("BENCH_PARAM_precision");
    if (env_val) precision = env_val;
    int num_warmup = 0;

    size_t bytes_A = (size_t)n * n * sizeof(float);
    size_t bytes_v = (size_t)n * sizeof(float);

    // -------------------------------------------------------------------
    // Metal device setup
    // -------------------------------------------------------------------
    id<MTLDevice> device = MTLCreateSystemDefaultDevice();
    if (!device) {
        fprintf(stderr, "Error: no Metal device found\n");
        return EXIT_FAILURE;
    }
    fprintf(stderr, "Device: %s\n", device.name.UTF8String);
    fprintf(stderr, "Matrix size: %d×%d\n\n", n, n);

    id<MTLCommandQueue> queue = [device newCommandQueue];

    // -------------------------------------------------------------------
    // Compile Metal shader from source file next to the binary
    // -------------------------------------------------------------------
    NSError *err = nil;
    id<MTLLibrary> library = nil;

    NSString *exePath = [[NSBundle mainBundle] executablePath];
    NSString *dir     = [exePath stringByDeletingLastPathComponent];
    NSString *srcPath = [dir stringByAppendingPathComponent:@"polybench_mvt.metal"];

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
        fprintf(stderr, "Error: polybench_mvt.metal not found at %s\n",
                srcPath.UTF8String);
        return EXIT_FAILURE;
    }

    // Create pipeline states for both kernels
    id<MTLFunction> fn1 = [library newFunctionWithName:@"mvt_kernel1"];
    if (!fn1) {
        fprintf(stderr, "Error: kernel 'mvt_kernel1' not found\n");
        return EXIT_FAILURE;
    }
    id<MTLComputePipelineState> pso1 =
        [device newComputePipelineStateWithFunction:fn1 error:&err];
    if (!pso1) {
        fprintf(stderr, "Error creating pipeline for kernel1: %s\n",
                err.localizedDescription.UTF8String);
        return EXIT_FAILURE;
    }

    id<MTLFunction> fn2 = [library newFunctionWithName:@"mvt_kernel2"];
    if (!fn2) {
        fprintf(stderr, "Error: kernel 'mvt_kernel2' not found\n");
        return EXIT_FAILURE;
    }
    id<MTLComputePipelineState> pso2 =
        [device newComputePipelineStateWithFunction:fn2 error:&err];
    if (!pso2) {
        fprintf(stderr, "Error creating pipeline for kernel2: %s\n",
                err.localizedDescription.UTF8String);
        return EXIT_FAILURE;
    }

    // -------------------------------------------------------------------
    // Allocate Metal buffers (shared memory)
    // -------------------------------------------------------------------
    id<MTLBuffer> buf_A  = [device newBufferWithLength:bytes_A
                                               options:MTLResourceStorageModeShared];
    id<MTLBuffer> buf_y1 = [device newBufferWithLength:bytes_v
                                               options:MTLResourceStorageModeShared];
    id<MTLBuffer> buf_y2 = [device newBufferWithLength:bytes_v
                                               options:MTLResourceStorageModeShared];
    id<MTLBuffer> buf_x1 = [device newBufferWithLength:bytes_v
                                               options:MTLResourceStorageModeShared];
    id<MTLBuffer> buf_x2 = [device newBufferWithLength:bytes_v
                                               options:MTLResourceStorageModeShared];

    // Initialize A, y1, y2 with random floats
    float* h_A  = (float*)buf_A.contents;
    float* h_y1 = (float*)buf_y1.contents;
    float* h_y2 = (float*)buf_y2.contents;

    srand(42);
    for (int i = 0; i < n * n; ++i)
        h_A[i] = (float)(rand() % 100) / 10.0f;
    for (int j = 0; j < n; ++j) {
        h_y1[j] = (float)(rand() % 100) / 10.0f;
        h_y2[j] = (float)(rand() % 100) / 10.0f;
    }

    // Kernel parameters: dims = {n, 0, 0, 0}
    uint32_t dims_data[4] = { (uint32_t)n, 0, 0, 0 };

    // Thread configuration
    NSUInteger tg_size1 = MIN((NSUInteger)256, pso1.maxTotalThreadsPerThreadgroup);
    NSUInteger tg_size2 = MIN((NSUInteger)256, pso2.maxTotalThreadsPerThreadgroup);
    NSUInteger grid_size = (NSUInteger)n;

    auto dispatch_mvt = [&]() {
        // Kernel 1: x1 = A * y1
        {
            id<MTLCommandBuffer>         cb  = [queue commandBuffer];
            id<MTLComputeCommandEncoder> enc = [cb computeCommandEncoder];
            [enc setComputePipelineState:pso1];
            [enc setBuffer:buf_A  offset:0 atIndex:0];
            [enc setBuffer:buf_y1 offset:0 atIndex:1];
            [enc setBuffer:buf_x1 offset:0 atIndex:2];
            [enc setBytes:dims_data length:sizeof(dims_data) atIndex:3];
            [enc dispatchThreads:MTLSizeMake(grid_size, 1, 1)
                threadsPerThreadgroup:MTLSizeMake(tg_size1, 1, 1)];
            [enc endEncoding];
            [cb commit];
            [cb waitUntilCompleted];
        }
        // Kernel 2: x2 = A^T * y2
        {
            id<MTLCommandBuffer>         cb  = [queue commandBuffer];
            id<MTLComputeCommandEncoder> enc = [cb computeCommandEncoder];
            [enc setComputePipelineState:pso2];
            [enc setBuffer:buf_A  offset:0 atIndex:0];
            [enc setBuffer:buf_y2 offset:0 atIndex:1];
            [enc setBuffer:buf_x2 offset:0 atIndex:2];
            [enc setBytes:dims_data length:sizeof(dims_data) atIndex:3];
            [enc dispatchThreads:MTLSizeMake(grid_size, 1, 1)
                threadsPerThreadgroup:MTLSizeMake(tg_size2, 1, 1)];
            [enc endEncoding];
            [cb commit];
            [cb waitUntilCompleted];
        }
    };

    // -------------------------------------------------------------------
    // Warmup
    // -------------------------------------------------------------------
    for (int w = 0; w < num_warmup; ++w) {
        dispatch_mvt();
    }

    // -------------------------------------------------------------------
    // Timed iterations
    // -------------------------------------------------------------------
    std::vector<double> times(1);
    for (int i = 0; i < 1; ++i) {
        uint64_t t0 = mach_absolute_time();
        dispatch_mvt();
        uint64_t t1 = mach_absolute_time();
        times[i] = ticks_to_ms(t1 - t0);
    }

    // -------------------------------------------------------------------
    // Correctness check against CPU reference (small subset)
    // -------------------------------------------------------------------
    int check_n = (n < 8) ? n : 8;
    std::vector<float> x1_ref((size_t)n), x2_ref((size_t)n);
    cpu_mvt(h_A, h_y1, h_y2, x1_ref.data(), x2_ref.data(), n);

    float* h_x1 = (float*)buf_x1.contents;
    float* h_x2 = (float*)buf_x2.contents;
    bool pass = true;
    for (int i = 0; i < check_n; ++i) {
        float rel1 = fabsf(h_x1[i] - x1_ref[i]) / (fabsf(x1_ref[i]) + 1e-6f);
        if (rel1 > 1e-3f) {
            fprintf(stderr, "MISMATCH x1[%d]: gpu=%.6f cpu=%.6f rel=%.4e\n",
                    i, h_x1[i], x1_ref[i], rel1);
            pass = false;
        }
        float rel2 = fabsf(h_x2[i] - x2_ref[i]) / (fabsf(x2_ref[i]) + 1e-6f);
        if (rel2 > 1e-3f) {
            fprintf(stderr, "MISMATCH x2[%d]: gpu=%.6f cpu=%.6f rel=%.4e\n",
                    i, h_x2[i], x2_ref[i], rel2);
            pass = false;
        }
    }
    fprintf(stderr, "Correctness check (first %d elements): %s\n\n",
            check_n, pass ? "PASS" : "FAIL");

    // -------------------------------------------------------------------
    // Statistics
    // -------------------------------------------------------------------
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

    // GB/s: A read twice (kernel1 + kernel2), each is N*N*4 bytes
    double gb   = 2.0 * (double)n * (double)n * sizeof(float) / 1e9;
    double gbps = gb / (avg_ms * 1e-3);

    fprintf(stderr, "Bandwidth: %.2f GB/s  (avg %.4f ms, min %.4f ms, max %.4f ms, stddev %.4f ms)\n",
            gbps, avg_ms, mn, mx, stddev);

    // JSON-lines output
    printf("{\"type\":\"kernel\",\"name\":\"mvt_kernel1\",\"time_ms\":%.6f,"
           "\"params\":{\"n\":%d\"block_size\":%d,"
           "\"precision\":\"%s\"}}\n",
           avg_ms, n, block_size_param, precision);
    printf("{\"type\":\"kernel\",\"name\":\"mvt_kernel2\",\"time_ms\":%.6f,"
           "\"params\":{\"n\":%d\"block_size\":%d,"
           "\"precision\":\"%s\"}}\n",
           avg_ms, n, block_size_param, precision);
    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"bandwidth_gbps\",\"value\":%.2f}]}\n",
           avg_ms, gbps);

    return EXIT_SUCCESS;

    } // @autoreleasepool
}
