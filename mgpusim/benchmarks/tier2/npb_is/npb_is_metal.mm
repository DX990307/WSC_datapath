/**
 * npb_is_metal.mm — Apple Metal host for the NAS IS benchmark.
 *
 * Parallel bucket sort of N random integers in range [0, MAX_KEY).
 * Three phases: histogram, prefix sum, scatter.
 * Measures throughput in Mkeys/sec = N / time_s / 1e6.
 *
 * Usage:
 *   ./npb_is [--size N]
 *
 *   --size N         Number of keys (default: 8388608 = 2^23)
 *   --iterations I   Timed iterations (default: 5)
 *
 * Output (stdout): CSV — npb_is,<N>,<time_ms>,<Mkeys_per_sec>
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

#define NUM_BUCKETS 1024
#define MAX_KEY     524288

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
// Generate keys with simple LCG (must match HIP version)
// ---------------------------------------------------------------------------

static void generate_keys(int* keys, int N) {
    unsigned int seed = 314159265u;
    for (int i = 0; i < N; ++i) {
        seed = seed * 1103515245u + 12345u;
        keys[i] = (int)((seed >> 4) % MAX_KEY);
    }
}

// ---------------------------------------------------------------------------
// CPU verification: check sorted output is bucket-ordered
// ---------------------------------------------------------------------------

static bool verify_sorted(const int* sorted, int N) {
    for (int i = 1; i < N; ++i) {
        int bucket_prev = sorted[i - 1] / (MAX_KEY / NUM_BUCKETS);
        int bucket_curr = sorted[i] / (MAX_KEY / NUM_BUCKETS);
        if (bucket_curr < bucket_prev) {
            fprintf(stderr, "Verification failed at index %d: %d (bucket %d) > %d (bucket %d)\n",
                    i, sorted[i - 1], bucket_prev, sorted[i], bucket_curr);
            return false;
        }
    }
    return true;
}

// ---------------------------------------------------------------------------
// main
// ---------------------------------------------------------------------------

int main(int argc, char* argv[]) {
    @autoreleasepool {
    setvbuf(stdout, NULL, _IONBF, 0);

    // Defaults from params.json
    int N          = 8388608;  // 2^23
    int block_size = 256;
    const char* verify = "true";

    // Command-line fallback
    N          = parseIntParam(argc, argv, "--size", N);

    // Environment variable overrides
    const char* env_val;
    env_val = getenv("BENCH_PARAM_size");
    if (env_val) N = atoi(env_val);
    env_val = getenv("BENCH_PARAM_block_size");
    if (env_val) block_size = atoi(env_val);
    env_val = getenv("BENCH_PARAM_verify");
    if (env_val) verify = env_val;
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
    fprintf(stderr, "IS keys: %d (2^%.0f)  |  Buckets: %d warmup + %d timed\n\n",
            N, log2((double)N), NUM_BUCKETS, num_warmup);

    id<MTLCommandQueue> queue = [device newCommandQueue];

    // -------------------------------------------------------------------
    // Compile Metal shader from source file next to the binary
    // -------------------------------------------------------------------
    NSError* err = nil;
    id<MTLLibrary> library = nil;

    NSString* exePath = [[NSBundle mainBundle] executablePath];
    NSString* dir     = [exePath stringByDeletingLastPathComponent];
    NSString* srcPath = [dir stringByAppendingPathComponent:@"npb_is.metal"];

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
        fprintf(stderr, "Error: npb_is.metal not found at %s\n",
                srcPath.UTF8String);
        return EXIT_FAILURE;
    }

    // Create pipeline states for all three kernels
    id<MTLFunction> fnHist    = [library newFunctionWithName:@"histogram_kernel"];
    id<MTLFunction> fnPrefix  = [library newFunctionWithName:@"prefix_sum_kernel"];
    id<MTLFunction> fnScatter = [library newFunctionWithName:@"scatter_kernel"];
    if (!fnHist || !fnPrefix || !fnScatter) {
        fprintf(stderr, "Error: kernel function(s) not found\n");
        return EXIT_FAILURE;
    }

    id<MTLComputePipelineState> psoHist =
        [device newComputePipelineStateWithFunction:fnHist error:&err];
    id<MTLComputePipelineState> psoPrefix =
        [device newComputePipelineStateWithFunction:fnPrefix error:&err];
    id<MTLComputePipelineState> psoScatter =
        [device newComputePipelineStateWithFunction:fnScatter error:&err];

    if (!psoHist || !psoPrefix || !psoScatter) {
        fprintf(stderr, "Error creating pipeline state(s)\n");
        return EXIT_FAILURE;
    }

    // -------------------------------------------------------------------
    // Allocate buffers
    // -------------------------------------------------------------------
    size_t keysBytes = (size_t)N * sizeof(int32_t);
    size_t histBytes = NUM_BUCKETS * sizeof(uint32_t);

    id<MTLBuffer> bufKeys    = [device newBufferWithLength:keysBytes
                                                   options:MTLResourceStorageModeShared];
    id<MTLBuffer> bufSorted  = [device newBufferWithLength:keysBytes
                                                   options:MTLResourceStorageModeShared];
    id<MTLBuffer> bufHist    = [device newBufferWithLength:histBytes
                                                   options:MTLResourceStorageModeShared];
    id<MTLBuffer> bufOffsets = [device newBufferWithLength:histBytes
                                                   options:MTLResourceStorageModeShared];

    // Generate keys
    generate_keys((int*)bufKeys.contents, N);

    NSUInteger tgSize = 256;
    NSUInteger gridN  = (NSUInteger)N;

    // Lambda: run full IS (histogram + prefix sum + scatter)
    auto run_is = [&]() {
        // Clear histogram
        memset(bufHist.contents, 0, histBytes);

        id<MTLCommandBuffer> cb = [queue commandBuffer];
        id<MTLComputeCommandEncoder> enc = [cb computeCommandEncoder];

        uint32_t nParam = (uint32_t)N;

        // Phase 1: histogram
        [enc setComputePipelineState:psoHist];
        [enc setBuffer:bufKeys offset:0 atIndex:0];
        [enc setBuffer:bufHist offset:0 atIndex:1];
        [enc setBytes:&nParam length:sizeof(nParam) atIndex:2];
        [enc dispatchThreads:MTLSizeMake(gridN, 1, 1)
            threadsPerThreadgroup:MTLSizeMake(tgSize, 1, 1)];

        [enc endEncoding];
        [cb commit];
        [cb waitUntilCompleted];

        // Phase 2: prefix sum (single threadgroup)
        cb = [queue commandBuffer];
        enc = [cb computeCommandEncoder];

        uint32_t numBucketsParam = (uint32_t)NUM_BUCKETS;
        [enc setComputePipelineState:psoPrefix];
        [enc setBuffer:bufHist offset:0 atIndex:0];
        [enc setBytes:&numBucketsParam length:sizeof(numBucketsParam) atIndex:1];
        [enc dispatchThreads:MTLSizeMake(NUM_BUCKETS, 1, 1)
            threadsPerThreadgroup:MTLSizeMake(NUM_BUCKETS, 1, 1)];

        [enc endEncoding];
        [cb commit];
        [cb waitUntilCompleted];

        // Copy prefix sums to offsets buffer (scatter will modify offsets)
        memcpy(bufOffsets.contents, bufHist.contents, histBytes);

        // Phase 3: scatter
        cb = [queue commandBuffer];
        enc = [cb computeCommandEncoder];

        [enc setComputePipelineState:psoScatter];
        [enc setBuffer:bufKeys    offset:0 atIndex:0];
        [enc setBuffer:bufOffsets offset:0 atIndex:1];
        [enc setBuffer:bufSorted  offset:0 atIndex:2];
        [enc setBytes:&nParam length:sizeof(nParam) atIndex:3];
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
        run_is();
    }

    // -------------------------------------------------------------------
    // Timed iterations
    // -------------------------------------------------------------------
    std::vector<double> times(1);
    for (int i = 0; i < 1; ++i) {
        uint64_t t0 = mach_absolute_time();
        run_is();
        uint64_t t1 = mach_absolute_time();
        times[i] = ticks_to_ms(t1 - t0);
    }

    double sum = 0.0;
    for (int i = 0; i < 1; ++i) sum += times[i];
    double avg_ms = sum / 1;

    // M keys/sec = N / time_s / 1e6
    double mkeys_sec = (double)N / (avg_ms * 1e-3) / 1e6;

    double total_ms = sum;

    // JSON-lines kernel events (one per kernel)
    printf("{\"type\":\"kernel\",\"name\":\"histogram_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"size\":%d\"block_size\":%d,"
           "\"verify\":\"%s\"}}\n",
           avg_ms, N, block_size, verify);

    printf("{\"type\":\"kernel\",\"name\":\"prefix_sum_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"size\":%d\"block_size\":%d,"
           "\"verify\":\"%s\"}}\n",
           avg_ms, N, block_size, verify);

    printf("{\"type\":\"kernel\",\"name\":\"scatter_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"size\":%d\"block_size\":%d,"
           "\"verify\":\"%s\"}}\n",
           avg_ms, N, block_size, verify);

    // JSON-lines summary event
    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"mkeys_per_sec\",\"value\":%.2f}]}\n",
           total_ms, mkeys_sec);

    // Human-readable output to stderr
    fprintf(stderr, "Average time: %.4f ms\n", avg_ms);
    fprintf(stderr, "Performance:  %.4f Mkeys/sec\n", mkeys_sec);

    // -------------------------------------------------------------------
    // Verification: check sorted output is bucket-ordered
    // -------------------------------------------------------------------
    const int* sorted = (const int*)bufSorted.contents;

    if (verify_sorted(sorted, N))
        fprintf(stderr, "PASS\n");
    else
        fprintf(stderr, "FAIL\n");

    return EXIT_SUCCESS;

    } // @autoreleasepool
}
