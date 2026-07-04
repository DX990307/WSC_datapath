/*
 * tango_blackscholes.metal — Metal compute shader for Black-Scholes pricing.
 *
 * Each thread computes call and put prices for one option using the
 * Black-Scholes formula with a polynomial CND approximation.
 *
 * Buffer layout:
 *   buffer(0): S          — float[N], stock prices
 *   buffer(1): K          — float[N], strike prices
 *   buffer(2): T          — float[N], time to expiration
 *   buffer(3): sigma      — float[N], volatility
 *   buffer(4): callPrice  — float[N], output call prices
 *   buffer(5): putPrice   — float[N], output put prices
 *   buffer(6): params     — uint N, float r
 */

#include <metal_stdlib>
using namespace metal;

// ---------------------------------------------------------------------------
// Cumulative normal distribution (Abramowitz & Stegun 26.2.17)
// ---------------------------------------------------------------------------

static inline float cnd(float d) {
    const float A1 = 0.31938153f;
    const float A2 = -0.356563782f;
    const float A3 = 1.781477937f;
    const float A4 = -1.821255978f;
    const float A5 = 1.330274429f;
    const float RSQRT2PI = 0.39894228040143267793994605993438f;

    float K = 1.0f / (1.0f + 0.2316419f * abs(d));
    float cnd_val = RSQRT2PI * exp(-0.5f * d * d) *
                    (K * (A1 + K * (A2 + K * (A3 + K * (A4 + K * A5)))));

    if (d > 0.0f) cnd_val = 1.0f - cnd_val;
    return cnd_val;
}

// ---------------------------------------------------------------------------
// Parameters struct
// ---------------------------------------------------------------------------

struct BSParams {
    uint  N;
    float r;
};

// ---------------------------------------------------------------------------
// Black-Scholes kernel
// ---------------------------------------------------------------------------

kernel void blackscholes_kernel(
    device const float*  S          [[ buffer(0) ]],
    device const float*  K          [[ buffer(1) ]],
    device const float*  T          [[ buffer(2) ]],
    device const float*  sigma      [[ buffer(3) ]],
    device float*        callPrice  [[ buffer(4) ]],
    device float*        putPrice   [[ buffer(5) ]],
    constant BSParams&   params     [[ buffer(6) ]],
    uint                 idx        [[ thread_position_in_grid ]])
{
    if (idx >= params.N) return;

    float s  = S[idx];
    float k  = K[idx];
    float t  = T[idx];
    float v  = sigma[idx];
    float r  = params.r;

    float sqrtT  = sqrt(t);
    float d1     = (log(s / k) + (r + 0.5f * v * v) * t) / (v * sqrtT);
    float d2     = d1 - v * sqrtT;

    float expRT  = exp(-r * t);
    float cnd_d1 = cnd(d1);
    float cnd_d2 = cnd(d2);

    callPrice[idx] = s * cnd_d1 - k * expRT * cnd_d2;
    putPrice[idx]  = k * expRT * (1.0f - cnd_d2) - s * (1.0f - cnd_d1);
}
