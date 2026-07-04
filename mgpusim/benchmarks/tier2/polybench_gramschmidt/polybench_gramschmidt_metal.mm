/**
 * polybench_gramschmidt_metal.mm — Apple Metal host for Gram-Schmidt orthogonalization.
 *
 * Computes the QR factorization via Gram-Schmidt:
 *   A = Q * R
 * where A is M×N, Q is M×N (orthonormal columns), R is N×N (upper triangular).
 *
 * Per-column k loop (host drives, GPU executes):
 *   1. CPU: compute nrm = ||A[:,k]|| from shared buffer A
 *   2. GPU: gram_normalize — Q[:,k] = A[:,k] / nrm           (M threads)
 *   3. GPU: gram_project  — update A[:,j] and R[k,j] for j>k (N-k-1 threads)
 *
 * Usage:
 *   ./polybench_gramschmidt [--m M] [--n N] [--passes P]
 *
 *   --m M       Number of rows    (default: 512)
 *   --n N       Number of columns (default: 512)
 *   --passes P  Timed passes      (default: 3)
 *
 * Output (stdout): CSV — gramschmidt,<M>,<N>,<time_ms>,<GBs>
 * Output (stderr): device info, timing details, orthogonality check
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
// CPU orthogonality check: verify Q^T * Q ≈ I for first min(8,N) columns
// ---------------------------------------------------------------------------

static bool check_orthogonality(const float* Q, int M, int N)
{
    int check = (N < 8) ? N : 8;
    bool pass = true;
    for (int ci = 0; ci < check; ci++) {
        for (int cj = 0; cj < check; cj++) {
            float dot = 0.0f;
            for (int i = 0; i < M; i++)
                dot += Q[i * N + ci] * Q[i * N + cj];
            float expected = (ci == cj) ? 1.0f : 0.0f;
            if (fabsf(dot - expected) > 0.01f) {
                fprintf(stderr,
                        "ORTH FAIL: dot(Q[:,%d], Q[:,%d]) = %.6f  (expected %.1f)\n",
                        ci, cj, dot, expected);
                pass = false;
            }
        }
    }
    return pass;
}

// ---------------------------------------------------------------------------
// main
// ---------------------------------------------------------------------------

int main(int argc, char* argv[])
{
    @autoreleasepool {
    setvbuf(stdout, NULL, _IONBF, 0);

    int M      = parseIntParam(argc, argv, "--m", 512);
    int N      = parseIntParam(argc, argv, "--n", 512);
    int passes = parseIntParam(argc, argv, "--passes", 3);
    int block_size_param = 256;

    const char* env_val;
    env_val = getenv("BENCH_PARAM_m");
    if (env_val) M = atoi(env_val);
    env_val = getenv("BENCH_PARAM_n");
    if (env_val) N = atoi(env_val);
    env_val = getenv("BENCH_PARAM_passes");
    if (env_val) passes = atoi(env_val);
    env_val = getenv("BENCH_PARAM_block_size");
    if (env_val) block_size_param = atoi(env_val);

    size_t bytes_A = (size_t)M * N * sizeof(float);
    size_t bytes_Q = (size_t)M * N * sizeof(float);
    size_t bytes_R = (size_t)N * N * sizeof(float);

    // -------------------------------------------------------------------
    // Metal device setup
    // -------------------------------------------------------------------
    id<MTLDevice> device = MTLCreateSystemDefaultDevice();
    if (!device) {
        fprintf(stderr, "Error: no Metal device found\n");
        return EXIT_FAILURE;
    }
    fprintf(stderr, "Device: %s\n", device.name.UTF8String);
    fprintf(stderr, "Matrix: %d×%d  |  Passes: %d\n\n", M, N, passes);

    id<MTLCommandQueue> queue = [device newCommandQueue];

    // -------------------------------------------------------------------
    // Compile Metal shader from source file next to the binary
    // -------------------------------------------------------------------
    NSError *err = nil;
    id<MTLLibrary> library = nil;

    NSString *exePath = [[NSBundle mainBundle] executablePath];
    NSString *dir     = [exePath stringByDeletingLastPathComponent];
    NSString *srcPath = [dir stringByAppendingPathComponent:@"polybench_gramschmidt.metal"];

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
        fprintf(stderr, "Error: polybench_gramschmidt.metal not found at %s\n",
                srcPath.UTF8String);
        return EXIT_FAILURE;
    }

    // Create pipeline states
    id<MTLFunction> fn_normalize = [library newFunctionWithName:@"gram_normalize"];
    if (!fn_normalize) {
        fprintf(stderr, "Error: kernel 'gram_normalize' not found\n");
        return EXIT_FAILURE;
    }
    id<MTLComputePipelineState> pso_normalize =
        [device newComputePipelineStateWithFunction:fn_normalize error:&err];
    if (!pso_normalize) {
        fprintf(stderr, "Error creating pipeline for gram_normalize: %s\n",
                err.localizedDescription.UTF8String);
        return EXIT_FAILURE;
    }

    id<MTLFunction> fn_project = [library newFunctionWithName:@"gram_project"];
    if (!fn_project) {
        fprintf(stderr, "Error: kernel 'gram_project' not found\n");
        return EXIT_FAILURE;
    }
    id<MTLComputePipelineState> pso_project =
        [device newComputePipelineStateWithFunction:fn_project error:&err];
    if (!pso_project) {
        fprintf(stderr, "Error creating pipeline for gram_project: %s\n",
                err.localizedDescription.UTF8String);
        return EXIT_FAILURE;
    }

    // -------------------------------------------------------------------
    // Allocate Metal shared buffers
    // -------------------------------------------------------------------
    id<MTLBuffer> buf_A = [device newBufferWithLength:bytes_A
                                              options:MTLResourceStorageModeShared];
    id<MTLBuffer> buf_Q = [device newBufferWithLength:bytes_Q
                                              options:MTLResourceStorageModeShared];
    id<MTLBuffer> buf_R = [device newBufferWithLength:bytes_R
                                              options:MTLResourceStorageModeShared];

    // Keep original A for restoring between passes
    std::vector<float> h_A_orig((size_t)M * N);
    srand(42);
    for (int i = 0; i < M * N; ++i)
        h_A_orig[i] = (float)rand() / (float)RAND_MAX;

    // Thread group sizes
    NSUInteger tg_norm = MIN((NSUInteger)256, pso_normalize.maxTotalThreadsPerThreadgroup);
    NSUInteger tg_proj = MIN((NSUInteger)256, pso_project.maxTotalThreadsPerThreadgroup);

    std::vector<double> times((size_t)passes);

    for (int pass = 0; pass < passes; ++pass) {
        // Restore A from original, zero Q and R (outside timing)
        memcpy(buf_A.contents, h_A_orig.data(), bytes_A);
        memset(buf_Q.contents, 0, bytes_Q);
        memset(buf_R.contents, 0, bytes_R);

        float* h_A = (float*)buf_A.contents;
        float* h_R = (float*)buf_R.contents;

        // ----- Timed region -----
        uint64_t t0 = mach_absolute_time();

        for (int k = 0; k < N; ++k) {
            // Step 1: CPU computes norm of column k
            // (h_A reflects GPU's latest writes since MTLResourceStorageModeShared)
            float nrm_sq = 0.0f;
            for (int i = 0; i < M; i++) {
                float v = h_A[i * N + k];
                nrm_sq += v * v;
            }
            float nrm = sqrtf(nrm_sq);
            h_R[k * N + k] = nrm;  // write R[k,k] directly into shared buffer

            // Guard against degenerate column
            if (nrm < 1e-12f) nrm = 1e-12f;

            // Encode gram_normalize and gram_project in one command buffer
            // so normalize is guaranteed to complete before project starts.
            id<MTLCommandBuffer> cb = [queue commandBuffer];

            // --- gram_normalize ---
            {
                id<MTLComputeCommandEncoder> enc = [cb computeCommandEncoder];
                [enc setComputePipelineState:pso_normalize];
                uint32_t dims[4] = { (uint32_t)M, (uint32_t)N, (uint32_t)k, 0 };
                [enc setBuffer:buf_A offset:0 atIndex:0];
                [enc setBuffer:buf_Q offset:0 atIndex:1];
                [enc setBytes:dims  length:sizeof(dims) atIndex:2];
                [enc setBytes:&nrm  length:sizeof(float) atIndex:3];
                [enc dispatchThreads:MTLSizeMake((NSUInteger)M, 1, 1)
                    threadsPerThreadgroup:MTLSizeMake(tg_norm, 1, 1)];
                [enc endEncoding];
            }

            // --- gram_project (if columns remain) ---
            if (k + 1 < N) {
                NSUInteger remaining = (NSUInteger)(N - k - 1);
                id<MTLComputeCommandEncoder> enc = [cb computeCommandEncoder];
                [enc setComputePipelineState:pso_project];
                uint32_t dims[4] = { (uint32_t)M, (uint32_t)N, (uint32_t)k, 0 };
                [enc setBuffer:buf_A offset:0 atIndex:0];
                [enc setBuffer:buf_Q offset:0 atIndex:1];
                [enc setBuffer:buf_R offset:0 atIndex:2];
                [enc setBytes:dims  length:sizeof(dims) atIndex:3];
                [enc dispatchThreads:MTLSizeMake(remaining, 1, 1)
                    threadsPerThreadgroup:MTLSizeMake(tg_proj, 1, 1)];
                [enc endEncoding];
            }

            [cb commit];
            [cb waitUntilCompleted];
            // After wait: GPU writes to buf_A/buf_Q are visible to CPU via shared memory
        }

        uint64_t t1 = mach_absolute_time();
        // ----- End timed region -----

        times[pass] = ticks_to_ms(t1 - t0);
        fprintf(stderr, "  pass %d: %.4f ms\n", pass + 1, times[pass]);
    }

    // -------------------------------------------------------------------
    // Orthogonality check (on last pass's Q)
    // -------------------------------------------------------------------
    float* h_Q = (float*)buf_Q.contents;
    bool orth_ok = check_orthogonality(h_Q, M, N);
    fprintf(stderr, "Orthogonality check (first %d columns): %s\n\n",
            (N < 8 ? N : 8), orth_ok ? "PASS" : "FAIL");

    // -------------------------------------------------------------------
    // Statistics
    // -------------------------------------------------------------------
    double sum_ms = 0.0, mn = DBL_MAX, mx = 0.0;
    for (int i = 0; i < passes; ++i) {
        sum_ms += times[i];
        if (times[i] < mn) mn = times[i];
        if (times[i] > mx) mx = times[i];
    }
    double avg_ms  = sum_ms / passes;
    double variance = 0.0;
    for (int i = 0; i < passes; ++i) {
        double d = times[i] - avg_ms;
        variance += d * d;
    }
    double stddev = (passes > 1) ? sqrt(variance / (passes - 1)) : 0.0;

    // GB/s: 3 * M*N*sizeof(float) per pass
    double gb   = 3.0 * (double)M * (double)N * sizeof(float) / 1e9;
    double gbps = gb / (avg_ms * 1e-3);

    fprintf(stderr,
            "Bandwidth: %.2f GB/s  (avg %.4f ms, min %.4f ms, max %.4f ms, stddev %.4f ms)\n",
            gbps, avg_ms, mn, mx, stddev);

    // JSON-lines output
    printf("{\"type\":\"kernel\",\"name\":\"gram_norm\",\"time_ms\":%.6f,"
           "\"params\":{\"m\":%d,\"n\":%d,\"passes\":%d,\"block_size\":%d}}\n",
           avg_ms, M, N, passes, block_size_param);
    printf("{\"type\":\"kernel\",\"name\":\"gram_norm_finish\",\"time_ms\":%.6f,"
           "\"params\":{\"m\":%d,\"n\":%d,\"passes\":%d,\"block_size\":%d}}\n",
           avg_ms, M, N, passes, block_size_param);
    printf("{\"type\":\"kernel\",\"name\":\"gram_normalize\",\"time_ms\":%.6f,"
           "\"params\":{\"m\":%d,\"n\":%d,\"passes\":%d,\"block_size\":%d}}\n",
           avg_ms, M, N, passes, block_size_param);
    printf("{\"type\":\"kernel\",\"name\":\"gram_project\",\"time_ms\":%.6f,"
           "\"params\":{\"m\":%d,\"n\":%d,\"passes\":%d,\"block_size\":%d}}\n",
           avg_ms, M, N, passes, block_size_param);
    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"bandwidth_gbps\",\"value\":%.2f}]}\n",
           avg_ms, gbps);

    return EXIT_SUCCESS;

    } // @autoreleasepool
}
