// R-GAT style sampled aggregation (T17 W-C), step-0 minimal kernels.
//
//   rgat_proj  one WG per source node: h[n] = x[n] . W   (W [Fin][Fout])
//   rgat_agg   one WG per target node of one relation (CSR from the host
//              sampler): el_j = h_j . a_l for each sampled neighbour j,
//              er_i = h_i . a_r, e_ij = LeakyReLU(el_j + er_i, 0.2),
//              softmax over the neighbours, out_i = acc_i + sum_j a_ij h_j
//              (acc = the previous relation's output; pull, no atomics).
//
// Simulator rules (T17): no atomics, no private arrays, only the implemented
// DS opcodes, fp32.

#define MAXDEG 32
#define NEG_INF (-3.0e38f)
#define LOG2E 1.4426950408889634f
// 32-bit unsigned element offsets and a 32-bit local id: signed 64-bit index
// math compiles to v_mad_i64_i32 / s_ashr_i64 and 64-bit id shifts to
// v_alignbit_b32, none of which the emulator implements.
#define IDX(a, b) ((uint)(a) * (uint)(b))
#define LOCAL_ID ((int)__builtin_amdgcn_workitem_id_x())

__kernel __attribute__((reqd_work_group_size(256, 1, 1)))
void rgat_proj(__global const float *x,
               __global const float *w,
               __global float *h,
               const int fin,
               const int fout) {
  const int n = get_group_id(0);
  const int lid = LOCAL_ID;
  const __global float *xp = x + IDX(n, fin);
  for (int c = lid; c < fout; c += 256) {
    float acc = 0.0f;
    for (int k = 0; k < fin; ++k) {
      acc += xp[k] * w[IDX(k, fout) + c];
    }
    h[IDX(n, fout) + c] = acc;
  }
}

__kernel __attribute__((reqd_work_group_size(256, 1, 1)))
void rgat_agg(__global const float *h_src,
              __global const float *h_dst,
              __global const int *row_ptr,
              __global const int *col_idx,
              __global const float *attn_l,
              __global const float *attn_r,
              // Unused: keeps the kernel arguments out of one 64-byte run
              // (s_load_dwordx16 is not implemented; see pagedattn).
              const int kernarg_gap,
              __global const float *acc_in,
              __global float *out,
              const int f,
              const int use_acc) {
  // One LDS array with score at offset 0: the compiler places separate
  // arrays largest first, and score at 0x2100 makes the neighbour loops add
  // the base with s_addk_i32 (SOPK 14, not implemented).
  __local float lds[MAXDEG + 64 + 64 * MAXDEG];
  __local float *score = lds;                 // [MAXDEG]
  __local float *rpart = lds + MAXDEG;        // [64]
  __local float *part = lds + MAXDEG + 64;    // [lane][neighbour]

  const int i = get_group_id(0);
  const int lid = LOCAL_ID;
  const int wave = lid >> 6;
  const int lane = lid & 63;
  // unsigned index: row_ptr[i + 1] with a signed i becomes s_ashr_i64
  const int beg = row_ptr[(uint)i];
  int deg = row_ptr[(uint)i + 1u] - beg;
  if (deg > MAXDEG) {
    deg = MAXDEG;
  }

  // el_j partial sums: wave w takes neighbours w, w+4, ...
  for (int j = wave; j < deg; j += 4) {
    const __global float *hj = h_src + IDX(col_idx[beg + j], f);
    float s = 0.0f;
    for (int c = lane; c < f; c += 64) {
      s += hj[c] * attn_l[c];
    }
    part[lane * MAXDEG + j] = s;
  }
  // er_i partial sums (wave 0)
  if (wave == 0) {
    const __global float *hi = h_dst + IDX(i, f);
    float s = 0.0f;
    for (int c = lane; c < f; c += 64) {
      s += hi[c] * attn_r[c];
    }
    rpart[lane] = s;
  }
  barrier(CLK_LOCAL_MEM_FENCE);

  // er_i: tree over the 64 lanes (a serial loop over contiguous LDS floats
  // becomes ds_read_b128, a rolled one s_cmpk_eq_i32/s_addk_i32; none of
  // them is implemented)
  for (int st = 32; st > 0; st >>= 1) {
    if (lid < st) {
      rpart[lid] += rpart[lid + st];
    }
    barrier(CLK_LOCAL_MEM_FENCE);
  }
  if (lid < deg) {
    float s = 0.0f;
#pragma unroll
    for (int c = 0; c < 64; ++c) {
      s += part[c * MAXDEG + lid]; // strided: pairs as ds_read2_b32
    }
    score[lid] = s;
  }
  barrier(CLK_LOCAL_MEM_FENCE);

  // neighbour softmax (thread 0; deg <= MAXDEG)
  if (lid == 0) {
    const float er = rpart[0];
    float m = NEG_INF;
    for (int j = 0; j < deg; ++j) {
      float e = score[j] + er;
      e = e > 0.0f ? e : 0.2f * e;
      score[j] = e;
      m = fmax(m, e);
    }
    float sum = 0.0f;
    for (int j = 0; j < deg; ++j) {
      const float p = native_exp2((score[j] - m) * LOG2E);
      score[j] = p;
      sum += p;
    }
    const float inv = deg > 0 ? native_recip(sum) : 0.0f;
    for (int j = 0; j < deg; ++j) {
      score[j] *= inv;
    }
  }
  barrier(CLK_LOCAL_MEM_FENCE);

  for (int c = lid; c < f; c += 256) {
    float acc = use_acc ? acc_in[IDX(i, f) + c] : 0.0f;
    for (int j = 0; j < deg; ++j) {
      acc += score[j] * h_src[IDX(col_idx[beg + j], f) + c];
    }
    out[IDX(i, f) + c] = acc;
  }
}
