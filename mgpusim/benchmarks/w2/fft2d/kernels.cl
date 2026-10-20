// 2D complex FFT (fp32) of an N x N row-major matrix with explicit global
// transposes, built on the SHOC FFT512 codelet (Volkov & Kazian, from
// gpu_benchmarks tier2/shoc_fft/shoc_fft.hip, also in MGPUSim's
// benchmarks/shoc/fft) for the CuPath wafer (W2 workload set).
//
// N = 512 * m, m in {1, 2, 4, 8}. A length-N row FFT is split as
// N = 512 * m (n = m*n1 + n2, k = k1 + 512*k2):
//   fft2d_fft512   WG (64 threads) per (row, n2): the SHOC FFT512 codelet
//                  over x[row][m*n1 + n2], in place; for m > 1 the outputs
//                  are multiplied by W_N^(n2*k1) (sign = dir).
//   fft2d_radixm   thread per (row, k1), m > 1 only: length-m DFT over n2
//                  (FFT2/FFT4/FFT8 codelets), out[row][k1 + 512*k2],
//                  times `scale` (1/m for the inverse).
//   fft2d_transpose  32 x 32 tiles through LDS (all-to-all across GPMs).
// Forward 2D = rows, transpose, rows, transpose; inverse the same with
// dir = +1 (IFFT512 scales by 1/512 as in SHOC).
//
// Simulator rules (T17): no atomics, no private arrays (the codelet's
// a[8] is fully unrolled into registers; SHOC's reversed[] tables are
// replaced by REV8), only the implemented DS opcodes (re/im kept in
// separate LDS planes, no 64-bit LDS stores). The emulator has no
// v_sin/v_cos, so twiddles W_N^r = exp(dir * 2*pi*i * r / N) come from
// tw(): octant reduction on the integer r and Taylor polynomials on
// [0, pi/4] (error < 1e-7).

#define T float
#define T2 float2

#define SQRT1_2F 0.70710678118654752440f
#define TWO_PI_F 6.28318530717958647692f

// Row offsets are 32 x 32 -> 64-bit multiplies, not (ulong)x << logn: the
// emulator reads the 32-bit shift operand of v_lshlrev_b64 as 64 bits (the
// next SGPR becomes the high half) and Go's << then yields 0.
#define UMUL(a, b) ((ulong)(uint)(a) * (ulong)(uint)(b))

#define REV8(i) ((((i) & 1) << 2) | ((i) & 2) | (((i) >> 2) & 1))
#define REV4(i) ((((i) & 1) << 1) | (((i) >> 1) & 1))

inline T2 cmul(T2 a, T2 b) { return (T2)(a.x * b.x - a.y * b.y, a.x * b.y + a.y * b.x); }
inline T2 cmulf(T2 a, T b) { return (T2)(b * a.x, b * a.y); }
inline T2 cadd(T2 a, T2 b) { return (T2)(a.x + b.x, a.y + b.y); }
inline T2 csub(T2 a, T2 b) { return (T2)(a.x - b.x, a.y - b.y); }

// exp(sign * 2 pi i r / n), n = 2^logn >= 8, r any non-negative integer
inline T2 tw(uint r, int logn, float sign) {
  const uint n = 1u << logn;
  r &= n - 1;
  const uint q4 = n >> 2;
  const uint q = r >> (logn - 2);
  uint t = r & (q4 - 1);
  const int refl = t > (n >> 3);
  t = refl ? q4 - t : t;
  // 2^-logn built from the exponent bits (no fp division)
  const float th = (float)t * (TWO_PI_F * as_float((uint)(127 - logn) << 23));
  const float x2 = th * th;
  float s = th * (1.0f + x2 * (-1.0f / 6.0f + x2 * (1.0f / 120.0f +
            x2 * (-1.0f / 5040.0f + x2 * (1.0f / 362880.0f)))));
  float c = 1.0f + x2 * (-0.5f + x2 * (1.0f / 24.0f + x2 * (-1.0f / 720.0f +
            x2 * (1.0f / 40320.0f))));
  float cc = refl ? s : c;
  float ss = refl ? c : s;
  // rotate by q * 90 degrees: (cc, ss), (-ss, cc), (-cc, -ss), (ss, -cc).
  // Signs are applied with an integer xor: a select of a negated value
  // compiles to v_cndmask_b32_e64 with a neg modifier, which the emulator
  // applies as an integer negation (wrong result).
  const int odd = (q == 1) || (q == 3);  // not q & 1: no v_bfe_u32
  const float rc0 = odd ? ss : cc;
  const float rs0 = odd ? cc : ss;
  const uint sgc = (q == 1 || q == 2) ? 0x80000000u : 0u;
  const uint sgs = (q >= 2) ? 0x80000000u : 0u;
  const float rc = as_float(as_uint(rc0) ^ sgc);
  const float rs = as_float(as_uint(rs0) ^ sgs);
  return (T2)(rc, sign * rs);
}

