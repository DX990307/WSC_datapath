/*
 * polybench_2dconv.metal — Metal compute shader for PolyBench 2D Convolution.
 *
 * Applies a fixed 3×3 kernel to an NI×NJ matrix A, producing output B.
 * PolyBench standard coefficients:
 *   c = {{0.8, 0.2, 0.3},
 *        {0.2, 0.7, 0.4},
 *        {0.1, 0.2, 0.5}}
 *
 *   B[i][j] = c00*A[i-1][j-1] + c01*A[i-1][j] + c02*A[i-1][j+1]
 *           + c10*A[i  ][j-1] + c11*A[i  ][j] + c12*A[i  ][j+1]
 *           + c20*A[i+1][j-1] + c21*A[i+1][j] + c22*A[i+1][j+1]
 *             for 1 <= i < NI-1, 1 <= j < NJ-1
 *
 * Buffer layout:
 *   buffer(0): A     [NI × NJ] float (read)
 *   buffer(1): B     [NI × NJ] float (write)
 *   buffer(2): dims  {NI, NJ}  uint2 (constant)
 */

#include <metal_stdlib>
using namespace metal;

kernel void convolution2D_kernel(
    device const float*  A    [[ buffer(0) ]],
    device       float*  B    [[ buffer(1) ]],
    constant     uint2&  dims [[ buffer(2) ]],
    uint2 gid [[ thread_position_in_grid ]])
{
    uint NJ = dims.x;
    uint NI = dims.y;

    uint j = gid.x;
    uint i = gid.y;

    // Skip border elements
    if (i < 1 || i >= NI - 1 || j < 1 || j >= NJ - 1)
        return;

    // PolyBench fixed 3×3 coefficients
    const float c00 = 0.8f, c01 = 0.2f, c02 = 0.3f;
    const float c10 = 0.2f, c11 = 0.7f, c12 = 0.4f;
    const float c20 = 0.1f, c21 = 0.2f, c22 = 0.5f;

    B[i * NJ + j] =
        c00 * A[(i - 1) * NJ + (j - 1)] +
        c01 * A[(i - 1) * NJ +  j     ] +
        c02 * A[(i - 1) * NJ + (j + 1)] +
        c10 * A[ i      * NJ + (j - 1)] +
        c11 * A[ i      * NJ +  j     ] +
        c12 * A[ i      * NJ + (j + 1)] +
        c20 * A[(i + 1) * NJ + (j - 1)] +
        c21 * A[(i + 1) * NJ +  j     ] +
        c22 * A[(i + 1) * NJ + (j + 1)];
}
