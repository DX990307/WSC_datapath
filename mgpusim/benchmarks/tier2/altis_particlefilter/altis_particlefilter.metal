/*
 * altis_particlefilter.metal — Metal compute shaders for Particle Filter.
 *
 * Sequential Importance Resampling (SIR) particle filter.
 *
 * Kernel 1 (update_particles_kernel): Random walk update for particle positions.
 * Kernel 2 (compute_weights_kernel): Gaussian likelihood weight computation.
 * Kernel 3 (prefix_sum_kernel / add_block_sums_kernel): Parallel prefix sum.
 * Kernel 4 (resample_kernel): Systematic resampling via binary search in CDF.
 */

#include <metal_stdlib>
using namespace metal;

#define GRID_SIZE    128
#define SIGMA_OBS    10.0f
#define SIGMA_MOVE   2.0f

// ---------------------------------------------------------------------------
// Parameters structs
// ---------------------------------------------------------------------------

struct PFParams {
    uint N;
    float obs_x;
    float obs_y;
    float total_weight;
};

struct ScanParams {
    uint N;
};

// ---------------------------------------------------------------------------
// PRNG (xorshift32)
// ---------------------------------------------------------------------------

static inline uint gpu_xorshift(uint s) {
    s ^= s << 13;
    s ^= s >> 17;
    s ^= s << 5;
    return s;
}

static inline float gpu_rand_uniform(thread uint& state) {
    state = gpu_xorshift(state);
    return (float)(state & 0xFFFFFF) / 16777216.0f;
}

static inline float gpu_rand_normal(thread uint& state) {
    float u1 = gpu_rand_uniform(state);
    float u2 = gpu_rand_uniform(state);
    u1 = max(u1, 1e-10f);
    return sqrt(-2.0f * log(u1)) * cos(2.0f * 3.14159265f * u2);
}

// ---------------------------------------------------------------------------
// Kernel 1: Update particle positions
// ---------------------------------------------------------------------------

kernel void update_particles_kernel(
    device float*  x_pos      [[ buffer(0) ]],
    device float*  y_pos      [[ buffer(1) ]],
    device uint*   rng_states [[ buffer(2) ]],
    constant PFParams& params [[ buffer(3) ]],
    uint idx                  [[ thread_position_in_grid ]])
{
    if (idx >= params.N) return;

    uint rng = rng_states[idx];

    float dx = SIGMA_MOVE * gpu_rand_normal(rng);
    float dy = SIGMA_MOVE * gpu_rand_normal(rng);

    float nx = x_pos[idx] + dx;
    float ny = y_pos[idx] + dy;

    nx = min(max(nx, 0.0f), (float)(GRID_SIZE - 1));
    ny = min(max(ny, 0.0f), (float)(GRID_SIZE - 1));

    x_pos[idx] = nx;
    y_pos[idx] = ny;
    rng_states[idx] = rng;
}

// ---------------------------------------------------------------------------
// Kernel 2: Compute weights
// ---------------------------------------------------------------------------

kernel void compute_weights_kernel(
    device const float*  x_pos   [[ buffer(0) ]],
    device const float*  y_pos   [[ buffer(1) ]],
    device float*        weights [[ buffer(2) ]],
    constant PFParams&   params  [[ buffer(3) ]],
    uint idx                     [[ thread_position_in_grid ]])
{
    if (idx >= params.N) return;

    float dx = x_pos[idx] - params.obs_x;
    float dy = y_pos[idx] - params.obs_y;
    float dist2 = dx * dx + dy * dy;

    float w = exp(-dist2 / (2.0f * SIGMA_OBS * SIGMA_OBS));
    weights[idx] = w;
}

// ---------------------------------------------------------------------------
// Kernel 3a: Prefix sum (Blelloch scan) within blocks
// ---------------------------------------------------------------------------

