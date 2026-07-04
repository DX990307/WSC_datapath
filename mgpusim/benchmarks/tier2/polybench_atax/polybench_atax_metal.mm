/**
 * polybench_atax_metal.mm — Apple Metal host for the PolyBench ATAX benchmark.
 *
 * Computes y = A^T * (A * x) where A is NX×NY, x and y are NY-vectors.
 * Measures memory bandwidth in GB/s (A read twice per iteration).
 *
 * Usage:
 *   ./polybench_atax [--nx NX] [--ny NY]
 *
 *   --nx NX          Number of rows    (default: 4096)
 *   --ny NY          Number of columns (default: 4096)
 *   --iterations I   Timed iterations  (default: 5)
 *
 * Output (stdout): JSON-lines protocol
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

static void cpu_atax(const float* A, const float* x, float* y_ref, int nx, int ny)
{
    std::vector<float> tmp(nx, 0.0f);
    for (int i = 0; i < nx; i++)
        for (int j = 0; j < ny; j++)
            tmp[i] += A[i * ny + j] * x[j];
    for (int j = 0; j < ny; j++) {
        y_ref[j] = 0.0f;
        for (int i = 0; i < nx; i++)
            y_ref[j] += A[i * ny + j] * tmp[i];
    }
}

// ---------------------------------------------------------------------------
// main
// ---------------------------------------------------------------------------

int main(int argc, char* argv[])
{
    @autoreleasepool {

    setvbuf(stdout, NULL, _IONBF, 0);

    int nx    = parseIntParam(argc, argv, "--nx", 4096);
    int ny    = parseIntParam(argc, argv, "--ny", 4096);
    int block_size = 256;

    const char* env_val;
    env_val = getenv("BENCH_PARAM_nx");
    if (env_val) nx = atoi(env_val);
    env_val = getenv("BENCH_PARAM_ny");
    if (env_val) ny = atoi(env_val);
    env_val = getenv("BENCH_PARAM_block_size");
    if (env_val) block_size = atoi(env_val);
    int num_warmup = 0;

    size_t bytes_A   = (size_t)nx * ny * sizeof(float);
    size_t bytes_x   = (size_t)ny * sizeof(float);
    size_t bytes_tmp = (size_t)nx * sizeof(float);
    size_t bytes_y   = (size_t)ny * sizeof(float);

    // -------------------------------------------------------------------
    // Metal device setup
    // -------------------------------------------------------------------
    id<MTLDevice> device = MTLCreateSystemDefaultDevice();
    if (!device) {
        fprintf(stderr, "Error: no Metal device found\n");
        return EXIT_FAILURE;
    }
    fprintf(stderr, "Device: %s\n", device.name.UTF8String);
    fprintf(stderr, "Matrix size: %d×%d\n\n", nx, ny);

    id<MTLCommandQueue> queue = [device newCommandQueue];

    // -------------------------------------------------------------------
    // Compile Metal shader from source file next to the binary
    // -------------------------------------------------------------------
    NSError *err = nil;
    id<MTLLibrary> library = nil;

    NSString *exePath = [[NSBundle mainBundle] executablePath];
    NSString *dir     = [exePath stringByDeletingLastPathComponent];
    NSString *srcPath = [dir stringByAppendingPathComponent:@"polybench_atax.metal"];

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
        fprintf(stderr, "Error: polybench_atax.metal not found at %s\n",
                srcPath.UTF8String);
        return EXIT_FAILURE;
    }

    // Create pipeline states for both kernels
    id<MTLFunction> fn1 = [library newFunctionWithName:@"atax_kernel1"];
    if (!fn1) {
        fprintf(stderr, "Error: kernel 'atax_kernel1' not found\n");
        return EXIT_FAILURE;
    }
    id<MTLComputePipelineState> pso1 =
        [device newComputePipelineStateWithFunction:fn1 error:&err];
    if (!pso1) {
        fprintf(stderr, "Error creating pipeline for kernel1: %s\n",
                err.localizedDescription.UTF8String);
        return EXIT_FAILURE;
    }

    id<MTLFunction> fn2 = [library newFunctionWithName:@"atax_kernel2"];
    if (!fn2) {
        fprintf(stderr, "Error: kernel 'atax_kernel2' not found\n");
        return EXIT_FAILURE;
    }
    id<MTLComputePipelineState> pso2 =
        [device newComputePipelineStateWithFunction:fn2 error:&err];
    if (!pso2) {
        fprintf(stderr, "Error creating pipeline for kernel2: %s\n",
                err.localizedDescription.UTF8String);
        return EXIT_FAILURE;
    }

    // -------------------------------------------------------------------
    // Allocate Metal buffers (shared memory)
    // -------------------------------------------------------------------
    id<MTLBuffer> buf_A   = [device newBufferWithLength:bytes_A
                                                options:MTLResourceStorageModeShared];
    id<MTLBuffer> buf_x   = [device newBufferWithLength:bytes_x
                                                options:MTLResourceStorageModeShared];
    id<MTLBuffer> buf_tmp = [device newBufferWithLength:bytes_tmp
                                                options:MTLResourceStorageModeShared];
    id<MTLBuffer> buf_y   = [device newBufferWithLength:bytes_y
                                                options:MTLResourceStorageModeShared];

    // Initialize A and x
    float* h_A = (float*)buf_A.contents;
    float* h_x = (float*)buf_x.contents;

    srand(42);
    for (int i = 0; i < nx * ny; ++i)
        h_A[i] = (float)(rand() % 100) / 10.0f;
    for (int j = 0; j < ny; ++j)
        h_x[j] = (float)(rand() % 100) / 10.0f;

    // Kernel parameters: dims = {nx, ny, 0, 0}
    uint32_t dims_data[4] = { (uint32_t)nx, (uint32_t)ny, 0, 0 };

    // Thread configuration
    NSUInteger tg_size1 = MIN((NSUInteger)256, pso1.maxTotalThreadsPerThreadgroup);
    NSUInteger tg_size2 = MIN((NSUInteger)256, pso2.maxTotalThreadsPerThreadgroup);
    NSUInteger grid_size1 = (NSUInteger)nx;
    NSUInteger grid_size2 = (NSUInteger)ny;

    auto dispatch_kernel1 = [&]() {
        id<MTLCommandBuffer>         cb  = [queue commandBuffer];
        id<MTLComputeCommandEncoder> enc = [cb computeCommandEncoder];
        [enc setComputePipelineState:pso1];
        [enc setBuffer:buf_A   offset:0 atIndex:0];
        [enc setBuffer:buf_x   offset:0 atIndex:1];
        [enc setBuffer:buf_tmp offset:0 atIndex:2];
        [enc setBytes:dims_data length:sizeof(dims_data) atIndex:3];
        [enc dispatchThreads:MTLSizeMake(grid_size1, 1, 1)
            threadsPerThreadgroup:MTLSizeMake(tg_size1, 1, 1)];
        [enc endEncoding];
        [cb commit];
        [cb waitUntilCompleted];
    };

    auto dispatch_kernel2 = [&]() {
        id<MTLCommandBuffer>         cb  = [queue commandBuffer];
        id<MTLComputeCommandEncoder> enc = [cb computeCommandEncoder];
        [enc setComputePipelineState:pso2];
        [enc setBuffer:buf_A   offset:0 atIndex:0];
        [enc setBuffer:buf_tmp offset:0 atIndex:1];
        [enc setBuffer:buf_y   offset:0 atIndex:2];
        [enc setBytes:dims_data length:sizeof(dims_data) atIndex:3];
        [enc dispatchThreads:MTLSizeMake(grid_size2, 1, 1)
            threadsPerThreadgroup:MTLSizeMake(tg_size2, 1, 1)];
        [enc endEncoding];
        [cb commit];
        [cb waitUntilCompleted];
    };

    // -------------------------------------------------------------------
    // Warmup
    // -------------------------------------------------------------------
    for (int w = 0; w < num_warmup; ++w) {
        dispatch_kernel1();
        dispatch_kernel2();
    }

    // -------------------------------------------------------------------
    // Timed iterations — time each kernel separately
    // -------------------------------------------------------------------
    std::vector<double> k1_times(1);
    std::vector<double> k2_times(1);
    for (int i = 0; i < 1; ++i) {
        uint64_t t0, t1;

        t0 = mach_absolute_time();
        dispatch_kernel1();
        t1 = mach_absolute_time();
        k1_times[i] = ticks_to_ms(t1 - t0);

        t0 = mach_absolute_time();
        dispatch_kernel2();
        t1 = mach_absolute_time();
        k2_times[i] = ticks_to_ms(t1 - t0);
    }

    // -------------------------------------------------------------------
    // Correctness check against CPU reference (small subset)
    // -------------------------------------------------------------------
    int check_n = (ny < 8) ? ny : 8;
    std::vector<float> y_ref((size_t)ny);
    cpu_atax(h_A, h_x, y_ref.data(), nx, ny);

    float* h_y = (float*)buf_y.contents;
    bool pass = true;
    for (int j = 0; j < check_n; ++j) {
        float rel = fabsf(h_y[j] - y_ref[j]) / (fabsf(y_ref[j]) + 1e-6f);
        if (rel > 1e-3f) {
            fprintf(stderr, "MISMATCH at y[%d]: gpu=%.6f cpu=%.6f rel=%.4e\n",
                    j, h_y[j], y_ref[j], rel);
            pass = false;
        }
    }
    fprintf(stderr, "Correctness check (first %d elements): %s\n\n",
            check_n, pass ? "PASS" : "FAIL");

    // -------------------------------------------------------------------
    // Statistics
    // -------------------------------------------------------------------
    double k1_sum = 0.0, k2_sum = 0.0;
    for (int i = 0; i < 1; ++i) {
        k1_sum += k1_times[i];
        k2_sum += k2_times[i];
    }
    double k1_avg = k1_sum / 1;
    double k2_avg = k2_sum / 1;
    double total_avg_ms = k1_avg + k2_avg;

    // GB/s: A read twice (kernel1 + kernel2), each is NX*NY*4 bytes
    double gb   = 2.0 * (double)nx * (double)ny * sizeof(float) / 1e9;
    double gbps = gb / (total_avg_ms * 1e-3);

    fprintf(stderr, "Bandwidth: %.2f GB/s  (total avg %.4f ms: k1=%.4f k2=%.4f)\n",
            gbps, total_avg_ms, k1_avg, k2_avg);

    // JSON-lines output
    printf("{\"type\":\"kernel\",\"name\":\"atax_kernel1\",\"time_ms\":%.6f,"
           "\"params\":{\"nx\":%d,\"ny\":%d\"block_size\":%d}}\n",
           k1_avg, nx, ny, block_size);
    printf("{\"type\":\"kernel\",\"name\":\"atax_kernel2\",\"time_ms\":%.6f,"
           "\"params\":{\"nx\":%d,\"ny\":%d\"block_size\":%d}}\n",
           k2_avg, nx, ny, block_size);
    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"bandwidth_gbps\",\"value\":%.2f}]}\n",
           total_avg_ms, gbps);

    return EXIT_SUCCESS;

    } // @autoreleasepool
}
