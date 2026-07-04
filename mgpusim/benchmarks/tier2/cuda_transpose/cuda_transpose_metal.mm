/**
 * cuda_transpose_metal.mm — Apple Metal host for shared-memory tiled matrix
 * transpose benchmark.
 *
 * Two variants on an N×N single-precision matrix (default 4096):
 *   1. Naive transpose      — coalesced reads, strided writes
 *   2. Optimized transpose  — threadgroup memory tile with +1 padding
 *
 * Usage:
 *   ./cuda_transpose [--size N]
 *
 *   --size N  Matrix dimension (default: 4096)
 *
 * Output (stdout): CSV rows —
 *   cuda_transpose,naive_<N>,<time_ms>,<GBs>
 *   cuda_transpose,optimized_<N>,<time_ms>,<GBs>
 *
 * Output (stderr): human-readable results
 *
 * Effective bandwidth = 2 * N * N * sizeof(float) / time_s / 1e9
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

static int parseIntParam(int argc, char** argv, const char* name,
                         int defaultVal) {
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
// Constants
// ---------------------------------------------------------------------------

static const int TILE_DIM   = 32;
static const int BLOCK_ROWS =  8;
static int WARMUP_ITERS = 5;
static const int TIMED_ITERS  = 5;

// ---------------------------------------------------------------------------
// CPU reference transpose (for verification)
// ---------------------------------------------------------------------------

static void transpose_cpu(const float* in, float* out, int width, int height) {
    for (int y = 0; y < height; ++y) {
        for (int x = 0; x < width; ++x) {
            out[x * height + y] = in[y * width + x];
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

    // Default values from params.json
    int N          = 4096;
    int block_size = 256;
    int tile_dim   = 32;

    // Command-line args as fallback
    N          = parseIntParam(argc, argv, "--size", N);
    block_size = parseIntParam(argc, argv, "--block_size", block_size);
    tile_dim   = parseIntParam(argc, argv, "--tile_dim", tile_dim);

    // Env vars override
    const char* env_val;
    env_val = getenv("BENCH_PARAM_size");
    if (env_val) N = atoi(env_val);
    env_val = getenv("BENCH_PARAM_block_size");
    if (env_val) block_size = atoi(env_val);
    env_val = getenv("BENCH_PARAM_tile_dim");
    if (env_val) tile_dim = atoi(env_val);

    size_t num_elems = (size_t)N * N;
    size_t bytes     = num_elems * sizeof(float);

    // Effective bandwidth: read + write = 2 * N * N * sizeof(float)
    double data_bytes = 2.0 * (double)N * (double)N * sizeof(float);

    // -------------------------------------------------------------------
    // Metal device setup
    // -------------------------------------------------------------------
    id<MTLDevice> device = MTLCreateSystemDefaultDevice();
    if (!device) {
        fprintf(stderr, "Error: no Metal device found\n");
        return EXIT_FAILURE;
    }
    fprintf(stderr, "Device: %s\n", device.name.UTF8String);
    fprintf(stderr, "Matrix size: %d×%d  |  Warmup: %d  |  Timed: %d\n\n",
            N, N, WARMUP_ITERS, TIMED_ITERS);

    id<MTLCommandQueue> queue = [device newCommandQueue];

    // -------------------------------------------------------------------
    // Compile Metal shader from source file next to the binary
    // -------------------------------------------------------------------
    NSError *err = nil;
    id<MTLLibrary> library = nil;

    NSString *exePath = [[NSBundle mainBundle] executablePath];
    NSString *dir     = [exePath stringByDeletingLastPathComponent];
    NSString *srcPath = [dir stringByAppendingPathComponent:
                            @"cuda_transpose.metal"];

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
        fprintf(stderr, "Error: cuda_transpose.metal not found at %s\n",
                srcPath.UTF8String);
        return EXIT_FAILURE;
    }

    // Create pipeline states for both kernels
    id<MTLFunction> fn_naive = [library newFunctionWithName:@"transpose_naive"];
    id<MTLFunction> fn_opt   = [library newFunctionWithName:@"transpose_optimized"];
    if (!fn_naive || !fn_opt) {
        fprintf(stderr, "Error: kernel function(s) not found in shader\n");
        return EXIT_FAILURE;
    }

    id<MTLComputePipelineState> pso_naive =
        [device newComputePipelineStateWithFunction:fn_naive error:&err];
    if (!pso_naive) {
        fprintf(stderr, "Error creating naive pipeline: %s\n",
                err.localizedDescription.UTF8String);
        return EXIT_FAILURE;
    }

    id<MTLComputePipelineState> pso_opt =
        [device newComputePipelineStateWithFunction:fn_opt error:&err];
    if (!pso_opt) {
        fprintf(stderr, "Error creating optimized pipeline: %s\n",
                err.localizedDescription.UTF8String);
        return EXIT_FAILURE;
    }

    // -------------------------------------------------------------------
    // Allocate buffers
    // -------------------------------------------------------------------
    id<MTLBuffer> buf_idata = [device newBufferWithLength:bytes
                                                  options:MTLResourceStorageModeShared];
    id<MTLBuffer> buf_odata = [device newBufferWithLength:bytes
                                                  options:MTLResourceStorageModeShared];

    // Initialize input matrix
    float* h_idata = (float*)buf_idata.contents;
    for (size_t i = 0; i < num_elems; ++i) {
        h_idata[i] = (float)(i % 1000) * 0.001f;
    }

    // Dims constant
    uint32_t dims_data[2] = { (uint32_t)N, (uint32_t)N };

    // Threadgroup size: TILE_DIM × BLOCK_ROWS
    MTLSize tg_size = MTLSizeMake(TILE_DIM, BLOCK_ROWS, 1);

    // Grid in threadgroups
    NSUInteger grid_x = (N + TILE_DIM - 1) / TILE_DIM;
    NSUInteger grid_y = (N + TILE_DIM - 1) / TILE_DIM;
    MTLSize tg_count = MTLSizeMake(grid_x, grid_y, 1);

    // CPU reference for verification
    std::vector<float> h_ref(num_elems);
    transpose_cpu(h_idata, h_ref.data(), N, N);

    // Lambda to dispatch a transpose kernel
    auto dispatch_kernel = [&](id<MTLComputePipelineState> pso) {
        float* odata = (float*)buf_odata.contents;
        memset(odata, 0, bytes);

        id<MTLCommandBuffer>         cb  = [queue commandBuffer];
        id<MTLComputeCommandEncoder> enc = [cb computeCommandEncoder];
        [enc setComputePipelineState:pso];
        [enc setBuffer:buf_idata offset:0 atIndex:0];
        [enc setBuffer:buf_odata offset:0 atIndex:1];
        [enc setBytes:dims_data length:sizeof(dims_data) atIndex:2];
        [enc dispatchThreadgroups:tg_count
            threadsPerThreadgroup:tg_size];
        [enc endEncoding];
        [cb commit];
        [cb waitUntilCompleted];
    };

    // Lambda to benchmark a kernel: 5 warmup + 5 timed, return avg ms
    auto benchmark_kernel = [&](id<MTLComputePipelineState> pso) -> double {
        // Warmup
        for (int i = 0; i < WARMUP_ITERS; ++i) {
            dispatch_kernel(pso);
        }

        // Timed
        double total_ms = 0.0;
        for (int i = 0; i < TIMED_ITERS; ++i) {
            uint64_t t0 = mach_absolute_time();
            dispatch_kernel(pso);
            uint64_t t1 = mach_absolute_time();
            total_ms += ticks_to_ms(t1 - t0);
        }
        return total_ms / TIMED_ITERS;
    };

    // Lambda to verify output against CPU reference
    auto verify = [&](const char* name) -> bool {
        const float* odata = (const float*)buf_odata.contents;
        int errors = 0;
        int check_count = (num_elems < 1000) ? (int)num_elems : 1000;
        for (int i = 0; i < check_count; ++i) {
            if (fabsf(odata[i] - h_ref[i]) > 1e-5f) {
                if (errors < 5) {
                    fprintf(stderr, "  %s mismatch at %d: got %f, expected %f\n",
                            name, i, odata[i], h_ref[i]);
                }
                errors++;
            }
        }
        return (errors == 0);
    };

    // -------------------------------------------------------------------
    // Variant 1: Naive transpose
    // -------------------------------------------------------------------
    double naive_ms = benchmark_kernel(pso_naive);
    double naive_gbs = data_bytes / (naive_ms * 1e-3) / 1e9;

    fprintf(stderr, "Naive transpose:     avg %.4f ms  |  %.2f GB/s\n",
            naive_ms, naive_gbs);

    // Verify (run one more dispatch to get fresh output)
    dispatch_kernel(pso_naive);
    bool naive_pass = verify("Naive");
    fprintf(stderr, "  Verification: %s\n", naive_pass ? "PASS" : "FAIL");

    // -------------------------------------------------------------------
    // Variant 2: Optimized transpose
    // -------------------------------------------------------------------
    double opt_ms = benchmark_kernel(pso_opt);
    double opt_gbs = data_bytes / (opt_ms * 1e-3) / 1e9;

    fprintf(stderr, "Optimized transpose: avg %.4f ms  |  %.2f GB/s\n",
            opt_ms, opt_gbs);

    // Verify
    dispatch_kernel(pso_opt);
    bool opt_pass = verify("Optimized");
    fprintf(stderr, "  Verification: %s\n", opt_pass ? "PASS" : "FAIL");

    // -------------------------------------------------------------------
    // JSON-lines output to stdout
    // -------------------------------------------------------------------
    double total_ms = naive_ms * TIMED_ITERS + opt_ms * TIMED_ITERS;

    printf("{\"type\":\"kernel\",\"name\":\"transpose_naive\",\"time_ms\":%.6f,"
           "\"params\":{\"size\":%d\"block_size\":%d,"
           "\"tile_dim\":%d}}\n",
           naive_ms, N, block_size, tile_dim);

    printf("{\"type\":\"kernel\",\"name\":\"transpose_optimized\",\"time_ms\":%.6f,"
           "\"params\":{\"size\":%d\"block_size\":%d,"
           "\"tile_dim\":%d}}\n",
           opt_ms, N, block_size, tile_dim);

    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"naive_bandwidth_gbps\",\"value\":%.2f},"
           "{\"name\":\"optimized_bandwidth_gbps\",\"value\":%.2f}]}\n",
           total_ms, naive_gbs, opt_gbs);

    return EXIT_SUCCESS;

    } // @autoreleasepool
}
