/*
 * shoc_stencil2d.metal — Metal compute shader for the SHOC 9-point 2D stencil benchmark.
 *
 * Applies a 9-point star stencil on an N×N grid:
 *
 *   out[i][j] = 0.5   * in[i][j]                                    (center)
 *             + 0.1   * (in[i-1][j] + in[i+1][j] +                 (N/S/E/W)
 *                        in[i][j-1] + in[i][j+1])
 *             + 0.025 * (in[i-1][j-1] + in[i-1][j+1] +             (diagonals)
 *                        in[i+1][j-1] + in[i+1][j+1])
 *
 * Interior cells only: rows/columns 1 .. N-2.
 * Boundaries remain fixed at 0 (not written by this kernel).
 *
 * Buffer layout:
 *   buffer(0): in   [N × N] float  (row-major, read)
 *   buffer(1): out  [N × N] float  (row-major, write)
 *   buffer(2): dims { N, N, 0, 0 } uint4
 *
 * Dispatch: threadgroups of 16×16; grid covers (N-2) interior cells per dim.
 */

#include <metal_stdlib>
using namespace metal;

kernel void stencil2d_kernel(
    device const float*  in   [[ buffer(0) ]],
    device       float*  out  [[ buffer(1) ]],
    constant     uint4&  dims [[ buffer(2) ]],
    uint2 gid [[ thread_position_in_grid ]])
{
    uint N = dims.x;

    // Map grid thread to interior cell (gid starts at 0 → interior starts at 1)
    uint i = gid.y + 1;  // row
    uint j = gid.x + 1;  // col

    if (i >= N - 1 || j >= N - 1) return;

    float center = in[i * N + j];
    float north  = in[(i - 1) * N + j];
    float south  = in[(i + 1) * N + j];
    float west   = in[i * N + (j - 1)];
    float east   = in[i * N + (j + 1)];
    float nw     = in[(i - 1) * N + (j - 1)];
    float ne     = in[(i - 1) * N + (j + 1)];
    float sw     = in[(i + 1) * N + (j - 1)];
    float se     = in[(i + 1) * N + (j + 1)];

    out[i * N + j] = 0.5f   * center
                   + 0.1f   * (north + south + west + east)
                   + 0.025f * (nw + ne + sw + se);
}
