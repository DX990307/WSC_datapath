/*
 * parboil_stencil.metal — Metal compute shader for the Parboil 3D Stencil benchmark.
 *
 * 7-point 3D Jacobi stencil:
 *   out[i] = c0*in[i] + c1*(in[i-1] + in[i+1]
 *                         + in[i-nx] + in[i+nx]
 *                         + in[i-nx*ny] + in[i+nx*ny])
 *
 * Only interior cells (1-cell border excluded) are updated.
 *
 * Buffer layout:
 *   buffer(0): in    [nx * ny * nz]  float  (read)
 *   buffer(1): out   [nx * ny * nz]  float  (write)
 *   buffer(2): params { nx, ny, nz, pad }   uint4
 *   buffer(3): coeffs { c0, c1, 0, 0 }      float4
 *
 * Dispatch: 3D grid — one thread per (x, y, z) cell.
 *   threadgroup size: (16, 16, 1)
 *   grid size:        (nx, ny, nz)
 */

#include <metal_stdlib>
using namespace metal;

kernel void stencil3d(
    device const float*  in      [[ buffer(0) ]],
    device       float*  out     [[ buffer(1) ]],
    constant     uint4&  dims    [[ buffer(2) ]],   // {nx, ny, nz, 0}
    constant     float4& coeffs  [[ buffer(3) ]],   // {c0, c1, 0, 0}
    uint3 gid [[ thread_position_in_grid ]])
{
    uint nx = dims.x;
    uint ny = dims.y;
    uint nz = dims.z;

    uint ix = gid.x;
    uint iy = gid.y;
    uint iz = gid.z;

    // Bounds check and interior-only update
    if (ix == 0 || ix >= nx - 1 ||
        iy == 0 || iy >= ny - 1 ||
        iz == 0 || iz >= nz - 1) return;

    float c0 = coeffs.x;
    float c1 = coeffs.y;

    uint idx = iz * ny * nx + iy * nx + ix;

    out[idx] = c0 * in[idx]
             + c1 * (in[idx - 1]        // x-1
                   + in[idx + 1]        // x+1
                   + in[idx - nx]       // y-1
                   + in[idx + nx]       // y+1
                   + in[idx - ny*nx]    // z-1
                   + in[idx + ny*nx]);  // z+1
}
