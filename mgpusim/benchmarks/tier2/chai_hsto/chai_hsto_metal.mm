/**
 * chai_hsto_metal.mm — Apple Metal host for histogram benchmark.
 *
 * Computes a 256-bin histogram of N random bytes using threadgroup memory
 * privatization and atomic reduction.
 *
 * Usage:
 *   ./chai_hsto [--size N]
 *
 *   --size N         Number of input bytes (default: 16777216 = 16M)
 *   --iterations I   Timed iterations (default: 5)
 *
 * Output (stdout): CSV — chai_hsto,<N>,<time_ms>,<GBs>
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
// Constants
// ---------------------------------------------------------------------------

#define NUM_BINS 256
#define BLOCK_SIZE 256

// ---------------------------------------------------------------------------
// CPU reference: histogram
// ---------------------------------------------------------------------------

static void histogram_cpu(const unsigned char* data, unsigned int* histo, int N) {
    memset(histo, 0, NUM_BINS * sizeof(unsigned int));
    for (int i = 0; i < N; ++i) {
        histo[data[i]]++;
    }
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------

int main(int argc, char* argv[]) {
    @autoreleasepool {
    setvbuf(stdout, NULL, _IONBF, 0);

    // Defaults from params.json
    int size       = 16777216;
    int num_bins   = NUM_BINS;
    int block_size = BLOCK_SIZE;

    // Command-line fallback
    size       = parseIntParam(argc, argv, "--size", size);
    num_bins   = parseIntParam(argc, argv, "--num_bins", num_bins);
    block_size = parseIntParam(argc, argv, "--block_size", block_size);

    // Environment variable overrides
    const char* env_val;
    env_val = getenv("BENCH_PARAM_size");
    if (env_val) size = atoi(env_val);
    env_val = getenv("BENCH_PARAM_num_bins");
    if (env_val) num_bins = atoi(env_val);
    env_val = getenv("BENCH_PARAM_block_size");
    if (env_val) block_size = atoi(env_val);
    int num_warmup = 0;

    int N     = size;

    size_t data_bytes  = (size_t)N * sizeof(unsigned char);
    size_t histo_bytes = num_bins * sizeof(unsigned int);

    // -------------------------------------------------------------------
    // Metal device setup
    // -------------------------------------------------------------------
    id<MTLDevice> device = MTLCreateSystemDefaultDevice();
    if (!device) {
        fprintf(stderr, "Error: no Metal device found\n");
        return EXIT_FAILURE;
    }
    fprintf(stderr, "Device: %s\n", device.name.UTF8String);
    fprintf(stderr, "Histogram  |  N: %d (%.2f MB)  |  Bins: %d  |  "
            "Iterations: %d warmup + %d timed\n\n",
            N, (double)data_bytes / (1024.0 * 1024.0), num_bins, num_warmup);

    id<MTLCommandQueue> queue = [device newCommandQueue];

    // -------------------------------------------------------------------
    // Compile Metal shader
    // -------------------------------------------------------------------
    NSError* err = nil;
    id<MTLLibrary> library = nil;

    NSString* exePath = [[NSBundle mainBundle] executablePath];
    NSString* dir     = [exePath stringByDeletingLastPathComponent];
    NSString* srcPath = [dir stringByAppendingPathComponent:@"chai_hsto.metal"];

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
        fprintf(stderr, "Error: chai_hsto.metal not found at %s\n",
                srcPath.UTF8String);
        return EXIT_FAILURE;
    }

    id<MTLFunction> fnKernel = [library newFunctionWithName:@"histogram_kernel"];
    if (!fnKernel) {
        fprintf(stderr, "Error: kernel function not found\n");
        return EXIT_FAILURE;
    }

    id<MTLComputePipelineState> pso =
        [device newComputePipelineStateWithFunction:fnKernel error:&err];
    if (!pso) {
        fprintf(stderr, "Error creating pipeline: %s\n",
                err.localizedDescription.UTF8String);
        return EXIT_FAILURE;
    }

    // -------------------------------------------------------------------
    // Generate random input data (deterministic seed)
    // -------------------------------------------------------------------
    std::vector<unsigned char> h_data(N);
    unsigned int seed = 12345;
    for (int i = 0; i < N; ++i) {
        seed = seed * 1103515245u + 12345u;
        h_data[i] = (unsigned char)((seed >> 16) & 0xFF);
    }

    // -------------------------------------------------------------------
    // Allocate Metal buffers
    // -------------------------------------------------------------------
    id<MTLBuffer> bufData = [device newBufferWithBytes:h_data.data()
                                               length:data_bytes
                                              options:MTLResourceStorageModeShared];
    id<MTLBuffer> bufHisto = [device newBufferWithLength:histo_bytes
                                                options:MTLResourceStorageModeShared];

    uint32_t paramN = (uint32_t)N;

    NSUInteger tgSize = (NSUInteger)block_size;
    // Grid size: enough threads with grid-stride, cap to reasonable number
    NSUInteger gridN = (NSUInteger)((N + tgSize - 1) / tgSize);
    if (gridN > 1024) gridN = 1024;
    gridN *= tgSize;

    // Lambda: run histogram
    auto run_histo = [&]() {
        // Clear histogram
        memset(bufHisto.contents, 0, histo_bytes);

        id<MTLCommandBuffer> cb = [queue commandBuffer];
        id<MTLComputeCommandEncoder> enc = [cb computeCommandEncoder];

        [enc setComputePipelineState:pso];
        [enc setBuffer:bufData  offset:0 atIndex:0];
        [enc setBuffer:bufHisto offset:0 atIndex:1];
        [enc setBytes:&paramN length:sizeof(paramN) atIndex:2];
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
        run_histo();
    }

    // -------------------------------------------------------------------
    // Timed iterations
    // -------------------------------------------------------------------
    std::vector<double> times(1);
    for (int i = 0; i < 1; ++i) {
        uint64_t t0 = mach_absolute_time();
        run_histo();
        uint64_t t1 = mach_absolute_time();
        times[i] = ticks_to_ms(t1 - t0);
    }

    double sum = 0.0;
    for (int i = 0; i < 1; ++i) sum += times[i];
    double avg_ms = sum / 1;

    // GB/s = N * sizeof(uint8_t) / time_s / 1e9
    double gbs = (double)N * 1.0 / (avg_ms * 1e-3) / 1e9;

    double total_ms = sum;

    // JSON-lines kernel event
    printf("{\"type\":\"kernel\",\"name\":\"histogram_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"size\":%d\"num_bins\":%d,"
           "\"block_size\":%d}}\n",
           avg_ms, size, num_bins, block_size);

    // JSON-lines summary event
    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"throughput_gbps\",\"value\":%.2f}]}\n",
           total_ms, gbs);

    // Human-readable output to stderr
    fprintf(stderr, "Average time: %.4f ms\n", avg_ms);
    fprintf(stderr, "Throughput:   %.4f GB/s\n", gbs);

    // -------------------------------------------------------------------
    // Verification (compare GPU histogram against CPU reference)
    // -------------------------------------------------------------------
    {
        const unsigned int* gpuHisto = (const unsigned int*)bufHisto.contents;

        std::vector<unsigned int> cpu_histo(num_bins);
        histogram_cpu(h_data.data(), cpu_histo.data(), N);

        int errors = 0;
        for (int b = 0; b < num_bins; ++b) {
            if (gpuHisto[b] != cpu_histo[b]) {
                if (errors < 10) {
                    fprintf(stderr, "Mismatch at bin %d: GPU=%u CPU=%u\n",
                            b, gpuHisto[b], cpu_histo[b]);
                }
                errors++;
            }
        }

        if (errors > 0)
            fprintf(stderr, "FAIL: %d bin errors out of %d bins\n",
                    errors, num_bins);
        else
            fprintf(stderr, "PASS\n");
    }

    return EXIT_SUCCESS;

    } // @autoreleasepool
}
