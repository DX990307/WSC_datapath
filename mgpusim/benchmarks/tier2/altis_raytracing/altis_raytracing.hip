// altis_raytracing.hip — Simple ray tracing benchmark (HIP)
//
// Casts rays from a camera through an image plane and intersects them with
// N randomly-placed spheres. Each thread handles one pixel (one ray), testing
// all spheres and shading the nearest hit with Phong lighting.
//
// Compiles with hipcc (AMD ROCm) or nvcc via HIP compatibility layer (CUDA).
//
// Usage:
//   ./altis_raytracing [--width W] [--height H] [--spheres S]
//
//   --width  W       Image width  (default: 1024)
//   --height H       Image height (default: 1024)
//   --spheres S      Number of spheres (default: 64)
//
// Output (stdout): CSV — altis_raytracing,<WxH>,<time_ms>,<Mrays_per_sec>
// Output (stderr): human-readable results

#if defined(__NVCC__) && !__has_include(<hip/hip_runtime.h>)
#include <cuda_runtime.h>
#define hipMalloc               cudaMalloc
#define hipFree                 cudaFree
#define hipMemcpy               cudaMemcpy
#define hipMemcpyAsync          cudaMemcpyAsync
#define hipMemset               cudaMemset
#define hipMemsetAsync          cudaMemsetAsync
#define hipMemcpyHostToDevice   cudaMemcpyHostToDevice
#define hipMemcpyDeviceToHost   cudaMemcpyDeviceToHost
#define hipMemcpyDeviceToDevice cudaMemcpyDeviceToDevice
#define hipMemcpyToSymbol       cudaMemcpyToSymbol
#define hipDeviceSynchronize    cudaDeviceSynchronize
#define hipGetDevice            cudaGetDevice
#define hipGetDeviceProperties  cudaGetDeviceProperties
#define hipGetLastError         cudaGetLastError
#define hipGetErrorString       cudaGetErrorString
#define hipEvent_t              cudaEvent_t
#define hipEventCreate          cudaEventCreate
#define hipEventDestroy         cudaEventDestroy
#define hipEventRecord          cudaEventRecord
#define hipEventSynchronize     cudaEventSynchronize
#define hipEventElapsedTime     cudaEventElapsedTime
#define hipStream_t             cudaStream_t
#define hipStreamCreate         cudaStreamCreate
#define hipStreamDestroy        cudaStreamDestroy
#define hipStreamSynchronize    cudaStreamSynchronize
#define hipDeviceProp_t         cudaDeviceProp
#define hipError_t              cudaError_t
#define hipSuccess              cudaSuccess
#define hipHostMalloc           cudaMallocHost
#define hipHostFree             cudaFreeHost
#define hipHostMallocDefault    cudaHostAllocDefault
#define hipBlockIdx_x           blockIdx.x
#define hipBlockIdx_y           blockIdx.y
#define hipBlockIdx_z           blockIdx.z
#define hipThreadIdx_x          threadIdx.x
#define hipThreadIdx_y          threadIdx.y
#define hipThreadIdx_z          threadIdx.z
#define hipBlockDim_x           blockDim.x
#define hipBlockDim_y           blockDim.y
#define hipBlockDim_z           blockDim.z
#define hipGridDim_x            gridDim.x
#define hipGridDim_y            gridDim.y
#define hipGridDim_z            gridDim.z
#define __HIP_PLATFORM_AMD__    0
#define hipLaunchKernelGGL(kernel, grid, block, smem, stream, ...) \
    kernel<<<grid, block, smem, stream>>>(__VA_ARGS__)
#else
#include <hip/hip_runtime.h>
#endif
#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <cmath>
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

// ---------------------------------------------------------------------------
// Sphere data structure
// ---------------------------------------------------------------------------

struct Sphere {
    float cx, cy, cz;  // center
    float radius;
    float r, g, b;     // color
};

// ---------------------------------------------------------------------------
// Device: ray-sphere intersection
// Returns t (distance) or -1 if no hit.
// ---------------------------------------------------------------------------

