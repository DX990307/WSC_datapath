/*
 * npb_is.metal — Metal compute shaders for the NAS IS benchmark.
 *
 * Parallel bucket sort of N random integers in range [0, MAX_KEY).
 * Three kernels:
 *   1. histogram_kernel  — count keys per bucket (atomic)
 *   2. prefix_sum_kernel — exclusive scan on histogram (single threadgroup)
 *   3. scatter_kernel    — scatter keys to sorted positions (atomic)
 *
 * Buffer layout:
 *   buffer(0): keys/sorted — int[N] (input keys or output sorted)
 *   buffer(1): hist/offsets — atomic_uint[NUM_BUCKETS]
 *   buffer(2): params — uint { N }
 *
 * For scatter_kernel:
 *   buffer(0): keys    — int[N], input
 *   buffer(1): offsets — atomic_uint[NUM_BUCKETS], prefix-sum offsets
 *   buffer(2): sorted  — int[N], output
 *   buffer(3): params  — uint { N }
 */

#include <metal_stdlib>
using namespace metal;

#define NUM_BUCKETS 1024
#define MAX_KEY     524288
#define BUCKET_SIZE (MAX_KEY / NUM_BUCKETS)  // 512

// ---------------------------------------------------------------------------
// Histogram kernel: count keys per bucket
// ---------------------------------------------------------------------------

kernel void histogram_kernel(
    device const int*   keys   [[ buffer(0) ]],
    device atomic_uint* hist   [[ buffer(1) ]],
    constant uint&      N      [[ buffer(2) ]],
    uint                idx    [[ thread_position_in_grid ]])
{
    if (idx >= N) return;

    int bucket = keys[idx] / BUCKET_SIZE;
    if (bucket >= NUM_BUCKETS) bucket = NUM_BUCKETS - 1;

    atomic_fetch_add_explicit(&hist[bucket], 1u, memory_order_relaxed);
}

// ---------------------------------------------------------------------------
// Prefix sum kernel: exclusive scan (single threadgroup, Blelloch)
//   hist buffer is read/written in-place.
//   One threadgroup with NUM_BUCKETS threads.
// ---------------------------------------------------------------------------

kernel void prefix_sum_kernel(
    device uint*  hist  [[ buffer(0) ]],
    constant uint& n    [[ buffer(1) ]],
    uint           tid  [[ thread_index_in_threadgroup ]])
{
    threadgroup uint sdata[NUM_BUCKETS];

    if (tid < n)
        sdata[tid] = hist[tid];
    else
        sdata[tid] = 0;

    threadgroup_barrier(mem_flags::mem_threadgroup);

    // Up-sweep (reduce)
    for (uint stride = 1; stride < n; stride <<= 1) {
        uint index = (tid + 1) * (stride << 1) - 1;
        if (index < n) {
            sdata[index] += sdata[index - stride];
        }
        threadgroup_barrier(mem_flags::mem_threadgroup);
    }

    // Set last to 0 for exclusive scan
    if (tid == 0) sdata[n - 1] = 0;
    threadgroup_barrier(mem_flags::mem_threadgroup);

    // Down-sweep
    for (uint stride = n >> 1; stride >= 1; stride >>= 1) {
        uint index = (tid + 1) * (stride << 1) - 1;
        if (index < n) {
            uint temp = sdata[index - stride];
            sdata[index - stride] = sdata[index];
            sdata[index] += temp;
        }
        threadgroup_barrier(mem_flags::mem_threadgroup);
    }

    if (tid < n)
        hist[tid] = sdata[tid];
}

// ---------------------------------------------------------------------------
// Scatter kernel: place keys into sorted positions
// ---------------------------------------------------------------------------

kernel void scatter_kernel(
    device const int*   keys    [[ buffer(0) ]],
    device atomic_uint* offsets [[ buffer(1) ]],
    device int*         sorted  [[ buffer(2) ]],
    constant uint&      N       [[ buffer(3) ]],
    uint                idx     [[ thread_position_in_grid ]])
{
    if (idx >= N) return;

    int bucket = keys[idx] / BUCKET_SIZE;
    if (bucket >= NUM_BUCKETS) bucket = NUM_BUCKETS - 1;

    uint pos = atomic_fetch_add_explicit(&offsets[bucket], 1u, memory_order_relaxed);
    sorted[pos] = keys[idx];
}
