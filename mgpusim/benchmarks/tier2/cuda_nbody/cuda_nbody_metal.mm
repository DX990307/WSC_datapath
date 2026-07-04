/**
 * cuda_nbody_metal.mm — Apple Metal host for the N-body benchmark.
 *
 * All-pairs N-body gravitational simulation with tile-based shared memory
 * optimization. Each thread computes the force on one body from all others.
 *
 * Usage:
 *   ./cuda_nbody [--bodies N] [--iterations I] [--timesteps T]
 *
 *   --bodies N       Number of bodies (default: 16384)
 *   --iterations I   Timed iterations (default: 5)
 *   --timesteps T    Simulation timesteps per run (default: 10)
 *
 * Output (stdout): CSV — cuda_nbody,<N>,<time_ms>,<GFLOPS>
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

#define TILE_SIZE 256
#define SOFTENING 1e-5f
#define DT        0.01f

// ---------------------------------------------------------------------------
// float4 struct for host side (matching Metal float4 layout)
// ---------------------------------------------------------------------------

struct vec4 { float x, y, z, w; };

// ---------------------------------------------------------------------------
// Simple deterministic PRNG for initialization
// ---------------------------------------------------------------------------

static float rand_float(uint32_t& state) {
    state ^= state << 13;
    state ^= state >> 17;
    state ^= state << 5;
    return (float)(state & 0xFFFFFF) / (float)0xFFFFFF * 2.0f - 1.0f;
}

// ---------------------------------------------------------------------------
// CPU reference N-body for verification
// ---------------------------------------------------------------------------

static void nbody_cpu(vec4* pos, vec4* vel, int N) {
    std::vector<float> accx(N, 0.0f), accy(N, 0.0f), accz(N, 0.0f);

    for (int i = 0; i < N; ++i) {
        for (int j = 0; j < N; ++j) {
            float dx = pos[j].x - pos[i].x;
            float dy = pos[j].y - pos[i].y;
            float dz = pos[j].z - pos[i].z;

            float distSqr = dx * dx + dy * dy + dz * dz + SOFTENING;
            float invDist  = 1.0f / sqrtf(distSqr);
            float invDist3 = invDist * invDist * invDist;
            float mass_j   = pos[j].w;

            accx[i] += dx * invDist3 * mass_j;
            accy[i] += dy * invDist3 * mass_j;
            accz[i] += dz * invDist3 * mass_j;
        }
    }

    for (int i = 0; i < N; ++i) {
        vel[i].x += accx[i] * DT;
        vel[i].y += accy[i] * DT;
        vel[i].z += accz[i] * DT;

        pos[i].x += vel[i].x * DT;
        pos[i].y += vel[i].y * DT;
        pos[i].z += vel[i].z * DT;
    }
}

// ---------------------------------------------------------------------------
// main
// ---------------------------------------------------------------------------

int main(int argc, char* argv[]) {
    @autoreleasepool {
    setvbuf(stdout, NULL, _IONBF, 0);

    // Default values from params.json
    int N          = 16384;
    int timesteps  = 10;
    int iters      = 5;
    int block_size = 256;

    // Command-line args as fallback
    N         = parseIntParam(argc, argv, "--bodies", N);
    iters     = parseIntParam(argc, argv, "--iterations", iters);
    timesteps = parseIntParam(argc, argv, "--timesteps", timesteps);
    block_size = parseIntParam(argc, argv, "--block_size", block_size);

    // Env vars override
    const char* env_val;
    env_val = getenv("BENCH_PARAM_bodies");
    if (env_val) N = atoi(env_val);
    env_val = getenv("BENCH_PARAM_timesteps");
    if (env_val) timesteps = atoi(env_val);
    env_val = getenv("BENCH_PARAM_iterations");
    if (env_val) iters = atoi(env_val);
    env_val = getenv("BENCH_PARAM_block_size");
    if (env_val) block_size = atoi(env_val);
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
    fprintf(stderr, "Bodies: %d  |  Timesteps: %d  |  Iterations: 5 warmup + %d timed\n\n",
            N, timesteps, iters);

    id<MTLCommandQueue> queue = [device newCommandQueue];

    // -------------------------------------------------------------------
    // Compile Metal shader
    // -------------------------------------------------------------------
    NSError* err = nil;
    id<MTLLibrary> library = nil;

    NSString* exePath = [[NSBundle mainBundle] executablePath];
    NSString* dir     = [exePath stringByDeletingLastPathComponent];
    NSString* srcPath = [dir stringByAppendingPathComponent:@"cuda_nbody.metal"];

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
        fprintf(stderr, "Error: cuda_nbody.metal not found at %s\n",
                srcPath.UTF8String);
        return EXIT_FAILURE;
    }

    id<MTLFunction> fnNBody = [library newFunctionWithName:@"nbody_kernel"];
    if (!fnNBody) {
        fprintf(stderr, "Error: nbody_kernel function not found\n");
        return EXIT_FAILURE;
    }

    id<MTLComputePipelineState> psoNBody =
        [device newComputePipelineStateWithFunction:fnNBody error:&err];
    if (!psoNBody) {
        fprintf(stderr, "Error creating nbody pipeline: %s\n",
                err.localizedDescription.UTF8String);
        return EXIT_FAILURE;
    }

    // -------------------------------------------------------------------
    // Initialize bodies
    // -------------------------------------------------------------------
    size_t bytes = (size_t)N * sizeof(vec4);

    std::vector<vec4> h_pos_orig(N), h_vel_orig(N);
    uint32_t rng = 42;

    for (int i = 0; i < N; ++i) {
        h_pos_orig[i].x = rand_float(rng);
        h_pos_orig[i].y = rand_float(rng);
        h_pos_orig[i].z = rand_float(rng);
        h_pos_orig[i].w = 1.0f;  // mass
        h_vel_orig[i] = {0.0f, 0.0f, 0.0f, 0.0f};
    }

    // -------------------------------------------------------------------
    // Allocate Metal buffers
    // -------------------------------------------------------------------
    id<MTLBuffer> bufPos = [device newBufferWithLength:bytes
                                               options:MTLResourceStorageModeShared];
    id<MTLBuffer> bufVel = [device newBufferWithLength:bytes
                                               options:MTLResourceStorageModeShared];

    NSUInteger tgSize = TILE_SIZE;
    NSUInteger gridN  = (NSUInteger)N;
    uint32_t nParam   = (uint32_t)N;

    // Lambda: run full simulation
    auto run_nbody = [&]() {
        memcpy(bufPos.contents, h_pos_orig.data(), bytes);
        memcpy(bufVel.contents, h_vel_orig.data(), bytes);

        for (int t = 0; t < timesteps; ++t) {
            id<MTLCommandBuffer> cb = [queue commandBuffer];
            id<MTLComputeCommandEncoder> enc = [cb computeCommandEncoder];

            [enc setComputePipelineState:psoNBody];
            [enc setBuffer:bufPos offset:0 atIndex:0];
            [enc setBuffer:bufVel offset:0 atIndex:1];
            [enc setBytes:&nParam length:sizeof(nParam) atIndex:2];

            [enc dispatchThreads:MTLSizeMake(gridN, 1, 1)
                threadsPerThreadgroup:MTLSizeMake(tgSize, 1, 1)];

            [enc endEncoding];
            [cb commit];
            [cb waitUntilCompleted];
        }
    };

    // -------------------------------------------------------------------
    // Warmup
    // -------------------------------------------------------------------
    for (int w = 0; w < num_warmup; ++w) {
        run_nbody();
    }

    // -------------------------------------------------------------------
    // Timed iterations
    // -------------------------------------------------------------------
    std::vector<double> times((size_t)iters);
    for (int i = 0; i < iters; ++i) {
        uint64_t t0 = mach_absolute_time();
        run_nbody();
        uint64_t t1 = mach_absolute_time();
        times[i] = ticks_to_ms(t1 - t0);
    }

    double sum = 0.0;
    for (int i = 0; i < iters; ++i) sum += times[i];
    double avg_ms = sum / iters;

    // GFLOPS = 20 * N * N * timesteps / time_s / 1e9
    double gflops = 20.0 * (double)N * (double)N * (double)timesteps
                    / (avg_ms * 1e-3) / 1e9;

    // JSON-lines output
    printf("{\"type\":\"kernel\",\"name\":\"nbody_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"bodies\":%d,\"timesteps\":%d,\"iterations\":%d,"
           "\"block_size\":%d}}\n",
           avg_ms, N, timesteps, iters, block_size);

    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"gflops\",\"value\":%.2f}]}\n",
           sum, gflops);

    // Human-readable output to stderr
    fprintf(stderr, "Average time: %.4f ms\n", avg_ms);
    fprintf(stderr, "Performance:  %.4f GFLOPS\n", gflops);

    // -------------------------------------------------------------------
    // Verification: 1 timestep on small subset
    // -------------------------------------------------------------------
    int verifyN = (N <= 4096) ? N : 4096;
    {
        std::vector<vec4> cpuPos(h_pos_orig.begin(), h_pos_orig.begin() + verifyN);
        std::vector<vec4> cpuVel(h_vel_orig.begin(), h_vel_orig.begin() + verifyN);

        // GPU: 1 timestep on verifyN bodies
        size_t vbytes = verifyN * sizeof(vec4);
        id<MTLBuffer> vBufPos = [device newBufferWithBytes:cpuPos.data()
                                                    length:vbytes
                                                   options:MTLResourceStorageModeShared];
        id<MTLBuffer> vBufVel = [device newBufferWithBytes:cpuVel.data()
                                                    length:vbytes
                                                   options:MTLResourceStorageModeShared];

        uint32_t vn = (uint32_t)verifyN;
        NSUInteger vGrid = (NSUInteger)verifyN;

        id<MTLCommandBuffer> cb = [queue commandBuffer];
        id<MTLComputeCommandEncoder> enc = [cb computeCommandEncoder];

        [enc setComputePipelineState:psoNBody];
        [enc setBuffer:vBufPos offset:0 atIndex:0];
        [enc setBuffer:vBufVel offset:0 atIndex:1];
        [enc setBytes:&vn      length:sizeof(vn) atIndex:2];

        [enc dispatchThreads:MTLSizeMake(vGrid, 1, 1)
            threadsPerThreadgroup:MTLSizeMake(tgSize, 1, 1)];

        [enc endEncoding];
        [cb commit];
        [cb waitUntilCompleted];

        const vec4* gpuPos = (const vec4*)vBufPos.contents;

        // CPU reference
        nbody_cpu(cpuPos.data(), cpuVel.data(), verifyN);

        int errors = 0;
        for (int i = 0; i < verifyN; ++i) {
            float dx = fabsf(gpuPos[i].x - cpuPos[i].x);
            float dy = fabsf(gpuPos[i].y - cpuPos[i].y);
            float dz = fabsf(gpuPos[i].z - cpuPos[i].z);
            float tol = 1e-2f * (fabsf(cpuPos[i].x) + fabsf(cpuPos[i].y) + fabsf(cpuPos[i].z)) + 1e-5f;
            if (dx > tol || dy > tol || dz > tol) {
                if (errors < 10) {
                    fprintf(stderr, "Mismatch at body %d: GPU=(%.6f,%.6f,%.6f) CPU=(%.6f,%.6f,%.6f)\n",
                            i, gpuPos[i].x, gpuPos[i].y, gpuPos[i].z,
                            cpuPos[i].x, cpuPos[i].y, cpuPos[i].z);
                }
                errors++;
            }
        }

        if (errors > 0)
            fprintf(stderr, "FAIL: %d errors out of %d bodies\n", errors, verifyN);
        else
            fprintf(stderr, "PASS (verified %d bodies, 1 timestep)\n", verifyN);
    }

    return EXIT_SUCCESS;

    } // @autoreleasepool
}
