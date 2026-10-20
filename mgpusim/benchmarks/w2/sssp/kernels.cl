// Single-source shortest paths (gpu_benchmarks tier2/lonestar_sssp), W2
// port. Upstream is a topology-driven synchronous Bellman-Ford: per round,
// snapshot dist into previous, then every reached u relaxes its out-edges
// with atomicMin(&dist[v], previous[u] + w). This port pulls instead:
//
//   sssp_pull  one thread per vertex v:
//              dist_out[v] = min(dist_in[v],
//                                min over neighbours u with dist_in[u] < INF
//                                    of dist_in[u] + w(u, v))
//
// which computes exactly the same round-by-round distances (Jacobi rounds)
// without atomics. The graph is undirected, so the in-edges of v are its CSR
// row. Each round writes a fresh dist buffer (no buffer is rewritten across
// kernels: with magic copies the timing model never invalidates the L1
// caches between kernels). The host reference gives the number of rounds
// (upstream's loop count, including the final round that changes nothing).
// Simulator rules (T17): no atomics, no private arrays.

#define LOCAL_ID ((int)__builtin_amdgcn_workitem_id_x())
#define INF 0x7fffffff

__kernel __attribute__((reqd_work_group_size(256, 1, 1)))
void sssp_pull(__global const int *row_ptr,
               __global const int *col_idx,
               __global const int *weights,
               __global const int *dist_in,
               __global int *dist_out,
               const int num_nodes) {
  const int v = get_group_id(0) * 256 + LOCAL_ID;
  if (v >= num_nodes) return;
  int d = dist_in[v];
  const int e0 = row_ptr[v];
  const int e1 = row_ptr[v + 1];
  for (int e = e0; e < e1; ++e) {
    const int du = dist_in[col_idx[e]];
    if (du != INF) {
      const int c = du + weights[e];
      d = c < d ? c : d;
    }
  }
  dist_out[v] = d;
}
