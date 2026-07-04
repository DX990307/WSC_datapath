# tier2/polybench_2dconv — PolyBench 2D Convolution

GPU benchmark for PolyBench 2D Convolution. Applies a fixed 3×3 kernel to an
N×N input matrix, measuring throughput in GFLOPS.

## Algorithm

Each GPU thread computes one interior output element:

```
B[i][j] = c[0][0]*A[i-1][j-1] + c[0][1]*A[i-1][j] + c[0][2]*A[i-1][j+1]
         + c[1][0]*A[i  ][j-1] + c[1][1]*A[i  ][j] + c[1][2]*A[i  ][j+1]
         + c[2][0]*A[i+1][j-1] + c[2][1]*A[i+1][j] + c[2][2]*A[i+1][j+1]
         for 1 <= i < N-1, 1 <= j < N-1
```

Fixed PolyBench 3×3 kernel coefficients:

```
c = {{0.8, 0.2, 0.3},
     {0.2, 0.7, 0.4},
     {0.1, 0.2, 0.5}}
```

**GFLOPS** = `2 × 9 × (N−2)² / time_s / 1e9`

## Files

| File | Description |
|------|-------------|
| `polybench_2dconv.hip` | HIP kernel + host (CUDA/ROCm, self-contained) |
| `polybench_2dconv.metal` | Metal compute shader |
| `polybench_2dconv_metal.mm` | ObjC++ Metal host |
| `Makefile` | Platform auto-detect build |

## Build

### Auto-detect (recommended)

```bash
make
```

Platform is auto-detected:
- **Darwin** → `metal`
- **Linux + `/opt/rocm`** → `rocm`
- **Linux, no ROCm** → `cuda`

### Explicit platform

```bash
make PLATFORM=cuda    # NVIDIA (nvcc)
make PLATFORM=rocm    # AMD ROCm (hipcc)
make PLATFORM=metal   # Apple Metal (clang++)
```

## Run

```bash
./polybench_2dconv [--size N] [--iterations I]
```

| Option | Default | Description |
|--------|---------|-------------|
| `--size N` | 2048 | Matrix dimension N×N |
| `--iterations I` | 5 | Number of timed iterations |

## Output

**stdout** (CSV):
```
kernel_name,problem_size,iterations,avg_ms,min_ms,max_ms,stddev_ms
polybench_2dconv,2048,5,3.2100,3.1800,3.2400,0.0240
```

**stderr**:
```
Device: NVIDIA A100 (id=0)
Matrix: 2048×2048  |  Iterations: 5

GFLOPS: 23.14  (avg 3.2100 ms)
```

## Notes

- Border elements (row/col 0 and N−1) are skipped, matching the PolyBench reference.
- Input is initialized with `rand() % 100 / 10.0f` using seed 42 (reproducible).
- Metal: shader source is compiled at runtime from `polybench_2dconv.metal`
  (must reside alongside the binary).
- The benchmark runs one warmup pass before the timed iterations.