__device__ static float intersect_sphere(
    float ox, float oy, float oz,   // ray origin
    float dx, float dy, float dz,   // ray direction (normalized)
    float cx, float cy, float cz,   // sphere center
    float radius)
{
    float ex = ox - cx;
    float ey = oy - cy;
    float ez = oz - cz;

    float a = dx * dx + dy * dy + dz * dz;
    float b = 2.0f * (ex * dx + ey * dy + ez * dz);
    float c = ex * ex + ey * ey + ez * ez - radius * radius;

    float disc = b * b - 4.0f * a * c;
    if (disc < 0.0f) return -1.0f;

    float sq = sqrtf(disc);
    float t0 = (-b - sq) / (2.0f * a);
    float t1 = (-b + sq) / (2.0f * a);

    if (t0 > 0.001f) return t0;
    if (t1 > 0.001f) return t1;
    return -1.0f;
}

// ---------------------------------------------------------------------------
// Kernel: ray trace one pixel per thread
// ---------------------------------------------------------------------------

__global__ void raytrace_kernel(
    unsigned char* __restrict__ image,
    const Sphere* __restrict__ spheres,
    int width, int height, int num_spheres)
{
    int idx = blockIdx.x * blockDim.x + threadIdx.x;
    int total_pixels = width * height;
    if (idx >= total_pixels) return;

    int px = idx % width;
    int py = idx / width;

    // Camera setup: look along -Z, image plane at z = -1
    float aspect = (float)width / (float)height;
    float fov_scale = 1.0f;  // tan(45°) ≈ 1

    // Map pixel to normalized device coords [-1, 1]
    float u = (2.0f * ((float)px + 0.5f) / (float)width - 1.0f) * aspect * fov_scale;
    float v = (1.0f - 2.0f * ((float)py + 0.5f) / (float)height) * fov_scale;

    // Ray origin and direction
    float ox = 0.0f, oy = 0.0f, oz = 5.0f;
    float dx = u, dy = v, dz = -1.0f;

    // Normalize direction
    float len = sqrtf(dx * dx + dy * dy + dz * dz);
    dx /= len; dy /= len; dz /= len;

    // Find closest intersection
    float closest_t = 1e20f;
    int closest_id = -1;

    for (int s = 0; s < num_spheres; ++s) {
        float t = intersect_sphere(ox, oy, oz, dx, dy, dz,
                                   spheres[s].cx, spheres[s].cy, spheres[s].cz,
                                   spheres[s].radius);
        if (t > 0.0f && t < closest_t) {
            closest_t = t;
            closest_id = s;
        }
    }

    // Shade pixel
    float pr = 0.05f, pg = 0.05f, pb = 0.1f;  // background

    if (closest_id >= 0) {
        // Hit point
        float hx = ox + closest_t * dx;
        float hy = oy + closest_t * dy;
        float hz = oz + closest_t * dz;

        // Normal at hit point
        const Sphere& sp = spheres[closest_id];
        float nx = (hx - sp.cx) / sp.radius;
        float ny = (hy - sp.cy) / sp.radius;
        float nz = (hz - sp.cz) / sp.radius;

        // Light direction (normalized, from upper-right)
        float lx = 0.577f, ly = 0.577f, lz = 0.577f;

        // Diffuse (Lambertian)
        float ndotl = nx * lx + ny * ly + nz * lz;
        if (ndotl < 0.0f) ndotl = 0.0f;

        // Specular (Phong)
        // Reflect light around normal: R = 2(N·L)N - L
        float rx = 2.0f * ndotl * nx - lx;
        float ry = 2.0f * ndotl * ny - ly;
        float rz = 2.0f * ndotl * nz - lz;

        // View direction (from hit to camera)
        float vx = -dx, vy = -dy, vz = -dz;
        float rdotv = rx * vx + ry * vy + rz * vz;
        if (rdotv < 0.0f) rdotv = 0.0f;
        float spec = rdotv * rdotv * rdotv * rdotv;  // specular exponent ~4
        spec = spec * spec;  // exponent ~8

        float ambient = 0.15f;
        pr = sp.r * (ambient + 0.7f * ndotl) + 0.3f * spec;
        pg = sp.g * (ambient + 0.7f * ndotl) + 0.3f * spec;
        pb = sp.b * (ambient + 0.7f * ndotl) + 0.3f * spec;

        // Clamp
        if (pr > 1.0f) pr = 1.0f;
        if (pg > 1.0f) pg = 1.0f;
        if (pb > 1.0f) pb = 1.0f;
    }

    // Write RGBA
    int base = idx * 4;
    image[base + 0] = (unsigned char)(pr * 255.0f);
    image[base + 1] = (unsigned char)(pg * 255.0f);
    image[base + 2] = (unsigned char)(pb * 255.0f);
    image[base + 3] = 255;
}

