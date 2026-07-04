# polybench_mvt

PolyBench MVT benchmark: matrix-vector product and transpose.

Computes:
- **x1 = A · y1** — matrix-vector product
- **x2 = Aᵀ · y2** — transpose matrix-vector product

where A is an N×N matrix and x1, x2, y1, y2 are N-vectors.

## Algorithm

Two GPU kernels are executed per iteration:

1. **mvt_kernel1** — `x1 = A · y1`  
   Each thread computes one row: `x1[i] = Σⱼ A[i,j] * y1[j]`

2. **mvt_kernel2** — `x2 = Aᵀ · y2`  
   Each thread computes one column: `x2[j] = Σᵢ A[i,j] * y2[i]`

## Metric

**GB/s** (memory bandwidth) — matrix A is read twice per iteration (once per kernel):

```
GB/s = (2 * N * N * 4 bytes) / time_s / 1e9
```

## Output

```
mvt,<N>,<time_ms>,<GB/s>
```

Example:
```
mvt,4096,12.3456,270.15
```

## Build

```bash
# Auto-detect platform (Darwin → metal, /opt/rocm exists → rocm, else → cuda)
make

# Explicit platform
make PLATFORM=cuda
make PLATFORM=rocm
make PLATFORM=metal
```

## Run

```bash
# Default: N=4096, 5 iterations
./polybench_mvt

# Custom size
./polybench_mvt --n 2048

# More iterations
./polybench_mvt --n 4096 --iterations 10
```

## Parameters

| Flag | Default | Description |
|------|---------|-------------|
| `--n N` | 4096 | Matrix dimension (N×N) |
| `--iterations I` | 5 | Number of timed iterations (plus 1 warmup) |

## Platform Notes

### CUDA / ROCm (HIP)

- Source: `polybench_mvt.hip`  
- Block size: 256 threads  
- Grid: `ceil(N/256)` for both kernels  
- Requires: `nvcc` (CUDA) or `hipcc` (ROCm)

### Metal (Apple Silicon / macOS)

- Source: `polybench_mvt_metal.mm` + `polybench_mvt.metal`  
- The `.metal` shader file must be in the same directory as the binary at runtime  
- Requires: macOS with Metal support, Xcode command-line tools

## Reference

Derived from the [PolyBench/GPU](https://sourceforge.net/projects/polybench/) benchmark suite, MVT kernel.
