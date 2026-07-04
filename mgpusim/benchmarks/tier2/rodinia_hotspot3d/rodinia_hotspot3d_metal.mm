/**
 * rodinia_hotspot3d_metal.mm — Apple Metal host for the Rodinia HotSpot3D benchmark.
 *
 * 3D stencil thermal simulation extending the 2D HotSpot benchmark.
 * Runs multiple stencil time-steps per benchmark iteration (ping-pong buffers).
 *
 * Output (stdout): JSON-lines
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
#include <cfloat>
#include <vector>
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

static float parseFloatParam(int argc, char** argv, const char* name, float defaultVal) {
    for (int i = 1; i < argc - 1; ++i) {
        if (strcmp(argv[i], name) == 0) {
            float v = (float)atof(argv[i + 1]);
            if (v > 0.0f) return v;
            break;
        }
    }
    return defaultVal;
}

// ---------------------------------------------------------------------------
// Chip thermal constants (Rodinia defaults, extended to 3D)
// ---------------------------------------------------------------------------
#define CHIP_HEIGHT 0.016f
#define CHIP_WIDTH  0.016f
#define CHIP_DEPTH  0.016f
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

    // Defaults from params.json
    int grid_size      = 64;
    int block_dim      = 8;
    int num_iterations = 10;
    float amb_temp     = 80.0f;

    // Command-line fallback
    grid_size      = parseIntParam(argc, argv, "--grid_size", grid_size);
    block_dim      = parseIntParam(argc, argv, "--block_size", block_dim);
    num_iterations = parseIntParam(argc, argv, "--num_iterations", num_iterations);
    amb_temp       = parseFloatParam(argc, argv, "--amb_temp", amb_temp);

    // Environment variable overrides
    const char* env_val;
    env_val = getenv("BENCH_PARAM_grid_size");
    if (env_val) grid_size = atoi(env_val);
    env_val = getenv("BENCH_PARAM_block_size");
    if (env_val) block_dim = atoi(env_val);
    env_val = getenv("BENCH_PARAM_num_iterations");
    if (env_val) num_iterations = atoi(env_val);
    env_val = getenv("BENCH_PARAM_amb_temp");
    if (env_val) amb_temp = (float)atof(env_val);
    int num_warmup = 0;

    int nx = grid_size, ny = grid_size, nz = grid_size;
    long long total_cells = (long long)nx * ny * nz;
    size_t bytes = (size_t)total_cells * sizeof(float);

    // Compute thermal parameters for 3D
    float dx = CHIP_WIDTH  / nx;
    float dy = CHIP_HEIGHT / ny;
    float dz = CHIP_DEPTH  / nz;

    float cap       = C_SI * T_CHIP * dx * dy;
    float Rx        = dx / (2.0f * K_SI * T_CHIP * dy);
    float Ry        = dy / (2.0f * K_SI * T_CHIP * dx);
    float Rz        = dz / (2.0f * K_SI * T_CHIP * dx);
    float Ra        = T_CHIP / (K_SI * dx * dy);
    float max_slope = K_SI / (0.5f * T_CHIP * C_SI);
    float step      = 0.001f / max_slope;
    float step_div_cap = step / cap;
    float Rx_1      = 1.0f / Rx;
    float Ry_1      = 1.0f / Ry;
    float Rz_1      = 1.0f / Rz;
    float Ra_1      = 1.0f / Ra;

    // -------------------------------------------------------------------
    // Metal device setup
    // -------------------------------------------------------------------
    id<MTLDevice> device = MTLCreateSystemDefaultDevice();
    if (!device) {
        fprintf(stderr, "Error: no Metal device found\n");
        return EXIT_FAILURE;
    }
    fprintf(stderr, "Device: %s\n", device.name.UTF8String);
    fprintf(stderr, "Grid: %d×%d×%d  |  Block: %d³  |  Steps/launch: %d\n\n",
            nx, ny, nz, block_dim, num_iterations);

    id<MTLCommandQueue> queue = [device newCommandQueue];

    // -------------------------------------------------------------------
    // Compile Metal shader from source file next to the binary
    // -------------------------------------------------------------------
    NSError *err = nil;
    id<MTLLibrary> library = nil;

    NSString *exePath = [[NSBundle mainBundle] executablePath];
    NSString *dir     = [exePath stringByDeletingLastPathComponent];
    NSString *srcPath = [dir stringByAppendingPathComponent:@"rodinia_hotspot3d.metal"];

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
        fprintf(stderr, "Error: rodinia_hotspot3d.metal not found at %s\n",
                srcPath.UTF8String);
        return EXIT_FAILURE;
    }

    id<MTLFunction> fn = [library newFunctionWithName:@"hotspot3d_kernel"];
    if (!fn) {
        fprintf(stderr, "Error: kernel 'hotspot3d_kernel' not found\n");
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
    for (long long i = 0; i < total_cells; ++i) {
        h_temp[i]  = amb_temp + (float)(rand() % 200) / 10.0f;
        h_power[i] = (float)(rand() % 100) / 500.0f;
    }

    // Save initial temperature for reset
    std::vector<float> h_temp_init(h_temp, h_temp + total_cells);

    // dims: {nx, ny, nz, 0}
    uint32_t dims_data[4]   = { (uint32_t)nx, (uint32_t)ny, (uint32_t)nz, 0 };
    // therm: {step_div_cap, Rx_1, Ry_1, Rz_1}
    float    therm_data[4]  = { step_div_cap, Rx_1, Ry_1, Rz_1 };
    // therm2: {Ra_1, amb_temp, 0, 0}
    float    therm2_data[4] = { Ra_1, amb_temp, 0.0f, 0.0f };

    // Thread dispatch: one thread per cell
    NSUInteger tg = (NSUInteger)block_dim;
    NSUInteger gx = (NSUInteger)(((nx + block_dim - 1) / block_dim) * block_dim);
    NSUInteger gy = (NSUInteger)(((ny + block_dim - 1) / block_dim) * block_dim);
    NSUInteger gz = (NSUInteger)(((nz + block_dim - 1) / block_dim) * block_dim);

    // Helper: run one full set of stencil steps
    auto run_stencil = [&](id<MTLBuffer> src_buf, id<MTLBuffer> dst_buf) {
        for (int k = 0; k < num_iterations; ++k) {
            id<MTLCommandBuffer>         cb  = [queue commandBuffer];
            id<MTLComputeCommandEncoder> enc = [cb computeCommandEncoder];
            [enc setComputePipelineState:pso];
            [enc setBuffer:src_buf   offset:0 atIndex:0];
            [enc setBuffer:dst_buf   offset:0 atIndex:1];
            [enc setBuffer:buf_power offset:0 atIndex:2];
            [enc setBytes:dims_data   length:sizeof(dims_data)   atIndex:3];
            [enc setBytes:therm_data  length:sizeof(therm_data)  atIndex:4];
            [enc setBytes:therm2_data length:sizeof(therm2_data) atIndex:5];
            [enc dispatchThreads:MTLSizeMake(gx, gy, gz)
                threadsPerThreadgroup:MTLSizeMake(tg, tg, tg)];
            [enc endEncoding];
            [cb commit];
            [cb waitUntilCompleted];
            // Swap
            id<MTLBuffer> tmp = src_buf; src_buf = dst_buf; dst_buf = tmp;
        }
    };

    // Warmup
    for (int w = 0; w < num_warmup; ++w) {
        memcpy(buf_temp0.contents, h_temp_init.data(), bytes);
        run_stencil(buf_temp0, buf_temp1);
    }

    // -------------------------------------------------------------------
    // Timed iterations
    // -------------------------------------------------------------------
    std::vector<double> times(1);
    for (int iter = 0; iter < 1; ++iter) {
        memcpy(buf_temp0.contents, h_temp_init.data(), bytes);

        uint64_t t0 = mach_absolute_time();
        run_stencil(buf_temp0, buf_temp1);
        uint64_t t1 = mach_absolute_time();

        times[iter] = ticks_to_ms(t1 - t0);
    }

    double sum = 0.0;
    for (int i = 0; i < 1; ++i) sum += times[i];
    double avg_ms = sum / 1;
    double total_ms = sum;

    // Effective bandwidth: 2 reads (temp_src, power) + 1 write (temp_dst) per step
    double bytes_per_step = 3.0 * (double)total_cells * sizeof(float);
    double total_bytes    = bytes_per_step * num_iterations;
    double bw_gb = total_bytes / (avg_ms * 1e-3) / 1e9;

    fprintf(stderr, "Effective bandwidth: %.2f GB/s  (avg %.4f ms, %d steps/iter)\n",
            bw_gb, avg_ms, num_iterations);

    // JSON-lines output
    printf("{\"type\":\"kernel\",\"name\":\"hotspot3d_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"grid_size\":%d,\"block_size\":%d,\"num_iterations\":%d,"
           "\"amb_temp\":%.1f}}\n",
           avg_ms, grid_size, block_dim, num_iterations, amb_temp);

    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"bandwidth_gbps\",\"value\":%.2f}]}\n",
           total_ms, bw_gb);

    return EXIT_SUCCESS;

    } // @autoreleasepool
}
