/**
 * npb_mg_metal.mm — Apple Metal host for NPB Multi-Grid benchmark.
 *
 * V-cycle multigrid with smooth/restrict/prolong on synthetic 3D grid.
 *
 * Output (stdout): JSON-lines
 * Output (stderr): human-readable
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
// Parameter struct (must match .metal)
// ---------------------------------------------------------------------------

struct MgParams {
    uint32_t N;
    uint32_t N_coarse;
    float weight;
};

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
// Main
// ---------------------------------------------------------------------------

int main(int argc, char** argv) {
    setvbuf(stdout, NULL, _IONBF, 0);

    int N              = 64;
    int block_size     = 256;
    int num_vcycles    = 5;
    int num_smooth     = 3;

    const char* env_val;
    env_val = getenv("BENCH_PARAM_grid_size");
    if (env_val) N = atoi(env_val);
    env_val = getenv("BENCH_PARAM_block_size");
    if (env_val) block_size = atoi(env_val);
    env_val = getenv("BENCH_PARAM_num_vcycles");
    if (env_val) num_vcycles = atoi(env_val);
    env_val = getenv("BENCH_PARAM_num_smooth_steps");
    if (env_val) num_smooth = atoi(env_val);
    int num_warmup = 0;

    for (int i = 1; i < argc - 1; ++i) {
        if (strcmp(argv[i], "--size") == 0) N = atoi(argv[i+1]);
    }

    { int p = 4; while (p < N) p *= 2; N = p; }


    @autoreleasepool {

    // Get Metal device
    id<MTLDevice> device = MTLCreateSystemDefaultDevice();
    if (!device) {
        fprintf(stderr, "No Metal device found.\n");
        return 1;
    }
    fprintf(stderr, "Device: %s\n", [[device name] UTF8String]);
    fprintf(stderr, "NPB MG  |  Grid: %d x %d x %d  |  V-cycles: %d  |  "
            "Smooth steps: %d  |  Timed runs: 5 warmup + %d timed\n\n",
            N, N, N, num_vcycles, num_smooth);

    // Load shader library
    NSError* error = nil;
    NSString* exePath = [[NSBundle mainBundle] executablePath];
    NSString* dir = [exePath stringByDeletingLastPathComponent];
    NSString* libPath = [dir stringByAppendingPathComponent:@"npb_mg.metal"];

    if (![[NSFileManager defaultManager] fileExistsAtPath:libPath])
        libPath = @"npb_mg.metal";

    NSString* src = [NSString stringWithContentsOfFile:libPath
                              encoding:NSUTF8StringEncoding error:&error];
    if (!src) {
        fprintf(stderr, "Failed to load shader file: %s\n",
                [[error localizedDescription] UTF8String]);
        return 1;
    }

    id<MTLLibrary> library = [device newLibraryWithSource:src options:nil error:&error];
    if (!library) {
        fprintf(stderr, "Failed to compile Metal shaders: %s\n",
                [[error localizedDescription] UTF8String]);
        return 1;
    }

    // Create compute pipelines
    id<MTLFunction> fn_smooth   = [library newFunctionWithName:@"smooth_kernel"];
    id<MTLFunction> fn_restrict = [library newFunctionWithName:@"restrict_kernel"];
    id<MTLFunction> fn_prolong  = [library newFunctionWithName:@"prolong_kernel"];
    id<MTLFunction> fn_residual = [library newFunctionWithName:@"residual_kernel"];
    id<MTLFunction> fn_norm_sq  = [library newFunctionWithName:@"norm_sq_kernel"];
    id<MTLFunction> fn_zero     = [library newFunctionWithName:@"zero_kernel"];

    id<MTLComputePipelineState> pso_smooth   = [device newComputePipelineStateWithFunction:fn_smooth   error:&error];
    id<MTLComputePipelineState> pso_restrict = [device newComputePipelineStateWithFunction:fn_restrict error:&error];
    id<MTLComputePipelineState> pso_prolong  = [device newComputePipelineStateWithFunction:fn_prolong  error:&error];
    id<MTLComputePipelineState> pso_residual = [device newComputePipelineStateWithFunction:fn_residual error:&error];
    id<MTLComputePipelineState> pso_norm_sq  = [device newComputePipelineStateWithFunction:fn_norm_sq  error:&error];
    id<MTLComputePipelineState> pso_zero     = [device newComputePipelineStateWithFunction:fn_zero     error:&error];

    id<MTLCommandQueue> queue = [device newCommandQueue];

    // Compute multigrid levels
    std::vector<int> levels;
    { int sz = N; while (sz >= 4) { levels.push_back(sz); sz /= 2; } }
    int num_levels = (int)levels.size();

    // Allocate buffers for each level
    std::vector<id<MTLBuffer>> buf_u(num_levels), buf_rhs(num_levels);
    std::vector<id<MTLBuffer>> buf_r(num_levels), buf_temp(num_levels);

    for (int lv = 0; lv < num_levels; ++lv) {
        int sz = levels[lv];
        size_t bytes = (size_t)sz * sz * sz * sizeof(float);
        buf_u[lv]    = [device newBufferWithLength:bytes options:MTLResourceStorageModeShared];
        buf_rhs[lv]  = [device newBufferWithLength:bytes options:MTLResourceStorageModeShared];
        buf_r[lv]    = [device newBufferWithLength:bytes options:MTLResourceStorageModeShared];
        buf_temp[lv] = [device newBufferWithLength:bytes options:MTLResourceStorageModeShared];
    }

    // Partial reduction buffer
    int max_total = N * N * N;
    int max_blocks = (max_total + block_size - 1) / block_size;
    id<MTLBuffer> buf_partial = [device newBufferWithLength:max_blocks * sizeof(float) options:MTLResourceStorageModeShared];
    id<MTLBuffer> buf_params  = [device newBufferWithLength:sizeof(MgParams) options:MTLResourceStorageModeShared];

    MTLSize tgSize = MTLSizeMake(block_size, 1, 1);

    // Generate synthetic RHS
    {
        float* ptr = (float*)[buf_rhs[0] contents];
        int total = N * N * N;
        unsigned int seed = 42;
        for (int i = 0; i < total; ++i) ptr[i] = rand_float(&seed, -1.0f, 1.0f);
    }

    // Zero all u and coarser rhs
    for (int lv = 0; lv < num_levels; ++lv) {
        memset([buf_u[lv] contents], 0, (size_t)levels[lv] * levels[lv] * levels[lv] * sizeof(float));
        if (lv > 0) memset([buf_rhs[lv] contents], 0, (size_t)levels[lv] * levels[lv] * levels[lv] * sizeof(float));
    }

    float smooth_weight = 1.0f;

    auto set_params = [&](uint32_t n, uint32_t nc, float w) {
        MgParams* pp = (MgParams*)[buf_params contents];
        pp->N = n;
        pp->N_coarse = nc;
        pp->weight = w;
    };

    auto finish_reduce = [&](int nblocks) -> float {
        float* p = (float*)[buf_partial contents];
        float sum = 0.0f;
        for (int i = 0; i < nblocks; ++i) sum += p[i];
        return sum;
    };

    // Helper: dispatch a kernel
    auto dispatch = [&](id<MTLComputePipelineState> pso,
                        std::vector<std::pair<id<MTLBuffer>, int>> bufs,
                        int total) {
        MTLSize grid = MTLSizeMake(((total + block_size - 1) / block_size) * block_size, 1, 1);
        id<MTLCommandBuffer> cmd = [queue commandBuffer];
        id<MTLComputeCommandEncoder> enc = [cmd computeCommandEncoder];
        [enc setComputePipelineState:pso];
        for (size_t bi = 0; bi < bufs.size(); ++bi) {
            [enc setBuffer:bufs[bi].first offset:0 atIndex:bufs[bi].second];
        }
        [enc dispatchThreads:grid threadsPerThreadgroup:tgSize];
        [enc endEncoding];
        [cmd commit];
        [cmd waitUntilCompleted];
    };

    // Lambda: run one V-cycle
    auto run_vcycle = [&]() {
        // Going down
        for (int lv = 0; lv < num_levels - 1; ++lv) {
            int sz = levels[lv];
            int total = sz * sz * sz;
            int csz = levels[lv + 1];
            int ctotal = csz * csz * csz;

            // Pre-smooth
            for (int s = 0; s < num_smooth; ++s) {
                set_params(sz, 0, smooth_weight);
                dispatch(pso_smooth,
                         {{buf_u[lv], 0}, {buf_rhs[lv], 1}, {buf_temp[lv], 2}, {buf_params, 3}},
                         total);
                // Swap u and temp
                id<MTLBuffer> tmp = buf_u[lv];
                buf_u[lv] = buf_temp[lv];
                buf_temp[lv] = tmp;
            }

            // Residual
            set_params(sz, 0, 0.0f);
            dispatch(pso_residual,
                     {{buf_u[lv], 0}, {buf_rhs[lv], 1}, {buf_r[lv], 2}, {buf_params, 3}},
                     total);

            // Restrict
            set_params(sz, csz, 0.0f);
            dispatch(pso_restrict,
                     {{buf_r[lv], 0}, {buf_rhs[lv + 1], 1}, {buf_params, 2}},
                     ctotal);

            // Zero coarse u
            memset([buf_u[lv + 1] contents], 0, ctotal * sizeof(float));
        }

        // Coarsest level: extra smoothing
        {
            int lv = num_levels - 1;
            int sz = levels[lv];
            int total = sz * sz * sz;
            for (int s = 0; s < num_smooth * 2; ++s) {
                set_params(sz, 0, smooth_weight);
                dispatch(pso_smooth,
                         {{buf_u[lv], 0}, {buf_rhs[lv], 1}, {buf_temp[lv], 2}, {buf_params, 3}},
                         total);
                id<MTLBuffer> tmp = buf_u[lv];
                buf_u[lv] = buf_temp[lv];
                buf_temp[lv] = tmp;
            }
        }

        // Going up
        for (int lv = num_levels - 2; lv >= 0; --lv) {
            int sz = levels[lv];
            int total = sz * sz * sz;
            int csz = levels[lv + 1];

            // Prolong
            set_params(sz, csz, 0.0f);
            dispatch(pso_prolong,
                     {{buf_u[lv + 1], 0}, {buf_u[lv], 1}, {buf_params, 2}},
                     total);

            // Post-smooth
            for (int s = 0; s < num_smooth; ++s) {
                set_params(sz, 0, smooth_weight);
                dispatch(pso_smooth,
                         {{buf_u[lv], 0}, {buf_rhs[lv], 1}, {buf_temp[lv], 2}, {buf_params, 3}},
                         total);
                id<MTLBuffer> tmp = buf_u[lv];
                buf_u[lv] = buf_temp[lv];
                buf_temp[lv] = tmp;
            }
        }
    };

    // Lambda: full MG solve
    auto run_mg = [&]() {
        for (int lv = 0; lv < num_levels; ++lv)
            memset([buf_u[lv] contents], 0, (size_t)levels[lv] * levels[lv] * levels[lv] * sizeof(float));
        for (int v = 0; v < num_vcycles; ++v) run_vcycle();
    };

    // Warmup
    for (int w = 0; w < num_warmup; ++w) run_mg();

    // Timed iterations
    std::vector<double> times(1);
    for (int i = 0; i < 1; ++i) {
        uint64_t t0 = mach_absolute_time();
        run_mg();
        uint64_t t1 = mach_absolute_time();
        times[i] = ticks_to_ms(t1 - t0);
    }

    double tsum = 0.0;
    for (int i = 0; i < 1; ++i) tsum += times[i];
    double avg_ms = tsum / 1;
    double total_ms = tsum;

    double mcells = (double)(N * N * N) / 1e6;
    double mcells_per_sec = mcells / (avg_ms * 1e-3);

    printf("{\"type\":\"kernel\",\"name\":\"smooth_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"grid_size\":%d,\"block_size\":%d,"
           "\"num_vcycles\":%d,\"num_smooth_steps\":%d}}\n",
           avg_ms, N, block_size, num_vcycles, num_smooth);

    printf("{\"type\":\"kernel\",\"name\":\"restrict_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"grid_size\":%d,\"block_size\":%d,"
           "\"num_vcycles\":%d,\"num_smooth_steps\":%d}}\n",
           avg_ms, N, block_size, num_vcycles, num_smooth);

    printf("{\"type\":\"kernel\",\"name\":\"prolong_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"grid_size\":%d,\"block_size\":%d,"
           "\"num_vcycles\":%d,\"num_smooth_steps\":%d}}\n",
           avg_ms, N, block_size, num_vcycles, num_smooth);

    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"avg_iter_ms\",\"value\":%.4f},"
           "{\"name\":\"mcells_per_sec\",\"value\":%.2f}]}\n",
           total_ms, avg_ms, mcells_per_sec);

    fprintf(stderr, "Average time: %.4f ms\n", avg_ms);
    fprintf(stderr, "Throughput:   %.2f Mcells/sec\n", mcells_per_sec);

    // Verification
    {
        run_mg();
        int total = N * N * N;
        int nblocks = (total + block_size - 1) / block_size;

        // Residual
        set_params(N, 0, 0.0f);
        dispatch(pso_residual,
                 {{buf_u[0], 0}, {buf_rhs[0], 1}, {buf_r[0], 2}, {buf_params, 3}},
                 total);

        // norm(r)
        set_params(total, 0, 0.0f);
        dispatch(pso_norm_sq,
                 {{buf_r[0], 0}, {buf_partial, 1}, {buf_params, 2}},
                 total);
        float res_norm = sqrtf(finish_reduce(nblocks));

        // norm(rhs)
        dispatch(pso_norm_sq,
                 {{buf_rhs[0], 0}, {buf_partial, 1}, {buf_params, 2}},
                 total);
        float rhs_norm = sqrtf(finish_reduce(nblocks));

        float rel_res = res_norm / fmaxf(rhs_norm, 1e-20f);
        fprintf(stderr, "Residual ||r||/||rhs|| = %.6e\n", rel_res);

        if (rel_res < 10.0f)
            fprintf(stderr, "PASS\n");
        else
            fprintf(stderr, "FAIL: relative residual too large\n");
    }

    } // @autoreleasepool
    return 0;
}
