// MoE layer (DeepSeekMoE-style routed experts, decode batch), T17 W-B.
//
//   moe_gate     one WG per token: logits = x . Wg, softmax over experts,
//                top-k by (prob + bias) in LDS; writes expert ids and weights
//                (the softmax probabilities, not renormalised).
//   (host)       groups the (token, k) pairs by expert: perm/offsets/tiles.
//   moe_dispatch one WG per permuted slot: xs[slot] = x[perm_token[slot]].
//   moe_up       one WG per (expert m-tile, F tile): act = silu(xs W1) * (xs W3)
//   moe_down     one WG per (expert m-tile, H tile): ys = act W2
//   moe_combine  one WG per token: out[t] = sum_k w[t][k] * ys[inv[t][k]]
//
// Simulator rules (T17): no atomics, no private arrays, only the implemented
// DS opcodes, fp32. build_hsaco.sh checks the generated code.
//
// Layouts: x [T][H], wg [H][E] (x . wg), bias [E], w1/w3 [E][H][F],
// w2 [E][F][H], xs/ys [S][H], act [S][F] with S = T * TOPK slots.
// tiles [n_tiles][3] = (expert, first slot, rows) built on the host; rows
// <= TM.

#define NE 64        // experts handled by moe_gate (compile-time)
#define TM 32        // slots per GEMM tile
#define TN 64        // output columns per GEMM tile
#define TK 32        // reduction chunk staged in LDS
#define NEG_INF (-3.0e38f)
#define LOG2E 1.4426950408889634f
// Element offsets are 32-bit unsigned (every buffer has < 2^32 elements; the
// host checks it): signed (long)int*int compiles to v_mad_i64_i32 (VOP3a
// opcode 489) and 64-bit shifts of thread ids to v_alignbit_b32 (VOP3a 462),
// neither of which the emulator implements.
#define IDX(a, b) ((uint)(a) * (uint)(b))
// 32-bit local id: shifts of the 64-bit get_local_id(0) become
// v_alignbit_b32 (VOP3a opcode 462), which the emulator does not implement.
#define LOCAL_ID ((int)__builtin_amdgcn_workitem_id_x())
// Store 8 floats as four 8-byte stores. Two 16-byte stores make clang
// insert "s_nop 0" (fiji store-data hazard), which hangs the timing CU
// (check_hsaco.sh); the empty asm keeps the stores from being merged.
#define STORE8(o, a, b, c, d, e, f, g, h)            \
  do {                                               \
    vstore2((float2)(a, b), 0, (o));                 \
    __asm__ volatile("" ::: "memory");               \
    vstore2((float2)(c, d), 0, (o) + 2);             \
    __asm__ volatile("" ::: "memory");               \
    vstore2((float2)(e, f), 0, (o) + 4);             \
    __asm__ volatile("" ::: "memory");               \
    vstore2((float2)(g, h), 0, (o) + 6);             \
  } while (0)

__kernel __attribute__((reqd_work_group_size(256, 1, 1)))
void moe_gate(__global const float *x,
              __global const float *wg,
              __global const float *bias,
              __global int *topk_idx,
              __global float *topk_w,
              const int hidden,
              const int topk) {
  // Sizes NE + 1: arrays 64 dwords apart make the compiler pair stores as
  // ds_write2st64_b32, which the simulator does not implement.
  __local float part[4 * NE];
  __local float prob[NE + 1];
  __local float sel[NE + 1];
  __local float rv[NE + 1];
  __local int ri[NE + 1];

  const int lid = LOCAL_ID;
  const int t = get_group_id(0);
  const int e = lid & (NE - 1);
  const int s = lid >> 6; // 4 slices of the hidden dimension
  const int chunk = hidden / 4;

  // logits: wave s sums its slice; lanes are experts (coalesced rows of wg)
  const __global float *xp = x + IDX(t, hidden) + s * chunk;
  const __global float *wp = wg + IDX(s * chunk, NE) + e;
  float acc = 0.0f;
  for (uint h = 0; h < (uint)chunk; ++h) {
    acc += xp[h] * wp[h * NE];
  }
  part[e * 4 + s] = acc;
  barrier(CLK_LOCAL_MEM_FENCE);

  // softmax over the experts (wave 0)
  if (lid < NE) {
    const float l = part[e * 4] + part[e * 4 + 1] + part[e * 4 + 2] + part[e * 4 + 3];
    prob[e] = l;
    rv[e] = l;
  }
  barrier(CLK_LOCAL_MEM_FENCE);
  for (int st = NE / 2; st > 0; st >>= 1) {
    if (lid < st) {
      rv[lid] = fmax(rv[lid], rv[lid + st]);
    }
    barrier(CLK_LOCAL_MEM_FENCE);
  }
  const float mx = rv[0];
  barrier(CLK_LOCAL_MEM_FENCE);
  if (lid < NE) {
    const float p = native_exp2((prob[e] - mx) * LOG2E);
    prob[e] = p;
    rv[e] = p;
  }
  barrier(CLK_LOCAL_MEM_FENCE);
  for (int st = NE / 2; st > 0; st >>= 1) {
    if (lid < st) {
      rv[lid] += rv[lid + st];
    }
    barrier(CLK_LOCAL_MEM_FENCE);
  }
  const float inv = native_recip(rv[0]);
  barrier(CLK_LOCAL_MEM_FENCE);
  if (lid < NE) {
    prob[e] *= inv;
    sel[e] = prob[e] + bias[e];
  }
  barrier(CLK_LOCAL_MEM_FENCE);

  // top-k by repeated arg-max in LDS (ties: lower expert id)
  for (int k = 0; k < topk; ++k) {
    if (lid < NE) {
      rv[e] = sel[e];
      ri[e] = e;
    }
    barrier(CLK_LOCAL_MEM_FENCE);
    for (int st = NE / 2; st > 0; st >>= 1) {
      if (lid < st) {
        const float a = rv[lid], b = rv[lid + st];
        if (b > a || (b == a && ri[lid + st] < ri[lid])) {
          rv[lid] = b;
          ri[lid] = ri[lid + st];
        }
      }
      barrier(CLK_LOCAL_MEM_FENCE);
    }
    if (lid == 0) {
      const int best = ri[0];
      topk_idx[t * topk + k] = best;
      topk_w[t * topk + k] = prob[best];
      sel[best] = NEG_INF;
    }
    barrier(CLK_LOCAL_MEM_FENCE);
  }
}

