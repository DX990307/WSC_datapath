/**
 * rodinia_lavamd_metal.mm — Apple Metal host for the Rodinia LavaMD benchmark.
 *
 * Short-range molecular dynamics with cell-list decomposition.
 * Computes Lennard-Jones type particle interactions within neighboring cells.
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

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------
#define BOX_SIZE 10.0f

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

    // Defaults from params.json
    int num_boxes         = 10;
    int block_size        = 128;
    int particles_per_box = 100;

    // Command-line fallback
    num_boxes         = parseIntParam(argc, argv, "--num_boxes", num_boxes);
    block_size        = parseIntParam(argc, argv, "--block_size", block_size);
    particles_per_box = parseIntParam(argc, argv, "--particles_per_box", particles_per_box);

    // Environment variable overrides
    const char* env_val;
    env_val = getenv("BENCH_PARAM_num_boxes");
    if (env_val) num_boxes = atoi(env_val);
    env_val = getenv("BENCH_PARAM_block_size");
    if (env_val) block_size = atoi(env_val);
    env_val = getenv("BENCH_PARAM_particles_per_box");
    if (env_val) particles_per_box = atoi(env_val);
    int num_warmup = 0;

    int nb = num_boxes;
    int total_boxes        = nb * nb * nb;
    long long total_particles = (long long)total_boxes * particles_per_box;

    // -------------------------------------------------------------------
    // Metal device setup
    // -------------------------------------------------------------------
    id<MTLDevice> device = MTLCreateSystemDefaultDevice();
    if (!device) {
        fprintf(stderr, "Error: no Metal device found\n");
        return EXIT_FAILURE;
    }
    fprintf(stderr, "Device: %s\n", device.name.UTF8String);
    fprintf(stderr, "Boxes: %d×%d×%d (%d total)  |  Particles/box: %d  |  "
            "Iterations: %d warmup + %d timed\n\n",
            nb, nb, nb, total_boxes, particles_per_box, num_warmup);

    id<MTLCommandQueue> queue = [device newCommandQueue];

    // -------------------------------------------------------------------
    // Compile Metal shader
    // -------------------------------------------------------------------
    NSError *err = nil;
    id<MTLLibrary> library = nil;

    NSString *exePath = [[NSBundle mainBundle] executablePath];
    NSString *dir     = [exePath stringByDeletingLastPathComponent];
    NSString *srcPath = [dir stringByAppendingPathComponent:@"rodinia_lavamd.metal"];

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
        fprintf(stderr, "Error: rodinia_lavamd.metal not found at %s\n",
                srcPath.UTF8String);
        return EXIT_FAILURE;
    }

    id<MTLFunction> fn = [library newFunctionWithName:@"lavamd_kernel"];
    if (!fn) {
        fprintf(stderr, "Error: kernel 'lavamd_kernel' not found\n");
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
    // Generate synthetic particle data
    // -------------------------------------------------------------------
    size_t pos_bytes    = (size_t)total_particles * sizeof(float);
    size_t neigh_bytes  = (size_t)total_boxes * 27 * sizeof(int);
    size_t ncount_bytes = (size_t)total_boxes * sizeof(int);

    id<MTLBuffer> buf_pos_x = [device newBufferWithLength:pos_bytes
                                                  options:MTLResourceStorageModeShared];
    id<MTLBuffer> buf_pos_y = [device newBufferWithLength:pos_bytes
                                                  options:MTLResourceStorageModeShared];
    id<MTLBuffer> buf_pos_z = [device newBufferWithLength:pos_bytes
                                                  options:MTLResourceStorageModeShared];
    id<MTLBuffer> buf_force_x = [device newBufferWithLength:pos_bytes
                                                    options:MTLResourceStorageModeShared];
    id<MTLBuffer> buf_force_y = [device newBufferWithLength:pos_bytes
                                                    options:MTLResourceStorageModeShared];
    id<MTLBuffer> buf_force_z = [device newBufferWithLength:pos_bytes
                                                    options:MTLResourceStorageModeShared];
    id<MTLBuffer> buf_energy = [device newBufferWithLength:pos_bytes
                                                  options:MTLResourceStorageModeShared];
    id<MTLBuffer> buf_neighbor_list = [device newBufferWithLength:neigh_bytes
                                                         options:MTLResourceStorageModeShared];
    id<MTLBuffer> buf_neighbor_count = [device newBufferWithLength:ncount_bytes
                                                          options:MTLResourceStorageModeShared];

    float* h_pos_x = (float*)buf_pos_x.contents;
    float* h_pos_y = (float*)buf_pos_y.contents;
    float* h_pos_z = (float*)buf_pos_z.contents;
    int*   h_neighbor_list  = (int*)buf_neighbor_list.contents;
    int*   h_neighbor_count = (int*)buf_neighbor_count.contents;

    unsigned int seed = 42;

    for (int bz = 0; bz < nb; ++bz) {
        for (int by = 0; by < nb; ++by) {
            for (int bx = 0; bx < nb; ++bx) {
                int box_id = bz * nb * nb + by * nb + bx;
                int base = box_id * particles_per_box;

                for (int p = 0; p < particles_per_box; ++p) {
                    h_pos_x[base + p] = bx * BOX_SIZE + rand_float(&seed, 0.5f, BOX_SIZE - 0.5f);
                    h_pos_y[base + p] = by * BOX_SIZE + rand_float(&seed, 0.5f, BOX_SIZE - 0.5f);
                    h_pos_z[base + p] = bz * BOX_SIZE + rand_float(&seed, 0.5f, BOX_SIZE - 0.5f);
                }

                int count = 0;
                for (int dz = -1; dz <= 1; ++dz) {
                    for (int dy = -1; dy <= 1; ++dy) {
                        for (int dx = -1; dx <= 1; ++dx) {
                            int nnx = bx + dx;
                            int nny = by + dy;
                            int nnz = bz + dz;
                            if (nnx >= 0 && nnx < nb && nny >= 0 && nny < nb && nnz >= 0 && nnz < nb) {
                                int nid = nnz * nb * nb + nny * nb + nnx;
                                h_neighbor_list[box_id * 27 + count] = nid;
                                count++;
                            }
                        }
                    }
                }
                h_neighbor_count[box_id] = count;
            }
        }
    }

    // Params buffer: {particles_per_box, total_boxes, 0, 0}
    uint32_t params_data[4] = { (uint32_t)particles_per_box, (uint32_t)total_boxes, 0, 0 };

    NSUInteger tg_size = (NSUInteger)block_size;

    // Helper: dispatch kernel
    auto run_kernel = [&]() {
        id<MTLCommandBuffer>         cb  = [queue commandBuffer];
        id<MTLComputeCommandEncoder> enc = [cb computeCommandEncoder];
        [enc setComputePipelineState:pso];
        [enc setBuffer:buf_pos_x          offset:0 atIndex:0];
        [enc setBuffer:buf_pos_y          offset:0 atIndex:1];
        [enc setBuffer:buf_pos_z          offset:0 atIndex:2];
        [enc setBuffer:buf_force_x        offset:0 atIndex:3];
        [enc setBuffer:buf_force_y        offset:0 atIndex:4];
        [enc setBuffer:buf_force_z        offset:0 atIndex:5];
        [enc setBuffer:buf_energy         offset:0 atIndex:6];
        [enc setBuffer:buf_neighbor_list  offset:0 atIndex:7];
        [enc setBuffer:buf_neighbor_count offset:0 atIndex:8];
        [enc setBytes:params_data length:sizeof(params_data) atIndex:9];
        [enc dispatchThreadgroups:MTLSizeMake((NSUInteger)total_boxes, 1, 1)
            threadsPerThreadgroup:MTLSizeMake(tg_size, 1, 1)];
        [enc endEncoding];
        [cb commit];
        [cb waitUntilCompleted];
    };

    // Warmup
    for (int w = 0; w < num_warmup; ++w) {
        memset(buf_force_x.contents, 0, pos_bytes);
        memset(buf_force_y.contents, 0, pos_bytes);
        memset(buf_force_z.contents, 0, pos_bytes);
        memset(buf_energy.contents,  0, pos_bytes);
        run_kernel();
    }

    // -------------------------------------------------------------------
    // Timed iterations
    // -------------------------------------------------------------------
    std::vector<double> times(1);
    for (int iter = 0; iter < 1; ++iter) {
        memset(buf_force_x.contents, 0, pos_bytes);
        memset(buf_force_y.contents, 0, pos_bytes);
        memset(buf_force_z.contents, 0, pos_bytes);
        memset(buf_energy.contents,  0, pos_bytes);

        uint64_t t0 = mach_absolute_time();
        run_kernel();
        uint64_t t1 = mach_absolute_time();

        times[iter] = ticks_to_ms(t1 - t0);
    }

    double sum = 0.0;
    for (int i = 0; i < 1; ++i) sum += times[i];
    double avg_ms = sum / 1;
    double total_ms = sum;

    // Compute GFLOPS
    double avg_neighbors = 0;
    for (int i = 0; i < total_boxes; ++i) avg_neighbors += h_neighbor_count[i];
    avg_neighbors /= total_boxes;
    double flops = (double)total_particles * avg_neighbors * particles_per_box * 20.0;
    double gflops = flops / (avg_ms * 1e-3) / 1e9;

    fprintf(stderr, "Average time: %.4f ms  |  Throughput: %.2f GFLOPS\n", avg_ms, gflops);

    // JSON-lines output
    printf("{\"type\":\"kernel\",\"name\":\"lavamd_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"num_boxes\":%d,\"block_size\":%d,"
           "\"particles_per_box\":%d}}\n",
           avg_ms, num_boxes, block_size, particles_per_box);

    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"gflops\",\"value\":%.2f}]}\n",
           total_ms, gflops);

    return EXIT_SUCCESS;

    } // @autoreleasepool
}
