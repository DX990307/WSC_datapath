/**
 * altis_raytracing_metal.mm — Apple Metal host for ray tracing benchmark.
 *
 * Casts rays from a camera through an image plane, intersects with N
 * randomly-placed spheres, and shades the nearest hit with Phong lighting.
 * Each thread traces one ray (one pixel).
 *
 * Usage:
 *   ./altis_raytracing [--width W] [--height H] [--spheres S]
 *
 * Output (stdout): CSV — altis_raytracing,<WxH>,<time_ms>,<Mrays_per_sec>
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
// Sphere data structure (must match Metal shader)
// ---------------------------------------------------------------------------

struct Sphere {
    float cx, cy, cz;
    float radius;
    float r, g, b;
};

struct Params {
    uint32_t width;
    uint32_t height;
    uint32_t num_spheres;
};

// ---------------------------------------------------------------------------
// CPU reference: ray-sphere intersection
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

int main(int argc, char* argv[]) {
    @autoreleasepool {
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
    int num_warmup = 0;


    int total_pixels = W * H;
    size_t image_bytes  = (size_t)total_pixels * 4;
    size_t sphere_bytes = (size_t)nSph * sizeof(Sphere);

    // -------------------------------------------------------------------
    // Metal device setup
    // -------------------------------------------------------------------
    id<MTLDevice> device = MTLCreateSystemDefaultDevice();
    if (!device) {
        fprintf(stderr, "Error: no Metal device found\n");
        return EXIT_FAILURE;
    }
    fprintf(stderr, "Device: %s\n", device.name.UTF8String);
    fprintf(stderr, "Ray Tracing  |  Image: %dx%d  |  Spheres: %d  |  "
            "Iterations: 5 warmup + %d timed\n\n", W, H, nSph);

    id<MTLCommandQueue> queue = [device newCommandQueue];

    // -------------------------------------------------------------------
    // Compile Metal shader
    // -------------------------------------------------------------------
    NSError* err = nil;
    id<MTLLibrary> library = nil;

    NSString* exePath = [[NSBundle mainBundle] executablePath];
    NSString* dir     = [exePath stringByDeletingLastPathComponent];
    NSString* srcPath = [dir stringByAppendingPathComponent:@"altis_raytracing.metal"];

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
        fprintf(stderr, "Error: altis_raytracing.metal not found at %s\n",
                srcPath.UTF8String);
        return EXIT_FAILURE;
    }

    id<MTLFunction> fnKernel = [library newFunctionWithName:@"raytrace_kernel"];
    if (!fnKernel) {
        fprintf(stderr, "Error: kernel function not found\n");
        return EXIT_FAILURE;
    }

    id<MTLComputePipelineState> pso =
        [device newComputePipelineStateWithFunction:fnKernel error:&err];
    if (!pso) {
        fprintf(stderr, "Error creating pipeline: %s\n",
                err.localizedDescription.UTF8String);
        return EXIT_FAILURE;
    }

    // -------------------------------------------------------------------
    // Generate random spheres (deterministic seed)
    // -------------------------------------------------------------------
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

    // -------------------------------------------------------------------
    // Allocate Metal buffers
    // -------------------------------------------------------------------
    id<MTLBuffer> bufImage = [device newBufferWithLength:image_bytes
                                                options:MTLResourceStorageModeShared];
    id<MTLBuffer> bufSpheres = [device newBufferWithBytes:h_spheres.data()
                                                   length:sphere_bytes
                                                  options:MTLResourceStorageModeShared];

    Params params = { (uint32_t)W, (uint32_t)H, (uint32_t)nSph };
    id<MTLBuffer> bufParams = [device newBufferWithBytes:&params
                                                  length:sizeof(Params)
                                                 options:MTLResourceStorageModeShared];

    NSUInteger tgSize = 256;
    NSUInteger gridN  = (NSUInteger)total_pixels;

    // Lambda: run ray tracing
    auto run_raytrace = [&]() {
        id<MTLCommandBuffer> cb = [queue commandBuffer];
        id<MTLComputeCommandEncoder> enc = [cb computeCommandEncoder];

        [enc setComputePipelineState:pso];
        [enc setBuffer:bufImage   offset:0 atIndex:0];
        [enc setBuffer:bufSpheres offset:0 atIndex:1];
        [enc setBuffer:bufParams  offset:0 atIndex:2];
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
        run_raytrace();
    }

    // -------------------------------------------------------------------
    // Timed iterations
    // -------------------------------------------------------------------
    std::vector<double> times(1);
    for (int i = 0; i < 1; ++i) {
        uint64_t t0 = mach_absolute_time();
        run_raytrace();
        uint64_t t1 = mach_absolute_time();
        times[i] = ticks_to_ms(t1 - t0);
    }

    double sum = 0.0;
    for (int i = 0; i < 1; ++i) sum += times[i];
    double avg_ms = sum / 1;

    // Mrays/sec = W * H / time_s / 1e6
    double mrays = (double)(W * H) / (avg_ms * 1e-3) / 1e6;

    double total_ms = sum;

    // JSON-lines kernel event
    printf("{\"type\":\"kernel\",\"name\":\"raytrace_kernel\",\"time_ms\":%.6f,"
           "\"params\":{\"width\":%d,\"height\":%d,\"spheres\":%d,"
           "}}\n",
           avg_ms, W, H, nSph);

    // JSON-lines summary event
    printf("{\"type\":\"summary\",\"total_time_ms\":%.6f,"
           "\"metrics\":[{\"name\":\"mrays_per_sec\",\"value\":%.2f}]}\n",
           total_ms, mrays);

    // Human-readable to stderr
    fprintf(stderr, "Average time: %.4f ms\n", avg_ms);
    fprintf(stderr, "Throughput:   %.4f Mrays/sec\n", mrays);

    // -------------------------------------------------------------------
    // Verification (compare subset of pixels against CPU reference)
    // -------------------------------------------------------------------
    {
        const unsigned char* gpuImage = (const unsigned char*)bufImage.contents;

        int verify_count = 1024;
        int errors = 0;
        unsigned int vseed = 7;

        for (int vi = 0; vi < verify_count; ++vi) {
            vseed = vseed * 1103515245u + 12345u;
            int px = (int)(vseed % (unsigned int)W);
            vseed = vseed * 1103515245u + 12345u;
            int py = (int)(vseed % (unsigned int)H);

            unsigned char cpu_pixel[4];
            cpu_raytrace_pixel(cpu_pixel, h_spheres.data(), nSph, px, py, W, H);

            int idx = (py * W + px) * 4;
            for (int c = 0; c < 4; ++c) {
                int diff = (int)gpuImage[idx + c] - (int)cpu_pixel[c];
                if (diff < -1 || diff > 1) {
                    if (errors < 10) {
                        fprintf(stderr,
                                "Mismatch at pixel (%d,%d) ch=%d: GPU=%d CPU=%d\n",
                                px, py, c, gpuImage[idx + c], cpu_pixel[c]);
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

    return EXIT_SUCCESS;

    } // @autoreleasepool
}
