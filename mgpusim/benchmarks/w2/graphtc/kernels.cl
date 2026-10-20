// Triangle counting (gpu_benchmarks tier2/graph_tc), W2 port.
//
//   tc_count  one thread per undirected edge (u, v), u < v: counts the
//             common neighbours of u and v by merging their sorted
//             adjacency lists (adj(v) is a data-dependent, mostly remote
//             read). Each triangle is counted once per edge, three times
//             in total; the host sums the per-edge counts and divides by 3.
//
// Deviations from upstream: per-edge counts are 32-bit (upstream long long;
// a single edge's count is bounded by the degree); otherwise unchanged.
// Simulator rules (T17): no atomics, no private arrays, fp32/int32 only.

#define LOCAL_ID ((int)__builtin_amdgcn_workitem_id_x())

__kernel __attribute__((reqd_work_group_size(256, 1, 1)))
void tc_count(__global const int *row_ptr,
              __global const int *col_idx,
              __global const int *edge_src,
              __global const int *edge_dst,
              __global uint *edge_counts,
              const int num_edges) {
  const int eid = get_group_id(0) * 256 + LOCAL_ID;
  if (eid >= num_edges) return;
  const int u = edge_src[eid];
  const int v = edge_dst[eid];
  int i = row_ptr[u];
  const int ie = row_ptr[u + 1];
  int j = row_ptr[v];
  const int je = row_ptr[v + 1];
  uint count = 0;
  while (i < ie && j < je) {
    const int nu = col_idx[i];
    const int nv = col_idx[j];
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
  edge_counts[eid] = count;
}
