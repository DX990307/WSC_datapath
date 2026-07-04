/**
 * rodinia_hotspot_metal.mm — Apple Metal host for the Rodinia Hotspot benchmark.
 *
 * Iterative 2D stencil thermal simulation for chip temperature estimation.
 * Runs multiple stencil time-steps per benchmark iteration (ping-pong buffers).
 *
 * Usage:
 *   ./rodinia_hotspot [--grid_size N] [--num_iterations K]
 *
 *   --grid_size N       Grid dimension (N×N)              (default: 512)
 *   --num_iterations K  Stencil time-steps per launch     (default: 10)
 *   --iterations I      Timed benchmark iterations        (default: 20)
 *
 * Output (stdout): CSV — kernel_name,problem_size,iterations,avg_ms,min_ms,max_ms,stddev_ms
 * Output (stderr): Effective bandwidth in GB/s
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
// Chip thermal constants (Rodinia defaults)
// ---------------------------------------------------------------------------
#define AMB_TEMP    80.0f
#define CHIP_HEIGHT 0.016f
#define CHIP_WIDTH  0.016f
#define T_CHIP      0.0005f
#define K_SI        100.0f
#define C_SI        1.75e6f

// ---------------------------------------------------------------------------
// main
// ---------------------------------------------------------------------------

int main(int argc, char* argv[])
{
    @autoreleasepool {
    setvbuf(stdout, NULL, _IONBF, 0);

    int grid_size      = parseIntParam(argc, argv, "--grid_size",     512);
    int num_iterations = parseIntParam(argc, argv, "--num_iterations", 10);
    int block_dim      = 16;

    const char* env_val;
    env_val = getenv("BENCH_PARAM_grid_size");
    if (env_val) grid_size = atoi(env_val);
    env_val = getenv("BENCH_PARAM_block_size");
    if (env_val) block_dim = atoi(env_val);
    env_val = getenv("BENCH_PARAM_num_iterations");
    if (env_val) num_iterations = atoi(env_val);
    int num_warmup = 0;

    int grid_rows   = grid_size;
    int grid_cols   = grid_size;
    int total_cells = grid_rows * grid_cols;
    size_t bytes    = (size_t)total_cells * sizeof(float);

    // Compute thermal parameters
    float grid_height  = CHIP_HEIGHT / grid_rows;
    float grid_width   = CHIP_WIDTH  / grid_cols;
    float cap          = C_SI * T_CHIP * grid_height * grid_width;
    float Rx           = grid_width  / (2.0f * K_SI * T_CHIP * grid_height);
    float Ry           = grid_height / (2.0f * K_SI * T_CHIP * grid_width);
    float Rz           = T_CHIP / (K_SI * grid_height * grid_width);
    float max_slope    = K_SI / (0.5f * T_CHIP * C_SI);
    float step         = 0.001f / max_slope;
    float step_div_cap = step / cap;
    float Rx_1         = 1.0f / Rx;
    float Ry_1         = 1.0f / Ry;
    float Rz_1         = 1.0f / Rz;

    // -------------------------------------------------------------------
    // Metal device setup
    // -------------------------------------------------------------------
    id<MTLDevice> device = MTLCreateSystemDefaultDevice();
    if (!device) {
        fprintf(stderr, "Error: no Metal device found\n");
        return EXIT_FAILURE;
    }
    fprintf(stderr, "Device: %s\n", device.name.UTF8String);
    fprintf(stderr, "Grid: %d×%d  |  Steps/launch: %d\n\n",
            grid_rows, grid_cols, num_iterations);

    id<MTLCommandQueue> queue = [device newCommandQueue];

    // -------------------------------------------------------------------
    // Compile Metal shader from source file next to the binary
    // -------------------------------------------------------------------
    NSError *err = nil;
    id<MTLLibrary> library = nil;

    NSString *exePath = [[NSBundle mainBundle] executablePath];
    NSString *dir     = [exePath stringByDeletingLastPathComponent];
    NSString *srcPath = [dir stringByAppendingPathComponent:@"rodinia_hotspot.metal"];

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
        fprintf(stderr, "Error: rodinia_hotspot.metal not found at %s\n",
                srcPath.UTF8String);
        return EXIT_FAILURE;
    }

    id<MTLFunction> fn = [library newFunctionWithName:@"hotspot_kernel"];
    if (!fn) {
        fprintf(stderr, "Error: kernel 'hotspot_kernel' not found\n");
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
    // Allocate buffers
    // -------------------------------------------------------------------
    id<MTLBuffer> buf_temp0 = [device newBufferWithLength:bytes
                                                  options:MTLResourceStorageModeShared];
    id<MTLBuffer> buf_temp1 = [device newBufferWithLength:bytes
                                                  options:MTLResourceStorageModeShared];
    id<MTLBuffer> buf_power = [device newBufferWithLength:bytes
                                                  options:MTLResourceStorageModeShared];

    // Generate synthetic temperature and power grids
    float* h_temp  = (float*)buf_temp0.contents;
    float* h_power = (float*)buf_power.contents;
    srand(42);
    for (int i = 0; i < total_cells; ++i) {
        h_temp[i]  = AMB_TEMP + (float)(rand() % 200) / 10.0f;
        h_power[i] = (float)(rand() % 100) / 500.0f;
    }

    // dims: {grid_cols, grid_rows, 0, 0}
    uint32_t dims_data[4]  = { (uint32_t)grid_cols, (uint32_t)grid_rows, 0, 0 };
    // therm: {step_div_cap, Rx_1, Ry_1, Rz_1}
    float    therm_data[4] = { step_div_cap, Rx_1, Ry_1, Rz_1 };

    // Thread dispatch: one thread per cell
    NSUInteger tg_x = 16, tg_y = 16;
    NSUInteger grid_x = (NSUInteger)((grid_cols + 15) / 16) * 16;
    NSUInteger grid_y = (NSUInteger)((grid_rows + 15) / 16) * 16;

    // Warmup
    for (int w = 0; w < num_warmup; ++w) {
        // Copy initial temps to buf_temp0
        memcpy(buf_temp0.contents, h_temp, bytes);

        id<MTLBuffer> src_buf = buf_temp0;
        id<MTLBuffer> dst_buf = buf_temp1;

        for (int k = 0; k < num_iterations; ++k) {
            id<MTLCommandBuffer>         cb  = [queue commandBuffer];
            id<MTLComputeCommandEncoder> enc = [cb computeCommandEncoder];
            [enc setComputePipelineState:pso];
            [enc setBuffer:src_buf  offset:0 atIndex:0];
            [enc setBuffer:dst_buf  offset:0 atIndex:1];
            [enc setBuffer:buf_power offset:0 atIndex:2];
            [enc setBytes:dims_data  length:sizeof(dims_data)  atIndex:3];
            [enc setBytes:therm_data length:sizeof(therm_data) atIndex:4];
            [enc dispatchThreads:MTLSizeMake(grid_x, grid_y, 1)
                threadsPerThreadgroup:MTLSizeMake(tg_x, tg_y, 1)];
            [enc endEncoding];
            [cb commit];
            [cb waitUntilCompleted];
            // Swap
            id<MTLBuffer> tmp = src_buf; src_buf = dst_buf; dst_buf = tmp;
        }
    }

    // -------------------------------------------------------------------
    // Timed iterations
    // -------------------------------------------------------------------
    std::vector<double> times(1);
    for (int iter = 0; iter < 1; ++iter) {
        // Reset temperature to initial state
        memcpy(buf_temp0.contents, h_temp, bytes);

        id<MTLBuffer> src_buf = buf_temp0;
        id<MTLBuffer> dst_buf = buf_temp1;

        uint64_t t0 = mach_absolute_time();

        for (int k = 0; k < num_iterations; ++k) {
            id<MTLCommandBuffer>         cb  = [queue commandBuffer];
            id<MTLComputeCommandEncoder> enc = [cb computeCommandEncoder];
            [enc setComputePipelineState:pso];
            [enc setBuffer:src_buf  offset:0 atIndex:0];
            [enc setBuffer:dst_buf  offset:0 atIndex:1];
            [enc setBuffer:buf_power offset:0 atIndex:2];
            [enc setBytes:dims_data  length:sizeof(dims_data)  atIndex:3];
            [enc setBytes:therm_data length:sizeof(therm_data) atIndex:4];
            [enc dispatchThreads:MTLSizeMake(grid_x, grid_y, 1)
                threadsPerThreadgroup:MTLSizeMake(tg_x, tg_y, 1)];
            [enc endEncoding];
            [cb commit];
            [cb waitUntilCompleted];
            // Swap
            id<MTLBuffer> tmp = src_buf; src_buf = dst_buf; dst_buf = tmp;
        }

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
    snprintf(problemSize, sizeof(problemSize), "%d", grid_size);

    BenchResult r;
    r.kernel_name  = "hotspot";
    r.problem_size = problemSize;
    r.iterations = 1;
    r.avg_ms       = avg;
    r.min_ms       = mn;
    r.max_ms       = mx;
    r.stddev_ms    = stddev;

    // Effective bandwidth per stencil step: 2 reads (temp_src, power) + 1 write
    double bytes_per_step = 3.0 * (double)total_cells * sizeof(float);
    double total_bytes    = bytes_per_step * num_iterations;
    double bw_gb = total_bytes / (r.avg_ms * 1e-3) / 1e9;
    fprintf(stderr, "Effective bandwidth: %.2f GB/s  (avg %.4f ms, %d steps/iter)\n",
            bw_gb, r.avg_ms, num_iterations);

    // JSON-lines output
    printf("{\"type\":\"kernel\",\"name\":\"hotspot_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"grid_size\":%d,\"block_size\":%d,\"num_iterations\":%d,"
           "}}\n",
           r.avg_ms, grid_size, block_dim, num_iterations);
    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"bandwidth_gbps\",\"value\":%.2f}]}\n",
           r.avg_ms, bw_gb);

    return EXIT_SUCCESS;

    } // @autoreleasepool
}
