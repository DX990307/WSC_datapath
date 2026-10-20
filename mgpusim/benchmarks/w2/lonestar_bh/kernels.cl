// Barnes-Hut force computation (gpu_benchmarks tier2/lonestar_bh, Lonestar
// GPU "bh" in spirit), W2 port.
//
//   bh_force      one thread per body: walk the quadtree (built on the host)
//                 and sum the accelerations. Stackless: the tree is stored in
//                 depth-first preorder, so "open a node" is k + 1 and "skip
//                 its subtree" is next[k] (host-precomputed); the upstream
//                 private stack[64] would spill to scratch, which the
//                 simulator does not implement.
//   bh_integrate  Euler update of velocities and positions.
//
// Node record: (cx, cy, mass, w2) with w2 = bw^2 for an internal node and -1
// for a leaf (a leaf is always accepted, as upstream). Opening criterion as
// upstream: accept when bw^2 / dist2 < theta^2, written as w2 < theta2 * dist2.

__kernel void bh_force(__global const float *px, __global const float *py,
                       __global float *ax, __global float *ay,
                       __global const float4 *node,
                       __global const int *next,
                       const int num_nodes, const int n,
                       const float theta2, const float eps2) {
  const int i = get_global_id(0);
  if (i >= n) return;
  const float xi = px[i];
  const float yi = py[i];
  float fax = 0.0f, fay = 0.0f;
  int k = 0;
  while (k < num_nodes) {
    const float4 nd = node[k];
    const float dx = nd.x - xi;
    const float dy = nd.y - yi;
    const float d2 = dx * dx + dy * dy + eps2;
    if (nd.w < theta2 * d2) {
      if (nd.z > 0.0f && d2 > eps2 * 2.0f) {
        const float inv = rsqrt(d2);
        const float inv3 = inv * inv * inv;
        fax += nd.z * dx * inv3;
        fay += nd.z * dy * inv3;
      }
      k = next[k];
    } else {
      k = k + 1;
    }
  }
  ax[i] = fax;
  ay[i] = fay;
}

__kernel void bh_integrate(__global float *px, __global float *py,
                           __global float *vx, __global float *vy,
                           __global const float *ax, __global const float *ay,
                           const int n, const float dt) {
  const int i = get_global_id(0);
  if (i >= n) return;
  const float nvx = vx[i] + ax[i] * dt;
  const float nvy = vy[i] + ay[i] * dt;
  vx[i] = nvx;
  vy[i] = nvy;
  px[i] = px[i] + nvx * dt;
  py[i] = py[i] + nvy * dt;
}
