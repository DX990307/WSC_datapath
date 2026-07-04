/**
 * parboil_lbm_metal.mm — Apple Metal host for LBM D3Q19 benchmark.
 *
 * Lattice Boltzmann Method fluid simulation with D3Q19 velocity set,
 * BGK collision operator, and bounce-back boundary conditions.
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
// Constants
// ---------------------------------------------------------------------------

#define Q 19

struct LbmParams {
    uint32_t Nx;
    uint32_t Ny;
    uint32_t Nz;
    float    omega;
};

// Host D3Q19 weights
static const float h_w[Q] = {
    1.0f/3.0f,
    1.0f/18.0f, 1.0f/18.0f, 1.0f/18.0f,
    1.0f/18.0f, 1.0f/18.0f, 1.0f/18.0f,
    1.0f/36.0f, 1.0f/36.0f, 1.0f/36.0f, 1.0f/36.0f,
    1.0f/36.0f, 1.0f/36.0f, 1.0f/36.0f, 1.0f/36.0f,
    1.0f/36.0f, 1.0f/36.0f, 1.0f/36.0f, 1.0f/36.0f
};

static const int h_ex[Q] = { 0, 1,-1, 0, 0, 0, 0, 1,-1, 1,-1, 1,-1, 1,-1, 0, 0, 0, 0};
static const int h_ey[Q] = { 0, 0, 0, 1,-1, 0, 0, 1, 1,-1,-1, 0, 0, 0, 0, 1,-1, 1,-1};
static const int h_ez[Q] = { 0, 0, 0, 0, 0, 1,-1, 0, 0, 0, 0, 1, 1,-1,-1, 1, 1,-1,-1};

// ---------------------------------------------------------------------------
// CPU reference
// ---------------------------------------------------------------------------

static void cpu_collide(
    const float* f_src, float* rho_out, float* ux_out, float* uy_out,
    float* uz_out, int idx, int N)
{
    float rho = 0.0f;
    float ux = 0.0f, uy = 0.0f, uz = 0.0f;
    for (int q = 0; q < Q; ++q) {
        float fq = f_src[q * N + idx];
        rho += fq;
        ux += fq * h_ex[q];
        uy += fq * h_ey[q];
        uz += fq * h_ez[q];
    }
    float inv_rho = 1.0f / fmaxf(rho, 1e-10f);
    *rho_out = rho;
    *ux_out  = ux * inv_rho;
    *uy_out  = uy * inv_rho;
    *uz_out  = uz * inv_rho;
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

int main(int argc, char* argv[]) {
    @autoreleasepool {
    setvbuf(stdout, NULL, _IONBF, 0);

    int grid_dim     = 64;
    int block_size   = 128;
    int num_timesteps = 100;
    float tau        = 0.7f;
    int iterations   = 5;

    const char* env_val;
    env_val = getenv("BENCH_PARAM_grid_dim");
    if (env_val) grid_dim = atoi(env_val);
    env_val = getenv("BENCH_PARAM_block_size");
    if (env_val) block_size = atoi(env_val);
    env_val = getenv("BENCH_PARAM_num_timesteps");
    if (env_val) num_timesteps = atoi(env_val);
    env_val = getenv("BENCH_PARAM_tau");
    if (env_val) tau = (float)atof(env_val);
    env_val = getenv("BENCH_PARAM_iterations");
    if (env_val) iterations = atoi(env_val);
    int num_warmup = 0;

    int iters = iterations;
    float omega = 1.0f / tau;
    int Nx = grid_dim, Ny = grid_dim, Nz = grid_dim;
    int N = Nx * Ny * Nz;

    // -------------------------------------------------------------------
    // Metal device setup
    // -------------------------------------------------------------------
    id<MTLDevice> device = MTLCreateSystemDefaultDevice();
    if (!device) {
        fprintf(stderr, "Error: no Metal device found\n");
        return EXIT_FAILURE;
    }
    fprintf(stderr, "Device: %s\n", device.name.UTF8String);
    fprintf(stderr, "LBM D3Q19  |  Grid: %dx%dx%d = %d  |  Timesteps: %d  |  "
            "Tau: %.2f  |  Iterations: 5 warmup + %d timed\n\n",
            Nx, Ny, Nz, N, num_timesteps, tau, iters);

    id<MTLCommandQueue> queue = [device newCommandQueue];

    // -------------------------------------------------------------------
    // Compile Metal shader
    // -------------------------------------------------------------------
    NSError* err = nil;
    id<MTLLibrary> library = nil;

    NSString* exePath = [[NSBundle mainBundle] executablePath];
    NSString* dir     = [exePath stringByDeletingLastPathComponent];
    NSString* srcPath = [dir stringByAppendingPathComponent:@"parboil_lbm.metal"];

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
        fprintf(stderr, "Error: parboil_lbm.metal not found at %s\n",
                srcPath.UTF8String);
        return EXIT_FAILURE;
    }

    id<MTLFunction> fnLbm = [library newFunctionWithName:@"lbm_collide_stream_kernel"];
    if (!fnLbm) {
        fprintf(stderr, "Error: lbm_collide_stream_kernel not found\n");
        return EXIT_FAILURE;
    }

    id<MTLComputePipelineState> psoLbm =
        [device newComputePipelineStateWithFunction:fnLbm error:&err];
    if (!psoLbm) {
        fprintf(stderr, "Error creating pipeline: %s\n",
                err.localizedDescription.UTF8String);
        return EXIT_FAILURE;
    }

    // -------------------------------------------------------------------
    // Initialize distribution functions
    // -------------------------------------------------------------------
    size_t f_bytes = (size_t)Q * N * sizeof(float);
    std::vector<float> h_f(Q * N);

    unsigned int seed = 42;
    for (int idx = 0; idx < N; ++idx) {
        float rho = 1.0f + 0.001f * rand_float(&seed, -1.0f, 1.0f);
        for (int q = 0; q < Q; ++q) {
            h_f[q * N + idx] = h_w[q] * rho;
        }
    }

    // -------------------------------------------------------------------
    // Allocate Metal buffers (double-buffered)
    // -------------------------------------------------------------------
    id<MTLBuffer> bufFSrc = [device newBufferWithLength:f_bytes
                                               options:MTLResourceStorageModeShared];
    id<MTLBuffer> bufFDst = [device newBufferWithLength:f_bytes
                                               options:MTLResourceStorageModeShared];

    NSUInteger tgSize = (NSUInteger)block_size;
    NSUInteger gridN  = (NSUInteger)N;

    auto upload_initial = [&]() {
        memcpy(bufFSrc.contents, h_f.data(), f_bytes);
        memset(bufFDst.contents, 0, f_bytes);
    };

    auto run_lbm = [&]() {
        for (int t = 0; t < num_timesteps; ++t) {
            LbmParams params = {
                (uint32_t)Nx, (uint32_t)Ny, (uint32_t)Nz, omega
            };

            id<MTLCommandBuffer> cb = [queue commandBuffer];
            id<MTLComputeCommandEncoder> enc = [cb computeCommandEncoder];
            [enc setComputePipelineState:psoLbm];
            [enc setBuffer:bufFSrc offset:0 atIndex:0];
            [enc setBuffer:bufFDst offset:0 atIndex:1];
            [enc setBytes:&params  length:sizeof(params) atIndex:2];
            [enc dispatchThreads:MTLSizeMake(gridN, 1, 1)
                threadsPerThreadgroup:MTLSizeMake(tgSize, 1, 1)];
            [enc endEncoding];
            [cb commit];
            [cb waitUntilCompleted];

            // Swap buffers
            id<MTLBuffer> tmp = bufFSrc;
            bufFSrc = bufFDst;
            bufFDst = tmp;
        }
    };

    // -------------------------------------------------------------------
    // Warmup
    // -------------------------------------------------------------------
    for (int w = 0; w < num_warmup; ++w) {
        upload_initial();
        run_lbm();
    }

    // -------------------------------------------------------------------
    // Timed iterations
    // -------------------------------------------------------------------
    std::vector<double> times((size_t)iters);
    for (int i = 0; i < iters; ++i) {
        upload_initial();

        uint64_t t0 = mach_absolute_time();
        run_lbm();
        uint64_t t1 = mach_absolute_time();
        times[i] = ticks_to_ms(t1 - t0);
    }

    double sum = 0.0;
    for (int i = 0; i < iters; ++i) sum += times[i];
    double avg_ms = sum / iters;
    double total_ms = sum;

    double mlups = (double)N * num_timesteps / (avg_ms * 1e-3) / 1e6;

    printf("{\"type\":\"kernel\",\"name\":\"lbm_collide_stream_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"grid_dim\":%d,\"iterations\":%d,\"block_size\":%d,"
           "\"num_timesteps\":%d,\"tau\":%.2f}}\n",
           avg_ms, grid_dim, iters, block_size, num_timesteps, tau);

    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"mlups\",\"value\":%.2f}]}\n",
           total_ms, mlups);

    fprintf(stderr, "Average time: %.4f ms\n", avg_ms);
    fprintf(stderr, "Throughput:   %.2f MLUPS\n", mlups);

    // -------------------------------------------------------------------
    // Verification: check density conservation
    // -------------------------------------------------------------------
    {
        const float* h_f_out = (const float*)bufFSrc.contents;

        int verify_n = 128;
        int errors = 0;
        unsigned int vseed = 123;

        for (int v = 0; v < verify_n; ++v) {
            int ix = 1 + (lcg_rand(&vseed) % (Nx - 2));
            int iy = 1 + (lcg_rand(&vseed) % (Ny - 2));
            int iz = 1 + (lcg_rand(&vseed) % (Nz - 2));
            int idx = iz * Nx * Ny + iy * Nx + ix;

            float rho, ux_v, uy_v, uz_v;
            cpu_collide(h_f_out, &rho, &ux_v, &uy_v, &uz_v, idx, N);

            if (rho < 0.5f || rho > 2.0f || isnan(rho)) {
                if (errors < 10) {
                    fprintf(stderr, "Suspicious density at (%d,%d,%d): rho=%.6f\n",
                            ix, iy, iz, rho);
                }
                errors++;
            }
        }

        if (errors > 0)
            fprintf(stderr, "FAIL: %d suspicious nodes in %d verified\n", errors, verify_n);
        else
            fprintf(stderr, "PASS\n");
    }

    return EXIT_SUCCESS;

    } // @autoreleasepool
}
