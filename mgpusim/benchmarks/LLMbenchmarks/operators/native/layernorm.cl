#include "common.h"

__kernel void llm_layernorm(__global float *out,
                            const __global float *in,
                            int rows,
                            int hidden,
                            float epsilon) {
  int row = get_group_id(0);
  int lid = get_local_id(0);

  if (row >= rows || lid != 0) {
    return;
  }

  float sum = 0.0F;
  float sq_sum = 0.0F;
  int base = row * hidden;
  for (int col = 0; col < hidden; col++) {
    float x = in[base + col];
    sum += x;
    sq_sum += x * x;
  }

  float inv_hidden = 1.0F / (float)hidden;
  float mean = sum * inv_hidden;
  float mean_sq = sq_sum * inv_hidden;
  float variance = fmax(mean_sq - mean * mean, 0.0F);
  float inv_std = llm_safe_rsqrt(variance, epsilon);

  for (int col = 0; col < hidden; col++) {
    out[base + col] = (in[base + col] - mean) * inv_std;
  }
}
