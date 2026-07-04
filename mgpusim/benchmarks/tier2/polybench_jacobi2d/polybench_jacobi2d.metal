/*
 * polybench_jacobi2d.metal — Metal compute shader for the PolyBench Jacobi 2D stencil.
 *
 * Computes one step of 2D Jacobi stencil on an N×N grid:
 *   B[i][j] = (A[i-1][j] + A[i+1][j] + A[i][j-1] + A[i][j+1] + A[i][j]) / 5.0
 *
 * Only interior points (i=1..N-2, j=1..N-2) are updated; boundaries remain 0.
 *
 * Buffer layout:
 *   buffer(0): A    [N × N] float  (row-major, source)
 *   buffer(1): B    [N × N] float  (row-major, destination)
 *   buffer(2): dims { N, N } uint2
 *
 * Grid: (N-2, N-2) threads (covering interior only, offset by 1 in each dim)
 */

#include <metal_stdlib>
using namespace metal;

kernel void jacobi2d_kernel(
    device const float*  A    [[ buffer(0) ]],
    device       float*  B    [[ buffer(1) ]],
    constant     uint2&  dims [[ buffer(2) ]],
    uint2 gid [[ thread_position_in_grid ]])
{
    uint N = dims.x;
    uint i = gid.y + 1;  // interior row (1-indexed)
    uint j = gid.x + 1;  // interior col (1-indexed)

    if (i >= N-1 || j >= N-1) return;

    B[i*N + j] = (A[(i-1)*N + j] + A[(i+1)*N + j] +
                  A[i*N + (j-1)] + A[i*N + (j+1)] +
                  A[i*N + j]) * 0.2f;
}
