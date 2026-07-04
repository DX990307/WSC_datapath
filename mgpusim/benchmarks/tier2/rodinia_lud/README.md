# tier2/rodinia_lud — Blocked LU Decomposition (Rodinia)

Implements the Rodinia LUD benchmark: blocked LU decomposition (no pivoting)
of a dense NxN single-precision matrix.

## Algorithm

Three GPU kernels are launched in sequence for each diagonal block step `k`:

| Kernel | Description |
|--------|-------------|
| `lud_diagonal` | In-place LU factor of the 16×16 diagonal block at `(k,k)` |
| `lud_perimeter` | Forward/back-solve for row-panel (right) and column-panel (below) blocks |
| `lud_internal` | Schur-complement update for all remaining interior blocks |

The matrix is stored in-place:
- Lower-triangular part (i>j) → **L** (unit diagonal implicit)
- Upper-triangular part (i≤j) → **U**

## Verification

After the warmup run the benchmark reconstructs **A = L·U** on the CPU and
checks: `||A - LU||_F / ||A||_F < 1e-4`.

## Performance Metric

```
GFLOPS = (2/3 · N³) / time_s / 1e9
```

## Build

```bash
# Auto-detect platform (Darwin → metal, /opt/rocm present → rocm, else → cuda)
make

# Explicit platform
make PLATFORM=cuda
make PLATFORM=rocm
make PLATFORM=metal
```

Requirements:
- **CUDA**: `nvcc` (CUDA toolkit)
- **ROCm**: `hipcc` (ROCm ≥ 5.0)
- **Metal**: macOS 12+, Xcode Command Line Tools; `rodinia_lud.metal` must be
  in the same directory as the binary at runtime.

## Run

```bash
./rodinia_lud                        # default: N=512, 3 iterations
./rodinia_lud --size 1024            # larger matrix
./rodinia_lud --size 512 --iterations 5
```

`N` must be divisible by the block size (16).

## Output

Progress and verification are printed to **stderr**; the CSV result line goes
to **stdout**:

```
lud,<N>,<time_ms>,<GFLOPS>
```

Example:
```
lud,512,12.3456,9.15
```

## Parameters

| Flag | Default | Description |
|------|---------|-------------|
| `--size N` | 512 | Matrix dimension (must be divisible by 16) |
| `--iterations I` | 3 | Number of timed iterations (average is reported) |

## Reference

Rodinia Benchmark Suite — [LUD](https://github.com/yuhc/gpu-rodinia)
S. Che et al., "Rodinia: A benchmark suite for heterogeneous computing,"
IISWC 2009.
