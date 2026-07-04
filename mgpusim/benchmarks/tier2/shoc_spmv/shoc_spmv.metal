/*
 * shoc_spmv.metal — Metal compute shader for SHOC CSR Sparse Matrix-Vector Multiply.
 *
 * Computes: y = A * x
 *   where A is a synthetic CSR sparse matrix with exactly NNZ_PER_ROW non-zeros per row.
 *
 * Each thread computes one output row:
 *   y[gid] = sum(values[k] * x[col_idx[k]]) for k in [row_ptr[gid], row_ptr[gid+1])
 *
 * Buffers (bound by the Metal host in shoc_spmv_metal.mm):
 *   0 — row_ptr   : int array, length (num_rows + 1)
 *   1 — col_idx   : int array, length NNZ
 *   2 — values    : float array, length NNZ
 *   3 — x         : float array, length num_rows (dense input vector)
 *   4 — y         : float array, length num_rows (dense output vector)
 *   5 — num_rows  : uint constant
 */

#include <metal_stdlib>
using namespace metal;

kernel void spmv_csr_kernel(
    device const int*   row_ptr  [[ buffer(0) ]],
    device const int*   col_idx  [[ buffer(1) ]],
    device const float* values   [[ buffer(2) ]],
    device const float* x        [[ buffer(3) ]],
    device       float* y        [[ buffer(4) ]],
    constant     uint&  num_rows [[ buffer(5) ]],
    uint gid [[ thread_position_in_grid ]])
{
    if (gid >= num_rows) return;
    float sum = 0.0f;
    int start = row_ptr[gid];
    int end   = row_ptr[gid + 1];
    for (int k = start; k < end; ++k)
        sum += values[k] * x[col_idx[k]];
    y[gid] = sum;
}
