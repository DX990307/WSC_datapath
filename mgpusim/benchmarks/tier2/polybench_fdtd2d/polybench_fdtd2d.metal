/*
 * polybench_fdtd2d.metal — Metal compute shaders for PolyBench FDTD-2D benchmark.
 *
 * 2D Finite Difference Time Domain electromagnetic simulation.
 * Per time step update equations:
 *
 *   Kernel update_ex:
 *     ex[0][j]  = 0                              (boundary)
 *     ex[i][j] += 0.5*(hz[i][j] - hz[i-1][j])   for i >= 1
 *
 *   Kernel update_ey:
 *     ey[i][0]  = 0                              (boundary)
 *     ey[i][j] += 0.5*(hz[i][j] - hz[i][j-1])   for j >= 1
 *
 *   Kernel update_hz:
 *     hz[i][j] -= 0.7*(ex[i][j+1] - ex[i][j] + ey[i+1][j] - ey[i][j])
 *                for i < NX-1, j < NY-1
 *
 * Buffer layout (all kernels):
 *   buffer(0): ex  [NX * NY] floats
 *   buffer(1): ey  [NX * NY] floats
 *   buffer(2): hz  [NX * NY] floats
 *   buffer(3): dims { NX, NY, 0, 0 } (uint4)
 */

#include <metal_stdlib>
using namespace metal;

// ---------------------------------------------------------------------------
// Kernel 1: Update ex
// ---------------------------------------------------------------------------
kernel void update_ex(
    device       float*  ex   [[ buffer(0) ]],
    device const float*  hz   [[ buffer(2) ]],
    constant     uint4&  dims [[ buffer(3) ]],
    uint2 gid [[ thread_position_in_grid ]])
{
    uint NX = dims.x;
    uint NY = dims.y;

    uint j = gid.x;  // column
    uint i = gid.y;  // row

    if (i >= NX || j >= NY) return;

    if (i == 0u) {
        ex[0u * NY + j] = 0.0f;
    } else {
        ex[i * NY + j] += 0.5f * (hz[i * NY + j] - hz[(i - 1u) * NY + j]);
    }
}

// ---------------------------------------------------------------------------
// Kernel 2: Update ey
// ---------------------------------------------------------------------------
kernel void update_ey(
    device       float*  ey   [[ buffer(1) ]],
    device const float*  hz   [[ buffer(2) ]],
    constant     uint4&  dims [[ buffer(3) ]],
    uint2 gid [[ thread_position_in_grid ]])
{
    uint NX = dims.x;
    uint NY = dims.y;

    uint j = gid.x;  // column
    uint i = gid.y;  // row

    if (i >= NX || j >= NY) return;

    if (j == 0u) {
        ey[i * NY + 0u] = 0.0f;
    } else {
        ey[i * NY + j] += 0.5f * (hz[i * NY + j] - hz[i * NY + (j - 1u)]);
    }
}

// ---------------------------------------------------------------------------
// Kernel 3: Update hz
// ---------------------------------------------------------------------------
kernel void update_hz(
    device const float*  ex   [[ buffer(0) ]],
    device const float*  ey   [[ buffer(1) ]],
    device       float*  hz   [[ buffer(2) ]],
    constant     uint4&  dims [[ buffer(3) ]],
    uint2 gid [[ thread_position_in_grid ]])
{
    uint NX = dims.x;
    uint NY = dims.y;

    uint j = gid.x;  // column
    uint i = gid.y;  // row

    // hz is only updated for i < NX-1, j < NY-1
    if (i >= NX - 1u || j >= NY - 1u) return;

    hz[i * NY + j] -= 0.7f * (ex[i * NY + (j + 1u)] - ex[i * NY + j] +
                               ey[(i + 1u) * NY + j] - ey[i * NY + j]);
}
