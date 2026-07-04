/*
 * cuda_scan_large.metal — Metal compute shaders for large-array prefix sum.
 *
 * Work-efficient Blelloch scan (exclusive prefix sum) for large arrays.
 * Three-phase hierarchical decomposition.
 *
 * Kernels:
 *   1. scan_block_kernel  — per-block exclusive scan + extract block sum
 *   2. add_block_sums_kernel — add scanned block sums back to each element
 *
 * Buffer layout for scan_block_kernel:
 *   buffer(0): data       — uint[N], input/output
 *   buffer(1): block_sums — uint[numBlocks], output block totals
 *   buffer(2): params     — { N } as uint
 *
 * Buffer layout for add_block_sums_kernel:
 *   buffer(0): data       — uint[N], input/output
 *   buffer(1): block_sums — uint[numBlocks], scanned block sums
 *   buffer(2): params     — { N } as uint
 */

#include <metal_stdlib>
using namespace metal;

constant int BLOCK_SIZE = 256;
constant int ELEMENTS_PER_BLOCK = 512;  // 2 * BLOCK_SIZE

// ---------------------------------------------------------------------------
// Blelloch scan kernel (per-block exclusive scan)
//   Each threadgroup processes ELEMENTS_PER_BLOCK elements.
//   Stores the block total in block_sums before zeroing the root.
// ---------------------------------------------------------------------------

kernel void scan_block_kernel(
    device uint*          data       [[ buffer(0) ]],
    device uint*          block_sums [[ buffer(1) ]],
    constant uint&        param_N    [[ buffer(2) ]],
    uint                  tid        [[ thread_index_in_threadgroup ]],
    uint                  tgid       [[ threadgroup_position_in_grid ]])
{
    threadgroup uint temp[512];  // ELEMENTS_PER_BLOCK

    uint N = param_N;
    uint blockOffset = tgid * ELEMENTS_PER_BLOCK;

    // Load input into threadgroup memory
    uint ai = tid;
    uint bi = tid + BLOCK_SIZE;
    uint ga = blockOffset + ai;
    uint gb = blockOffset + bi;

    temp[ai] = (ga < N) ? data[ga] : 0u;
    temp[bi] = (gb < N) ? data[gb] : 0u;

    // Up-sweep (reduce) phase
    uint offset = 1u;
    for (int d = ELEMENTS_PER_BLOCK >> 1; d > 0; d >>= 1) {
        threadgroup_barrier(mem_flags::mem_threadgroup);
        if (tid < uint(d)) {
            uint ai2 = offset * (2u * tid + 1u) - 1u;
            uint bi2 = offset * (2u * tid + 2u) - 1u;
            temp[bi2] += temp[ai2];
        }
        offset <<= 1u;
    }

    threadgroup_barrier(mem_flags::mem_threadgroup);

    // Store block sum and clear last element
    if (tid == 0u) {
        block_sums[tgid] = temp[ELEMENTS_PER_BLOCK - 1];
        temp[ELEMENTS_PER_BLOCK - 1] = 0u;
    }

    // Down-sweep phase
    for (int d = 1; d < ELEMENTS_PER_BLOCK; d <<= 1) {
        offset >>= 1u;
        threadgroup_barrier(mem_flags::mem_threadgroup);
        if (tid < uint(d)) {
            uint ai2 = offset * (2u * tid + 1u) - 1u;
            uint bi2 = offset * (2u * tid + 2u) - 1u;
            uint t = temp[ai2];
            temp[ai2] = temp[bi2];
            temp[bi2] += t;
        }
    }

    threadgroup_barrier(mem_flags::mem_threadgroup);

    // Write results back
    if (ga < N) data[ga] = temp[ai];
    if (gb < N) data[gb] = temp[bi];
}

// ---------------------------------------------------------------------------
// Add scanned block sums back to each element
// ---------------------------------------------------------------------------

kernel void add_block_sums_kernel(
    device uint*          data       [[ buffer(0) ]],
    device const uint*    block_sums [[ buffer(1) ]],
    constant uint&        param_N    [[ buffer(2) ]],
    uint                  tid        [[ thread_index_in_threadgroup ]],
    uint                  tgid       [[ threadgroup_position_in_grid ]])
{
    uint N = param_N;
    uint blockOffset = tgid * ELEMENTS_PER_BLOCK;
    uint val = block_sums[tgid];

    uint idx0 = blockOffset + tid;
    uint idx1 = blockOffset + tid + BLOCK_SIZE;

    if (idx0 < N) data[idx0] += val;
    if (idx1 < N) data[idx1] += val;
}