#define exp_1_8 (T2)(1, -1)
#define exp_1_4 (T2)(0, -1)
#define exp_3_8 (T2)(-1, -1)
#define iexp_1_8 (T2)(1, 1)
#define iexp_1_4 (T2)(0, 1)
#define iexp_3_8 (T2)(-1, 1)

#define FFT2(a0, a1)            \
  {                             \
    T2 c0 = *(a0);              \
    *(a0) = cadd(c0, *(a1));    \
    *(a1) = csub(c0, *(a1));    \
  }

#define FFT4(a0, a1, a2, a3)         \
  {                                  \
    FFT2(a0, a2);                    \
    FFT2(a1, a3);                    \
    *(a3) = cmul(*(a3), exp_1_4);    \
    FFT2(a0, a1);                    \
    FFT2(a2, a3);                    \
  }

#define IFFT4(a0, a1, a2, a3)        \
  {                                  \
    FFT2(a0, a2);                    \
    FFT2(a1, a3);                    \
    *(a3) = cmul(*(a3), iexp_1_4);   \
    FFT2(a0, a1);                    \
    FFT2(a2, a3);                    \
  }

#define FFT8(a)                                              \
  {                                                          \
    FFT2(&a[0], &a[4]);                                      \
    FFT2(&a[1], &a[5]);                                      \
    FFT2(&a[2], &a[6]);                                      \
    FFT2(&a[3], &a[7]);                                      \
    a[5] = cmulf(cmul(a[5], exp_1_8), SQRT1_2F);          \
    a[6] = cmul(a[6], exp_1_4);                              \
    a[7] = cmulf(cmul(a[7], exp_3_8), SQRT1_2F);          \
    FFT4(&a[0], &a[1], &a[2], &a[3]);                        \
    FFT4(&a[4], &a[5], &a[6], &a[7]);                        \
  }

#define IFFT8(a)                                             \
  {                                                          \
    FFT2(&a[0], &a[4]);                                      \
    FFT2(&a[1], &a[5]);                                      \
    FFT2(&a[2], &a[6]);                                      \
    FFT2(&a[3], &a[7]);                                      \
    a[5] = cmulf(cmul(a[5], iexp_1_8), SQRT1_2F);         \
    a[6] = cmul(a[6], iexp_1_4);                             \
    a[7] = cmulf(cmul(a[7], iexp_3_8), SQRT1_2F);         \
    IFFT4(&a[0], &a[1], &a[2], &a[3]);                       \
    IFFT4(&a[4], &a[5], &a[6], &a[7]);                       \
  }

// twiddle8(a, i, n): a[j] *= exp(sign 2 pi i rev(j) i / n), n = 512 or 64
#define TWIDDLE8(a, i, logn, sign)                                   \
  {                                                                  \
    _Pragma("unroll") for (int j = 1; j < 8; j++) {                  \
      a[j] = cmul(a[j], tw((uint)(REV8(j) * (i)), logn, sign));     \
    }                                                                \
  }

#define STOREX8(a, ptr, sx) \
  { _Pragma("unroll") for (int i = 0; i < 8; i++) (ptr)[i * (sx)] = a[REV8(i)].x; }
#define STOREY8(a, ptr, sx) \
  { _Pragma("unroll") for (int i = 0; i < 8; i++) (ptr)[i * (sx)] = a[REV8(i)].y; }
#define LOADX8(a, ptr, sx) \
  { _Pragma("unroll") for (int i = 0; i < 8; i++) a[i].x = (ptr)[i * (sx)]; }
#define LOADY8(a, ptr, sx) \
  { _Pragma("unroll") for (int i = 0; i < 8; i++) a[i].y = (ptr)[i * (sx)]; }

#define TRANSPOSE(a, s, ds, l, dl, sync)                                 \
  {                                                                      \
    STOREX8(a, s, ds); if ((sync) & 8) barrier(CLK_LOCAL_MEM_FENCE);     \
    LOADX8(a, l, dl);  if ((sync) & 4) barrier(CLK_LOCAL_MEM_FENCE);     \
    STOREY8(a, s, ds); if ((sync) & 2) barrier(CLK_LOCAL_MEM_FENCE);     \
    LOADY8(a, l, dl);  if ((sync) & 1) barrier(CLK_LOCAL_MEM_FENCE);     \
  }

