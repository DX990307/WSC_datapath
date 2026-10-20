// NAS MG (NPB-GPU HIP/MG/mg.cpp kernels via gpu_benchmarks tier2/npb_mg),
// W2 port, fp32. Grids are cubes of width n (interior n-2 plus one periodic
// ghost on each side), x fastest: u[(i3*n + i2)*n + i1].
//
//   mg_resid    WG per (i2, i3) line: r = v - A u   (r may alias v)
//   mg_psinv    WG per (i2, i3) line: u += S r      (smoother)
//   mg_rprj3    WG per coarse (j2, j3) line: restriction r_fine -> s_coarse
//   mg_interp   WG per coarse (i2, i3): u_fine += P z_coarse
//   mg_comm3_1/2/3  periodic ghost exchange along x, y, z (in that order)
//   mg_zero     u = 0
//   mg_norm     WG per (i2, i3) line: partial sum of squares and max |r|
//               (host finishes, as upstream)
// Line partial sums in LDS as upstream (u1/u2, r1/r2, x1/y1, z1/z2/z3).
// The WG's x size is passed as bs (lines longer than bs are looped).

#define MW 514
#define IDX3(i3, i2, i1, n) (((uint)(i3) * (uint)(n) + (uint)(i2)) * (uint)(n) + (uint)(i1))

__kernel void mg_resid(__global const float *u, __global const float *v,
                       __global float *r, const int n, const int bs,
                       const float a0, const float a2, const float a3) {
  __local float u1[MW];
  __local float u2[MW];
  const int i2 = get_group_id(0) + 1;
  const int i3 = get_group_id(1) + 1;
  const int lid = get_local_id(0);
  for (int i1 = lid; i1 < n; i1 += bs) {
    u1[i1] = u[IDX3(i3, i2 - 1, i1, n)] + u[IDX3(i3, i2 + 1, i1, n)] +
             u[IDX3(i3 - 1, i2, i1, n)] + u[IDX3(i3 + 1, i2, i1, n)];
    u2[i1] = u[IDX3(i3 - 1, i2 - 1, i1, n)] + u[IDX3(i3 - 1, i2 + 1, i1, n)] +
             u[IDX3(i3 + 1, i2 - 1, i1, n)] + u[IDX3(i3 + 1, i2 + 1, i1, n)];
  }
  barrier(CLK_LOCAL_MEM_FENCE);
  for (int i1 = lid + 1; i1 < n - 1; i1 += bs) {
    const uint p = IDX3(i3, i2, i1, n);
    r[p] = v[p] - a0 * u[p] - a2 * (u2[i1] + u1[i1 - 1] + u1[i1 + 1]) -
           a3 * (u2[i1 - 1] + u2[i1 + 1]);
  }
}

__kernel void mg_psinv(__global const float *r, __global float *u,
                       const int n, const int bs,
                       const float c0, const float c1, const float c2) {
  __local float r1[MW];
  __local float r2[MW];
  const int i2 = get_group_id(0) + 1;
  const int i3 = get_group_id(1) + 1;
  const int lid = get_local_id(0);
  for (int i1 = lid; i1 < n; i1 += bs) {
    r1[i1] = r[IDX3(i3, i2 - 1, i1, n)] + r[IDX3(i3, i2 + 1, i1, n)] +
             r[IDX3(i3 - 1, i2, i1, n)] + r[IDX3(i3 + 1, i2, i1, n)];
    r2[i1] = r[IDX3(i3 - 1, i2 - 1, i1, n)] + r[IDX3(i3 - 1, i2 + 1, i1, n)] +
             r[IDX3(i3 + 1, i2 - 1, i1, n)] + r[IDX3(i3 + 1, i2 + 1, i1, n)];
  }
  barrier(CLK_LOCAL_MEM_FENCE);
  for (int i1 = lid + 1; i1 < n - 1; i1 += bs) {
    const uint p = IDX3(i3, i2, i1, n);
    u[p] = u[p] + c0 * r[p] + c1 * (r[p - 1] + r[p + 1] + r1[i1]) +
           c2 * (r2[i1] + r1[i1 - 1] + r1[i1 + 1]);
  }
}

