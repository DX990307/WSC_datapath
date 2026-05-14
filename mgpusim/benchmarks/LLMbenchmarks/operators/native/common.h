#ifndef LLM_BENCHMARKS_OPERATORS_COMMON_H
#define LLM_BENCHMARKS_OPERATORS_COMMON_H

#define LLM_LN_WORKGROUP_SIZE 256
#define LLM_SOFTMAX_WORKGROUP_SIZE 256
#define LLM_NEG_INF -3.4028234663852886e+38F

inline float llm_gelu_value(float x) {
  if (x <= -3.0F) {
    return 0.0F;
  }
  if (x >= 3.0F) {
    return x;
  }

  float x3 = x * x * x;
  float gate = 0.5F + 0.197F * x - 0.004F * x3;
  gate = fmin(fmax(gate, 0.0F), 1.0F);
  return x * gate;
}

inline float llm_safe_rsqrt(float x, float epsilon) {
  return rsqrt(fmax(x, 0.0F) + epsilon);
}

inline float llm_exp_approx(float x) {
  x = fmin(fmax(x, -10.0F), 0.0F);
  float x2 = x * x;
  float x3 = x2 * x;
  float x4 = x2 * x2;
  float y = 1.0F + x + 0.5F * x2 + 0.1666666716F * x3 +
            0.0416666679F * x4;
  return fmax(y, 0.000001F);
}

inline int llm_nchw_channel(int flat_index, int channels, int height, int width) {
  int spatial = height * width;
  int within_image = flat_index % (channels * spatial);
  return within_image / spatial;
}

inline float llm_synthetic_value(int row, int col, int hidden, int seed) {
  uint x = (uint)(row * 1315423911u) ^
           (uint)(col * 2654435761u) ^
           (uint)(hidden * 2246822519u) ^
           (uint)seed;
  x ^= x >> 16;
  x *= 2246822519u;
  x ^= x >> 13;
  x *= 3266489917u;
  x ^= x >> 16;
  return ((float)(x & 1023u) / 512.0F) - 1.0F;
}

#endif