// dir = -1 forward, +1 inverse (inverse scales by 1/512 like SHOC IFFT512)
__kernel __attribute__((reqd_work_group_size(64, 1, 1)))
void fft2d_fft512(__global T2 *work, const int logn, const int m,
                  const float dir) {
  __local T smem[8 * 8 * 9];
  const int tid = get_local_id(0);
  const int g = get_group_id(0);
  const int row = g / m;
  const int n2 = g - row * m;
  const int hi = tid >> 3;
  const int lo = tid & 7;
  __global T2 *x = work + UMUL(row, 1u << logn) + (uint)n2;
  T2 a[8];

#pragma unroll
  for (int i = 0; i < 8; i++) {
    a[i] = x[(uint)((tid + 64 * i) * m)];
  }
  if (dir < 0.0f) {
    FFT8(a);
  } else {
    IFFT8(a);
  }
  TWIDDLE8(a, tid, 9, dir);
  TRANSPOSE(a, &smem[hi * 8 + lo], 66, &smem[lo * 66 + hi], 8, 0xf);
  if (dir < 0.0f) {
    FFT8(a);
  } else {
    IFFT8(a);
  }
  TWIDDLE8(a, hi * 8, 9, dir);
  TRANSPOSE(a, &smem[hi * 8 + lo], 8 * 9, &smem[hi * 8 * 9 + lo], 8, 0xE);
  if (dir < 0.0f) {
    FFT8(a);
  } else {
    IFFT8(a);
  }
  const float scale = dir < 0.0f ? 1.0f : 1.0f / 512.0f;
#pragma unroll
  for (int i = 0; i < 8; i++) {
    const int k1 = i * 64 + tid;
    T2 v = cmulf(a[REV8(i)], scale);
    if (m > 1) {
      v = cmul(v, tw((uint)(n2 * k1), logn, dir));
    }
    x[(uint)(k1 * m)] = v;
  }
}

// thread per (row, k1); m in {2, 4, 8}
__kernel __attribute__((reqd_work_group_size(256, 1, 1)))
void fft2d_radixm(__global const T2 *in, __global T2 *out, const int logn,
                  const int m, const float dir, const float scale) {
  const uint gid = get_global_id(0);
  const uint row = gid >> 9;
  const uint k1 = gid & 511;
  __global const T2 *x = in + UMUL(row, 1u << logn) + k1 * (uint)m;
  __global T2 *y = out + UMUL(row, 1u << logn) + k1;
  T2 a[8];
  if (m == 8) {
#pragma unroll
    for (int i = 0; i < 8; i++) a[i] = x[i];
    if (dir < 0.0f) {
      FFT8(a);
    } else {
      IFFT8(a);
    }
#pragma unroll
    for (int i = 0; i < 8; i++) y[512 * i] = cmulf(a[REV8(i)], scale);
  } else if (m == 4) {
#pragma unroll
    for (int i = 0; i < 4; i++) a[i] = x[i];
    if (dir < 0.0f) {
      FFT4(&a[0], &a[1], &a[2], &a[3]);
    } else {
      IFFT4(&a[0], &a[1], &a[2], &a[3]);
    }
#pragma unroll
    for (int i = 0; i < 4; i++) y[512 * i] = cmulf(a[REV4(i)], scale);
  } else {
    a[0] = x[0];
    a[1] = x[1];
    FFT2(&a[0], &a[1]);
    y[0] = cmulf(a[0], scale);
    y[512] = cmulf(a[1], scale);
  }
}

// out = in^T, N x N, 32 x 32 tiles, 256 threads (32 x 8)
#define TILE 32
#define TPAD 33
__kernel __attribute__((reqd_work_group_size(256, 1, 1)))
void fft2d_transpose(__global const T2 *in, __global T2 *out, const int logn) {
  __local T re[TILE * TPAD];
  __local T im[TILE * TPAD];
  const int n = 1 << logn;
  const int tiles = n / TILE;
  const int g = get_group_id(0);
  const int by = g / tiles;
  const int bx = g - by * tiles;
  const int lid = get_local_id(0);
  const int tx = lid & 31;
  const int ty = lid >> 5;
#pragma unroll
  for (int k = 0; k < TILE; k += 8) {
    T2 v = in[UMUL(by * TILE + ty + k, n) + (uint)(bx * TILE + tx)];
    re[(ty + k) * TPAD + tx] = v.x;
    im[(ty + k) * TPAD + tx] = v.y;
  }
  barrier(CLK_LOCAL_MEM_FENCE);
#pragma unroll
  for (int k = 0; k < TILE; k += 8) {
    T2 v;
    v.x = re[tx * TPAD + ty + k];
    v.y = im[tx * TPAD + ty + k];
    out[UMUL(bx * TILE + ty + k, n) + (uint)(by * TILE + tx)] = v;
  }
}

