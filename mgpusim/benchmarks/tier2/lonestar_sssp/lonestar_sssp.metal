/*
 * lonestar_sssp.metal — Metal compute shaders for Bellman-Ford SSSP.
 *
 * Each thread processes one vertex's outgoing edges and relaxes distances
 * using atomic_min.
 *
 * Buffer layout:
 *   buffer(0): row_offsets — int[N+1], CSR row offsets
 *   buffer(1): col_indices — int[num_edges], CSR column indices
 *   buffer(2): weights     — int[num_edges], edge weights
 *   buffer(3): dist        — atomic_int[N], shortest distances
 *   buffer(4): changed     — atomic_int[1], flag for convergence
 *   buffer(5): params      — uint { N }
 */

#include <metal_stdlib>
using namespace metal;

kernel void bellman_ford_kernel(
    device const int*       row_offsets  [[ buffer(0) ]],
    device const int*       col_indices  [[ buffer(1) ]],
    device const int*       weights      [[ buffer(2) ]],
    device atomic_int*      dist         [[ buffer(3) ]],
    device atomic_int*      changed      [[ buffer(4) ]],
    constant uint&          N            [[ buffer(5) ]],
    uint                    u            [[ thread_position_in_grid ]])
{
    if (u >= N) return;

    int d_u = atomic_load_explicit(&dist[u], memory_order_relaxed);
    if (d_u == 0x7FFFFFFF) return; // INT_MAX — unreachable

    int start = row_offsets[u];
    int end   = row_offsets[u + 1];

    for (int e = start; e < end; ++e) {
        int v = col_indices[e];
        int w = weights[e];
        int new_dist = d_u + w;

        int old_dist = atomic_load_explicit(&dist[v], memory_order_relaxed);
        while (new_dist < old_dist) {
            if (atomic_compare_exchange_weak_explicit(&dist[v], &old_dist, new_dist,
                                                       memory_order_relaxed,
                                                       memory_order_relaxed)) {
                atomic_store_explicit(changed, 1, memory_order_relaxed);
                break;
            }
        }
    }
}
