/**
 * polybench_correlation_metal.mm — Apple Metal host for PolyBench Correlation.
 *
 * Compute correlation matrix from a data matrix (M samples x N features).
 * Steps: mean, stddev, normalize, correlation (matmul-like).
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

    int N          = parseIntParam(argc, argv, "--size", 1024);
    int block_size = parseIntParam(argc, argv, "--block_size", 256);
    const char* precision = "float";

    const char* env_val;
    env_val = getenv("BENCH_PARAM_size");
    if (env_val) N = atoi(env_val);
    env_val = getenv("BENCH_PARAM_block_size");
    if (env_val) block_size = atoi(env_val);
    env_val = getenv("BENCH_PARAM_precision");
    if (env_val) precision = env_val;
    int num_warmup = 0;

    int M = N;

    size_t data_bytes = (size_t)M * N * sizeof(float);
    size_t vec_bytes  = (size_t)N * sizeof(float);
    size_t corr_bytes = (size_t)N * N * sizeof(float);

    // -------------------------------------------------------------------
    // Metal device setup
    // -------------------------------------------------------------------
    id<MTLDevice> device = MTLCreateSystemDefaultDevice();
    if (!device) {
        fprintf(stderr, "Error: no Metal device found\n");
        return EXIT_FAILURE;
    }
    fprintf(stderr, "Device: %s\n", device.name.UTF8String);
    fprintf(stderr, "Data matrix: %dx%d\n\n", M, N);

    id<MTLCommandQueue> queue = [device newCommandQueue];

    // -------------------------------------------------------------------
    // Compile Metal shader
    // -------------------------------------------------------------------
    NSError *err = nil;
    id<MTLLibrary> library = nil;

    NSString *exePath = [[NSBundle mainBundle] executablePath];
    NSString *dir     = [exePath stringByDeletingLastPathComponent];
    NSString *srcPath = [dir stringByAppendingPathComponent:@"polybench_correlation.metal"];

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
        fprintf(stderr, "Error: polybench_correlation.metal not found at %s\n",
                srcPath.UTF8String);
        return EXIT_FAILURE;
    }

    // -------------------------------------------------------------------
    // Create pipeline states for all 4 kernels
    // -------------------------------------------------------------------
    auto make_pso = [&](const char* name) -> id<MTLComputePipelineState> {
        id<MTLFunction> fn = [library newFunctionWithName:
            [NSString stringWithUTF8String:name]];
        if (!fn) {
            fprintf(stderr, "Error: kernel '%s' not found\n", name);
            exit(EXIT_FAILURE);
        }
        NSError *e = nil;
        id<MTLComputePipelineState> p =
            [device newComputePipelineStateWithFunction:fn error:&e];
        if (!p) {
            fprintf(stderr, "Error creating pipeline for '%s': %s\n",
                    name, e.localizedDescription.UTF8String);
            exit(EXIT_FAILURE);
        }
        return p;
    };

    id<MTLComputePipelineState> pso_mean   = make_pso("mean_kernel");
    id<MTLComputePipelineState> pso_stddev = make_pso("stddev_kernel");
    id<MTLComputePipelineState> pso_norm   = make_pso("normalize_kernel");
    id<MTLComputePipelineState> pso_corr   = make_pso("correlation_kernel");

    // -------------------------------------------------------------------
    // Allocate Metal buffers
    // -------------------------------------------------------------------
    id<MTLBuffer> buf_data   = [device newBufferWithLength:data_bytes
                                                   options:MTLResourceStorageModeShared];
    id<MTLBuffer> buf_mean   = [device newBufferWithLength:vec_bytes
                                                   options:MTLResourceStorageModeShared];
    id<MTLBuffer> buf_stddev = [device newBufferWithLength:vec_bytes
                                                   options:MTLResourceStorageModeShared];
    id<MTLBuffer> buf_corr   = [device newBufferWithLength:corr_bytes
                                                   options:MTLResourceStorageModeShared];

    // Keep a copy for re-initialization
    float* h_data_orig = (float*)malloc(data_bytes);
    srand(42);
    for (size_t i = 0; i < (size_t)M * N; ++i)
        h_data_orig[i] = (float)(rand() % 1000) / 100.0f;

    uint32_t params_data[4] = { (uint32_t)M, (uint32_t)N, 0, 0 };

    NSUInteger tg_1d = (NSUInteger)block_size;
    NSUInteger grid_n  = (NSUInteger)((N + block_size - 1) / block_size) * block_size;
    NSUInteger grid_mn = (NSUInteger)(((size_t)M * N + block_size - 1) / block_size) * block_size;

    NSUInteger tg_2d = 16;
    NSUInteger corr_grid_x = (NSUInteger)((N + 15) / 16) * 16;
    NSUInteger corr_grid_y = corr_grid_x;

    // Helper to run all 4 kernels
    auto run_pipeline = [&](double* t_mean_out, double* t_stddev_out,
                            double* t_norm_out, double* t_corr_out) {
        // Re-upload data
        memcpy(buf_data.contents, h_data_orig, data_bytes);

        // Mean kernel
        uint64_t t0, t1;
        {
            t0 = mach_absolute_time();
            id<MTLCommandBuffer> cb = [queue commandBuffer];
            id<MTLComputeCommandEncoder> enc = [cb computeCommandEncoder];
            [enc setComputePipelineState:pso_mean];
            [enc setBuffer:buf_data offset:0 atIndex:0];
            [enc setBuffer:buf_mean offset:0 atIndex:1];
            [enc setBytes:params_data length:sizeof(params_data) atIndex:3];
            [enc dispatchThreads:MTLSizeMake(grid_n, 1, 1)
                threadsPerThreadgroup:MTLSizeMake(tg_1d, 1, 1)];
            [enc endEncoding];
            [cb commit];
            [cb waitUntilCompleted];
            t1 = mach_absolute_time();
            *t_mean_out = ticks_to_ms(t1 - t0);
        }

        // Stddev kernel
        {
            t0 = mach_absolute_time();
            id<MTLCommandBuffer> cb = [queue commandBuffer];
            id<MTLComputeCommandEncoder> enc = [cb computeCommandEncoder];
            [enc setComputePipelineState:pso_stddev];
            [enc setBuffer:buf_data   offset:0 atIndex:0];
            [enc setBuffer:buf_mean   offset:0 atIndex:1];
            [enc setBuffer:buf_stddev offset:0 atIndex:2];
            [enc setBytes:params_data length:sizeof(params_data) atIndex:3];
            [enc dispatchThreads:MTLSizeMake(grid_n, 1, 1)
                threadsPerThreadgroup:MTLSizeMake(tg_1d, 1, 1)];
            [enc endEncoding];
            [cb commit];
            [cb waitUntilCompleted];
            t1 = mach_absolute_time();
            *t_stddev_out = ticks_to_ms(t1 - t0);
        }

        // Normalize kernel
        {
            t0 = mach_absolute_time();
            id<MTLCommandBuffer> cb = [queue commandBuffer];
            id<MTLComputeCommandEncoder> enc = [cb computeCommandEncoder];
            [enc setComputePipelineState:pso_norm];
            [enc setBuffer:buf_data   offset:0 atIndex:0];
            [enc setBuffer:buf_mean   offset:0 atIndex:1];
            [enc setBuffer:buf_stddev offset:0 atIndex:2];
            [enc setBytes:params_data length:sizeof(params_data) atIndex:3];
            [enc dispatchThreads:MTLSizeMake(grid_mn, 1, 1)
                threadsPerThreadgroup:MTLSizeMake(tg_1d, 1, 1)];
            [enc endEncoding];
            [cb commit];
            [cb waitUntilCompleted];
            t1 = mach_absolute_time();
            *t_norm_out = ticks_to_ms(t1 - t0);
        }

        // Correlation kernel
        {
            t0 = mach_absolute_time();
            id<MTLCommandBuffer> cb = [queue commandBuffer];
            id<MTLComputeCommandEncoder> enc = [cb computeCommandEncoder];
            [enc setComputePipelineState:pso_corr];
            [enc setBuffer:buf_data offset:0 atIndex:0];
            [enc setBuffer:buf_corr offset:0 atIndex:1];
            [enc setBytes:params_data length:sizeof(params_data) atIndex:3];
            [enc dispatchThreads:MTLSizeMake(corr_grid_x, corr_grid_y, 1)
                threadsPerThreadgroup:MTLSizeMake(tg_2d, tg_2d, 1)];
            [enc endEncoding];
            [cb commit];
            [cb waitUntilCompleted];
            t1 = mach_absolute_time();
            *t_corr_out = ticks_to_ms(t1 - t0);
        }
    };

    // -------------------------------------------------------------------
    // Warmup
    // -------------------------------------------------------------------
    for (int w = 0; w < num_warmup; ++w) {
        double dummy[4];
        run_pipeline(&dummy[0], &dummy[1], &dummy[2], &dummy[3]);
    }

    // -------------------------------------------------------------------
    // Timed iterations
    // -------------------------------------------------------------------
    std::vector<double> t_mean_v(1), t_stddev_v(1),
                        t_norm_v(1), t_corr_v(1);

    for (int it = 0; it < 1; ++it) {
        run_pipeline(&t_mean_v[it], &t_stddev_v[it],
                     &t_norm_v[it], &t_corr_v[it]);
    }

    auto avg = [](const std::vector<double>& v) {
        double s = 0; for (auto x : v) s += x; return s / v.size();
    };

    double avg_mean   = avg(t_mean_v);
    double avg_stddev = avg(t_stddev_v);
    double avg_norm   = avg(t_norm_v);
    double avg_corr   = avg(t_corr_v);
    double total_ms   = avg_mean + avg_stddev + avg_norm + avg_corr;

    fprintf(stderr, "Kernel timings (avg ms): mean=%.4f  stddev=%.4f  normalize=%.4f  correlation=%.4f\n",
            avg_mean, avg_stddev, avg_norm, avg_corr);
    fprintf(stderr, "Total: %.4f ms\n", total_ms);

    // JSON-lines output
    printf("{\"type\":\"kernel\",\"name\":\"mean_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"size\":%d\"block_size\":%d,"
           "\"precision\":\"%s\"}}\n",
           avg_mean, N, block_size, precision);
    printf("{\"type\":\"kernel\",\"name\":\"stddev_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"size\":%d\"block_size\":%d,"
           "\"precision\":\"%s\"}}\n",
           avg_stddev, N, block_size, precision);
    printf("{\"type\":\"kernel\",\"name\":\"normalize_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"size\":%d\"block_size\":%d,"
           "\"precision\":\"%s\"}}\n",
           avg_norm, N, block_size, precision);
    printf("{\"type\":\"kernel\",\"name\":\"correlation_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"size\":%d\"block_size\":%d,"
           "\"precision\":\"%s\"}}\n",
           avg_corr, N, block_size, precision);
    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[]}\n",
           total_ms);

    free(h_data_orig);

    return EXIT_SUCCESS;

    } // @autoreleasepool
}
