// Single-source shortest paths (gpu_benchmarks tier2/lonestar_sssp), W2
// port. Upstream is a topology-driven synchronous Bellman-Ford: per round,
// snapshot dist into previous, then every reached u relaxes its out-edges
// with atomicMin(&dist[v], previous[u] + w). This port pulls instead:
//
//   sssp_pull  one 64-lane wave per vertex v (4 per WG), lanes stride v's
//              edges, min-reduced in LDS:
//              dist_out[v] = min(dist_in[v],
//                                min over neighbours u with dist_in[u] < INF
//                                    of dist_in[u] + w(u, v))
//
// which computes exactly the same round-by-round distances (Jacobi rounds)
// without atomics. The graph is undirected, so the in-edges of v are its CSR
// row. A wave (not a thread) per vertex keeps hub vertices of the skewed
// R-MAT input from becoming one long serial loop. Each round writes a fresh
// dist buffer (no buffer is rewritten across kernels: with magic copies the
// timing model never invalidates the L1 caches between kernels). The host
// reference gives the number of rounds (upstream's loop count, including
// the final round that changes nothing).
// Simulator rules (T17): no atomics, no private arrays, implemented DS ops.

#define LOCAL_ID ((int)__builtin_amdgcn_workitem_id_x())
#define INF 0x7fffffff

__kernel __attribute__((reqd_work_group_size(256, 1, 1)))
void sssp_pull(__global const int *row_ptr,
               __global const int *col_idx,
               __global const int *weights,
               __global const int *dist_in,
               __global int *dist_out,
               const int num_nodes) {
  // 65 per wave: 64-dword strides would pair LDS ops as ds_*st64
  __local int red[4 * 65];
  const int lid = LOCAL_ID;
  const int w = lid >> 6;
  const int lane = lid & 63;
  const int v = get_group_id(0) * 4 + w;
  int d = INF;
  if (v < num_nodes) {
    if (lane == 0) d = dist_in[v];
    const int e1 = row_ptr[v + 1];
    for (int e = row_ptr[v] + lane; e < e1; e += 64) {
      const int du = dist_in[col_idx[e]];
      if (du != INF) {
        const int c = du + weights[e];
        d = c < d ? c : d;
      }
    }
  }
  red[w * 65 + lane] = d;
  barrier(CLK_LOCAL_MEM_FENCE);
  for (int s = 32; s > 0; s >>= 1) {
    if (lane < s) {
      const int o = red[w * 65 + lane + s];
      if (o < red[w * 65 + lane]) red[w * 65 + lane] = o;
    }
    barrier(CLK_LOCAL_MEM_FENCE);
  }
  if (lane == 0 && v < num_nodes) dist_out[v] = red[w * 65];
}
