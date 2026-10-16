// T14 platform microbenchmarks (fp32 only).

// Single-thread pointer chase: every load depends on the previous one, so at
// most one request is in flight. next[] holds element indices.
// Adding the local id (always 0, launched with one thread) keeps p in a vector
// register; otherwise the compiler emits s_load_dword and the chase measures
// the scalar cache instead of L1V.
__kernel void chase(__global const uint* next, __global uint* out,
                    const uint start, const uint steps) {
  uint p = start + (uint)get_local_id(0);
  for (uint i = 0; i < steps; i++) {
    p = next[p];
  }
  out[0] = p;
}

// Streaming bandwidth over n4 float4s, repeated iters times. Only work-groups
// below active_wgs do work; with the partition dispatcher, work-group g runs
// on CU g / wgs_per_cu, so this selects the active CUs.
// mode 0: read a; mode 1: write a; mode 2: copy a -> b.
// Signed int counters: the simulator does not implement s_cmp_le_u32.
__kernel void bw(__global float4* a, __global float4* b, __global float* sink,
                 const int n4, const int iters, const int mode,
                 const int active_wgs) {
  int g = (int)get_group_id(0);
  if (g >= active_wgs) {
    return;
  }
  int lsz = (int)get_local_size(0);
  int tid = g * lsz + (int)get_local_id(0);
  int nt = active_wgs * lsz;
  float4 acc = (float4)(0.0f);
  for (int it = 0; it < iters; it++) {
    for (int i = tid; i < n4; i += nt) {
      if (mode == 0) {
        acc += a[i];
      } else if (mode == 1) {
        a[i] = (float4)((float)i);
      } else {
        b[i] = a[i];
      }
    }
  }
  if (mode == 0 && acc.x == 12345.0f) {
    sink[0] = acc.x + acc.y + acc.z + acc.w;
  }
}

// Reads chunk4 float4s at offset off4 from the shard of every rank listed in
// srcs[0..nsrc), through the pointer table (table[r] = address of rank r's
// shard). Work-groups start at different sources so that sources are read
// concurrently.
__kernel void gather(__global const ulong* table, __global const uint* srcs,
                     __global float* sink, const int nsrc, const int chunk4,
                     const int off4, const int iters) {
  int g = (int)get_group_id(0);
  int ngroups = (int)get_num_groups(0);
  int lsz = (int)get_local_size(0);
  float4 acc = (float4)(0.0f);
  for (int it = 0; it < iters; it++) {
    for (int s0 = 0; s0 < nsrc; s0++) {
      int s = (s0 + g) % nsrc;
      __global const float4* p = (__global const float4*)(table[srcs[s]]) + off4;
      for (int i = g * lsz + (int)get_local_id(0); i < chunk4; i += ngroups * lsz) {
        acc += p[i];
      }
    }
  }
  if (acc.x == 12345.0f) {
    sink[0] = acc.x + acc.y + acc.z + acc.w;
  }
}

// T16 remote-fraction sweep. Every wavefront reads iters 1-KB chunks (one
// float4 per lane, 16 cache lines). For each chunk a hash of (rank, wavefront,
// iteration) decides whether it is remote, with probability fthr / 65536: a
// remote chunk comes from a uniformly random other rank's shard (all-to-all),
// a local chunk from this rank's own shard; the chunk index within the shard
// is uniformly random too. The shard holds (chunk_mask + 1) chunks (a power of
// two), reached through table[rank]. Only shifts, xor, multiply and signed
// compares are used (the simulator lacks s_cmp_le_u32).
uint t16_hash(uint h) {
  h ^= h >> 16;
  h *= 0x7feb352dU;
  h ^= h >> 15;
  h *= 0x846ca68bU;
  h ^= h >> 16;
  return h;
}

__kernel void mixread(__global const ulong* table, __global float* sink,
                      const int me, const int nranks, const int chunk_mask,
                      const int iters, const int fthr, const int seed) {
  int lid = (int)get_local_id(0);
  int wfPerWG = ((int)get_local_size(0) + 63) >> 6;
  uint wf = (uint)((int)get_group_id(0) * wfPerWG + (lid >> 6));
  int lane = lid & 63;
  uint base = (uint)seed ^ ((uint)me * 0x9e3779b9U) ^ (wf * 0x85ebca6bU);
  float4 acc = (float4)(0.0f);
  for (int it = 0; it < iters; it++) {
    uint h = t16_hash(base ^ ((uint)it * 0xc2b2ae35U));
    int dst = me;
    if ((int)(h & 0xffffU) < fthr) {
      int k = (int)(((h >> 16) * (uint)(nranks - 1)) >> 16);
      dst = k >= me ? k + 1 : k;
    }
    int chunk = (int)(t16_hash(h + 0x27d4eb2dU) & (uint)chunk_mask);
    __global const float4* p = (__global const float4*)(table[dst]);
    acc += p[chunk * 64 + lane];
  }
  if (acc.x == 12345.0f) {
    sink[0] = acc.x + acc.y + acc.z + acc.w;
  }
}

__kernel void empty(__global float* sink) {
  if (get_global_id(0) == 0x7fffffff) {
    sink[0] = 1.0f;
  }
}
