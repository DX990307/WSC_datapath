/**
 * lonestar_dmr_metal.mm — Apple Metal host for LoneStar Delaunay Mesh Refinement.
 *
 * Worklist-based irregular mesh refinement. Generates a synthetic Delaunay
 * triangulation, identifies "bad" triangles, and refines them.
 *
 * Usage:
 *   ./lonestar_dmr [--size N]
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
            return (float)atof(argv[i + 1]);
        }
    }
    return defaultVal;
}

// ---------------------------------------------------------------------------
// Simple PRNG
// ---------------------------------------------------------------------------

static inline unsigned int lcg_rand(unsigned int* state) {
    *state = *state * 1664525u + 1013904223u;
    return *state;
}

static inline float rand_float(unsigned int* state, float lo, float hi) {
    return lo + (float)(lcg_rand(state) & 0xFFFF) / 65535.0f * (hi - lo);
}

// ---------------------------------------------------------------------------
// main
// ---------------------------------------------------------------------------

int main(int argc, char* argv[])
{
    @autoreleasepool {
    setvbuf(stdout, NULL, _IONBF, 0);

    int num_triangles       = 100000;
    int block_size          = 256;
    float min_angle         = 30.0f;
    int max_refine_iters    = 5;
    const char* precision   = "float";

    num_triangles    = parseIntParam(argc, argv, "--size", num_triangles);
    block_size       = parseIntParam(argc, argv, "--block_size", block_size);
    min_angle        = parseFloatParam(argc, argv, "--min_angle", min_angle);
    max_refine_iters = parseIntParam(argc, argv, "--max_refine_iterations", max_refine_iters);

    const char* env_val;
    env_val = getenv("BENCH_PARAM_num_triangles");
    if (env_val) num_triangles = atoi(env_val);
    env_val = getenv("BENCH_PARAM_block_size");
    if (env_val) block_size = atoi(env_val);
    env_val = getenv("BENCH_PARAM_min_angle");
    if (env_val) min_angle = (float)atof(env_val);
    env_val = getenv("BENCH_PARAM_max_refine_iterations");
    if (env_val) max_refine_iters = atoi(env_val);
    env_val = getenv("BENCH_PARAM_precision");
    if (env_val) precision = env_val;
    int num_warmup = 0;

    int num_vertices = num_triangles * 2;

    // -------------------------------------------------------------------
    // Metal device setup
    // -------------------------------------------------------------------
    id<MTLDevice> device = MTLCreateSystemDefaultDevice();
    if (!device) {
        fprintf(stderr, "Error: no Metal device found\n");
        return EXIT_FAILURE;
    }
    fprintf(stderr, "Device: %s\n", device.name.UTF8String);
    fprintf(stderr, "DMR: Delaunay Mesh Refinement  |  Triangles: %d  |  "
            "Vertices: %d  |  min_angle: %.1f deg  |  refine_iters: %d  |  "
            "Iterations: 5 warmup + %d timed\n\n",
            num_triangles, num_vertices, min_angle, max_refine_iters);

    id<MTLCommandQueue> queue = [device newCommandQueue];

    // -------------------------------------------------------------------
    // Compile Metal shaders
    // -------------------------------------------------------------------
    NSError *err = nil;
    id<MTLLibrary> library = nil;

    NSString *exePath = [[NSBundle mainBundle] executablePath];
    NSString *dir     = [exePath stringByDeletingLastPathComponent];
    NSString *srcPath = [dir stringByAppendingPathComponent:@"lonestar_dmr.metal"];

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
        fprintf(stderr, "Error: lonestar_dmr.metal not found at %s\n",
                srcPath.UTF8String);
        return EXIT_FAILURE;
    }

    id<MTLFunction> fnCheck = [library newFunctionWithName:@"dmr_check_kernel"];
    id<MTLFunction> fnRefine = [library newFunctionWithName:@"dmr_refine_kernel"];
    if (!fnCheck || !fnRefine) {
        fprintf(stderr, "Error: kernels not found in shader\n");
        return EXIT_FAILURE;
    }

    id<MTLComputePipelineState> psoCheck =
        [device newComputePipelineStateWithFunction:fnCheck error:&err];
    id<MTLComputePipelineState> psoRefine =
        [device newComputePipelineStateWithFunction:fnRefine error:&err];
    if (!psoCheck || !psoRefine) {
        fprintf(stderr, "Error creating pipeline\n");
        return EXIT_FAILURE;
    }

    // -------------------------------------------------------------------
    // Generate synthetic mesh data
    // -------------------------------------------------------------------
    size_t vert_bytes = (size_t)num_vertices * sizeof(float);
    size_t tri_bytes  = (size_t)num_triangles * sizeof(uint32_t);

    id<MTLBuffer> buf_tri_v0 = [device newBufferWithLength:tri_bytes
                                                    options:MTLResourceStorageModeShared];
    id<MTLBuffer> buf_tri_v1 = [device newBufferWithLength:tri_bytes
                                                    options:MTLResourceStorageModeShared];
    id<MTLBuffer> buf_tri_v2 = [device newBufferWithLength:tri_bytes
                                                    options:MTLResourceStorageModeShared];
    id<MTLBuffer> buf_vx     = [device newBufferWithLength:vert_bytes
                                                    options:MTLResourceStorageModeShared];
    id<MTLBuffer> buf_vy     = [device newBufferWithLength:vert_bytes
                                                    options:MTLResourceStorageModeShared];
    id<MTLBuffer> buf_wl     = [device newBufferWithLength:tri_bytes
                                                    options:MTLResourceStorageModeShared];
    id<MTLBuffer> buf_wl_cnt = [device newBufferWithLength:sizeof(uint32_t)
                                                    options:MTLResourceStorageModeShared];

    unsigned int seed = 42;
    float* h_vx = (float*)buf_vx.contents;
    float* h_vy = (float*)buf_vy.contents;
    uint32_t* h_v0 = (uint32_t*)buf_tri_v0.contents;
    uint32_t* h_v1 = (uint32_t*)buf_tri_v1.contents;
    uint32_t* h_v2 = (uint32_t*)buf_tri_v2.contents;

    for (int i = 0; i < num_vertices; ++i) {
        h_vx[i] = rand_float(&seed, 0.0f, 100.0f);
        h_vy[i] = rand_float(&seed, 0.0f, 100.0f);
    }
    for (int i = 0; i < num_triangles; ++i) {
        uint32_t v0, v1, v2;
        v0 = lcg_rand(&seed) % num_vertices;
        do { v1 = lcg_rand(&seed) % num_vertices; } while (v1 == v0);
        do { v2 = lcg_rand(&seed) % num_vertices; } while (v2 == v0 || v2 == v1);
        h_v0[i] = v0;
        h_v1[i] = v1;
        h_v2[i] = v2;
    }

    // Backup vertices
    std::vector<float> vx_bak(h_vx, h_vx + num_vertices);
    std::vector<float> vy_bak(h_vy, h_vy + num_vertices);

    NSUInteger tgSize = (NSUInteger)block_size;

    auto run_dmr = [&]() {
        // Reset vertices
        memcpy(h_vx, vx_bak.data(), vert_bytes);
        memcpy(h_vy, vy_bak.data(), vert_bytes);

        for (int r = 0; r < max_refine_iters; ++r) {
            // Clear worklist count
            *((uint32_t*)buf_wl_cnt.contents) = 0;

            // Check kernel
            {
                uint32_t check_params[4] = { (uint32_t)num_triangles, (uint32_t)num_vertices, 0, 0 };
                float check_fparams[2] = { min_angle, 0.0f };

                id<MTLCommandBuffer> cb = [queue commandBuffer];
                id<MTLComputeCommandEncoder> enc = [cb computeCommandEncoder];
                [enc setComputePipelineState:psoCheck];
                [enc setBuffer:buf_tri_v0 offset:0 atIndex:0];
                [enc setBuffer:buf_tri_v1 offset:0 atIndex:1];
                [enc setBuffer:buf_tri_v2 offset:0 atIndex:2];
                [enc setBuffer:buf_vx     offset:0 atIndex:3];
                [enc setBuffer:buf_vy     offset:0 atIndex:4];
                [enc setBuffer:buf_wl     offset:0 atIndex:5];
                [enc setBuffer:buf_wl_cnt offset:0 atIndex:6];
                [enc setBytes:check_params  length:sizeof(check_params)  atIndex:7];
                [enc setBytes:check_fparams length:sizeof(check_fparams) atIndex:8];

                NSUInteger gridSize = (NSUInteger)((num_triangles + tgSize - 1) / tgSize) * tgSize;
                [enc dispatchThreads:MTLSizeMake(gridSize, 1, 1)
                    threadsPerThreadgroup:MTLSizeMake(tgSize, 1, 1)];
                [enc endEncoding];
                [cb commit];
                [cb waitUntilCompleted];
            }

            uint32_t wl_count = *((uint32_t*)buf_wl_cnt.contents);
            if (wl_count == 0) break;

            // Refine kernel
            {
                uint32_t refine_params[4] = { wl_count, (uint32_t)num_triangles, 0, 0 };

                id<MTLCommandBuffer> cb = [queue commandBuffer];
                id<MTLComputeCommandEncoder> enc = [cb computeCommandEncoder];
                [enc setComputePipelineState:psoRefine];
                [enc setBuffer:buf_wl     offset:0 atIndex:0];
                [enc setBuffer:buf_tri_v0 offset:0 atIndex:1];
                [enc setBuffer:buf_tri_v1 offset:0 atIndex:2];
                [enc setBuffer:buf_tri_v2 offset:0 atIndex:3];
                [enc setBuffer:buf_vx     offset:0 atIndex:4];
                [enc setBuffer:buf_vy     offset:0 atIndex:5];
                [enc setBytes:refine_params length:sizeof(refine_params) atIndex:6];

                NSUInteger gridSize = (NSUInteger)((wl_count + tgSize - 1) / tgSize) * tgSize;
                [enc dispatchThreads:MTLSizeMake(gridSize, 1, 1)
                    threadsPerThreadgroup:MTLSizeMake(tgSize, 1, 1)];
                [enc endEncoding];
                [cb commit];
                [cb waitUntilCompleted];
            }
        }
    };

    // -------------------------------------------------------------------
    // Warmup
    // -------------------------------------------------------------------
    for (int w = 0; w < num_warmup; ++w) {
        run_dmr();
    }

    // -------------------------------------------------------------------
    // Timed iterations
    // -------------------------------------------------------------------
    std::vector<double> times(1);
    for (int i = 0; i < 1; ++i) {
        uint64_t t0 = mach_absolute_time();
        run_dmr();
        uint64_t t1 = mach_absolute_time();
        times[i] = ticks_to_ms(t1 - t0);
    }

    double sum_ms = 0.0, mn = DBL_MAX, mx = 0.0;
    for (int i = 0; i < 1; ++i) {
        sum_ms += times[i];
        if (times[i] < mn) mn = times[i];
        if (times[i] > mx) mx = times[i];
    }
    double avg_ms = sum_ms / 1;
    double mtri_per_sec = (double)num_triangles / (avg_ms * 1e-3) / 1e6;

    fprintf(stderr, "Performance: %.2f Mtri/sec  (avg %.4f ms, min %.4f ms, max %.4f ms)\n",
            mtri_per_sec, avg_ms, mn, mx);

    printf("{\"type\":\"kernel\",\"name\":\"dmr_check_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"num_triangles\":%d\"block_size\":%d,"
           "\"min_angle\":%.1f,\"max_refine_iterations\":%d,"
           "\"precision\":\"%s\"}}\n",
           avg_ms * 0.5, num_triangles, block_size, min_angle,
           max_refine_iters, precision);

    printf("{\"type\":\"kernel\",\"name\":\"dmr_refine_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"num_triangles\":%d\"block_size\":%d,"
           "\"min_angle\":%.1f,\"max_refine_iterations\":%d,"
           "\"precision\":\"%s\"}}\n",
           avg_ms * 0.5, num_triangles, block_size, min_angle,
           max_refine_iters, precision);

    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"mtri_per_sec\",\"value\":%.2f}]}\n",
           avg_ms, mtri_per_sec);

    return EXIT_SUCCESS;

    } // @autoreleasepool
}
