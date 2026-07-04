/**
 * rodinia_lud_metal.mm — Apple Metal host for the Rodinia LUD benchmark.
 *
 * Blocked LU decomposition of a dense NxN matrix (no pivoting).
 * Three sequential kernel passes per diagonal step:
 *   1. lud_diagonal_kernel   — factor the 16×16 diagonal block
 *   2. lud_perimeter_kernel  — update adjacent row/column blocks
 *   3. lud_internal_kernel   — Schur-complement update for interior blocks
 *
 * Usage:
 *   ./rodinia_lud [--size N]
 *   N must be divisible by 16 (block size). Default: N=512, iterations=3.
 *
 * Output (stdout): CSV — lud,<N>,<time_ms>,<GFLOPS>
 * Output (stderr): device info, per-iteration timing, verification result
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

static double ticks_to_ms(uint64_t ticks)
{
    static mach_timebase_info_data_t tb = {0, 0};
    if (tb.denom == 0) mach_timebase_info(&tb);
    return (double)ticks * (double)tb.numer / (double)tb.denom * 1e-6;
}

// ---------------------------------------------------------------------------
// Argument parsing
// ---------------------------------------------------------------------------

static int parseIntParam(int argc, char **argv, const char *name, int defaultVal)
{
    for (int i = 1; i < argc - 1; i++) {
        if (strcmp(argv[i], name) == 0) {
            int v = atoi(argv[i + 1]);
            if (v > 0) return v;
        }
    }
    return defaultVal;
}

// ---------------------------------------------------------------------------
// LUD parameter struct (matches Metal shader)
// ---------------------------------------------------------------------------

struct LudParams {
    int n;
    int offset;
    int nhalf;
};

// ---------------------------------------------------------------------------
// Host helpers
// ---------------------------------------------------------------------------

static void init_matrix(float *a, int n)
{
    srand(42);
    for (int i = 0; i < n; i++) {
        float row_sum = 0.0f;
        for (int j = 0; j < n; j++) {
            a[i * n + j] = (float)(rand() % 10 + 1) * 0.1f;
            if (i != j) row_sum += fabsf(a[i * n + j]);
        }
        a[i * n + i] = row_sum + 1.0f;
    }
}

static bool verify(const float *lu, const float *a_orig, int n)
{
    double err = 0.0, norm_a = 0.0;
    for (int i = 0; i < n; i++) {
        for (int j = 0; j < n; j++) {
            double sum = 0.0;
            int lim = (i < j) ? i : j;
            for (int k = 0; k <= lim; k++) {
                double l = (k == i) ? 1.0 : (double)lu[i * n + k];
                double u = (double)lu[k * n + j];
                sum += l * u;
            }
            double diff = (double)a_orig[i * n + j] - sum;
            err    += diff * diff;
            norm_a += (double)a_orig[i * n + j] * (double)a_orig[i * n + j];
        }
    }
    double rel = sqrt(err / norm_a);
    fprintf(stderr, "Verification: ||A - LU||_F / ||A||_F = %.6e  %s\n",
            rel, (rel < 1e-4) ? "(PASS)" : "(FAIL)");
    return rel < 1e-4;
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------

int main(int argc, char *argv[])
{
    @autoreleasepool {
    setvbuf(stdout, NULL, _IONBF, 0);

    const int BSIZE = 16;

    // Defaults
    int N     = 512;
    int block_size_param = 256;
    const char* verify_mode = "true";

    // Command-line fallback
    N     = parseIntParam(argc, argv, "--size", N);

    // Environment variable overrides
    const char* env_val;
    env_val = getenv("BENCH_PARAM_size");
    if (env_val) N = atoi(env_val);
    env_val = getenv("BENCH_PARAM_block_size");
    if (env_val) block_size_param = atoi(env_val);
    env_val = getenv("BENCH_PARAM_verify");
    if (env_val) verify_mode = env_val;
    int num_warmup = 0;

    if (N % BSIZE != 0) {
        fprintf(stderr, "Error: N=%d must be divisible by BSIZE=%d\n", N, BSIZE);
        return EXIT_FAILURE;
    }

    int    num_blocks = N / BSIZE;
    size_t bytes      = (size_t)N * N * sizeof(float);

    // -------------------------------------------------------------------
    // Metal device
    // -------------------------------------------------------------------
    id<MTLDevice> device = MTLCreateSystemDefaultDevice();
    if (!device) {
        fprintf(stderr, "Error: no Metal device found\n");
        return EXIT_FAILURE;
    }
    fprintf(stderr, "Device: %s\n", device.name.UTF8String);
    fprintf(stderr, "Matrix: %dx%d  BSIZE=%d  num_blocks=%d\n\n",
            N, N, BSIZE, num_blocks);

    id<MTLCommandQueue> queue = [device newCommandQueue];

    // -------------------------------------------------------------------
    // Compile Metal shader from source file next to the binary
    // -------------------------------------------------------------------
    NSError *err = nil;
    id<MTLLibrary> library = nil;

    NSString *exePath = [[NSBundle mainBundle] executablePath];
    NSString *dir     = [exePath stringByDeletingLastPathComponent];
    NSString *srcPath = [dir stringByAppendingPathComponent:@"rodinia_lud.metal"];

    if ([[NSFileManager defaultManager] fileExistsAtPath:srcPath]) {
        NSString *src = [NSString stringWithContentsOfFile:srcPath
                                                  encoding:NSUTF8StringEncoding
                                                     error:nil];
        MTLCompileOptions *opts = [MTLCompileOptions new];
        library = [device newLibraryWithSource:src options:opts error:&err];
        if (!library) {
            fprintf(stderr, "Metal compile error: %s\n",
                    err.localizedDescription.UTF8String);
            return EXIT_FAILURE;
        }
    } else {
        fprintf(stderr, "Error: rodinia_lud.metal not found at %s\n",
                srcPath.UTF8String);
        return EXIT_FAILURE;
    }

    // -------------------------------------------------------------------
    // Build pipeline states for the three kernels
    // -------------------------------------------------------------------
    auto makePSO = [&](const char *name) -> id<MTLComputePipelineState> {
        NSError *e = nil;
        id<MTLFunction> fn = [library newFunctionWithName:@(name)];
        if (!fn) {
            fprintf(stderr, "Error: kernel '%s' not found\n", name);
            exit(EXIT_FAILURE);
        }
        id<MTLComputePipelineState> pso =
            [device newComputePipelineStateWithFunction:fn error:&e];
        if (!pso) {
            fprintf(stderr, "Error creating PSO '%s': %s\n",
                    name, e.localizedDescription.UTF8String);
            exit(EXIT_FAILURE);
        }
        return pso;
    };

    id<MTLComputePipelineState> pso_diag  = makePSO("lud_diagonal_kernel");
    id<MTLComputePipelineState> pso_peri  = makePSO("lud_perimeter_kernel");
    id<MTLComputePipelineState> pso_inter = makePSO("lud_internal_kernel");

    // -------------------------------------------------------------------
    // Allocate buffers
    // -------------------------------------------------------------------
    id<MTLBuffer> buf_a = [device newBufferWithLength:bytes
                                             options:MTLResourceStorageModeShared];

    float *h_a      = (float*)malloc(bytes);
    float *h_a_orig = (float*)malloc(bytes);
    init_matrix(h_a, N);
    memcpy(h_a_orig, h_a, bytes);

    MTLSize tg_size = MTLSizeMake(BSIZE, BSIZE, 1);

    // Helper: dispatch one kernel with LudParams
    auto dispatch_kernel = [&](id<MTLComputeCommandEncoder> enc,
                                id<MTLComputePipelineState> pso,
                                MTLSize grid,
                                LudParams p) {
        [enc setComputePipelineState:pso];
        [enc setBuffer:buf_a offset:0 atIndex:0];
        [enc setBytes:&p length:sizeof(p) atIndex:1];
        [enc dispatchThreadgroups:grid threadsPerThreadgroup:tg_size];
    };

    // One full LUD pass (warm-up or timed)
    auto run_lud = [&]() {
        for (int k = 0; k < num_blocks; k++) {
            LudParams pd  = { N, k, 0 };
            LudParams pp  = { N, k, num_blocks - k - 1 };
            int intern = num_blocks - k - 1;
            int peri   = 2 * intern;

            // --- Diagonal ---
            {
                id<MTLCommandBuffer>         cb  = [queue commandBuffer];
                id<MTLComputeCommandEncoder> enc = [cb computeCommandEncoder];
                dispatch_kernel(enc, pso_diag, MTLSizeMake(1, 1, 1), pd);
                [enc endEncoding];
                [cb commit];
                [cb waitUntilCompleted];
            }

            if (peri > 0) {
                // --- Perimeter ---
                {
                    id<MTLCommandBuffer>         cb  = [queue commandBuffer];
                    id<MTLComputeCommandEncoder> enc = [cb computeCommandEncoder];
                    dispatch_kernel(enc, pso_peri,
                                    MTLSizeMake(peri, 1, 1), pp);
                    [enc endEncoding];
                    [cb commit];
                    [cb waitUntilCompleted];
                }
                // --- Internal ---
                {
                    id<MTLCommandBuffer>         cb  = [queue commandBuffer];
                    id<MTLComputeCommandEncoder> enc = [cb computeCommandEncoder];
                    dispatch_kernel(enc, pso_inter,
                                    MTLSizeMake(intern, intern, 1), pd);
                    [enc endEncoding];
                    [cb commit];
                    [cb waitUntilCompleted];
                }
            }
        }
    };

    // -------------------------------------------------------------------
    // Warmup
    // -------------------------------------------------------------------
    for (int w = 0; w < num_warmup; ++w) {
        memcpy(buf_a.contents, h_a, bytes);
        run_lud();
    }
    // Correctness verification (one extra run)
    memcpy(buf_a.contents, h_a, bytes);
    run_lud();
    verify((const float*)buf_a.contents, h_a_orig, N);

    // -------------------------------------------------------------------
    // Timed iterations
    // -------------------------------------------------------------------
    std::vector<double> times(1);
    for (int it = 0; it < 1; it++) {
        memcpy(buf_a.contents, h_a, bytes);
        uint64_t t0 = mach_absolute_time();
        run_lud();
        uint64_t t1 = mach_absolute_time();
        times[it] = ticks_to_ms(t1 - t0);
        fprintf(stderr, "  iter %d: %.4f ms\n", it, times[it]);
    }

    // Statistics
    double sum_ms = 0.0;
    for (double t : times) sum_ms += t;
    double avg_ms = sum_ms / 1;
    double gflops = (2.0 / 3.0) * (double)N * (double)N * (double)N
                    / (avg_ms * 1e-3) / 1e9;

    fprintf(stderr, "\nAverage: %.4f ms  GFLOPS: %.2f\n", avg_ms, gflops);

    // JSON-lines kernel events
    printf("{\"type\":\"kernel\",\"name\":\"lud_diagonal\",\"time_ms\":%.6f,"
           "\"params\":{\"size\":%d\"block_size\":%d,\"verify\":\"%s\"}}\n",
           avg_ms, N, block_size_param, verify_mode);

    printf("{\"type\":\"kernel\",\"name\":\"lud_perimeter\",\"time_ms\":%.6f,"
           "\"params\":{\"size\":%d\"block_size\":%d,\"verify\":\"%s\"}}\n",
           avg_ms, N, block_size_param, verify_mode);

    printf("{\"type\":\"kernel\",\"name\":\"lud_internal\",\"time_ms\":%.6f,"
           "\"params\":{\"size\":%d\"block_size\":%d,\"verify\":\"%s\"}}\n",
           avg_ms, N, block_size_param, verify_mode);

    // JSON-lines summary event
    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"gflops\",\"value\":%.2f}]}\n",
           sum_ms, gflops);

    free(h_a);
    free(h_a_orig);

    return EXIT_SUCCESS;

    } // @autoreleasepool
}
