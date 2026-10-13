// T11 probe kernel. mode 0: read (accumulate into a register and write one
// value so the loads are not optimized away); mode 1: write.
__kernel void touch(__global float* buf, __global float* sink, const int n,
                    const int mode) {
  int tid = get_global_id(0), nt = get_global_size(0);
  float acc = 0.0f;
  for (int i = tid; i < n; i += nt) {
    if (mode == 0) acc += buf[i]; else buf[i] = (float)i;
  }
  if (mode == 0 && acc == 12345.0f) sink[0] = acc;
}
