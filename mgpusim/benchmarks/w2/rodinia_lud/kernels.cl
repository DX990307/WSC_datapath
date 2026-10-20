// Rodinia LUD (blocked LU, no pivoting), gpu_benchmarks tier2/rodinia_lud,
// W2 port. 16x16 tiles in LDS, WG = 16x16 threads, step `offset`:
//   lud_diagonal   1 WG: factor the diagonal tile in place
//   lud_perimeter  2*nhalf WGs, nhalf = nb-offset-1 (an argument: the
//                  simulator does not provide get_num_groups): row panel (forward solve) in the
//                  first half, column panel (back solve) in the second
//   lud_internal   (nb-offset-1)^2 WGs (2-D grid): Schur complement update
// Bodies as upstream. Row-major element offsets are 32-bit (N <= 46340).

#define BSIZE 16

__kernel __attribute__((reqd_work_group_size(BSIZE, BSIZE, 1)))
void lud_diagonal(__global float *a, const int n, const int offset) {
  __local float s[BSIZE][BSIZE];
  const int tx = get_local_id(0), ty = get_local_id(1);
  const uint base = (uint)(offset * BSIZE) * (uint)n + (uint)(offset * BSIZE);
  s[ty][tx] = a[base + (uint)ty * (uint)n + tx];
  barrier(CLK_LOCAL_MEM_FENCE);
  for (int k = 0; k < BSIZE - 1; k++) {
    if (ty > k && tx == k) s[ty][k] /= s[k][k];
    barrier(CLK_LOCAL_MEM_FENCE);
    if (ty > k && tx > k) s[ty][tx] -= s[ty][k] * s[k][tx];
    barrier(CLK_LOCAL_MEM_FENCE);
  }
  a[base + (uint)ty * (uint)n + tx] = s[ty][tx];
}

__kernel __attribute__((reqd_work_group_size(BSIZE, BSIZE, 1)))
void lud_perimeter(__global float *a, const int n, const int offset,
                   const int nhalf) {
  __local float dia[BSIZE][BSIZE];
  __local float peri[BSIZE][BSIZE];
  const int tx = get_local_id(0), ty = get_local_id(1);
  const int g = get_group_id(0);
  const bool is_row = g < nhalf;
  const int idx = is_row ? g : g - nhalf;
  const int blk = offset + idx + 1;
  const uint row0 = (uint)(offset * BSIZE);
  const uint un = (uint)n;

  dia[ty][tx] = a[(row0 + ty) * un + row0 + tx];
  uint p;
  if (is_row)
    p = (row0 + ty) * un + (uint)(blk * BSIZE) + tx;
  else
    p = ((uint)(blk * BSIZE) + ty) * un + row0 + tx;
  peri[ty][tx] = a[p];
  barrier(CLK_LOCAL_MEM_FENCE);

  if (is_row) {
    for (int k = 0; k < BSIZE - 1; k++) {
      if (ty > k) peri[ty][tx] -= dia[ty][k] * peri[k][tx];
      barrier(CLK_LOCAL_MEM_FENCE);
    }
  } else {
    for (int k = 0; k < BSIZE; k++) {
      if (tx == k) peri[ty][k] /= dia[k][k];
      barrier(CLK_LOCAL_MEM_FENCE);
      if (tx > k) peri[ty][tx] -= peri[ty][k] * dia[k][tx];
      barrier(CLK_LOCAL_MEM_FENCE);
    }
  }
  a[p] = peri[ty][tx];
}

__kernel __attribute__((reqd_work_group_size(BSIZE, BSIZE, 1)))
void lud_internal(__global float *a, const int n, const int offset) {
  __local float peri_row[BSIZE][BSIZE];
  __local float peri_col[BSIZE][BSIZE];
  const int tx = get_local_id(0), ty = get_local_id(1);
  const uint col0 = (uint)((offset + get_group_id(0) + 1) * BSIZE);
  const uint rowb = (uint)((offset + get_group_id(1) + 1) * BSIZE);
  const uint off0 = (uint)(offset * BSIZE);
  const uint un = (uint)n;

  peri_row[ty][tx] = a[(off0 + ty) * un + col0 + tx];
  peri_col[ty][tx] = a[(rowb + ty) * un + off0 + tx];
  barrier(CLK_LOCAL_MEM_FENCE);
  float sum = 0.0f;
  for (int k = 0; k < BSIZE; k++) sum += peri_col[ty][k] * peri_row[k][tx];
  a[(rowb + ty) * un + col0 + tx] -= sum;
}
