# tier2/shoc_fft — Cooley-Tukey Radix-2 FFT (SHOC FFT)

Benchmarks a radix-2 iterative Cooley-Tukey FFT on **N complex elements** (stored as `float2`). Measures throughput in GFLOPS.

Derived from the [SHOC benchmark suite](https://github.com/vetter/shoc) FFT workload.

## Algorithm

- **N** complex elements stored as `float2` (default: N=1,048,576 = 2²⁰)
- Iterative Cooley-Tukey radix-2 FFT:
  1. **Bit-reversal permutation** — reorder input elements by bit-reversed indices
  2. **log₂(N) butterfly stages** — each stage performs N/2 butterfly operations with twiddle factors
- Each butterfly: complex multiply + add/subtract = 10 FP ops
- GFLOPS = 5 · N · log₂(N) / time\_sec / 10⁹

## Files

| File | Description |
|------|-------------|
| `shoc_fft.hip` | HIP source — bit-reversal + butterfly kernels (one kernel per stage) |
| `shoc_fft.metal` | Metal compute shaders — bit-reversal + butterfly kernels |
| `shoc_fft_metal.mm` | ObjC++ Metal host code |
| `Makefile` | Auto-detecting build (Darwin→metal, ROCm→rocm, else→cuda) |

## Build

```bash
make               # auto-detect platform
make PLATFORM=cuda
make PLATFORM=rocm
make PLATFORM=metal
```

## Run

```bash
./shoc_fft                              # defaults: N=1048576, 5 iterations
./shoc_fft --size 65536                 # smaller FFT
./shoc_fft --size 1048576 --iterations 10
```

## Output

**stdout** — CSV timing row:
```
shoc_fft,1048576,12.3456,42.5000
```

Format: `shoc_fft,<N>,<time_ms>,<GFLOPS>`

**stderr** — human-readable results:
```
Device: AMD Radeon RX 7900 XTX (id=0)
FFT size: 1048576 (2^20)  |  Iterations: 5 warmup + 5 timed

Average time: 12.3456 ms
Performance:  42.5000 GFLOPS
PASS (Parseval)
```

## Verification

- For small sizes (N ≤ 8192): element-wise comparison against CPU reference FFT
- For large sizes: Parseval's theorem check (energy conservation between time and frequency domains)

## Platform Notes

- **CUDA/ROCm**: compiled with `nvcc`/`hipcc` from `shoc_fft.hip`; one kernel launch per butterfly stage
- **Metal**: compiled with `clang++` from `shoc_fft_metal.mm`; the Metal shader (`shoc_fft.metal`) is loaded and compiled at runtime from the same directory as the binary
