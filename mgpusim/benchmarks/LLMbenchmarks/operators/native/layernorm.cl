#include "common.h"

__kernel void llm_layernorm(__global float *out,
                            const __global float *in,
                            int rows,
                            int hidden,
                            float epsilon) {
  int row = get_group_id(0);
  int lid = get_local_id(0);
  int local_size = get_local_size(0);

  if (row >= rows) {
    return;
  }

  float sum = 0.0F;
  float sq_sum = 0.0F;
  int base = row * hidden;

  for (int col = lid; col < hidden; col += local_size) {
    float x = in[base + col];
    sum += x;
    sq_sum += x * x;
  }

  __local float local_data[LLM_LN_WORKGROUP_SIZE];
  local_data[lid] = sum;
  barrier(CLK_LOCAL_MEM_FENCE);

  for (int stride = local_size / 2; stride > 0; stride >>= 1) {
    if (lid < stride) {
      local_data[lid] += local_data[lid + stride];
    }
    barrier(CLK_LOCAL_MEM_FENCE);
  }

  float inv_hidden = 1.0F / (float)hidden;
  float mean = local_data[0] * inv_hidden;

  local_data[lid] = sq_sum;
  barrier(CLK_LOCAL_MEM_FENCE);

  for (int stride = local_size / 2; stride > 0; stride >>= 1) {
    if (lid < stride) {
      local_data[lid] += local_data[lid + stride];
    }
    barrier(CLK_LOCAL_MEM_FENCE);
  }

  if (lid == 0) {
    float mean_sq = local_data[0] * inv_hidden;
    float variance = mean_sq - mean * mean;
    if (variance < 0.0F) {
      variance = 0.0F;
    }
    local_data[0] = llm_safe_rsqrt(variance, epsilon);
  }
  barrier(CLK_LOCAL_MEM_FENCE);

  float inv_std = local_data[0];

  for (int col = lid; col < hidden; col += local_size) {
    out[base + col] = (in[base + col] - mean) * inv_std;
  }
}
