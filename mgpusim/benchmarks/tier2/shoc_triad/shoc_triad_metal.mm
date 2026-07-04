/**
 * shoc_triad_metal.mm — Apple Metal host for the SHOC Triad benchmark.
 *
 * Stream Triad: a[i] = b[i] + scalar * c[i]
 * Classic memory-bandwidth benchmark derived from the SHOC suite.
 *
 * The Metal compute kernel lives in shoc_triad.metal and is compiled at
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

/** Convert mach_absolute_time ticks to seconds. */
static double ticks_to_seconds(uint64_t ticks)
{
    static mach_timebase_info_data_t tb = {0, 0};
    if (tb.denom == 0) {
        mach_timebase_info(&tb);
    }
    return (double)ticks * (double)tb.numer / (double)tb.denom * 1e-9;
}

/** Convert ticks to milliseconds. */
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
// main
// ---------------------------------------------------------------------------

int main(int argc, char* argv[])
{
    @autoreleasepool {

    setvbuf(stdout, NULL, _IONBF, 0);

    // Defaults
    int array_size  = 4194304;
    int block_size  = 256;
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

    size_t bytes  = (size_t)N * sizeof(float);
    float  scalar = 1.75f;

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
    NSError *err = nil;
    id<MTLLibrary> library = nil;

    NSString *exePath = [[NSBundle mainBundle] executablePath];
    NSString *dir     = [exePath stringByDeletingLastPathComponent];
    NSString *srcPath = [dir stringByAppendingPathComponent:@"shoc_triad.metal"];

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
        fprintf(stderr, "Error: shoc_triad.metal not found at %s\n",
                srcPath.UTF8String);
        return EXIT_FAILURE;
    }

    // Build compute pipeline
    id<MTLFunction> fn = [library newFunctionWithName:@"triad_kernel"];
    if (!fn) {
        fprintf(stderr, "Error: kernel 'triad_kernel' not found in library\n");
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
    id<MTLBuffer> buf_a = [device newBufferWithLength:bytes
                                              options:MTLResourceStorageModePrivate];
    id<MTLBuffer> buf_b = [device newBufferWithLength:bytes
                                              options:MTLResourceStorageModeShared];
    id<MTLBuffer> buf_c = [device newBufferWithLength:bytes
                                              options:MTLResourceStorageModeShared];

    // Initialize b and c on the CPU
    float* h_b = (float*)buf_b.contents;
    float* h_c = (float*)buf_c.contents;
    for (int i = 0; i < N; ++i) {
        h_b[i] = (float)(i % 1000) * 0.001f;
        h_c[i] = (float)((i + 37) % 1000) * 0.001f;
    }

    // Kernel dispatch configuration
    NSUInteger tg_size = pso.maxTotalThreadsPerThreadgroup;
    if (tg_size > 256) tg_size = 256;

    // Packed params: x = N, y = scalar bits
    uint32_t scalar_bits;
    memcpy(&scalar_bits, &scalar, sizeof(scalar_bits));
    uint32_t params[2] = { (uint32_t)N, scalar_bits };

    // Helper lambda: encode and commit one triad kernel dispatch
    auto dispatch_triad = [&]() {
        id<MTLCommandBuffer>       cb  = [queue commandBuffer];
        id<MTLComputeCommandEncoder> enc = [cb computeCommandEncoder];
        [enc setComputePipelineState:pso];
        [enc setBuffer:buf_a offset:0 atIndex:0];
        [enc setBuffer:buf_b offset:0 atIndex:1];
        [enc setBuffer:buf_c offset:0 atIndex:2];
        [enc setBytes:params length:sizeof(params) atIndex:3];
        [enc dispatchThreads:MTLSizeMake((NSUInteger)N, 1, 1)
            threadsPerThreadgroup:MTLSizeMake(tg_size, 1, 1)];
        [enc endEncoding];
        [cb commit];
        [cb waitUntilCompleted];
    };

    // -------------------------------------------------------------------
    // Warmup
    // -------------------------------------------------------------------
    for (int w = 0; w < num_warmup; ++w) {
        dispatch_triad();
    }

    // -------------------------------------------------------------------
    // Timed iterations
    // -------------------------------------------------------------------
    std::vector<double> times(1);
    for (int i = 0; i < 1; ++i) {
        uint64_t t0 = mach_absolute_time();
        dispatch_triad();
        uint64_t t1 = mach_absolute_time();
        times[i] = ticks_to_ms(t1 - t0);
    }

    // Compute statistics
    double sum = 0.0, mn = DBL_MAX, mx = 0.0;
    for (int i = 0; i < 1; ++i) {
        sum += times[i];
        if (times[i] < mn) mn = times[i];
        if (times[i] > mx) mx = times[i];
    }
    double avg_ms = sum / 1;
    double total_ms = sum;

    // Effective bandwidth: 3 arrays × N × 4 bytes / time
    double bandwidth_gb = 3.0 * (double)N * sizeof(float)
                          / (avg_ms * 1e-3) / 1e9;

    // JSON-lines kernel event
    printf("{\"type\":\"kernel\",\"name\":\"triad_kernel\",\"time_ms\":%.6f,"
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
    // Verification: read back a[] and compare to CPU reference
    // -------------------------------------------------------------------
    id<MTLBuffer> buf_a_out = [device newBufferWithLength:bytes
                                                  options:MTLResourceStorageModeShared];
    {
        id<MTLCommandBuffer>     cb   = [queue commandBuffer];
        id<MTLBlitCommandEncoder> blit = [cb blitCommandEncoder];
        [blit copyFromBuffer:buf_a sourceOffset:0
                    toBuffer:buf_a_out destinationOffset:0
                        size:bytes];
        [blit endEncoding];
        [cb commit];
        [cb waitUntilCompleted];
    }

    const float* h_a   = (const float*)buf_a_out.contents;
    int errors = 0;
    for (int i = 0; i < N; ++i) {
        float expected = h_b[i] + scalar * h_c[i];
        float diff     = fabsf(h_a[i] - expected);
        float thresh   = 1e-5f * fabsf(expected) + 1e-6f;
        if (diff > thresh) {
            if (errors < 10) {
                fprintf(stderr, "Mismatch at %d: got %.8f, expected %.8f\n",
                        i, h_a[i], expected);
            }
            errors++;
        }
    }
    if (errors > 0) {
        fprintf(stderr, "FAIL: %d mismatches\n", errors);
    } else {
        fprintf(stderr, "PASS\n");
    }

    return (errors > 0) ? EXIT_FAILURE : EXIT_SUCCESS;

    } // @autoreleasepool
}
