#include "common.h"

__kernel void llm_embedding_synthetic(__global float *out,
                                      const __global int *token_ids,
                                      const __global float *token_table,
                                      const __global float *position_table,
                                      int rows,
                                      int hidden,
                                      int vocab_size) {
  int gid = get_global_id(0);
  int n = rows * hidden;
  if (gid >= n) {
    return;
  }

  int row = gid / hidden;
  int col = gid - row * hidden;
  int token = token_ids[row] % vocab_size;
  if (token < 0) {
    token += vocab_size;
  }

  out[gid] = token_table[token * hidden + col] +
             position_table[row * hidden + col];
}
