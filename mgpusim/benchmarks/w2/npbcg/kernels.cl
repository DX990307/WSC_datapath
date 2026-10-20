// NAS CG inverse-power benchmark (gpu_benchmarks tier2/npb_cg, after
// GMAP/NPB-GPU CUDA/CG), W2 port in fp32.
//
//   cg_init       z = 0, r = x, p = x
//   cg_spmv       y = A x, one 64-lane wave per CSR row (4 rows per WG; the
//                 pinned CUDA code also maps a block to a row), lanes stride
//                 the row, LDS tree reduction. Reads of x[col] are data
//                 dependent and mostly remote.
//   cg_dot        per-WG partial of a.b (or |a-b|^2), 64-thread WGs
//   cg_reduce     one WG: sums the partials into a scalar
//   cg_axpy       alpha = rho / pq (scalars in memory);
//                 z_out = z + alpha p, r_out = r - alpha q
//   cg_direction  beta = rho_new / rho_old; p_out = r + beta p
//   cg_normalize  x_out = z / sqrt(zz)
//
// Deviations from upstream: fp32 instead of fp64; the dot-product partials
// are reduced on the GPU (cg_reduce) and alpha/beta are computed by the
// consuming kernels instead of on the host, so no host round trip between
// kernels; every vector/scalar is a fresh buffer per iteration (with magic
// copies the timing model never invalidates the L1 caches between kernels,
// so a buffer rewritten across kernels could be read stale); SpMV maps a
// wave (not a thread) to a row so the kernel has n/4 WGs.
// Simulator rules (T17): no atomics, no private arrays, implemented DS ops
// only; native_divide / native_rsqrt (v_rcp/v_rsq).

#define LOCAL_ID ((int)__builtin_amdgcn_workitem_id_x())

__kernel __attribute__((reqd_work_group_size(64, 1, 1)))
void cg_init(__global const float *x, __global float *z, __global float *p,
             __global float *r, const int n) {
  const int i = get_group_id(0) * 64 + LOCAL_ID;
  if (i >= n) return;
  const float xi = x[i];
  z[i] = 0.0f;
  r[i] = xi;
  p[i] = xi;
}

__kernel __attribute__((reqd_work_group_size(256, 1, 1)))
void cg_spmv(__global const int *offsets, __global const int *columns,
             __global const float *values, __global const float *x,
             __global float *y, const int n) {
  // 65 floats per wave: wave strides of 64 dwords would let the compiler
  // pair LDS ops as ds_*st64 (not implemented)
  __local float red[4 * 65];
  const int lid = LOCAL_ID;
  const int w = lid >> 6;
  const int lane = lid & 63;
  const int row = get_group_id(0) * 4 + w;
  float sum = 0.0f;
  if (row < n) {
    const int k1 = offsets[row + 1];
    for (int k = offsets[row] + lane; k < k1; k += 64)
      sum += values[k] * x[columns[k]];
  }
  red[w * 65 + lane] = sum;
  barrier(CLK_LOCAL_MEM_FENCE);
  for (int s = 32; s > 0; s >>= 1) {
    if (lane < s) red[w * 65 + lane] += red[w * 65 + lane + s];
    barrier(CLK_LOCAL_MEM_FENCE);
  }
  if (lane == 0 && row < n) y[row] = red[w * 65];
}

__kernel __attribute__((reqd_work_group_size(64, 1, 1)))
void cg_dot(__global const float *a, __global const float *b,
            __global float *partial, const int n, const int difference) {
  __local float red[64];
  const int lid = LOCAL_ID;
  const int i = get_group_id(0) * 64 + lid;
  float v = 0.0f;
  if (i < n) {
    if (difference) {
      const float d = a[i] - b[i];
      v = d * d;
    } else {
      v = a[i] * b[i];
    }
  }
  red[lid] = v;
  barrier(CLK_LOCAL_MEM_FENCE);
  for (int s = 32; s > 0; s >>= 1) {
    if (lid < s) red[lid] += red[lid + s];
    barrier(CLK_LOCAL_MEM_FENCE);
  }
  if (lid == 0) partial[get_group_id(0)] = red[0];
}

// 64 threads: a 256-wide tree would pair LDS reads 128 dwords apart as
// ds_read2st64_b32 (not implemented)
__kernel __attribute__((reqd_work_group_size(64, 1, 1)))
void cg_reduce(__global const float *partial, __global float *out,
               const int nparts) {
  __local float red[64];
  const int lid = LOCAL_ID;
  float v = 0.0f;
  for (int i = lid; i < nparts; i += 64) v += partial[i];
  red[lid] = v;
  barrier(CLK_LOCAL_MEM_FENCE);
  for (int s = 32; s > 0; s >>= 1) {
    if (lid < s) red[lid] += red[lid + s];
    barrier(CLK_LOCAL_MEM_FENCE);
  }
  if (lid == 0) out[0] = red[0];
}

__kernel __attribute__((reqd_work_group_size(64, 1, 1)))
void cg_axpy(__global const float *rho, __global const float *pq,
             __global const float *p, __global const float *q,
             __global const float *z, __global const float *r,
             __global float *z_out, __global float *r_out, const int n) {
  const int i = get_group_id(0) * 64 + LOCAL_ID;
  if (i >= n) return;
  const float alpha = native_divide(rho[0], pq[0]);
  z_out[i] = z[i] + alpha * p[i];
  r_out[i] = r[i] - alpha * q[i];
}

__kernel __attribute__((reqd_work_group_size(64, 1, 1)))
void cg_direction(__global const float *rho_new, __global const float *rho_old,
                  __global const float *r, __global const float *p,
                  __global float *p_out, const int n) {
  const int i = get_group_id(0) * 64 + LOCAL_ID;
  if (i >= n) return;
  const float beta = native_divide(rho_new[0], rho_old[0]);
  p_out[i] = r[i] + beta * p[i];
}

__kernel __attribute__((reqd_work_group_size(64, 1, 1)))
void cg_normalize(__global const float *zz, __global const float *z,
                  __global float *x_out, const int n) {
  const int i = get_group_id(0) * 64 + LOCAL_ID;
  if (i >= n) return;
  x_out[i] = native_rsqrt(zz[0]) * z[i];
}
