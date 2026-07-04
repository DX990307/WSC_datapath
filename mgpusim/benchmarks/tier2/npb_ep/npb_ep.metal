/*
 * npb_ep.metal — Metal compute shaders for NAS EP benchmark.
 *
 * Generates N pairs of Gaussian random deviates via Box-Muller transform
 * with per-thread integer LCG RNG, counts into 10 annular bins.
 *
 * Kernel:
 *   ep_kernel — each thread produces one Gaussian pair, atomically bins it.
 *
 * Buffer layout:
 *   buffer(0): bins    — atomic_uint[NUM_BINS], bin counters
 *   buffer(1): params  — uint { N }
 */

#include <metal_stdlib>
using namespace metal;

#define NUM_BINS 10

// ---------------------------------------------------------------------------
// Integer LCG RNG — matches CPU/HIP version exactly
//   seed = seed * 1103515245 + 12345  (mod 2^32 by uint overflow)
// ---------------------------------------------------------------------------

static inline uint lcg_next(uint seed) {
    return seed * 1103515245u + 12345u;
}

// ---------------------------------------------------------------------------
// EP kernel: generate Gaussian pair + bin
// ---------------------------------------------------------------------------

kernel void ep_kernel(
    device atomic_uint* bins   [[ buffer(0) ]],
    constant uint&      N      [[ buffer(1) ]],
    uint                idx    [[ thread_position_in_grid ]])
{
    if (idx >= N) return;

    // Per-thread seed derived from thread index
    uint seed = idx + 1u;
    // Advance RNG for decorrelation
    seed = lcg_next(seed);
    seed = lcg_next(seed);

    // Generate two uniform random numbers in (0, 1)
    seed = lcg_next(seed);
    float u1 = float(seed) / 4294967296.0f;
    seed = lcg_next(seed);
    float u2 = float(seed) / 4294967296.0f;

    // Avoid log(0)
    if (u1 < 1e-10f) u1 = 1e-10f;

    // Box-Muller transform
    float r = sqrt(-2.0f * log(u1));
    float theta = 2.0f * M_PI_F * u2;
    float x1 = r * cos(theta);
    float x2 = r * sin(theta);

    // Distance squared
    float t = x1 * x1 + x2 * x2;

    // Bin index: floor(sqrt(t)), capped at NUM_BINS-1
    int bin = int(sqrt(t));
    if (bin >= NUM_BINS) bin = NUM_BINS - 1;
    if (bin < 0) bin = 0;

    atomic_fetch_add_explicit(&bins[bin], 1u, memory_order_relaxed);
}
