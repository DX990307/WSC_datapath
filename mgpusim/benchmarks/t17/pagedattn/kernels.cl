// PagedAttention decode (vLLM, SOSP'23) with a FlashDecoding-style split
// over the sequence: one work-group per (sequence, KV head, partition) in
// pa_partial, then one work-group per (sequence, query head) in pa_reduce.
//
// Simulator rules (T17): no atomics, no private arrays (no scratch), only the
// DS opcodes ds_write_b32/write2_b32/read_b32/read2_b32/write2_b64/read_b64/
// read2_b64, fp32 only. build_hsaco.sh checks the generated code.
//
// Layouts (fp32):
//   q            [B][Hq][D]            Hq = Hkv * GQA
//   k_cache      [NB][Hkv][BS][D]      one physical KV block = BS tokens
//   v_cache      [NB][Hkv][BS][D]
//   block_tables [B][max_blocks]       physical block of logical block j
//   seq_lens     [B]
//   work         [n_work][2]           (sequence, partition), host-built
//   tmp_out      [B][Hq][max_parts][D] unnormalised partial outputs
//   max_logits   [B][Hq][max_parts]    log2-domain max of the partition
//   exp_sums     [B][Hq][max_parts]    sum of 2^(s - max) of the partition
//   out          [B][Hq][D]
//
// Scores are kept in the log2 domain (scale_log2 = log2(e) / sqrt(D)), so the
// softmax uses v_exp_f32 (2^x) directly; the result is the usual softmax.

#define D 128
#define GQA 4
#define BS 16
#define MAX_PART 512
#define NEG_INF (-3.0e38f)
// Index arithmetic is unsigned on purpose: signed (long)int*int compiles to
// v_mad_i64_i32 (VOP3a opcode 489), which the emulator does not implement;
// the unsigned form compiles to v_mad_u64_u32 / 32-bit multiplies.
#define UMUL(a, b) ((ulong)(uint)(a) * (ulong)(uint)(b))

