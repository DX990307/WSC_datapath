# tier2/parboil_sgemm — Tiled Single-Precision GEMM (Parboil)

Benchmarks tiled single-precision matrix multiplication: **C = α·A·B + β·C** for square N×N matrices. Measures compute throughput in GFLOPS.

Derived from the [Parboil benchmark suite](http://impact.crhc.illinois.edu/parboil/parboil.aspx) SGEMM workload.

## Algorithm

- Square N×N single-precision matrix multiply: **C = alpha·A·B + beta·C**
- Default: N=1024, alpha=1.5, beta=1.2
- Tiled kernel: 16×16 thread tiles load sub-matrices into shared/threadgroup memory to improve cache reuse
- 5 timed iterations (+ 1 warmup), reports average
- GFLOPS = 2·N³ / time\_sec / 1e9

## Files

| File | Description |
|------|-------------|
| `parboil_sgemm.hip` | HIP/CUDA source — tiled SGEMM kernel + self-contained utilities |
| `parboil_sgemm.metal` | Metal compute shader — tiled SGEMM using threadgroup memory |
| `parboil_sgemm_metal.mm` | ObjC++ Metal host code |
| `Makefile` | Auto-detecting build (Darwin→metal, ROCm→rocm, else→cuda) |
| `README.md` | This file |

## Build

```bash
make               # auto-detect platform
make PLATFORM=cuda
make PLATFORM=rocm
make PLATFORM=metal
```

## Run

```bash
./parboil_sgemm                      # defaults: 1024×1024, 5 iterations
./parboil_sgemm --size 2048
./parboil_sgemm --size 512 --iterations 10
```

## Output

**stdout** — CSV result:
```
parboil_sgemm,1024,4321.56
```
Format: `parboil_sgemm,<N>,<GFLOPS>`

**stderr** — human-readable results:
```
Device: AMD Radeon RX 7900 XTX (id=0)
Matrix size: 1024×1024  |  alpha=1.5  beta=1.2  |  Iterations: 5

Performance: 4321.56 GFLOPS  (avg 0.4950 ms, min 0.4900 ms, max 0.5050 ms)
Skipping verification for large size
```

## Platform Notes

- **CUDA**: compiled with `nvcc` from `parboil_sgemm.hip` (uses `-x cu` flag)
- **ROCm**: compiled with `hipcc` from `parboil_sgemm.hip`
- **Metal**: compiled with `clang++` from `parboil_sgemm_metal.mm`; the Metal shader (`parboil_sgemm.metal`) is loaded and compiled at runtime from the same directory as the binary
- Correctness verification runs only for N≤256 (CPU reference is O(N³))
