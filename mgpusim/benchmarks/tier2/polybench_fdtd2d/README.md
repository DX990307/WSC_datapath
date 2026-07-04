# polybench_fdtd2d — PolyBench FDTD-2D Benchmark

2D Finite Difference Time Domain (FDTD) electromagnetic simulation, adapted from the PolyBench benchmark suite. Measures effective memory bandwidth of iterative stencil updates on three 2D field arrays.

## Algorithm

Three field arrays `ex[NX][NY]`, `ey[NX][NY]`, `hz[NX][NY]` are updated each time step:

```
ex[0][j]  = 0                                          (boundary)
ex[i][j] += 0.5*(hz[i][j] - hz[i-1][j])  for i >= 1

ey[i][0]  = 0                                          (boundary)
ey[i][j] += 0.5*(hz[i][j] - hz[i][j-1])  for j >= 1

hz[i][j] -= 0.7*(ex[i][j+1] - ex[i][j] +
                 ey[i+1][j]  - ey[i][j])  for i < NX-1, j < NY-1
```

The benchmark runs `TMAX` time steps per timed iteration and reports effective bandwidth using `7 * NX * NY * sizeof(float)` bytes per step.

## Files

| File | Description |
|------|-------------|
| `polybench_fdtd2d.hip` | HIP/CUDA source — 3 GPU kernels, self-contained |
| `polybench_fdtd2d.metal` | Metal compute shader — 3 kernels (`update_ex`, `update_ey`, `update_hz`) |
| `polybench_fdtd2d_metal.mm` | Objective-C++ Metal host program |
| `Makefile` | Build system with platform auto-detection |
| `README.md` | This file |

## Build

Platform is auto-detected. Override with `PLATFORM=cuda|rocm|metal`.

```bash
# Auto-detect
make

# Force CUDA
make PLATFORM=cuda

# Force ROCm/HIP
make PLATFORM=rocm

# Force Metal (macOS only)
make PLATFORM=metal

# Clean
make clean
```

### Requirements

- **CUDA**: NVIDIA GPU + CUDA toolkit (`nvcc`)
- **ROCm**: AMD GPU + ROCm/HIP toolkit (`hipcc`)
- **Metal**: macOS with Apple GPU (`clang++`, Xcode command-line tools)

## Run

```bash
# Default: 512×512 grid, 50 time steps, 3 iterations
./polybench_fdtd2d

# Custom grid size, time steps, iterations
./polybench_fdtd2d --size 1024 --tmax 100 --iterations 5
```

### Arguments

| Flag | Default | Description |
|------|---------|-------------|
| `--size N` | 512 | Grid dimension (N×N) |
| `--tmax T` | 50 | FDTD time steps per benchmark iteration |
| `--iterations I` | 3 | Number of timed benchmark iterations |

## Output

**stdout** (CSV):
```
kernel_name,problem_size,iterations,avg_ms,min_ms,max_ms,stddev_ms
polybench_fdtd2d,512x512,3,45.2310,44.8120,45.9100,0.5821
```

**stderr**:
```
Device: NVIDIA GeForce RTX 3090 (id=0)
Grid: 512×512  |  TMAX: 50  |  Iterations: 3

Effective bandwidth: 312.45 GB/s  (avg 45.2310 ms, 50 steps/iter)
```

## Performance Notes

- Each time step performs three stencil passes over NX×NY float arrays
- Memory-bound benchmark; performance scales with GPU memory bandwidth
- Effective bandwidth formula: `7 * NX * NY * 4 bytes * TMAX / time_seconds`
  - The factor 7 accounts for reads and writes across all three kernels
