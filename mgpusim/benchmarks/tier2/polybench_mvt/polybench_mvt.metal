/*
 * polybench_mvt.metal — Metal compute shaders for the PolyBench MVT benchmark.
 *
 * Computes:
 *   x1 = A * y1   (matrix-vector product)
 *   x2 = A^T * y2 (transpose matrix-vector product)
 * where A is N×N and x1, x2, y1, y2 are N-vectors.
 *
 * Two kernels:
 *   mvt_kernel1: x1 = A * y1   (each thread handles one row i)
 *   mvt_kernel2: x2 = A^T * y2 (each thread handles one column j)
 *
 * Buffer layout:
 *   kernel1:
 *     buffer(0): A   [N × N] float  (row-major)
 *     buffer(1): y1  [N]     float
 *     buffer(2): x1  [N]     float  (output)
 *     buffer(3): dims { n, 0, 0, 0 } uint4
 *   kernel2:
 *     buffer(0): A   [N × N] float  (row-major)
 *     buffer(1): y2  [N]     float
 *     buffer(2): x2  [N]     float  (output)
 *     buffer(3): dims { n, 0, 0, 0 } uint4
 */

#include <metal_stdlib>
using namespace metal;

// ---------------------------------------------------------------------------
// Kernel 1: x1[i] = sum_j A[i,j] * y1[j]
// ---------------------------------------------------------------------------
kernel void mvt_kernel1(
    device const float*  A    [[ buffer(0) ]],
    device const float*  y1   [[ buffer(1) ]],
    device       float*  x1   [[ buffer(2) ]],
    constant     uint4&  dims [[ buffer(3) ]],
    uint gid [[ thread_position_in_grid ]])
{
    uint n = dims.x;

    if (gid >= n) return;

    float sum = 0.0f;
    for (uint j = 0; j < n; j++)
        sum += A[gid * n + j] * y1[j];
    x1[gid] = sum;
}

// ---------------------------------------------------------------------------
// Kernel 2: x2[j] = sum_i A[i,j] * y2[i]
// ---------------------------------------------------------------------------
kernel void mvt_kernel2(
    device const float*  A    [[ buffer(0) ]],
    device const float*  y2   [[ buffer(1) ]],
    device       float*  x2   [[ buffer(2) ]],
    constant     uint4&  dims [[ buffer(3) ]],
    uint gid [[ thread_position_in_grid ]])
{
    uint n = dims.x;

    if (gid >= n) return;

    float sum = 0.0f;
    for (uint i = 0; i < n; i++)
        sum += A[i * n + gid] * y2[i];
    x2[gid] = sum;
}
