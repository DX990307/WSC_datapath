/**
 * polybench_jacobi2d_metal.mm — Apple Metal host for the PolyBench Jacobi 2D stencil benchmark.
 *
 * Computes TSTEPS iterations of 2D Jacobi stencil on an N×N grid:
 *   B[i][j] = (A[i-1][j] + A[i+1][j] + A[i][j-1] + A[i][j+1] + A[i][j]) / 5.0
 *   then swap A and B (double-buffer)
 *
 * Only interior points (i=1..N-2, j=1..N-2) are updated; boundaries remain 0.
 * Measures memory bandwidth in GB/s (read N×N + write N×N each step).
 *
 * Usage:
 *   ./polybench_jacobi2d [--n N] [--tsteps T] [--trials K]
 *
 *   --n N        Grid size N×N           (default: 1024)
 *   --tsteps T   Number of time steps    (default: 50)
 *   --trials K   Number of timed trials  (default: 3)
 *
 * Output (stdout): CSV — jacobi2d,<N>,<TSTEPS>,<time_ms>,<GBs>
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

static void cpu_jacobi2d(const float* A, float* B, int N)
{
    for (int i = 1; i < N-1; ++i) {
        for (int j = 1; j < N-1; ++j) {
            B[i*N + j] = (A[(i-1)*N + j] + A[(i+1)*N + j] +
                          A[i*N + (j-1)] + A[i*N + (j+1)] +
                          A[i*N + j]) * 0.2f;
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

    int N      = parseIntParam(argc, argv, "--n",      1024);
    int TSTEPS = parseIntParam(argc, argv, "--tsteps", 50);
    int trials = parseIntParam(argc, argv, "--trials", 3);
    int block_size_param = 256;

    const char* env_val;
    env_val = getenv("BENCH_PARAM_n");
    if (env_val) N = atoi(env_val);
    env_val = getenv("BENCH_PARAM_tsteps");
    if (env_val) TSTEPS = atoi(env_val);
    env_val = getenv("BENCH_PARAM_trials");
    if (env_val) trials = atoi(env_val);
    env_val = getenv("BENCH_PARAM_block_size");
    if (env_val) block_size_param = atoi(env_val);
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
    fprintf(stderr, "Grid: %d×%d  |  TSTEPS: %d  |  Trials: %d\n\n",
            N, N, TSTEPS, trials);

    id<MTLCommandQueue> queue = [device newCommandQueue];

    // -------------------------------------------------------------------
    // Compile Metal shader from source file next to the binary
    // -------------------------------------------------------------------
    NSError *err = nil;
    id<MTLLibrary> library = nil;

    NSString *exePath = [[NSBundle mainBundle] executablePath];
    NSString *dir     = [exePath stringByDeletingLastPathComponent];
    NSString *srcPath = [dir stringByAppendingPathComponent:@"polybench_jacobi2d.metal"];

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
        fprintf(stderr, "Error: polybench_jacobi2d.metal not found at %s\n",
                srcPath.UTF8String);
        return EXIT_FAILURE;
    }

    // Create pipeline state
    id<MTLFunction> fn = [library newFunctionWithName:@"jacobi2d_kernel"];
    if (!fn) {
        fprintf(stderr, "Error: kernel 'jacobi2d_kernel' not found\n");
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
    id<MTLBuffer> buf_A = [device newBufferWithLength:bytes
                                              options:MTLResourceStorageModeShared];
    id<MTLBuffer> buf_B = [device newBufferWithLength:bytes
                                              options:MTLResourceStorageModeShared];

    // Initialize A with random floats (interior only; boundaries stay 0)
    float* h_A = (float*)buf_A.contents;
    float* h_B = (float*)buf_B.contents;
    memset(h_A, 0, bytes);
    memset(h_B, 0, bytes);

    srand(42);
    for (int i = 1; i < N-1; ++i)
        for (int j = 1; j < N-1; ++j)
            h_A[i*N + j] = (float)(rand() % 100) / 10.0f;

    // Keep a copy for resetting between trials
    std::vector<float> init_A(h_A, h_A + (size_t)N * N);

    // Kernel parameters: dims = {N, N}
    uint32_t dims_data[2] = { (uint32_t)N, (uint32_t)N };

    // Thread configuration: 16×16 threadgroup
    // gridSize = total threads (not threadgroups) when using dispatchThreads:
    MTLSize threadgroupSize = { 16, 16, 1 };
    MTLSize gridSize = { (NSUInteger)(N-2), (NSUInteger)(N-2), 1 };

    // Helper: dispatch one Jacobi step (src → dst)
    auto dispatch_step = [&](id<MTLBuffer> src, id<MTLBuffer> dst) {
        id<MTLCommandBuffer>         cb  = [queue commandBuffer];
        id<MTLComputeCommandEncoder> enc = [cb computeCommandEncoder];
        [enc setComputePipelineState:pso];
        [enc setBuffer:src  offset:0 atIndex:0];
        [enc setBuffer:dst  offset:0 atIndex:1];
        [enc setBytes:dims_data length:sizeof(dims_data) atIndex:2];
        [enc dispatchThreads:gridSize
            threadsPerThreadgroup:threadgroupSize];
        [enc endEncoding];
        [cb commit];
        [cb waitUntilCompleted];
    };

    // Helper: run TSTEPS with ping-pong, return pointer to final result buffer
    auto run_tsteps = [&]() -> id<MTLBuffer> {
        id<MTLBuffer> bufs[2] = { buf_A, buf_B };
        int cur = 0;
        for (int t = 0; t < TSTEPS; ++t) {
            dispatch_step(bufs[cur], bufs[1 - cur]);
            cur = 1 - cur;
        }
        // cur now points to the latest destination (which just received the write)
        return bufs[cur];
    };

    // Reset A to initial state
    auto reset_A = [&]() {
        memcpy(buf_A.contents, init_A.data(), bytes);
        memset(buf_B.contents, 0, bytes);
    };

    // -------------------------------------------------------------------
    // Warmup (not measured)
    // -------------------------------------------------------------------
    for (int w = 0; w < num_warmup; ++w) {
        reset_A();
        run_tsteps();
    }

    // -------------------------------------------------------------------
    // Timed trials
    // -------------------------------------------------------------------
    std::vector<double> times((size_t)trials);
    id<MTLBuffer> result_buf = nil;
    for (int tr = 0; tr < trials; ++tr) {
        reset_A();
        uint64_t t0 = mach_absolute_time();
        result_buf = run_tsteps();
        uint64_t t1 = mach_absolute_time();
        times[tr] = ticks_to_ms(t1 - t0);
    }

    // -------------------------------------------------------------------
    // Correctness check: run CPU for TSTEPS on small 64×64 grid
    // -------------------------------------------------------------------
    {
        int CN = 64;
        size_t cbytes = (size_t)CN * CN * sizeof(float);
        std::vector<float> cA((size_t)CN * CN, 0.0f);
        std::vector<float> cB((size_t)CN * CN, 0.0f);

        srand(42);
        for (int i = 1; i < CN-1; ++i)
            for (int j = 1; j < CN-1; ++j)
                cA[i*CN + j] = (float)(rand() % 100) / 10.0f;
        std::vector<float> cA_gpu_init = cA;

        // Run CPU TSTEPS
        for (int t = 0; t < TSTEPS; ++t) {
            cpu_jacobi2d(cA.data(), cB.data(), CN);
            cA.swap(cB);
        }
        // cA now holds CPU result

        // Run GPU on CN×CN grid
        id<MTLBuffer> cbuf_A = [device newBufferWithLength:cbytes
                                                   options:MTLResourceStorageModeShared];
        id<MTLBuffer> cbuf_B = [device newBufferWithLength:cbytes
                                                   options:MTLResourceStorageModeShared];
        memcpy(cbuf_A.contents, cA_gpu_init.data(), cbytes);
        memset(cbuf_B.contents, 0, cbytes);

        uint32_t cdims[2] = { (uint32_t)CN, (uint32_t)CN };
        // Total threads for the small grid correctness check
        MTLSize cgrid = { (NSUInteger)(CN-2), (NSUInteger)(CN-2), 1 };

        id<MTLBuffer> cbufs[2] = { cbuf_A, cbuf_B };
        int ccur = 0;
        for (int t = 0; t < TSTEPS; ++t) {
            id<MTLCommandBuffer>         cb  = [queue commandBuffer];
            id<MTLComputeCommandEncoder> enc = [cb computeCommandEncoder];
            [enc setComputePipelineState:pso];
            [enc setBuffer:cbufs[ccur]     offset:0 atIndex:0];
            [enc setBuffer:cbufs[1-ccur]   offset:0 atIndex:1];
            [enc setBytes:cdims length:sizeof(cdims) atIndex:2];
            [enc dispatchThreads:cgrid
                threadsPerThreadgroup:threadgroupSize];
            [enc endEncoding];
            [cb commit];
            [cb waitUntilCompleted];
            ccur = 1 - ccur;
        }
        float* gpu_result = (float*)cbufs[ccur].contents;

        bool pass = true;
        float max_rel = 0.0f;
        for (int i = 1; i < CN-1 && pass; ++i) {
            for (int j = 1; j < CN-1; ++j) {
                float ref = cA[i*CN + j];
                float gpu = gpu_result[i*CN + j];
                float rel = fabsf(gpu - ref) / (fabsf(ref) + 1e-6f);
                if (rel > max_rel) max_rel = rel;
                if (rel > 1e-3f) {
                    fprintf(stderr, "MISMATCH [%d][%d]: gpu=%.6f cpu=%.6f rel=%.4e\n",
                            i, j, gpu, ref, rel);
                    pass = false;
                    break;
                }
            }
        }
        fprintf(stderr, "Correctness check (CPU vs GPU, %d×%d, %d steps): %s (max_rel=%.2e)\n\n",
                CN, CN, TSTEPS, pass ? "PASS" : "FAIL", max_rel);
    }

    // -------------------------------------------------------------------
    // Statistics
    // -------------------------------------------------------------------
    double sum_ms = 0.0, mn = DBL_MAX, mx = 0.0;
    for (int i = 0; i < trials; ++i) {
        sum_ms += times[i];
        if (times[i] < mn) mn = times[i];
        if (times[i] > mx) mx = times[i];
    }
    double avg_ms = sum_ms / trials;
    double variance = 0.0;
    for (int i = 0; i < trials; ++i) {
        double d = times[i] - avg_ms;
        variance += d * d;
    }
    double stddev = (trials > 1) ? sqrt(variance / (trials - 1)) : 0.0;

    // GB/s: read N×N + write N×N per step, TSTEPS steps
    double gb   = (double)TSTEPS * 2.0 * (double)N * (double)N * sizeof(float) / 1e9;
    double gbps = gb / (avg_ms * 1e-3);

    fprintf(stderr, "Bandwidth: %.2f GB/s  (avg %.4f ms, min %.4f ms, max %.4f ms, stddev %.4f ms)\n",
            gbps, avg_ms, mn, mx, stddev);

    // JSON-lines output
    printf("{\"type\":\"kernel\",\"name\":\"jacobi2d_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"n\":%d,\"tsteps\":%d,\"trials\":%d,\"block_size\":%d}}\n",
           avg_ms, N, TSTEPS, trials, block_size_param);
    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"bandwidth_gbps\",\"value\":%.2f}]}\n",
           avg_ms, gbps);

    return EXIT_SUCCESS;

    } // @autoreleasepool
}
