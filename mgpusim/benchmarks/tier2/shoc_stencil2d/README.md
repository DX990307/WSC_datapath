# shoc_stencil2d — SHOC 9-Point 2D Stencil Benchmark

## Description

Applies a 9-point star stencil iteratively on a 2D floating-point grid.
Measures effective memory bandwidth in GB/s.
Derived from the SHOC (Scalable Heterogeneous Computing) benchmark suite.

## Algorithm

The stencil reads 9 neighbors for each interior cell:

```
out[i][j] = 0.5   * in[i][j]
          + 0.1   * (in[i-1][j] + in[i+1][j] + in[i][j-1] + in[i][j+1])
          + 0.025 * (in[i-1][j-1] + in[i-1][j+1] + in[i+1][j-1] + in[i+1][j+1])
```

- **Grid**: N×N floats (default N=2048)
- **Interior only**: rows/columns 1 .. N-2; boundary cells remain 0
- **Thread blocks**: 16×16
- **Ping-pong buffers**: `in_buf` and `out_buf` swapped each iteration
- **Trials**: 3 timed trials, average reported

## Files

| File                        | Description                                |
|-----------------------------|--------------------------------------------|
| `shoc_stencil2d.hip`        | HIP/CUDA kernel + host (NVIDIA & AMD)      |
| `shoc_stencil2d.metal`      | Metal compute shader (Apple)               |
| `shoc_stencil2d_metal.mm`   | Metal Objective-C++ host (Apple)           |
| `Makefile`                  | Multi-platform build (cuda/rocm/metal)     |
| `README.md`                 | This file                                  |

## Build

```bash
# Auto-detect platform
make

# Explicit platform
make PLATFORM=cuda    # NVIDIA CUDA
make PLATFORM=rocm    # AMD ROCm/HIP
make PLATFORM=metal   # Apple Metal

make clean
```

## Run

```bash
# Default: 2048×2048 grid, 5 iterations, 3 trials
./shoc_stencil2d

# Custom parameters
./shoc_stencil2d --n 4096 --iterations 10 --trials 5
```

### Options

| Option           | Default | Description                         |
|------------------|---------|-------------------------------------|
| `--n N`          | 2048    | Grid dimension (N×N floats)         |
| `--iterations I` | 5       | Stencil steps per timed trial       |
| `--trials T`     | 3       | Number of timed trials              |

## Output Format

**stderr**: device info, per-trial timing, correctness check result

**stdout** (CSV):
```
stencil2d,<N>,<ITERATIONS>,<time_ms>,<GBs>
```

Example:
```
stencil2d,2048,5,12.3456,270.45
```

## Performance Metric

Effective memory bandwidth (GB/s) approximates each stencil step as
reading and writing the full N×N grid:

```
GB/s = ITERATIONS × 2 × N × N × sizeof(float) / (avg_time_s × 1e9)
```

## Correctness Verification

Before the timed run, a single stencil iteration is applied to a small
64×64 grid on both CPU and GPU. The GPU result is compared element-wise
to the CPU reference; the check passes when the maximum relative error
across all interior cells is less than 1×10⁻⁴.
