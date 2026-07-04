/**
 * shoc_scan_metal.mm — Apple Metal host for the SHOC Scan benchmark.
 *
 * Exclusive prefix scan (sum): output[i] = sum(input[0..i-1])
 * Implements a work-efficient (Blelloch) multi-pass scan for arbitrarily
 * large arrays.  Derived from the SHOC benchmark suite.
 *
 * The Metal compute kernels live in shoc_scan.metal and are compiled at
 * runtime from source (loaded from the same directory as the binary).
 *
 * Parameters are read from BENCH_PARAM_* environment variables,
 * with command-line --flags as fallback.
 *
 * Output (stdout): JSON-lines protocol
 * Output (stderr): Human-readable diagnostics
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
    if (tb.denom == 0) {
        mach_timebase_info(&tb);
    }
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

static const char* parseStrParam(int argc, char** argv, const char* name,
                                 const char* defaultVal) {
    for (int i = 1; i < argc - 1; ++i) {
        if (strcmp(argv[i], name) == 0) {
            return argv[i + 1];
        }
    }
    return defaultVal;
}

// ---------------------------------------------------------------------------
// Metal scan dispatch helpers
// ---------------------------------------------------------------------------

static NSUInteger dispatch_scan_block(
    id<MTLCommandQueue>         queue,
    id<MTLComputePipelineState> pso_scan,
    NSUInteger                  tg_size,
    id<MTLBuffer>               buf_input,
    id<MTLBuffer>               buf_output,
    id<MTLBuffer>               buf_block_sums,
    NSUInteger                  N)
{
    NSUInteger elems_per_tg = tg_size * 2;
    NSUInteger num_groups   = (N + elems_per_tg - 1) / elems_per_tg;

    uint32_t n32 = (uint32_t)N;

    id<MTLCommandBuffer>       cb  = [queue commandBuffer];
    id<MTLComputeCommandEncoder> enc = [cb computeCommandEncoder];
    [enc setComputePipelineState:pso_scan];
    [enc setBuffer:buf_input      offset:0 atIndex:0];
    [enc setBuffer:buf_output     offset:0 atIndex:1];
    [enc setBuffer:buf_block_sums offset:0 atIndex:2];
    [enc setBytes:&n32 length:sizeof(n32)  atIndex:3];
    [enc setThreadgroupMemoryLength:elems_per_tg * sizeof(float) atIndex:0];
    [enc dispatchThreadgroups:MTLSizeMake(num_groups, 1, 1)
       threadsPerThreadgroup:MTLSizeMake(tg_size,     1, 1)];
    [enc endEncoding];
    [cb commit];
    [cb waitUntilCompleted];

    return num_groups;
}

static void dispatch_add_block_sums(
    id<MTLCommandQueue>         queue,
    id<MTLComputePipelineState> pso_add,
    NSUInteger                  tg_size,
    id<MTLBuffer>               buf_data,
    id<MTLBuffer>               buf_scanned_block_sums,
    NSUInteger                  N,
    NSUInteger                  num_groups)
{
    uint32_t n32 = (uint32_t)N;

    id<MTLCommandBuffer>       cb  = [queue commandBuffer];
    id<MTLComputeCommandEncoder> enc = [cb computeCommandEncoder];
    [enc setComputePipelineState:pso_add];
    [enc setBuffer:buf_data                offset:0 atIndex:0];
    [enc setBuffer:buf_scanned_block_sums  offset:0 atIndex:1];
    [enc setBytes:&n32 length:sizeof(n32)            atIndex:2];
    [enc dispatchThreadgroups:MTLSizeMake(num_groups, 1, 1)
       threadsPerThreadgroup:MTLSizeMake(tg_size,     1, 1)];
    [enc endEncoding];
    [cb commit];
    [cb waitUntilCompleted];
}

// Forward declaration
static void scan_recursive_metal(
    id<MTLCommandQueue>         queue,
    id<MTLComputePipelineState> pso_scan,
    id<MTLComputePipelineState> pso_add,
    id<MTLDevice>               device,
    NSUInteger                  tg_size,
    id<MTLBuffer>               buf_input,
    id<MTLBuffer>               buf_output,
    NSUInteger                  N);

static void scan_recursive_metal(
    id<MTLCommandQueue>         queue,
    id<MTLComputePipelineState> pso_scan,
    id<MTLComputePipelineState> pso_add,
    id<MTLDevice>               device,
    NSUInteger                  tg_size,
    id<MTLBuffer>               buf_input,
    id<MTLBuffer>               buf_output,
    NSUInteger                  N)
{
    NSUInteger elems_per_tg = tg_size * 2;
    NSUInteger num_groups   = (N + elems_per_tg - 1) / elems_per_tg;

    id<MTLBuffer> buf_block_sums =
        [device newBufferWithLength:num_groups * sizeof(float)
                            options:MTLResourceStorageModeShared];

    dispatch_scan_block(queue, pso_scan, tg_size,
                        buf_input, buf_output, buf_block_sums, N);

    if (num_groups > 1) {
        id<MTLBuffer> buf_scanned_block_sums =
            [device newBufferWithLength:num_groups * sizeof(float)
                                options:MTLResourceStorageModeShared];

        scan_recursive_metal(queue, pso_scan, pso_add, device, tg_size,
                             buf_block_sums, buf_scanned_block_sums, num_groups);

        dispatch_add_block_sums(queue, pso_add, tg_size,
                                buf_output, buf_scanned_block_sums, N, num_groups);
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
    int array_size = 1048576;
    int block_size = 256;
    const char* precision = "float";

    // Command-line fallback
    array_size = parseIntParam(argc, argv, "--array_size", array_size);
    block_size = parseIntParam(argc, argv, "--block_size", block_size);
    precision  = parseStrParam(argc, argv, "--precision", precision);

    // Environment variable overrides
    const char* env_val;
    env_val = getenv("BENCH_PARAM_array_size");
    if (env_val) array_size = atoi(env_val);
    env_val = getenv("BENCH_PARAM_block_size");
    if (env_val) block_size = atoi(env_val);
    env_val = getenv("BENCH_PARAM_precision");
    if (env_val) precision = env_val;
    int num_warmup = 0;

    int N     = array_size;

    // -------------------------------------------------------------------
    // Metal device setup
    // -------------------------------------------------------------------
    id<MTLDevice> device = MTLCreateSystemDefaultDevice();
    if (!device) {
        fprintf(stderr, "Error: no Metal device found\n");
        return EXIT_FAILURE;
    }
    fprintf(stderr, "Device: %s\n", device.name.UTF8String);

    id<MTLCommandQueue> queue = [device newCommandQueue];

    // -------------------------------------------------------------------
    // Compile Metal shader from source file next to the binary
    // -------------------------------------------------------------------
    NSError        *err     = nil;
    id<MTLLibrary>  library = nil;

    NSString *exePath = [[NSBundle mainBundle] executablePath];
    NSString *dir     = [exePath stringByDeletingLastPathComponent];
    NSString *srcPath = [dir stringByAppendingPathComponent:@"shoc_scan.metal"];

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
        fprintf(stderr, "Error: shoc_scan.metal not found at %s\n",
                srcPath.UTF8String);
        return EXIT_FAILURE;
    }

    id<MTLFunction> fn_scan = [library newFunctionWithName:@"scan_block_kernel"];
    if (!fn_scan) {
        fprintf(stderr, "Error: kernel 'scan_block_kernel' not found\n");
        return EXIT_FAILURE;
    }
    id<MTLComputePipelineState> pso_scan =
        [device newComputePipelineStateWithFunction:fn_scan error:&err];
    if (!pso_scan) {
        fprintf(stderr, "Error creating scan pipeline: %s\n",
                err.localizedDescription.UTF8String);
        return EXIT_FAILURE;
    }

    id<MTLFunction> fn_add = [library newFunctionWithName:@"add_block_sums_kernel"];
    if (!fn_add) {
        fprintf(stderr, "Error: kernel 'add_block_sums_kernel' not found\n");
        return EXIT_FAILURE;
    }
    id<MTLComputePipelineState> pso_add =
        [device newComputePipelineStateWithFunction:fn_add error:&err];
    if (!pso_add) {
        fprintf(stderr, "Error creating add pipeline: %s\n",
                err.localizedDescription.UTF8String);
        return EXIT_FAILURE;
    }

    NSUInteger tg_size = pso_scan.maxTotalThreadsPerThreadgroup;
    if (tg_size > 256) tg_size = 256;

    // Round tg_size down to power-of-2
    NSUInteger tg_pow2 = 1;
    while (tg_pow2 * 2 <= tg_size) tg_pow2 <<= 1;
    tg_size = tg_pow2;

    // Pad N_padded
    NSUInteger elems_per_tg = tg_size * 2;
    NSUInteger N_padded = ((NSUInteger)N + elems_per_tg - 1) / elems_per_tg
                          * elems_per_tg;

    size_t bytes        = N_padded * sizeof(float);
    size_t bytes_orig   = (size_t)N * sizeof(float);

    fprintf(stderr, "Array size: %d floats (%.1f MiB, padded to %lu)\n",
            N, (double)bytes_orig / (1024.0 * 1024.0), (unsigned long)N_padded);
    fprintf(stderr, "Threadgroup size: %lu\n\n",
            (unsigned long)tg_size);

    // -------------------------------------------------------------------
    // Allocate GPU buffers
    // -------------------------------------------------------------------
    id<MTLBuffer> buf_input  = [device newBufferWithLength:bytes
                                                   options:MTLResourceStorageModeShared];
    id<MTLBuffer> buf_output = [device newBufferWithLength:bytes
                                                   options:MTLResourceStorageModeShared];

    float* h_input = (float*)buf_input.contents;
    for (NSUInteger i = 0; i < N_padded; ++i) {
        h_input[i] = (i < (NSUInteger)N) ? ((float)(i % 7) + 1.0f) : 0.0f;
    }

    // -------------------------------------------------------------------
    // Warmup
    // -------------------------------------------------------------------
    for (int w = 0; w < num_warmup; ++w) {
        scan_recursive_metal(queue, pso_scan, pso_add, device, tg_size,
                             buf_input, buf_output, N_padded);
    }

    // -------------------------------------------------------------------
    // Timed iterations
    // -------------------------------------------------------------------
    std::vector<double> times(1);
    for (int i = 0; i < 1; ++i) {
        uint64_t t0 = mach_absolute_time();
        scan_recursive_metal(queue, pso_scan, pso_add, device, tg_size,
                             buf_input, buf_output, N_padded);
        uint64_t t1 = mach_absolute_time();
        times[i] = ticks_to_ms(t1 - t0);
    }

    // Compute statistics
    double sum_t = 0.0, mn = DBL_MAX, mx = 0.0;
    for (int i = 0; i < 1; ++i) {
        sum_t += times[i];
        if (times[i] < mn) mn = times[i];
        if (times[i] > mx) mx = times[i];
    }
    double avg_ms = sum_t / 1;
    double total_ms = sum_t;

    // Effective bandwidth
    double bandwidth_gb = 2.0 * (double)N * sizeof(float)
                          / (avg_ms * 1e-3) / 1e9;

    // JSON-lines: one event per kernel listed in params.json
    printf("{\"type\":\"kernel\",\"name\":\"scan_block_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"array_size\":%d,\"block_size\":%d"
           "\"precision\":\"%s\"}}\n",
           avg_ms, array_size, block_size, precision);

    printf("{\"type\":\"kernel\",\"name\":\"add_block_sums_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"array_size\":%d,\"block_size\":%d"
           "\"precision\":\"%s\"}}\n",
           avg_ms, array_size, block_size, precision);

    // JSON-lines summary event
    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"bandwidth_gbps\",\"value\":%.2f}]}\n",
           total_ms, bandwidth_gb);

    fprintf(stderr, "Effective bandwidth: %.2f GB/s  (avg %.4f ms)\n",
            bandwidth_gb, avg_ms);

    // -------------------------------------------------------------------
    // Verification
    // -------------------------------------------------------------------
    scan_recursive_metal(queue, pso_scan, pso_add, device, tg_size,
                         buf_input, buf_output, N_padded);

    const float* h_output = (const float*)buf_output.contents;

    std::vector<float> h_ref((size_t)N);
    h_ref[0] = 0.0f;
    for (int i = 1; i < N; ++i) {
        h_ref[i] = h_ref[i - 1] + h_input[i - 1];
    }

    int errors = 0;
    for (int i = 0; i < N; ++i) {
        float diff = fabsf(h_output[i] - h_ref[i]);
        float tol  = 1e-3f * fabsf(h_ref[i]) + 1e-5f;
        if (diff > tol) {
            if (errors < 10) {
                fprintf(stderr, "Mismatch at %d: got %.6f, expected %.6f\n",
                        i, h_output[i], h_ref[i]);
            }
            errors++;
        }
    }
    if (errors > 0) {
        fprintf(stderr, "FAIL: %d errors out of %d\n", errors, N);
    } else {
        fprintf(stderr, "PASS\n");
    }

    return (errors > 0) ? EXIT_FAILURE : EXIT_SUCCESS;

    } // @autoreleasepool
}
