// Breadth-first search (gpu_benchmarks tier2/rodinia_bfs), W2 port.
// Level-synchronous, one thread per vertex, two kernels per level as in
// Rodinia:
//
//   bfs_expand  for every frontier vertex u: for each neighbour nb with
//               !visited[nb], next[nb] = 1  (upstream: atomicExch(&cost[nb],
//               cost[u]+1); every writer stores the same value, so a plain
//               store is equivalent)
//   bfs_update  per vertex: newly = !visited[v] && next[v];
//               frontier_out[v] = newly; visited_out[v] = visited[v] | newly;
//               if newly, cost[v] = level + 1
//               (upstream also does atomicExch(updated, 1); the host knows
//               the level count, see below)
//
// Deviations from upstream:
//   - frontier / visited / next are fresh buffers for every level (each is
//     written by one kernel and then only read): with magic copies the
//     timing model never invalidates the L1 caches between kernels, so a
//     buffer rewritten across kernels could be read stale from an L1V.
//   - the number of levels comes from the host reference BFS (the same
//     count as upstream's "while (updated)" loop, including the final level
//     that discovers nothing), so no device-to-host flag copy per level.
//   - the cost of a frontier vertex is the level (kernel argument) instead
//     of a cost[] read.
// Simulator rules (T17): no atomics, no private arrays.

#define LOCAL_ID ((int)__builtin_amdgcn_workitem_id_x())

__kernel __attribute__((reqd_work_group_size(256, 1, 1)))
void bfs_expand(__global const int *row_ptr,
                __global const int *col_idx,
                __global const int *frontier,
                __global const int *visited,
                __global int *next,
                const int num_nodes) {
  const int tid = get_group_id(0) * 256 + LOCAL_ID;
  if (tid >= num_nodes || !frontier[tid]) return;
  const int e0 = row_ptr[tid];
  const int e1 = row_ptr[tid + 1];
  for (int e = e0; e < e1; ++e) {
    const int nb = col_idx[e];
    if (!visited[nb]) next[nb] = 1;
  }
}

__kernel __attribute__((reqd_work_group_size(256, 1, 1)))
void bfs_update(__global const int *next,
                __global const int *visited,
                __global int *visited_out,
                __global int *frontier_out,
                __global int *cost,
                const int new_cost,
                const int num_nodes) {
  const int tid = get_group_id(0) * 256 + LOCAL_ID;
  if (tid >= num_nodes) return;
  const int was = visited[tid];
  const int newly = (!was && next[tid]) ? 1 : 0;
  frontier_out[tid] = newly;
  visited_out[tid] = was | newly;
  if (newly) cost[tid] = new_cost;
}
