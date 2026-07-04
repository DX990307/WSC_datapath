/**
 * rodinia_pathfinder_metal.mm — Apple Metal host for Rodinia PathFinder.
 *
 * Computes the minimum-cost path through a 2D grid (top to bottom) using
 * double-buffered row sweeps on the GPU.
 *
 * Usage:
 *   ./rodinia_pathfinder [--rows R] [--cols C]
 *
 *   --rows R        Grid rows                  (default: 100000)
 *   --cols C        Grid columns               (default: 100)
 *   --iterations I  Timed benchmark iterations (default: 3)
 *
 * Output (stdout): CSV — benchmark,rows,cols,time_ms,GBs
 * Output (stderr): Device info and verification result
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
#include <climits>
#include <vector>
#include <algorithm>
#include <mach/mach_time.h>

// ---------------------------------------------------------------------------
// Timing helpers
// ---------------------------------------------------------------------------

static double ticks_to_ms(uint64_t ticks)
{
    static mach_timebase_info_data_t tb = {0, 0};
    if (tb.denom == 0) mach_timebase_info(&tb);
    return (double)ticks * (double)tb.numer / (double)tb.denom * 1e-6;
}

// ---------------------------------------------------------------------------
// Argument parsing
// ---------------------------------------------------------------------------

static int parseIntParam(int argc, char** argv, const char* name, int def) {
    for (int i = 1; i < argc - 1; ++i) {
        if (strcmp(argv[i], name) == 0) {
            int v = atoi(argv[i + 1]);
            if (v > 0) return v;
        }
    }
    return def;
}

// ---------------------------------------------------------------------------
// CPU reference for verification
// ---------------------------------------------------------------------------

static void cpu_pathfinder(const int* wall, int* result, int rows, int cols) {
    std::vector<int> src(cols), dst(cols);
    for (int c = 0; c < cols; c++) src[c] = wall[c];
    for (int t = 1; t < rows; t++) {
        for (int c = 0; c < cols; c++) {
            int left  = (c > 0)      ? src[c - 1] : INT_MAX;
            int above = src[c];
            int right = (c < cols-1) ? src[c + 1] : INT_MAX;
            dst[c] = wall[(long long)t * cols + c] + std::min({left, above, right});
        }
        std::swap(src, dst);
    }
    for (int c = 0; c < cols; c++) result[c] = src[c];
}

// ---------------------------------------------------------------------------
// main
// ---------------------------------------------------------------------------

int main(int argc, char* argv[])
{
    @autoreleasepool {
    setvbuf(stdout, NULL, _IONBF, 0);

    // Defaults
    int cols  = 100;
    int rows  = 100000;
    int block_size_param = 256;

    // Command-line fallback
    rows  = parseIntParam(argc, argv, "--rows", rows);
    cols  = parseIntParam(argc, argv, "--cols", cols);

    // Environment variable overrides
    const char* env_val;
    env_val = getenv("BENCH_PARAM_cols");
    if (env_val) cols = atoi(env_val);
    env_val = getenv("BENCH_PARAM_rows");
    if (env_val) rows = atoi(env_val);
    env_val = getenv("BENCH_PARAM_block_size");
    if (env_val) block_size_param = atoi(env_val);
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
    fprintf(stderr, "Grid: %d rows x %d cols\n\n",
            rows, cols);

    id<MTLCommandQueue> queue = [device newCommandQueue];

    // -------------------------------------------------------------------
    // Compile Metal shader from source file next to the binary
    // -------------------------------------------------------------------
    NSError* err = nil;
    id<MTLLibrary> library = nil;

    NSString* exePath = [[NSBundle mainBundle] executablePath];
    NSString* dir     = [exePath stringByDeletingLastPathComponent];
    NSString* srcPath = [dir stringByAppendingPathComponent:@"rodinia_pathfinder.metal"];

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
        fprintf(stderr, "Error: rodinia_pathfinder.metal not found at %s\n",
                srcPath.UTF8String);
        return EXIT_FAILURE;
    }

    id<MTLFunction> fn = [library newFunctionWithName:@"dynproc_kernel"];
    if (!fn) {
        fprintf(stderr, "Error: kernel 'dynproc_kernel' not found\n");
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
    // Host data initialization
    // -------------------------------------------------------------------
    long long total_cells = (long long)rows * cols;
    size_t wall_bytes = (size_t)total_cells * sizeof(int);
    size_t row_bytes  = (size_t)cols * sizeof(int);

    // Allocate wall on CPU (may be large; use plain malloc)
    int* h_wall = (int*)malloc(wall_bytes);
    if (!h_wall) {
        fprintf(stderr, "Error: failed to allocate host wall (%zu bytes)\n", wall_bytes);
        return EXIT_FAILURE;
    }
    srand(42);
    for (long long i = 0; i < total_cells; i++) {
        h_wall[i] = rand() % 10;
    }

    // -------------------------------------------------------------------
    // Verification with small grid
    // -------------------------------------------------------------------
    {
        int vrows = (rows  < 1000) ? rows  : 1000;
        int vcols = (cols  < 100)  ? cols  : 100;
        long long vcells = (long long)vrows * vcols;
        size_t vwall_bytes = (size_t)vcells * sizeof(int);
        size_t vrow_bytes  = (size_t)vcols  * sizeof(int);

        int* v_wall = (int*)malloc(vwall_bytes);
        srand(123);
        for (long long i = 0; i < vcells; i++) v_wall[i] = rand() % 10;

        // CPU reference
        std::vector<int> cpu_result(vcols);
        cpu_pathfinder(v_wall, cpu_result.data(), vrows, vcols);

        // GPU (Metal) reference
        id<MTLBuffer> vbuf_wall = [device newBufferWithBytes:v_wall
                                                      length:vwall_bytes
                                                     options:MTLResourceStorageModeShared];
        id<MTLBuffer> vbuf0 = [device newBufferWithLength:vrow_bytes
                                                   options:MTLResourceStorageModeShared];
        id<MTLBuffer> vbuf1 = [device newBufferWithLength:vrow_bytes
                                                   options:MTLResourceStorageModeShared];

        // Initialize src = row 0
        memcpy(vbuf0.contents, v_wall, vrow_bytes);

        NSUInteger threadgroup_size = (NSUInteger)std::min(vcols, 256);
        NSUInteger threads_total    = (NSUInteger)vcols;

        id<MTLBuffer> src_buf = vbuf0;
        id<MTLBuffer> dst_buf = vbuf1;

        for (int t = 1; t < vrows; t++) {
            uint32_t params[2] = {(uint32_t)vcols, (uint32_t)t};
            id<MTLCommandBuffer>         cb  = [queue commandBuffer];
            id<MTLComputeCommandEncoder> enc = [cb computeCommandEncoder];
            [enc setComputePipelineState:pso];
            [enc setBuffer:src_buf  offset:0 atIndex:0];
            [enc setBuffer:dst_buf  offset:0 atIndex:1];
            [enc setBuffer:vbuf_wall offset:0 atIndex:2];
            [enc setBytes:params length:sizeof(params) atIndex:3];
            [enc dispatchThreads:MTLSizeMake(threads_total, 1, 1)
                threadsPerThreadgroup:MTLSizeMake(threadgroup_size, 1, 1)];
            [enc endEncoding];
            [cb commit];
            [cb waitUntilCompleted];
            id<MTLBuffer> tmp = src_buf; src_buf = dst_buf; dst_buf = tmp;
        }

        int* gpu_result = (int*)src_buf.contents;
        bool ok = true;
        for (int c = 0; c < vcols && ok; c++) {
            if (cpu_result[c] != gpu_result[c]) {
                fprintf(stderr, "VERIFY FAIL at col %d: cpu=%d gpu=%d\n",
                        c, cpu_result[c], gpu_result[c]);
                ok = false;
            }
        }
        if (ok) fprintf(stderr, "Verification PASSED (small grid %dx%d)\n", vrows, vcols);
        free(v_wall);
    }

    // -------------------------------------------------------------------
    // Allocate GPU buffers for full benchmark
    // -------------------------------------------------------------------
    // Wall buffer — large, use shared storage for efficiency on Apple Silicon
    id<MTLBuffer> buf_wall = [device newBufferWithBytes:h_wall
                                                 length:wall_bytes
                                                options:MTLResourceStorageModeShared];
    id<MTLBuffer> buf0 = [device newBufferWithLength:row_bytes
                                             options:MTLResourceStorageModeShared];
    id<MTLBuffer> buf1 = [device newBufferWithLength:row_bytes
                                             options:MTLResourceStorageModeShared];

    if (!buf_wall || !buf0 || !buf1) {
        fprintf(stderr, "Error: failed to allocate Metal buffers\n");
        return EXIT_FAILURE;
    }

    NSUInteger threadgroup_size = (NSUInteger)std::min(cols, 256);
    NSUInteger threads_total    = (NSUInteger)cols;

    // Number of rows to batch per command buffer (reduces host overhead)
    const int BATCH_ROWS = 1000;

    // -------------------------------------------------------------------
    // Warmup
    // -------------------------------------------------------------------
    for (int w = 0; w < num_warmup; ++w) {
        memcpy(buf0.contents, h_wall, row_bytes); // src = row 0
        id<MTLBuffer> src_buf = buf0;
        id<MTLBuffer> dst_buf = buf1;

        int t = 1;
        while (t < rows) {
            int batch_end = std::min(t + BATCH_ROWS, rows);
            id<MTLCommandBuffer> cb = [queue commandBuffer];
            for (int row_t = t; row_t < batch_end; row_t++) {
                uint32_t params[2] = {(uint32_t)cols, (uint32_t)row_t};
                id<MTLComputeCommandEncoder> enc = [cb computeCommandEncoder];
                [enc setComputePipelineState:pso];
                [enc setBuffer:src_buf  offset:0 atIndex:0];
                [enc setBuffer:dst_buf  offset:0 atIndex:1];
                [enc setBuffer:buf_wall offset:0 atIndex:2];
                [enc setBytes:params length:sizeof(params) atIndex:3];
                [enc dispatchThreads:MTLSizeMake(threads_total, 1, 1)
                    threadsPerThreadgroup:MTLSizeMake(threadgroup_size, 1, 1)];
                [enc endEncoding];
                id<MTLBuffer> tmp = src_buf; src_buf = dst_buf; dst_buf = tmp;
            }
            [cb commit];
            [cb waitUntilCompleted];
            t = batch_end;
        }
    }

    // -------------------------------------------------------------------
    // Timed iterations
    // -------------------------------------------------------------------
    std::vector<double> times(1);

    for (int iter = 0; iter < 1; iter++) {
        memcpy(buf0.contents, h_wall, row_bytes); // reset src = row 0
        id<MTLBuffer> src_buf = buf0;
        id<MTLBuffer> dst_buf = buf1;

        uint64_t t0 = mach_absolute_time();

        int t = 1;
        while (t < rows) {
            int batch_end = std::min(t + BATCH_ROWS, rows);
            id<MTLCommandBuffer> cb = [queue commandBuffer];
            for (int row_t = t; row_t < batch_end; row_t++) {
                uint32_t params[2] = {(uint32_t)cols, (uint32_t)row_t};
                id<MTLComputeCommandEncoder> enc = [cb computeCommandEncoder];
                [enc setComputePipelineState:pso];
                [enc setBuffer:src_buf  offset:0 atIndex:0];
                [enc setBuffer:dst_buf  offset:0 atIndex:1];
                [enc setBuffer:buf_wall offset:0 atIndex:2];
                [enc setBytes:params length:sizeof(params) atIndex:3];
                [enc dispatchThreads:MTLSizeMake(threads_total, 1, 1)
                    threadsPerThreadgroup:MTLSizeMake(threadgroup_size, 1, 1)];
                [enc endEncoding];
                id<MTLBuffer> tmp = src_buf; src_buf = dst_buf; dst_buf = tmp;
            }
            [cb commit];
            [cb waitUntilCompleted];
            t = batch_end;
        }

        uint64_t t1 = mach_absolute_time();
        times[iter] = ticks_to_ms(t1 - t0);

        // Report final row min on last iteration
        if (iter == 1 - 1) {
            int* final_row = (int*)src_buf.contents;
            int min_cost = final_row[0];
            for (int c = 1; c < cols; c++)
                if (final_row[c] < min_cost) min_cost = final_row[c];
            fprintf(stderr, "Min cost in final row: %d\n", min_cost);
        }
    }

    // Compute stats
    double sum = 0.0;
    for (int i = 0; i < 1; i++) sum += times[i];
    double avg_ms = sum / 1;

    // GB/s: ROWS * COLS * sizeof(int) * 3 (2 reads: src+wall, 1 write: dst)
    double total_bytes_accessed = (double)rows * cols * sizeof(int) * 3.0;
    double gb_s = total_bytes_accessed / (avg_ms * 1e-3) / 1e9;

    double total_ms = sum;

    // JSON-lines kernel event
    printf("{\"type\":\"kernel\",\"name\":\"dynproc_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"cols\":%d,\"rows\":%d\"block_size\":%d}}\n",
           avg_ms, cols, rows, block_size_param);

    // JSON-lines summary event
    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"bandwidth_gbps\",\"value\":%.4f}]}\n",
           total_ms, gb_s);

    free(h_wall);
    return EXIT_SUCCESS;

    } // @autoreleasepool
}
