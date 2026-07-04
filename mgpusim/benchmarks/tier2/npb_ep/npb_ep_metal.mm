/**
 * npb_ep_metal.mm — Apple Metal host for the NAS EP benchmark.
 *
 * Generates N pairs of Gaussian random deviates via Box-Muller transform
 * with per-thread integer LCG, counts into 10 annular bins.
 * Measures throughput in Gpairs/sec = N / time_s / 1e9.
 *
 * Usage:
 *   ./npb_ep [--size N]
 *
 *   --size N         Number of Gaussian pairs (default: 16777216 = 2^24)
 *   --iterations I   Timed iterations (default: 5)
 *
 * Output (stdout): CSV — npb_ep,<N>,<time_ms>,<Gpairs_per_sec>
 * Output (stderr): human-readable results
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
#include <vector>
#include <mach/mach_time.h>

#ifndef M_PI
#define M_PI 3.14159265358979323846
#endif

#define NUM_BINS 10

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
// Integer LCG RNG — matches Metal/HIP kernel
// ---------------------------------------------------------------------------

static inline unsigned int lcg_next(unsigned int seed) {
    return seed * 1103515245u + 12345u;
}

// ---------------------------------------------------------------------------
// CPU reference EP for verification
// ---------------------------------------------------------------------------

static void ep_cpu(int N, unsigned long long* bins) {
    for (int i = 0; i < NUM_BINS; ++i) bins[i] = 0;

    for (int idx = 0; idx < N; ++idx) {
        unsigned int seed = (unsigned int)(idx + 1);
        seed = lcg_next(seed);
        seed = lcg_next(seed);

        seed = lcg_next(seed);
        float u1 = (float)seed / 4294967296.0f;
        seed = lcg_next(seed);
        float u2 = (float)seed / 4294967296.0f;

        if (u1 < 1e-10f) u1 = 1e-10f;

        float r = sqrtf(-2.0f * logf(u1));
        float theta = 2.0f * (float)M_PI * u2;
        float x1 = r * cosf(theta);
        float x2 = r * sinf(theta);

        float t = x1 * x1 + x2 * x2;

        int bin = (int)sqrtf(t);
        if (bin >= NUM_BINS) bin = NUM_BINS - 1;
        if (bin < 0) bin = 0;

        bins[bin] += 1;
    }
}

// ---------------------------------------------------------------------------
// main
// ---------------------------------------------------------------------------

int main(int argc, char* argv[]) {
    @autoreleasepool {
    setvbuf(stdout, NULL, _IONBF, 0);

    // Defaults from params.json
    int N          = 16777216;  // 2^24
    int block_size = 256;
    int seed_param = 271828183;

    // Command-line fallback
    N          = parseIntParam(argc, argv, "--size", N);

    // Environment variable overrides
    const char* env_val;
    env_val = getenv("BENCH_PARAM_size");
    if (env_val) N = atoi(env_val);
    env_val = getenv("BENCH_PARAM_block_size");
    if (env_val) block_size = atoi(env_val);
    env_val = getenv("BENCH_PARAM_seed");
    if (env_val) seed_param = atoi(env_val);
    int num_warmup = 0;


    // -------------------------------------------------------------------
    // Metal device setup
    // -------------------------------------------------------------------
    id<MTLDevice> device = MTLCreateSystemDefaultDevice();
    if (!device) {
        fprintf(stderr, "Error: no Metal device found\n");
        return EXIT_FAILURE;
    }
    fprintf(stderr, "Device: %s\n", device.name.UTF8String);
    fprintf(stderr, "EP pairs: %d (2^%.0f) warmup + %d timed\n\n",
            N, log2((double)N), num_warmup);

    id<MTLCommandQueue> queue = [device newCommandQueue];

    // -------------------------------------------------------------------
    // Compile Metal shader from source file next to the binary
    // -------------------------------------------------------------------
    NSError* err = nil;
    id<MTLLibrary> library = nil;

    NSString* exePath = [[NSBundle mainBundle] executablePath];
    NSString* dir     = [exePath stringByDeletingLastPathComponent];
    NSString* srcPath = [dir stringByAppendingPathComponent:@"npb_ep.metal"];

    if ([[NSFileManager defaultManager] fileExistsAtPath:srcPath]) {
        NSString* metalSrc = [NSString stringWithContentsOfFile:srcPath
                                                       encoding:NSUTF8StringEncoding
                                                          error:nil];
        MTLCompileOptions* opts = [MTLCompileOptions new];
        library = [device newLibraryWithSource:metalSrc options:opts error:&err];
        if (!library) {
            fprintf(stderr, "Metal compile error: %s\n",
                    err.localizedDescription.UTF8String);
            return EXIT_FAILURE;
        }
    } else {
        fprintf(stderr, "Error: npb_ep.metal not found at %s\n",
                srcPath.UTF8String);
        return EXIT_FAILURE;
    }

    // Create pipeline state
    id<MTLFunction> fnEP = [library newFunctionWithName:@"ep_kernel"];
    if (!fnEP) {
        fprintf(stderr, "Error: ep_kernel function not found\n");
        return EXIT_FAILURE;
    }

    id<MTLComputePipelineState> psoEP =
        [device newComputePipelineStateWithFunction:fnEP error:&err];
    if (!psoEP) {
        fprintf(stderr, "Error creating ep pipeline: %s\n",
                err.localizedDescription.UTF8String);
        return EXIT_FAILURE;
    }

    // -------------------------------------------------------------------
    // Allocate buffers
    // -------------------------------------------------------------------
    size_t binsBytes = NUM_BINS * sizeof(uint32_t);
    id<MTLBuffer> bufBins = [device newBufferWithLength:binsBytes
                                               options:MTLResourceStorageModeShared];

    NSUInteger tgSize = 256;
    NSUInteger gridN  = (NSUInteger)N;

    // Lambda: run EP kernel
    auto run_ep = [&]() {
        // Zero out bins
        memset(bufBins.contents, 0, binsBytes);

        id<MTLCommandBuffer> cb = [queue commandBuffer];
        id<MTLComputeCommandEncoder> enc = [cb computeCommandEncoder];

        uint32_t nParam = (uint32_t)N;
        [enc setComputePipelineState:psoEP];
        [enc setBuffer:bufBins offset:0 atIndex:0];
        [enc setBytes:&nParam length:sizeof(nParam) atIndex:1];
        [enc dispatchThreads:MTLSizeMake(gridN, 1, 1)
            threadsPerThreadgroup:MTLSizeMake(tgSize, 1, 1)];

        [enc endEncoding];
        [cb commit];
        [cb waitUntilCompleted];
    };

    // -------------------------------------------------------------------
    // Warmup
    // -------------------------------------------------------------------
    for (int w = 0; w < num_warmup; ++w) {
        run_ep();
    }

    // -------------------------------------------------------------------
    // Timed iterations
    // -------------------------------------------------------------------
    std::vector<double> times(1);
    for (int i = 0; i < 1; ++i) {
        uint64_t t0 = mach_absolute_time();
        run_ep();
        uint64_t t1 = mach_absolute_time();
        times[i] = ticks_to_ms(t1 - t0);
    }

    double sum = 0.0;
    for (int i = 0; i < 1; ++i) sum += times[i];
    double avg_ms = sum / 1;

    // G pairs/sec = N / time_s / 1e9
    double gpairs_sec = (double)N / (avg_ms * 1e-3) / 1e9;

    double total_ms = sum;

    // JSON-lines kernel event
    printf("{\"type\":\"kernel\",\"name\":\"ep_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"size\":%d\"block_size\":%d,"
           "\"seed\":%d}}\n",
           avg_ms, N, block_size, seed_param);

    // JSON-lines summary event
    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"gpairs_per_sec\",\"value\":%.2f}]}\n",
           total_ms, gpairs_sec);

    // Human-readable output to stderr
    fprintf(stderr, "Average time: %.4f ms\n", avg_ms);
    fprintf(stderr, "Performance:  %.4f Gpairs/sec\n", gpairs_sec);

    // -------------------------------------------------------------------
    // Verification: compare Metal bins against CPU reference
    // -------------------------------------------------------------------
    const uint32_t* gpuBins = (const uint32_t*)bufBins.contents;

    // Use smaller N for CPU verification if needed
    int verifyN = (N <= 1048576) ? N : 1048576;

    // Re-run GPU with verifyN if different
    uint32_t gpuVerifyBins[NUM_BINS];
    if (verifyN != N) {
        memset(bufBins.contents, 0, binsBytes);

        id<MTLCommandBuffer> cb = [queue commandBuffer];
        id<MTLComputeCommandEncoder> enc = [cb computeCommandEncoder];

        uint32_t nParam = (uint32_t)verifyN;
        [enc setComputePipelineState:psoEP];
        [enc setBuffer:bufBins offset:0 atIndex:0];
        [enc setBytes:&nParam length:sizeof(nParam) atIndex:1];
        [enc dispatchThreads:MTLSizeMake((NSUInteger)verifyN, 1, 1)
            threadsPerThreadgroup:MTLSizeMake(tgSize, 1, 1)];

        [enc endEncoding];
        [cb commit];
        [cb waitUntilCompleted];

        const uint32_t* ptr = (const uint32_t*)bufBins.contents;
        for (int i = 0; i < NUM_BINS; ++i) gpuVerifyBins[i] = ptr[i];
    } else {
        for (int i = 0; i < NUM_BINS; ++i) gpuVerifyBins[i] = gpuBins[i];
    }

    unsigned long long cpu_bins[NUM_BINS];
    ep_cpu(verifyN, cpu_bins);

    fprintf(stderr, "\nBin counts (GPU vs CPU, N=%d):\n", verifyN);
    int errors = 0;
    for (int i = 0; i < NUM_BINS; ++i) {
        fprintf(stderr, "  Bin %d: GPU=%u  CPU=%llu\n", i, gpuVerifyBins[i], cpu_bins[i]);
        if ((unsigned long long)gpuVerifyBins[i] != cpu_bins[i]) errors++;
    }

    if (errors > 0)
        fprintf(stderr, "FAIL: %d bin mismatches\n", errors);
    else
        fprintf(stderr, "PASS\n");

    return EXIT_SUCCESS;

    } // @autoreleasepool
}
