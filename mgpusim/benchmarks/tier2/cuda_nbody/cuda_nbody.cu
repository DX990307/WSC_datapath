// cuda_nbody.cu — CUDA N-body gravitational simulation benchmark
//
// All-pairs N-body with tile-based shared memory optimization.
// Each thread computes the force on one body from all others.
//
// Native CUDA implementation.
//
// Usage:
//   ./cuda_nbody [--bodies N] [--iterations I] [--timesteps T]
//
//   --bodies N       Number of bodies (default: 16384)
//   --iterations I   Timed iterations (default: 5)
//   --timesteps T    Simulation timesteps per run (default: 10)
//
// Output (stdout): CSV row — cuda_nbody,<N>,<time_ms>,<GFLOPS>
// Output (stderr): human-readable results

#include <cstdio>
#include <cstdint>
#include <cstdlib>
#include <cstring>
#include <cmath>
#include <vector>
#include <cuda_runtime.h>

// ---------------------------------------------------------------------------
// Error-checking macro
// ---------------------------------------------------------------------------

#define CUDA_CHECK(cmd)                                                         \
    do {                                                                       \
        cudaError_t _e = (cmd);                                                 \
        if (_e != cudaSuccess) {                                                \
            fprintf(stderr, "CUDA error %s at %s:%d\n",                        \
                    cudaGetErrorString(_e), __FILE__, __LINE__);                \
            exit(1);                                                           \
        }                                                                      \
    } while (0)

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
// Kernel: tile-based N-body force computation + velocity/position update
//   Uses shared memory to cache tiles of positions for coalesced access.
// ---------------------------------------------------------------------------

__global__ void nbody_kernel(float4* __restrict__ pos,
                              float4* __restrict__ vel,
                              int N) {
    int i = blockIdx.x * blockDim.x + threadIdx.x;

    float4 myPos;
    if (i < N) myPos = pos[i];

    float3 acc = make_float3(0.0f, 0.0f, 0.0f);

    __shared__ float4 shPos[TILE_SIZE];

    for (int tile = 0; tile < N; tile += TILE_SIZE) {
        int idx = tile + threadIdx.x;
        if (idx < N)
            shPos[threadIdx.x] = pos[idx];
        else
            shPos[threadIdx.x] = make_float4(0.0f, 0.0f, 0.0f, 0.0f);

        __syncthreads();

        if (i < N) {
            for (int j = 0; j < TILE_SIZE; ++j) {
                float dx = shPos[j].x - myPos.x;
                float dy = shPos[j].y - myPos.y;
                float dz = shPos[j].z - myPos.z;

                float distSqr = dx * dx + dy * dy + dz * dz + SOFTENING;
                float invDist  = rsqrtf(distSqr);
                float invDist3 = invDist * invDist * invDist;
                float mass_j   = shPos[j].w;

                acc.x += dx * invDist3 * mass_j;
                acc.y += dy * invDist3 * mass_j;
                acc.z += dz * invDist3 * mass_j;
            }
        }

        __syncthreads();
    }

    if (i < N) {
        float4 myVel = vel[i];
        myVel.x += acc.x * DT;
        myVel.y += acc.y * DT;
        myVel.z += acc.z * DT;

        myPos.x += myVel.x * DT;
        myPos.y += myVel.y * DT;
        myPos.z += myVel.z * DT;

        pos[i] = myPos;
        vel[i] = myVel;
    }
}

// ---------------------------------------------------------------------------
// CPU reference N-body for verification
// ---------------------------------------------------------------------------

static void nbody_cpu(float4* pos, float4* vel, int N) {
    std::vector<float3> acc(N);
    for (int ii = 0; ii < N; ++ii) acc[ii] = make_float3(0.0f, 0.0f, 0.0f);

    for (int i = 0; i < N; ++i) {
        for (int j = 0; j < N; ++j) {
            float dx = pos[j].x - pos[i].x;
            float dy = pos[j].y - pos[i].y;
            float dz = pos[j].z - pos[i].z;

            float distSqr = dx * dx + dy * dy + dz * dz + SOFTENING;
            float invDist  = 1.0f / sqrtf(distSqr);
            float invDist3 = invDist * invDist * invDist;
            float mass_j   = pos[j].w;

            acc[i].x += dx * invDist3 * mass_j;
            acc[i].y += dy * invDist3 * mass_j;
            acc[i].z += dz * invDist3 * mass_j;
        }
    }

    for (int i = 0; i < N; ++i) {
        vel[i].x += acc[i].x * DT;
        vel[i].y += acc[i].y * DT;
        vel[i].z += acc[i].z * DT;

        pos[i].x += vel[i].x * DT;
        pos[i].y += vel[i].y * DT;
        pos[i].z += vel[i].z * DT;
    }
}

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
// Main
// ---------------------------------------------------------------------------

