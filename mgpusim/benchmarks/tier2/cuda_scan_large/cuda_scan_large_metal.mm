/**
 * cuda_scan_large_metal.mm — Apple Metal host for large-array prefix sum.
 *
 * Work-efficient Blelloch scan (exclusive prefix sum) for large arrays.
 * Three-phase hierarchical decomposition:
 *   1. Block-level scan of each block
 *   2. Scan of block sums
 *   3. Add block sums back to each block
 *
 * Measures throughput in GB/s = 2 * N * sizeof(uint) / time_s / 1e9.
 *
 * Usage:
 *   ./cuda_scan_large [--size N]
 *
 *   --size N         Number of elements (default: 16777216 = 16M)
 *   --iterations I   Timed iterations (default: 5)
 *
 * Output (stdout): CSV — cuda_scan_large,<N>,<time_ms>,<GBs>
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

// ---------------------------------------------------------------------------
// Constants (must match .metal shader)
// ---------------------------------------------------------------------------

#define BLOCK_SIZE 256
#define ELEMENTS_PER_BLOCK (2 * BLOCK_SIZE)

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
// CPU reference: exclusive prefix sum
// ---------------------------------------------------------------------------

static void prefix_sum_cpu(const unsigned int* input, unsigned int* output, int N) {
    unsigned int sum = 0;
    for (int i = 0; i < N; ++i) {
        output[i] = sum;
        sum += input[i];
    }
}

// ---------------------------------------------------------------------------
// Recursive scan on Metal
// ---------------------------------------------------------------------------

static id<MTLDevice> g_device;
static id<MTLCommandQueue> g_queue;
static id<MTLComputePipelineState> g_psoScan;
static id<MTLComputePipelineState> g_psoAdd;

static void scan_recursive_metal(id<MTLBuffer> bufData, int N) {
    int numBlocks = (N + ELEMENTS_PER_BLOCK - 1) / ELEMENTS_PER_BLOCK;

    // Allocate buffer for block sums
    id<MTLBuffer> bufBlockSums = [g_device newBufferWithLength:numBlocks * sizeof(uint32_t)
                                                       options:MTLResourceStorageModeShared];

    // Phase 1: Block-level scan
    {
        id<MTLCommandBuffer> cb = [g_queue commandBuffer];
        id<MTLComputeCommandEncoder> enc = [cb computeCommandEncoder];

        uint32_t paramN = (uint32_t)N;

        [enc setComputePipelineState:g_psoScan];
        [enc setBuffer:bufData      offset:0 atIndex:0];
        [enc setBuffer:bufBlockSums offset:0 atIndex:1];
        [enc setBytes:&paramN length:sizeof(paramN) atIndex:2];
        [enc dispatchThreadgroups:MTLSizeMake(numBlocks, 1, 1)
            threadsPerThreadgroup:MTLSizeMake(BLOCK_SIZE, 1, 1)];

        [enc endEncoding];
        [cb commit];
        [cb waitUntilCompleted];
    }

    if (numBlocks > 1) {
        // Phase 2: Recursively scan block sums
        scan_recursive_metal(bufBlockSums, numBlocks);

        // Phase 3: Add scanned block sums back
        {
            id<MTLCommandBuffer> cb = [g_queue commandBuffer];
            id<MTLComputeCommandEncoder> enc = [cb computeCommandEncoder];

            uint32_t paramN = (uint32_t)N;

            [enc setComputePipelineState:g_psoAdd];
            [enc setBuffer:bufData      offset:0 atIndex:0];
            [enc setBuffer:bufBlockSums offset:0 atIndex:1];
            [enc setBytes:&paramN length:sizeof(paramN) atIndex:2];
            [enc dispatchThreadgroups:MTLSizeMake(numBlocks, 1, 1)
                threadsPerThreadgroup:MTLSizeMake(BLOCK_SIZE, 1, 1)];

            [enc endEncoding];
            [cb commit];
            [cb waitUntilCompleted];
        }
    }
}

// ---------------------------------------------------------------------------
// main
// ---------------------------------------------------------------------------

static const char* parseStrParam(int argc, char** argv, const char* name,
                                 const char* defaultVal) {
    for (int i = 1; i < argc - 1; ++i) {
        if (strcmp(argv[i], name) == 0) {
            return argv[i + 1];
        }
    }
    return defaultVal;
}

int main(int argc, char* argv[]) {
    @autoreleasepool {
    setvbuf(stdout, NULL, _IONBF, 0);

    // Defaults from params.json
    int size       = 16777216;
    int block_size = 256;
    const char* verify = "true";

    // Command-line fallback
    size       = parseIntParam(argc, argv, "--size", size);
    block_size = parseIntParam(argc, argv, "--block_size", block_size);
    verify     = parseStrParam(argc, argv, "--verify", verify);

    // Environment variable overrides
    const char* env_val;
    env_val = getenv("BENCH_PARAM_size");
    if (env_val) size = atoi(env_val);
    env_val = getenv("BENCH_PARAM_block_size");
    if (env_val) block_size = atoi(env_val);
    env_val = getenv("BENCH_PARAM_verify");
    if (env_val) verify = env_val;
    int num_warmup = 0;

    int N     = size;

    size_t bytes = (size_t)N * sizeof(unsigned int);

    // -------------------------------------------------------------------
    // Metal device setup
    // -------------------------------------------------------------------
    g_device = MTLCreateSystemDefaultDevice();
    if (!g_device) {
        fprintf(stderr, "Error: no Metal device found\n");
        return EXIT_FAILURE;
    }
    fprintf(stderr, "Device: %s\n", g_device.name.UTF8String);
    fprintf(stderr, "Scan size: %d warmup + %d timed\n\n", N, num_warmup);

    g_queue = [g_device newCommandQueue];

    // -------------------------------------------------------------------
    // Compile Metal shader from source file next to the binary
    // -------------------------------------------------------------------
    NSError* err = nil;
    id<MTLLibrary> library = nil;

    NSString* exePath = [[NSBundle mainBundle] executablePath];
    NSString* dir     = [exePath stringByDeletingLastPathComponent];
    NSString* srcPath = [dir stringByAppendingPathComponent:@"cuda_scan_large.metal"];

    if ([[NSFileManager defaultManager] fileExistsAtPath:srcPath]) {
        NSString* metalSrc = [NSString stringWithContentsOfFile:srcPath
                                                       encoding:NSUTF8StringEncoding
                                                          error:nil];
        MTLCompileOptions* opts = [MTLCompileOptions new];
        library = [g_device newLibraryWithSource:metalSrc options:opts error:&err];
        if (!library) {
            fprintf(stderr, "Metal compile error: %s\n",
                    err.localizedDescription.UTF8String);
            return EXIT_FAILURE;
        }
    } else {
        fprintf(stderr, "Error: cuda_scan_large.metal not found at %s\n",
                srcPath.UTF8String);
        return EXIT_FAILURE;
    }

    // Create pipeline states
    id<MTLFunction> fnScan = [library newFunctionWithName:@"scan_block_kernel"];
    id<MTLFunction> fnAdd  = [library newFunctionWithName:@"add_block_sums_kernel"];
    if (!fnScan || !fnAdd) {
        fprintf(stderr, "Error: kernel function(s) not found\n");
        return EXIT_FAILURE;
    }

    g_psoScan = [g_device newComputePipelineStateWithFunction:fnScan error:&err];
    if (!g_psoScan) {
        fprintf(stderr, "Error creating scan pipeline: %s\n",
                err.localizedDescription.UTF8String);
        return EXIT_FAILURE;
    }

    g_psoAdd = [g_device newComputePipelineStateWithFunction:fnAdd error:&err];
    if (!g_psoAdd) {
        fprintf(stderr, "Error creating add pipeline: %s\n",
                err.localizedDescription.UTF8String);
        return EXIT_FAILURE;
    }

    // -------------------------------------------------------------------
    // Allocate buffers
    // -------------------------------------------------------------------
    id<MTLBuffer> bufData = [g_device newBufferWithLength:bytes
                                                  options:MTLResourceStorageModeShared];
    id<MTLBuffer> bufOrig = [g_device newBufferWithLength:bytes
                                                  options:MTLResourceStorageModeShared];

    // Initialize original data
    unsigned int* origPtr = (unsigned int*)bufOrig.contents;
    for (int i = 0; i < N; ++i) {
        origPtr[i] = (unsigned int)(i % 10);  // small values to avoid overflow
    }

    // Lambda: run full scan
    auto run_scan = [&]() {
        memcpy(bufData.contents, bufOrig.contents, bytes);
        scan_recursive_metal(bufData, N);
    };

    // -------------------------------------------------------------------
    // Warmup
    // -------------------------------------------------------------------
    for (int w = 0; w < num_warmup; ++w) {
        run_scan();
    }

    // -------------------------------------------------------------------
    // Timed iterations
    // -------------------------------------------------------------------
    std::vector<double> times(1);
    for (int i = 0; i < 1; ++i) {
        uint64_t t0 = mach_absolute_time();
        run_scan();
        uint64_t t1 = mach_absolute_time();
        times[i] = ticks_to_ms(t1 - t0);
    }

    double sum = 0.0;
    for (int i = 0; i < 1; ++i) sum += times[i];
    double avg_ms = sum / 1;

    // GB/s = 2 * N * sizeof(uint) / time_s / 1e9
    double data_bytes = 2.0 * (double)N * sizeof(unsigned int);
    double gbs = data_bytes / (avg_ms * 1e-3) / 1e9;

    double total_ms = sum;

    // JSON-lines kernel events
    printf("{\"type\":\"kernel\",\"name\":\"scan_block_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"size\":%d\"block_size\":%d,"
           "\"verify\":\"%s\"}}\n",
           avg_ms, size, block_size, verify);

    printf("{\"type\":\"kernel\",\"name\":\"add_block_sums_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"size\":%d\"block_size\":%d,"
           "\"verify\":\"%s\"}}\n",
           avg_ms, size, block_size, verify);

    // JSON-lines summary event
    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"throughput_gbps\",\"value\":%.2f}]}\n",
           total_ms, gbs);

    // Human-readable output to stderr
    fprintf(stderr, "Average time: %.4f ms\n", avg_ms);
    fprintf(stderr, "Throughput:   %.4f GB/s\n", gbs);

    // -------------------------------------------------------------------
    // Verification
    // -------------------------------------------------------------------
    {
        const unsigned int* gpuOutput = (const unsigned int*)bufData.contents;

        std::vector<unsigned int> ref((size_t)N);
        prefix_sum_cpu(origPtr, ref.data(), N);

        int errors = 0;
        for (int i = 0; i < N; ++i) {
            if (gpuOutput[i] != ref[i]) {
                if (errors < 10) {
                    fprintf(stderr, "Mismatch at %d: GPU=%u CPU=%u\n",
                            i, gpuOutput[i], ref[i]);
                }
                errors++;
            }
        }

        if (errors > 0)
            fprintf(stderr, "FAIL: %d errors out of %d\n", errors, N);
        else
            fprintf(stderr, "PASS\n");
    }

    return EXIT_SUCCESS;

    } // @autoreleasepool
}
