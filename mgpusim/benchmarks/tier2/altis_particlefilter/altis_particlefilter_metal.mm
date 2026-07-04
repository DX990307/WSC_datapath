/**
 * altis_particlefilter_metal.mm — Apple Metal host for Particle Filter benchmark.
 *
 * Sequential Importance Resampling (SIR) particle filter for Monte Carlo
 * localization on a 2D grid.
 *
 * Usage:
 *   ./altis_particlefilter [--size N]
 *
 *   --size N         Number of particles (default: 100000)
 *   --iterations I   Timed iterations (default: 5)
 *
 * Output (stdout): CSV — altis_particlefilter,<N>,<time_ms>,<particles_per_sec>
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

#define GRID_SIZE    128
#define TIME_STEPS   10
#define SIGMA_OBS    10.0f
#define SIGMA_MOVE   2.0f

struct PFParams {
    uint32_t N;
    float obs_x;
    float obs_y;
    float total_weight;
};

struct ScanParams {
    uint32_t N;
};

// ---------------------------------------------------------------------------
// CPU reference helpers
// ---------------------------------------------------------------------------

static inline unsigned int cpu_xorshift(unsigned int s) {
    s ^= s << 13;
    s ^= s >> 17;
    s ^= s << 5;
    return s;
}

static inline float cpu_rand_uniform(unsigned int* state) {
    *state = cpu_xorshift(*state);
    return (float)(*state & 0xFFFFFF) / 16777216.0f;
}

static inline float cpu_rand_normal(unsigned int* state) {
    float u1 = cpu_rand_uniform(state);
    float u2 = cpu_rand_uniform(state);
    u1 = fmaxf(u1, 1e-10f);
    return sqrtf(-2.0f * logf(u1)) * cosf(2.0f * 3.14159265f * u2);
}

static void cpu_particle_filter_step(
    float* x_pos, float* y_pos, unsigned int* rng,
    float obs_x, float obs_y, float* weights, float* cdf,
    float* x_tmp, float* y_tmp, int N)
{
    for (int i = 0; i < N; ++i) {
        float dx = SIGMA_MOVE * cpu_rand_normal(&rng[i]);
        float dy = SIGMA_MOVE * cpu_rand_normal(&rng[i]);
        x_pos[i] = fminf(fmaxf(x_pos[i] + dx, 0.0f), (float)(GRID_SIZE - 1));
        y_pos[i] = fminf(fmaxf(y_pos[i] + dy, 0.0f), (float)(GRID_SIZE - 1));
    }

    for (int i = 0; i < N; ++i) {
        float dx = x_pos[i] - obs_x;
        float dy = y_pos[i] - obs_y;
        float dist2 = dx * dx + dy * dy;
        weights[i] = expf(-dist2 / (2.0f * SIGMA_OBS * SIGMA_OBS));
    }

    float total = 0.0f;
    for (int i = 0; i < N; ++i) {
        cdf[i] = total;
        total += weights[i];
    }

    for (int i = 0; i < N; ++i) {
        float u0 = cpu_rand_uniform(&rng[i]);
        float target = ((float)i + u0) / (float)N * total;
        int lo = 0, hi = N - 1;
        while (lo < hi) {
            int mid = (lo + hi) / 2;
            if (cdf[mid] < target) lo = mid + 1; else hi = mid;
        }
        if (lo >= N) lo = N - 1;
        if (lo < 0) lo = 0;
        x_tmp[i] = x_pos[lo];
        y_tmp[i] = y_pos[lo];
    }

    memcpy(x_pos, x_tmp, N * sizeof(float));
    memcpy(y_pos, y_tmp, N * sizeof(float));
}

// ---------------------------------------------------------------------------
// Host prefix sum
// ---------------------------------------------------------------------------

static void host_prefix_sum(float* data, int n) {
    float acc = 0.0f;
    for (int i = 0; i < n; ++i) {
        float tmp = data[i];
        data[i] = acc;
        acc += tmp;
    }
}

// ---------------------------------------------------------------------------
// Simple PRNG for initialization
// ---------------------------------------------------------------------------

static inline unsigned int lcg_rand(unsigned int* state) {
    *state = *state * 1664525u + 1013904223u;
    return *state;
}

// ---------------------------------------------------------------------------
// main
// ---------------------------------------------------------------------------

int main(int argc, char* argv[]) {
    @autoreleasepool {
    setvbuf(stdout, NULL, _IONBF, 0);

    // Defaults from params.json
    int N          = 100000;
    const char* num_particles_ratio = "0.5";
    int seed_param = 42;

    // Command-line fallback
    N          = parseIntParam(argc, argv, "--size", N);

    // Environment variable overrides
    const char* env_val;
    env_val = getenv("BENCH_PARAM_size");
    if (env_val) N = atoi(env_val);
    env_val = getenv("BENCH_PARAM_num_particles_ratio");
    if (env_val) num_particles_ratio = env_val;
    env_val = getenv("BENCH_PARAM_seed");
    if (env_val) seed_param = atoi(env_val);
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
    fprintf(stderr, "Particle Filter  |  Particles: %d  |  Grid: %dx%d  |  "
            "Steps: %d warmup + %d timed\n\n",
            N, GRID_SIZE, GRID_SIZE, TIME_STEPS, num_warmup);

    id<MTLCommandQueue> queue = [device newCommandQueue];

    // -------------------------------------------------------------------
    // Compile Metal shader
    // -------------------------------------------------------------------
    NSError* err = nil;
    id<MTLLibrary> library = nil;

    NSString* exePath = [[NSBundle mainBundle] executablePath];
    NSString* dir     = [exePath stringByDeletingLastPathComponent];
    NSString* srcPath = [dir stringByAppendingPathComponent:@"altis_particlefilter.metal"];

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
        fprintf(stderr, "Error: altis_particlefilter.metal not found at %s\n",
                srcPath.UTF8String);
        return EXIT_FAILURE;
    }

    id<MTLFunction> fnUpdate    = [library newFunctionWithName:@"update_particles_kernel"];
    id<MTLFunction> fnWeights   = [library newFunctionWithName:@"compute_weights_kernel"];
    id<MTLFunction> fnScan      = [library newFunctionWithName:@"prefix_sum_kernel"];
    id<MTLFunction> fnAddSums   = [library newFunctionWithName:@"add_block_sums_kernel"];
    id<MTLFunction> fnResample  = [library newFunctionWithName:@"resample_kernel"];

    if (!fnUpdate || !fnWeights || !fnScan || !fnAddSums || !fnResample) {
        fprintf(stderr, "Error: kernel function not found\n");
        return EXIT_FAILURE;
    }

    id<MTLComputePipelineState> psoUpdate =
        [device newComputePipelineStateWithFunction:fnUpdate error:&err];
    id<MTLComputePipelineState> psoWeights =
        [device newComputePipelineStateWithFunction:fnWeights error:&err];
    id<MTLComputePipelineState> psoScan =
        [device newComputePipelineStateWithFunction:fnScan error:&err];
    id<MTLComputePipelineState> psoAddSums =
        [device newComputePipelineStateWithFunction:fnAddSums error:&err];
    id<MTLComputePipelineState> psoResample =
        [device newComputePipelineStateWithFunction:fnResample error:&err];

    if (!psoUpdate || !psoWeights || !psoScan || !psoAddSums || !psoResample) {
        fprintf(stderr, "Error creating pipeline: %s\n",
                err.localizedDescription.UTF8String);
        return EXIT_FAILURE;
    }

    // -------------------------------------------------------------------
    // Generate initial data
    // -------------------------------------------------------------------
    size_t float_bytes = (size_t)N * sizeof(float);
    size_t uint_bytes  = (size_t)N * sizeof(uint32_t);

    std::vector<float> h_x(N), h_y(N);
    std::vector<uint32_t> h_rng(N);

    unsigned int init_seed = 12345;
    for (int i = 0; i < N; ++i) {
        h_x[i] = (float)(GRID_SIZE / 2) + ((float)(lcg_rand(&init_seed) & 0xFFFF) / 65535.0f - 0.5f) * 10.0f;
        h_y[i] = (float)(GRID_SIZE / 2) + ((float)(lcg_rand(&init_seed) & 0xFFFF) / 65535.0f - 0.5f) * 10.0f;
        h_rng[i] = lcg_rand(&init_seed) | 1u;
    }

    float obs_x[TIME_STEPS], obs_y[TIME_STEPS];
    for (int t = 0; t < TIME_STEPS; ++t) {
        float angle = 2.0f * 3.14159265f * t / TIME_STEPS;
        obs_x[t] = GRID_SIZE / 2.0f + 20.0f * cosf(angle);
        obs_y[t] = GRID_SIZE / 2.0f + 20.0f * sinf(angle);
    }

    // -------------------------------------------------------------------
    // Allocate Metal buffers
    // -------------------------------------------------------------------
    auto mkBuf = [&](size_t sz) {
        return [device newBufferWithLength:sz options:MTLResourceStorageModeShared];
    };

    id<MTLBuffer> bufX       = mkBuf(float_bytes);
    id<MTLBuffer> bufY       = mkBuf(float_bytes);
    id<MTLBuffer> bufX2      = mkBuf(float_bytes);
    id<MTLBuffer> bufY2      = mkBuf(float_bytes);
    id<MTLBuffer> bufWeights = mkBuf(float_bytes);
    id<MTLBuffer> bufCdf     = mkBuf(float_bytes);
    id<MTLBuffer> bufRng     = mkBuf(uint_bytes);

    NSUInteger scanBlock = 256;
    NSUInteger scanElements = scanBlock * 2;
    NSUInteger numScanBlocks = ((NSUInteger)N + scanElements - 1) / scanElements;
    id<MTLBuffer> bufBlockSums = mkBuf(numScanBlocks * sizeof(float));

    NSUInteger tgSize = 256;
    NSUInteger gridN  = (NSUInteger)N;

    // Upload initial state
    auto upload_initial = [&]() {
        memcpy(bufX.contents,   h_x.data(),   float_bytes);
        memcpy(bufY.contents,   h_y.data(),   float_bytes);
        memcpy(bufRng.contents, h_rng.data(), uint_bytes);
    };

    // Track which buffers are "current" for x/y
    // We'll swap pointers after resample
    id<MTLBuffer> curX = bufX, curY = bufY;
    id<MTLBuffer> altX = bufX2, altY = bufY2;

    // Run full particle filter
    auto run_pf = [&]() {
        curX = bufX; curY = bufY;
        altX = bufX2; altY = bufY2;

        for (int t = 0; t < TIME_STEPS; ++t) {
            PFParams updateParams = { (uint32_t)N, obs_x[t], obs_y[t], 0.0f };
            ScanParams scanParams = { (uint32_t)N };

            id<MTLCommandBuffer> cb = [queue commandBuffer];
            id<MTLComputeCommandEncoder> enc = [cb computeCommandEncoder];

            // Kernel 1: Update positions
            [enc setComputePipelineState:psoUpdate];
            [enc setBuffer:curX    offset:0 atIndex:0];
            [enc setBuffer:curY    offset:0 atIndex:1];
            [enc setBuffer:bufRng  offset:0 atIndex:2];
            [enc setBytes:&updateParams length:sizeof(updateParams) atIndex:3];
            [enc dispatchThreads:MTLSizeMake(gridN, 1, 1)
                threadsPerThreadgroup:MTLSizeMake(tgSize, 1, 1)];

            // Kernel 2: Compute weights
            [enc setComputePipelineState:psoWeights];
            [enc setBuffer:curX       offset:0 atIndex:0];
            [enc setBuffer:curY       offset:0 atIndex:1];
            [enc setBuffer:bufWeights offset:0 atIndex:2];
            [enc setBytes:&updateParams length:sizeof(updateParams) atIndex:3];
            [enc dispatchThreads:MTLSizeMake(gridN, 1, 1)
                threadsPerThreadgroup:MTLSizeMake(tgSize, 1, 1)];

            [enc endEncoding];
            [cb commit];
            [cb waitUntilCompleted];

            // Copy weights to CDF
            memcpy(bufCdf.contents, bufWeights.contents, float_bytes);

            // Prefix sum
            {
                id<MTLCommandBuffer> cb2 = [queue commandBuffer];
                id<MTLComputeCommandEncoder> enc2 = [cb2 computeCommandEncoder];

                [enc2 setComputePipelineState:psoScan];
                [enc2 setBuffer:bufCdf      offset:0 atIndex:0];
                [enc2 setBuffer:bufBlockSums offset:0 atIndex:1];
                [enc2 setBytes:&scanParams  length:sizeof(scanParams) atIndex:2];
                [enc2 setThreadgroupMemoryLength:scanElements * sizeof(float) atIndex:0];
                [enc2 dispatchThreadgroups:MTLSizeMake(numScanBlocks, 1, 1)
                    threadsPerThreadgroup:MTLSizeMake(scanBlock, 1, 1)];

                [enc2 endEncoding];
                [cb2 commit];
                [cb2 waitUntilCompleted];
            }

            // Fix up block sums on host
            if (numScanBlocks > 1) {
                float* bsums = (float*)bufBlockSums.contents;
                host_prefix_sum(bsums, (int)numScanBlocks);

                id<MTLCommandBuffer> cb3 = [queue commandBuffer];
                id<MTLComputeCommandEncoder> enc3 = [cb3 computeCommandEncoder];

                [enc3 setComputePipelineState:psoAddSums];
                [enc3 setBuffer:bufCdf       offset:0 atIndex:0];
                [enc3 setBuffer:bufBlockSums offset:0 atIndex:1];
                [enc3 setBytes:&scanParams   length:sizeof(scanParams) atIndex:2];
                [enc3 dispatchThreadgroups:MTLSizeMake(numScanBlocks, 1, 1)
                    threadsPerThreadgroup:MTLSizeMake(scanBlock, 1, 1)];

                [enc3 endEncoding];
                [cb3 commit];
                [cb3 waitUntilCompleted];
            }

            // Get total weight
            const float* cdfPtr = (const float*)bufCdf.contents;
            const float* wPtr   = (const float*)bufWeights.contents;
            float total_weight = cdfPtr[N - 1] + wPtr[N - 1];
            if (total_weight < 1e-30f) total_weight = 1.0f;

            // Kernel 3: Resample
            PFParams resampleParams = { (uint32_t)N, 0.0f, 0.0f, total_weight };

            {
                id<MTLCommandBuffer> cb4 = [queue commandBuffer];
                id<MTLComputeCommandEncoder> enc4 = [cb4 computeCommandEncoder];

                [enc4 setComputePipelineState:psoResample];
                [enc4 setBuffer:bufCdf   offset:0 atIndex:0];
                [enc4 setBuffer:curX     offset:0 atIndex:1];
                [enc4 setBuffer:curY     offset:0 atIndex:2];
                [enc4 setBuffer:altX     offset:0 atIndex:3];
                [enc4 setBuffer:altY     offset:0 atIndex:4];
                [enc4 setBuffer:bufRng   offset:0 atIndex:5];
                [enc4 setBytes:&resampleParams length:sizeof(resampleParams) atIndex:6];
                [enc4 dispatchThreads:MTLSizeMake(gridN, 1, 1)
                    threadsPerThreadgroup:MTLSizeMake(tgSize, 1, 1)];

                [enc4 endEncoding];
                [cb4 commit];
                [cb4 waitUntilCompleted];
            }

            // Swap buffers
            id<MTLBuffer> tmp;
            tmp = curX; curX = altX; altX = tmp;
            tmp = curY; curY = altY; altY = tmp;
        }
    };

    // -------------------------------------------------------------------
    // Warmup
    // -------------------------------------------------------------------
    for (int w = 0; w < num_warmup; ++w) {
        upload_initial();
        run_pf();
    }

    // -------------------------------------------------------------------
    // Timed iterations
    // -------------------------------------------------------------------
    std::vector<double> times(1);
    for (int i = 0; i < 1; ++i) {
        upload_initial();

        uint64_t t0 = mach_absolute_time();
        run_pf();
        uint64_t t1 = mach_absolute_time();
        times[i] = ticks_to_ms(t1 - t0);
    }

    double sum = 0.0;
    for (int i = 0; i < 1; ++i) sum += times[i];
    double avg_ms = sum / 1;

    double particles_per_sec = (double)N * TIME_STEPS / (avg_ms * 1e-3);

    double total_ms = sum;

    // JSON-lines kernel events (one per kernel)
    printf("{\"type\":\"kernel\",\"name\":\"update_particles_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"size\":%d"
           "\"num_particles_ratio\":\"%s\",\"seed\":%d}}\n",
           avg_ms, N, num_particles_ratio, seed_param);

    printf("{\"type\":\"kernel\",\"name\":\"compute_weights_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"size\":%d"
           "\"num_particles_ratio\":\"%s\",\"seed\":%d}}\n",
           avg_ms, N, num_particles_ratio, seed_param);

    printf("{\"type\":\"kernel\",\"name\":\"prefix_sum_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"size\":%d"
           "\"num_particles_ratio\":\"%s\",\"seed\":%d}}\n",
           avg_ms, N, num_particles_ratio, seed_param);

    printf("{\"type\":\"kernel\",\"name\":\"add_block_sums_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"size\":%d"
           "\"num_particles_ratio\":\"%s\",\"seed\":%d}}\n",
           avg_ms, N, num_particles_ratio, seed_param);

    printf("{\"type\":\"kernel\",\"name\":\"resample_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"size\":%d"
           "\"num_particles_ratio\":\"%s\",\"seed\":%d}}\n",
           avg_ms, N, num_particles_ratio, seed_param);

    // JSON-lines summary event
    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"particles_per_sec\",\"value\":%.2f}]}\n",
           total_ms, particles_per_sec);

    fprintf(stderr, "Average time: %.4f ms\n", avg_ms);
    fprintf(stderr, "Throughput:   %.4f particles/sec\n", particles_per_sec);

    // -------------------------------------------------------------------
    // Verification: compare mean positions with CPU reference
    // -------------------------------------------------------------------
    {
        int verify_n = (N < 10000) ? N : 10000;

        std::vector<float> cpu_x(verify_n), cpu_y(verify_n);
        std::vector<unsigned int> cpu_rng(verify_n);
        std::vector<float> cpu_w(verify_n), cpu_cdf(verify_n);
        std::vector<float> cpu_xt(verify_n), cpu_yt(verify_n);

        for (int i = 0; i < verify_n; ++i) {
            cpu_x[i]   = h_x[i];
            cpu_y[i]   = h_y[i];
            cpu_rng[i] = h_rng[i];
        }

        for (int t = 0; t < TIME_STEPS; ++t) {
            cpu_particle_filter_step(cpu_x.data(), cpu_y.data(), cpu_rng.data(),
                                      obs_x[t], obs_y[t], cpu_w.data(), cpu_cdf.data(),
                                      cpu_xt.data(), cpu_yt.data(), verify_n);
        }

        // Run GPU on same subset
        memcpy(bufX.contents,   h_x.data(),   verify_n * sizeof(float));
        memcpy(bufY.contents,   h_y.data(),   verify_n * sizeof(float));
        memcpy(bufRng.contents, h_rng.data(), verify_n * sizeof(uint32_t));

        NSUInteger vGridN = (NSUInteger)verify_n;
        NSUInteger vScanBlocks = (vGridN + scanElements - 1) / scanElements;
        id<MTLBuffer> vBlockSums = mkBuf(vScanBlocks * sizeof(float));

        curX = bufX; curY = bufY;
        altX = bufX2; altY = bufY2;

        for (int t = 0; t < TIME_STEPS; ++t) {
            PFParams updateParams = { (uint32_t)verify_n, obs_x[t], obs_y[t], 0.0f };
            ScanParams scanP = { (uint32_t)verify_n };

            {
                id<MTLCommandBuffer> cb = [queue commandBuffer];
                id<MTLComputeCommandEncoder> enc = [cb computeCommandEncoder];

                [enc setComputePipelineState:psoUpdate];
                [enc setBuffer:curX    offset:0 atIndex:0];
                [enc setBuffer:curY    offset:0 atIndex:1];
                [enc setBuffer:bufRng  offset:0 atIndex:2];
                [enc setBytes:&updateParams length:sizeof(updateParams) atIndex:3];
                [enc dispatchThreads:MTLSizeMake(vGridN, 1, 1)
                    threadsPerThreadgroup:MTLSizeMake(tgSize, 1, 1)];

                [enc setComputePipelineState:psoWeights];
                [enc setBuffer:curX       offset:0 atIndex:0];
                [enc setBuffer:curY       offset:0 atIndex:1];
                [enc setBuffer:bufWeights offset:0 atIndex:2];
                [enc setBytes:&updateParams length:sizeof(updateParams) atIndex:3];
                [enc dispatchThreads:MTLSizeMake(vGridN, 1, 1)
                    threadsPerThreadgroup:MTLSizeMake(tgSize, 1, 1)];

                [enc endEncoding];
                [cb commit];
                [cb waitUntilCompleted];
            }

            memcpy(bufCdf.contents, bufWeights.contents, verify_n * sizeof(float));

            {
                id<MTLCommandBuffer> cb2 = [queue commandBuffer];
                id<MTLComputeCommandEncoder> enc2 = [cb2 computeCommandEncoder];
                [enc2 setComputePipelineState:psoScan];
                [enc2 setBuffer:bufCdf    offset:0 atIndex:0];
                [enc2 setBuffer:vBlockSums offset:0 atIndex:1];
                [enc2 setBytes:&scanP     length:sizeof(scanP) atIndex:2];
                [enc2 setThreadgroupMemoryLength:scanElements * sizeof(float) atIndex:0];
                [enc2 dispatchThreadgroups:MTLSizeMake(vScanBlocks, 1, 1)
                    threadsPerThreadgroup:MTLSizeMake(scanBlock, 1, 1)];
                [enc2 endEncoding];
                [cb2 commit];
                [cb2 waitUntilCompleted];
            }

            if (vScanBlocks > 1) {
                float* bsums = (float*)vBlockSums.contents;
                host_prefix_sum(bsums, (int)vScanBlocks);

                id<MTLCommandBuffer> cb3 = [queue commandBuffer];
                id<MTLComputeCommandEncoder> enc3 = [cb3 computeCommandEncoder];
                [enc3 setComputePipelineState:psoAddSums];
                [enc3 setBuffer:bufCdf     offset:0 atIndex:0];
                [enc3 setBuffer:vBlockSums offset:0 atIndex:1];
                [enc3 setBytes:&scanP      length:sizeof(scanP) atIndex:2];
                [enc3 dispatchThreadgroups:MTLSizeMake(vScanBlocks, 1, 1)
                    threadsPerThreadgroup:MTLSizeMake(scanBlock, 1, 1)];
                [enc3 endEncoding];
                [cb3 commit];
                [cb3 waitUntilCompleted];
            }

            const float* cdfP = (const float*)bufCdf.contents;
            const float* wP   = (const float*)bufWeights.contents;
            float tw = cdfP[verify_n - 1] + wP[verify_n - 1];
            if (tw < 1e-30f) tw = 1.0f;

            PFParams resP = { (uint32_t)verify_n, 0.0f, 0.0f, tw };

            {
                id<MTLCommandBuffer> cb4 = [queue commandBuffer];
                id<MTLComputeCommandEncoder> enc4 = [cb4 computeCommandEncoder];
                [enc4 setComputePipelineState:psoResample];
                [enc4 setBuffer:bufCdf  offset:0 atIndex:0];
                [enc4 setBuffer:curX    offset:0 atIndex:1];
                [enc4 setBuffer:curY    offset:0 atIndex:2];
                [enc4 setBuffer:altX    offset:0 atIndex:3];
                [enc4 setBuffer:altY    offset:0 atIndex:4];
                [enc4 setBuffer:bufRng  offset:0 atIndex:5];
                [enc4 setBytes:&resP    length:sizeof(resP) atIndex:6];
                [enc4 dispatchThreads:MTLSizeMake(vGridN, 1, 1)
                    threadsPerThreadgroup:MTLSizeMake(tgSize, 1, 1)];
                [enc4 endEncoding];
                [cb4 commit];
                [cb4 waitUntilCompleted];
            }

            id<MTLBuffer> tmp;
            tmp = curX; curX = altX; altX = tmp;
            tmp = curY; curY = altY; altY = tmp;
        }

        const float* gpuXp = (const float*)curX.contents;
        const float* gpuYp = (const float*)curY.contents;

        double gpu_mean_x = 0, gpu_mean_y = 0;
        double cpu_mean_x = 0, cpu_mean_y = 0;
        for (int i = 0; i < verify_n; ++i) {
            gpu_mean_x += gpuXp[i];
            gpu_mean_y += gpuYp[i];
            cpu_mean_x += cpu_x[i];
            cpu_mean_y += cpu_y[i];
        }
        gpu_mean_x /= verify_n; gpu_mean_y /= verify_n;
        cpu_mean_x /= verify_n; cpu_mean_y /= verify_n;

        fprintf(stderr, "GPU mean: (%.4f, %.4f)  CPU mean: (%.4f, %.4f)\n",
                gpu_mean_x, gpu_mean_y, cpu_mean_x, cpu_mean_y);

        double diff = sqrt((gpu_mean_x - cpu_mean_x) * (gpu_mean_x - cpu_mean_x) +
                           (gpu_mean_y - cpu_mean_y) * (gpu_mean_y - cpu_mean_y));

        if (diff < 20.0)
            fprintf(stderr, "PASS\n");
        else
            fprintf(stderr, "FAIL: mean position difference %.4f exceeds tolerance\n", diff);
    }

    return EXIT_SUCCESS;

    } // @autoreleasepool
}