// ---------------------------------------------------------------------------
// CPU reference: ray trace a single pixel
// ---------------------------------------------------------------------------

static float cpu_intersect_sphere(
    float ox, float oy, float oz,
    float dx, float dy, float dz,
    float cx, float cy, float cz,
    float radius)
{
    float ex = ox - cx, ey = oy - cy, ez = oz - cz;
    float a = dx * dx + dy * dy + dz * dz;
    float b = 2.0f * (ex * dx + ey * dy + ez * dz);
    float c = ex * ex + ey * ey + ez * ez - radius * radius;
    float disc = b * b - 4.0f * a * c;
    if (disc < 0.0f) return -1.0f;
    float sq = sqrtf(disc);
    float t0 = (-b - sq) / (2.0f * a);
    float t1 = (-b + sq) / (2.0f * a);
    if (t0 > 0.001f) return t0;
    if (t1 > 0.001f) return t1;
    return -1.0f;
}

static void cpu_raytrace_pixel(
    unsigned char* out,
    const Sphere* spheres, int num_spheres,
    int px, int py, int width, int height)
{
    float aspect = (float)width / (float)height;
    float fov_scale = 1.0f;
    float u = (2.0f * ((float)px + 0.5f) / (float)width - 1.0f) * aspect * fov_scale;
    float v = (1.0f - 2.0f * ((float)py + 0.5f) / (float)height) * fov_scale;

    float ox = 0.0f, oy = 0.0f, oz = 5.0f;
    float dx = u, dy = v, dz = -1.0f;
    float len = sqrtf(dx * dx + dy * dy + dz * dz);
    dx /= len; dy /= len; dz /= len;

    float closest_t = 1e20f;
    int closest_id = -1;
    for (int s = 0; s < num_spheres; ++s) {
        float t = cpu_intersect_sphere(ox, oy, oz, dx, dy, dz,
                                       spheres[s].cx, spheres[s].cy, spheres[s].cz,
                                       spheres[s].radius);
        if (t > 0.0f && t < closest_t) {
            closest_t = t;
            closest_id = s;
        }
    }

    float pr = 0.05f, pg = 0.05f, pb = 0.1f;
    if (closest_id >= 0) {
        float hx = ox + closest_t * dx;
        float hy = oy + closest_t * dy;
        float hz = oz + closest_t * dz;
        const Sphere& sp = spheres[closest_id];
        float nx = (hx - sp.cx) / sp.radius;
        float ny = (hy - sp.cy) / sp.radius;
        float nz = (hz - sp.cz) / sp.radius;
        float lx = 0.577f, ly = 0.577f, lz = 0.577f;
        float ndotl = nx * lx + ny * ly + nz * lz;
        if (ndotl < 0.0f) ndotl = 0.0f;
        float rx = 2.0f * ndotl * nx - lx;
        float ry = 2.0f * ndotl * ny - ly;
        float rz = 2.0f * ndotl * nz - lz;
        float vx = -dx, vy = -dy, vz = -dz;
        float rdotv = rx * vx + ry * vy + rz * vz;
        if (rdotv < 0.0f) rdotv = 0.0f;
        float spec = rdotv * rdotv * rdotv * rdotv;
        spec = spec * spec;
        float ambient = 0.15f;
        pr = sp.r * (ambient + 0.7f * ndotl) + 0.3f * spec;
        pg = sp.g * (ambient + 0.7f * ndotl) + 0.3f * spec;
        pb = sp.b * (ambient + 0.7f * ndotl) + 0.3f * spec;
        if (pr > 1.0f) pr = 1.0f;
        if (pg > 1.0f) pg = 1.0f;
        if (pb > 1.0f) pb = 1.0f;
    }

    out[0] = (unsigned char)(pr * 255.0f);
    out[1] = (unsigned char)(pg * 255.0f);
    out[2] = (unsigned char)(pb * 255.0f);
    out[3] = 255;
}

