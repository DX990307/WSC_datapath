/*
 * chai_hsto.metal — Metal compute shader for histogram benchmark.
 *
 * Computes a 256-bin histogram of N bytes using threadgroup memory
 * privatization and atomic reduction to global memory.
 *
 * Buffer layout:
 *   buffer(0): data  — uchar[N], input bytes
 *   buffer(1): histo — uint[256], output histogram bins
 *   buffer(2): params — { N }
 */

#include <metal_stdlib>
using namespace metal;

#define NUM_BINS 256

// ---------------------------------------------------------------------------
// Histogram kernel — shared memory privatization + global reduction
// ---------------------------------------------------------------------------

kernel void histogram_kernel(
    device const uchar*  data   [[ buffer(0) ]],
    device atomic_uint*  histo  [[ buffer(1) ]],
    constant uint&       N      [[ buffer(2) ]],
    uint                 gid    [[ thread_position_in_grid ]],
    uint                 tid    [[ thread_position_in_threadgroup ]],
    uint                 tgSize [[ threads_per_threadgroup ]],
    uint                 gridSz [[ threads_per_grid ]])
{
    // Threadgroup-local histogram
    threadgroup atomic_uint s_histo[NUM_BINS];

    // Initialize shared histogram to zero
    if (tid < NUM_BINS) {
        atomic_store_explicit(&s_histo[tid], 0, memory_order_relaxed);
    }
    threadgroup_barrier(mem_flags::mem_threadgroup);

    // Each thread processes multiple elements (grid-stride loop)
    for (uint i = gid; i < N; i += gridSz) {
        atomic_fetch_add_explicit(&s_histo[data[i]], 1, memory_order_relaxed);
    }
    threadgroup_barrier(mem_flags::mem_threadgroup);

    // Reduce shared histogram to global histogram
    if (tid < NUM_BINS) {
        uint val = atomic_load_explicit(&s_histo[tid], memory_order_relaxed);
        if (val > 0) {
            atomic_fetch_add_explicit(&histo[tid], val, memory_order_relaxed);
        }
    }
}
