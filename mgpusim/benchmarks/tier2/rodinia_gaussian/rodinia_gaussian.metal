/*
 * rodinia_gaussian.metal — Metal compute shaders for the Rodinia Gaussian benchmark.
 *
 * Gaussian elimination with back-substitution to solve Ax=b.
 * Two kernels implementing the classic Rodinia two-pass approach:
 *
 *   fan1_kernel: Compute multipliers for pivot column t
 *     m[i][t] = a[i][t] / a[t][t]  for i = t+1..N-1
 *
 *   fan2_kernel: Eliminate pivot column from the submatrix
 *     a[i][j] -= m[i][t] * a[t][j]  for i,j > t
 *     b[i]    -= m[i][t] * b[t]      for i > t  (handled by thread with col==0)
 *
 * Buffer layout:
 *   fan1_kernel:
 *     buffer(0): m     [N × N]  float  (multiplier matrix, output)
 *     buffer(1): a     [N × N]  float  (matrix A, read-only for fan1)
 *     buffer(2): params { Size, t, 0, 0 }  uint4
 *
 *   fan2_kernel:
 *     buffer(0): m     [N × N]  float  (multiplier matrix, read-only for fan2)
 *     buffer(1): a     [N × N]  float  (matrix A, updated in-place)
 *     buffer(2): b     [N]      float  (vector b, updated in-place)
 *     buffer(3): params { Size, t, 0, 0 }  uint4
 */

#include <metal_stdlib>
using namespace metal;

// ---------------------------------------------------------------------------
// fan1_kernel: compute multipliers for pivot column t
//
// Each thread i (relative) computes:
//   m[(t+1+i)*Size + t] = a[(t+1+i)*Size + t] / a[t*Size + t]
// ---------------------------------------------------------------------------
kernel void fan1_kernel(
    device       float*  m      [[ buffer(0) ]],
    device const float*  a      [[ buffer(1) ]],
    constant     uint4&  params [[ buffer(2) ]],
    uint gid [[ thread_position_in_grid ]])
{
    uint Size = params.x;
    uint t    = params.y;
    uint remaining = Size - t - 1;

    if (gid >= remaining) return;

    uint row = t + 1 + gid;
    m[row * Size + t] = a[row * Size + t] / a[t * Size + t];
}

// ---------------------------------------------------------------------------
// fan2_kernel: eliminate pivot column from the submatrix
//
// Each thread (col, row) relative to the submatrix updates:
//   a[abs_row*Size + abs_col] -= m[abs_row*Size + t] * a[t*Size + abs_col]
// The thread with col==0 also updates b:
//   b[abs_row] -= m[abs_row*Size + t] * b[t]
// ---------------------------------------------------------------------------
kernel void fan2_kernel(
    device const float*  m      [[ buffer(0) ]],
    device       float*  a      [[ buffer(1) ]],
    device       float*  b      [[ buffer(2) ]],
    constant     uint4&  params [[ buffer(3) ]],
    uint2 gid [[ thread_position_in_grid ]])
{
    uint Size = params.x;
    uint t    = params.y;
    uint remaining = Size - t - 1;

    uint col = gid.x;  // relative column
    uint row = gid.y;  // relative row

    if (col >= remaining || row >= remaining) return;

    uint abs_row = t + 1 + row;
    uint abs_col = t + 1 + col;

    a[abs_row * Size + abs_col] -= m[abs_row * Size + t] * a[t * Size + abs_col];

    if (col == 0) {
        b[abs_row] -= m[abs_row * Size + t] * b[t];
    }
}
