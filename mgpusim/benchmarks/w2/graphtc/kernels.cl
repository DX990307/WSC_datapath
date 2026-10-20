// Triangle counting (gpu_benchmarks tier2/graph_tc), W2 port.
//
//   tc_count  one 64-lane wave per undirected edge (u, v), u < v, 4 edges
//             per WG: counts the common neighbours of u and v. The lanes
//             stride the shorter adjacency list and binary-search each
//             element in the longer one (reads of adj(v) are data
//             dependent and mostly remote); the lane counts are summed in
//             LDS. Each triangle is counted once per edge, three times in
//             total; the host sums the per-edge counts and divides by 3.
//
// Deviations from upstream: upstream maps a thread to an edge and merges
// the two sorted lists; on the skewed R-MAT input a single hub-hub merge is
// thousands of dependent loads in one thread (the kernel becomes one long
// latency-bound tail), so this port spreads an edge over a wave and
// intersects by binary search (same count). Per-edge counts are 32-bit
// (upstream long long).
// Simulator rules (T17): no atomics, no private arrays, implemented DS ops.

#define LOCAL_ID ((int)__builtin_amdgcn_workitem_id_x())

__kernel __attribute__((reqd_work_group_size(256, 1, 1)))
void tc_count(__global const int *row_ptr,
              __global const int *col_idx,
              __global const int *edge_src,
              __global const int *edge_dst,
              __global uint *edge_counts,
              const int num_edges) {
  // 65 per wave: 64-dword strides would pair LDS ops as ds_*st64
  __local uint red[4 * 65];
  const int lid = LOCAL_ID;
  const int w = lid >> 6;
  const int lane = lid & 63;
  const int eid = get_group_id(0) * 4 + w;
  uint count = 0;
  if (eid < num_edges) {
    const int u = edge_src[eid];
    const int v = edge_dst[eid];
    int a0 = row_ptr[u], a1 = row_ptr[u + 1];
    int b0 = row_ptr[v], b1 = row_ptr[v + 1];
    if (a1 - a0 > b1 - b0) {
      int t = a0; a0 = b0; b0 = t;
      t = a1; a1 = b1; b1 = t;
    }
    for (int i = a0 + lane; i < a1; i += 64) {
      const int key = col_idx[i];
      int lo = b0, hi = b1;
      while (lo < hi) {
        const int mid = (lo + hi) >> 1;
        const int x = col_idx[mid];
        if (x == key) {
          count++;
          break;
        }
        if (x < key) lo = mid + 1;
        else hi = mid;
      }
    }
  }
  red[w * 65 + lane] = count;
  barrier(CLK_LOCAL_MEM_FENCE);
  for (int s = 32; s > 0; s >>= 1) {
    if (lane < s) red[w * 65 + lane] += red[w * 65 + lane + s];
    barrier(CLK_LOCAL_MEM_FENCE);
  }
  if (lane == 0 && eid < num_edges) edge_counts[eid] = red[w * 65];
}
