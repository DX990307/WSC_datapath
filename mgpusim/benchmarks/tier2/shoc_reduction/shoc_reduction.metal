/*
 * shoc_reduction.metal — Metal compute shader for the SHOC Reduction benchmark.
 *
 * Implements a parallel sum reduction using threadgroup (shared) memory.
 * Each threadgroup reduces 2 * THREADS_PER_THREADGROUP elements to one
 * partial sum, written to partial_sums[threadgroup_position_in_grid].
 *
 * The host (shoc_reduction_metal.mm) runs two passes:
 *   Pass 1 — reduce_kernel over the full input array → partial_sums[]
 *   Pass 2 — reduce_kernel over partial_sums[]       → result[0]
 */

#include <metal_stdlib>
using namespace metal;

/*
 * reduce_kernel — tree-reduction within a threadgroup.
 *
 * @param input       Input float array
 * @param output      Output array: one partial sum per threadgroup
 * @param params      params[0] = N (number of elements in `input`)
 * @param scratch     Threadgroup scratch memory (THREADS_PER_THREADGROUP floats)
 * @param tgid        Threadgroup index in the grid
 * @param lid         Lane index within the threadgroup
 * @param tg_size     Number of threads per threadgroup (from [[threads_per_threadgroup]])
 */
kernel void reduce_kernel(
    device const float*  input   [[ buffer(0) ]],
    device       float*  output  [[ buffer(1) ]],
    constant     uint&   N       [[ buffer(2) ]],
    threadgroup  float*  scratch [[ threadgroup(0) ]],
    uint tgid    [[ threadgroup_position_in_grid ]],
    uint lid     [[ thread_position_in_threadgroup ]],
    uint tg_size [[ threads_per_threadgroup ]])
{
    // Each thread is responsible for 2 consecutive elements.
    uint base = tgid * tg_size * 2 + lid;

    float val = 0.0f;
    if (base < N)
        val = input[base];
    if (base + tg_size < N)
        val += input[base + tg_size];

    scratch[lid] = val;
    threadgroup_barrier(mem_flags::mem_threadgroup);

    // Tree reduction: halve active threads each step
    for (uint s = tg_size / 2; s > 0; s >>= 1) {
        if (lid < s) {
            scratch[lid] += scratch[lid + s];
        }
        threadgroup_barrier(mem_flags::mem_threadgroup);
    }

    // Thread 0 of each threadgroup writes the partial sum
    if (lid == 0) {
        output[tgid] = scratch[0];
    }
}
