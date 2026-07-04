/*
 * altis_raytracing.metal — Metal compute shader for simple ray tracing.
 *
 * Each thread traces one ray (one pixel), testing all spheres for intersection
 * and shading the nearest hit with Phong lighting.
 *
 * Buffer layout:
 *   buffer(0): image      — uchar[W*H*4], RGBA output
 *   buffer(1): spheres    — Sphere[num_spheres]
 *   buffer(2): params     — { width, height, num_spheres }
 */

#include <metal_stdlib>
using namespace metal;

// ---------------------------------------------------------------------------
// Sphere data structure (must match host layout)
// ---------------------------------------------------------------------------

struct Sphere {
    float cx, cy, cz;
    float radius;
    float r, g, b;
};

struct Params {
    uint width;
    uint height;
    uint num_spheres;
};

// ---------------------------------------------------------------------------
// Ray-sphere intersection
// ---------------------------------------------------------------------------

static float intersect_sphere(
    float ox, float oy, float oz,
    float dx, float dy, float dz,
    float cx, float cy, float cz,
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

    float sq = sqrt(disc);
    float t0 = (-b - sq) / (2.0f * a);
    float t1 = (-b + sq) / (2.0f * a);

    if (t0 > 0.001f) return t0;
    if (t1 > 0.001f) return t1;
    return -1.0f;
}

// ---------------------------------------------------------------------------
// Raytrace kernel — one pixel per thread
// ---------------------------------------------------------------------------

kernel void raytrace_kernel(
    device uchar*          image      [[ buffer(0) ]],
    constant Sphere*       spheres    [[ buffer(1) ]],
    constant Params&       params     [[ buffer(2) ]],
    uint                   idx        [[ thread_position_in_grid ]])
{
    uint width  = params.width;
    uint height = params.height;
    uint num_spheres = params.num_spheres;
    uint total_pixels = width * height;

    if (idx >= total_pixels) return;

    uint px = idx % width;
    uint py = idx / width;

    // Camera setup
    float aspect = (float)width / (float)height;
    float fov_scale = 1.0f;

    float u = (2.0f * ((float)px + 0.5f) / (float)width - 1.0f) * aspect * fov_scale;
    float v = (1.0f - 2.0f * ((float)py + 0.5f) / (float)height) * fov_scale;

    float ox = 0.0f, oy = 0.0f, oz = 5.0f;
    float dx = u, dy = v, dz = -1.0f;

    float len = sqrt(dx * dx + dy * dy + dz * dz);
    dx /= len; dy /= len; dz /= len;

    // Find closest intersection
    float closest_t = 1e20f;
    int closest_id = -1;

    for (uint s = 0; s < num_spheres; ++s) {
        float t = intersect_sphere(ox, oy, oz, dx, dy, dz,
                                   spheres[s].cx, spheres[s].cy, spheres[s].cz,
                                   spheres[s].radius);
        if (t > 0.0f && t < closest_t) {
            closest_t = t;
            closest_id = (int)s;
        }
    }

    // Shade pixel
    float pr = 0.05f, pg = 0.05f, pb = 0.1f;

    if (closest_id >= 0) {
        float hx = ox + closest_t * dx;
        float hy = oy + closest_t * dy;
        float hz = oz + closest_t * dz;

        float nx = (hx - spheres[closest_id].cx) / spheres[closest_id].radius;
        float ny = (hy - spheres[closest_id].cy) / spheres[closest_id].radius;
        float nz = (hz - spheres[closest_id].cz) / spheres[closest_id].radius;

        float lx = 0.577f, ly = 0.577f, lz = 0.577f;
        float ndotl = nx * lx + ny * ly + nz * lz;
        if (ndotl < 0.0f) ndotl = 0.0f;

        float rx = 2.0f * ndotl * nx - lx;
        float ry = 2.0f * ndotl * ny - ly;
        float rz = 2.0f * ndotl * nz - lz;

        float vvx = -dx, vvy = -dy, vvz = -dz;
        float rdotv = rx * vvx + ry * vvy + rz * vvz;
        if (rdotv < 0.0f) rdotv = 0.0f;
        float spec = rdotv * rdotv * rdotv * rdotv;
        spec = spec * spec;

        float ambient = 0.15f;
        pr = spheres[closest_id].r * (ambient + 0.7f * ndotl) + 0.3f * spec;
        pg = spheres[closest_id].g * (ambient + 0.7f * ndotl) + 0.3f * spec;
        pb = spheres[closest_id].b * (ambient + 0.7f * ndotl) + 0.3f * spec;

        if (pr > 1.0f) pr = 1.0f;
        if (pg > 1.0f) pg = 1.0f;
        if (pb > 1.0f) pb = 1.0f;
    }

    uint base = idx * 4;
    image[base + 0] = (uchar)(pr * 255.0f);
    image[base + 1] = (uchar)(pg * 255.0f);
    image[base + 2] = (uchar)(pb * 255.0f);
    image[base + 3] = 255;
}
