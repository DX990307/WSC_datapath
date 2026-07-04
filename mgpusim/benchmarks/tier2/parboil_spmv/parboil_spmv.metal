/*
 * parboil_spmv.metal — Metal compute shader for CSR Sparse Matrix-Vector Multiplication.
 *
 * Implements: y = A * x
 *   where A is stored in Compressed Sparse Row (CSR) format.
 *
 * Each thread computes one row of the output:
 *   y[row] = sum(values[j] * x[col_idx[j]]) for j in [row_ptr[row], row_ptr[row+1])
 *
 * Buffers (bound by the Metal host in parboil_spmv_metal.mm):
 *   0 — row_ptr   : uint array, length (num_rows + 1)
 *   1 — col_idx   : uint array, length total_nnz
 *   2 — values    : float array, length total_nnz
 *   3 — x         : float array, length num_rows (dense input vector)
 *   4 — y         : float array, length num_rows (dense output vector)
 *   5 — params    : packed { uint num_rows }
 */

#include <metal_stdlib>
using namespace metal;

kernel void spmv_csr_kernel(device const uint*  row_ptr [[ buffer(0) ]],
                             device const uint*  col_idx [[ buffer(1) ]],
                             device const float* values  [[ buffer(2) ]],
                             device const float* x       [[ buffer(3) ]],
                             device       float* y       [[ buffer(4) ]],
                             constant     uint&  num_rows[[ buffer(5) ]],
                             uint tid [[ thread_position_in_grid ]])
{
    if (tid >= num_rows) return;

    uint row_start = row_ptr[tid];
    uint row_end   = row_ptr[tid + 1];
    float sum = 0.0f;
    for (uint j = row_start; j < row_end; ++j) {
        sum += values[j] * x[col_idx[j]];
    }
    y[tid] = sum;
}
