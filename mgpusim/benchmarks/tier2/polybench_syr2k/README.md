# tier2/polybench_syr2k — Symmetric Rank-2k Update (PolyBench SYR2K)

Benchmarks the symmetric rank-2k matrix operation: **C = α·A·Bᵀ + α·B·Aᵀ + β·C** where A and B are N×M single-precision matrices and C is an N×N symmetric output matrix. Measures peak compute throughput in GFLOPS.

Derived from the [PolyBench/GPU benchmark suite](https://sourceforge.net/projects/polybench/) SYR2K workload (BLAS Level 3).

## Algorithm

- Symmetric rank-2k update combining two transposed matrix products
- `C[i][j] = alpha * sum_k(A[i][k]*B[j][k]) + alpha * sum_k(B[i][k]*A[j][k]) + beta * C[i][j]`
- Default parameters: N=1024, M=1024, alpha=1.5, beta=1.2
- Tiled shared-memory kernel: 16×16 thread tiles with 4 shared-memory arrays (A\_row, B\_row, A\_col, B\_col) for efficient data reuse
- GFLOPS = 4·N²·M / time\_sec / 1e9

## Files

| File | Description |
|------|-------------|
| `polybench_syr2k.cu` | CUDA source — tiled SYR2K kernel + self-contained utilities |
| `polybench_syr2k.hip` | HIP source — tiled SYR2K kernel with HIP/CUDA compat layer |
| `polybench_syr2k.metal` | Metal compute shader — tiled SYR2K using threadgroup memory |
| `polybench_syr2k_metal.mm` | ObjC++ Metal host code |
| `Makefile` | Auto-detecting build (Darwin→metal, ROCm→rocm, else→cuda) |
| `params.json` | Parameter specification for the benchmark harness |

## Build

```bash
make               # auto-detect platform
make PLATFORM=cuda
make PLATFORM=rocm
make PLATFORM=metal
```

## Run

```bash
./polybench_syr2k                     # defaults: N=1024, M=1024, 5 iterations
./polybench_syr2k --size 2048
./polybench_syr2k --size 512 --inner_size 256 --iterations 10
```

Environment variables override CLI flags:
```bash
BENCH_PARAM_matrix_size=2048 BENCH_PARAM_inner_size=512 ./polybench_syr2k
```

## Output

**stdout** — JSON-lines:
```json
{"type":"kernel","name":"syr2k_kernel","time_ms":12.345678,"params":{"matrix_size":1024,"inner_size":1024,"iterations":5,"block_size":256,"alpha":1.50,"beta":1.20}}
{"type":"summary","total_time_ms":12.345678,"metrics":[{"name":"gflops","value":345.67}]}
```

**stderr** — human-readable results:
```
Device: NVIDIA A100 (id=0)
SYR2K: N=1024, M=1024  |  alpha=1.5  beta=1.2  |  Iterations: 5

Performance: 345.67 GFLOPS  (avg 12.3457 ms, min 12.1000 ms, max 12.6000 ms, stddev 0.1823 ms)
```

## Platform Notes

- **CUDA**: compiled with `nvcc` from `polybench_syr2k.cu`
- **ROCm/HIP**: compiled with `hipcc` from `polybench_syr2k.hip`; includes CUDA compat layer for `nvcc` fallback
- **Metal**: compiled with `clang++` from `polybench_syr2k_metal.mm`; the Metal shader (`polybench_syr2k.metal`) is loaded and compiled at runtime from the same directory as the binary

## Differences from polybench_gemm

| Property | polybench_gemm | polybench_syr2k |
|----------|---------------|-----------------|
| Operation | C = α·A·B + β·C | C = α·A·Bᵀ + α·B·Aᵀ + β·C |
| Matrix shapes | A,B,C all N×N | A,B are N×M; C is N×N |
| Kernel complexity | 1 matrix product | 2 transposed products |
| Shared memory tiles | 2 (sA, sB) | 4 (sA\_row, sB\_row, sA\_col, sB\_col) |
| FLOPs formula | 2·N³ | 4·N²·M |
