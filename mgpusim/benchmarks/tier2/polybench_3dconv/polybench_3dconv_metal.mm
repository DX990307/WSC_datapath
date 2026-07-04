/**
 * polybench_3dconv_metal.mm — Apple Metal host for PolyBench 3D Convolution.
 *
 * 3D convolution over an NxNxN volume with a small 3D filter (e.g., 3x3x3).
 * Each output point is the weighted sum of its neighborhood.
 *
 * Output (stdout): JSON-lines
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
// main
// ---------------------------------------------------------------------------

int main(int argc, char* argv[])
{
    @autoreleasepool {
    setvbuf(stdout, NULL, _IONBF, 0);

    int N           = parseIntParam(argc, argv, "--size", 128);
    int block_size  = parseIntParam(argc, argv, "--block_size", 8);
    int filter_size = parseIntParam(argc, argv, "--filter_size", 3);

    const char* env_val;
    env_val = getenv("BENCH_PARAM_size");
    if (env_val) N = atoi(env_val);
    env_val = getenv("BENCH_PARAM_block_size");
    if (env_val) block_size = atoi(env_val);
    env_val = getenv("BENCH_PARAM_filter_size");
    if (env_val) filter_size = atoi(env_val);
    int num_warmup = 0;

    size_t vol_elems   = (size_t)N * N * N;
    size_t vol_bytes   = vol_elems * sizeof(float);
    size_t filt_elems  = (size_t)filter_size * filter_size * filter_size;
    size_t filt_bytes  = filt_elems * sizeof(float);

    // -------------------------------------------------------------------
    // Metal device setup
    // -------------------------------------------------------------------
    id<MTLDevice> device = MTLCreateSystemDefaultDevice();
    if (!device) {
        fprintf(stderr, "Error: no Metal device found\n");
        return EXIT_FAILURE;
    }
    fprintf(stderr, "Device: %s\n", device.name.UTF8String);
    fprintf(stderr, "Volume: %dx%dx%d  |  Filter: %dx%dx%d\n\n",
            N, N, N, filter_size, filter_size, filter_size);

    id<MTLCommandQueue> queue = [device newCommandQueue];

    // -------------------------------------------------------------------
    // Compile Metal shader
    // -------------------------------------------------------------------
    NSError *err = nil;
    id<MTLLibrary> library = nil;

    NSString *exePath = [[NSBundle mainBundle] executablePath];
    NSString *dir     = [exePath stringByDeletingLastPathComponent];
    NSString *srcPath = [dir stringByAppendingPathComponent:@"polybench_3dconv.metal"];

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
        fprintf(stderr, "Error: polybench_3dconv.metal not found at %s\n",
                srcPath.UTF8String);
        return EXIT_FAILURE;
    }

    id<MTLFunction> fn = [library newFunctionWithName:@"conv3d_kernel"];
    if (!fn) {
        fprintf(stderr, "Error: kernel 'conv3d_kernel' not found\n");
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
    // Allocate Metal buffers
    // -------------------------------------------------------------------
    id<MTLBuffer> buf_input  = [device newBufferWithLength:vol_bytes
                                                   options:MTLResourceStorageModeShared];
    id<MTLBuffer> buf_filter = [device newBufferWithLength:filt_bytes
                                                   options:MTLResourceStorageModeShared];
    id<MTLBuffer> buf_output = [device newBufferWithLength:vol_bytes
                                                   options:MTLResourceStorageModeShared];

    // Initialize data
    float* h_input  = (float*)buf_input.contents;
    float* h_filter = (float*)buf_filter.contents;

    srand(42);
    for (size_t i = 0; i < vol_elems; ++i)
        h_input[i] = (float)(rand() % 100) / 100.0f;
    float filt_sum = 0.0f;
    for (size_t i = 0; i < filt_elems; ++i) {
        h_filter[i] = (float)(rand() % 10 + 1);
        filt_sum += h_filter[i];
    }
    for (size_t i = 0; i < filt_elems; ++i)
        h_filter[i] /= filt_sum;

    // Kernel parameters
    uint32_t params_data[4] = { (uint32_t)N, (uint32_t)filter_size, 0, 0 };

    NSUInteger tg = (NSUInteger)block_size;
    NSUInteger grid_x = (NSUInteger)((N + block_size - 1) / block_size) * block_size;
    NSUInteger grid_y = grid_x;
    NSUInteger grid_z = grid_x;

    auto dispatch_conv = [&]() {
        id<MTLCommandBuffer>         cb  = [queue commandBuffer];
        id<MTLComputeCommandEncoder> enc = [cb computeCommandEncoder];
        [enc setComputePipelineState:pso];
        [enc setBuffer:buf_input  offset:0 atIndex:0];
        [enc setBuffer:buf_filter offset:0 atIndex:1];
        [enc setBuffer:buf_output offset:0 atIndex:2];
        [enc setBytes:params_data length:sizeof(params_data) atIndex:3];
        [enc dispatchThreads:MTLSizeMake(grid_x, grid_y, grid_z)
            threadsPerThreadgroup:MTLSizeMake(tg, tg, tg)];
        [enc endEncoding];
        [cb commit];
        [cb waitUntilCompleted];
    };

    // -------------------------------------------------------------------
    // Warmup
    // -------------------------------------------------------------------
    for (int w = 0; w < num_warmup; ++w) dispatch_conv();

    // -------------------------------------------------------------------
    // Timed iterations
    // -------------------------------------------------------------------
    std::vector<double> times(1);
    for (int i = 0; i < 1; ++i) {
        uint64_t t0 = mach_absolute_time();
        dispatch_conv();
        uint64_t t1 = mach_absolute_time();
        times[i] = ticks_to_ms(t1 - t0);
    }

    // Compute statistics
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

    double flops = 2.0 * (double)vol_elems * (double)filt_elems;
    double gflops = flops / (avg_ms * 1e-3) / 1e9;

    fprintf(stderr, "Performance: %.2f GFLOPS  (avg %.4f ms, min %.4f ms, max %.4f ms, stddev %.4f ms)\n",
            gflops, avg_ms, mn, mx, stddev);

    // JSON-lines output
    printf("{\"type\":\"kernel\",\"name\":\"conv3d_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"size\":%d\"block_size\":%d,"
           "\"filter_size\":%d}}\n",
           avg_ms, N, block_size, filter_size);
    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"gflops\",\"value\":%.2f}]}\n",
           avg_ms, gflops);

    return EXIT_SUCCESS;

    } // @autoreleasepool
}
