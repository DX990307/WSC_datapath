// lonestar_dmr.hip — LoneStar Delaunay Mesh Refinement benchmark (HIP)
//
// Worklist-based irregular mesh refinement. Generates a synthetic Delaunay
// triangulation, identifies "bad" triangles (minimum angle below threshold),
// and refines them by inserting circumcenters (simplified Ruppert's algorithm).
//
// Compiles with hipcc (AMD ROCm) or nvcc via HIP compatibility layer (CUDA).

#if defined(__NVCC__) && !__has_include(<hip/hip_runtime.h>)
#include <cuda_runtime.h>
#define hipMalloc               cudaMalloc
#define hipFree                 cudaFree
#define hipMemcpy               cudaMemcpy
#define hipMemset               cudaMemset
#define hipMemcpyHostToDevice   cudaMemcpyHostToDevice
#define hipMemcpyDeviceToHost   cudaMemcpyDeviceToHost
#define hipDeviceSynchronize    cudaDeviceSynchronize
#define hipGetDevice            cudaGetDevice
#define hipGetDeviceProperties  cudaGetDeviceProperties
#define hipGetErrorString       cudaGetErrorString
#define hipEvent_t              cudaEvent_t
#define hipEventCreate          cudaEventCreate
#define hipEventDestroy         cudaEventDestroy
#define hipEventRecord          cudaEventRecord
#define hipEventSynchronize     cudaEventSynchronize
#define hipEventElapsedTime     cudaEventElapsedTime
#define hipStream_t             cudaStream_t
#define hipDeviceProp_t         cudaDeviceProp
#define hipError_t              cudaError_t
#define hipSuccess              cudaSuccess
#define hipLaunchKernelGGL(kernel, grid, block, smem, stream, ...) \
    kernel<<<grid, block, smem, stream>>>(__VA_ARGS__)
#else
#include <hip/hip_runtime.h>
#endif
#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <cmath>
#include <cfloat>
#include <vector>

// ---------------------------------------------------------------------------
// Error-checking macro
// ---------------------------------------------------------------------------