// ---------------------------------------------------------------------------
// Main
// ---------------------------------------------------------------------------

int main(int argc, char** argv) {
    setvbuf(stdout, NULL, _IONBF, 0);

    // Defaults from params.json
    int W          = 1024;
    int H          = 1024;
    int nSph       = 64;

    // Command-line fallback
    W          = parseIntParam(argc, argv, "--width", W);
    H          = parseIntParam(argc, argv, "--height", H);
    nSph       = parseIntParam(argc, argv, "--spheres", nSph);

    // Environment variable overrides
    const char* env_val;
    env_val = getenv("BENCH_PARAM_width");
    if (env_val) W = atoi(env_val);
    env_val = getenv("BENCH_PARAM_height");
    if (env_val) H = atoi(env_val);
    env_val = getenv("BENCH_PARAM_spheres");
    if (env_val) nSph = atoi(env_val);
    int num_warmup = 5;


    int total_pixels = W * H;
    size_t image_bytes  = (size_t)total_pixels * 4;
    size_t sphere_bytes = (size_t)nSph * sizeof(Sphere);

    // Print device info
    int device_id = 0;
    HIP_CHECK(hipGetDevice(&device_id));
    hipDeviceProp_t prop;
    HIP_CHECK(hipGetDeviceProperties(&prop, device_id));
    fprintf(stderr, "Device: %s (id=%d)\n", prop.name, device_id);
    fprintf(stderr, "Ray Tracing  |  Image: %dx%d  |  Spheres: %d  |  "
            "Iterations: 5 warmup + %d timed\n\n", W, H, nSph);

    // Generate random spheres (deterministic seed)
    std::vector<Sphere> h_spheres(nSph);
    unsigned int seed = 42;
    for (int i = 0; i < nSph; ++i) {
        seed = seed * 1103515245u + 12345u;
        h_spheres[i].cx = ((float)(seed % 10000) / 10000.0f) * 6.0f - 3.0f;
        seed = seed * 1103515245u + 12345u;
        h_spheres[i].cy = ((float)(seed % 10000) / 10000.0f) * 6.0f - 3.0f;
        seed = seed * 1103515245u + 12345u;
        h_spheres[i].cz = ((float)(seed % 10000) / 10000.0f) * 4.0f - 4.0f;
        seed = seed * 1103515245u + 12345u;
        h_spheres[i].radius = ((float)(seed % 10000) / 10000.0f) * 0.5f + 0.2f;
        seed = seed * 1103515245u + 12345u;
        h_spheres[i].r = ((float)(seed % 10000) / 10000.0f) * 0.8f + 0.2f;
        seed = seed * 1103515245u + 12345u;
        h_spheres[i].g = ((float)(seed % 10000) / 10000.0f) * 0.8f + 0.2f;
        seed = seed * 1103515245u + 12345u;
        h_spheres[i].b = ((float)(seed % 10000) / 10000.0f) * 0.8f + 0.2f;
    }

    // Device allocation
    unsigned char* d_image;
    Sphere* d_spheres;
    HIP_CHECK(hipMalloc(&d_image, image_bytes));
    HIP_CHECK(hipMalloc(&d_spheres, sphere_bytes));

    // Copy spheres to device
    HIP_CHECK(hipMemcpy(d_spheres, h_spheres.data(), sphere_bytes,
                        hipMemcpyHostToDevice));

    // Kernel launch config
    int blockSize = 256;
    int gridSize  = (total_pixels + blockSize - 1) / blockSize;

    auto run_raytrace = [&]() {
        hipLaunchKernelGGL(raytrace_kernel, dim3(gridSize), dim3(blockSize),
                           0, 0, d_image, d_spheres, W, H, nSph);
    };

    // -------------------------------------------------------------------
    // Warmup
    // -------------------------------------------------------------------
    for (int w = 0; w < num_warmup; ++w) {
        run_raytrace();
        HIP_CHECK(hipDeviceSynchronize());
    }

    // -------------------------------------------------------------------
    // Timed iterations
    // -------------------------------------------------------------------
    hipEvent_t evStart, evStop;
    HIP_CHECK(hipEventCreate(&evStart));
    HIP_CHECK(hipEventCreate(&evStop));

    std::vector<double> times(1);
    for (int i = 0; i < 1; ++i) {
        HIP_CHECK(hipEventRecord(evStart, 0));
        run_raytrace();
        HIP_CHECK(hipEventRecord(evStop, 0));
        HIP_CHECK(hipEventSynchronize(evStop));

        float ms = 0.0f;
        HIP_CHECK(hipEventElapsedTime(&ms, evStart, evStop));
        times[i] = (double)ms;
    }

    HIP_CHECK(hipEventDestroy(evStart));
    HIP_CHECK(hipEventDestroy(evStop));

    // Compute average time
    double sum = 0.0;
    for (int i = 0; i < 1; ++i) sum += times[i];
    double avg_ms = sum / 1;

    // Mrays/sec = W * H / time_s / 1e6
    double mrays = (double)(W * H) / (avg_ms * 1e-3) / 1e6;

    double total_ms = sum;

    // JSON-lines kernel event
    printf("{\"type\":\"kernel\",\"name\":\"raytrace_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"width\":%d,\"height\":%d,\"spheres\":%d"
           "}}\n",
           avg_ms, W, H, nSph);

    // JSON-lines summary event
    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"mrays_per_sec\",\"value\":%.2f}]}\n",
           total_ms, mrays);

    // Human-readable output to stderr
    fprintf(stderr, "Average time: %.4f ms\n", avg_ms);
    fprintf(stderr, "Throughput:   %.4f Mrays/sec\n", mrays);

    // -------------------------------------------------------------------
    // Verification (compare a subset of pixels against CPU reference)
    // -------------------------------------------------------------------
    {
        std::vector<unsigned char> h_image(image_bytes);
        HIP_CHECK(hipMemcpy(h_image.data(), d_image, image_bytes,
                            hipMemcpyDeviceToHost));

        int verify_count = 1024;  // check 1024 random pixels
        int errors = 0;
        unsigned int vseed = 7;

        for (int v = 0; v < verify_count; ++v) {
            vseed = vseed * 1103515245u + 12345u;
            int px = (int)(vseed % (unsigned int)W);
            vseed = vseed * 1103515245u + 12345u;
            int py = (int)(vseed % (unsigned int)H);

            unsigned char cpu_pixel[4];
            cpu_raytrace_pixel(cpu_pixel, h_spheres.data(), nSph, px, py, W, H);

            int idx = (py * W + px) * 4;
            for (int c = 0; c < 4; ++c) {
                int diff = (int)h_image[idx + c] - (int)cpu_pixel[c];
                if (diff < -1 || diff > 1) {  // allow ±1 for float rounding
                    if (errors < 10) {
                        fprintf(stderr,
                                "Mismatch at pixel (%d,%d) ch=%d: GPU=%d CPU=%d\n",
                                px, py, c, h_image[idx + c], cpu_pixel[c]);
                    }
                    errors++;
                }
            }
        }

        if (errors > 0)
            fprintf(stderr, "FAIL: %d channel errors in %d verified pixels\n",
                    errors, verify_count);
        else
            fprintf(stderr, "PASS\n");
    }

    HIP_CHECK(hipFree(d_image));
    HIP_CHECK(hipFree(d_spheres));

    return 0;
}
