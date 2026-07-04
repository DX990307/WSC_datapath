/*
 * shoc_triad.metal — Metal compute shader for the SHOC Triad benchmark.
 *
 * Implements the Stream Triad: a[i] = b[i] + scalar * c[i]
 *
 * Each thread processes one float element.  The host code in
 * shoc_triad_metal.mm dispatches enough threads to cover all N elements.
 */

#include <metal_stdlib>
using namespace metal;

/**
 * triad_kernel — Stream Triad kernel.
 *
 * @param a       Output buffer: a[i] = b[i] + scalar * c[i]
 * @param b       Input buffer b
 * @param c       Input buffer c
 * @param params  Packed parameters: params[0] = N (element count),
 *                                   params[1] = scalar (as uint bit-pattern)
 * @param tid     Thread index in the global grid
 */
kernel void triad_kernel(device       float*  a      [[ buffer(0) ]],
                         device const float*  b      [[ buffer(1) ]],
                         device const float*  c      [[ buffer(2) ]],
                         constant     uint2&  params [[ buffer(3) ]],
                         uint tid [[ thread_position_in_grid ]])
{
    uint N = params.x;
    // Reinterpret the bit-pattern stored in params.y back to float
    float scalar = as_type<float>(params.y);

    if (tid < N) {
        a[tid] = b[tid] + scalar * c[tid];
    }
}
