/**
 * shoc_stencil2d_metal.mm — Apple Metal host for the SHOC 9-point 2D stencil benchmark.
 *
 * Applies a 9-point star stencil iteratively on an N×N grid:
 *
 *   out[i][j] = 0.5   * in[i][j]
 *             + 0.1   * (in[i-1][j] + in[i+1][j] + in[i][j-1] + in[i][j+1])
 *             + 0.025 * (in[i-1][j-1] + in[i-1][j+1] + in[i+1][j-1] + in[i+1][j+1])
 *
 * Interior cells only: rows/columns 1 .. N-2.
 * Boundaries remain fixed at 0.
 * Uses ping-pong buffers (two MTLBuffers, swapped each step).
 *
 * Usage:
 *   ./shoc_stencil2d [--n N] [--iterations I] [--trials T]
 *
 *   --n N            Grid dimension (N×N)           (default: 2048)
 *   --iterations I   Stencil steps per trial         (default: 5)
 *   --trials T       Number of timed trials          (default: 3)
 *
 * Output (stdout): CSV — stencil2d,<N>,<ITERATIONS>,<time_ms>,<GBs>
 * Output (stderr): device info, timing details, correctness check
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

static int parseIntParam(int argc, char** argv, const char* name, int defVal) {
    for (int i = 1; i < argc - 1; ++i) {
        if (strcmp(argv[i], name) == 0) {
            int v = atoi(argv[i + 1]);
            if (v > 0) return v;
            break;
        }
    }
    return defVal;
}

// ---------------------------------------------------------------------------
// CPU reference (single iteration, for correctness check)
// ---------------------------------------------------------------------------

static void cpu_stencil2d(const float* in, float* out, int N)
{
    for (int i = 1; i < N - 1; ++i) {
        for (int j = 1; j < N - 1; ++j) {
            float center = in[i * N + j];
            float north  = in[(i - 1) * N + j];
            float south  = in[(i + 1) * N + j];
            float west   = in[i * N + (j - 1)];
            float east   = in[i * N + (j + 1)];
            float nw     = in[(i - 1) * N + (j - 1)];
            float ne     = in[(i - 1) * N + (j + 1)];
            float sw     = in[(i + 1) * N + (j - 1)];
            float se     = in[(i + 1) * N + (j + 1)];

            out[i * N + j] = 0.5f   * center
                           + 0.1f   * (north + south + west + east)
                           + 0.025f * (nw + ne + sw + se);
        }
    }
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------

int main(int argc, char* argv[])
{
    @autoreleasepool {

    setvbuf(stdout, NULL, _IONBF, 0);

    int N      = parseIntParam(argc, argv, "--n",          2048);
    int iters  = parseIntParam(argc, argv, "--iterations", 5);
    int trials = parseIntParam(argc, argv, "--trials",     3);
    int block_size = 16;

    const char* env_val;
    env_val = getenv("BENCH_PARAM_n");
    if (env_val) N = atoi(env_val);
    env_val = getenv("BENCH_PARAM_iterations");
    if (env_val) iters = atoi(env_val);
    env_val = getenv("BENCH_PARAM_trials");
    if (env_val) trials = atoi(env_val);
    env_val = getenv("BENCH_PARAM_block_size");
    if (env_val) block_size = atoi(env_val);
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
    fprintf(stderr, "Grid: %d×%d  |  Iterations/trial: %d  |  Trials: %d\n\n",
            N, N, iters, trials);

    id<MTLCommandQueue> queue = [device newCommandQueue];

    // -------------------------------------------------------------------
    // Compile Metal shader from source file next to the binary
    // -------------------------------------------------------------------
    NSError *err = nil;
    id<MTLLibrary> library = nil;

    NSString *exePath = [[NSBundle mainBundle] executablePath];
    NSString *dir     = [exePath stringByDeletingLastPathComponent];
    NSString *srcPath = [dir stringByAppendingPathComponent:@"shoc_stencil2d.metal"];

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
        fprintf(stderr, "Error: shoc_stencil2d.metal not found at %s\n",
                srcPath.UTF8String);
        return EXIT_FAILURE;
    }

    id<MTLFunction> fn = [library newFunctionWithName:@"stencil2d_kernel"];
    if (!fn) {
        fprintf(stderr, "Error: kernel 'stencil2d_kernel' not found\n");
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
    // Allocate Metal buffers (shared memory)
    // -------------------------------------------------------------------
    id<MTLBuffer> buf0 = [device newBufferWithLength:bytes
                                             options:MTLResourceStorageModeShared];
    id<MTLBuffer> buf1 = [device newBufferWithLength:bytes
                                             options:MTLResourceStorageModeShared];

    // Initialize: interior cells random, boundaries = 0
    float* h_data = (float*)buf0.contents;
    memset(h_data, 0, bytes);
    srand(123);
    for (int i = 1; i < N - 1; ++i)
        for (int j = 1; j < N - 1; ++j)
            h_data[i * N + j] = (float)(rand() % 100) / 10.0f;

    // Zero buf1
    memset((float*)buf1.contents, 0, bytes);

    // Dims constant: { N, N, 0, 0 }
    uint32_t dims_data[4] = { (uint32_t)N, (uint32_t)N, 0, 0 };

    // Thread configuration: block_size×block_size threadgroup, cover (N-2)×(N-2) interior
    MTLSize threadgroupSize = MTLSizeMake(block_size, block_size, 1);
    NSUInteger gridW = ((NSUInteger)(N - 2) + block_size - 1) / block_size;
    NSUInteger gridH = ((NSUInteger)(N - 2) + block_size - 1) / block_size;
    MTLSize threadgroupGrid = MTLSizeMake(gridW, gridH, 1);

    // Lambda to run one trial (iters stencil steps) using ping-pong
    auto run_trial = [&](id<MTLBuffer> in_buf, id<MTLBuffer> out_buf) {
        id<MTLBuffer> cur_in  = in_buf;
        id<MTLBuffer> cur_out = out_buf;
        for (int it = 0; it < iters; ++it) {
            id<MTLCommandBuffer>         cb  = [queue commandBuffer];
            id<MTLComputeCommandEncoder> enc = [cb computeCommandEncoder];
            [enc setComputePipelineState:pso];
            [enc setBuffer:cur_in  offset:0 atIndex:0];
            [enc setBuffer:cur_out offset:0 atIndex:1];
            [enc setBytes:dims_data length:sizeof(dims_data) atIndex:2];
            [enc dispatchThreadgroups:threadgroupGrid
               threadsPerThreadgroup:threadgroupSize];
            [enc endEncoding];
            [cb commit];
            [cb waitUntilCompleted];
            // swap
            id<MTLBuffer> tmp = cur_in;
            cur_in  = cur_out;
            cur_out = tmp;
        }
    };

    // -------------------------------------------------------------------
    // Correctness check: 64×64 grid, 1 iteration
    // -------------------------------------------------------------------
    {
        const int NC = 64;
        size_t nc_bytes = (size_t)NC * NC * sizeof(float);
        id<MTLBuffer> nc_buf0 = [device newBufferWithLength:nc_bytes
                                                    options:MTLResourceStorageModeShared];
        id<MTLBuffer> nc_buf1 = [device newBufferWithLength:nc_bytes
                                                    options:MTLResourceStorageModeShared];
        float* nc_in  = (float*)nc_buf0.contents;
        float* nc_out = (float*)nc_buf1.contents;
        memset(nc_in,  0, nc_bytes);
        memset(nc_out, 0, nc_bytes);

        srand(42);
        for (int i = 1; i < NC - 1; ++i)
            for (int j = 1; j < NC - 1; ++j)
                nc_in[i * NC + j] = (float)(rand() % 100) / 10.0f;

        // CPU reference
        std::vector<float> cpu_out(NC * NC, 0.0f);
        cpu_stencil2d(nc_in, cpu_out.data(), NC);

        // GPU: 1 iteration
        uint32_t nc_dims[4] = { (uint32_t)NC, (uint32_t)NC, 0, 0 };
        NSUInteger nc_gW = ((NSUInteger)(NC - 2) + 15) / 16;
        NSUInteger nc_gH = ((NSUInteger)(NC - 2) + 15) / 16;
        {
            id<MTLCommandBuffer>         cb  = [queue commandBuffer];
            id<MTLComputeCommandEncoder> enc = [cb computeCommandEncoder];
            [enc setComputePipelineState:pso];
            [enc setBuffer:nc_buf0 offset:0 atIndex:0];
            [enc setBuffer:nc_buf1 offset:0 atIndex:1];
            [enc setBytes:nc_dims length:sizeof(nc_dims) atIndex:2];
            [enc dispatchThreadgroups:MTLSizeMake(nc_gW, nc_gH, 1)
               threadsPerThreadgroup:MTLSizeMake(16, 16, 1)];
            [enc endEncoding];
            [cb commit];
            [cb waitUntilCompleted];
        }

        bool pass = true;
        for (int i = 1; i < NC - 1 && pass; ++i) {
            for (int j = 1; j < NC - 1 && pass; ++j) {
                float ref = cpu_out[i * NC + j];
                float gpu = nc_out[i * NC + j];
                float rel = fabsf(gpu - ref) / (fabsf(ref) + 1e-6f);
                if (rel > 1e-4f) {
                    fprintf(stderr, "MISMATCH at [%d][%d]: gpu=%.6f cpu=%.6f rel=%.4e\n",
                            i, j, gpu, ref, rel);
                    pass = false;
                }
            }
        }
        fprintf(stderr, "Correctness check (64×64, 1 iteration): %s\n\n",
                pass ? "PASS" : "FAIL");
    }

    // -------------------------------------------------------------------
    // Warmup
    // -------------------------------------------------------------------
    for (int w = 0; w < num_warmup; ++w) {
        run_trial(buf0, buf1);
    }

    // -------------------------------------------------------------------
    // Timed trials
    // -------------------------------------------------------------------
    std::vector<double> trial_times((size_t)trials);
    for (int t = 0; t < trials; ++t) {
        // Reset data
        float* p0 = (float*)buf0.contents;
        float* p1 = (float*)buf1.contents;
        memset(p0, 0, bytes);
        memset(p1, 0, bytes);
        srand(123);
        for (int i = 1; i < N - 1; ++i)
            for (int j = 1; j < N - 1; ++j)
                p0[i * N + j] = (float)(rand() % 100) / 10.0f;

        uint64_t t0 = mach_absolute_time();
        run_trial(buf0, buf1);
        uint64_t t1 = mach_absolute_time();
        trial_times[t] = ticks_to_ms(t1 - t0);
    }

    // -------------------------------------------------------------------
    // Statistics
    // -------------------------------------------------------------------
    double sum_ms = 0.0, mn_ms = DBL_MAX, mx_ms = 0.0;
    for (int t = 0; t < trials; ++t) {
        sum_ms += trial_times[t];
        if (trial_times[t] < mn_ms) mn_ms = trial_times[t];
        if (trial_times[t] > mx_ms) mx_ms = trial_times[t];
    }
    double avg_ms = sum_ms / trials;
    double variance = 0.0;
    for (int t = 0; t < trials; ++t) {
        double d = trial_times[t] - avg_ms;
        variance += d * d;
    }
    double stddev = (trials > 1) ? sqrt(variance / (trials - 1)) : 0.0;

    // GB/s: each iteration reads N*N floats + writes (N-2)*(N-2) ≈ 2*N*N floats
    double gb   = (double)iters * 2.0 * (double)N * (double)N * sizeof(float) / 1e9;
    double gbps = gb / (avg_ms * 1e-3);

    fprintf(stderr, "Bandwidth: %.2f GB/s  (avg %.4f ms, min %.4f ms, max %.4f ms, stddev %.4f ms)\n",
            gbps, avg_ms, mn_ms, mx_ms, stddev);

    // JSON-lines output
    printf("{\"type\":\"kernel\",\"name\":\"stencil2d_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"n\":%d,\"iterations\":%d,\"trials\":%d,\"block_size\":%d}}\n",
           avg_ms, N, iters, trials, block_size);
    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"bandwidth_gbps\",\"value\":%.2f}]}\n",
           avg_ms, gbps);

    return EXIT_SUCCESS;

    } // @autoreleasepool
}
