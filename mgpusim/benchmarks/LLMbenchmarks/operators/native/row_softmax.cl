#include "common.h"

__kernel void llm_row_softmax(__global float *out,
                              const __global float *in,
                              int rows,
                              int cols) {
  int row = get_group_id(0);
  int lid = get_local_id(0);

  if (row >= rows || lid != 0) {
    return;
  }

  int base = row * cols;
  float row_max = LLM_NEG_INF;
  for (int col = 0; col < cols; col++) {
    row_max = fmax(row_max, in[base + col]);
  }

  float denom = 0.0F;
  for (int col = 0; col < cols; col++) {
    denom += llm_exp_approx(in[base + col] - row_max);
  }

  for (int col = 0; col < cols; col++) {
    out[base + col] = llm_exp_approx(in[base + col] - row_max) / denom;
  }
}