kernel void prefix_sum_kernel(
    device float*         data       [[ buffer(0) ]],
    device float*         block_sums [[ buffer(1) ]],
    constant ScanParams&  params     [[ buffer(2) ]],
    threadgroup float*    sdata      [[ threadgroup(0) ]],
    uint tid                         [[ thread_index_in_threadgroup ]],
    uint bid                         [[ threadgroup_position_in_grid ]],
    uint tgSize                      [[ threads_per_threadgroup ]])
{
    uint n = tgSize * 2;
    uint gid = bid * n + tid;

    sdata[tid]         = (gid < params.N)         ? data[gid]         : 0.0f;
    sdata[tid + tgSize] = (gid + tgSize < params.N) ? data[gid + tgSize] : 0.0f;

    threadgroup_barrier(mem_flags::mem_threadgroup);

    // Up-sweep
    for (uint stride = 1; stride < n; stride *= 2) {
        uint index = (tid + 1) * stride * 2 - 1;
        if (index < n)
            sdata[index] += sdata[index - stride];
        threadgroup_barrier(mem_flags::mem_threadgroup);
    }

    // Save block total and clear
    if (tid == 0) {
        block_sums[bid] = sdata[n - 1];
        sdata[n - 1] = 0.0f;
    }
    threadgroup_barrier(mem_flags::mem_threadgroup);

    // Down-sweep
    for (uint stride = n / 2; stride >= 1; stride /= 2) {
        uint index = (tid + 1) * stride * 2 - 1;
        if (index < n) {
            float t = sdata[index - stride];
            sdata[index - stride] = sdata[index];
            sdata[index] += t;
        }
        threadgroup_barrier(mem_flags::mem_threadgroup);
    }

    if (gid < params.N)         data[gid]         = sdata[tid];
    if (gid + tgSize < params.N) data[gid + tgSize] = sdata[tid + tgSize];
}

// ---------------------------------------------------------------------------
// Kernel 3b: Add block sums
// ---------------------------------------------------------------------------

kernel void add_block_sums_kernel(
    device float*         data       [[ buffer(0) ]],
    device const float*   block_sums [[ buffer(1) ]],
    constant ScanParams&  params     [[ buffer(2) ]],
    uint tid                         [[ thread_index_in_threadgroup ]],
    uint bid                         [[ threadgroup_position_in_grid ]],
    uint tgSize                      [[ threads_per_threadgroup ]])
{
    uint n = tgSize * 2;
    uint gid = bid * n + tid;
    float val = block_sums[bid];

    if (gid < params.N)         data[gid]         += val;
    if (gid + tgSize < params.N) data[gid + tgSize] += val;
}

// ---------------------------------------------------------------------------
// Kernel 4: Systematic resampling
// ---------------------------------------------------------------------------

kernel void resample_kernel(
    device const float*  cdf        [[ buffer(0) ]],
    device const float*  x_pos_in   [[ buffer(1) ]],
    device const float*  y_pos_in   [[ buffer(2) ]],
    device float*        x_pos_out  [[ buffer(3) ]],
    device float*        y_pos_out  [[ buffer(4) ]],
    device uint*         rng_states [[ buffer(5) ]],
    constant PFParams&   params     [[ buffer(6) ]],
    uint idx                        [[ thread_position_in_grid ]])
{
    if (idx >= params.N) return;

    uint rng = rng_states[idx];
    float u0 = gpu_rand_uniform(rng);
    rng_states[idx] = rng;

    float target = (((float)idx + u0) / (float)params.N) * params.total_weight;

    // Binary search in CDF
    int lo = 0, hi = (int)params.N - 1;
    while (lo < hi) {
        int mid = (lo + hi) / 2;
        if (cdf[mid] < target)
            lo = mid + 1;
        else
            hi = mid;
    }

    lo = min(lo, (int)params.N - 1);
    lo = max(lo, 0);

    x_pos_out[idx] = x_pos_in[lo];
    y_pos_out[idx] = y_pos_in[lo];
}