#define HIP_CHECK(cmd)                                                         \
    do {                                                                       \
        hipError_t _e = (cmd);                                                 \
        if (_e != hipSuccess) {                                                \
            fprintf(stderr, "HIP error %s at %s:%d\n",                        \
                    hipGetErrorString(_e), __FILE__, __LINE__);                \
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
// Triangle geometry helpers
// ---------------------------------------------------------------------------

__device__ __host__ static inline float compute_min_angle(
    float x0, float y0, float x1, float y1, float x2, float y2)
{
    float ax = x1 - x0, ay = y1 - y0;
    float bx = x2 - x0, by = y2 - y0;
    float cx = x2 - x1, cy = y2 - y1;

    float la = sqrtf(ax * ax + ay * ay);
    float lb = sqrtf(bx * bx + by * by);
    float lc = sqrtf(cx * cx + cy * cy);

    if (la < 1e-10f || lb < 1e-10f || lc < 1e-10f) return 0.0f;

    float cos_A = (ax * bx + ay * by) / (la * lb);
    float cos_B = (-ax * cx + -ay * cy) / (la * lc);
    float cos_C = (-bx * (-cx) + -by * (-cy)) / (lb * lc);

    cos_A = fminf(fmaxf(cos_A, -1.0f), 1.0f);
    cos_B = fminf(fmaxf(cos_B, -1.0f), 1.0f);
    cos_C = fminf(fmaxf(cos_C, -1.0f), 1.0f);

    float a_A = acosf(cos_A) * (180.0f / 3.14159265f);
    float a_B = acosf(cos_B) * (180.0f / 3.14159265f);
    float a_C = acosf(cos_C) * (180.0f / 3.14159265f);

    return fminf(a_A, fminf(a_B, a_C));
}

__device__ __host__ static inline void compute_circumcenter(
    float x0, float y0, float x1, float y1, float x2, float y2,
    float* cx, float* cy)
{
    float D = 2.0f * (x0 * (y1 - y2) + x1 * (y2 - y0) + x2 * (y0 - y1));
    if (fabsf(D) < 1e-10f) {
        *cx = (x0 + x1 + x2) / 3.0f;
        *cy = (y0 + y1 + y2) / 3.0f;
        return;
    }
    float sq0 = x0 * x0 + y0 * y0;
    float sq1 = x1 * x1 + y1 * y1;
    float sq2 = x2 * x2 + y2 * y2;
    *cx = (sq0 * (y1 - y2) + sq1 * (y2 - y0) + sq2 * (y0 - y1)) / D;
    *cy = (sq0 * (x2 - x1) + sq1 * (x0 - x2) + sq2 * (x1 - x0)) / D;
}

// ---------------------------------------------------------------------------
// Kernel 1: Check all triangles, mark bad ones
// ---------------------------------------------------------------------------

__global__ void dmr_check_kernel(
    const int*   __restrict__ tri_v0,
    const int*   __restrict__ tri_v1,
    const int*   __restrict__ tri_v2,
    const float* __restrict__ vx,
    const float* __restrict__ vy,
    int*   __restrict__ worklist,
    int*   __restrict__ worklist_count,
    float  min_angle_threshold,
    int    num_triangles)
{
    int idx = blockIdx.x * blockDim.x + threadIdx.x;
    if (idx >= num_triangles) return;

    int v0 = tri_v0[idx];
    int v1 = tri_v1[idx];
    int v2 = tri_v2[idx];

    float angle = compute_min_angle(
        vx[v0], vy[v0], vx[v1], vy[v1], vx[v2], vy[v2]);

    if (angle < min_angle_threshold) {
        int pos = atomicAdd(worklist_count, 1);
        worklist[pos] = idx;
    }
}

// ---------------------------------------------------------------------------
// Kernel 2: Refine bad triangles
// ---------------------------------------------------------------------------

__global__ void dmr_refine_kernel(
    const int*   __restrict__ worklist,
    int          worklist_size,
    int*   __restrict__ tri_v0,
    int*   __restrict__ tri_v1,
    int*   __restrict__ tri_v2,
    float* __restrict__ vx,
    float* __restrict__ vy,
    int    num_vertices,
    int    num_triangles)
{
    int idx = blockIdx.x * blockDim.x + threadIdx.x;
    if (idx >= worklist_size) return;

    int tri_idx = worklist[idx];
    if (tri_idx < 0 || tri_idx >= num_triangles) return;

    int v0 = tri_v0[tri_idx];
    int v1 = tri_v1[tri_idx];
    int v2 = tri_v2[tri_idx];

    float cx, cy;
    compute_circumcenter(
        vx[v0], vy[v0], vx[v1], vy[v1], vx[v2], vy[v2], &cx, &cy);

    float blend = 0.1f;
    atomicAdd(&vx[v0], blend * (cx - vx[v0]));
    atomicAdd(&vy[v0], blend * (cy - vy[v0]));
    atomicAdd(&vx[v1], blend * (cx - vx[v1]));
    atomicAdd(&vy[v1], blend * (cy - vy[v1]));
    atomicAdd(&vx[v2], blend * (cx - vx[v2]));
    atomicAdd(&vy[v2], blend * (cy - vy[v2]));
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------

int main(int argc, char** argv) {
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
    int num_warmup = 5;

    int num_vertices = num_triangles * 2;

    int device_id = 0;
    HIP_CHECK(hipGetDevice(&device_id));
    hipDeviceProp_t prop;
    HIP_CHECK(hipGetDeviceProperties(&prop, device_id));
    fprintf(stderr, "Device: %s (id=%d)\n", prop.name, device_id);
    fprintf(stderr, "DMR: Delaunay Mesh Refinement  |  Triangles: %d  |  "
            "Vertices: %d  |  min_angle: %.1f deg  |  refine_iters: %d  |  "
            "Iterations: 5 warmup + %d timed\n\n",
            num_triangles, num_vertices, min_angle, max_refine_iters);

    // Generate synthetic mesh data
    std::vector<float> h_vx(num_vertices), h_vy(num_vertices);
    std::vector<int> h_tri_v0(num_triangles), h_tri_v1(num_triangles), h_tri_v2(num_triangles);

    unsigned int seed = 42;
    for (int i = 0; i < num_vertices; ++i) {
        h_vx[i] = rand_float(&seed, 0.0f, 100.0f);
        h_vy[i] = rand_float(&seed, 0.0f, 100.0f);
    }
    for (int i = 0; i < num_triangles; ++i) {
        int v0, v1, v2;
        v0 = lcg_rand(&seed) % num_vertices;
        do { v1 = lcg_rand(&seed) % num_vertices; } while (v1 == v0);
        do { v2 = lcg_rand(&seed) % num_vertices; } while (v2 == v0 || v2 == v1);
        h_tri_v0[i] = v0;
        h_tri_v1[i] = v1;
        h_tri_v2[i] = v2;
    }

    std::vector<float> h_vx_bak = h_vx;
    std::vector<float> h_vy_bak = h_vy;

    size_t vert_bytes = (size_t)num_vertices * sizeof(float);
    size_t tri_bytes  = (size_t)num_triangles * sizeof(int);

    float *d_vx, *d_vy;
    int *d_tri_v0, *d_tri_v1, *d_tri_v2;
    int *d_worklist, *d_worklist_count;

    HIP_CHECK(hipMalloc(&d_vx, vert_bytes));
    HIP_CHECK(hipMalloc(&d_vy, vert_bytes));
    HIP_CHECK(hipMalloc(&d_tri_v0, tri_bytes));
    HIP_CHECK(hipMalloc(&d_tri_v1, tri_bytes));
    HIP_CHECK(hipMalloc(&d_tri_v2, tri_bytes));
    HIP_CHECK(hipMalloc(&d_worklist, tri_bytes));
    HIP_CHECK(hipMalloc(&d_worklist_count, sizeof(int)));

    HIP_CHECK(hipMemcpy(d_tri_v0, h_tri_v0.data(), tri_bytes, hipMemcpyHostToDevice));
    HIP_CHECK(hipMemcpy(d_tri_v1, h_tri_v1.data(), tri_bytes, hipMemcpyHostToDevice));
    HIP_CHECK(hipMemcpy(d_tri_v2, h_tri_v2.data(), tri_bytes, hipMemcpyHostToDevice));

    int gridTri = (num_triangles + block_size - 1) / block_size;

    // Run the DMR refinement loop. When timing events are supplied, each kernel
    // launch is wrapped in its own event pair and its elapsed time is added to
    // the matching accumulator, so each distinct kernel reports its OWN
    // separately measured GPU time (summed across the refine loop).
    auto run_dmr = [&](hipEvent_t* evStart, hipEvent_t* evStop,
                       double* t_check, double* t_refine) {
        HIP_CHECK(hipMemcpy(d_vx, h_vx_bak.data(), vert_bytes, hipMemcpyHostToDevice));
        HIP_CHECK(hipMemcpy(d_vy, h_vy_bak.data(), vert_bytes, hipMemcpyHostToDevice));

        for (int r = 0; r < max_refine_iters; ++r) {
            HIP_CHECK(hipMemset(d_worklist_count, 0, sizeof(int)));

            if (evStart) HIP_CHECK(hipEventRecord(*evStart, 0));
            hipLaunchKernelGGL(dmr_check_kernel, dim3(gridTri), dim3(block_size), 0, 0,
                d_tri_v0, d_tri_v1, d_tri_v2,
                d_vx, d_vy,
                d_worklist, d_worklist_count,
                min_angle, num_triangles);
            if (evStart) {
                HIP_CHECK(hipEventRecord(*evStop, 0));
                HIP_CHECK(hipEventSynchronize(*evStop));
                float ms = 0.0f;
                HIP_CHECK(hipEventElapsedTime(&ms, *evStart, *evStop));
                *t_check += (double)ms;
            }

            int h_wl_count = 0;
            HIP_CHECK(hipMemcpy(&h_wl_count, d_worklist_count, sizeof(int),
                                hipMemcpyDeviceToHost));

            if (h_wl_count == 0) break;

            int gridWL = (h_wl_count + block_size - 1) / block_size;
            if (evStart) HIP_CHECK(hipEventRecord(*evStart, 0));
            hipLaunchKernelGGL(dmr_refine_kernel, dim3(gridWL), dim3(block_size), 0, 0,
                d_worklist, h_wl_count,
                d_tri_v0, d_tri_v1, d_tri_v2,
                d_vx, d_vy,
                num_vertices, num_triangles);
            if (evStart) {
                HIP_CHECK(hipEventRecord(*evStop, 0));
                HIP_CHECK(hipEventSynchronize(*evStop));
                float ms = 0.0f;
                HIP_CHECK(hipEventElapsedTime(&ms, *evStart, *evStop));
                *t_refine += (double)ms;
            }
        }
    };

    // Warmup
    for (int w = 0; w < num_warmup; ++w) {
        run_dmr(nullptr, nullptr, nullptr, nullptr);  // untimed full sequence
        HIP_CHECK(hipDeviceSynchronize());
    }

    // Timed iterations — each kernel measured separately with its own
    // accumulator (summed across the refine loop inside run_dmr).
    hipEvent_t evStart, evStop;
    HIP_CHECK(hipEventCreate(&evStart));
    HIP_CHECK(hipEventCreate(&evStop));

    double t_check = 0.0;   // dmr_check_kernel
    double t_refine = 0.0;  // dmr_refine_kernel
    for (int i = 0; i < 1; ++i) {
        run_dmr(&evStart, &evStop, &t_check, &t_refine);
    }

    HIP_CHECK(hipEventDestroy(evStart));
    HIP_CHECK(hipEventDestroy(evStop));

    double total_ms = t_check + t_refine;
    double avg_ms = total_ms;
    double mtri_per_sec = (double)num_triangles / (avg_ms * 1e-3) / 1e6;

    fprintf(stderr, "Performance: %.2f Mtri/sec  (total %.4f ms: check=%.4f refine=%.4f)\n",
            mtri_per_sec, avg_ms, t_check, t_refine);

    printf("{\"type\":\"kernel\",\"name\":\"dmr_check_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"num_triangles\":%d,\"block_size\":%d,"
           "\"min_angle\":%.1f,\"max_refine_iterations\":%d,"
           "\"precision\":\"%s\"}}\n",
           t_check, num_triangles, block_size, min_angle,
           max_refine_iters, precision);

    printf("{\"type\":\"kernel\",\"name\":\"dmr_refine_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"num_triangles\":%d,\"block_size\":%d,"
           "\"min_angle\":%.1f,\"max_refine_iterations\":%d,"
           "\"precision\":\"%s\"}}\n",
           t_refine, num_triangles, block_size, min_angle,
           max_refine_iters, precision);

    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"mtri_per_sec\",\"value\":%.2f}]}\n",
           total_ms, mtri_per_sec);

    HIP_CHECK(hipFree(d_vx));
    HIP_CHECK(hipFree(d_vy));
    HIP_CHECK(hipFree(d_tri_v0));
    HIP_CHECK(hipFree(d_tri_v1));
    HIP_CHECK(hipFree(d_tri_v2));
    HIP_CHECK(hipFree(d_worklist));
    HIP_CHECK(hipFree(d_worklist_count));

    return 0;
}