__kernel __attribute__((reqd_work_group_size(256, 1, 1)))
void moe_dispatch(__global const float *x,
                  __global const int *perm_token,
                  __global float *xs,
                  const int hidden) {
  const int slot = get_group_id(0);
  const int lid = LOCAL_ID;
  const uint src = IDX(perm_token[slot], hidden);
  const uint dst = IDX(slot, hidden);
  for (int h = lid * 4; h < hidden; h += 256 * 4) {
    vstore4(vload4(0, x + src + h), 0, xs + dst + h);
  }
}

// Shared tiled GEMM body: C[TM][TN] = A[rows][K] * B[K][N] restricted to
// rows of one expert. Thread = (row r = lid / 8, 8 columns c0 = (lid % 8) * 8).
// a_lds [TM][TK], b_lds [TK][TN].

inline void load_a_tile(__local float *a_lds, __global const float *a,
                        int slot0, int rows, int lda, int k0, int lid) {
  // TM * TK = 1024 floats, 4 per thread
  const int r = lid >> 3;
  const int kk = (lid & 7) * 4;
  float4 v = (float4)(0.0f);
  if (r < rows) {
    v = vload4(0, a + IDX(slot0 + r, lda) + k0 + kk);
  }
  a_lds[r * TK + kk] = v.x;
  a_lds[r * TK + kk + 1] = v.y;
  a_lds[r * TK + kk + 2] = v.z;
  a_lds[r * TK + kk + 3] = v.w;
}

inline void load_b_tile(__local float *b_lds, __global const float *b,
                        int ldb, int k0, int n0, int lid) {
  // TK * TN = 2048 floats, 8 contiguous per thread (8 threads per 256-B row);
  // contiguous LDS stores avoid the unimplemented ds_write2st64_b32.
  const int k = lid >> 3;           // 0..31
  const int c = (lid & 7) * 8;      // 0..56
  const __global float *src = b + IDX(k0 + k, ldb) + n0 + c;
  const float4 v0 = vload4(0, src);
  const float4 v1 = vload4(0, src + 4);
  __local float *dst = b_lds + k * TN + c;
  dst[0] = v0.x;
  dst[1] = v0.y;
  dst[2] = v0.z;
  dst[3] = v0.w;
  dst[4] = v1.x;
  dst[5] = v1.y;
  dst[6] = v1.z;
  dst[7] = v1.w;
}

