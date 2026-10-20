// NAS Parallel Benchmarks IS (integer sort, rank()) ported from
// gpu_benchmarks tier2/npb_is/npb_is.hip to OpenCL C for the CuPath wafer
// (W2 workload set), rewritten without atomics.
//
// Upstream rank(): perturb two keys, clear key_buff1[MAX_KEY], histogram all
// keys with atomicAdd (copying them to key_buff2), inclusive scan ->
// key_buff1[k] = #keys <= k. Here the same key_buff1 is computed with NPB's
// bucketed variant (USE_BUCKETS, NB buckets of W = MAX_KEY / NB key values)
// in the MPI-IS style, all counts privatised:
//   is_modify      1 WG: key[it] = it, key[it + 10] = MAX_KEY - it
//   is_count       WG per tile of 2048 keys (8 per thread): per-wavefront
//                  private bucket histograms in LDS; tile counts
//                  C[tile][NB] (local write) and each key's rank inside
//                  (tile, bucket) R[i] (local write)
//   is_colscan     pull reduce-scatter: WG per 16 buckets reads that column
//                  strip of C from every tile (all GPMs): exclusive offsets
//                  Off[tile][b] and bucket totals Tot[b]
//   is_bucketscan  1 WG: BStart[b] = exclusive scan of Tot (NB entries)
//   is_scatter     WG per tile: key_buff2[BStart[b] + Off[tile][b] + R[i]]
//                  = key[i] (all-to-all write)
//   is_rank        WG per bucket: per-wavefront histograms of its W key
//                  values in LDS, inclusive scan + BStart[b] ->
//                  key_buff1[b*W .. b*W + W) (= upstream key_buff1)
//   is_gather      after the last iteration: rank[i] = key_buff1[key[i]]
//                  (full_verify-style gather; hot middle bins)
//
// LDS histograms without atomics: each wavefront owns a sub-histogram and
// updates it with the tag-and-check loop of pre-atomic GPUs (write
// lane-tag | count+1, read back; the lane whose tag survived owns that
// increment, the others retry). This relies on wavefront-synchronous LDS
// access (64 lanes in lock step), true for GCN hardware and MGPUSim.
//
// Simulator rules (T17): no atomics, no private arrays, DS opcodes
// ds_write_b32/write2_b32/read_b32/read2_b32/write2_b64/read_b64/read2_b64;
// 64-bit index math as 32x32 multiplies (no 64-bit shifts by a register).

#define WGS 256
#define KPT 8
#define TILE (WGS * KPT)
#define CMASK 0x03FFFFFFu
#define BPW 16  // buckets per is_colscan WG
#define PADI(l) ((l) + ((l) >> 5))
#define UMUL(a, b) ((ulong)(uint)(a) * (ulong)(uint)(b))

// tag-and-check increment of h[b]; returns the count before the increment
inline uint tagged_inc(volatile __local uint *h, uint b, uint lane) {
  uint rank = 0;
  int done = 0;
  while (!done) {
    uint old = h[b] & CMASK;
    uint tagged = (lane << 26) | (old + 1);
    h[b] = tagged;
    if (h[b] == tagged) {
      done = 1;
      rank = old;
    }
  }
  return rank;
}

__kernel void is_modify(__global int *key, const int iteration,
                        const int max_key) {
  if (get_global_id(0) == 0) {
    key[iteration] = iteration;
    key[iteration + 10] = max_key - iteration;
  }
}

