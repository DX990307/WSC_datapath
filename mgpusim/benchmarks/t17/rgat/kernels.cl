// R-GAT style sampled aggregation (MLPerf Inference RGAT on IGBH), T17 W-C.
// One layer = for every relation r (src type -> dst type) of the sampled
// block, two kernels (no atomics, pull only):
//
//   rgat_proj  WG per (32 sampled source rows, 64 output columns):
//              h_r[k] = x_src[src_ids[k]] . W_r     (gather + projection)
//   rgat_agg   WG per destination node i of the block:
//              er    = x_dst[dst_ids[i]] . v_r      (v_r = W_r a_r, host)
//              el_j  = h_r[j] . a_l                 for sampled neighbours j
//              e_ij  = LeakyReLU(el_j + er, 0.2), softmax over j
//              out[i] = (acc ? out[i] : 0) + sum_j alpha_ij h_r[j]
//              (ReLU on the last relation of a hidden layer)
//
// The relations into one destination type run one after another and add
// into the same output rows (each row is read and written by its own WG).
//
// Simulator rules (T17): no atomics, no private arrays, only implemented DS
// opcodes, fp32. 32-bit element offsets (every buffer < 2^32 elements; the
// host checks it).

#define MAXDEG 32
#define TM 32
#define TN 64
#define TK 32
#define NEG_INF (-3.0e38f)
#define LOG2E 1.4426950408889634f
#define IDX(a, b) ((uint)(a) * (uint)(b))
#define LOCAL_ID ((int)__builtin_amdgcn_workitem_id_x())

__kernel __attribute__((reqd_work_group_size(256, 1, 1)))
void rgat_proj(__global const float *x,
               __global const int *src_ids,
               __global const float *w,
               __global float *h,
               const int n_rows,
               const int f_in,
               const int f_out,
               const int n_tiles_n) {
  __local float a_lds[TM * TK];
  __local float b_lds[TK * TN];

  const int lid = LOCAL_ID;
  const int wg = get_group_id(0);
  const int row0 = (wg / n_tiles_n) * TM;
  const int n0 = (wg % n_tiles_n) * TN;
  const int r = lid >> 3;
  const int c0 = (lid & 7) * 8;

  // the gathered feature row this thread loads into a_lds (row r, 4 floats)
  const int ar = lid >> 3;
  const int ak = (lid & 7) * 4;
  const bool a_valid = row0 + ar < n_rows;
  const uint a_base = a_valid ? IDX(src_ids[row0 + ar], f_in) : 0;
  // b tile: row bk, 8 contiguous columns
  const int bk = lid >> 3;
  const int bc = (lid & 7) * 8;

  float y0 = 0.0f, y1 = 0.0f, y2 = 0.0f, y3 = 0.0f;
  float y4 = 0.0f, y5 = 0.0f, y6 = 0.0f, y7 = 0.0f;
  for (int k0 = 0; k0 < f_in; k0 += TK) {
    float4 v = (float4)(0.0f);
    if (a_valid) {
      v = vload4(0, x + a_base + k0 + ak);
    }
    a_lds[ar * TK + ak] = v.x;
    a_lds[ar * TK + ak + 1] = v.y;
    a_lds[ar * TK + ak + 2] = v.z;
    a_lds[ar * TK + ak + 3] = v.w;
    const __global float *src = w + IDX(k0 + bk, f_out) + n0 + bc;
    const float4 b0 = vload4(0, src);
    const float4 b1 = vload4(0, src + 4);
    __local float *dst = b_lds + bk * TN + bc;
    dst[0] = b0.x;
    dst[1] = b0.y;
    dst[2] = b0.z;
    dst[3] = b0.w;
    dst[4] = b1.x;
    dst[5] = b1.y;
    dst[6] = b1.z;
    dst[7] = b1.w;
    barrier(CLK_LOCAL_MEM_FENCE);
#pragma unroll
    for (int k = 0; k < TK; ++k) {
      const float a = a_lds[r * TK + k];
      const __local float *p = b_lds + k * TN + c0;
      y0 += a * p[0]; y1 += a * p[1]; y2 += a * p[2]; y3 += a * p[3];
      y4 += a * p[4]; y5 += a * p[5]; y6 += a * p[6]; y7 += a * p[7];
    }
    barrier(CLK_LOCAL_MEM_FENCE);
  }
  if (row0 + r < n_rows) {
    __global float *o = h + IDX(row0 + r, f_out) + n0 + c0;
    vstore4((float4)(y0, y1, y2, y3), 0, o);
    vstore4((float4)(y4, y5, y6, y7), 0, o + 4);
  }
}

