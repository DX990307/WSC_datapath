/**
 * parboil_cutcp_metal.mm — Apple Metal host for CUTCP benchmark.
 *
 * Coulombic potential with distance cutoff on a 3D grid.
 * Each GPU thread computes potential at one grid point from all atoms
 * within the cutoff radius.
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

#define GRID_DIM_SIDE 64

struct CutcpParams {
    uint32_t num_atoms;
    uint32_t grid_side;
    float    grid_spacing;
    float    cutoff;
    float    cutoff2;
};

// ---------------------------------------------------------------------------
// CPU reference
// ---------------------------------------------------------------------------

static float cpu_potential(
    const float* atoms_x, const float* atoms_y,
    const float* atoms_z, const float* atoms_q,
    int num_atoms, float px, float py, float pz,
    float cutoff2)
{
    float pot = 0.0f;
    for (int i = 0; i < num_atoms; ++i) {
        float dx = px - atoms_x[i];
        float dy = py - atoms_y[i];
        float dz = pz - atoms_z[i];
        float r2 = dx * dx + dy * dy + dz * dz;
        if (r2 < cutoff2 && r2 > 1e-12f) {
            pot += atoms_q[i] / sqrtf(r2);
        }
    }
    return pot;
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
// Atom struct (matches float4 in shader)
// ---------------------------------------------------------------------------

struct Atom4 {
    float x, y, z, q;
};

// ---------------------------------------------------------------------------
// main
// ---------------------------------------------------------------------------

int main(int argc, char* argv[]) {
    @autoreleasepool {
    setvbuf(stdout, NULL, _IONBF, 0);

    int num_atoms    = 10000;
    int block_size   = 128;
    float grid_spacing = 0.5f;
    float cutoff     = 12.0f;

    const char* env_val;
    env_val = getenv("BENCH_PARAM_num_atoms");
    if (env_val) num_atoms = atoi(env_val);
    env_val = getenv("BENCH_PARAM_block_size");
    if (env_val) block_size = atoi(env_val);
    env_val = getenv("BENCH_PARAM_grid_spacing");
    if (env_val) grid_spacing = (float)atof(env_val);
    env_val = getenv("BENCH_PARAM_cutoff_radius");
    if (env_val) cutoff = (float)atof(env_val);
    int num_warmup = 0;

    float cutoff2 = cutoff * cutoff;
    int grid_side = GRID_DIM_SIDE;
    int total_grid_points = grid_side * grid_side * grid_side;
    float domain_size = grid_side * grid_spacing;

    // -------------------------------------------------------------------
    // Metal device setup
    // -------------------------------------------------------------------
    id<MTLDevice> device = MTLCreateSystemDefaultDevice();
    if (!device) {
        fprintf(stderr, "Error: no Metal device found\n");
        return EXIT_FAILURE;
    }
    fprintf(stderr, "Device: %s\n", device.name.UTF8String);
    fprintf(stderr, "CUTCP  |  Atoms: %d  |  Grid: %d^3 = %d  |  Cutoff: %.1f  |  "
            "Spacing: %.2f  |  Iterations: 5 warmup + %d timed\n\n",
            num_atoms, grid_side, total_grid_points, cutoff, grid_spacing);

    id<MTLCommandQueue> queue = [device newCommandQueue];

    // -------------------------------------------------------------------
    // Compile Metal shader
    // -------------------------------------------------------------------
    NSError* err = nil;
    id<MTLLibrary> library = nil;

    NSString* exePath = [[NSBundle mainBundle] executablePath];
    NSString* dir     = [exePath stringByDeletingLastPathComponent];
    NSString* srcPath = [dir stringByAppendingPathComponent:@"parboil_cutcp.metal"];

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
        fprintf(stderr, "Error: parboil_cutcp.metal not found at %s\n",
                srcPath.UTF8String);
        return EXIT_FAILURE;
    }

    id<MTLFunction> fnCutcp = [library newFunctionWithName:@"cutcp_kernel"];
    if (!fnCutcp) {
        fprintf(stderr, "Error: cutcp_kernel not found\n");
        return EXIT_FAILURE;
    }

    id<MTLComputePipelineState> psoCutcp =
        [device newComputePipelineStateWithFunction:fnCutcp error:&err];
    if (!psoCutcp) {
        fprintf(stderr, "Error creating pipeline: %s\n",
                err.localizedDescription.UTF8String);
        return EXIT_FAILURE;
    }

    // -------------------------------------------------------------------
    // Generate synthetic atom data
    // -------------------------------------------------------------------
    std::vector<float> h_atoms_x(num_atoms), h_atoms_y(num_atoms);
    std::vector<float> h_atoms_z(num_atoms), h_atoms_q(num_atoms);
    std::vector<Atom4> h_atoms(num_atoms);

    unsigned int seed = 42;
    for (int i = 0; i < num_atoms; ++i) {
        float x = rand_float(&seed, 0.0f, domain_size);
        float y = rand_float(&seed, 0.0f, domain_size);
        float z = rand_float(&seed, 0.0f, domain_size);
        float q = rand_float(&seed, -1.0f, 1.0f);
        h_atoms_x[i] = x;
        h_atoms_y[i] = y;
        h_atoms_z[i] = z;
        h_atoms_q[i] = q;
        h_atoms[i] = {x, y, z, q};
    }

    // -------------------------------------------------------------------
    // Allocate Metal buffers
    // -------------------------------------------------------------------
    size_t atom_bytes = (size_t)num_atoms * sizeof(Atom4);
    size_t grid_bytes = (size_t)total_grid_points * sizeof(float);

    id<MTLBuffer> bufAtoms = [device newBufferWithBytes:h_atoms.data()
                                                length:atom_bytes
                                               options:MTLResourceStorageModeShared];
    id<MTLBuffer> bufPotential = [device newBufferWithLength:grid_bytes
                                                    options:MTLResourceStorageModeShared];

    NSUInteger tgSize = (NSUInteger)block_size;
    NSUInteger gridN  = (NSUInteger)total_grid_points;

    // Run kernel
    auto run_cutcp = [&]() {
        CutcpParams params = {
            (uint32_t)num_atoms,
            (uint32_t)grid_side,
            grid_spacing,
            cutoff,
            cutoff2
        };

        id<MTLCommandBuffer> cb = [queue commandBuffer];
        id<MTLComputeCommandEncoder> enc = [cb computeCommandEncoder];
        [enc setComputePipelineState:psoCutcp];
        [enc setBuffer:bufAtoms     offset:0 atIndex:0];
        [enc setBuffer:bufPotential offset:0 atIndex:1];
        [enc setBytes:&params       length:sizeof(params) atIndex:2];
        [enc dispatchThreads:MTLSizeMake(gridN, 1, 1)
            threadsPerThreadgroup:MTLSizeMake(tgSize, 1, 1)];
        [enc endEncoding];
        [cb commit];
        [cb waitUntilCompleted];
    };

    // -------------------------------------------------------------------
    // Warmup
    // -------------------------------------------------------------------
    for (int w = 0; w < num_warmup; ++w) {
        run_cutcp();
    }

    // -------------------------------------------------------------------
    // Timed iterations
    // -------------------------------------------------------------------
    std::vector<double> times(1);
    for (int i = 0; i < 1; ++i) {
        uint64_t t0 = mach_absolute_time();
        run_cutcp();
        uint64_t t1 = mach_absolute_time();
        times[i] = ticks_to_ms(t1 - t0);
    }

    double sum = 0.0;
    for (int i = 0; i < 1; ++i) sum += times[i];
    double avg_ms = sum / 1;
    double total_ms = sum;

    double interactions = (double)num_atoms * (double)total_grid_points;
    double ginteractions_per_sec = interactions / (avg_ms * 1e-3) / 1e9;

    printf("{\"type\":\"kernel\",\"name\":\"cutcp_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"num_atoms\":%d\"block_size\":%d,"
           "\"grid_spacing\":%.2f,\"cutoff_radius\":%.1f}}\n",
           avg_ms, num_atoms, block_size, grid_spacing, cutoff);

    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"ginteractions_per_sec\",\"value\":%.4f}]}\n",
           total_ms, ginteractions_per_sec);

    fprintf(stderr, "Average time: %.4f ms\n", avg_ms);
    fprintf(stderr, "Throughput:   %.4f Ginteractions/sec\n", ginteractions_per_sec);

    // -------------------------------------------------------------------
    // Verification
    // -------------------------------------------------------------------
    {
        const float* h_potential = (const float*)bufPotential.contents;

        int verify_n = 64;
        int errors = 0;
        unsigned int vseed = 123;
        for (int v = 0; v < verify_n; ++v) {
            int idx = lcg_rand(&vseed) % total_grid_points;
            int gz = idx / (grid_side * grid_side);
            int gy = (idx / grid_side) % grid_side;
            int gx = idx % grid_side;
            float px = gx * grid_spacing;
            float py = gy * grid_spacing;
            float pz = gz * grid_spacing;

            float cpu_pot = cpu_potential(
                h_atoms_x.data(), h_atoms_y.data(),
                h_atoms_z.data(), h_atoms_q.data(),
                num_atoms, px, py, pz, cutoff2);

            float gpu_pot = h_potential[idx];
            float diff = fabsf(gpu_pot - cpu_pot);
            float tol = 1e-3f * (fabsf(cpu_pot) + 1e-6f);
            if (diff > tol) {
                if (errors < 10) {
                    fprintf(stderr, "Mismatch at grid[%d] (%d,%d,%d): "
                            "GPU=%.6f CPU=%.6f diff=%.6f\n",
                            idx, gx, gy, gz, gpu_pot, cpu_pot, diff);
                }
                errors++;
            }
        }

        if (errors > 0)
            fprintf(stderr, "FAIL: %d errors in %d verified points\n", errors, verify_n);
        else
            fprintf(stderr, "PASS\n");
    }

    return EXIT_SUCCESS;

    } // @autoreleasepool
}
