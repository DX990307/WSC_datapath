# polybench_jacobi2d

PolyBench 2D Jacobi stencil benchmark — GPU implementation for CUDA, ROCm/HIP, and Apple Metal.

## Algorithm

Iterates a 2D Jacobi stencil on an N×N grid for TSTEPS time steps:

```
B[i][j] = (A[i-1][j] + A[i+1][j] + A[i][j-1] + A[i][j+1] + A[i][j]) / 5.0
```

After each step, the source and destination buffers are swapped (double-buffer technique). Only interior points (`i = 1..N-2`, `j = 1..N-2`) are updated; boundary values remain fixed at 0.

## Files

| File | Description |
|------|-------------|
| `polybench_jacobi2d.hip` | HIP kernel + host code (CUDA and ROCm/HIP) |
| `polybench_jacobi2d.metal` | Metal compute shader |
| `polybench_jacobi2d_metal.mm` | Objective-C++ Metal host code |
| `Makefile` | Build system with platform auto-detection |
| `README.md` | This file |

## Build Instructions

### Auto-detect platform
```bash
make
```

### NVIDIA CUDA
```bash
make PLATFORM=cuda
```

### AMD ROCm/HIP
```bash
make PLATFORM=rocm
```

### Apple Metal
```bash
make PLATFORM=metal
```

### Clean
```bash
make clean
```

## Run Instructions

```bash
./polybench_jacobi2d [--n N] [--tsteps T] [--trials K]
```

| Option | Description | Default |
|--------|-------------|---------|
| `--n N` | Grid size (N×N) | 1024 |
| `--tsteps T` | Number of time steps | 50 |
| `--trials K` | Number of timed trials | 3 |

### Examples

```bash
# Default run (1024×1024, 50 steps, 3 trials)
./polybench_jacobi2d

# Large grid
./polybench_jacobi2d --n 2048 --tsteps 100

# Quick test
./polybench_jacobi2d --n 512 --tsteps 10 --trials 1
```

## Output Format

**stderr** — device info, correctness check result, and bandwidth summary:
```
Device: NVIDIA GeForce RTX 3080 (id=0)
Grid: 1024×1024  |  TSTEPS: 50  |  Trials: 3

Correctness check (CPU vs GPU, 64×64, 50 steps): PASS (max_rel=0.00e+00)

Bandwidth: 412.35 GB/s  (avg 25.1234 ms, min 25.0001 ms, max 25.3456 ms, stddev 0.1234 ms)
```

**stdout** — CSV result line:
```
jacobi2d,<N>,<TSTEPS>,<time_ms>,<GBs>
```

Example:
```
jacobi2d,1024,50,25.1234,412.35
```

## Performance Metric

Memory bandwidth is computed as:

```
GB/s = TSTEPS × 2 × N × N × sizeof(float) / time_s / 1e9
```

Each time step reads the full N×N source array and writes the full N×N destination array.

## Correctness Verification

A CPU reference computation is run on a small 64×64 grid for the same number of TSTEPS. The GPU result is compared against the CPU result. The test passes if the maximum relative error across all interior points is below 1e-3.

## Kernel Configuration

- Thread block size: 16×16
- Grid size: `ceil((N-2)/16) × ceil((N-2)/16)`
- Each thread computes one interior point per time step
