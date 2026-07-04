/*
 * npb_cg.metal — Metal compute shaders for NPB Conjugate Gradient.
 *
 * Kernels:
 *   1. spmv_kernel      — Sparse matrix-vector multiply (CSR)
 *   2. dot_product_kernel — Block-level partial dot product
 *   3. axpy_kernel       — y = alpha * x + y
 *   4. scale_kernel      — y = alpha * x
 */

#include <metal_stdlib>
using namespace metal;

// ---------------------------------------------------------------------------
// Parameter structs
// ---------------------------------------------------------------------------

struct CgParams {
    uint N;
    float alpha;
};

// ---------------------------------------------------------------------------
// Kernel: SpMV (CSR)
// ---------------------------------------------------------------------------

kernel void spmv_kernel(
    device const int*   row_ptr   [[ buffer(0) ]],
    device const int*   col_idx   [[ buffer(1) ]],
    device const float* values    [[ buffer(2) ]],
    device const float* x         [[ buffer(3) ]],
    device float*       y         [[ buffer(4) ]],
    constant CgParams&  params    [[ buffer(5) ]],
    uint                gid       [[ thread_position_in_grid ]])
{
    if (gid >= params.N) return;

    float sum = 0.0f;
    int start = row_ptr[gid];
    int end   = row_ptr[gid + 1];
    for (int j = start; j < end; ++j) {
        sum += values[j] * x[col_idx[j]];
    }
    y[gid] = sum;
}

// ---------------------------------------------------------------------------
// Kernel: Partial dot product (threadgroup reduction)
// ---------------------------------------------------------------------------

kernel void dot_product_kernel(
    device const float* a         [[ buffer(0) ]],
    device const float* b         [[ buffer(1) ]],
    device float*       partial   [[ buffer(2) ]],
    constant CgParams&  params    [[ buffer(3) ]],
    uint                gid       [[ thread_position_in_grid ]],
    uint                lid       [[ thread_position_in_threadgroup ]],
    uint                gid_group [[ threadgroup_position_in_grid ]],
    uint                tg_size   [[ threads_per_threadgroup ]])
{
    threadgroup float sdata[256];

    float val = 0.0f;
    if (gid < params.N) val = a[gid] * b[gid];
    sdata[lid] = val;
    threadgroup_barrier(mem_flags::mem_threadgroup);

    for (uint s = tg_size / 2; s > 0; s >>= 1) {
        if (lid < s) sdata[lid] += sdata[lid + s];
        threadgroup_barrier(mem_flags::mem_threadgroup);
    }

    if (lid == 0) partial[gid_group] = sdata[0];
}

// ---------------------------------------------------------------------------
// Kernel: AXPY  y = alpha * x + y
// ---------------------------------------------------------------------------

kernel void axpy_kernel(
    device const float* x         [[ buffer(0) ]],
    device float*       y         [[ buffer(1) ]],
    constant CgParams&  params    [[ buffer(2) ]],
    uint                gid       [[ thread_position_in_grid ]])
{
    if (gid >= params.N) return;
    y[gid] = params.alpha * x[gid] + y[gid];
}

// ---------------------------------------------------------------------------
// Kernel: Scale  y = alpha * x
// ---------------------------------------------------------------------------

kernel void scale_kernel(
    device const float* x         [[ buffer(0) ]],
    device float*       y         [[ buffer(1) ]],
    constant CgParams&  params    [[ buffer(2) ]],
    uint                gid       [[ thread_position_in_grid ]])
{
    if (gid >= params.N) return;
    y[gid] = params.alpha * x[gid];
}

// ---------------------------------------------------------------------------
// Kernel: Copy  y = x
// ---------------------------------------------------------------------------

kernel void copy_kernel(
    device const float* x         [[ buffer(0) ]],
    device float*       y         [[ buffer(1) ]],
    constant CgParams&  params    [[ buffer(2) ]],
    uint                gid       [[ thread_position_in_grid ]])
{
    if (gid >= params.N) return;
    y[gid] = x[gid];
}
