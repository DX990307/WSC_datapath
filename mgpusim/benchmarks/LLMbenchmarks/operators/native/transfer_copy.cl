// D2D copy kernel used by the decomposed LLM transfer operator.
//
// The benchmark currently invokes the shared driver copyKernel. This copy keeps
// the transfer kernel source next to the other native LLM operators.

__kernel void copyKernel(__global const float *d_in,
                         __global float *d_out,
                         const int n) {
  int global_id = get_global_id(0);

  if (global_id >= n) {
    return;
  }

  d_out[global_id] = d_in[global_id];
}
