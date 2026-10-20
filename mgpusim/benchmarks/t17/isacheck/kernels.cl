// End-to-end check of the instructions added for T17 (v_mad_i64_i32,
// v_alignbit_b32, s_ashr_i64, s_addk_i32, s_cmpk_eq_i32, s_load_dwordx16).
// The source is written so that clang emits them; opcheck/objdump confirm.
// One work-item per element; results are compared on the host.

__kernel void isa_check(__global const int *a,      // 0
                        __global const int *b,      // 8
                        __global const long *c,     // 16
                        __global const uint *hi,    // 24
                        __global const uint *lo,    // 32
                        __global long *out_mad,     // 40
                        __global uint *out_align,   // 48
                        __global int *out_scalar,   // 56  (64 contiguous
                                                    //  used kernarg bytes:
                                                    //  s_load_dwordx16)
                        const int k,
                        const int n,
                        const long kk) {
  const int i = get_global_id(0);
  if (i >= n) {
    return;
  }
  // signed 32x32+64 -> v_mad_i64_i32
  out_mad[i] = (long)a[i] * (long)b[i] + c[i];
  // 64-bit funnel shift -> v_alignbit_b32
  const ulong v = ((ulong)hi[i] << 32) | lo[i];
  out_align[i] = (uint)(v >> (b[i] & 31));
  // uniform scalar math: s_ashr_i64 (64-bit arithmetic shift of a uniform
  // value), s_addk_i32 (16-bit constant added in a uniform loop),
  // s_cmpk_eq_i32 (compare with a 16-bit constant)
  int s = (int)(kk >> 35);
  for (int j = 0; j < k; ++j) {
    s = (s ^ j) + 0x2100;
  }
  if (k == 0x1234) {
    s += 7;
  }
  out_scalar[i] = s + i;
}
