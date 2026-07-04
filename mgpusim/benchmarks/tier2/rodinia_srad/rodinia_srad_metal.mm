/**
 * rodinia_srad_metal.mm — Apple Metal host for the Rodinia SRAD benchmark.
 *
 * Speckle-Reducing Anisotropic Diffusion (SRAD): iterative two-kernel image
 * smoothing using the Perona-Malik anisotropic diffusion scheme.
 *
 * Each iteration dispatches two Metal kernels:
 *   1. srad1_kernel — computes directional gradients and diffusion coefficient
 *   2. srad2_kernel — updates the image using the diffusion coefficients
 *
 * Usage:
 *   ./rodinia_srad [--image_size N] [--num_iterations K]
 *
 *   --image_size N      Image dimension (N×N)               (default: 512)
 *   --num_iterations K  SRAD iterations per timed launch    (default: 50)
 *   --iterations I      Timed benchmark iterations          (default: 20)
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

static double ticks_to_ms(uint64_t ticks) {
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
// Run one SRAD iteration batch (num_iters srad1+srad2 pairs)
// ---------------------------------------------------------------------------

static void runSRADIters(
    id<MTLCommandQueue>         queue,
    id<MTLComputePipelineState> pso1,    // srad1_kernel
    id<MTLComputePipelineState> pso2,    // srad2_kernel
    id<MTLBuffer>               buf_J,
    id<MTLBuffer>               buf_dN,
    id<MTLBuffer>               buf_dS,
    id<MTLBuffer>               buf_dW,
    id<MTLBuffer>               buf_dE,
    id<MTLBuffer>               buf_c,
    uint32_t                    dims[4],    // {rows, cols, 0, 0}
    float                       fparams[4], // {q0sqr, lambda, 0, 0}
    int                         num_iters,
    MTLSize                     grid_size,
    MTLSize                     tg_size)
{
    for (int k = 0; k < num_iters; ++k) {
        // --- srad1: compute gradients and diffusion coefficients ---
        {
            id<MTLCommandBuffer>         cb  = [queue commandBuffer];
            id<MTLComputeCommandEncoder> enc = [cb computeCommandEncoder];
            [enc setComputePipelineState:pso1];
            [enc setBuffer:buf_J  offset:0 atIndex:0];
            [enc setBuffer:buf_dN offset:0 atIndex:1];
            [enc setBuffer:buf_dS offset:0 atIndex:2];
            [enc setBuffer:buf_dW offset:0 atIndex:3];
            [enc setBuffer:buf_dE offset:0 atIndex:4];
            [enc setBuffer:buf_c  offset:0 atIndex:5];
            [enc setBytes:dims    length:sizeof(uint32_t)*4 atIndex:6];
            [enc setBytes:fparams length:sizeof(float)*4    atIndex:7];
            [enc dispatchThreads:grid_size threadsPerThreadgroup:tg_size];
            [enc endEncoding];
            [cb commit];
            [cb waitUntilCompleted];
        }

        // --- srad2: update image ---
        {
            id<MTLCommandBuffer>         cb  = [queue commandBuffer];
            id<MTLComputeCommandEncoder> enc = [cb computeCommandEncoder];
            [enc setComputePipelineState:pso2];
            [enc setBuffer:buf_J  offset:0 atIndex:0];
            [enc setBuffer:buf_dN offset:0 atIndex:1];
            [enc setBuffer:buf_dS offset:0 atIndex:2];
            [enc setBuffer:buf_dW offset:0 atIndex:3];
            [enc setBuffer:buf_dE offset:0 atIndex:4];
            [enc setBuffer:buf_c  offset:0 atIndex:5];
            [enc setBytes:dims    length:sizeof(uint32_t)*4 atIndex:6];
            [enc setBytes:fparams length:sizeof(float)*4    atIndex:7];
            [enc dispatchThreads:grid_size threadsPerThreadgroup:tg_size];
            [enc endEncoding];
            [cb commit];
            [cb waitUntilCompleted];
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

    // Defaults
    int image_size  = 512;
    int num_iters   = 50;
    int block_dim   = 16;

    // Command-line fallback
    image_size = parseIntParam(argc, argv, "--image_size", image_size);
    num_iters  = parseIntParam(argc, argv, "--num_iterations", num_iters);

    float lambda = 0.25f;
    for (int i = 1; i < argc - 1; ++i) {
        if (strcmp(argv[i], "--lambda") == 0) {
            lambda = (float)atof(argv[i + 1]);
            break;
        }
    }

    // Environment variable overrides
    const char* env_val;
    env_val = getenv("BENCH_PARAM_image_size");
    if (env_val) image_size = atoi(env_val);
    env_val = getenv("BENCH_PARAM_num_iterations");
    if (env_val) num_iters = atoi(env_val);
    env_val = getenv("BENCH_PARAM_block_size");
    if (env_val) block_dim = atoi(env_val);
    int num_warmup = 0;

    int    rows = image_size;
    int    cols = image_size;
    int    size = rows * cols;
    size_t bytes = (size_t)size * sizeof(float);

    float q0sqr = 0.05f;  // speckle noise parameter

    // -------------------------------------------------------------------
    // Metal device setup
    // -------------------------------------------------------------------
    id<MTLDevice> device = MTLCreateSystemDefaultDevice();
    if (!device) {
        fprintf(stderr, "Error: no Metal device found\n");
        return EXIT_FAILURE;
    }
    fprintf(stderr, "Device: %s\n", device.name.UTF8String);
    fprintf(stderr, "Image: %d×%d  |  SRAD iters/launch: %d\n\n",
            rows, cols, num_iters);

    id<MTLCommandQueue> queue = [device newCommandQueue];

    // -------------------------------------------------------------------
    // Compile Metal shader from source file next to the binary
    // -------------------------------------------------------------------
    NSError *err = nil;
    id<MTLLibrary> library = nil;

    NSString *exePath = [[NSBundle mainBundle] executablePath];
    NSString *dir     = [exePath stringByDeletingLastPathComponent];
    NSString *srcPath = [dir stringByAppendingPathComponent:@"rodinia_srad.metal"];

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
        fprintf(stderr, "Error: rodinia_srad.metal not found at %s\n",
                srcPath.UTF8String);
        return EXIT_FAILURE;
    }

    id<MTLFunction> fn1 = [library newFunctionWithName:@"srad1_kernel"];
    id<MTLFunction> fn2 = [library newFunctionWithName:@"srad2_kernel"];
    if (!fn1 || !fn2) {
        fprintf(stderr, "Error: could not find srad1_kernel or srad2_kernel\n");
        return EXIT_FAILURE;
    }

    id<MTLComputePipelineState> pso1 =
        [device newComputePipelineStateWithFunction:fn1 error:&err];
    id<MTLComputePipelineState> pso2 =
        [device newComputePipelineStateWithFunction:fn2 error:&err];
    if (!pso1 || !pso2) {
        fprintf(stderr, "Error creating pipeline: %s\n",
                err.localizedDescription.UTF8String);
        return EXIT_FAILURE;
    }

    // -------------------------------------------------------------------
    // Allocate buffers
    // -------------------------------------------------------------------
    id<MTLBuffer> buf_J  = [device newBufferWithLength:bytes options:MTLResourceStorageModeShared];
    id<MTLBuffer> buf_dN = [device newBufferWithLength:bytes options:MTLResourceStorageModeShared];
    id<MTLBuffer> buf_dS = [device newBufferWithLength:bytes options:MTLResourceStorageModeShared];
    id<MTLBuffer> buf_dW = [device newBufferWithLength:bytes options:MTLResourceStorageModeShared];
    id<MTLBuffer> buf_dE = [device newBufferWithLength:bytes options:MTLResourceStorageModeShared];
    id<MTLBuffer> buf_c  = [device newBufferWithLength:bytes options:MTLResourceStorageModeShared];

    // Initialize image with random floats in [0, 1]
    float* h_J_init = (float*)malloc(bytes);
    srand(42);
    for (int i = 0; i < size; ++i) {
        h_J_init[i] = (float)rand() / (float)RAND_MAX;
    }

    // Parameters
    uint32_t dims[4]    = { (uint32_t)rows, (uint32_t)cols, 0, 0 };
    float    fparams[4] = { q0sqr, lambda, 0.0f, 0.0f };

    // Thread dispatch: one thread per pixel
    MTLSize tg_size   = MTLSizeMake(16, 16, 1);
    MTLSize grid_size = MTLSizeMake((NSUInteger)((cols + 15) / 16) * 16,
                                    (NSUInteger)((rows + 15) / 16) * 16, 1);

    // -------------------------------------------------------------------
    // Warmup
    // -------------------------------------------------------------------
    for (int w = 0; w < num_warmup; ++w) {
        memcpy(buf_J.contents, h_J_init, bytes);
        runSRADIters(queue, pso1, pso2, buf_J, buf_dN, buf_dS, buf_dW, buf_dE, buf_c,
                     dims, fparams, num_iters, grid_size, tg_size);
    }

    // -------------------------------------------------------------------
    // Timed benchmark iterations
    // -------------------------------------------------------------------
    std::vector<double> times(1);
    for (int iter = 0; iter < 1; ++iter) {
        // Reset image to initial state before each timed run
        memcpy(buf_J.contents, h_J_init, bytes);

        uint64_t t0 = mach_absolute_time();
        runSRADIters(queue, pso1, pso2, buf_J, buf_dN, buf_dS, buf_dW, buf_dE, buf_c,
                     dims, fparams, num_iters, grid_size, tg_size);
        uint64_t t1 = mach_absolute_time();

        times[iter] = ticks_to_ms(t1 - t0);
    }

    // -------------------------------------------------------------------
    // Compute statistics and report
    // -------------------------------------------------------------------
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
    snprintf(problemSize, sizeof(problemSize), "%dx%d", rows, cols);

    BenchResult r;
    r.kernel_name  = "rodinia_srad";
    r.problem_size = problemSize;
    r.iterations = 1;
    r.avg_ms       = avg;
    r.min_ms       = mn;
    r.max_ms       = mx;
    r.stddev_ms    = stddev;

    // Effective bandwidth
    double bytes_per_iter = 13.0 * (double)size * sizeof(float);
    double total_bytes    = bytes_per_iter * num_iters;
    double bw_gb = total_bytes / (r.avg_ms * 1e-3) / 1e9;
    double total_ms = r.avg_ms;

    // JSON-lines kernel events
    printf("{\"type\":\"kernel\",\"name\":\"srad1\",\"time_ms\":%.6f,"
           "\"params\":{\"image_size\":%d,\"num_iterations\":%d,\"block_size\":%d}}\n",
           r.avg_ms, image_size, num_iters, block_dim);

    printf("{\"type\":\"kernel\",\"name\":\"srad2\",\"time_ms\":%.6f,"
           "\"params\":{\"image_size\":%d,\"num_iterations\":%d,\"block_size\":%d}}\n",
           r.avg_ms, image_size, num_iters, block_dim);

    // JSON-lines summary event
    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"effective_bandwidth_gbps\",\"value\":%.2f}]}\n",
           total_ms, bw_gb);

    fprintf(stderr, "Effective bandwidth: %.2f GB/s  (avg %.4f ms, %d SRAD iters/launch)\n",
            bw_gb, r.avg_ms, num_iters);

    free(h_J_init);
    return EXIT_SUCCESS;

    } // @autoreleasepool
}
