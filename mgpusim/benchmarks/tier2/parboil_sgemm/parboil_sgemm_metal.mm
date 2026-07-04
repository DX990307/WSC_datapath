/**
 * parboil_sgemm_metal.mm — Apple Metal host for the Parboil SGEMM benchmark.
 *
 * Tiled single-precision GEMM: C = alpha*A*B + beta*C  (N×N matrices)
 * Measures compute throughput in GFLOPS.
 *
 * Usage:
 *   ./parboil_sgemm [--size N]
 *
 *   --size N         Matrix dimension N (default: 1024)
 *   --iterations I   Timed iterations  (default: 5)
 *
 * Output (stdout): parboil_sgemm,<N>,<GFLOPS>
 * Output (stderr): human-readable timing and performance info
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
// CPU reference for correctness check
// ---------------------------------------------------------------------------

static void sgemm_cpu(const float* A, const float* B, float* C,
                      int N, float alpha, float beta)
{
    for (int i = 0; i < N; ++i) {
        for (int j = 0; j < N; ++j) {
            float s = 0.0f;
            for (int k = 0; k < N; ++k) {
                s += A[i * N + k] * B[k * N + j];
            }
            C[i * N + j] = alpha * s + beta * C[i * N + j];
        }
    }
}

// ---------------------------------------------------------------------------
// main
// ---------------------------------------------------------------------------

int main(int argc, char* argv[])
{
    @autoreleasepool {
    setvbuf(stdout, NULL, _IONBF, 0);

    // Default values from params.json
    int N     = 1024;
    int block_size = 256;
    const char* precision = "float";

    // Command-line args as fallback
    N     = parseIntParam(argc, argv, "--size", N);
    block_size = parseIntParam(argc, argv, "--block_size", block_size);

    // Env vars override
    const char* env_val;
    env_val = getenv("BENCH_PARAM_size");
    if (env_val) N = atoi(env_val);
    env_val = getenv("BENCH_PARAM_block_size");
    if (env_val) block_size = atoi(env_val);
    env_val = getenv("BENCH_PARAM_precision");
    if (env_val) precision = env_val;
    int num_warmup = 0;

    float alpha = 1.5f;
    float beta  = 1.2f;

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
    fprintf(stderr, "Matrix size: %d×%d  |  alpha=%.1f  beta=%.1f\n\n",
            N, N, alpha, beta);

    id<MTLCommandQueue> queue = [device newCommandQueue];

    // -------------------------------------------------------------------
    // Compile Metal shader from source file next to the binary
    // -------------------------------------------------------------------
    NSError *err = nil;
    id<MTLLibrary> library = nil;

    NSString *exePath = [[NSBundle mainBundle] executablePath];
    NSString *dir     = [exePath stringByDeletingLastPathComponent];
    NSString *srcPath = [dir stringByAppendingPathComponent:@"parboil_sgemm.metal"];

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
        fprintf(stderr, "Error: parboil_sgemm.metal not found at %s\n",
                srcPath.UTF8String);
        return EXIT_FAILURE;
    }

    id<MTLFunction> fn = [library newFunctionWithName:@"sgemm_kernel"];
    if (!fn) {
        fprintf(stderr, "Error: kernel 'sgemm_kernel' not found\n");
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
    // Allocate Metal buffers (shared memory for easy CPU init)
    // -------------------------------------------------------------------
    id<MTLBuffer> buf_A = [device newBufferWithLength:bytes
                                              options:MTLResourceStorageModeShared];
    id<MTLBuffer> buf_B = [device newBufferWithLength:bytes
                                              options:MTLResourceStorageModeShared];
    id<MTLBuffer> buf_C = [device newBufferWithLength:bytes
                                              options:MTLResourceStorageModeShared];

    float* h_A = (float*)buf_A.contents;
    float* h_B = (float*)buf_B.contents;
    float* h_C = (float*)buf_C.contents;

    // Initialize inputs
    for (int i = 0; i < N * N; ++i)
        h_A[i] = (float)(i % 1000) * 0.001f;
    for (int i = 0; i < N * N; ++i)
        h_B[i] = (float)((i + 37) % 1000) * 0.001f;
    for (int i = 0; i < N * N; ++i)
        h_C[i] = (float)(i % 100) * 0.01f;

    // Save initial C for reset between iterations
    std::vector<float> h_C_init(h_C, h_C + (size_t)N * N);

    // Kernel parameters
    uint32_t params_data[4]  = { (uint32_t)N, 0, 0, 0 };
    float    ab_data[2]      = { alpha, beta };

    // Threadgroup size: 16×16
    NSUInteger tg_x = 16, tg_y = 16;
    // Grid: round up to multiples of tile size
    NSUInteger grid_x = (NSUInteger)((N + 15) / 16) * 16;
    NSUInteger grid_y = (NSUInteger)((N + 15) / 16) * 16;

    auto dispatch_sgemm = [&]() {
        // Reset C to initial state (beta != 0 so we must reset to avoid accumulation)
        memcpy(h_C, h_C_init.data(), bytes);

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
    // Warmup
    // -------------------------------------------------------------------
    for (int w = 0; w < num_warmup; ++w) {
        dispatch_sgemm();
    }

    // -------------------------------------------------------------------
    // Timed iterations
    // -------------------------------------------------------------------
    std::vector<double> times(1);
    for (int i = 0; i < 1; ++i) {
        uint64_t t0 = mach_absolute_time();
        dispatch_sgemm();
        uint64_t t1 = mach_absolute_time();
        times[i] = ticks_to_ms(t1 - t0);
    }

    // Compute statistics
    double sum_t = 0.0, mn = DBL_MAX, mx = 0.0;
    for (int i = 0; i < 1; ++i) {
        sum_t += times[i];
        if (times[i] < mn) mn = times[i];
        if (times[i] > mx) mx = times[i];
    }
    double avg_ms = sum_t / 1;

    // GFLOPS: 2*N*N*N multiply-adds
    double gflops = 2.0 * (double)N * (double)N * (double)N
                    / (avg_ms * 1e-3) / 1e9;

    // JSON-lines output
    printf("{\"type\":\"kernel\",\"name\":\"sgemm_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"size\":%d\"block_size\":%d,"
           "\"precision\":\"%s\"}}\n",
           avg_ms, N, block_size, precision);

    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"gflops\",\"value\":%.2f}]}\n",
           sum_t, gflops);

    // Human-readable stderr
    fprintf(stderr, "Performance: %.2f GFLOPS  (avg %.4f ms, min %.4f ms, max %.4f ms)\n",
            gflops, avg_ms, mn, mx);

    // -------------------------------------------------------------------
    // Correctness check (only for small N)
    // -------------------------------------------------------------------
    if (N <= 256) {
        const float* gpu_C = (const float*)buf_C.contents;

        // Build CPU reference
        std::vector<float> ref_C(h_C_init);
        sgemm_cpu(h_A, h_B, ref_C.data(), N, alpha, beta);

        int errors = 0;
        for (int i = 0; i < N * N; ++i) {
            float diff = fabsf(gpu_C[i] - ref_C[i]);
            if (diff > 1e-3f * fabsf(ref_C[i]) + 1e-4f) {
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
