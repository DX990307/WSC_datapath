/*
 * polybench_bicg.metal — Metal compute shaders for the PolyBench BiCG benchmark.
 *
 * Computes:
 *   s = A^T * r  (one thread per column j)
 *   q = A   * p  (one thread per row i)
 * where A is M×N, p is N-vector, r is M-vector,
 *       s is N-vector (output), q is M-vector (output).
 *
 * Two kernels:
 *   bicg_kernel1: s[j] = sum_i A[i,j] * r[i]   (reduction per column)
 *   bicg_kernel2: q[i] = sum_j A[i,j] * p[j]   (dot product per row)
 *
 * Buffer layout:
 *   kernel1:
 *     buffer(0): A    [M × N] float  (row-major)
 *     buffer(1): r    [M]     float
 *     buffer(2): s    [N]     float  (output)
 *     buffer(3): dims { m, n, 0, 0 } uint4
 *   kernel2:
 *     buffer(0): A    [M × N] float  (row-major)
 *     buffer(1): p    [N]     float
 *     buffer(2): q    [M]     float  (output)
 *     buffer(3): dims { m, n, 0, 0 } uint4
 */

#include <metal_stdlib>
using namespace metal;

// ---------------------------------------------------------------------------
// Kernel 1: s[j] = sum_i A[i,j] * r[i]
// Each thread handles one column j.
// ---------------------------------------------------------------------------
kernel void bicg_kernel1(
    device const float*  A    [[ buffer(0) ]],
    device const float*  r    [[ buffer(1) ]],
    device       float*  s    [[ buffer(2) ]],
    constant     uint4&  dims [[ buffer(3) ]],
    uint gid [[ thread_position_in_grid ]])
{
    uint m = dims.x;
    uint n = dims.y;

    if (gid >= n) return;

    float sum = 0.0f;
    for (uint i = 0; i < m; i++)
        sum += A[i * n + gid] * r[i];
    s[gid] = sum;
}

// ---------------------------------------------------------------------------
// Kernel 2: q[i] = sum_j A[i,j] * p[j]
// Each thread handles one row i.
// ---------------------------------------------------------------------------
kernel void bicg_kernel2(
    device const float*  A    [[ buffer(0) ]],
    device const float*  p    [[ buffer(1) ]],
    device       float*  q    [[ buffer(2) ]],
    constant     uint4&  dims [[ buffer(3) ]],
    uint gid [[ thread_position_in_grid ]])
{
    uint m = dims.x;
    uint n = dims.y;

    if (gid >= m) return;

    float sum = 0.0f;
    for (uint j = 0; j < n; j++)
        sum += A[gid * n + j] * p[j];
    q[gid] = sum;
}
