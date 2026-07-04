# tier2/polybench_3mm — Three Chained Matrix Multiplications (PolyBench 3mm)

Benchmarks three chained matrix multiplications using GPU compute:

```
E = A * B
F = C * D
G = E * F
```

All matrices are square N×N single-precision floats (default N=256). Reports combined throughput in GFLOPS.

Derived from the [PolyBench/GPU benchmark suite](https://sourceforge.net/projects/polybench/) 3mm workload.

## Algorithm

- Three sequential GPU kernel launches per iteration
- Each kernel: one thread per output element, simple dot-product inner loop (no tiling)
- Default size: N=256 (all matrices 256×256)
- GFLOPS = (2·N³ + 2·N³ + 2·N³) / time\_sec / 1e9 = 6·N³ / time\_sec / 1e9

## Files

| File | Description |
|------|-------------|
| `polybench_3mm.hip` | HIP source — three matrix-multiply kernels + self-contained utilities |
| `polybench_3mm.metal` | Metal compute shaders — three kernels (mm3\_kernel1/2/3) |
| `polybench_3mm_metal.mm` | ObjC++ Metal host code |
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
./polybench_3mm                       # defaults: 256×256, 5 iterations
./polybench_3mm --size 512
./polybench_3mm --size 256 --iterations 10
```

## Output

**stdout** — CSV row with GFLOPS:
```
polybench_3mm,256,125.34
```

**stderr** — human-readable results:
```
Device: AMD Radeon RX 7900 XTX (id=0)
Matrix size: 256×256 (all)  |  Iterations: 5

Performance: 125.34 GFLOPS  (avg 2.1234 ms, min 2.1100 ms, max 2.1500 ms, stddev 0.0150 ms)
```

## Platform Notes

- **CUDA/ROCm**: compiled with `nvcc`/`hipcc` from `polybench_3mm.hip`
- **Metal**: compiled with `clang++` from `polybench_3mm_metal.mm`; the Metal shader (`polybench_3mm.metal`) is loaded and compiled at runtime from the same directory as the binary

## Matrix Dimensions

With `--size N`:

| Matrix | Rows | Cols |
|--------|------|------|
| A      | N    | N    |
| B      | N    | N    |
| E=A*B  | N    | N    |
| C      | N    | N    |
| D      | N    | N    |
| F=C*D  | N    | N    |
| G=E*F  | N    | N    |
