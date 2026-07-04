/**
 * rodinia_gaussian_metal.mm — Apple Metal host for the Rodinia Gaussian benchmark.
 *
 * Gaussian elimination with back-substitution to solve Ax=b for an NxN matrix.
 * Derived from the Rodinia benchmark suite Gaussian workload.
 *
 * Two GPU kernels (fan1_kernel, fan2_kernel) are called iteratively for each
 * pivot step. After forward elimination, back-substitution is done on the CPU.
 *
 * Usage:
 *   ./rodinia_gaussian [--size N]
 *
 *   --size N         Matrix dimension N (N×N)  (default: 512)
 *   --iterations I   Timed iterations          (default: 3)
 *
 * Output (stdout): CSV — gaussian,<N>,<time_ms>,<GFLOPS>
 * Output (stderr): device info, timing details, verification result
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
// CPU back-substitution: solve upper-triangular system Ux = b
// ---------------------------------------------------------------------------
static void backSubstitution(const float* a, const float* b, float* x, int N)
{
    for (int i = N - 1; i >= 0; --i) {
        double sum = (double)b[i];
        for (int j = i + 1; j < N; ++j) {
            sum -= (double)a[i * N + j] * (double)x[j];
        }
        x[i] = (float)(sum / (double)a[i * N + i]);
    }
}

// ---------------------------------------------------------------------------
// main
// ---------------------------------------------------------------------------

int main(int argc, char* argv[])
{
    @autoreleasepool {
    setvbuf(stdout, NULL, _IONBF, 0);

    // Defaults
    int N     = 512;
    int block_size_param = 256;
    const char* verify = "true";

    // Command-line fallback
    N     = parseIntParam(argc, argv, "--size", N);

    // Environment variable overrides
    const char* env_val;
    env_val = getenv("BENCH_PARAM_size");
    if (env_val) N = atoi(env_val);
    env_val = getenv("BENCH_PARAM_block_size");
    if (env_val) block_size_param = atoi(env_val);
    env_val = getenv("BENCH_PARAM_verify");
    if (env_val) verify = env_val;
    int num_warmup = 0;

    size_t bytes_a = (size_t)N * N * sizeof(float);
    size_t bytes_b = (size_t)N * sizeof(float);

    // -------------------------------------------------------------------
    // Metal device setup
    // -------------------------------------------------------------------
    id<MTLDevice> device = MTLCreateSystemDefaultDevice();
    if (!device) {
        fprintf(stderr, "Error: no Metal device found\n");
        return EXIT_FAILURE;
    }
    fprintf(stderr, "Device: %s\n", device.name.UTF8String);
    fprintf(stderr, "Matrix size: %d×%d\n\n", N, N);

    id<MTLCommandQueue> queue = [device newCommandQueue];

    // -------------------------------------------------------------------
    // Compile Metal shader from source file next to the binary
    // -------------------------------------------------------------------
    NSError *err = nil;
    id<MTLLibrary> library = nil;

    NSString *exePath = [[NSBundle mainBundle] executablePath];
    NSString *dir     = [exePath stringByDeletingLastPathComponent];
    NSString *srcPath = [dir stringByAppendingPathComponent:@"rodinia_gaussian.metal"];

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
        fprintf(stderr, "Error: rodinia_gaussian.metal not found at %s\n",
                srcPath.UTF8String);
        return EXIT_FAILURE;
    }

    // Create pipeline states for both kernels
    id<MTLFunction> fn1 = [library newFunctionWithName:@"fan1_kernel"];
    if (!fn1) {
        fprintf(stderr, "Error: kernel 'fan1_kernel' not found\n");
        return EXIT_FAILURE;
    }
    id<MTLComputePipelineState> pso_fan1 =
        [device newComputePipelineStateWithFunction:fn1 error:&err];
    if (!pso_fan1) {
        fprintf(stderr, "Error creating pipeline for fan1_kernel: %s\n",
                err.localizedDescription.UTF8String);
        return EXIT_FAILURE;
    }

    id<MTLFunction> fn2 = [library newFunctionWithName:@"fan2_kernel"];
    if (!fn2) {
        fprintf(stderr, "Error: kernel 'fan2_kernel' not found\n");
        return EXIT_FAILURE;
    }
    id<MTLComputePipelineState> pso_fan2 =
        [device newComputePipelineStateWithFunction:fn2 error:&err];
    if (!pso_fan2) {
        fprintf(stderr, "Error creating pipeline for fan2_kernel: %s\n",
                err.localizedDescription.UTF8String);
        return EXIT_FAILURE;
    }

    // -------------------------------------------------------------------
    // Allocate Metal buffers (shared memory — accessible from CPU and GPU)
    // -------------------------------------------------------------------
    id<MTLBuffer> buf_a = [device newBufferWithLength:bytes_a
                                              options:MTLResourceStorageModeShared];
    id<MTLBuffer> buf_b = [device newBufferWithLength:bytes_b
                                              options:MTLResourceStorageModeShared];
    id<MTLBuffer> buf_m = [device newBufferWithLength:bytes_a
                                              options:MTLResourceStorageModeShared];

    // Initialize matrix A and vector b
    float* h_a = (float*)buf_a.contents;
    float* h_b = (float*)buf_b.contents;
    float* h_m = (float*)buf_m.contents;

    // Diagonally dominant matrix for numerical stability
    srand(42);
    for (int i = 0; i < N * N; ++i) {
        h_a[i] = (float)(rand() % 10) / 10.0f;
    }
    for (int i = 0; i < N; ++i) {
        h_a[i * N + i] += (float)N;
    }
    for (int i = 0; i < N; ++i) {
        h_b[i] = (float)(rand() % 10) / 10.0f + 1.0f;
    }
    memset(h_m, 0, bytes_a);

    // Save originals for correctness verification
    std::vector<float> h_a_orig(N * N);
    std::vector<float> h_b_orig(N);
    memcpy(h_a_orig.data(), h_a, bytes_a);
    memcpy(h_b_orig.data(), h_b, bytes_b);

    // Thread configuration for fan1 (1D)
    NSUInteger tg_size1 = MIN((NSUInteger)256, pso_fan1.maxTotalThreadsPerThreadgroup);
    // Thread configuration for fan2 (2D): 16x16=256
    NSUInteger tg_w2 = 16, tg_h2 = 16;

    // Lambda: run one full forward elimination pass
    auto run_forward_elimination = [&]() {
        // All steps in a single command buffer for efficiency
        id<MTLCommandBuffer> cb = [queue commandBuffer];

        for (int t = 0; t < N - 1; ++t) {
            NSUInteger remaining = (NSUInteger)(N - t - 1);
            uint32_t params[4] = { (uint32_t)N, (uint32_t)t, 0, 0 };

            // fan1: compute multipliers
            {
                id<MTLComputeCommandEncoder> enc = [cb computeCommandEncoder];
                [enc setComputePipelineState:pso_fan1];
                [enc setBuffer:buf_m offset:0 atIndex:0];
                [enc setBuffer:buf_a offset:0 atIndex:1];
                [enc setBytes:params length:sizeof(params) atIndex:2];
                [enc dispatchThreads:MTLSizeMake(remaining, 1, 1)
                    threadsPerThreadgroup:MTLSizeMake(tg_size1, 1, 1)];
                [enc endEncoding];
            }

            // fan2: update submatrix
            {
                id<MTLComputeCommandEncoder> enc = [cb computeCommandEncoder];
                [enc setComputePipelineState:pso_fan2];
                [enc setBuffer:buf_m offset:0 atIndex:0];
                [enc setBuffer:buf_a offset:0 atIndex:1];
                [enc setBuffer:buf_b offset:0 atIndex:2];
                [enc setBytes:params length:sizeof(params) atIndex:3];
                [enc dispatchThreads:MTLSizeMake(remaining, remaining, 1)
                    threadsPerThreadgroup:MTLSizeMake(tg_w2, tg_h2, 1)];
                [enc endEncoding];
            }
        }

        [cb commit];
        [cb waitUntilCompleted];
    };

    // -------------------------------------------------------------------
    // Warmup
    // -------------------------------------------------------------------
    for (int w = 0; w < num_warmup; ++w) {
        run_forward_elimination();
    }

    // -------------------------------------------------------------------
    // Timed iterations
    // -------------------------------------------------------------------
    std::vector<double> times(1);

    for (int iter = 0; iter < 1; ++iter) {
        // Reset buffers to original data (outside timing)
        memcpy(h_a, h_a_orig.data(), bytes_a);
        memcpy(h_b, h_b_orig.data(), bytes_b);
        memset(h_m, 0, bytes_a);

        uint64_t t0 = mach_absolute_time();
        run_forward_elimination();
        uint64_t t1 = mach_absolute_time();
        times[iter] = ticks_to_ms(t1 - t0);
    }

    // -------------------------------------------------------------------
    // CPU back-substitution on the factored system (last iteration result)
    // -------------------------------------------------------------------
    std::vector<float> h_x(N);
    backSubstitution(h_a, h_b, h_x.data(), N);

    // -------------------------------------------------------------------
    // Correctness check: compute ||A_orig * x - b_orig|| / ||b_orig||
    // -------------------------------------------------------------------
    double norm_res = 0.0, norm_b = 0.0;
    for (int i = 0; i < N; ++i) {
        double res = -(double)h_b_orig[i];
        for (int j = 0; j < N; ++j) {
            res += (double)h_a_orig[i * N + j] * (double)h_x[j];
        }
        norm_res += res * res;
        norm_b   += (double)h_b_orig[i] * (double)h_b_orig[i];
    }
    double rel_err = sqrt(norm_res / (norm_b + 1e-30));
    bool pass = (rel_err < 1e-4);
    fprintf(stderr, "Verification: rel_err = %.2e — %s\n\n",
            rel_err, pass ? "PASS" : "FAIL");

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

    // GFLOPS: 2/3 * N^3 floating-point ops for forward elimination
    double flops  = (2.0 / 3.0) * (double)N * (double)N * (double)N;
    double gflops = flops / (avg_ms * 1e-3) / 1e9;

    fprintf(stderr, "Performance: %.2f GFLOPS  (avg %.4f ms, min %.4f ms, max %.4f ms, stddev %.4f ms)\n",
            gflops, avg_ms, mn, mx, stddev);

    double total_ms = sum_ms;

    // JSON-lines kernel events
    printf("{\"type\":\"kernel\",\"name\":\"fan1\",\"time_ms\":%.6f,"
           "\"params\":{\"size\":%d\"block_size\":%d,\"verify\":\"%s\"}}\n",
           avg_ms, N, block_size_param, verify);

    printf("{\"type\":\"kernel\",\"name\":\"fan2\",\"time_ms\":%.6f,"
           "\"params\":{\"size\":%d\"block_size\":%d,\"verify\":\"%s\"}}\n",
           avg_ms, N, block_size_param, verify);

    // JSON-lines summary event
    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"gflops\",\"value\":%.2f}]}\n",
           total_ms, gflops);

    return pass ? EXIT_SUCCESS : EXIT_FAILURE;

    } // @autoreleasepool
}
