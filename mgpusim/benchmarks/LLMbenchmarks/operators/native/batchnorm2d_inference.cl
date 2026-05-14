#include "common.h"

__kernel void llm_batchnorm2d_inference(__global float *out,
                                        const __global float *in,
                                        const __global float *mean,
                                        const __global float *variance,
                                        const __global float *gamma,
                                        const __global float *beta,
                                        int n,
                                        int channels,
                                        int height,
                                        int width,
                                        float epsilon) {
  int gid = get_global_id(0);
  if (gid >= n) {
    return;
  }

  int channel = llm_nchw_channel(gid, channels, height, width);
  float inv_std = llm_safe_rsqrt(variance[channel], epsilon);
  out[gid] = (in[gid] - mean[channel]) * inv_std * gamma[channel] + beta[channel];
}
