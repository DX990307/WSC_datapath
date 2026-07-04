# tier2/polybench_2mm — Two Matrix Multiplications (PolyBench 2MM)

Benchmarks two consecutive dense matrix multiplications:
- **D = α·A·B + β·D**
- **E = α·C·D + β·E**

for square N×N single-precision matrices. Measures total compute throughput in GFLOPS.

Derived from the [PolyBench/GPU benchmark suite](https://sourceforge.net/projects/polybench/) 2MM workload.

## Algorithm

- Two back-to-back tiled GEMM operations on NxN matrices
- Default parameters: N=1024, alpha=1.5, beta=1.2 (PolyBench default values)
- Tiled/shared-memory kernel: 16×16 thread tiles load sub-matrices into shared memory
- Total FLOPs: 4·N³ (two matrix multiplies, each 2·N³)

## Files

| File | Description |
|------|-------------|
| `polybench_2mm.cu` | CUDA source — tiled 2MM kernel |
| `polybench_2mm.hip` | HIP source — tiled 2MM kernel with HIP/CUDA compat |
| `polybench_2mm.metal` | Metal compute shader — tiled MM using threadgroup memory |
| `polybench_2mm_metal.mm` | ObjC++ Metal host code |
| `Makefile` | Auto-detecting build (Darwin→metal, ROCm→rocm, else→cuda) |
| `params.json` | Parameter specification |

## Build

```bash
make               # auto-detect platform
make PLATFORM=cuda
make PLATFORM=rocm
make PLATFORM=metal
```

## Run

```bash
./polybench_2mm                     # defaults: 1024×1024, 5 iterations
./polybench_2mm --size 2048
./polybench_2mm --size 512 --iterations 10
BENCH_PARAM_size=2048 ./polybench_2mm
```

## Output

**stdout** — JSON-lines:
```json
{"type":"kernel","name":"mm_kernel_1","time_ms":1.234,"params":{"size":1024,...}}
{"type":"kernel","name":"mm_kernel_2","time_ms":1.234,"params":{"size":1024,...}}
{"type":"summary","total_time_ms":2.468,"metrics":[{"name":"gflops","value":1234.56}]}
```

**stderr** — human-readable device info and timing details.

## Differences from polybench_gemm

| Property | polybench_gemm | polybench_2mm |
|----------|---------------|---------------|
| Operation | C = α·A·B + β·C | D = α·A·B + β·D, E = α·C·D + β·E |
| Matrices | 3 (A, B, C) | 5 (A, B, C, D, E) |
| Kernels | 1 | 2 (sequential) |
| FLOPs | 2·N³ | 4·N³ |