__kernel __attribute__((reqd_work_group_size(256, 1, 1)))
void pa_partial(__global const float *q,
                __global const float *k_cache,
                __global const float *v_cache,
                __global float *tmp_out,
                __global float *max_logits,
                __global float *exp_sums,
                // Unused: breaks the kernel arguments into runs shorter than
                // 64 bytes. The compiler loads all arguments at entry and
                // merges 16 contiguous dwords into s_load_dwordx16, which the
                // simulator does not implement (SMEM opcode 4).
                const int kernarg_gap,
                __global const int *block_tables,
                __global const int *seq_lens,
                __global const int *work,
                const float scale_log2,
                const int num_kv_heads,
                const int max_blocks,
                const int part_tokens,
                const int max_parts) {
  __local float sc[GQA * MAX_PART];  // [token][head] scores, then
                                     // probabilities, then
                                     // the cross-wave V partial sums
  __local float pdot[GQA * BS * 16]; // [token][slice][head] QK partials
  __local float red[GQA * 64];       // per-head reductions

  const int lid = get_local_id(0);
  const int wg = get_group_id(0);
  const int kvh = wg % num_kv_heads;
  const int item = wg / num_kv_heads;
  const int seq = work[2 * item];
  const int part = work[2 * item + 1];
  const int len = seq_lens[seq];
  const int tok0 = part * part_tokens;
  int ntok = len - tok0;
  if (ntok > part_tokens) {
    ntok = part_tokens;
  }
  const int blk0 = tok0 / BS;
  const int nblk = (ntok + BS - 1) / BS;
  const int nq = num_kv_heads * GQA;
  const int hq0 = kvh * GQA;
  const uint bt_base = (uint)seq * (uint)max_blocks + (uint)blk0;

  // ---- phase 1: scores. Thread = (token in block, 8-dim slice); the work-
  // group reads one 8 KB K block per step, each byte once.
  const int ktok = lid >> 4;
  const int kseg = lid & 15;
  const __global float *qp = q + ((uint)seq * nq + hq0) * D + kseg * 8;
  const float8 q0 = vload8(0, qp);
  const float8 q1 = vload8(0, qp + D);
  const float8 q2 = vload8(0, qp + 2 * D);
  const float8 q3 = vload8(0, qp + 3 * D);

  for (int b = 0; b < nblk; ++b) {
    const int phys = block_tables[bt_base + b];
    const int t = b * BS + ktok;
    float d0 = 0.0f, d1 = 0.0f, d2 = 0.0f, d3 = 0.0f;
    if (t < ntok) {
      const __global float *kp =
          k_cache + UMUL((uint)phys * num_kv_heads + kvh, BS * D) +
          ktok * D + kseg * 8;
      const float8 kv = vload8(0, kp);
      d0 = dot(q0.lo, kv.lo) + dot(q0.hi, kv.hi);
      d1 = dot(q1.lo, kv.lo) + dot(q1.hi, kv.hi);
      d2 = dot(q2.lo, kv.lo) + dot(q2.hi, kv.hi);
      d3 = dot(q3.lo, kv.lo) + dot(q3.hi, kv.hi);
    }
    pdot[(ktok * 16 + kseg) * GQA + 0] = d0;
    pdot[(ktok * 16 + kseg) * GQA + 1] = d1;
    pdot[(ktok * 16 + kseg) * GQA + 2] = d2;
    pdot[(ktok * 16 + kseg) * GQA + 3] = d3;
    barrier(CLK_LOCAL_MEM_FENCE);
    if (lid < GQA * BS) {
      const int h = lid >> 4;
      const int tk = lid & 15;
      float s = 0.0f;
      for (int i = 0; i < 16; ++i) {
        s += pdot[(tk * 16 + i) * GQA + h];
      }
      const int t2 = b * BS + tk;
      sc[t2 * GQA + h] = (t2 < ntok) ? s * scale_log2 : NEG_INF;
    }
    barrier(CLK_LOCAL_MEM_FENCE);
  }

  // ---- phase 2: softmax inside the partition. Wave h owns query head h.
  const int h = lid >> 6;
  const int lane = lid & 63;
  float m = NEG_INF;
  for (int t = lane; t < ntok; t += 64) {
    m = fmax(m, sc[t * GQA + h]);
  }
  red[h * 64 + lane] = m;
  barrier(CLK_LOCAL_MEM_FENCE);
  for (int s = 32; s > 0; s >>= 1) {
    if (lane < s) {
      red[h * 64 + lane] = fmax(red[h * 64 + lane], red[h * 64 + lane + s]);
    }
    barrier(CLK_LOCAL_MEM_FENCE);
  }
  m = red[h * 64];
  barrier(CLK_LOCAL_MEM_FENCE);

  float l = 0.0f;
  for (int t = lane; t < ntok; t += 64) {
    const float p = native_exp2(sc[t * GQA + h] - m);
    sc[t * GQA + h] = p;
    l += p;
  }
  red[h * 64 + lane] = l;
  barrier(CLK_LOCAL_MEM_FENCE);
  for (int s = 32; s > 0; s >>= 1) {
    if (lane < s) {
      red[h * 64 + lane] += red[h * 64 + lane + s];
    }
    barrier(CLK_LOCAL_MEM_FENCE);
  }
  l = red[h * 64];

  // ---- phase 3: P x V. Wave w takes tokens w, w+4, w+8, w+12 of every
  // block for all four heads; lane = 2 dims. Each V byte is read once.
  const int w = h;
  float a00 = 0.0f, a01 = 0.0f, a10 = 0.0f, a11 = 0.0f;
  float a20 = 0.0f, a21 = 0.0f, a30 = 0.0f, a31 = 0.0f;
  for (int b = 0; b < nblk; ++b) {
    const int phys = block_tables[bt_base + b];
    const __global float *vp =
        v_cache + UMUL((uint)phys * num_kv_heads + kvh, BS * D) + 2 * lane;
    for (int tk = w; tk < BS; tk += GQA) {
      const int t = b * BS + tk;
      if (t < ntok) {
        const float2 v = vload2(0, vp + tk * D);
        const float p0 = sc[t * GQA + 0];
        const float p1 = sc[t * GQA + 1];
        const float p2 = sc[t * GQA + 2];
        const float p3 = sc[t * GQA + 3];
        a00 += p0 * v.x;
        a01 += p0 * v.y;
        a10 += p1 * v.x;
        a11 += p1 * v.y;
        a20 += p2 * v.x;
        a21 += p2 * v.y;
        a30 += p3 * v.x;
        a31 += p3 * v.y;
      }
    }
  }
  barrier(CLK_LOCAL_MEM_FENCE); // all waves are done with the probabilities
  // sc reused as [dim][wave][head]: the 8 values of a thread sit within a
  // few dwords, so the compiler pairs them as ds_write2_b32 rather than the
  // unimplemented ds_write2st64_b32 (same for the reads below).
  const int d0i = (2 * lane) * GQA * GQA + w * GQA;
  const int d1i = (2 * lane + 1) * GQA * GQA + w * GQA;
  sc[d0i + 0] = a00;
  sc[d1i + 0] = a01;
  sc[d0i + 1] = a10;
  sc[d1i + 1] = a11;
  sc[d0i + 2] = a20;
  sc[d1i + 2] = a21;
  sc[d0i + 3] = a30;
  sc[d1i + 3] = a31;
  barrier(CLK_LOCAL_MEM_FENCE);

  float o0 = 0.0f, o1 = 0.0f;
  for (int ww = 0; ww < GQA; ++ww) {
    o0 += sc[(2 * lane) * GQA * GQA + ww * GQA + h];
    o1 += sc[(2 * lane + 1) * GQA * GQA + ww * GQA + h];
  }
  const uint o = ((uint)seq * nq + hq0 + h) * max_parts + part;
  vstore2((float2)(o0, o1), 0, tmp_out + UMUL(o, D) + 2 * lane);
  if (lane == 0) {
    max_logits[o] = m;
    exp_sums[o] = l;
  }
}

// One wavefront per (sequence, query head): merge the partitions.
__kernel __attribute__((reqd_work_group_size(64, 1, 1)))
void pa_reduce(__global const float *tmp_out,
               __global const float *max_logits,
               __global const float *exp_sums,
               __global const int *seq_lens,
               __global float *out,
               const int num_q_heads,
               const int part_tokens,
               const int max_parts) {
  const int lane = get_local_id(0);
  const int wg = get_group_id(0);
  const int seq = wg / num_q_heads;
  const int qh = wg % num_q_heads;
  const int len = seq_lens[seq];
  const int np = (len + part_tokens - 1) / part_tokens;
  const uint base = ((uint)seq * num_q_heads + qh) * max_parts;

  float gm = NEG_INF;
  for (int p = 0; p < np; ++p) {
    gm = fmax(gm, max_logits[base + p]);
  }
  float gl = 0.0f, a0 = 0.0f, a1 = 0.0f;
  for (int p = 0; p < np; ++p) {
    const float wgt = native_exp2(max_logits[base + p] - gm);
    gl += wgt * exp_sums[base + p];
    const float2 v = vload2(0, tmp_out + UMUL(base + p, D) + 2 * lane);
    a0 += wgt * v.x;
    a1 += wgt * v.y;
  }
  const float inv = native_recip(gl);
  vstore2((float2)(a0 * inv, a1 * inv), 0,
          out + UMUL((uint)seq * num_q_heads + qh, D) + 2 * lane);
}