// WG of mj-1 threads (j1 = lid + 1), m = fine width, mj = coarse width.
// one = 1 (an argument): clang turns 2*x+1 on scalar (group-id) values
// into s_or_b32, which the emulator does not implement.
__kernel void mg_rprj3(__global const float *r, __global float *s,
                       const int m, const int mj, const int one) {
  __local float x1[MW];
  __local float y1[MW];
  const int j2 = get_group_id(0) + 1;
  const int j3 = get_group_id(1) + 1;
  const int j1 = get_local_id(0) + 1;
  const int i3 = 2 * j3 - one, i2 = 2 * j2 - one;
  int i1 = 2 * j1 - 1;
  x1[i1] = r[IDX3(i3 + 1, i2, i1, m)] + r[IDX3(i3 + 1, i2 + 2, i1, m)] +
           r[IDX3(i3, i2 + 1, i1, m)] + r[IDX3(i3 + 2, i2 + 1, i1, m)];
  y1[i1] = r[IDX3(i3, i2, i1, m)] + r[IDX3(i3 + 2, i2, i1, m)] +
           r[IDX3(i3, i2 + 2, i1, m)] + r[IDX3(i3 + 2, i2 + 2, i1, m)];
  barrier(CLK_LOCAL_MEM_FENCE);
  if (j1 < mj - 1) {
    const float y2 = r[IDX3(i3, i2, i1 + 1, m)] + r[IDX3(i3 + 2, i2, i1 + 1, m)] +
                     r[IDX3(i3, i2 + 2, i1 + 1, m)] + r[IDX3(i3 + 2, i2 + 2, i1 + 1, m)];
    const float x2 = r[IDX3(i3 + 1, i2, i1 + 1, m)] + r[IDX3(i3 + 1, i2 + 2, i1 + 1, m)] +
                     r[IDX3(i3, i2 + 1, i1 + 1, m)] + r[IDX3(i3 + 2, i2 + 1, i1 + 1, m)];
    s[IDX3(j3, j2, j1, mj)] =
        0.5f * r[IDX3(i3 + 1, i2 + 1, i1 + 1, m)] +
        0.25f * (r[IDX3(i3 + 1, i2 + 1, i1, m)] + r[IDX3(i3 + 1, i2 + 1, i1 + 2, m)] + x2) +
        0.125f * (x1[i1] + x1[i1 + 2] + y2) + 0.0625f * (y1[i1] + y1[i1 + 2]);
  }
}

// WG of mm threads (i1 = lid), mm = coarse width, n = fine width.
__kernel void mg_interp(__global const float *z, __global float *u,
                        const int mm, const int n, const int one) {
  __local float z1[MW];
  __local float z2[MW];
  __local float z3[MW];
  const int i2 = get_group_id(0);
  const int i3 = get_group_id(1);
  const int i1 = get_local_id(0);
  const float z000 = z[IDX3(i3, i2, i1, mm)];
  const float a = z[IDX3(i3, i2 + 1, i1, mm)] + z000;
  z1[i1] = a;
  z2[i1] = z[IDX3(i3 + 1, i2, i1, mm)] + z000;
  z3[i1] = z[IDX3(i3 + 1, i2 + 1, i1, mm)] + z[IDX3(i3 + 1, i2, i1, mm)] + a;
  barrier(CLK_LOCAL_MEM_FENCE);
  if (i1 < mm - 1) {
    const int o1 = 2 * i1;
    u[IDX3(2 * i3, 2 * i2, o1, n)] += z000;
    u[IDX3(2 * i3, 2 * i2, o1 + 1, n)] += 0.5f * (z[IDX3(i3, i2, i1 + 1, mm)] + z000);
    u[IDX3(2 * i3, 2 * i2 + one, o1, n)] += 0.5f * z1[i1];
    u[IDX3(2 * i3, 2 * i2 + one, o1 + 1, n)] += 0.25f * (z1[i1] + z1[i1 + 1]);
    u[IDX3(2 * i3 + one, 2 * i2, o1, n)] += 0.5f * z2[i1];
    u[IDX3(2 * i3 + one, 2 * i2, o1 + 1, n)] += 0.25f * (z2[i1] + z2[i1 + 1]);
    u[IDX3(2 * i3 + one, 2 * i2 + one, o1, n)] += 0.25f * z3[i1];
    u[IDX3(2 * i3 + one, 2 * i2 + one, o1 + 1, n)] += 0.125f * (z3[i1] + z3[i1 + 1]);
  }
}

