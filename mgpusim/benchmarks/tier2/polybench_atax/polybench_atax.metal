/*
 * polybench_atax.metal — Metal compute shaders for the PolyBench ATAX benchmark.
 *
 * Computes y = A^T * (A * x)  where A is NX×NY, x and y are NY-vectors.
 *
 * Two kernels:
 *   atax_kernel1: tmp = A * x   (each thread handles one row i)
 *   atax_kernel2: y = A^T * tmp (each thread handles one column j)
 *
 * Buffer layout:
 *   kernel1:
 *     buffer(0): A    [NX × NY] float  (row-major)
 *     buffer(1): x    [NY]      float
 *     buffer(2): tmp  [NX]      float  (output)
 *     buffer(3): dims { nx, ny, 0, 0 } uint4
 *   kernel2:
 *     buffer(0): A    [NX × NY] float  (row-major)
 *     buffer(1): tmp  [NX]      float
 *     buffer(2): y    [NY]      float  (output)
 *     buffer(3): dims { nx, ny, 0, 0 } uint4
 */

#include <metal_stdlib>
using namespace metal;

// ---------------------------------------------------------------------------
// Kernel 1: tmp[i] = sum_j A[i,j] * x[j]
// ---------------------------------------------------------------------------
kernel void atax_kernel1(
    device const float*  A    [[ buffer(0) ]],
    device const float*  x    [[ buffer(1) ]],
    device       float*  tmp  [[ buffer(2) ]],
    constant     uint4&  dims [[ buffer(3) ]],
    uint gid [[ thread_position_in_grid ]])
{
    uint nx = dims.x;
    uint ny = dims.y;

    if (gid >= nx) return;

    float sum = 0.0f;
    for (uint j = 0; j < ny; j++)
        sum += A[gid * ny + j] * x[j];
    tmp[gid] = sum;
}

// ---------------------------------------------------------------------------
// Kernel 2: y[j] = sum_i A[i,j] * tmp[i]
// ---------------------------------------------------------------------------
kernel void atax_kernel2(
    device const float*  A    [[ buffer(0) ]],
    device const float*  tmp  [[ buffer(1) ]],
    device       float*  y    [[ buffer(2) ]],
    constant     uint4&  dims [[ buffer(3) ]],
    uint gid [[ thread_position_in_grid ]])
{
    uint nx = dims.x;
    uint ny = dims.y;

    if (gid >= ny) return;

    float sum = 0.0f;
    for (uint i = 0; i < nx; i++)
        sum += A[i * ny + gid] * tmp[i];
    y[gid] = sum;
}
