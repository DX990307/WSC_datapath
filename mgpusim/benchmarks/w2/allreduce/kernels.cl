// Allreduce of R gradient copies (data-parallel training), as NCCL's
// reduce-scatter followed by all-gather, both in pull form (no atomics).
//
//   in      [R][G]  copy of rank r = row r (one buffer: with the default
//                   allocator row r lives on GPM r when R = 48)
//   red     [G]     reduced vector; shard r = elements [r*G/R, (r+1)*G/R)
//   out     [R][G]  every row = red
//
//   ar_reduce_scatter  one float per thread: red[e] = sum_r in[r][e]
//                      (the work-groups of shard r run on GPM r when the
//                      work-group count is a multiple of 48 * CUs per GPM)
//   ar_all_gather      one float4 per thread: out[r][e..e+3] = red[e..e+3]
//
// Simulator rules (T17): no atomics, no private arrays.

#define UMUL(a, b) ((ulong)(uint)(a) * (ulong)(uint)(b))

__kernel __attribute__((reqd_work_group_size(256, 1, 1)))
void ar_reduce_scatter(__global const float *in,
                       __global float *red,
                       const int ranks,
                       const int g) {
  const uint e = get_global_id(0);
  float acc = 0.0f;
  for (int r = 0; r < ranks; r++) {
    acc += in[UMUL(r, g) + e];
  }
  red[e] = acc;
}

__kernel __attribute__((reqd_work_group_size(256, 1, 1)))
void ar_all_gather(__global const float4 *red,
                   __global float4 *out,
                   const int g4) {
  const uint i = get_global_id(0);
  const uint e = i % (uint)g4;
  out[i] = red[e];
}
