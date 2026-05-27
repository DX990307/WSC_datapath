#include "common.h"

__kernel void llm_row_softmax(__global float *out,
                              const __global float *in,
                              int rows,
                              int cols) {
  int row = get_group_id(0);
  int lid = get_local_id(0);
  int local_size = get_local_size(0);

  if (row >= rows) {
    return;
  }

  int base = row * cols;
  float row_max = LLM_NEG_INF;

  for (int col = lid; col < cols; col += local_size) {
    row_max = fmax(row_max, in[base + col]);
  }

  __local float local_max[LLM_SOFTMAX_WORKGROUP_SIZE];
  __local float local_denom[LLM_SOFTMAX_WORKGROUP_SIZE];
  local_max[lid] = row_max;
  barrier(CLK_LOCAL_MEM_FENCE);

  for (int stride = local_size / 2; stride > 0; stride >>= 1) {
    if (lid < stride) {
      local_max[lid] = fmax(local_max[lid], local_max[lid + stride]);
    }
    barrier(CLK_LOCAL_MEM_FENCE);
  }

  row_max = local_max[0];
  float denom = 0.0F;
  for (int col = lid; col < cols; col += local_size) {
    denom += llm_exp_value(in[base + col] - row_max);
  }

  local_denom[lid] = denom;
  barrier(CLK_LOCAL_MEM_FENCE);

  for (int stride = local_size / 2; stride > 0; stride >>= 1) {
    if (lid < stride) {
      local_denom[lid] += local_denom[lid + stride];
    }
    barrier(CLK_LOCAL_MEM_FENCE);
  }

  denom = local_denom[0];
  float inv_denom = llm_recip_value(denom);
  for (int col = lid; col < cols; col += local_size) {
    out[base + col] = llm_exp_value(in[base + col] - row_max) * inv_denom;
  }
}