// x ghosts: global (i2-1, i3-1) over (n-2) x (n-2)
__kernel void mg_comm3_1(__global float *u, const int n) {
  const int i2 = get_global_id(0) + 1;
  const int i3 = get_global_id(1) + 1;
  if (i2 >= n - 1) return;
  u[IDX3(i3, i2, 0, n)] = u[IDX3(i3, i2, n - 2, n)];
  u[IDX3(i3, i2, n - 1, n)] = u[IDX3(i3, i2, 1, n)];
}

// y ghosts: global (i1, i3-1) over n x (n-2)
__kernel void mg_comm3_2(__global float *u, const int n) {
  const int i1 = get_global_id(0);
  const int i3 = get_global_id(1) + 1;
  if (i1 >= n) return;
  u[IDX3(i3, 0, i1, n)] = u[IDX3(i3, n - 2, i1, n)];
  u[IDX3(i3, n - 1, i1, n)] = u[IDX3(i3, 1, i1, n)];
}

// z ghosts: global (i1, i2) over n x n
__kernel void mg_comm3_3(__global float *u, const int n) {
  const int i1 = get_global_id(0);
  const int i2 = get_global_id(1);
  if (i1 >= n) return;
  u[IDX3(0, i2, i1, n)] = u[IDX3(n - 2, i2, i1, n)];
  u[IDX3(n - 1, i2, i1, n)] = u[IDX3(1, i2, i1, n)];
}

__kernel void mg_zero(__global float *u, const int count) {
  const int i = get_global_id(0);
  if (i < count) u[i] = 0.0f;
}

// 64 threads per line; partial[line] = sum r^2, partial[lines + line] = max
__kernel __attribute__((reqd_work_group_size(64, 1, 1)))
void mg_norm(__global const float *r, __global float *partial, const int n) {
  // sum and max interleaved: two arrays 64 dwords apart would compile to
  // ds_*2st64_b32, which the simulator does not implement
  __local float red[128];
  const int i2 = get_group_id(0) + 1;
  const int i3 = get_group_id(1) + 1;
  const int lid = get_local_id(0);
  float s = 0.0f, mx = 0.0f;
  for (int i1 = lid + 1; i1 < n - 1; i1 += 64) {
    const float v = r[IDX3(i3, i2, i1, n)];
    s += v * v;
    mx = fmax(mx, fabs(v));
  }
  red[2 * lid] = s;
  red[2 * lid + 1] = mx;
  barrier(CLK_LOCAL_MEM_FENCE);
  for (int i = 32; i > 0; i >>= 1) {
    if (lid < i) {
      red[2 * lid] += red[2 * (lid + i)];
      red[2 * lid + 1] = fmax(red[2 * lid + 1], red[2 * (lid + i) + 1]);
    }
    barrier(CLK_LOCAL_MEM_FENCE);
  }
  if (lid == 0) {
    const int line = (i3 - 1) * (n - 2) + (i2 - 1);
    partial[line] = red[0];
    partial[(n - 2) * (n - 2) + line] = red[1];
  }
}
