# tier2/shoc_gemm — Dense Matrix Multiply (SHOC GEMM)

Benchmarks dense matrix multiplication: **C = α·A·B + β·C** for square N×N single-precision matrices. Measures peak compute throughput in GFLOPS.

Derived from the [SHOC benchmark suite](https://github.com/vetter/shoc) GEMM workload.

## Algorithm

- Square N×N single-precision matrix multiply
- Tiled/shared-memory kernel: 16×16 thread tiles load sub-matrices into threadgroup memory to improve cache reuse
- GFLOPS = 2·N³ / time\_sec / 1e9

## Files

| File | Description |
|------|-------------|
| `shoc_gemm.hip` | HIP source — tiled GEMM kernel + self-contained utilities |
| `shoc_gemm.metal` | Metal compute shader — tiled GEMM using threadgroup memory |
| `shoc_gemm_metal.mm` | ObjC++ Metal host code |
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
./shoc_gemm                     # defaults: 512×512, 20 iterations
./shoc_gemm --size 1024
./shoc_gemm --size 256 --iterations 50
```

## Output

**stdout** — CSV timing row:
```
kernel_name,problem_size,iterations,avg_ms,min_ms,max_ms,stddev_ms
gemm,512,20,12.3456,12.1000,12.9000,0.1234
```

**stderr** — human-readable results:
```
Device: AMD Radeon RX 7900 XTX (id=0)
Matrix size: 512×512  |  Iterations: 20

Performance: 8765.43 GFLOPS  (avg 12.3456 ms)
PASS
```

## Platform Notes

- **CUDA/ROCm**: compiled with `nvcc`/`hipcc` from `shoc_gemm.hip`
- **Metal**: compiled with `clang++` from `shoc_gemm_metal.mm`; the Metal shader (`shoc_gemm.metal`) is loaded and compiled at runtime from the same directory as the binary