__kernel __attribute__((reqd_work_group_size(256, 1, 1)))
void rgat_agg(__global const float *h_src,
              __global const float *x_dst,
              __global const int *dst_ids,
              __global const int *row_ptr,
              __global const int *col_idx,
              __global const float *attn_l,
              __global const float *v_r,
              __global float *out,
              const int f_in,
              const int f_h,
              const int use_acc,
              const int act) {
  // one LDS array, score at offset 0 (see build_hsaco.sh)
  __local float lds[MAXDEG + 320 + 64 * MAXDEG];
  __local float *score = lds;               // [MAXDEG]
  __local float *rpart = lds + MAXDEG;      // [64][5]
  __local float *part = lds + MAXDEG + 320; // [lane][neighbour]

  const int i = get_group_id(0);
  const int lid = LOCAL_ID;
  const int wave = lid >> 6;
  const int lane = lid & 63;
  const int beg = row_ptr[(uint)i];
  int deg = row_ptr[(uint)i + 1u] - beg;
  if (deg > MAXDEG) {
    deg = MAXDEG;
  }

  // el_j partial sums: wave w takes neighbours w, w+4, ...
  for (int j = wave; j < deg; j += 4) {
    const __global float *hj = h_src + IDX(col_idx[beg + j], f_h);
    float s = 0.0f;
    for (int c = lane; c < f_h; c += 64) {
      s += hj[c] * attn_l[c];
    }
    part[lane * MAXDEG + j] = s;
  }
  // er_i = x_dst . v_r over f_in, all 256 threads; partials stored with a
  // stride of 5 so no two operands are a multiple of 64 dwords apart
  // (ds_read2st64_b32 is not implemented)
  {
    const __global float *xi = x_dst + IDX(dst_ids[i], f_in);
    float s = 0.0f;
    for (int c = lid; c < f_in; c += 256) {
      s += xi[c] * v_r[c];
    }
    rpart[lane * 5 + wave] = s;
  }
  barrier(CLK_LOCAL_MEM_FENCE);
  if (lid < 64) {
    rpart[lid * 5 + 4] = rpart[lid * 5] + rpart[lid * 5 + 1] +
                         rpart[lid * 5 + 2] + rpart[lid * 5 + 3];
  }
  barrier(CLK_LOCAL_MEM_FENCE);
  for (int st = 32; st > 0; st >>= 1) {
    if (lid < st) {
      rpart[lid * 5 + 4] += rpart[(lid + st) * 5 + 4];
    }
    barrier(CLK_LOCAL_MEM_FENCE);
  }
  if (lid < deg) {
    float s = 0.0f;
#pragma unroll
    for (int c = 0; c < 64; ++c) {
      s += part[c * MAXDEG + lid];
    }
    score[lid] = s;
  }
  barrier(CLK_LOCAL_MEM_FENCE);

  if (lid == 0) {
    const float er = rpart[4];
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

  for (int c = lid; c < f_h; c += 256) {
    const uint o = IDX(i, f_h) + c;
    float acc = use_acc ? out[o] : 0.0f;
    for (int j = 0; j < deg; ++j) {
      acc += score[j] * h_src[IDX(col_idx[beg + j], f_h) + c];
    }
    if (act) {
      acc = fmax(acc, 0.0f);
    }
    out[o] = acc;
  }
}
