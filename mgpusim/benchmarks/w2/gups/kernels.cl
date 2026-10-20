// GUPS / RandomAccess (Altis gups.cu, HPCC): 4*N LFSR XOR updates to a
// power-of-two table of 64-bit words. Thread t continues the stream from the
// host-computed state starts[t] (upstream jumps with a square table on the
// GPU) and applies q (+1 for t < r) updates with a plain load / xor / store:
// no atomics, so concurrent updates of one entry may be lost (HPCC allows
// <= 1% wrong entries).
//
// Simulator rules (T17): no atomics, no private arrays.

#define POLY 0x7UL

__kernel __attribute__((reqd_work_group_size(256, 1, 1)))
void gups_update(__global ulong *table,
                 __global const ulong *starts,
                 const uint mask,
                 const uint q,
                 const uint r,
                 const uint pad) {
  const uint t = get_global_id(0);
  ulong s = starts[t];
  const uint n = q + (t < r ? 1u : 0u);
  for (uint i = 0; i < n; i++) {
    s = (s << 1) ^ (((long)s < 0) ? POLY : 0UL);
    const uint e = (uint)s & mask;
    table[e] ^= s;
  }
}
