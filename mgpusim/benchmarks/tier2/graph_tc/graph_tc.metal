/*
 * graph_tc.metal — Metal compute shader for Triangle Counting.
 *
 * Each thread handles one edge (u, v) with u < v and counts common
 * neighbors via sorted adjacency list intersection.
 *
 * Buffer layout:
 *   buffer(0): row_ptr      — int[N+1], CSR row pointers
 *   buffer(1): col_idx      — int[nnz], CSR column indices (sorted per row)
 *   buffer(2): edge_counts  — long long[num_edges], per-edge triangle counts (output)
 *   buffer(3): edge_src     — int[num_edges], source vertex of each edge
 *   buffer(4): edge_dst     — int[num_edges], destination vertex of each edge
 *   buffer(5): params       — uint { num_edges }
 */

#include <metal_stdlib>
using namespace metal;

kernel void triangle_count_kernel(
    device const int*     row_ptr      [[ buffer(0) ]],
    device const int*     col_idx      [[ buffer(1) ]],
    device int*           edge_counts  [[ buffer(2) ]],
    device const int*     edge_src     [[ buffer(3) ]],
    device const int*     edge_dst     [[ buffer(4) ]],
    constant uint&        num_edges    [[ buffer(5) ]],
    uint                  eid          [[ thread_position_in_grid ]])
{
    if (eid >= num_edges) return;

    int u = edge_src[eid];
    int v = edge_dst[eid];

    int u_start = row_ptr[u];
    int u_end   = row_ptr[u + 1];
    int v_start = row_ptr[v];
    int v_end   = row_ptr[v + 1];

    int count = 0;
    int i = u_start;
    int j = v_start;

    while (i < u_end && j < v_end) {
        int nu = col_idx[i];
        int nv = col_idx[j];
        if (nu == nv) {
            count++;
            i++;
            j++;
        } else if (nu < nv) {
            i++;
        } else {
            j++;
        }
    }

    edge_counts[eid] = (int)count;
}
