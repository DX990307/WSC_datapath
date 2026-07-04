/**
 * rodinia_backprop_metal.mm — Apple Metal host for the Backpropagation benchmark.
 *
 * Implements a two-layer fully-connected neural network:
 *   input (INPUT_N) → hidden (HIDDEN_N) → output (OUTPUT_N)
 *
 * Default sizes:
 *   INPUT_N  = 65536
 *   HIDDEN_N = 1024
 *   OUTPUT_N = 1
 *
 * Usage:
 *   ./rodinia_backprop [--input N] [--hidden H] [--output O]
 *
 * Output (stdout): CSV — backprop,<input_n>,<hidden_n>,<time_ms>,<GFLOPS>
 * Output (stderr): device info, timing details
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
#include <vector>
#include <mach/mach_time.h>

// ---------------------------------------------------------------------------
// Default sizes
// ---------------------------------------------------------------------------
#define DEFAULT_INPUT_N  65536
#define DEFAULT_HIDDEN_N 1024
#define DEFAULT_OUTPUT_N 1
#define DEFAULT_ITERS    5
#define LR_DEFAULT       0.1f

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
static int parseIntParam(int argc, char** argv, const char* name, int def)
{
    for (int i = 1; i < argc - 1; i++) {
        if (strcmp(argv[i], name) == 0) {
            int v = atoi(argv[i + 1]);
            if (v > 0) return v;
        }
    }
    return def;
}

// ---------------------------------------------------------------------------
// main
// ---------------------------------------------------------------------------
int main(int argc, char* argv[])
{
    @autoreleasepool {
    setvbuf(stdout, NULL, _IONBF, 0);

    // Defaults
    int input_n  = DEFAULT_INPUT_N;
    int hidden_n = DEFAULT_HIDDEN_N;
    int output_n = DEFAULT_OUTPUT_N;

    // Command-line fallback
    input_n  = parseIntParam(argc, argv, "--input", input_n);
    hidden_n = parseIntParam(argc, argv, "--hidden", hidden_n);
    output_n = parseIntParam(argc, argv, "--output", output_n);

    // Environment variable overrides
    const char* env_val;
    env_val = getenv("BENCH_PARAM_input");
    if (env_val) input_n = atoi(env_val);
    env_val = getenv("BENCH_PARAM_hidden");
    if (env_val) hidden_n = atoi(env_val);
    env_val = getenv("BENCH_PARAM_output");
    if (env_val) output_n = atoi(env_val);
    int num_warmup = 0;

    // -----------------------------------------------------------------------
    // Metal device setup
    // -----------------------------------------------------------------------
    id<MTLDevice> device = MTLCreateSystemDefaultDevice();
    if (!device) {
        fprintf(stderr, "Error: no Metal device found\n");
        return EXIT_FAILURE;
    }
    fprintf(stderr, "Device: %s\n", device.name.UTF8String);
    fprintf(stderr, "Network: %d → %d → %d\n\n",
            input_n, hidden_n, output_n);

    id<MTLCommandQueue> queue = [device newCommandQueue];

    // -----------------------------------------------------------------------
    // Load Metal shader (look next to the executable)
    // -----------------------------------------------------------------------
    NSError *err = nil;
    id<MTLLibrary> library = nil;

    NSString *exePath = [[NSBundle mainBundle] executablePath];
    NSString *dir     = [exePath stringByDeletingLastPathComponent];
    NSString *srcPath = [dir stringByAppendingPathComponent:@"rodinia_backprop.metal"];

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
        fprintf(stderr, "Error: rodinia_backprop.metal not found at %s\n",
                srcPath.UTF8String);
        return EXIT_FAILURE;
    }

    // -----------------------------------------------------------------------
    // Create pipeline states for all 6 kernels
    // -----------------------------------------------------------------------
    auto makePSO = [&](const char* name) -> id<MTLComputePipelineState> {
        id<MTLFunction> fn = [library newFunctionWithName:
                              [NSString stringWithUTF8String:name]];
        if (!fn) {
            fprintf(stderr, "Error: kernel '%s' not found in library\n", name);
            exit(EXIT_FAILURE);
        }
        NSError *e = nil;
        id<MTLComputePipelineState> pso =
            [device newComputePipelineStateWithFunction:fn error:&e];
        if (!pso) {
            fprintf(stderr, "Error creating pipeline '%s': %s\n",
                    name, e.localizedDescription.UTF8String);
            exit(EXIT_FAILURE);
        }
        return pso;
    };

    id<MTLComputePipelineState> pso_fwd_hid  = makePSO("forward_hidden");
    id<MTLComputePipelineState> pso_fwd_out  = makePSO("forward_output");
    id<MTLComputePipelineState> pso_bwd_out  = makePSO("backward_output_delta");
    id<MTLComputePipelineState> pso_bwd_hid  = makePSO("backward_hidden_delta");
    id<MTLComputePipelineState> pso_upd_w1   = makePSO("update_w1");
    id<MTLComputePipelineState> pso_upd_w2   = makePSO("update_w2");

    // -----------------------------------------------------------------------
    // Allocate Metal buffers (shared memory)
    // -----------------------------------------------------------------------
    size_t sz_in  = (size_t)input_n  * sizeof(float);
    size_t sz_hid = (size_t)hidden_n * sizeof(float);
    size_t sz_out = (size_t)output_n * sizeof(float);
    size_t sz_w1  = (size_t)input_n  * hidden_n * sizeof(float);
    size_t sz_w2  = (size_t)hidden_n * output_n  * sizeof(float);

    id<MTLBuffer> buf_input     = [device newBufferWithLength:sz_in
                                                      options:MTLResourceStorageModeShared];
    id<MTLBuffer> buf_w1        = [device newBufferWithLength:sz_w1
                                                      options:MTLResourceStorageModeShared];
    id<MTLBuffer> buf_b1        = [device newBufferWithLength:sz_hid
                                                      options:MTLResourceStorageModeShared];
    id<MTLBuffer> buf_hidden    = [device newBufferWithLength:sz_hid
                                                      options:MTLResourceStorageModeShared];
    id<MTLBuffer> buf_w2        = [device newBufferWithLength:sz_w2
                                                      options:MTLResourceStorageModeShared];
    id<MTLBuffer> buf_b2        = [device newBufferWithLength:sz_out
                                                      options:MTLResourceStorageModeShared];
    id<MTLBuffer> buf_output    = [device newBufferWithLength:sz_out
                                                      options:MTLResourceStorageModeShared];
    id<MTLBuffer> buf_target    = [device newBufferWithLength:sz_out
                                                      options:MTLResourceStorageModeShared];
    id<MTLBuffer> buf_delta_out = [device newBufferWithLength:sz_out
                                                      options:MTLResourceStorageModeShared];
    id<MTLBuffer> buf_delta_hid = [device newBufferWithLength:sz_hid
                                                      options:MTLResourceStorageModeShared];

    // -----------------------------------------------------------------------
    // Initialize host data
    // -----------------------------------------------------------------------
    float* h_input  = (float*)buf_input.contents;
    float* h_w1     = (float*)buf_w1.contents;
    float* h_b1     = (float*)buf_b1.contents;
    float* h_w2     = (float*)buf_w2.contents;
    float* h_b2     = (float*)buf_b2.contents;
    float* h_target = (float*)buf_target.contents;

    srand(42);
    for (int i = 0; i < input_n; i++)
        h_input[i] = (float)(i % 256) / 256.0f;
    for (int i = 0; i < input_n * hidden_n; i++)
        h_w1[i] = ((float)rand() / RAND_MAX - 0.5f) * 0.2f;  // [-0.1, 0.1]
    memset(h_b1, 0, sz_hid);
    for (int i = 0; i < hidden_n * output_n; i++)
        h_w2[i] = ((float)rand() / RAND_MAX - 0.5f) * 0.2f;
    memset(h_b2, 0, sz_out);
    for (int k = 0; k < output_n; k++)
        h_target[k] = 1.0f;

    // -----------------------------------------------------------------------
    // Constant buffer data
    // -----------------------------------------------------------------------
    // dims for various kernels (uint2 or uint)
    uint32_t dims_fwd_hid[2] = { (uint32_t)input_n,  (uint32_t)hidden_n };
    uint32_t dims_fwd_out[2] = { (uint32_t)hidden_n, (uint32_t)output_n };
    uint32_t dims_bwd_hid[2] = { (uint32_t)hidden_n, (uint32_t)output_n };
    uint32_t dims_upd_w1[2]  = { (uint32_t)input_n,  (uint32_t)hidden_n };
    uint32_t dims_upd_w2[2]  = { (uint32_t)hidden_n, (uint32_t)output_n };
    uint32_t out_n_scalar     = (uint32_t)output_n;
    float    lr_val           = LR_DEFAULT;

    // Thread group sizes
    NSUInteger tg1d = MIN((NSUInteger)256, pso_fwd_hid.maxTotalThreadsPerThreadgroup);

    // -----------------------------------------------------------------------
    // Helper: dispatch a 1D kernel
    // -----------------------------------------------------------------------
    auto dispatch1D = [&](id<MTLCommandBuffer> cb,
                          id<MTLComputePipelineState> pso,
                          NSUInteger gridSize,
                          NSUInteger tgSize,
                          // up to 6 buffers + 2 constants
                          std::vector<id<MTLBuffer>> bufs,
                          std::vector<std::pair<const void*, size_t>> consts)
    {
        id<MTLComputeCommandEncoder> enc = [cb computeCommandEncoder];
        [enc setComputePipelineState:pso];
        for (NSUInteger i = 0; i < (NSUInteger)bufs.size(); i++)
            [enc setBuffer:bufs[i] offset:0 atIndex:i];
        NSUInteger idx = (NSUInteger)bufs.size();
        for (auto& c : consts) {
            [enc setBytes:c.first length:c.second atIndex:idx++];
        }
        [enc dispatchThreads:MTLSizeMake(gridSize, 1, 1)
            threadsPerThreadgroup:MTLSizeMake(tgSize, 1, 1)];
        [enc endEncoding];
    };

    // -----------------------------------------------------------------------
    // One epoch: forward + backward
    // -----------------------------------------------------------------------
    auto run_epoch = [&]() {
        // 1. forward_hidden
        {
            id<MTLCommandBuffer> cb = [queue commandBuffer];
            dispatch1D(cb, pso_fwd_hid, (NSUInteger)hidden_n, tg1d,
                       { buf_input, buf_w1, buf_b1, buf_hidden },
                       { { dims_fwd_hid, sizeof(dims_fwd_hid) } });
            [cb commit];
            [cb waitUntilCompleted];
        }
        // 2. forward_output
        {
            id<MTLCommandBuffer> cb = [queue commandBuffer];
            dispatch1D(cb, pso_fwd_out, (NSUInteger)output_n, tg1d,
                       { buf_hidden, buf_w2, buf_b2, buf_output },
                       { { dims_fwd_out, sizeof(dims_fwd_out) } });
            [cb commit];
            [cb waitUntilCompleted];
        }
        // 3. backward_output_delta
        {
            id<MTLCommandBuffer> cb = [queue commandBuffer];
            dispatch1D(cb, pso_bwd_out, (NSUInteger)output_n, tg1d,
                       { buf_output, buf_target, buf_delta_out },
                       { { &out_n_scalar, sizeof(out_n_scalar) } });
            [cb commit];
            [cb waitUntilCompleted];
        }
        // 4. backward_hidden_delta
        {
            id<MTLCommandBuffer> cb = [queue commandBuffer];
            dispatch1D(cb, pso_bwd_hid, (NSUInteger)hidden_n, tg1d,
                       { buf_hidden, buf_w2, buf_delta_out, buf_delta_hid },
                       { { dims_bwd_hid, sizeof(dims_bwd_hid) } });
            [cb commit];
            [cb waitUntilCompleted];
        }
        // 5. update_w1 (2D dispatch)
        {
            id<MTLCommandBuffer>         cb  = [queue commandBuffer];
            id<MTLComputeCommandEncoder> enc = [cb computeCommandEncoder];
            [enc setComputePipelineState:pso_upd_w1];
            [enc setBuffer:buf_w1        offset:0 atIndex:0];
            [enc setBuffer:buf_input     offset:0 atIndex:1];
            [enc setBuffer:buf_delta_hid offset:0 atIndex:2];
            [enc setBytes:dims_upd_w1 length:sizeof(dims_upd_w1) atIndex:3];
            [enc setBytes:&lr_val    length:sizeof(lr_val)        atIndex:4];
            MTLSize tgSize2D = { 16, 16, 1 };
            MTLSize gridSize2D = { (NSUInteger)input_n, (NSUInteger)hidden_n, 1 };
            [enc dispatchThreads:gridSize2D threadsPerThreadgroup:tgSize2D];
            [enc endEncoding];
            [cb commit];
            [cb waitUntilCompleted];
        }
        // 6. update_w2
        {
            id<MTLCommandBuffer> cb = [queue commandBuffer];
            dispatch1D(cb, pso_upd_w2, (NSUInteger)hidden_n, tg1d,
                       { buf_w2, buf_hidden, buf_delta_out },
                       { { dims_upd_w2, sizeof(dims_upd_w2) },
                         { &lr_val,      sizeof(lr_val) } });
            [cb commit];
            [cb waitUntilCompleted];
        }
    };

    // -----------------------------------------------------------------------
    // Warmup
    // -----------------------------------------------------------------------
    for (int w = 0; w < num_warmup; ++w) {
        run_epoch();
    }

    // -----------------------------------------------------------------------
    // Timed iterations
    // -----------------------------------------------------------------------
    std::vector<double> times(1);
    for (int it = 0; it < 1; it++) {
        uint64_t t0 = mach_absolute_time();
        run_epoch();
        uint64_t t1 = mach_absolute_time();
        times[it] = ticks_to_ms(t1 - t0);
    }

    // -----------------------------------------------------------------------
    // Statistics
    // -----------------------------------------------------------------------
    double sum_ms = 0.0, mn_ms = DBL_MAX, mx_ms = 0.0;
    for (int i = 0; i < 1; i++) {
        sum_ms += times[i];
        if (times[i] < mn_ms) mn_ms = times[i];
        if (times[i] > mx_ms) mx_ms = times[i];
    }
    double avg_ms = sum_ms / 1;

    double flops  = 4.0 * (double)input_n * hidden_n +
                    4.0 * (double)hidden_n * output_n;
    double gflops = flops / (avg_ms * 1e-3) / 1e9;

    fprintf(stderr, "GFLOPS: %.2f  (avg %.4f ms, min %.4f ms, max %.4f ms)\n",
            gflops, avg_ms, mn_ms, mx_ms);

    // JSON-lines kernel events (one per kernel in params.json)
    const char* kernel_names[] = {
        "forward_hidden", "forward_output", "backward_output_delta",
        "backward_hidden_delta", "update_w1", "update_w2"
    };
    for (int ki = 0; ki < 6; ki++) {
        printf("{\"type\":\"kernel\",\"name\":\"%s\",\"time_ms\":%.6f,"
               "\"params\":{\"input\":%d,\"hidden\":%d,\"output\":%d}}\n",
               kernel_names[ki], avg_ms, input_n, hidden_n, output_n);
    }

    // JSON-lines summary event
    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"gflops\",\"value\":%.2f}]}\n",
           sum_ms, gflops);

    return EXIT_SUCCESS;

    } // @autoreleasepool
}
