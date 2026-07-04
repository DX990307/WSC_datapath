/**
 * altis_cfd_metal.mm — Apple Metal host for CFD Euler solver benchmark.
 *
 * Solves compressible Euler equations on a synthetic unstructured mesh using
 * a finite-volume method with Rusanov flux scheme and Runge-Kutta time stepping.
 *
 * Usage:
 *   ./altis_cfd [--size N]
 *
 *   --size N         Number of mesh cells (default: 97000)
 *   --iterations I   Timed iterations (default: 5)
 *
 * Output (stdout): CSV — altis_cfd,<N>,<time_ms>,<Mcells_per_sec>
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

#define GAMMA    1.4f
#define GAMMA_M1 0.4f
#define NUM_NEIGHBORS 4
#define RK_STEPS 2
#define DT 0.0001f

struct CfdParams {
    uint32_t N;
    float dt;
    float rk_coeff;
};

// ---------------------------------------------------------------------------
// CPU reference helpers
// ---------------------------------------------------------------------------

static inline float compute_pressure(float rho, float mx, float my,
                                      float mz, float e) {
    float ke = 0.5f * (mx * mx + my * my + mz * mz) / fmaxf(rho, 1e-10f);
    return GAMMA_M1 * (e - ke);
}

static inline float compute_speed_of_sound(float rho, float p) {
    return sqrtf(fmaxf(GAMMA * p / fmaxf(rho, 1e-10f), 1e-10f));
}

static void cpu_compute_flux(
    const float* rho, const float* mx, const float* my, const float* mz,
    const float* energy, const int* neighbors, const float* normals,
    const float* areas, float* flux_rho, float* flux_mx, float* flux_my,
    float* flux_mz, float* flux_energy, int idx)
{
    float rho_i = rho[idx], mx_i = mx[idx], my_i = my[idx];
    float mz_i  = mz[idx],  e_i  = energy[idx];
    float p_i = compute_pressure(rho_i, mx_i, my_i, mz_i, e_i);
    float a_i = compute_speed_of_sound(rho_i, p_i);

    float inv_rho_i = 1.0f / fmaxf(rho_i, 1e-10f);
    float vx_i = mx_i * inv_rho_i;
    float vy_i = my_i * inv_rho_i;
    float vz_i = mz_i * inv_rho_i;

    float f_rho = 0, f_mx = 0, f_my = 0, f_mz = 0, f_e = 0;

    for (int f = 0; f < NUM_NEIGHBORS; ++f) {
        int j = neighbors[idx * NUM_NEIGHBORS + f];
        int nbase = (idx * NUM_NEIGHBORS + f) * 3;
        float nx = normals[nbase], ny = normals[nbase+1], nz = normals[nbase+2];
        float area = areas[idx * NUM_NEIGHBORS + f];

        float rho_j = rho[j], mx_j = mx[j], my_j = my[j];
        float mz_j = mz[j], e_j = energy[j];
        float p_j = compute_pressure(rho_j, mx_j, my_j, mz_j, e_j);
        float a_j = compute_speed_of_sound(rho_j, p_j);

        float inv_rho_j = 1.0f / fmaxf(rho_j, 1e-10f);
        float vx_j = mx_j * inv_rho_j;
        float vy_j = my_j * inv_rho_j;
        float vz_j = mz_j * inv_rho_j;

        float vn_i = vx_i*nx + vy_i*ny + vz_i*nz;
        float vn_j = vx_j*nx + vy_j*ny + vz_j*nz;

        float f_rho_i = rho_i*vn_i, f_rho_j = rho_j*vn_j;
        float f_mx_i = mx_i*vn_i + p_i*nx, f_mx_j = mx_j*vn_j + p_j*nx;
        float f_my_i = my_i*vn_i + p_i*ny, f_my_j = my_j*vn_j + p_j*ny;
        float f_mz_i = mz_i*vn_i + p_i*nz, f_mz_j = mz_j*vn_j + p_j*nz;
        float f_e_i = (e_i + p_i)*vn_i, f_e_j = (e_j + p_j)*vn_j;

        float lambda = fmaxf(fabsf(vn_i) + a_i, fabsf(vn_j) + a_j);

        f_rho += area * (0.5f*(f_rho_i + f_rho_j) - 0.5f*lambda*(rho_j - rho_i));
        f_mx  += area * (0.5f*(f_mx_i  + f_mx_j)  - 0.5f*lambda*(mx_j  - mx_i));
        f_my  += area * (0.5f*(f_my_i  + f_my_j)  - 0.5f*lambda*(my_j  - my_i));
        f_mz  += area * (0.5f*(f_mz_i  + f_mz_j)  - 0.5f*lambda*(mz_j  - mz_i));
        f_e   += area * (0.5f*(f_e_i   + f_e_j)   - 0.5f*lambda*(e_j   - e_i));
    }

    flux_rho[idx]    = f_rho;
    flux_mx[idx]     = f_mx;
    flux_my[idx]     = f_my;
    flux_mz[idx]     = f_mz;
    flux_energy[idx] = f_e;
}

// ---------------------------------------------------------------------------
// Simple PRNG for synthetic data generation
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

    // Defaults from params.json
    int N          = 97000;
    int block_size = 256;
    const char* precision = "float";

    // Command-line fallback
    N          = parseIntParam(argc, argv, "--size", N);

    // Environment variable overrides
    const char* env_val;
    env_val = getenv("BENCH_PARAM_size");
    if (env_val) N = atoi(env_val);
    env_val = getenv("BENCH_PARAM_block_size");
    if (env_val) block_size = atoi(env_val);
    env_val = getenv("BENCH_PARAM_precision");
    if (env_val) precision = env_val;
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
    fprintf(stderr, "CFD Euler solver  |  Cells: %d  |  Neighbors: %d  |  "
            "Iterations: 5 warmup + %d timed\n\n", N, NUM_NEIGHBORS);

    id<MTLCommandQueue> queue = [device newCommandQueue];

    // -------------------------------------------------------------------
    // Compile Metal shader
    // -------------------------------------------------------------------
    NSError* err = nil;
    id<MTLLibrary> library = nil;

    NSString* exePath = [[NSBundle mainBundle] executablePath];
    NSString* dir     = [exePath stringByDeletingLastPathComponent];
    NSString* srcPath = [dir stringByAppendingPathComponent:@"altis_cfd.metal"];

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
        fprintf(stderr, "Error: altis_cfd.metal not found at %s\n",
                srcPath.UTF8String);
        return EXIT_FAILURE;
    }

    id<MTLFunction> fnFlux = [library newFunctionWithName:@"compute_flux_kernel"];
    id<MTLFunction> fnRK   = [library newFunctionWithName:@"rk_update_kernel"];
    if (!fnFlux || !fnRK) {
        fprintf(stderr, "Error: kernel function not found\n");
        return EXIT_FAILURE;
    }

    id<MTLComputePipelineState> psoFlux =
        [device newComputePipelineStateWithFunction:fnFlux error:&err];
    id<MTLComputePipelineState> psoRK =
        [device newComputePipelineStateWithFunction:fnRK error:&err];
    if (!psoFlux || !psoRK) {
        fprintf(stderr, "Error creating pipeline: %s\n",
                err.localizedDescription.UTF8String);
        return EXIT_FAILURE;
    }

    // -------------------------------------------------------------------
    // Generate synthetic mesh data
    // -------------------------------------------------------------------
    size_t cell_bytes   = (size_t)N * sizeof(float);
    size_t neigh_bytes  = (size_t)N * NUM_NEIGHBORS * sizeof(int);
    size_t normal_bytes = (size_t)N * NUM_NEIGHBORS * 3 * sizeof(float);
    size_t area_bytes   = (size_t)N * NUM_NEIGHBORS * sizeof(float);

    std::vector<float> h_rho(N), h_mx(N), h_my(N), h_mz(N), h_energy(N);
    std::vector<float> h_volumes(N);
    std::vector<int>   h_neighbors(N * NUM_NEIGHBORS);
    std::vector<float> h_normals(N * NUM_NEIGHBORS * 3);
    std::vector<float> h_areas(N * NUM_NEIGHBORS);

    unsigned int seed = 42;

    for (int i = 0; i < N; ++i) {
        h_rho[i]    = 1.0f + 0.01f * rand_float(&seed, -1.0f, 1.0f);
        h_mx[i]     = 0.5f + 0.01f * rand_float(&seed, -1.0f, 1.0f);
        h_my[i]     = 0.0f + 0.01f * rand_float(&seed, -1.0f, 1.0f);
        h_mz[i]     = 0.0f + 0.01f * rand_float(&seed, -1.0f, 1.0f);
        float p = 1.0f / GAMMA;
        float ke = 0.5f * (h_mx[i]*h_mx[i] + h_my[i]*h_my[i] + h_mz[i]*h_mz[i]) / h_rho[i];
        h_energy[i] = p / GAMMA_M1 + ke;
        h_volumes[i] = 0.01f + 0.001f * rand_float(&seed, 0.0f, 1.0f);
    }

    for (int i = 0; i < N; ++i) {
        for (int f = 0; f < NUM_NEIGHBORS; ++f) {
            int j;
            do { j = lcg_rand(&seed) % N; } while (j == i);
            h_neighbors[i * NUM_NEIGHBORS + f] = j;

            float nx = rand_float(&seed, -1.0f, 1.0f);
            float ny = rand_float(&seed, -1.0f, 1.0f);
            float nz = rand_float(&seed, -1.0f, 1.0f);
            float len = sqrtf(nx*nx + ny*ny + nz*nz);
            if (len < 1e-6f) { nx = 1.0f; ny = 0.0f; nz = 0.0f; len = 1.0f; }
            int base = (i * NUM_NEIGHBORS + f) * 3;
            h_normals[base + 0] = nx / len;
            h_normals[base + 1] = ny / len;
            h_normals[base + 2] = nz / len;
            h_areas[i * NUM_NEIGHBORS + f] = 0.001f + 0.0005f * rand_float(&seed, 0.0f, 1.0f);
        }
    }

    // -------------------------------------------------------------------
    // Allocate Metal buffers
    // -------------------------------------------------------------------
    auto mkBuf = [&](size_t sz) {
        return [device newBufferWithLength:sz options:MTLResourceStorageModeShared];
    };

    // State arrays (current + old)
    id<MTLBuffer> bufRho      = mkBuf(cell_bytes);
    id<MTLBuffer> bufMx       = mkBuf(cell_bytes);
    id<MTLBuffer> bufMy       = mkBuf(cell_bytes);
    id<MTLBuffer> bufMz       = mkBuf(cell_bytes);
    id<MTLBuffer> bufEnergy   = mkBuf(cell_bytes);
    id<MTLBuffer> bufRhoOld   = mkBuf(cell_bytes);
    id<MTLBuffer> bufMxOld    = mkBuf(cell_bytes);
    id<MTLBuffer> bufMyOld    = mkBuf(cell_bytes);
    id<MTLBuffer> bufMzOld    = mkBuf(cell_bytes);
    id<MTLBuffer> bufEnergyOld= mkBuf(cell_bytes);

    // Flux arrays
    id<MTLBuffer> bufFluxRho  = mkBuf(cell_bytes);
    id<MTLBuffer> bufFluxMx   = mkBuf(cell_bytes);
    id<MTLBuffer> bufFluxMy   = mkBuf(cell_bytes);
    id<MTLBuffer> bufFluxMz   = mkBuf(cell_bytes);
    id<MTLBuffer> bufFluxE    = mkBuf(cell_bytes);

    // Mesh data
    id<MTLBuffer> bufNeighbors = mkBuf(neigh_bytes);
    id<MTLBuffer> bufNormals   = mkBuf(normal_bytes);
    id<MTLBuffer> bufAreas     = mkBuf(area_bytes);
    id<MTLBuffer> bufVolumes   = mkBuf(cell_bytes);

    // Copy mesh data
    memcpy(bufNeighbors.contents, h_neighbors.data(), neigh_bytes);
    memcpy(bufNormals.contents,   h_normals.data(),   normal_bytes);
    memcpy(bufAreas.contents,     h_areas.data(),     area_bytes);
    memcpy(bufVolumes.contents,   h_volumes.data(),   cell_bytes);

    NSUInteger tgSize = 256;
    NSUInteger gridN  = (NSUInteger)N;

    float rk_coeffs[RK_STEPS] = {0.0f, 0.5f};

    // Upload initial state
    auto upload_initial = [&]() {
        memcpy(bufRho.contents,    h_rho.data(),    cell_bytes);
        memcpy(bufMx.contents,     h_mx.data(),     cell_bytes);
        memcpy(bufMy.contents,     h_my.data(),     cell_bytes);
        memcpy(bufMz.contents,     h_mz.data(),     cell_bytes);
        memcpy(bufEnergy.contents, h_energy.data(), cell_bytes);
    };

    // Run one RK time step
    auto run_cfd_step = [&]() {
        // Save old state
        memcpy(bufRhoOld.contents,    bufRho.contents,    cell_bytes);
        memcpy(bufMxOld.contents,     bufMx.contents,     cell_bytes);
        memcpy(bufMyOld.contents,     bufMy.contents,     cell_bytes);
        memcpy(bufMzOld.contents,     bufMz.contents,     cell_bytes);
        memcpy(bufEnergyOld.contents, bufEnergy.contents, cell_bytes);

        for (int stage = 0; stage < RK_STEPS; ++stage) {
            CfdParams fluxParams = { (uint32_t)N, DT, rk_coeffs[stage] };
            CfdParams rkParams   = { (uint32_t)N, DT, rk_coeffs[stage] };

            id<MTLCommandBuffer> cb = [queue commandBuffer];
            id<MTLComputeCommandEncoder> enc = [cb computeCommandEncoder];

            // Compute flux
            [enc setComputePipelineState:psoFlux];
            [enc setBuffer:bufRho      offset:0 atIndex:0];
            [enc setBuffer:bufMx       offset:0 atIndex:1];
            [enc setBuffer:bufMy       offset:0 atIndex:2];
            [enc setBuffer:bufMz       offset:0 atIndex:3];
            [enc setBuffer:bufEnergy   offset:0 atIndex:4];
            [enc setBuffer:bufNeighbors offset:0 atIndex:5];
            [enc setBuffer:bufNormals  offset:0 atIndex:6];
            [enc setBuffer:bufAreas    offset:0 atIndex:7];
            [enc setBuffer:bufFluxRho  offset:0 atIndex:8];
            [enc setBuffer:bufFluxMx   offset:0 atIndex:9];
            [enc setBuffer:bufFluxMy   offset:0 atIndex:10];
            [enc setBuffer:bufFluxMz   offset:0 atIndex:11];
            [enc setBuffer:bufFluxE    offset:0 atIndex:12];
            [enc setBytes:&fluxParams  length:sizeof(fluxParams) atIndex:13];
            [enc dispatchThreads:MTLSizeMake(gridN, 1, 1)
                threadsPerThreadgroup:MTLSizeMake(tgSize, 1, 1)];

            // RK update
            [enc setComputePipelineState:psoRK];
            [enc setBuffer:bufRho      offset:0 atIndex:0];
            [enc setBuffer:bufMx       offset:0 atIndex:1];
            [enc setBuffer:bufMy       offset:0 atIndex:2];
            [enc setBuffer:bufMz       offset:0 atIndex:3];
            [enc setBuffer:bufEnergy   offset:0 atIndex:4];
            [enc setBuffer:bufRhoOld   offset:0 atIndex:5];
            [enc setBuffer:bufMxOld    offset:0 atIndex:6];
            [enc setBuffer:bufMyOld    offset:0 atIndex:7];
            [enc setBuffer:bufMzOld    offset:0 atIndex:8];
            [enc setBuffer:bufEnergyOld offset:0 atIndex:9];
            [enc setBuffer:bufFluxRho  offset:0 atIndex:10];
            [enc setBuffer:bufFluxMx   offset:0 atIndex:11];
            [enc setBuffer:bufFluxMy   offset:0 atIndex:12];
            [enc setBuffer:bufFluxMz   offset:0 atIndex:13];
            [enc setBuffer:bufFluxE    offset:0 atIndex:14];
            [enc setBuffer:bufVolumes  offset:0 atIndex:15];
            [enc setBytes:&rkParams    length:sizeof(rkParams) atIndex:16];
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
        upload_initial();
        run_cfd_step();
    }

    // -------------------------------------------------------------------
    // Timed iterations
    // -------------------------------------------------------------------
    std::vector<double> times(1);
    for (int i = 0; i < 1; ++i) {
        upload_initial();

        uint64_t t0 = mach_absolute_time();
        run_cfd_step();
        uint64_t t1 = mach_absolute_time();
        times[i] = ticks_to_ms(t1 - t0);
    }

    double sum = 0.0;
    for (int i = 0; i < 1; ++i) sum += times[i];
    double avg_ms = sum / 1;

    double mcells_per_sec = (double)N / (avg_ms * 1e-3) / 1e6;

    double total_ms = sum;

    // JSON-lines kernel events (one per kernel)
    printf("{\"type\":\"kernel\",\"name\":\"compute_flux_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"size\":%d\"block_size\":%d,"
           "\"precision\":\"%s\"}}\n",
           avg_ms, N, block_size, precision);

    printf("{\"type\":\"kernel\",\"name\":\"rk_update_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"size\":%d\"block_size\":%d,"
           "\"precision\":\"%s\"}}\n",
           avg_ms, N, block_size, precision);

    // JSON-lines summary event
    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"mcells_per_sec\",\"value\":%.2f}]}\n",
           total_ms, mcells_per_sec);

    fprintf(stderr, "Average time: %.4f ms\n", avg_ms);
    fprintf(stderr, "Throughput:   %.4f Mcells/sec\n", mcells_per_sec);

    // -------------------------------------------------------------------
    // Verification: compare GPU flux for first 256 cells with CPU
    // -------------------------------------------------------------------
    {
        int verify_n = (N < 256) ? N : 256;

        // Re-upload and compute flux
        upload_initial();

        CfdParams fluxParams = { (uint32_t)N, DT, 0.0f };
        id<MTLCommandBuffer> cb = [queue commandBuffer];
        id<MTLComputeCommandEncoder> enc = [cb computeCommandEncoder];

        [enc setComputePipelineState:psoFlux];
        [enc setBuffer:bufRho      offset:0 atIndex:0];
        [enc setBuffer:bufMx       offset:0 atIndex:1];
        [enc setBuffer:bufMy       offset:0 atIndex:2];
        [enc setBuffer:bufMz       offset:0 atIndex:3];
        [enc setBuffer:bufEnergy   offset:0 atIndex:4];
        [enc setBuffer:bufNeighbors offset:0 atIndex:5];
        [enc setBuffer:bufNormals  offset:0 atIndex:6];
        [enc setBuffer:bufAreas    offset:0 atIndex:7];
        [enc setBuffer:bufFluxRho  offset:0 atIndex:8];
        [enc setBuffer:bufFluxMx   offset:0 atIndex:9];
        [enc setBuffer:bufFluxMy   offset:0 atIndex:10];
        [enc setBuffer:bufFluxMz   offset:0 atIndex:11];
        [enc setBuffer:bufFluxE    offset:0 atIndex:12];
        [enc setBytes:&fluxParams  length:sizeof(fluxParams) atIndex:13];
        [enc dispatchThreads:MTLSizeMake(gridN, 1, 1)
            threadsPerThreadgroup:MTLSizeMake(tgSize, 1, 1)];

        [enc endEncoding];
        [cb commit];
        [cb waitUntilCompleted];

        const float* gpuFluxRho = (const float*)bufFluxRho.contents;
        const float* gpuFluxMx  = (const float*)bufFluxMx.contents;

        std::vector<float> cpu_f_rho(verify_n), cpu_f_mx(verify_n);
        std::vector<float> cpu_f_my(verify_n), cpu_f_mz(verify_n), cpu_f_e(verify_n);

        for (int i = 0; i < verify_n; ++i) {
            cpu_compute_flux(h_rho.data(), h_mx.data(), h_my.data(), h_mz.data(),
                             h_energy.data(), h_neighbors.data(), h_normals.data(),
                             h_areas.data(), cpu_f_rho.data(), cpu_f_mx.data(),
                             cpu_f_my.data(), cpu_f_mz.data(), cpu_f_e.data(), i);
        }

        int errors = 0;
        for (int i = 0; i < verify_n; ++i) {
            float diff_rho = fabsf(gpuFluxRho[i] - cpu_f_rho[i]);
            float diff_mx  = fabsf(gpuFluxMx[i]  - cpu_f_mx[i]);
            float tol = 1e-3f * (fabsf(cpu_f_rho[i]) + fabsf(cpu_f_mx[i]) + 1e-6f);
            if (diff_rho > tol || diff_mx > tol) {
                if (errors < 10) {
                    fprintf(stderr, "Mismatch at cell %d: "
                            "GPU_rho=%.6f CPU_rho=%.6f  GPU_mx=%.6f CPU_mx=%.6f\n",
                            i, gpuFluxRho[i], cpu_f_rho[i],
                            gpuFluxMx[i], cpu_f_mx[i]);
                }
                errors++;
            }
        }

        if (errors > 0)
            fprintf(stderr, "FAIL: %d errors in first %d cells\n", errors, verify_n);
        else
            fprintf(stderr, "PASS\n");
    }

    return EXIT_SUCCESS;

    } // @autoreleasepool
}
