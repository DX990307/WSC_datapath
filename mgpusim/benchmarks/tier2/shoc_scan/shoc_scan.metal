/*
 * shoc_scan.metal — Metal compute shaders for the SHOC Scan benchmark.
 *
 * Implements an exclusive prefix scan (sum) using a work-efficient
 * (Blelloch) two-phase algorithm in threadgroup memory.
 *
 * The host (shoc_scan_metal.mm) orchestrates a multi-pass scan for arrays
 * larger than a single threadgroup:
 *
 *   Pass 1  scan_block_kernel  over the full input  →  per-block scanned output
 *                                                   →  block_sums[]
 *   Pass 2  (recursive) scan  block_sums[]          →  scanned_block_sums[]
 *   Pass 3  add_block_sums_kernel propagates scanned_block_sums back
 *
 * Each threadgroup handles TG_SIZE * 2 elements (two elements per thread).
 */

#include <metal_stdlib>
using namespace metal;

// ---------------------------------------------------------------------------
// scan_block_kernel
//
// Performs a work-efficient (Blelloch up-sweep / down-sweep) exclusive
// prefix scan over the elements owned by one threadgroup.
//
// Parameters:
//   input       — source float array
//   output      — destination float array (exclusive scan result)
//   block_sums  — one float per threadgroup: the block's total (pre-zeroing)
//   N           — number of valid elements in input
//   scratch     — threadgroup memory: 2 * threads_per_threadgroup floats
//   tgid        — threadgroup index in the grid
//   lid         — thread index within the threadgroup
//   tg_size     — threads per threadgroup
// ---------------------------------------------------------------------------
kernel void scan_block_kernel(
    device const float*  input      [[ buffer(0) ]],
    device       float*  output     [[ buffer(1) ]],
    device       float*  block_sums [[ buffer(2) ]],
    constant     uint&   N          [[ buffer(3) ]],
    threadgroup  float*  scratch    [[ threadgroup(0) ]],
    uint tgid    [[ threadgroup_position_in_grid ]],
    uint lid     [[ thread_position_in_threadgroup ]],
    uint tg_size [[ threads_per_threadgroup ]])
{
    uint n        = tg_size * 2;          // elements per threadgroup
    uint base     = tgid * n;             // global offset for this threadgroup
    uint ai       = lid;
    uint bi       = lid + tg_size;

    // Load two elements per thread; zero-pad if out of bounds
    scratch[ai] = (base + ai < N) ? input[base + ai] : 0.0f;
    scratch[bi] = (base + bi < N) ? input[base + bi] : 0.0f;
    threadgroup_barrier(mem_flags::mem_threadgroup);

    // ---- Up-sweep (reduce) phase ----
    uint offset = 1;
    for (uint d = tg_size; d > 0; d >>= 1) {
        threadgroup_barrier(mem_flags::mem_threadgroup);
        if (lid < d) {
            uint ai2 = offset * (2 * lid + 1) - 1;
            uint bi2 = offset * (2 * lid + 2) - 1;
            scratch[bi2] += scratch[ai2];
        }
        offset <<= 1;
    }

    // Save the block total, then clear the last element to seed the down-sweep
    threadgroup_barrier(mem_flags::mem_threadgroup);
    if (lid == 0) {
        block_sums[tgid] = scratch[n - 1];
        scratch[n - 1]   = 0.0f;
    }
    threadgroup_barrier(mem_flags::mem_threadgroup);

    // ---- Down-sweep phase ----
    for (uint d = 1; d < n; d <<= 1) {
        offset >>= 1;
        threadgroup_barrier(mem_flags::mem_threadgroup);
        if (lid < d) {
            uint  ai2 = offset * (2 * lid + 1) - 1;
            uint  bi2 = offset * (2 * lid + 2) - 1;
            float t   = scratch[ai2];
            scratch[ai2] = scratch[bi2];
            scratch[bi2] += t;
        }
    }
    threadgroup_barrier(mem_flags::mem_threadgroup);

    // Write results (guard against out-of-bounds for padded arrays)
    if (base + ai < N) output[base + ai] = scratch[ai];
    if (base + bi < N) output[base + bi] = scratch[bi];
}

// ---------------------------------------------------------------------------
// add_block_sums_kernel
//
// After the per-block sums have themselves been exclusively scanned, this
// kernel adds each block's prefix sum to all elements in that block.
// Block 0 is a no-op (its prefix is 0 by definition).
//
// Parameters:
//   data        — in/out: the per-block scanned array to fix up
//   block_sums  — exclusively-scanned block prefix sums
//   N           — number of valid elements
//   tgid        — threadgroup index
//   lid         — thread index within threadgroup
//   tg_size     — threads per threadgroup
// ---------------------------------------------------------------------------
kernel void add_block_sums_kernel(
    device       float*  data       [[ buffer(0) ]],
    device const float*  block_sums [[ buffer(1) ]],
    constant     uint&   N          [[ buffer(2) ]],
    uint tgid    [[ threadgroup_position_in_grid ]],
    uint lid     [[ thread_position_in_threadgroup ]],
    uint tg_size [[ threads_per_threadgroup ]])
{
    // Block 0 needs no adjustment
    if (tgid == 0) return;

    float addend = block_sums[tgid];
    uint  n      = tg_size * 2;
    uint  base   = tgid * n;
    uint  idx_a  = base + lid;
    uint  idx_b  = base + lid + tg_size;

    if (idx_a < N) data[idx_a] += addend;
    if (idx_b < N) data[idx_b] += addend;
}
