/*
 * polybench_gramschmidt.metal — Metal compute shaders for Gram-Schmidt orthogonalization.
 *
 * The norm of each column is computed on the CPU (host) since unified memory
 * makes column reads cheap on Apple Silicon.  The GPU handles the two
 * compute-intensive steps per column k:
 *
 *   gram_normalize : Q[:,k] = A[:,k] / nrm          (M threads)
 *   gram_project   : for j>k: R[k,j] = dot(Q[:,k], A[:,j]);
 *                              A[:,j] -= R[k,j] * Q[:,k]
 *                    (one thread per column j, j = gid + k + 1)
 *
 * Buffer layout:
 *   gram_normalize:
 *     buffer(0): A    [M × N] float  (row-major, read-only)
 *     buffer(1): Q    [M × N] float  (write column k)
 *     buffer(2): dims { M, N, k, 0 } uint4
 *     buffer(3): nrm  float  (norm of column k, passed inline)
 *
 *   gram_project:
 *     buffer(0): A    [M × N] float  (read/write)
 *     buffer(1): Q    [M × N] float  (read column k)
 *     buffer(2): R    [N × N] float  (write row k, columns > k)
 *     buffer(3): dims { M, N, k, 0 } uint4
 */

#include <metal_stdlib>
using namespace metal;

// ---------------------------------------------------------------------------
// gram_normalize: Q[i,k] = A[i,k] / nrm  for i = gid
// ---------------------------------------------------------------------------
kernel void gram_normalize(
    device const float*  A    [[ buffer(0) ]],
    device       float*  Q    [[ buffer(1) ]],
    constant     uint4&  dims [[ buffer(2) ]],
    constant     float&  nrm  [[ buffer(3) ]],
    uint gid [[ thread_position_in_grid ]])
{
    uint M = dims.x;
    uint N = dims.y;
    uint k = dims.z;

    if (gid >= M) return;
    Q[gid * N + k] = A[gid * N + k] / nrm;
}

// ---------------------------------------------------------------------------
// gram_project: update column j = gid + k + 1  (one thread per remaining col)
//   dot    = sum_i Q[i,k] * A[i,j]
//   R[k,j] = dot
//   A[i,j] -= dot * Q[i,k]  for all i
// ---------------------------------------------------------------------------
kernel void gram_project(
    device       float*  A    [[ buffer(0) ]],
    device const float*  Q    [[ buffer(1) ]],
    device       float*  R    [[ buffer(2) ]],
    constant     uint4&  dims [[ buffer(3) ]],
    uint gid [[ thread_position_in_grid ]])
{
    uint M = dims.x;
    uint N = dims.y;
    uint k = dims.z;

    uint j = gid + k + 1;
    if (j >= N) return;

    float dot = 0.0f;
    for (uint i = 0; i < M; i++)
        dot += Q[i * N + k] * A[i * N + j];

    R[k * N + j] = dot;

    for (uint i = 0; i < M; i++)
        A[i * N + j] -= dot * Q[i * N + k];
}