// hist: 4 wavefronts x nb counters (nb <= 1024)
__kernel __attribute__((reqd_work_group_size(WGS, 1, 1)))
void is_count(__global const int *key, __global uint *C, __global uint *R,
              const int nb, const int logw) {
  __local uint hist[4 * 1024];
  const uint lid = get_local_id(0);
  const uint wave = lid >> 6;
  const uint lane = lid & 63;
  const uint tile = get_group_id(0);
  volatile __local uint *h = hist + wave * (uint)nb;

  for (uint b = lid; b < 4u * (uint)nb; b += WGS) {
    hist[b] = 0;
  }
  barrier(CLK_LOCAL_MEM_FENCE);

  // keys tile*TILE + k*WGS + lid, k = 0..7 (coalesced); registers only
  const ulong base = UMUL(tile, TILE) + lid;
  uint k0 = key[base + 0 * WGS], k1 = key[base + 1 * WGS];
  uint k2 = key[base + 2 * WGS], k3 = key[base + 3 * WGS];
  uint k4 = key[base + 4 * WGS], k5 = key[base + 5 * WGS];
  uint k6 = key[base + 6 * WGS], k7 = key[base + 7 * WGS];
  uint r0 = tagged_inc(h, k0 >> logw, lane);
  uint r1 = tagged_inc(h, k1 >> logw, lane);
  uint r2 = tagged_inc(h, k2 >> logw, lane);
  uint r3 = tagged_inc(h, k3 >> logw, lane);
  uint r4 = tagged_inc(h, k4 >> logw, lane);
  uint r5 = tagged_inc(h, k5 >> logw, lane);
  uint r6 = tagged_inc(h, k6 >> logw, lane);
  uint r7 = tagged_inc(h, k7 >> logw, lane);
  barrier(CLK_LOCAL_MEM_FENCE);

  // per bucket: exclusive offsets of the 4 wavefronts, tile count
  for (uint b = lid; b < (uint)nb; b += WGS) {
    uint c0 = hist[b] & CMASK;
    uint c1 = hist[(uint)nb + b] & CMASK;
    uint c2 = hist[2u * nb + b] & CMASK;
    uint c3 = hist[3u * nb + b] & CMASK;
    hist[b] = 0;
    hist[(uint)nb + b] = c0;
    hist[2u * nb + b] = c0 + c1;
    hist[3u * nb + b] = c0 + c1 + c2;
    C[UMUL(tile, nb) + b] = c0 + c1 + c2 + c3;
  }
  barrier(CLK_LOCAL_MEM_FENCE);

  __local uint *o = hist + wave * (uint)nb;
  R[base + 0 * WGS] = r0 + o[k0 >> logw];
  R[base + 1 * WGS] = r1 + o[k1 >> logw];
  R[base + 2 * WGS] = r2 + o[k2 >> logw];
  R[base + 3 * WGS] = r3 + o[k3 >> logw];
  R[base + 4 * WGS] = r4 + o[k4 >> logw];
  R[base + 5 * WGS] = r5 + o[k5 >> logw];
  R[base + 6 * WGS] = r6 + o[k6 >> logw];
  R[base + 7 * WGS] = r7 + o[k7 >> logw];
}

// WG per BPW buckets; thread (g = lid / BPW, bl = lid % BPW) scans tiles
// [g * chunk, (g + 1) * chunk) of bucket wg * BPW + bl.
__kernel __attribute__((reqd_work_group_size(WGS, 1, 1)))
void is_colscan(__global const uint *C, __global uint *Off,
                __global uint *Tot, const int nb, const int nt) {
  __local uint s[16 * (BPW + 1)];
  const uint lid = get_local_id(0);
  const uint bl = lid & (BPW - 1);
  const uint g = lid >> 4;
  const uint b = get_group_id(0) * BPW + bl;
  const uint chunk = ((uint)nt + 15) >> 4;
  const uint t0 = g * chunk;
  uint t1 = t0 + chunk;
  if (t1 > (uint)nt) {
    t1 = nt;
  }
  uint sum = 0;
  if (b < (uint)nb) {
    for (uint t = t0; t < t1; t++) {
      sum += C[UMUL(t, nb) + b];
    }
  }
  s[g * (BPW + 1) + bl] = sum;  // [g][bl], stride BPW + 1
  barrier(CLK_LOCAL_MEM_FENCE);
  uint pre = 0;
  for (uint j = 0; j < g; j++) {
    pre += s[j * (BPW + 1) + bl];
  }
  if (b < (uint)nb) {
    for (uint t = t0; t < t1; t++) {
      const ulong idx = UMUL(t, nb) + b;
      const uint c = C[idx];
      Off[idx] = pre;
      pre += c;
    }
    if (g == 15) {
      Tot[b] = pre;
    }
  }
}

