/**
 * rodinia_nw_metal.mm — Apple Metal host for the Rodinia Needleman-Wunsch benchmark.
 *
 * Dynamic programming sequence alignment using the Needleman-Wunsch algorithm.
 * The NxN scoring matrix is filled using block-based anti-diagonal wavefront
 * parallelism with threadgroup shared memory. Two Metal kernels cover the
 * upper-left and lower-right triangles of the block dependency graph.
 *
 * Usage:
 *   ./rodinia_nw [--sequence_length N] [--block_size B] [--penalty P]
 *
 *   --sequence_length N  Sequence length (matrix is NxN)  (default: 2048)
 *   --block_size B       Block size (threads per block)   (default: 16)
 *   --penalty P          Gap penalty                      (default: 2)
 *   --iterations I       Timed benchmark iterations       (default: 3)
 *
 * Output (stdout): CSV — kernel_name,problem_size,iterations,avg_ms,min_ms,max_ms,stddev_ms
 *                  + CSV metrics line: rodinia_nw,<N>,<GCUPS>
 * Output (stderr): GCUPS and effective GB/s
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
// CSV output
// ---------------------------------------------------------------------------

struct BenchResult {
    const char* kernel_name;
    const char* problem_size;
    int         iterations;
    double      avg_ms;
    double      min_ms;
    double      max_ms;
    double      stddev_ms;
};


// ---------------------------------------------------------------------------
// NWParams — must match the Metal struct definition
// ---------------------------------------------------------------------------
struct NWParams {
    int cols;
    int penalty;
    int block_idx;
    int num_blocks;
    int block_size;
    int phase;
    int pad0;
    int pad1;
};

// ---------------------------------------------------------------------------
// main
// ---------------------------------------------------------------------------
int main(int argc, char* argv[])
{
    @autoreleasepool {
    setvbuf(stdout, NULL, _IONBF, 0);

    // Defaults
    int seq_len    = 2048;
    int block_size = 16;
    int penalty    = 2;

    // Command-line fallback
    seq_len    = parseIntParam(argc, argv, "--sequence_length", seq_len);
    block_size = parseIntParam(argc, argv, "--block_size", block_size);
    penalty    = parseIntParam(argc, argv, "--penalty", penalty);

    // Environment variable overrides
    const char* env_val;
    env_val = getenv("BENCH_PARAM_sequence_length");
    if (env_val) seq_len = atoi(env_val);
    env_val = getenv("BENCH_PARAM_block_size");
    if (env_val) block_size = atoi(env_val);
    env_val = getenv("BENCH_PARAM_penalty");
    if (env_val) penalty = atoi(env_val);
    int num_warmup = 0;

    // Round up to multiple of block_size
    int num_blocks = (seq_len + block_size - 1) / block_size;
    int padded_len = num_blocks * block_size;

    int  cols        = padded_len + 1;
    int  rows        = padded_len + 1;
    long matrix_elems = (long)rows * cols;
    size_t seq_bytes    = (size_t)(padded_len + 1) * sizeof(int);
    size_t matrix_bytes = (size_t)matrix_elems * sizeof(int);

    // -------------------------------------------------------------------
    // Metal device setup
    // -------------------------------------------------------------------
    id<MTLDevice> device = MTLCreateSystemDefaultDevice();
    if (!device) {
        fprintf(stderr, "Error: no Metal device found\n");
        return EXIT_FAILURE;
    }
    fprintf(stderr, "Device: %s\n", device.name.UTF8String);
    fprintf(stderr, "Sequence length: %d  |  Block size: %d  |  Penalty: %d\n\n",
            seq_len, block_size, penalty);

    id<MTLCommandQueue> queue = [device newCommandQueue];

    // -------------------------------------------------------------------
    // Compile Metal shader from source file adjacent to the binary
    // -------------------------------------------------------------------
    NSError *err = nil;
    id<MTLLibrary> library = nil;

    NSString *exePath = [[NSBundle mainBundle] executablePath];
    NSString *dir     = [exePath stringByDeletingLastPathComponent];
    NSString *srcPath = [dir stringByAppendingPathComponent:@"rodinia_nw.metal"];

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
        fprintf(stderr, "Error: rodinia_nw.metal not found at %s\n",
                srcPath.UTF8String);
        return EXIT_FAILURE;
    }

    id<MTLFunction> fn1 = [library newFunctionWithName:@"nw_kernel1"];
    id<MTLFunction> fn2 = [library newFunctionWithName:@"nw_kernel2"];
    if (!fn1 || !fn2) {
        fprintf(stderr, "Error: NW kernel functions not found in Metal library\n");
        return EXIT_FAILURE;
    }

    id<MTLComputePipelineState> pso1 =
        [device newComputePipelineStateWithFunction:fn1 error:&err];
    id<MTLComputePipelineState> pso2 =
        [device newComputePipelineStateWithFunction:fn2 error:&err];
    if (!pso1 || !pso2) {
        fprintf(stderr, "Error creating pipeline: %s\n",
                err.localizedDescription.UTF8String);
        return EXIT_FAILURE;
    }

    // -------------------------------------------------------------------
    // Allocate buffers
    // -------------------------------------------------------------------
    id<MTLBuffer> buf_seq    = [device newBufferWithLength:seq_bytes
                                                   options:MTLResourceStorageModeShared];
    id<MTLBuffer> buf_matrix = [device newBufferWithLength:matrix_bytes
                                                   options:MTLResourceStorageModeShared];

    // Generate synthetic sequences
    int* h_seq = (int*)buf_seq.contents;
    srand(42);
    for (int i = 0; i <= padded_len; i++) {
        h_seq[i] = rand() % 10;
    }

    // Build the initial DP matrix (border only) in a separate host buffer
    // so we can reset it cheaply before each benchmark iteration
    std::vector<int> h_matrix_init(matrix_elems, 0);
    for (int i = 0; i < rows; i++) {
        h_matrix_init[(long)i * cols + 0] = -i * penalty;
    }
    for (int j = 0; j < cols; j++) {
        h_matrix_init[(long)0 * cols + j] = -j * penalty;
    }

    // Threadgroup shared memory size per block
    // Must match: (block_size+1)*(block_size+1) + block_size  elements of int
    size_t tg_shared = (size_t)((block_size + 1) * (block_size + 1) + block_size) * sizeof(int);

    // Helper: dispatch one NW kernel pass for a given diagonal
    auto dispatchKernel = [&](id<MTLComputePipelineState> pso,
                               id<MTLCommandBuffer> cb,
                               int diag,
                               int n_tgroups,
                               int phase)
    {
        NWParams params;
        params.cols       = cols;
        params.penalty    = penalty;
        params.block_idx  = diag;
        params.num_blocks = num_blocks;
        params.block_size = block_size;
        params.phase      = phase;
        params.pad0       = 0;
        params.pad1       = 0;

        id<MTLComputeCommandEncoder> enc = [cb computeCommandEncoder];
        [enc setComputePipelineState:pso];
        [enc setBuffer:buf_seq    offset:0 atIndex:0];
        [enc setBuffer:buf_matrix offset:0 atIndex:1];
        [enc setBytes:&params length:sizeof(params) atIndex:2];
        [enc setThreadgroupMemoryLength:tg_shared atIndex:0];
        // Dispatch: n_tgroups threadgroups, each of block_size threads
        [enc dispatchThreadgroups:MTLSizeMake((NSUInteger)n_tgroups, 1, 1)
             threadsPerThreadgroup:MTLSizeMake((NSUInteger)block_size, 1, 1)];
        [enc endEncoding];
        [cb commit];
        [cb waitUntilCompleted];
    };

    // Helper: run the full NW sweep (all diagonals)
    auto runNW = [&]() {
        // Reset matrix
        memcpy(buf_matrix.contents, h_matrix_init.data(), matrix_bytes);

        // Phase 1: upper-left triangle
        for (int diag = 0; diag < num_blocks; diag++) {
            int n = diag + 1;
            id<MTLCommandBuffer> cb = [queue commandBuffer];
            dispatchKernel(pso1, cb, diag, n, 0);
        }

        // Phase 2: lower-right triangle
        for (int diag = 0; diag < num_blocks - 1; diag++) {
            int n = num_blocks - 1 - diag;
            id<MTLCommandBuffer> cb = [queue commandBuffer];
            dispatchKernel(pso2, cb, diag, n, 1);
        }
    };

    // -------------------------------------------------------------------
    // Warmup
    // -------------------------------------------------------------------
    for (int w = 0; w < num_warmup; ++w) {
        runNW();
    }

    // -------------------------------------------------------------------
    // Timed iterations
    // -------------------------------------------------------------------
    std::vector<double> times(1);
    for (int iter = 0; iter < 1; ++iter) {
        uint64_t t0 = mach_absolute_time();
        runNW();
        uint64_t t1 = mach_absolute_time();
        times[iter] = ticks_to_ms(t1 - t0);
    }

    double sum = 0.0, mn = DBL_MAX, mx = 0.0;
    for (int i = 0; i < 1; ++i) {
        sum += times[i];
        if (times[i] < mn) mn = times[i];
        if (times[i] > mx) mx = times[i];
    }
    double avg = sum / 1;
    double variance = 0.0;
    for (int i = 0; i < 1; ++i) {
        double d = times[i] - avg;
        variance += d * d;
    }
    double stddev = (1 > 1) ? sqrt(variance / (1 - 1)) : 0.0;

    char problemSize[64];
    snprintf(problemSize, sizeof(problemSize), "%d", seq_len);

    BenchResult r;
    r.kernel_name  = "rodinia_nw";
    r.problem_size = problemSize;
    r.iterations = 1;
    r.avg_ms       = avg;
    r.min_ms       = mn;
    r.max_ms       = mx;
    r.stddev_ms    = stddev;

    // Performance metrics
    long cells   = (long)seq_len * seq_len;
    double gcups = (double)cells / (r.avg_ms * 1e-3) / 1e9;
    double eff_bw = (double)cells * 4 * sizeof(int) / (r.avg_ms * 1e-3) / 1e9;
    double total_ms = r.avg_ms;

    // JSON-lines kernel events
    printf("{\"type\":\"kernel\",\"name\":\"nw_kernel1\",\"time_ms\":%.6f,"
           "\"params\":{\"sequence_length\":%d,\"block_size\":%d,\"penalty\":%d}}\n",
           r.avg_ms, seq_len, block_size, penalty);

    printf("{\"type\":\"kernel\",\"name\":\"nw_kernel2\",\"time_ms\":%.6f,"
           "\"params\":{\"sequence_length\":%d,\"block_size\":%d,\"penalty\":%d}}\n",
           r.avg_ms, seq_len, block_size, penalty);

    // JSON-lines summary event
    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"gcups\",\"value\":%.2f},{\"name\":\"effective_gbps\",\"value\":%.2f}]}\n",
           total_ms, gcups, eff_bw);

    fprintf(stderr, "GCUPS: %.2f  |  Effective GB/s: %.2f  (avg %.4f ms)\n",
            gcups, eff_bw, r.avg_ms);

    return EXIT_SUCCESS;

    } // @autoreleasepool
}
