// Minimal proxy kernel used by the KV-cache benchmark.
//
// The checked-in hsaco currently reuses the existing ReLUForward kernel ABI so
// the benchmark can run without requiring a local OpenCL/AMDGPU compiler. The
// operation is intentionally simple: one pass reads and writes a contiguous
// region. The Go benchmark uses multiple launches to model KV-cache decode
// stages such as append, QK-score, and value-reduce.
__kernel void ReLUForward(const int count, __global float* in, __global float* out) {
  int index = get_global_id(0);
  if (index < count) {
    out[index] = in[index] > 0 ? in[index] : 0;
  }
}
