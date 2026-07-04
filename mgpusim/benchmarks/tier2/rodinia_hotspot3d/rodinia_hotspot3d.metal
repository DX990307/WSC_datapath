/*
 * rodinia_hotspot3d.metal — Metal compute shader for the Rodinia HotSpot3D benchmark.
 *
 * 3D stencil thermal simulation:
 *   T_new[z][y][x] = T[z][y][x] + step_div_cap * (
 *       power[z][y][x]
 *       + (T[z][y][x-1] + T[z][y][x+1] - 2*T) * Rx_1
 *       + (T[z][y-1][x] + T[z][y+1][x] - 2*T) * Ry_1
 *       + (T[z-1][y][x] + T[z+1][y][x] - 2*T) * Rz_1
 *       + (AMB_TEMP - T) * Ra_1
 *   )
 *
 * Buffer layout:
 *   buffer(0): temp_src  [nz * ny * nx] (read)
 *   buffer(1): temp_dst  [nz * ny * nx] (write)
 *   buffer(2): power     [nz * ny * nx] (read)
 *   buffer(3): dims      { nx, ny, nz, 0 }  (uint4)
 *   buffer(4): therm     { step_div_cap, Rx_1, Ry_1, Rz_1 }  (float4)
 *   buffer(5): therm2    { Ra_1, amb_temp, 0, 0 }  (float4)
 */

#include <metal_stdlib>
using namespace metal;

kernel void hotspot3d_kernel(
    device const float*  temp_src  [[ buffer(0) ]],
    device       float*  temp_dst  [[ buffer(1) ]],
    device const float*  power     [[ buffer(2) ]],
    constant     uint4&  dims      [[ buffer(3) ]],  // {nx, ny, nz, 0}
    constant     float4& therm     [[ buffer(4) ]],  // {step_div_cap, Rx_1, Ry_1, Rz_1}
    constant     float4& therm2    [[ buffer(5) ]],  // {Ra_1, amb_temp, 0, 0}
    uint3 gid [[ thread_position_in_grid ]])
{
    uint nx = dims.x;
    uint ny = dims.y;
    uint nz = dims.z;

    uint x = gid.x;
    uint y = gid.y;
    uint z = gid.z;

    if (x >= nx || y >= ny || z >= nz) return;

    float step_div_cap = therm.x;
    float Rx_1         = therm.y;
    float Ry_1         = therm.z;
    float Rz_1         = therm.w;
    float Ra_1         = therm2.x;
    float amb_temp     = therm2.y;

    uint idx = z * ny * nx + y * nx + x;

    float tc = temp_src[idx];

    // ±x neighbors (clamped boundary)
    float txm = (x > 0)      ? temp_src[z * ny * nx + y * nx + (x - 1)] : tc;
    float txp = (x < nx - 1) ? temp_src[z * ny * nx + y * nx + (x + 1)] : tc;

    // ±y neighbors (clamped boundary)
    float tym = (y > 0)      ? temp_src[z * ny * nx + (y - 1) * nx + x] : tc;
    float typ = (y < ny - 1) ? temp_src[z * ny * nx + (y + 1) * nx + x] : tc;

    // ±z neighbors (clamped boundary)
    float tzm = (z > 0)      ? temp_src[(z - 1) * ny * nx + y * nx + x] : tc;
    float tzp = (z < nz - 1) ? temp_src[(z + 1) * ny * nx + y * nx + x] : tc;

    float delta = step_div_cap * (
        power[idx]
        + (txm + txp - 2.0f * tc) * Rx_1
        + (tym + typ - 2.0f * tc) * Ry_1
        + (tzm + tzp - 2.0f * tc) * Rz_1
        + (amb_temp - tc) * Ra_1
    );

    temp_dst[idx] = tc + delta;
}