int main(int argc, char** argv) {
    setvbuf(stdout, NULL, _IONBF, 0);

    // Default values from params.json
    int N          = 16384;
    int timesteps  = 10;
    int iters      = 5;
    int block_size = 256;

    // Command-line args as fallback
    N          = parseIntParam(argc, argv, "--bodies", N);
    iters      = parseIntParam(argc, argv, "--iterations", iters);
    timesteps  = parseIntParam(argc, argv, "--timesteps", timesteps);
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

    // Print device info
    int device_id = 0;
    CUDA_CHECK(cudaGetDevice(&device_id));
    cudaDeviceProp prop;
    CUDA_CHECK(cudaGetDeviceProperties(&prop, device_id));
    fprintf(stderr, "Device: %s (id=%d)\n", prop.name, device_id);
    fprintf(stderr, "Bodies: %d  |  Timesteps: %d  |  Iterations: 5 warmup + %d timed\n\n",
            N, timesteps, iters);

    size_t bytes = (size_t)N * sizeof(float4);

    // Host allocation + initialization
    std::vector<float4> h_pos(N), h_vel(N);
    std::vector<float4> h_pos_orig(N), h_vel_orig(N);
    uint32_t rng = 42;

    for (int i = 0; i < N; ++i) {
        h_pos[i].x = rand_float(rng);
        h_pos[i].y = rand_float(rng);
        h_pos[i].z = rand_float(rng);
        h_pos[i].w = 1.0f;  // mass
        h_vel[i] = make_float4(0.0f, 0.0f, 0.0f, 0.0f);
        h_pos_orig[i] = h_pos[i];
        h_vel_orig[i] = h_vel[i];
    }

    // Device allocation
    float4 *d_pos, *d_vel;
    CUDA_CHECK(cudaMalloc(&d_pos, bytes));
    CUDA_CHECK(cudaMalloc(&d_vel, bytes));

    int blockSize = TILE_SIZE;
    int gridSize  = (N + blockSize - 1) / blockSize;

    // Lambda: run full simulation (timesteps steps)
    auto run_nbody = [&]() {
        CUDA_CHECK(cudaMemcpy(d_pos, h_pos_orig.data(), bytes, cudaMemcpyHostToDevice));
        CUDA_CHECK(cudaMemcpy(d_vel, h_vel_orig.data(), bytes, cudaMemcpyHostToDevice));

        for (int t = 0; t < timesteps; ++t) {
            nbody_kernel<<<dim3(gridSize), dim3(blockSize), 0, 0>>>(d_pos, d_vel, N);
        }
    };

    // -------------------------------------------------------------------
    // Warmup
    // -------------------------------------------------------------------
    for (int w = 0; w < num_warmup; ++w) {
        run_nbody();
        CUDA_CHECK(cudaDeviceSynchronize());
    }

    // -------------------------------------------------------------------
    // Timed iterations
    // -------------------------------------------------------------------
    cudaEvent_t evStart, evStop;
    CUDA_CHECK(cudaEventCreate(&evStart));
    CUDA_CHECK(cudaEventCreate(&evStop));

    std::vector<double> times((size_t)iters);
    for (int i = 0; i < iters; ++i) {
        CUDA_CHECK(cudaEventRecord(evStart, 0));
        run_nbody();
        CUDA_CHECK(cudaEventRecord(evStop, 0));
        CUDA_CHECK(cudaEventSynchronize(evStop));

        float ms = 0.0f;
        CUDA_CHECK(cudaEventElapsedTime(&ms, evStart, evStop));
        times[i] = (double)ms;
    }

    CUDA_CHECK(cudaEventDestroy(evStart));
    CUDA_CHECK(cudaEventDestroy(evStop));

    // Compute average time
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
    // Verification: run 1 timestep on CPU with small subset, compare
    // -------------------------------------------------------------------
    int verifyN = (N <= 4096) ? N : 4096;  // limit CPU verification size
    {
        // Re-init from originals
        std::vector<float4> cpuPos(h_pos_orig.begin(), h_pos_orig.begin() + verifyN);
        std::vector<float4> cpuVel(h_vel_orig.begin(), h_vel_orig.begin() + verifyN);

        // GPU: run 1 timestep on verifyN bodies
        float4 *d_vpos, *d_vvel;
        size_t vbytes = verifyN * sizeof(float4);
        CUDA_CHECK(cudaMalloc(&d_vpos, vbytes));
        CUDA_CHECK(cudaMalloc(&d_vvel, vbytes));
        CUDA_CHECK(cudaMemcpy(d_vpos, cpuPos.data(), vbytes, cudaMemcpyHostToDevice));
        CUDA_CHECK(cudaMemcpy(d_vvel, cpuVel.data(), vbytes, cudaMemcpyHostToDevice));

        int vgrid = (verifyN + blockSize - 1) / blockSize;
        nbody_kernel<<<dim3(vgrid), dim3(blockSize), 0, 0>>>(d_vpos, d_vvel, verifyN);
        CUDA_CHECK(cudaDeviceSynchronize());

        std::vector<float4> gpuPos(verifyN);
        CUDA_CHECK(cudaMemcpy(gpuPos.data(), d_vpos, vbytes, cudaMemcpyDeviceToHost));

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

        CUDA_CHECK(cudaFree(d_vpos));
        CUDA_CHECK(cudaFree(d_vvel));
    }

    CUDA_CHECK(cudaFree(d_pos));
    CUDA_CHECK(cudaFree(d_vel));

    return 0;
}
