/*
 * rodinia_bfs.metal — Metal compute shaders for the Rodinia BFS benchmark.
 *
 * Two-pass iterative BFS on a CSR graph:
 *
 *   Pass 1 (bfs_kernel): For each node in the current frontier, write
 *     cost[neighbor] = cost[node] + 1  for all unvisited neighbors.
 *
 *   Pass 2 (bfs_update): Promote nodes whose cost was just set (not yet
 *     visited) into the next frontier, mark them visited, signal updated.
 *
 * Buffer layout (bfs_kernel):
 *   buffer(0): row_offsets  [num_nodes + 1]  (read)
 *   buffer(1): col_indices  [num_edges]       (read)
 *   buffer(2): cost         [num_nodes]       (read/write)
 *   buffer(3): frontier     [num_nodes]       (read)
 *   buffer(4): params       {num_nodes}       (read)
 *
 * Buffer layout (bfs_update):
 *   buffer(0): frontier     [num_nodes]       (write)
 *   buffer(1): cost         [num_nodes]       (read)
 *   buffer(2): visited      [num_nodes]       (read/write)
 *   buffer(3): updated      [1]               (write — set to 1 if any change)
 *   buffer(4): params       {num_nodes}       (read)
 */

#include <metal_stdlib>
using namespace metal;

// ---------------------------------------------------------------------------
// Pass 1: propagate costs to neighbors of frontier nodes
// ---------------------------------------------------------------------------
kernel void bfs_kernel(
    device const int*    row_offsets  [[ buffer(0) ]],
    device const int*    col_indices  [[ buffer(1) ]],
    device       int*    cost         [[ buffer(2) ]],
    device const int*    frontier     [[ buffer(3) ]],
    constant     uint&   num_nodes    [[ buffer(4) ]],
    uint                 tid          [[ thread_position_in_grid ]])
{
    if (tid >= num_nodes) return;
    if (!frontier[tid]) return;

    int row_start = row_offsets[tid];
    int row_end   = row_offsets[tid + 1];
    int new_cost  = cost[tid] + 1;

    for (int e = row_start; e < row_end; ++e) {
        uint nb = (uint)col_indices[e];
        if (nb < num_nodes && cost[nb] < 0) {
            cost[nb] = new_cost;  // concurrent writes of same value are benign
        }
    }
}

// ---------------------------------------------------------------------------
// Pass 2: promote newly-discovered nodes into the next frontier
// ---------------------------------------------------------------------------
kernel void bfs_update(
    device       int*    frontier     [[ buffer(0) ]],
    device const int*    cost         [[ buffer(1) ]],
    device       int*    visited      [[ buffer(2) ]],
    device       int*    updated      [[ buffer(3) ]],
    constant     uint&   num_nodes    [[ buffer(4) ]],
    uint                 tid          [[ thread_position_in_grid ]])
{
    if (tid >= num_nodes) return;

    frontier[tid] = 0;
    if (!visited[tid] && cost[tid] >= 0) {
        visited[tid]  = 1;
        frontier[tid] = 1;
        // Signal that at least one node was added to the frontier.
        // Multiple threads may write 1 simultaneously — that is fine.
        atomic_store_explicit((device atomic_int*)updated, 1,
                              memory_order_relaxed);
    }
}
