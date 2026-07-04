/*
 * rodinia_kmeans.metal — Metal compute shaders for K-Means clustering benchmark.
 *
 * Two kernels:
 *   kmeans_assign — assign each point to nearest cluster center
 *   kmeans_update — accumulate new cluster centers using device atomics
 *
 * The host (rodinia_kmeans_metal.mm) runs the iteration loop:
 *   1. kmeans_assign  → fills membership[]
 *   2. Zero new_centers[] and counts[]
 *   3. kmeans_update  → accumulates sums and counts
 *   4. CPU divides to get new centers
 *   5. Repeat until convergence or max_iter
 */

#include <metal_stdlib>
#include <metal_atomic>
using namespace metal;

// ---------------------------------------------------------------------------
// kmeans_assign — one thread per point
//
//   points[idx * D + d]   — input points (N x D, row-major)
//   centers[k  * D + d]   — current cluster centers (K x D, row-major)
//   membership[idx]        — output: cluster index for point idx
//   params: [N, D, K]
// ---------------------------------------------------------------------------
kernel void kmeans_assign(
    device const float* points     [[ buffer(0) ]],
    device const float* centers    [[ buffer(1) ]],
    device       int*   membership [[ buffer(2) ]],
    constant     uint&  N          [[ buffer(3) ]],
    constant     uint&  D          [[ buffer(4) ]],
    constant     uint&  K          [[ buffer(5) ]],
    uint idx [[ thread_position_in_grid ]])
{
    if (idx >= N) return;

    float min_dist = MAXFLOAT;
    int   best_k   = 0;

    for (uint k = 0; k < K; ++k) {
        float dist = 0.0f;
        for (uint d = 0; d < D; ++d) {
            float diff = points[idx * D + d] - centers[k * D + d];
            dist += diff * diff;
        }
        if (dist < min_dist) {
            min_dist = dist;
            best_k   = (int)k;
        }
    }
    membership[idx] = best_k;
}

// ---------------------------------------------------------------------------
// kmeans_update — one thread per point, accumulates with device atomics
//
//   new_centers[k * D + d] — running sum (zeroed before each call by host)
//   counts[k]              — number of points assigned to cluster k
// ---------------------------------------------------------------------------
kernel void kmeans_update(
    device const float*   points      [[ buffer(0) ]],
    device atomic_int*    counts      [[ buffer(1) ]],
    device atomic_float*  new_centers [[ buffer(2) ]],
    device const int*     membership  [[ buffer(3) ]],
    constant     uint&    N           [[ buffer(4) ]],
    constant     uint&    D           [[ buffer(5) ]],
    uint idx [[ thread_position_in_grid ]])
{
    if (idx >= N) return;

    int k = membership[idx];
    atomic_fetch_add_explicit(&counts[k], 1, memory_order_relaxed);
    for (uint d = 0; d < D; ++d) {
        atomic_fetch_add_explicit(&new_centers[(uint)k * D + d],
                                  points[idx * D + d],
                                  memory_order_relaxed);
    }
}
