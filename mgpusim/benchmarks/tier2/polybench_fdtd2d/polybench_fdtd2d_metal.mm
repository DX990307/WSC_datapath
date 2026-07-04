/**
 * polybench_fdtd2d_metal.mm — Apple Metal host for the PolyBench FDTD-2D benchmark.
 *
 * 2D Finite Difference Time Domain electromagnetic simulation.
 * Three field arrays: ex[NX][NY], ey[NX][NY], hz[NX][NY]
 * Three kernels per time step: update_ex, update_ey, update_hz
 *
 * Usage:
 *   ./polybench_fdtd2d [--size N] [--tmax T]
 *
 *   --size N        Grid dimension (N×N)            (default: 512)
 *   --tmax T        Time steps per benchmark iter   (default: 50)
 *   --iterations I  Timed benchmark iterations      (default: 3)
 *
 * Output (stdout): CSV — kernel_name,problem_size,iterations,avg_ms,min_ms,max_ms,stddev_ms
 * Output (stderr): Effective bandwidth in GB/s
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
// CSV output
// ---------------------------------------------------------------------------

struct BenchResult {
    const char* kernel_name;
    const char* problem_size;
    int         iterations;
    double      avg_ms;
    double      min_ms;
    double      max_ms;
    double      stddev_ms;
};


// ---------------------------------------------------------------------------
// main
// ---------------------------------------------------------------------------

int main(int argc, char* argv[])
{
    @autoreleasepool {
    setvbuf(stdout, NULL, _IONBF, 0);

    int N     = parseIntParam(argc, argv, "--size", 512);
    int TMAX  = parseIntParam(argc, argv, "--tmax",  50);
    int block_size = 256;

    const char* env_val;
    env_val = getenv("BENCH_PARAM_size");
    if (env_val) N = atoi(env_val);
    env_val = getenv("BENCH_PARAM_tmax");
    if (env_val) TMAX = atoi(env_val);
    env_val = getenv("BENCH_PARAM_block_size");
    if (env_val) block_size = atoi(env_val);
    int num_warmup = 0;

    int NX = N, NY = N;
    size_t grid_bytes = (size_t)NX * NY * sizeof(float);

    // -------------------------------------------------------------------
    // Metal device setup
    // -------------------------------------------------------------------
    id<MTLDevice> device = MTLCreateSystemDefaultDevice();
    if (!device) {
        fprintf(stderr, "Error: no Metal device found\n");
        return EXIT_FAILURE;
    }
    fprintf(stderr, "Device: %s\n", device.name.UTF8String);
    fprintf(stderr, "Grid: %d×%d  |  TMAX: %d\n\n",
            NX, NY, TMAX);

    id<MTLCommandQueue> queue = [device newCommandQueue];

    // -------------------------------------------------------------------
    // Compile Metal shader from source file next to the binary
    // -------------------------------------------------------------------
    NSError *err = nil;
    id<MTLLibrary> library = nil;

    NSString *exePath = [[NSBundle mainBundle] executablePath];
    NSString *dir     = [exePath stringByDeletingLastPathComponent];
    NSString *srcPath = [dir stringByAppendingPathComponent:@"polybench_fdtd2d.metal"];

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
        fprintf(stderr, "Error: polybench_fdtd2d.metal not found at %s\n",
                srcPath.UTF8String);
        return EXIT_FAILURE;
    }

    // Retrieve kernel functions
    id<MTLFunction> fn_ex = [library newFunctionWithName:@"update_ex"];
    id<MTLFunction> fn_ey = [library newFunctionWithName:@"update_ey"];
    id<MTLFunction> fn_hz = [library newFunctionWithName:@"update_hz"];
    if (!fn_ex || !fn_ey || !fn_hz) {
        fprintf(stderr, "Error: one or more kernel functions not found in Metal library\n");
        return EXIT_FAILURE;
    }

    id<MTLComputePipelineState> pso_ex =
        [device newComputePipelineStateWithFunction:fn_ex error:&err];
    id<MTLComputePipelineState> pso_ey =
        [device newComputePipelineStateWithFunction:fn_ey error:&err];
    id<MTLComputePipelineState> pso_hz =
        [device newComputePipelineStateWithFunction:fn_hz error:&err];
    if (!pso_ex || !pso_ey || !pso_hz) {
        fprintf(stderr, "Error creating pipeline state: %s\n",
                err.localizedDescription.UTF8String);
        return EXIT_FAILURE;
    }

    // -------------------------------------------------------------------
    // Allocate GPU buffers
    // -------------------------------------------------------------------
    id<MTLBuffer> buf_ex = [device newBufferWithLength:grid_bytes
                                               options:MTLResourceStorageModeShared];
    id<MTLBuffer> buf_ey = [device newBufferWithLength:grid_bytes
                                               options:MTLResourceStorageModeShared];
    id<MTLBuffer> buf_hz = [device newBufferWithLength:grid_bytes
                                               options:MTLResourceStorageModeShared];

    // Initialize with random data (host side)
    float* h_ex = (float*)buf_ex.contents;
    float* h_ey = (float*)buf_ey.contents;
    float* h_hz = (float*)buf_hz.contents;

    // Save initial values for resetting between iterations
    std::vector<float> init_ex((size_t)NX * NY);
    std::vector<float> init_ey((size_t)NX * NY);
    std::vector<float> init_hz((size_t)NX * NY);

    srand(42);
    for (int i = 0; i < NX * NY; i++) init_ex[i] = (float)(rand() % 100) / 10.0f;
    for (int i = 0; i < NX * NY; i++) init_ey[i] = (float)(rand() % 100) / 10.0f;
    for (int i = 0; i < NX * NY; i++) init_hz[i] = (float)(rand() % 100) / 10.0f;

    // dims: {NX, NY, 0, 0}
    uint32_t dims_data[4] = { (uint32_t)NX, (uint32_t)NY, 0u, 0u };

    // Thread dispatch: one thread per grid cell (NX×NY)
    const NSUInteger tg = 16;
    MTLSize threadsPerThreadgroup = MTLSizeMake(tg, tg, 1);
    MTLSize gridSize = MTLSizeMake(
        ((NSUInteger)NY + tg - 1) / tg * tg,
        ((NSUInteger)NX + tg - 1) / tg * tg,
        1);

    // Helper lambda: encode one time step into a command buffer
    auto run_timestep = [&](id<MTLCommandBuffer> cb) {
        // --- update_ex ---
        id<MTLComputeCommandEncoder> enc_ex = [cb computeCommandEncoder];
        [enc_ex setComputePipelineState:pso_ex];
        [enc_ex setBuffer:buf_ex offset:0 atIndex:0];
        [enc_ex setBuffer:buf_ey offset:0 atIndex:1];
        [enc_ex setBuffer:buf_hz offset:0 atIndex:2];
        [enc_ex setBytes:dims_data length:sizeof(dims_data) atIndex:3];
        [enc_ex dispatchThreads:gridSize threadsPerThreadgroup:threadsPerThreadgroup];
        [enc_ex endEncoding];

        // --- update_ey ---
        id<MTLComputeCommandEncoder> enc_ey = [cb computeCommandEncoder];
        [enc_ey setComputePipelineState:pso_ey];
        [enc_ey setBuffer:buf_ex offset:0 atIndex:0];
        [enc_ey setBuffer:buf_ey offset:0 atIndex:1];
        [enc_ey setBuffer:buf_hz offset:0 atIndex:2];
        [enc_ey setBytes:dims_data length:sizeof(dims_data) atIndex:3];
        [enc_ey dispatchThreads:gridSize threadsPerThreadgroup:threadsPerThreadgroup];
        [enc_ey endEncoding];

        // --- update_hz ---
        id<MTLComputeCommandEncoder> enc_hz = [cb computeCommandEncoder];
        [enc_hz setComputePipelineState:pso_hz];
        [enc_hz setBuffer:buf_ex offset:0 atIndex:0];
        [enc_hz setBuffer:buf_ey offset:0 atIndex:1];
        [enc_hz setBuffer:buf_hz offset:0 atIndex:2];
        [enc_hz setBytes:dims_data length:sizeof(dims_data) atIndex:3];
        [enc_hz dispatchThreads:gridSize threadsPerThreadgroup:threadsPerThreadgroup];
        [enc_hz endEncoding];
    };

    // -------------------------------------------------------------------
    // Warmup (not timed)
    // -------------------------------------------------------------------
    for (int w = 0; w < num_warmup; ++w) {
        memcpy(h_ex, init_ex.data(), grid_bytes);
        memcpy(h_ey, init_ey.data(), grid_bytes);
        memcpy(h_hz, init_hz.data(), grid_bytes);

        for (int t = 0; t < TMAX; ++t) {
            id<MTLCommandBuffer> cb = [queue commandBuffer];
            run_timestep(cb);
            [cb commit];
            [cb waitUntilCompleted];
        }
    }

    // -------------------------------------------------------------------
    // Timed iterations
    // -------------------------------------------------------------------
    std::vector<double> times(1);

    for (int iter = 0; iter < 1; ++iter) {
        // Reset arrays to initial state
        memcpy(h_ex, init_ex.data(), grid_bytes);
        memcpy(h_ey, init_ey.data(), grid_bytes);
        memcpy(h_hz, init_hz.data(), grid_bytes);

        uint64_t t0 = mach_absolute_time();

        for (int t = 0; t < TMAX; ++t) {
            id<MTLCommandBuffer> cb = [queue commandBuffer];
            run_timestep(cb);
            [cb commit];
            [cb waitUntilCompleted];
        }

        uint64_t t1 = mach_absolute_time();
        times[iter] = ticks_to_ms(t1 - t0);
    }

    // Compute statistics
    double sum = 0.0, mn = DBL_MAX, mx = 0.0;
    for (int i = 0; i < 1; ++i) {
        sum += times[i];
        if (times[i] < mn) mn = times[i];
        if (times[i] > mx) mx = times[i];
    }
    double avg = sum / 1;
    double variance = 0.0;
    for (int i = 0; i < 1; ++i) {
        double d = times[i] - avg;
        variance += d * d;
    }
    double stddev = (1 > 1) ? sqrt(variance / (1 - 1)) : 0.0;

    char problemSize[64];
    snprintf(problemSize, sizeof(problemSize), "%dx%d", NX, NY);

    BenchResult r;
    r.kernel_name  = "polybench_fdtd2d";
    r.problem_size = problemSize;
    r.iterations = 1;
    r.avg_ms       = avg;
    r.min_ms       = mn;
    r.max_ms       = mx;
    r.stddev_ms    = stddev;

    // Effective bandwidth: 7*NX*NY*sizeof(float) bytes/step
    double bytes_per_step = 7.0 * (double)NX * NY * sizeof(float);
    double total_bytes    = bytes_per_step * TMAX;
    double bw_gb = total_bytes / (r.avg_ms * 1e-3) / 1e9;
    fprintf(stderr, "Effective bandwidth: %.2f GB/s  (avg %.4f ms, %d steps/iter)\n",
            bw_gb, r.avg_ms, TMAX);

    // JSON-lines output
    printf("{\"type\":\"kernel\",\"name\":\"fdtd_update_ex\",\"time_ms\":%.6f,"
           "\"params\":{\"size\":%d,\"tmax\":%d\"block_size\":%d}}\n",
           r.avg_ms, N, TMAX, block_size);
    printf("{\"type\":\"kernel\",\"name\":\"fdtd_update_ey\",\"time_ms\":%.6f,"
           "\"params\":{\"size\":%d,\"tmax\":%d\"block_size\":%d}}\n",
           r.avg_ms, N, TMAX, block_size);
    printf("{\"type\":\"kernel\",\"name\":\"fdtd_update_hz\",\"time_ms\":%.6f,"
           "\"params\":{\"size\":%d,\"tmax\":%d\"block_size\":%d}}\n",
           r.avg_ms, N, TMAX, block_size);
    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"bandwidth_gbps\",\"value\":%.2f}]}\n",
           r.avg_ms, bw_gb);

    return EXIT_SUCCESS;

    } // @autoreleasepool
}
