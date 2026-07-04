/**
 * shoc_reduction_metal.mm — Apple Metal host for the SHOC Reduction benchmark.
 *
 * Parallel sum reduction: reduces a float array to a single sum using
 * tree-based threadgroup (shared-memory) reduction.  Derived from SHOC.
 *
 * The Metal compute kernel lives in shoc_reduction.metal and is compiled at
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
// Reduction dispatch helper
// ---------------------------------------------------------------------------

static id<MTLBuffer> dispatch_reduction(
    id<MTLCommandQueue>          queue,
    id<MTLComputePipelineState>  pso,
    NSUInteger                   tg_size,
    id<MTLBuffer>                buf_input,
    NSUInteger                   N_input,
    id<MTLBuffer>                buf_scratch1,
    id<MTLBuffer>                buf_scratch2)
{
    id<MTLBuffer> buf_src = buf_input;
    NSUInteger    n_src   = N_input;

    id<MTLBuffer> scratch[2] = { buf_scratch1, buf_scratch2 };
    int scratch_idx = 0;

    while (n_src > 1) {
        NSUInteger num_groups = (n_src + tg_size * 2 - 1) / (tg_size * 2);
        id<MTLBuffer> buf_dst = scratch[scratch_idx & 1];
        scratch_idx++;

        id<MTLCommandBuffer>       cb  = [queue commandBuffer];
        id<MTLComputeCommandEncoder> enc = [cb computeCommandEncoder];
        [enc setComputePipelineState:pso];
        [enc setBuffer:buf_src  offset:0 atIndex:0];
        [enc setBuffer:buf_dst  offset:0 atIndex:1];
        uint32_t n32 = (uint32_t)n_src;
        [enc setBytes:&n32 length:sizeof(n32) atIndex:2];
        [enc setThreadgroupMemoryLength:tg_size * sizeof(float) atIndex:0];
        [enc dispatchThreadgroups:MTLSizeMake(num_groups, 1, 1)
           threadsPerThreadgroup:MTLSizeMake(tg_size, 1, 1)];
        [enc endEncoding];
        [cb commit];
        [cb waitUntilCompleted];

        buf_src = buf_dst;
        n_src   = num_groups;
    }

    return buf_src;
}

// ---------------------------------------------------------------------------
// main
// ---------------------------------------------------------------------------

int main(int argc, char* argv[])
{
    @autoreleasepool {

    setvbuf(stdout, NULL, _IONBF, 0);

    // Defaults
    int size       = 4194304;
    int block_size = 256;
    const char* precision = "float";

    // Command-line fallback
    size       = parseIntParam(argc, argv, "--size", size);
    block_size = parseIntParam(argc, argv, "--block_size", block_size);
    precision  = parseStrParam(argc, argv, "--precision", precision);

    // Environment variable overrides
    const char* env_val;
    env_val = getenv("BENCH_PARAM_size");
    if (env_val) size = atoi(env_val);
    env_val = getenv("BENCH_PARAM_block_size");
    if (env_val) block_size = atoi(env_val);
    env_val = getenv("BENCH_PARAM_precision");
    if (env_val) precision = env_val;
    int num_warmup = 0;

    int N     = size;
    size_t bytes = (size_t)N * sizeof(float);

    // -------------------------------------------------------------------
    // Metal device setup
    // -------------------------------------------------------------------
    id<MTLDevice> device = MTLCreateSystemDefaultDevice();
    if (!device) {
        fprintf(stderr, "Error: no Metal device found\n");
        return EXIT_FAILURE;
    }
    fprintf(stderr, "Device: %s\n", device.name.UTF8String);
    fprintf(stderr, "Array size: %d floats (%.1f MiB)\n",
            N, (double)bytes / (1024.0 * 1024.0));
    fprintf(stderr, "\n\n");

    id<MTLCommandQueue> queue = [device newCommandQueue];

    // -------------------------------------------------------------------
    // Compile Metal shader from source file next to the binary
    // -------------------------------------------------------------------
    NSError   *err     = nil;
    id<MTLLibrary> library = nil;

    NSString *exePath = [[NSBundle mainBundle] executablePath];
    NSString *dir     = [exePath stringByDeletingLastPathComponent];
    NSString *srcPath = [dir stringByAppendingPathComponent:@"shoc_reduction.metal"];

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
        fprintf(stderr, "Error: shoc_reduction.metal not found at %s\n",
                srcPath.UTF8String);
        return EXIT_FAILURE;
    }

    id<MTLFunction> fn = [library newFunctionWithName:@"reduce_kernel"];
    if (!fn) {
        fprintf(stderr, "Error: kernel 'reduce_kernel' not found in library\n");
        return EXIT_FAILURE;
    }
    id<MTLComputePipelineState> pso =
        [device newComputePipelineStateWithFunction:fn error:&err];
    if (!pso) {
        fprintf(stderr, "Error creating pipeline: %s\n",
                err.localizedDescription.UTF8String);
        return EXIT_FAILURE;
    }

    NSUInteger tg_size = pso.maxTotalThreadsPerThreadgroup;
    if (tg_size > 256) tg_size = 256;

    // -------------------------------------------------------------------
    // Allocate buffers
    // -------------------------------------------------------------------
    NSUInteger num_groups1 = ((NSUInteger)N + tg_size * 2 - 1) / (tg_size * 2);
    NSUInteger num_groups2 = (num_groups1 + tg_size * 2 - 1) / (tg_size * 2);
    if (num_groups2 < 1) num_groups2 = 1;

    id<MTLBuffer> buf_input = [device newBufferWithLength:bytes
                                                  options:MTLResourceStorageModeShared];
    id<MTLBuffer> buf_s1    = [device newBufferWithLength:num_groups1 * sizeof(float)
                                                  options:MTLResourceStorageModeShared];
    id<MTLBuffer> buf_s2    = [device newBufferWithLength:(num_groups2 < 1 ? 1 : num_groups2) * sizeof(float)
                                                  options:MTLResourceStorageModeShared];

    float* h_input = (float*)buf_input.contents;
    for (int i = 0; i < N; ++i) {
        h_input[i] = (float)(i % 1000) * 0.001f;
    }

    // -------------------------------------------------------------------
    // Warmup
    // -------------------------------------------------------------------
    id<MTLBuffer> buf_result = nil;
    for (int w = 0; w < num_warmup; ++w) {
        buf_result = dispatch_reduction(queue, pso, tg_size,
                                        buf_input, (NSUInteger)N,
                                        buf_s1, buf_s2);
    }

    // -------------------------------------------------------------------
    // Timed iterations
    // -------------------------------------------------------------------
    std::vector<double> times(1);
    for (int i = 0; i < 1; ++i) {
        uint64_t t0 = mach_absolute_time();
        buf_result = dispatch_reduction(queue, pso, tg_size,
                                        buf_input, (NSUInteger)N,
                                        buf_s1, buf_s2);
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

    // JSON-lines kernel event
    printf("{\"type\":\"kernel\",\"name\":\"reduce_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"size\":%d,\"block_size\":%d"
           "\"precision\":\"%s\"}}\n",
           avg_ms, size, block_size, precision);

    // JSON-lines summary event
    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"bandwidth_gbps\",\"value\":%.2f}]}\n",
           total_ms, bandwidth_gb);

    fprintf(stderr, "Effective bandwidth: %.2f GB/s  (avg %.4f ms)\n",
            bandwidth_gb, avg_ms);

    // -------------------------------------------------------------------
    // Verification
    // -------------------------------------------------------------------
    float gpu_result = *((const float*)buf_result.contents);

    double cpu_result = 0.0;
    for (int i = 0; i < N; ++i) {
        cpu_result += (double)h_input[i];
    }

    double rel_err = fabs((double)gpu_result - cpu_result)
                     / (fabs(cpu_result) + 1e-10);
    fprintf(stderr, "GPU sum: %f, CPU sum: %f, relative error: %e\n",
            gpu_result, cpu_result, rel_err);

    if (rel_err < 1e-3) {
        fprintf(stderr, "PASS\n");
    } else {
        fprintf(stderr, "FAIL: relative error too large\n");
    }

    return (rel_err >= 1e-3) ? EXIT_FAILURE : EXIT_SUCCESS;

    } // @autoreleasepool
}
