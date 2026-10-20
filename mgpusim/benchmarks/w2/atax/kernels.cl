// PolyBench ATAX, y = A^T (A x), ported from gpu_benchmarks
// tier2/polybench_atax/polybench_atax.hip to OpenCL C for MGPUSim.
// A is NX x NY row-major (fp32).
//
// Upstream: atax_kernel1 (thread per row i, tmp[i] = sum_j A[i,j] x[j]) and
// atax_kernel2 (thread per column j, y[j] = sum_i A[i,j] tmp[i]), 256-thread
// blocks, i.e. only NX/256 and NY/256 work-groups (32 for N = 8192). To
// fill the 48-GPM wafer (>= 1344 WGs) the same sums are tiled:
//   atax_k1     WG per row i: the 256 threads stride over j (coalesced row
//               read) and reduce in LDS. NX WGs.
//   atax_k2     WG per (256-column block cb, row chunk rc), ordered
//               column-block-major (wg = cb * nchunks + rc): thread j sums
//               A[i,j] tmp[i] over the ROWS of the chunk (coalesced row
//               segments, the upstream column-wise pass) into
//               part[cb][rc][256]. A GPM's contiguous WG range covers whole
//               column blocks, i.e. it reads every row of A (all GPMs), as
//               upstream's column-per-thread kernel does.
//   atax_k2red  WG per 256-column block: y[j] = sum_rc part[cb][rc][j]
//               (NY/256 WGs, small).
// Summation order differs from upstream (tree in k1, chunked in k2): fp32
// results agree within rounding, not bit-exactly.
//
// Simulator rules (T17): no atomics, no private arrays, DS opcodes
// ds_write_b32/write2_b32/read_b32/read2_b32/write2_b64/read_b64/read2_b64.

#define WG 256
// LDS index with one pad dword per 32: keeps the tree reduction's partner
// offsets (128, 64 dwords) from being folded into ds_read2st64_b32.
#define PADI(l) ((l) + ((l) >> 5))

__kernel __attribute__((reqd_work_group_size(WG, 1, 1)))
void atax_k1(__global const float *A, __global const float *x,
             __global float *tmp, const int nx, const int ny) {
  __local float red[WG + WG / 32];
  const int lid = get_local_id(0);
  const int i = get_group_id(0);
  __global const float *row = A + (ulong)(uint)i * (ulong)(uint)ny;
  float sum = 0.0f;
  for (int j = lid; j < ny; j += WG) {
    sum += row[j] * x[j];
  }
  red[PADI(lid)] = sum;
  barrier(CLK_LOCAL_MEM_FENCE);
  for (int s = WG / 2; s > 0; s >>= 1) {
    if (lid < s) {
      red[PADI(lid)] += red[PADI(lid + s)];
    }
    barrier(CLK_LOCAL_MEM_FENCE);
  }
  if (lid == 0) {
    tmp[i] = red[0];
  }
}

__kernel __attribute__((reqd_work_group_size(WG, 1, 1)))
void atax_k2(__global const float *A, __global const float *tmp,
             __global float *part, const int nx, const int ny,
             const int rows_per_chunk, const int nchunks) {
  const int lid = get_local_id(0);
  const int wg = get_group_id(0);
  const int cb = wg / nchunks;
  const int rc = wg - cb * nchunks;
  const int j = cb * WG + lid;
  const int i0 = rc * rows_per_chunk;
  int i1 = i0 + rows_per_chunk;
  if (i1 > nx) {
    i1 = nx;
  }
  float sum = 0.0f;
  if (j < ny) {
    for (int i = i0; i < i1; i++) {
      sum += A[(ulong)(uint)i * (ulong)(uint)ny + (uint)j] * tmp[i];
    }
  }
  part[((ulong)(uint)cb * (uint)nchunks + (uint)rc) * WG + lid] = sum;
}

__kernel __attribute__((reqd_work_group_size(WG, 1, 1)))
void atax_k2red(__global const float *part, __global float *y, const int ny,
                const int nchunks) {
  const int lid = get_local_id(0);
  const int cb = get_group_id(0);
  const int j = cb * WG + lid;
  __global const float *p = part + (ulong)(uint)cb * (uint)nchunks * WG + lid;
  float sum = 0.0f;
  for (int rc = 0; rc < nchunks; rc++) {
    sum += p[rc * WG];
  }
  if (j < ny) {
    y[j] = sum;
  }
}