// one WG: BStart[b] = sum_{b' < b} Tot[b'], BStart[nb] = total
__kernel __attribute__((reqd_work_group_size(WGS, 1, 1)))
void is_bucketscan(__global const uint *Tot, __global uint *BStart,
                   const int nb, const int per) {
  __local uint s[PADI(WGS) + 1];
  const uint lid = get_local_id(0);
  // per = ceil(nb / WGS) is an argument: computing it here makes the
  // compiler test it against 256 with s_cmpk_*_u32 (not in the emulator)
  const uint b0 = lid * (uint)per;
  uint sum = 0;
  for (int j = 0; j < per; j++) {
    if (b0 + j < (uint)nb) {
      sum += Tot[b0 + j];
    }
  }
  s[PADI(lid)] = sum;
  barrier(CLK_LOCAL_MEM_FENCE);
  for (uint d = 1; d < WGS; d <<= 1) {
    uint v = lid >= d ? s[PADI(lid - d)] : 0;
    barrier(CLK_LOCAL_MEM_FENCE);
    s[PADI(lid)] += v;
    barrier(CLK_LOCAL_MEM_FENCE);
  }
  uint pre = s[PADI(lid)] - sum;  // exclusive
  for (int j = 0; j < per; j++) {
    if (b0 + j < (uint)nb) {
      BStart[b0 + j] = pre;
      pre += Tot[b0 + j];
    }
  }
  if (lid == WGS - 1) {
    BStart[nb] = s[PADI(lid)];
  }
}

__kernel __attribute__((reqd_work_group_size(WGS, 1, 1)))
void is_scatter(__global const int *key, __global const uint *R,
                __global const uint *Off, __global const uint *BStart,
                __global int *key_buff2, const int nb, const int logw) {
  const uint lid = get_local_id(0);
  const uint tile = get_group_id(0);
  const ulong base = UMUL(tile, TILE) + lid;
  const ulong orow = UMUL(tile, nb);
  for (int k = 0; k < KPT; k++) {
    const ulong i = base + (uint)(k * WGS);
    const uint kv = key[i];
    const uint b = kv >> logw;
    const uint pos = BStart[b] + Off[orow + b] + R[i];
    key_buff2[pos] = kv;
  }
}

// WG per bucket; hist: 4 wavefronts x w bins (w <= 2048)
__kernel __attribute__((reqd_work_group_size(WGS, 1, 1)))
void is_rank(__global const int *key_buff2, __global const uint *BStart,
             __global int *key_buff1, const int w, const int per) {
  __local uint hist[4 * 2048];
  __local uint s[PADI(WGS) + 1];
  const uint lid = get_local_id(0);
  const uint wave = lid >> 6;
  const uint lane = lid & 63;
  const uint b = get_group_id(0);
  volatile __local uint *h = hist + wave * (uint)w;
  for (uint j = lid; j < 4u * (uint)w; j += WGS) {
    hist[j] = 0;
  }
  barrier(CLK_LOCAL_MEM_FENCE);
  const uint start = BStart[b];
  const uint end = BStart[b + 1];
  const uint kbase = b * (uint)w;
  for (uint i = start + lid; i < end; i += WGS) {
    tagged_inc(h, (uint)key_buff2[i] - kbase, lane);
  }
  barrier(CLK_LOCAL_MEM_FENCE);
  // thread lid owns bins [lid * per, (lid + 1) * per), per = w / WGS (an
  // argument, see is_bucketscan)
  uint sum = 0;
  for (int j = 0; j < per; j++) {
    const uint bin = lid * (uint)per + j;
    sum += (hist[bin] & CMASK) + (hist[(uint)w + bin] & CMASK) +
           (hist[2u * w + bin] & CMASK) + (hist[3u * w + bin] & CMASK);
  }
  s[PADI(lid)] = sum;
  barrier(CLK_LOCAL_MEM_FENCE);
  for (uint d = 1; d < WGS; d <<= 1) {
    uint v = lid >= d ? s[PADI(lid - d)] : 0;
    barrier(CLK_LOCAL_MEM_FENCE);
    s[PADI(lid)] += v;
    barrier(CLK_LOCAL_MEM_FENCE);
  }
  uint run = start + s[PADI(lid)] - sum;
  for (int j = 0; j < per; j++) {
    const uint bin = lid * (uint)per + j;
    run += (hist[bin] & CMASK) + (hist[(uint)w + bin] & CMASK) +
           (hist[2u * w + bin] & CMASK) + (hist[3u * w + bin] & CMASK);
    key_buff1[kbase + bin] = run;
  }
}

__kernel __attribute__((reqd_work_group_size(WGS, 1, 1)))
void is_gather(__global const int *key, __global const int *key_buff1,
               __global int *rank, const int n) {
  const uint i = get_global_id(0);
  if (i < (uint)n) {
    rank[i] = key_buff1[key[i]];
  }
}
