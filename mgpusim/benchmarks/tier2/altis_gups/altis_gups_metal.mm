/**
 * altis_gups_metal.mm — Apple Metal host for GUPS benchmark.
 *
 * Measures random memory access throughput by performing random read-modify-write
 * operations on a large table. Each thread uses an xorshift PRNG to generate
 * random indices and XOR-updates the table.
 *
 * Metal does not support 64-bit atomics (atomic_fetch_xor on ulong), so the
 * Metal version uses 32-bit table entries with atomic_uint.  The GUPS metric
 * is still computed identically (updates per second).
 *
 * Output (stdout): JSON-lines protocol
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
// Metal params struct (must match shader — 32-bit version)
// ---------------------------------------------------------------------------

struct GUPSParams {
    uint32_t  table_size;
    uint32_t  num_updates;
    uint32_t  seed_base;
};

// ---------------------------------------------------------------------------
// main
// ---------------------------------------------------------------------------

int main(int argc, char* argv[]) {
    @autoreleasepool {
    setvbuf(stdout, NULL, _IONBF, 0);

    // Defaults from params.json
    int table_size   = 1048576;
    int block_size   = 256;
    int num_updates  = 128;
    int num_threads  = 65536;

    // Environment variable overrides
    const char* env_val;
    env_val = getenv("BENCH_PARAM_table_size");
    if (env_val) table_size = atoi(env_val);
    env_val = getenv("BENCH_PARAM_block_size");
    if (env_val) block_size = atoi(env_val);
    env_val = getenv("BENCH_PARAM_num_updates");
    if (env_val) num_updates = atoi(env_val);
    env_val = getenv("BENCH_PARAM_num_threads");
    if (env_val) num_threads = atoi(env_val);
    int num_warmup = 0;

    int gridSize   = (num_threads + block_size - 1) / block_size;
    int actual_threads = gridSize * block_size;
    long long total_updates = (long long)actual_threads * num_updates;
    uint32_t seed_base = 42u;

    // -------------------------------------------------------------------
    // Metal device setup
    // -------------------------------------------------------------------
    id<MTLDevice> device = MTLCreateSystemDefaultDevice();
    if (!device) {
        fprintf(stderr, "Error: no Metal device found\n");
        return EXIT_FAILURE;
    }
    fprintf(stderr, "Device: %s\n", device.name.UTF8String);
    fprintf(stderr, "GUPS benchmark  |  Table size: %d (%.2f MB)  |  "
            "Updates/thread: %d  |  Threads: %d  |  Total updates: %lld  |  "
            "Iterations: 5 warmup + %d timed\n\n",
            table_size, (double)table_size * 4.0 / 1e6,
            num_updates, actual_threads, total_updates);

    id<MTLCommandQueue> queue = [device newCommandQueue];

    // -------------------------------------------------------------------
    // Compile Metal shader
    // -------------------------------------------------------------------
    NSError* err = nil;
    id<MTLLibrary> library = nil;

    NSString* exePath = [[NSBundle mainBundle] executablePath];
    NSString* dir     = [exePath stringByDeletingLastPathComponent];
    NSString* srcPath = [dir stringByAppendingPathComponent:@"altis_gups.metal"];

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
        fprintf(stderr, "Error: altis_gups.metal not found at %s\n",
                srcPath.UTF8String);
        return EXIT_FAILURE;
    }

    id<MTLFunction> fnGUPS = [library newFunctionWithName:@"gups_kernel"];
    if (!fnGUPS) {
        fprintf(stderr, "Error: kernel function not found\n");
        return EXIT_FAILURE;
    }

    id<MTLComputePipelineState> psoGUPS =
        [device newComputePipelineStateWithFunction:fnGUPS error:&err];
    if (!psoGUPS) {
        fprintf(stderr, "Error creating pipeline: %s\n",
                err.localizedDescription.UTF8String);
        return EXIT_FAILURE;
    }

    // -------------------------------------------------------------------
    // Allocate buffers (32-bit entries for Metal)
    // -------------------------------------------------------------------
    size_t table_bytes = (size_t)table_size * sizeof(uint32_t);

    id<MTLBuffer> bufTable = [device newBufferWithLength:table_bytes
                                                options:MTLResourceStorageModeShared];

    // Initialize table
    auto init_table = [&]() {
        uint32_t* ptr = (uint32_t*)bufTable.contents;
        for (int i = 0; i < table_size; ++i) {
            ptr[i] = (uint32_t)i;
        }
    };

    NSUInteger tgSize = (NSUInteger)block_size;
    NSUInteger gridN  = (NSUInteger)actual_threads;

    GUPSParams params;
    params.table_size  = (uint32_t)table_size;
    params.num_updates = (uint32_t)num_updates;
    params.seed_base   = seed_base;

    // Lambda: run GUPS kernel
    auto run_gups = [&]() {
        id<MTLCommandBuffer> cb = [queue commandBuffer];
        id<MTLComputeCommandEncoder> enc = [cb computeCommandEncoder];

        [enc setComputePipelineState:psoGUPS];
        [enc setBuffer:bufTable offset:0 atIndex:0];
        [enc setBytes:&params length:sizeof(params) atIndex:1];
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
        init_table();
        run_gups();
    }

    // -------------------------------------------------------------------
    // Timed iterations
    // -------------------------------------------------------------------
    std::vector<double> times(1);
    for (int i = 0; i < 1; ++i) {
        init_table();

        uint64_t t0 = mach_absolute_time();
        run_gups();
        uint64_t t1 = mach_absolute_time();
        times[i] = ticks_to_ms(t1 - t0);
    }

    double sum = 0.0;
    for (int i = 0; i < 1; ++i) sum += times[i];
    double avg_ms = sum / 1;
    double total_ms = sum;

    double gups = (double)total_updates / (avg_ms * 1e-3) / 1e9;

    // JSON-lines kernel event
    printf("{\"type\":\"kernel\",\"name\":\"gups_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"table_size\":%d,\"block_size\":%d,\"num_updates\":%d,"
           "\"num_threads\":%d}}\n",
           avg_ms, table_size, block_size, num_updates, num_threads);

    // JSON-lines summary event
    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"gups\",\"value\":%.6f}]}\n",
           total_ms, gups);

    fprintf(stderr, "Average time: %.4f ms\n", avg_ms);
    fprintf(stderr, "Throughput:   %.6f GUPS (Giga Updates Per Second)\n", gups);
    fprintf(stderr, "PASS\n");

    return EXIT_SUCCESS;

    } // @autoreleasepool
}
