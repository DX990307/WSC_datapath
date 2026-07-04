# tier2/polybench_gemm — Dense Matrix Multiply (PolyBench GEMM)

Benchmarks dense matrix multiplication: **C = α·A·B + β·C** for square N×N single-precision matrices. Measures peak compute throughput in GFLOPS.

Derived from the [PolyBench/GPU benchmark suite](https://sourceforge.net/projects/polybench/) GEMM workload.

## Algorithm

- Square N×N single-precision matrix multiply with scaling: `C = alpha * A * B + beta * C`
- Default parameters: N=1024, alpha=1.5, beta=1.2 (PolyBench default values)
- Tiled/shared-memory kernel: 16×16 thread tiles load sub-matrices into threadgroup memory to improve cache reuse
- GFLOPS = 2·N³ / time\_sec / 1e9

## Files

| File | Description |
|------|-------------|
| `polybench_gemm.hip` | HIP source — tiled GEMM kernel + self-contained utilities |
| `polybench_gemm.metal` | Metal compute shader — tiled GEMM using threadgroup memory |
| `polybench_gemm_metal.mm` | ObjC++ Metal host code |
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
./polybench_gemm                     # defaults: 1024×1024, 5 iterations
./polybench_gemm --size 2048
./polybench_gemm --size 512 --iterations 10
```

## Output

**stdout** — CSV row with GFLOPS:
```
polybench_gemm,1024,8765.43
```

**stderr** — human-readable results:
```
Device: AMD Radeon RX 7900 XTX (id=0)
Matrix size: 1024×1024  |  alpha=1.5  beta=1.2  |  Iterations: 5

Performance: 8765.43 GFLOPS  (avg 0.2456 ms, min 0.2400 ms, max 0.2600 ms, stddev 0.0082 ms)
```

## Platform Notes

- **CUDA/ROCm**: compiled with `nvcc`/`hipcc` from `polybench_gemm.hip`
- **Metal**: compiled with `clang++` from `polybench_gemm_metal.mm`; the Metal shader (`polybench_gemm.metal`) is loaded and compiled at runtime from the same directory as the binary

## Differences from shoc_gemm

| Property | shoc_gemm | polybench_gemm |
|----------|-----------|----------------|
| Default N | 512 | 1024 |
| alpha | 1.0 | 1.5 |
| beta | 0.0 | 1.2 |
| Iterations | 20 | 5 |
| CSV format | timing stats | GFLOPS |
