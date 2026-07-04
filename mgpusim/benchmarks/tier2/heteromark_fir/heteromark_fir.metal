/*
 * heteromark_fir.metal — Metal compute shader for FIR filter benchmark.
 *
 * 1D FIR (Finite Impulse Response) convolution:
 *   output[i] = sum(coeff[k] * input[i - k]) for k = 0..num_taps-1
 *
 * Each thread computes one output sample. Filter coefficients are loaded
 * into threadgroup memory for performance.
 *
 * Buffer layout:
 *   buffer(0): input  — float[N], input signal
 *   buffer(1): output — float[N], filtered output
 *   buffer(2): coeff  — float[num_taps], filter coefficients
 *   buffer(3): params — uint2 { num_samples, num_taps }
 */

#include <metal_stdlib>
using namespace metal;

#define MAX_TAPS 128

// ---------------------------------------------------------------------------
// FIR filter kernel — one output sample per thread
//   Uses threadgroup memory for filter coefficients.
// ---------------------------------------------------------------------------

kernel void fir_filter_kernel(
    device const float*    input       [[ buffer(0) ]],
    device float*          output      [[ buffer(1) ]],
    device const float*    coeff       [[ buffer(2) ]],
    constant uint2&        params      [[ buffer(3) ]],
    uint                   idx         [[ thread_position_in_grid ]],
    uint                   tid         [[ thread_index_in_threadgroup ]],
    uint                   tg_size     [[ threads_per_threadgroup ]])
{
    uint num_samples = params.x;
    uint num_taps    = params.y;

    // Load coefficients into threadgroup memory cooperatively
    threadgroup float s_coeff[MAX_TAPS];
    for (uint t = tid; t < num_taps; t += tg_size) {
        s_coeff[t] = coeff[t];
    }
    threadgroup_barrier(mem_flags::mem_threadgroup);

    if (idx >= num_samples) return;

    float sum = 0.0f;
    for (uint k = 0; k < num_taps; ++k) {
        int in_idx = (int)idx - (int)k;
        if (in_idx >= 0) {
            sum += s_coeff[k] * input[in_idx];
        }
    }

    output[idx] = sum;
}
