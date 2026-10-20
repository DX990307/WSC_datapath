// DLRM embedding bag (sum pooling), inference only, pull form.
//
// One bag = (sample b, table t); a 256-thread work-group pools two bags
// (128 threads each, one embedding dimension per thread). Bag index
// bag = b * T + t, so consecutive work-groups walk samples in order and every
// work-group gathers rows from all tables (all-to-all reads of the table
// buffer; hot rows are read by every GPM).
//
//   emb     [T][R][D]  fp32, one buffer
//   idx     [B*T][P]   row in its table (host-generated, power law)
//   out     [B*T][D]   = [B][T][D] (DLRM concatenation order)
//
// Simulator rules (T17): no atomics, no private arrays, fp32.

#define D 128
#define UMUL(a, b) ((ulong)(uint)(a) * (ulong)(uint)(b))

__kernel __attribute__((reqd_work_group_size(256, 1, 1)))
void embbag_sum(__global const float *emb,
                __global const int *idx,
                __global float *out,
                const int num_tables,
                const int rows,
                const int pooling,
                const int num_bags) {
  const int lid = get_local_id(0);
  const int bag = get_group_id(0) * 2 + (lid >> 7);
  const int d = lid & (D - 1);
  if (bag >= num_bags) {
    return;
  }
  const int t = bag % num_tables;
  const ulong tbase = UMUL(t, rows);
  const uint ibase = (uint)bag * (uint)pooling;
  float acc = 0.0f;
  for (int p = 0; p < pooling; p++) {
    const uint r = (uint)idx[ibase + p];
    acc += emb[(tbase + r) * D + d];
  }
  out[UMUL(bag, D) + d] = acc;
}