__kernel __attribute__((reqd_work_group_size(256, 1, 1)))
void moe_up(__global const float *xs,
            __global const float *w1,
            __global const float *w3,
            __global const int *tiles,
            __global float *act,
            const int hidden,
            const int ffn,
            const int n_tiles_n) {
  __local float a_lds[TM * TK];
  __local float b1_lds[TK * TN];
  __local float b3_lds[TK * TN];

  const int lid = LOCAL_ID;
  const int wgid = get_group_id(0);
  const int mt = wgid / n_tiles_n;
  const int n0 = (wgid % n_tiles_n) * TN;
  const int e = tiles[3 * mt];
  const int slot0 = tiles[3 * mt + 1];
  const int rows = tiles[3 * mt + 2];
  const __global float *b1 = w1 + IDX(e, hidden * ffn);
  const __global float *b3 = w3 + IDX(e, hidden * ffn);

  const int r = lid >> 3;
  const int c0 = (lid & 7) * 8;
  float g0 = 0.0f, g1 = 0.0f, g2 = 0.0f, g3 = 0.0f;
  float g4 = 0.0f, g5 = 0.0f, g6 = 0.0f, g7 = 0.0f;
  float u0 = 0.0f, u1 = 0.0f, u2 = 0.0f, u3 = 0.0f;
  float u4 = 0.0f, u5 = 0.0f, u6 = 0.0f, u7 = 0.0f;

  for (int k0 = 0; k0 < hidden; k0 += TK) {
    load_a_tile(a_lds, xs, slot0, rows, hidden, k0, lid);
    load_b_tile(b1_lds, b1, ffn, k0, n0, lid);
    load_b_tile(b3_lds, b3, ffn, k0, n0, lid);
    barrier(CLK_LOCAL_MEM_FENCE);
    // fully unrolled: the loop exit test would be s_cmpk_eq_i32 (SOPK 2),
    // which the emulator does not implement
#pragma unroll
    for (int k = 0; k < TK; ++k) {
      const float a = a_lds[r * TK + k];
      const __local float *p1 = b1_lds + k * TN + c0;
      const __local float *p3 = b3_lds + k * TN + c0;
      g0 += a * p1[0]; g1 += a * p1[1]; g2 += a * p1[2]; g3 += a * p1[3];
      g4 += a * p1[4]; g5 += a * p1[5]; g6 += a * p1[6]; g7 += a * p1[7];
      u0 += a * p3[0]; u1 += a * p3[1]; u2 += a * p3[2]; u3 += a * p3[3];
      u4 += a * p3[4]; u5 += a * p3[5]; u6 += a * p3[6]; u7 += a * p3[7];
    }
    barrier(CLK_LOCAL_MEM_FENCE);
  }
  if (r < rows) {
    __global float *o = act + IDX(slot0 + r, ffn) + n0 + c0;
#define SWIGLU(g, u) ((g) * native_recip(1.0f + native_exp2(-(g) * LOG2E)) * (u))
    vstore4((float4)(SWIGLU(g0, u0), SWIGLU(g1, u1), SWIGLU(g2, u2), SWIGLU(g3, u3)), 0, o);
    vstore4((float4)(SWIGLU(g4, u4), SWIGLU(g5, u5), SWIGLU(g6, u6), SWIGLU(g7, u7)), 0, o + 4);
#undef SWIGLU
  }
}

__kernel __attribute__((reqd_work_group_size(256, 1, 1)))
void moe_down(__global const float *act,
              __global const float *w2,
              __global const int *tiles,
              __global float *ys,
              const int hidden,
              const int ffn,
              const int n_tiles_n) {
  __local float a_lds[TM * TK];
  __local float b_lds[TK * TN];

  const int lid = LOCAL_ID;
  const int wgid = get_group_id(0);
  const int mt = wgid / n_tiles_n;
  const int n0 = (wgid % n_tiles_n) * TN;
  const int e = tiles[3 * mt];
  const int slot0 = tiles[3 * mt + 1];
  const int rows = tiles[3 * mt + 2];
  const __global float *b = w2 + IDX(e, ffn * hidden);

  const int r = lid >> 3;
  const int c0 = (lid & 7) * 8;
  float y0 = 0.0f, y1 = 0.0f, y2 = 0.0f, y3 = 0.0f;
  float y4 = 0.0f, y5 = 0.0f, y6 = 0.0f, y7 = 0.0f;

  for (int k0 = 0; k0 < ffn; k0 += TK) {
    load_a_tile(a_lds, act, slot0, rows, ffn, k0, lid);
    load_b_tile(b_lds, b, hidden, k0, n0, lid);
    barrier(CLK_LOCAL_MEM_FENCE);
    // fully unrolled: the loop exit test would be s_cmpk_eq_i32 (SOPK 2),
    // which the emulator does not implement
#pragma unroll
    for (int k = 0; k < TK; ++k) {
      const float a = a_lds[r * TK + k];
      const __local float *p = b_lds + k * TN + c0;
      y0 += a * p[0]; y1 += a * p[1]; y2 += a * p[2]; y3 += a * p[3];
      y4 += a * p[4]; y5 += a * p[5]; y6 += a * p[6]; y7 += a * p[7];
    }
    barrier(CLK_LOCAL_MEM_FENCE);
  }
  if (r < rows) {
    __global float *o = ys + IDX(slot0 + r, hidden) + n0 + c0;
    STORE8(o, y0, y1, y2, y3, y4, y5, y6, y7);
  }
}

__kernel __attribute__((reqd_work_group_size(256, 1, 1)))
void moe_combine(__global const float *ys,
                 __global const int *inv_slot,
                 __global const float *topk_w,
                 __global float *out,
                 const int hidden,
                 const int topk) {
  const int t = get_group_id(0);
  const int lid = LOCAL_ID;
  for (int h = lid * 4; h < hidden; h += 256 * 4) {
    float4 acc = (float4)(0.0f);
    for (int k = 0; k < topk; ++k) {
      const float w = topk_w[t * topk + k];
      const int slot = inv_slot[t * topk + k];
      acc += w * vload4(0, ys + IDX(slot, hidden) + h);
    }
    vstore4(acc, 0, out + IDX(t, hidden) + h);
  }
}
