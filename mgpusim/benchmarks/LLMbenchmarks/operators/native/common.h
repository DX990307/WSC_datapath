#ifndef LLM_BENCHMARKS_OPERATORS_COMMON_H
#define LLM_BENCHMARKS_OPERATORS_COMMON_H

#define LLM_LN_WORKGROUP_SIZE 256
#define LLM_SOFTMAX_WORKGROUP_SIZE 256
#define LLM_NEG_INF -3.4028234663852886e+38F
#define LLM_GELU_TANH_SCALE 0.7978845608028654F

inline float llm_tanh_value(float x) {
  float x2 = x * x;
  float y = x * (27.0F + x2) / (27.0F + 9.0F * x2);
  return fmin(fmax(y, -1.0F), 1.0F);
}

inline float llm_recip_value(float x) {
  float clipped = fmax(x, 1.0e-20F);
  float inv_sqrt = rsqrt(clipped);
  return inv_sqrt * inv_sqrt;
}

inline float llm_gelu_value(float x) {
  float x3 = x * x * x;
  float inner = LLM_GELU_TANH_SCALE * (x + 0.044715F * x3);
  return 0.5F * x * (1.0F + llm_tanh_value(inner));
}

inline float llm_safe_rsqrt(float x, float epsilon) {
  float clipped = fmax(x, 0.0F);
  return rsqrt(clipped + epsilon);
}

inline float llm_exp_value(float x) {
  float y = 0.0F - x;
  y = fmax(y, 0.0F);
  y = fmin(y, 20.0F);
  float y2 = y * y;
  float y4 = y2 * y2;
  float y8 = y4 * y4;
  float denom =
      1.0F + y + 0.5F * y2 + 0.1666666716F * y2 * y +
      0.0416666679F * y4 + 0.0083333338F * y4 * y +
      0.0013888889F * y4 * y2 + 0.0001984127F * y4 * y2 * y +
      0.0000248016F * y8;
  return llm_recip_value(denom);
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
