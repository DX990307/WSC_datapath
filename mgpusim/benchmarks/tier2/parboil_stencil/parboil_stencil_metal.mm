/**
 * parboil_stencil_metal.mm — Apple Metal host for the Parboil 3D Stencil benchmark.
 *
 * 7-point 3D Jacobi stencil over an N×N×N grid using ping-pong buffers.
 * Boundary cells (1-cell border) remain fixed.
 *
 * Usage:
 *   ./parboil_stencil [--grid_dim N] [--num_timesteps T] [--iterations I]
 *
 *   --grid_dim N        Grid dimension (N×N×N)            (default: 64)
 *   --num_timesteps T   Stencil time-steps per launch     (default: 10)
 *   --iterations I      Timed benchmark iterations        (default: 5)
 *
 * Output (stdout): CSV row — kernel_name,problem_size,iterations,avg_ms,min_ms,max_ms,stddev_ms
 *                  CSV summary — parboil_stencil,<NxNxN>,<GFLOPS>
 * Output (stderr): GFLOPS and GB/s
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

static int parseIterations(int argc, char** argv) {
    int iters = 5;
    for (int i = 1; i < argc - 1; ++i) {
        if (strcmp(argv[i], "--iterations") == 0) {
            int v = atoi(argv[i + 1]);
            if (v >= 1) iters = v;
            break;
        }
    }
    return iters;
}

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

    // Default values from params.json
    int grid_dim      = 64;
    int num_timesteps = 10;
    int iters         = 5;
    int block_size    = 256;

    // Command-line args as fallback
    grid_dim      = parseIntParam(argc, argv, "--grid_dim",      grid_dim);
    num_timesteps = parseIntParam(argc, argv, "--num_timesteps", num_timesteps);
    iters         = parseIterations(argc, argv);
    block_size    = parseIntParam(argc, argv, "--block_size",    block_size);

    // Env vars override
    const char* env_val;
    env_val = getenv("BENCH_PARAM_grid_dim");
    if (env_val) grid_dim = atoi(env_val);
    env_val = getenv("BENCH_PARAM_num_timesteps");
    if (env_val) num_timesteps = atoi(env_val);
    env_val = getenv("BENCH_PARAM_iterations");
    if (env_val) iters = atoi(env_val);
    env_val = getenv("BENCH_PARAM_block_size");
    if (env_val) block_size = atoi(env_val);
    int num_warmup = 0;

    int nx    = grid_dim;
    int ny    = grid_dim;
    int nz    = grid_dim;
    int total = nx * ny * nz;
    size_t bytes = (size_t)total * sizeof(float);

    float c0 = 0.6f;
    float c1 = (1.0f - c0) / 6.0f;

    // -------------------------------------------------------------------
    // Metal device setup
    // -------------------------------------------------------------------
    id<MTLDevice> device = MTLCreateSystemDefaultDevice();
    if (!device) {
        fprintf(stderr, "Error: no Metal device found\n");
        return EXIT_FAILURE;
    }
    fprintf(stderr, "Device: %s\n", device.name.UTF8String);
    fprintf(stderr, "Grid: %d×%d×%d  |  Timesteps/launch: %d  |  Iterations: %d\n\n",
            nx, ny, nz, num_timesteps, iters);

    id<MTLCommandQueue> queue = [device newCommandQueue];

    // -------------------------------------------------------------------
    // Compile Metal shader from source file next to the binary
    // -------------------------------------------------------------------
    NSError *err = nil;
    id<MTLLibrary> library = nil;

    NSString *exePath = [[NSBundle mainBundle] executablePath];
    NSString *dir     = [exePath stringByDeletingLastPathComponent];
    NSString *srcPath = [dir stringByAppendingPathComponent:@"parboil_stencil.metal"];

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
        fprintf(stderr, "Error: parboil_stencil.metal not found at %s\n",
                srcPath.UTF8String);
        return EXIT_FAILURE;
    }

    id<MTLFunction> fn = [library newFunctionWithName:@"stencil3d"];
    if (!fn) {
        fprintf(stderr, "Error: kernel 'stencil3d' not found\n");
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
    id<MTLBuffer> buf_A = [device newBufferWithLength:bytes
                                              options:MTLResourceStorageModeShared];
    id<MTLBuffer> buf_B = [device newBufferWithLength:bytes
                                              options:MTLResourceStorageModeShared];

    // Initialize with synthetic data
    float* h_data = (float*)buf_A.contents;
    srand(42);
    for (int i = 0; i < total; ++i) {
        h_data[i] = (float)(rand() % 1000) / 1000.0f;
    }
    // Copy same initial data into buf_B
    memcpy(buf_B.contents, buf_A.contents, bytes);

    // Parameter buffers
    uint32_t dims_data[4]   = { (uint32_t)nx, (uint32_t)ny, (uint32_t)nz, 0 };
    float    coeffs_data[4] = { c0, c1, 0.0f, 0.0f };

    // Thread dispatch: 3D grid — one thread per (x,y,z)
    // threadgroup: 16×16×1; grid: nx×ny×nz (Metal uses exact dispatch)
    NSUInteger tg_x = 16, tg_y = 16, tg_z = 1;
    // Use dispatchThreads (exact, no overshoot needed — kernel does bounds check)
    MTLSize threadsPerGroup  = MTLSizeMake(tg_x, tg_y, tg_z);
    MTLSize threadsPerGrid   = MTLSizeMake((NSUInteger)nx, (NSUInteger)ny, (NSUInteger)nz);

    // -------------------------------------------------------------------
    // Warmup (not timed)
    // -------------------------------------------------------------------
    for (int w = 0; w < num_warmup; ++w) {
        id<MTLBuffer> src_buf = buf_A;
        id<MTLBuffer> dst_buf = buf_B;

        for (int t = 0; t < num_timesteps; ++t) {
            id<MTLCommandBuffer>         cb  = [queue commandBuffer];
            id<MTLComputeCommandEncoder> enc = [cb computeCommandEncoder];
            [enc setComputePipelineState:pso];
            [enc setBuffer:src_buf    offset:0 atIndex:0];
            [enc setBuffer:dst_buf    offset:0 atIndex:1];
            [enc setBytes:dims_data   length:sizeof(dims_data)   atIndex:2];
            [enc setBytes:coeffs_data length:sizeof(coeffs_data) atIndex:3];
            [enc dispatchThreads:threadsPerGrid
                threadsPerThreadgroup:threadsPerGroup];
            [enc endEncoding];
            [cb commit];
            [cb waitUntilCompleted];
            // Ping-pong
            id<MTLBuffer> tmp = src_buf; src_buf = dst_buf; dst_buf = tmp;
        }
    }

    // -------------------------------------------------------------------
    // Timed iterations
    // -------------------------------------------------------------------
    std::vector<double> times((size_t)iters);

    for (int iter = 0; iter < iters; ++iter) {
        // Reset to initial state
        memcpy(buf_A.contents, h_data, bytes);
        memcpy(buf_B.contents, h_data, bytes);

        id<MTLBuffer> src_buf = buf_A;
        id<MTLBuffer> dst_buf = buf_B;

        uint64_t t0 = mach_absolute_time();

        for (int t = 0; t < num_timesteps; ++t) {
            id<MTLCommandBuffer>         cb  = [queue commandBuffer];
            id<MTLComputeCommandEncoder> enc = [cb computeCommandEncoder];
            [enc setComputePipelineState:pso];
            [enc setBuffer:src_buf    offset:0 atIndex:0];
            [enc setBuffer:dst_buf    offset:0 atIndex:1];
            [enc setBytes:dims_data   length:sizeof(dims_data)   atIndex:2];
            [enc setBytes:coeffs_data length:sizeof(coeffs_data) atIndex:3];
            [enc dispatchThreads:threadsPerGrid
                threadsPerThreadgroup:threadsPerGroup];
            [enc endEncoding];
            [cb commit];
            [cb waitUntilCompleted];
            // Ping-pong
            id<MTLBuffer> tmp = src_buf; src_buf = dst_buf; dst_buf = tmp;
        }

        uint64_t t1 = mach_absolute_time();
        times[iter] = ticks_to_ms(t1 - t0);
    }

    // -------------------------------------------------------------------
    // Compute statistics
    // -------------------------------------------------------------------
    double sum = 0.0, mn = DBL_MAX, mx = 0.0;
    for (int i = 0; i < iters; ++i) {
        sum += times[i];
        if (times[i] < mn) mn = times[i];
        if (times[i] > mx) mx = times[i];
    }
    double avg = sum / iters;
    double variance = 0.0;
    for (int i = 0; i < iters; ++i) {
        double d = times[i] - avg;
        variance += d * d;
    }
    double stddev = (iters > 1) ? sqrt(variance / (iters - 1)) : 0.0;

    char problemSize[64];
    snprintf(problemSize, sizeof(problemSize), "%dx%dx%d", nx, ny, nz);

    BenchResult r;
    r.kernel_name  = "stencil3d";
    r.problem_size = problemSize;
    r.iterations   = iters;
    r.avg_ms       = avg;
    r.min_ms       = mn;
    r.max_ms       = mx;
    r.stddev_ms    = stddev;

    // Performance metrics
    long long interior_cells = (long long)(nx - 2) * (ny - 2) * (nz - 2);
    double gflops = 8.0 * (double)interior_cells * num_timesteps
                    / (r.avg_ms * 1e-3) / 1e9;
    double gb_s   = 8.0 * sizeof(float) * (double)interior_cells * num_timesteps
                    / (r.avg_ms * 1e-3) / 1e9;

    // JSON-lines output
    printf("{\"type\":\"kernel\",\"name\":\"stencil3d\",\"time_ms\":%.6f,"
           "\"params\":{\"grid_dim\":%d,\"num_timesteps\":%d,\"iterations\":%d,"
           "\"block_size\":%d}}\n",
           r.avg_ms, grid_dim, num_timesteps, iters, block_size);

    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"gflops\",\"value\":%.2f},"
           "{\"name\":\"bandwidth_gbps\",\"value\":%.2f}]}\n",
           r.avg_ms * iters, gflops, gb_s);

    fprintf(stderr, "GFLOPS: %.2f  |  GB/s: %.2f  (avg %.4f ms, %d timesteps/iter)\n",
            gflops, gb_s, r.avg_ms, num_timesteps);

    return EXIT_SUCCESS;

    } // @autoreleasepool
}
