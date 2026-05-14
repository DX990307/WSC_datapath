#include "common.h"

__kernel void llm_apply_causal_mask(__global float *scores,
                                    int rows,
                                    int seq_len,
                                    int batch_size,
                                    float mask_value) {
  int key = get_global_id(0);
  int query = get_global_id(1);

  if (key >= rows || query >= rows) {
    return;
  }

  int query_batch = query / seq_len;
  int key_batch = key / seq_len;
  int query_pos = query - query_batch * seq_len;
  int key_pos = key - key_batch * seq_len;

  if (query_batch >= batch_size || key_batch >= batch_size ||
      query_batch != key_batch || key_pos > query_pos) {
    scores[query * rows + key] = mask_value;
  }
}
