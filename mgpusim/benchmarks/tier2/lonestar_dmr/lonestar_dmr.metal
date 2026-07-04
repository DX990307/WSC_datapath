/*
 * lonestar_dmr.metal — Metal compute shaders for Delaunay Mesh Refinement.
 *
 * Two kernels:
 *   1. dmr_check_kernel  — scan triangles, flag bad ones (min angle < threshold)
 *   2. dmr_refine_kernel — refine bad triangles by perturbing vertices toward circumcenter
 *
 * Buffer layout:
 *   buffer(0): tri_v0      [num_triangles]  uint   — vertex 0 index per triangle
 *   buffer(1): tri_v1      [num_triangles]  uint
 *   buffer(2): tri_v2      [num_triangles]  uint
 *   buffer(3): vx          [num_vertices]   float  — vertex X coordinates
 *   buffer(4): vy          [num_vertices]   float  — vertex Y coordinates
 *   buffer(5): worklist    [num_triangles]  uint   — output bad triangle indices
 *   buffer(6): worklist_count [1]           atomic_uint
 *   buffer(7): params      { num_triangles, num_vertices, 0, 0 } uint4
 *   buffer(8): fparams     { min_angle_threshold, 0 } float2
 */

#include <metal_stdlib>
using namespace metal;

static inline float compute_min_angle(
    float x0, float y0, float x1, float y1, float x2, float y2)
{
    float ax = x1 - x0, ay = y1 - y0;
    float bx = x2 - x0, by = y2 - y0;
    float cx = x2 - x1, cy = y2 - y1;

    float la = sqrt(ax * ax + ay * ay);
    float lb = sqrt(bx * bx + by * by);
    float lc = sqrt(cx * cx + cy * cy);

    if (la < 1e-10f || lb < 1e-10f || lc < 1e-10f) return 0.0f;

    float cos_A = (ax * bx + ay * by) / (la * lb);
    float cos_B = (-ax * cx + -ay * cy) / (la * lc);
    float cos_C = (-bx * (-cx) + -by * (-cy)) / (lb * lc);

    cos_A = clamp(cos_A, -1.0f, 1.0f);
    cos_B = clamp(cos_B, -1.0f, 1.0f);
    cos_C = clamp(cos_C, -1.0f, 1.0f);

    float a_A = acos(cos_A) * (180.0f / 3.14159265f);
    float a_B = acos(cos_B) * (180.0f / 3.14159265f);
    float a_C = acos(cos_C) * (180.0f / 3.14159265f);

    return min(a_A, min(a_B, a_C));
}

static inline float2 compute_circumcenter(
    float x0, float y0, float x1, float y1, float x2, float y2)
{
    float D = 2.0f * (x0 * (y1 - y2) + x1 * (y2 - y0) + x2 * (y0 - y1));
    if (abs(D) < 1e-10f) {
        return float2((x0 + x1 + x2) / 3.0f, (y0 + y1 + y2) / 3.0f);
    }
    float sq0 = x0 * x0 + y0 * y0;
    float sq1 = x1 * x1 + y1 * y1;
    float sq2 = x2 * x2 + y2 * y2;
    float cx = (sq0 * (y1 - y2) + sq1 * (y2 - y0) + sq2 * (y0 - y1)) / D;
    float cy = (sq0 * (x2 - x1) + sq1 * (x0 - x2) + sq2 * (x1 - x0)) / D;
    return float2(cx, cy);
}

kernel void dmr_check_kernel(
    device const uint*   tri_v0          [[ buffer(0) ]],
    device const uint*   tri_v1          [[ buffer(1) ]],
    device const uint*   tri_v2          [[ buffer(2) ]],
    device const float*  vx              [[ buffer(3) ]],
    device const float*  vy              [[ buffer(4) ]],
    device       uint*   worklist        [[ buffer(5) ]],
    device atomic_uint*  worklist_count  [[ buffer(6) ]],
    constant     uint4&  params          [[ buffer(7) ]],
    constant     float2& fparams         [[ buffer(8) ]],
    uint tid [[ thread_position_in_grid ]])
{
    uint num_triangles = params.x;
    if (tid >= num_triangles) return;

    uint v0 = tri_v0[tid];
    uint v1 = tri_v1[tid];
    uint v2 = tri_v2[tid];

    float angle = compute_min_angle(
        vx[v0], vy[v0], vx[v1], vy[v1], vx[v2], vy[v2]);

    float min_angle_threshold = fparams.x;
    if (angle < min_angle_threshold) {
        uint pos = atomic_fetch_add_explicit(worklist_count, 1u, memory_order_relaxed);
        worklist[pos] = tid;
    }
}

kernel void dmr_refine_kernel(
    device const uint*   worklist        [[ buffer(0) ]],
    device const uint*   tri_v0          [[ buffer(1) ]],
    device const uint*   tri_v1          [[ buffer(2) ]],
    device const uint*   tri_v2          [[ buffer(3) ]],
    device       float*  vx              [[ buffer(4) ]],
    device       float*  vy              [[ buffer(5) ]],
    constant     uint4&  params          [[ buffer(6) ]],
    uint tid [[ thread_position_in_grid ]])
{
    uint worklist_size  = params.x;
    uint num_triangles  = params.y;
    if (tid >= worklist_size) return;

    uint tri_idx = worklist[tid];
    if (tri_idx >= num_triangles) return;

    uint v0 = tri_v0[tri_idx];
    uint v1 = tri_v1[tri_idx];
    uint v2 = tri_v2[tri_idx];

    float2 cc = compute_circumcenter(
        vx[v0], vy[v0], vx[v1], vy[v1], vx[v2], vy[v2]);

    float blend = 0.1f;

    // Note: Metal doesn't have atomic float add in older versions,
    // so we use non-atomic writes (acceptable for this simplified benchmark)
    vx[v0] += blend * (cc.x - vx[v0]);
    vy[v0] += blend * (cc.y - vy[v0]);
    vx[v1] += blend * (cc.x - vx[v1]);
    vy[v1] += blend * (cc.y - vy[v1]);
    vx[v2] += blend * (cc.x - vx[v2]);
    vy[v2] += blend * (cc.y - vy[v2]);
}
