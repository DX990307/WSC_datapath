#include "common.h"

__kernel void llm_residual_add(__global float *out,
                               const __global float *a,
                               const __global float *b,
                               int n) {
  int gid = get_global_id(0);
  if (gid >= n) {
    return;
  }

  out[gid] = a[gid] + b[gid];
}
