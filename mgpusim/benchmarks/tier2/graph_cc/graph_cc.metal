/*
 * graph_cc.metal — Metal compute shader for Connected Components benchmark.
 *
 * Label propagation: each thread checks all neighbors of one vertex and
 * updates the label to the minimum among itself and its neighbors.
 *
 * Buffer layout:
 *   buffer(0): row_offsets  — int[N+1], CSR row offsets
 *   buffer(1): col_indices  — int[num_edges], CSR column indices (neighbors)
 *   buffer(2): labels       — int[N], current component labels (read/write)
 *   buffer(3): changed      — atomic int[1], set to 1 if any label changed
 *   buffer(4): params       — { uint N }
 */

#include <metal_stdlib>
using namespace metal;

struct CCParams {
    uint N;
};

kernel void cc_propagate_kernel(
    device const int*      row_offsets  [[ buffer(0) ]],
    device const int*      col_indices  [[ buffer(1) ]],
    device int*            labels       [[ buffer(2) ]],
    device atomic_int*     changed      [[ buffer(3) ]],
    constant CCParams&     params       [[ buffer(4) ]],
    uint                   v            [[ thread_position_in_grid ]])
{
    if (v >= params.N) return;

    int my_label = labels[v];
    int new_label = my_label;

    int start = row_offsets[v];
    int end   = row_offsets[v + 1];

    for (int e = start; e < end; ++e) {
        int u = col_indices[e];
        int neighbor_label = labels[u];
        if (neighbor_label < new_label) {
            new_label = neighbor_label;
        }
    }

    if (new_label < my_label) {
        labels[v] = new_label;
        atomic_store_explicit(changed, 1, memory_order_relaxed);
    }
}
