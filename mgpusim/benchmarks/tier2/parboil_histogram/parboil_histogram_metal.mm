/**
 * parboil_histogram_metal.mm — Apple Metal host for the Parboil Histogram benchmark.
 *
 * Computes a 256-bin histogram of N random uint32 values using atomic operations.
 * Each element is mapped to a bin via: bin = element & 0xFF (lower 8 bits).
 * Runs 5 timed iterations (histogram reset before each), reports average time.
 * CPU verify: compare all 256 bins exactly.
 *
 * Usage:
 *   ./parboil_histogram [--n N]
 *
 *   --n N            Number of input elements (default: 16777216 = 16*1024*1024)
 *   --iterations I   Timed iterations         (default: 5)
 *
 * Output (stdout): CSV — histogram,<N>,<time_ms>,<GBs>
 * Output (stderr): device info, timing details, PASS/FAIL
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
#include <cstdint>
#include <vector>
#include <mach/mach_time.h>

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

#define NUM_BINS 256

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
// CPU reference histogram for correctness verification
// ---------------------------------------------------------------------------

static void cpu_histogram(const uint32_t* data, uint32_t* hist, uint32_t n)
{
    memset(hist, 0, NUM_BINS * sizeof(uint32_t));
    for (uint32_t i = 0; i < n; ++i) {
        hist[data[i] & 0xFF]++;
    }
}

// ---------------------------------------------------------------------------
// Embedded Metal shader source
// ---------------------------------------------------------------------------

static const char* kMetalSource = R"metal(
#include <metal_stdlib>
using namespace metal;

kernel void histogram_kernel(
    device const uint*        data [[ buffer(0) ]],
    device       atomic_uint* hist [[ buffer(1) ]],
    constant     uint&        n    [[ buffer(2) ]],
    uint gid [[ thread_position_in_grid ]])
{
    if (gid >= n) return;
    uint bin = data[gid] & 0xFFu;
    atomic_fetch_add_explicit(&hist[bin], 1u, memory_order_relaxed);
}
)metal";

// ---------------------------------------------------------------------------
// main
// ---------------------------------------------------------------------------

int main(int argc, char* argv[])
{
    @autoreleasepool {
    setvbuf(stdout, NULL, _IONBF, 0);

    // Default values from params.json
    uint32_t n     = 16 * 1024 * 1024;
    int      num_bins = 256;
    int      block_size = 256;

    // Command-line args as fallback
    n     = (uint32_t)parseIntParam(argc, argv, "--n", (int)n);
    num_bins = parseIntParam(argc, argv, "--num_bins", num_bins);
    block_size = parseIntParam(argc, argv, "--block_size", block_size);

    // Env vars override
    const char* env_val;
    env_val = getenv("BENCH_PARAM_n");
    if (env_val) n = (uint32_t)atoi(env_val);
    env_val = getenv("BENCH_PARAM_num_bins");
    if (env_val) num_bins = atoi(env_val);
    env_val = getenv("BENCH_PARAM_block_size");
    if (env_val) block_size = atoi(env_val);
    int num_warmup = 0;

    size_t bytes_data = (size_t)n * sizeof(uint32_t);
    size_t bytes_hist = (size_t)NUM_BINS * sizeof(uint32_t);

    // -------------------------------------------------------------------
    // Metal device setup
    // -------------------------------------------------------------------
    id<MTLDevice> device = MTLCreateSystemDefaultDevice();
    if (!device) {
        fprintf(stderr, "Error: no Metal device found\n");
        return EXIT_FAILURE;
    }
    fprintf(stderr, "Device: %s\n", device.name.UTF8String);
    fprintf(stderr, "N = %u elements  |  Bins = %d  |  Iterations = %d\n\n",
            n, NUM_BINS);

    id<MTLCommandQueue> queue = [device newCommandQueue];

    // -------------------------------------------------------------------
    // Compile Metal shader from embedded source string
    // -------------------------------------------------------------------
    NSError *err = nil;
    NSString *metalSrc = [NSString stringWithUTF8String:kMetalSource];
    MTLCompileOptions *opts = [MTLCompileOptions new];
    id<MTLLibrary> library = [device newLibraryWithSource:metalSrc options:opts error:&err];
    if (!library) {
        fprintf(stderr, "Metal compile error: %s\n",
                err.localizedDescription.UTF8String);
        return EXIT_FAILURE;
    }

    id<MTLFunction> fn = [library newFunctionWithName:@"histogram_kernel"];
    if (!fn) {
        fprintf(stderr, "Error: kernel 'histogram_kernel' not found\n");
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
    // Allocate Metal buffers (shared memory)
    // -------------------------------------------------------------------
    id<MTLBuffer> buf_data = [device newBufferWithLength:bytes_data
                                                 options:MTLResourceStorageModeShared];
    id<MTLBuffer> buf_hist = [device newBufferWithLength:bytes_hist
                                                 options:MTLResourceStorageModeShared];
    id<MTLBuffer> buf_n    = [device newBufferWithBytes:&n
                                                 length:sizeof(uint32_t)
                                                options:MTLResourceStorageModeShared];

    // Initialize input data with pseudo-random uint32 values
    uint32_t* h_data = (uint32_t*)buf_data.contents;
    srand(42);
    for (uint32_t i = 0; i < n; ++i) {
        h_data[i] = ((uint32_t)rand() ^ ((uint32_t)rand() << 15) ^ ((uint32_t)rand() << 30));
    }

    // Thread configuration
    NSUInteger tg_size   = MIN((NSUInteger)256, pso.maxTotalThreadsPerThreadgroup);
    NSUInteger grid_size = (NSUInteger)n;

    // Helper lambda: reset histogram and dispatch one kernel invocation
    auto dispatch_histogram = [&]() {
        // Reset histogram buffer to zero before each run
        memset(buf_hist.contents, 0, bytes_hist);

        id<MTLCommandBuffer>         cb  = [queue commandBuffer];
        id<MTLComputeCommandEncoder> enc = [cb computeCommandEncoder];
        [enc setComputePipelineState:pso];
        [enc setBuffer:buf_data offset:0 atIndex:0];
        [enc setBuffer:buf_hist offset:0 atIndex:1];
        [enc setBuffer:buf_n    offset:0 atIndex:2];
        [enc dispatchThreads:MTLSizeMake(grid_size, 1, 1)
            threadsPerThreadgroup:MTLSizeMake(tg_size, 1, 1)];
        [enc endEncoding];
        [cb commit];
        [cb waitUntilCompleted];
    };

    // -------------------------------------------------------------------
    // Warmup (not measured)
    // -------------------------------------------------------------------
    for (int w = 0; w < num_warmup; ++w) {
        dispatch_histogram();
    }

    // -------------------------------------------------------------------
    // Timed iterations
    // -------------------------------------------------------------------
    std::vector<double> times(1);
    for (int i = 0; i < 1; ++i) {
        uint64_t t0 = mach_absolute_time();
        dispatch_histogram();
        uint64_t t1 = mach_absolute_time();
        times[i] = ticks_to_ms(t1 - t0);
    }

    // -------------------------------------------------------------------
    // Correctness check: compare GPU histogram to CPU reference
    // -------------------------------------------------------------------
    uint32_t* h_hist = (uint32_t*)buf_hist.contents;
    uint32_t  h_ref[NUM_BINS];
    cpu_histogram(h_data, h_ref, n);

    bool pass = true;
    for (int b = 0; b < NUM_BINS; ++b) {
        if (h_hist[b] != h_ref[b]) {
            fprintf(stderr, "MISMATCH bin[%d]: gpu=%u cpu=%u\n", b, h_hist[b], h_ref[b]);
            pass = false;
        }
    }
    fprintf(stderr, "Correctness check (all %d bins): %s\n\n",
            NUM_BINS, pass ? "PASS" : "FAIL");

    // -------------------------------------------------------------------
    // Statistics
    // -------------------------------------------------------------------
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

    // GB/s: N * sizeof(uint32) bytes of input data read
    double gb       = (double)n * sizeof(uint32_t) / 1.0e9;
    double gbps     = gb / (avg_ms * 1e-3);
    double melems_s = (double)n / (avg_ms * 1e-3) / 1.0e6;

    fprintf(stderr, "Bandwidth:     %.2f GB/s\n", gbps);
    fprintf(stderr, "Throughput:    %.2f Melements/s\n", melems_s);
    fprintf(stderr, "Timing:        avg %.4f ms  min %.4f ms  max %.4f ms  stddev %.4f ms\n",
            avg_ms, mn, mx, stddev);

    // JSON-lines output
    printf("{\"type\":\"kernel\",\"name\":\"histogram_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"n\":%u\"num_bins\":%d,"
           "\"block_size\":%d}}\n",
           avg_ms, n, num_bins, block_size);

    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"bandwidth_gbps\",\"value\":%.2f}]}\n",
           sum_ms, gbps);

    return EXIT_SUCCESS;

    } // @autoreleasepool
}
