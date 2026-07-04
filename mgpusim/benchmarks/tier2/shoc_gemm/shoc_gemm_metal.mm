/**
 * shoc_gemm_metal.mm — Apple Metal host for the SHOC GEMM benchmark.
 *
 * Dense matrix multiply: C = alpha * A * B + beta * C  (N×N, single-precision)
 * Measures peak compute throughput in GFLOPS.
 *
 * Parameters are read from BENCH_PARAM_* environment variables,
 * with command-line --flags as fallback.
 *
 * Output (stdout): JSON-lines protocol
 * Output (stderr): Human-readable diagnostics
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

static const char* parseStrParam(int argc, char** argv, const char* name,
                                 const char* defaultVal) {
    for (int i = 1; i < argc - 1; ++i) {
        if (strcmp(argv[i], name) == 0) {
            return argv[i + 1];
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
    int size       = 512;
    int block_size = 16;
    const char* precision = "float";

    // Command-line fallback
    size       = parseIntParam(argc, argv, "--size", size);
    block_size = parseIntParam(argc, argv, "--block_size", block_size);
    precision  = parseStrParam(argc, argv, "--precision", precision);

    // Environment variable overrides
    const char* env_val;
    env_val = getenv("BENCH_PARAM_size");
    if (env_val) size = atoi(env_val);
    env_val = getenv("BENCH_PARAM_block_size");
    if (env_val) block_size = atoi(env_val);
    env_val = getenv("BENCH_PARAM_precision");
    if (env_val) precision = env_val;
    int num_warmup = 0;

    int M = size, N = size, K = size;
    float alpha = 1.0f, beta = 0.0f;

    size_t bytesA = (size_t)M * K * sizeof(float);
    size_t bytesB = (size_t)K * N * sizeof(float);
    size_t bytesC = (size_t)M * N * sizeof(float);

    // -------------------------------------------------------------------
    // Metal device setup
    // -------------------------------------------------------------------
    id<MTLDevice> device = MTLCreateSystemDefaultDevice();
    if (!device) {
        fprintf(stderr, "Error: no Metal device found\n");
        return EXIT_FAILURE;
    }
    fprintf(stderr, "Device: %s\n", device.name.UTF8String);
    fprintf(stderr, "Matrix size: %d×%d\n\n", size, size);

    id<MTLCommandQueue> queue = [device newCommandQueue];

    // -------------------------------------------------------------------
    // Compile Metal shader from source file next to the binary
    // -------------------------------------------------------------------
    NSError *err = nil;
    id<MTLLibrary> library = nil;

    NSString *exePath = [[NSBundle mainBundle] executablePath];
    NSString *dir     = [exePath stringByDeletingLastPathComponent];
    NSString *srcPath = [dir stringByAppendingPathComponent:@"shoc_gemm.metal"];

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
        fprintf(stderr, "Error: shoc_gemm.metal not found at %s\n",
                srcPath.UTF8String);
        return EXIT_FAILURE;
    }

    id<MTLFunction> fn = [library newFunctionWithName:@"gemm_kernel"];
    if (!fn) {
        fprintf(stderr, "Error: kernel 'gemm_kernel' not found\n");
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
    // Allocate buffers
    // -------------------------------------------------------------------
    id<MTLBuffer> buf_A = [device newBufferWithLength:bytesA
                                              options:MTLResourceStorageModeShared];
    id<MTLBuffer> buf_B = [device newBufferWithLength:bytesB
                                              options:MTLResourceStorageModeShared];
    id<MTLBuffer> buf_C = [device newBufferWithLength:bytesC
                                              options:MTLResourceStorageModeShared];

    float* h_A = (float*)buf_A.contents;
    float* h_B = (float*)buf_B.contents;
    float* h_C = (float*)buf_C.contents;

    for (int i = 0; i < M * K; ++i)
        h_A[i] = (float)(i % 1000) * 0.001f;
    for (int i = 0; i < K * N; ++i)
        h_B[i] = (float)((i + 37) % 1000) * 0.001f;
    for (int i = 0; i < M * N; ++i)
        h_C[i] = 0.0f;

    uint32_t dims_data[4]  = { (uint32_t)M, (uint32_t)N, (uint32_t)K, 0 };
    float    ab_data[2]    = { alpha, beta };

    NSUInteger tg_x = 16, tg_y = 16;
    NSUInteger grid_x = (NSUInteger)((N + 15) / 16) * 16;
    NSUInteger grid_y = (NSUInteger)((M + 15) / 16) * 16;

    auto dispatch_gemm = [&]() {
        memset(h_C, 0, bytesC);

        id<MTLCommandBuffer>         cb  = [queue commandBuffer];
        id<MTLComputeCommandEncoder> enc = [cb computeCommandEncoder];
        [enc setComputePipelineState:pso];
        [enc setBuffer:buf_A offset:0 atIndex:0];
        [enc setBuffer:buf_B offset:0 atIndex:1];
        [enc setBuffer:buf_C offset:0 atIndex:2];
        [enc setBytes:dims_data length:sizeof(dims_data) atIndex:3];
        [enc setBytes:ab_data   length:sizeof(ab_data)   atIndex:4];
        [enc dispatchThreads:MTLSizeMake(grid_x, grid_y, 1)
            threadsPerThreadgroup:MTLSizeMake(tg_x, tg_y, 1)];
        [enc endEncoding];
        [cb commit];
        [cb waitUntilCompleted];
    };

    // -------------------------------------------------------------------
    // Warmup
    // -------------------------------------------------------------------
    for (int w = 0; w < num_warmup; ++w) {
        dispatch_gemm();
    }

    // -------------------------------------------------------------------
    // Timed iterations
    // -------------------------------------------------------------------
    std::vector<double> times(1);
    for (int i = 0; i < 1; ++i) {
        uint64_t t0 = mach_absolute_time();
        dispatch_gemm();
        uint64_t t1 = mach_absolute_time();
        times[i] = ticks_to_ms(t1 - t0);
    }

    double sum_t = 0.0, mn = DBL_MAX, mx = 0.0;
    for (int i = 0; i < 1; ++i) {
        sum_t += times[i];
        if (times[i] < mn) mn = times[i];
        if (times[i] > mx) mx = times[i];
    }
    double avg_ms = sum_t / 1;
    double total_ms = sum_t;

    double gflops = 2.0 * (double)M * (double)N * (double)K
                    / (avg_ms * 1e-3) / 1e9;

    // JSON-lines kernel event
    printf("{\"type\":\"kernel\",\"name\":\"gemm_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"size\":%d,\"block_size\":%d"
           "\"precision\":\"%s\"}}\n",
           avg_ms, size, block_size, precision);

    // JSON-lines summary event
    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"gflops\",\"value\":%.2f}]}\n",
           total_ms, gflops);

    fprintf(stderr, "Performance: %.2f GFLOPS  (avg %.4f ms)\n", gflops, avg_ms);

    // -------------------------------------------------------------------
    // Verification (only for small sizes)
    // -------------------------------------------------------------------
    if (size <= 256) {
        const float* gpu_C = (const float*)buf_C.contents;

        std::vector<float> ref_C((size_t)M * N, 0.0f);
        for (int i = 0; i < M; ++i) {
            for (int j = 0; j < N; ++j) {
                float s = 0.0f;
                for (int k = 0; k < K; ++k) {
                    s += h_A[i * K + k] * h_B[k * N + j];
                }
                ref_C[i * N + j] = alpha * s;
            }
        }

        int errors = 0;
        for (int i = 0; i < M * N; ++i) {
            float diff = fabsf(gpu_C[i] - ref_C[i]);
            if (diff > 1e-3f * fabsf(ref_C[i]) + 1e-5f) {
                if (errors < 10) {
                    fprintf(stderr, "Mismatch at %d: got %f, expected %f\n",
                            i, gpu_C[i], ref_C[i]);
                }
                errors++;
            }
        }
        if (errors > 0)
            fprintf(stderr, "FAIL: %d errors\n", errors);
        else
            fprintf(stderr, "PASS\n");
    } else {
        fprintf(stderr, "Skipping verification for large size\n");
    }

    return EXIT_SUCCESS;

    } // @autoreleasepool
}
