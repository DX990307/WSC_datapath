/*
 * shoc_fft.metal — Metal compute shaders for the SHOC FFT benchmark.
 *
 * Cooley-Tukey radix-2 iterative FFT on N complex elements (float2).
 *
 * Two kernels:
 *   1. bit_reverse_kernel  — in-place bit-reversal permutation
 *   2. fft_butterfly_kernel — one butterfly stage (dispatched once per stage)
 *
 * Buffer layout:
 *   buffer(0): data   — float2[N], complex input/output
 *   buffer(1): params — uint2 { N, log2N } for bit-reverse,
 *                        uint2 { N, stage } for butterfly
 */

#include <metal_stdlib>
using namespace metal;

// ---------------------------------------------------------------------------
// Bit-reversal permutation kernel
//   Each thread handles one element. Only swaps if rev > idx.
//   params.x = N, params.y = log2N
// ---------------------------------------------------------------------------

kernel void bit_reverse_kernel(
    device float2*    data   [[ buffer(0) ]],
    constant uint2&   params [[ buffer(1) ]],
    uint              idx    [[ thread_position_in_grid ]])
{
    uint N     = params.x;
    uint log2N = params.y;

    if (idx >= N) return;

    // Compute bit-reversed index
    uint rev  = 0;
    uint temp = idx;
    for (uint i = 0; i < log2N; ++i) {
        rev  = (rev << 1) | (temp & 1);
        temp >>= 1;
    }

    // Swap only once per pair
    if (rev > idx) {
        float2 tmp = data[idx];
        data[idx]  = data[rev];
        data[rev]  = tmp;
    }
}

// ---------------------------------------------------------------------------
// FFT butterfly kernel (one stage)
//   Each thread handles one butterfly operation (N/2 total per stage).
//   params.x = N, params.y = stage
// ---------------------------------------------------------------------------

kernel void fft_butterfly_kernel(
    device float2*    data   [[ buffer(0) ]],
    constant uint2&   params [[ buffer(1) ]],
    uint              idx    [[ thread_position_in_grid ]])
{
    uint N     = params.x;
    uint stage = params.y;

    if (idx >= N / 2) return;

    uint m      = 1u << (stage + 1);   // butterfly group size
    uint half_m = 1u << stage;          // half of group size

    uint group = idx / half_m;
    uint pair  = idx % half_m;

    uint top = group * m + pair;
    uint bot = top + half_m;

    float angle = -2.0f * M_PI_F * float(pair) / float(m);
    float w_re  = cos(angle);
    float w_im  = sin(angle);

    float2 u = data[top];
    float2 v = data[bot];

    // Complex multiply: t = w * v
    float t_re = w_re * v.x - w_im * v.y;
    float t_im = w_re * v.y + w_im * v.x;

    data[top] = float2(u.x + t_re, u.y + t_im);
    data[bot] = float2(u.x - t_re, u.y - t_im);
}
