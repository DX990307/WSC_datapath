/*
 * graph_pr.metal — Metal compute shaders for pull-based PageRank.
 *
 * Each thread computes the new rank for one vertex by pulling from
 * incoming neighbors via the CSC format.
 *
 * Buffer layout:
 *   buffer(0): col_offsets — int[N+1], CSC column offsets
 *   buffer(1): row_indices — int[num_edges], CSC row indices (source vertices)
 *   buffer(2): out_degree  — int[N], out-degree per vertex
 *   buffer(3): rank_in     — float[N], current rank values
 *   buffer(4): rank_out    — float[N], new rank values (output)
 *   buffer(5): params      — { uint N, float damping, float base_rank }
 */

#include <metal_stdlib>
using namespace metal;

struct PRParams {
    uint  N;
    float damping;
    float base_rank;
};

kernel void pagerank_kernel(
    device const int*    col_offsets  [[ buffer(0) ]],
    device const int*    row_indices  [[ buffer(1) ]],
    device const int*    out_degree   [[ buffer(2) ]],
    device const float*  rank_in      [[ buffer(3) ]],
    device float*        rank_out     [[ buffer(4) ]],
    constant PRParams&   params       [[ buffer(5) ]],
    uint                 v            [[ thread_position_in_grid ]])
{
    if (v >= params.N) return;

    int start = col_offsets[v];
    int end   = col_offsets[v + 1];

    float sum = 0.0f;
    for (int e = start; e < end; ++e) {
        int u = row_indices[e];
        int deg = out_degree[u];
        if (deg > 0)
            sum += rank_in[u] / float(deg);
    }

    rank_out[v] = params.base_rank + params.damping * sum;
}
