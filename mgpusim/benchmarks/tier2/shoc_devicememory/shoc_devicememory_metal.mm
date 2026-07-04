/**
 * shoc_devicememory_metal.mm — Apple Metal host for SHOC DeviceMemory benchmark.
 *
 * Measures device global memory bandwidth for three access patterns:
 *   - Read-only
 *   - Write-only
 *   - Read-write
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
#include <functional>
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
// Benchmark helper
// ---------------------------------------------------------------------------

struct KernelResult {
    double avg_ms;
    double total_ms;
};

static int g_num_warmup = 0;

static KernelResult runBenchmarkMetal(
    int iters,
    id<MTLCommandQueue>          queue,
    id<MTLComputePipelineState>  pso,
    NSUInteger                   num_threads,
    NSUInteger                   tg_size,
    std::function<void(id<MTLComputeCommandEncoder>)> setup_fn)
{
    auto dispatch_one = [&]() {
        id<MTLCommandBuffer>         cb  = [queue commandBuffer];
        id<MTLComputeCommandEncoder> enc = [cb computeCommandEncoder];
        [enc setComputePipelineState:pso];
        setup_fn(enc);
        [enc dispatchThreads:MTLSizeMake(num_threads, 1, 1)
            threadsPerThreadgroup:MTLSizeMake(tg_size, 1, 1)];
        [enc endEncoding];
        [cb commit];
        [cb waitUntilCompleted];
    };

    // Warmup
    for (int w = 0; w < g_num_warmup; ++w) {
        dispatch_one();
    }

    std::vector<double> times(1);
    for (int i = 0; i < 1; ++i) {
        uint64_t t0 = mach_absolute_time();
        dispatch_one();
        uint64_t t1 = mach_absolute_time();
        times[i] = ticks_to_ms(t1 - t0);
    }

    double sum = 0.0;
    for (int i = 0; i < 1; ++i) sum += times[i];
    double avg = sum / 1;

    KernelResult r;
    r.avg_ms = avg;
    r.total_ms = sum;
    return r;
}

// ---------------------------------------------------------------------------
// main
// ---------------------------------------------------------------------------

int main(int argc, char* argv[])
{
    @autoreleasepool {

    setvbuf(stdout, NULL, _IONBF, 0);

    // Defaults
    int array_size_mb = 64;
    int block_size    = 256;
    const char* transfer_mode = "readwrite";

    // Command-line fallback
    array_size_mb = parseIntParam(argc, argv, "--array_size_mb", array_size_mb);
    block_size    = parseIntParam(argc, argv, "--block_size", block_size);
    transfer_mode = parseStrParam(argc, argv, "--transfer_mode", transfer_mode);

    // Environment variable overrides
    const char* env_val;
    env_val = getenv("BENCH_PARAM_array_size_mb");
    if (env_val) array_size_mb = atoi(env_val);
    env_val = getenv("BENCH_PARAM_block_size");
    if (env_val) block_size = atoi(env_val);
    env_val = getenv("BENCH_PARAM_transfer_mode");
    if (env_val) transfer_mode = env_val;
    int num_warmup = 0;

    size_t N     = (size_t)array_size_mb * 1024 * 1024 / sizeof(float);
    size_t bytes = N * sizeof(float);

    // -------------------------------------------------------------------
    // Metal device setup
    // -------------------------------------------------------------------
    id<MTLDevice> device = MTLCreateSystemDefaultDevice();
    if (!device) {
        fprintf(stderr, "Error: no Metal device found\n");
        return EXIT_FAILURE;
    }
    fprintf(stderr, "Device: %s\n", device.name.UTF8String);
    fprintf(stderr, "Array size: %d MB (%zu floats)\n\n",
            array_size_mb, N);

    id<MTLCommandQueue> queue = [device newCommandQueue];

    // -------------------------------------------------------------------
    // Compile Metal shader from source file next to the binary
    // -------------------------------------------------------------------
    NSError *err = nil;
    id<MTLLibrary> library = nil;

    NSString *exePath = [[NSBundle mainBundle] executablePath];
    NSString *dir     = [exePath stringByDeletingLastPathComponent];
    NSString *srcPath = [dir stringByAppendingPathComponent:@"shoc_devicememory.metal"];

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
        fprintf(stderr, "Error: shoc_devicememory.metal not found at %s\n",
                srcPath.UTF8String);
        return EXIT_FAILURE;
    }

    auto makePSO = [&](NSString* name) -> id<MTLComputePipelineState> {
        id<MTLFunction> fn = [library newFunctionWithName:name];
        if (!fn) {
            fprintf(stderr, "Error: kernel '%s' not found\n", name.UTF8String);
            exit(EXIT_FAILURE);
        }
        NSError *e = nil;
        id<MTLComputePipelineState> p = [device newComputePipelineStateWithFunction:fn error:&e];
        if (!p) {
            fprintf(stderr, "Error creating pipeline for '%s': %s\n",
                    name.UTF8String, e.localizedDescription.UTF8String);
            exit(EXIT_FAILURE);
        }
        return p;
    };

    id<MTLComputePipelineState> pso_read  = makePSO(@"readOnly_kernel");
    id<MTLComputePipelineState> pso_write = makePSO(@"writeOnly_kernel");
    id<MTLComputePipelineState> pso_rw    = makePSO(@"readWrite_kernel");

    // -------------------------------------------------------------------
    // Allocate buffers
    // -------------------------------------------------------------------
    id<MTLBuffer> buf_input  = [device newBufferWithLength:bytes
                                                   options:MTLResourceStorageModeShared];
    id<MTLBuffer> buf_output = [device newBufferWithLength:bytes
                                                   options:MTLResourceStorageModeShared];

    float* h_input = (float*)buf_input.contents;
    for (size_t i = 0; i < N; ++i)
        h_input[i] = (float)(i % 1000) * 0.001f;

    NSUInteger tg_size    = 256;
    NSUInteger num_groups = (N + tg_size - 1) / tg_size;
    if (num_groups > 65535) num_groups = 65535;
    NSUInteger num_threads = num_groups * tg_size;

    uint32_t params_ro[2] = { (uint32_t)N, (uint32_t)num_threads };
    uint32_t params_wo[2] = { (uint32_t)N, (uint32_t)num_threads };
    uint32_t params_rw[2] = { (uint32_t)N, (uint32_t)num_threads };

    g_num_warmup = num_warmup;
    double total_all_ms = 0.0;

    // --- Read-only ---
    {
        KernelResult kr = runBenchmarkMetal(
            1, queue, pso_read, num_threads, tg_size,
            [&](id<MTLComputeCommandEncoder> enc) {
                [enc setBuffer:buf_input  offset:0 atIndex:0];
                [enc setBuffer:buf_output offset:0 atIndex:1];
                [enc setBytes:params_ro length:sizeof(params_ro) atIndex:2];
            });
        double read_bw = (double)bytes / (kr.avg_ms * 1e-3) / 1e9;
        total_all_ms += kr.total_ms;

        printf("{\"type\":\"kernel\",\"name\":\"readOnly_kernel\",\"time_ms\":%.6f,"
               "\"params\":{\"array_size_mb\":%d,\"block_size\":%d"
               "\"transfer_mode\":\"%s\"}}\n",
               kr.avg_ms, array_size_mb, block_size, transfer_mode);

        fprintf(stderr, "Read-only bandwidth:  %.2f GB/s  (%d MB)\n", read_bw, array_size_mb);
    }

    // --- Write-only ---
    {
        KernelResult kr = runBenchmarkMetal(
            1, queue, pso_write, num_threads, tg_size,
            [&](id<MTLComputeCommandEncoder> enc) {
                [enc setBuffer:buf_output offset:0 atIndex:0];
                [enc setBytes:params_wo length:sizeof(params_wo) atIndex:1];
            });
        double write_bw = (double)bytes / (kr.avg_ms * 1e-3) / 1e9;
        total_all_ms += kr.total_ms;

        printf("{\"type\":\"kernel\",\"name\":\"writeOnly_kernel\",\"time_ms\":%.6f,"
               "\"params\":{\"array_size_mb\":%d,\"block_size\":%d"
               "\"transfer_mode\":\"%s\"}}\n",
               kr.avg_ms, array_size_mb, block_size, transfer_mode);

        fprintf(stderr, "Write-only bandwidth: %.2f GB/s  (%d MB)\n", write_bw, array_size_mb);
    }

    // --- Read-write ---
    for (size_t i = 0; i < N; ++i)
        h_input[i] = (float)(i % 1000) * 0.001f;

    {
        KernelResult kr = runBenchmarkMetal(
            1, queue, pso_rw, num_threads, tg_size,
            [&](id<MTLComputeCommandEncoder> enc) {
                [enc setBuffer:buf_input offset:0 atIndex:0];
                [enc setBytes:params_rw length:sizeof(params_rw) atIndex:1];
            });
        double rw_bw = 2.0 * (double)bytes / (kr.avg_ms * 1e-3) / 1e9;
        total_all_ms += kr.total_ms;

        printf("{\"type\":\"kernel\",\"name\":\"readWrite_kernel\",\"time_ms\":%.6f,"
               "\"params\":{\"array_size_mb\":%d,\"block_size\":%d"
               "\"transfer_mode\":\"%s\"}}\n",
               kr.avg_ms, array_size_mb, block_size, transfer_mode);

        fprintf(stderr, "Read-write bandwidth: %.2f GB/s  (%d MB)\n", rw_bw, array_size_mb);
    }

    // JSON-lines summary event
    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[]}\n",
           total_all_ms);

    return EXIT_SUCCESS;

    } // @autoreleasepool
}
