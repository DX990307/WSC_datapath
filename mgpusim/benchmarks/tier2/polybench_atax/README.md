# polybench_atax

PolyBench ATAX benchmark: matrix-transpose times vector.

Computes **y = Aᵀ · (A · x)** where A is an NX×NY matrix and x, y are NY-vectors.

## Algorithm

Two GPU kernels are executed per iteration:

1. **atax_kernel1** — `tmp = A · x`  
   Each thread computes one row: `tmp[i] = Σⱼ A[i,j] * x[j]`

2. **atax_kernel2** — `y = Aᵀ · tmp`  
   Each thread computes one column: `y[j] = Σᵢ A[i,j] * tmp[i]`

## Metric

**GB/s** (memory bandwidth) — matrix A is read twice per iteration (once per kernel):

```
GB/s = (2 * NX * NY * 4 bytes) / time_s / 1e9
```

## Output

```
polybench_atax,<NX>x<NY>,<GB/s>
```

Example:
```
polybench_atax,4096x4096,245.73
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
# Default: NX=NY=4096, 5 iterations
./polybench_atax

# Custom size
./polybench_atax --nx 2048 --ny 2048

# More iterations
./polybench_atax --nx 4096 --ny 4096 --iterations 10
```

## Parameters

| Flag | Default | Description |
|------|---------|-------------|
| `--nx NX` | 4096 | Number of matrix rows |
| `--ny NY` | 4096 | Number of matrix columns |
| `--iterations I` | 5 | Number of timed iterations (plus 1 warmup) |

## Platform Notes

### CUDA / ROCm (HIP)

- Source: `polybench_atax.hip`  
- Block size: 256 threads  
- Grid: `ceil(NX/256)` for kernel1, `ceil(NY/256)` for kernel2  
- Requires: `nvcc` (CUDA) or `hipcc` (ROCm)

### Metal (Apple Silicon / macOS)

- Source: `polybench_atax_metal.mm` + `polybench_atax.metal`  
- The `.metal` shader file must be in the same directory as the binary at runtime  
- Requires: macOS with Metal support, Xcode command-line tools

## Reference

Derived from the [PolyBench/GPU](https://sourceforge.net/projects/polybench/) benchmark suite, ATAX kernel.
