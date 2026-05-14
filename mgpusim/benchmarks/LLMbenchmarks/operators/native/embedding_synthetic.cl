#include "common.h"

__kernel void llm_embedding_synthetic(__global float *out,
                                      int rows,
                                      int hidden,
                                      int seed) {
  int gid = get_global_id(0);
  int n = rows * hidden;
  if (gid >= n) {
    return;
  }

  int row = gid / hidden;
  int col = gid - row * hidden;
  float token = llm_synthetic_value(row, col, hidden, seed);
  float position = llm_synthetic_value(row, col, hidden, seed + 17);
  out[gid] = token + position;
}
