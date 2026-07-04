# polybench_gramschmidt

PolyBench Gram-Schmidt orthogonalization benchmark.

## Description

Computes the QR factorization of a dense M×N matrix A using the Gram-Schmidt
orthogonalization process:

```
A = Q * R
```

where Q is M×N with orthonormal columns and R is an N×N upper-triangular matrix.
This benchmark is derived from the [PolyBench/GPU](https://web.cse.ohio-state.edu/~pouchet.2/software/polybench/) suite.

## Algorithm

For each column k = 0 .. N−1:

1. **norm step**: compute `nrm = ||A[:,k]||₂`; set `R[k,k] = nrm`
2. **normalize step**: `Q[:,k] = A[:,k] / nrm`
3. **projection step**: for each column j = k+1 .. N−1:
   - `R[k,j] = Q[:,k]ᵀ · A[:,j]`
   - `A[:,j] -= R[k,j] * Q[:,k]`

After all N columns, Q has orthonormal columns: `Qᵀ Q ≈ Iₙ`.

### GPU Kernel Structure

| Kernel | Threads | Work |
|--------|---------|------|
| `gram_norm` (HIP) | M threads, atomicAdd | Accumulate `||A[:,k]||²` |
| `gram_norm_finish` (HIP) | 1 thread | `R[k,k] = sqrt(accum)` |
| `gram_normalize` | M threads | `Q[:,k] = A[:,k] / nrm` |
| `gram_project` | N−k−1 threads | Update columns j > k |

On Metal, the norm is computed on the CPU using the shared memory buffer, which
is efficient on Apple Silicon's unified memory architecture.

## Files

| File | Description |
|------|-------------|
| `polybench_gramschmidt.hip` | HIP/CUDA implementation (NVIDIA + AMD) |
| `polybench_gramschmidt.metal` | Metal compute shaders (Apple) |
| `polybench_gramschmidt_metal.mm` | Metal Objective-C++ host |
| `Makefile` | Multi-platform build |
| `README.md` | This file |

## Build

Platform is auto-detected; override with `PLATFORM=cuda|rocm|metal`.

```bash
# Auto-detect
make

# NVIDIA CUDA
make PLATFORM=cuda

# AMD ROCm/HIP
make PLATFORM=rocm

# Apple Metal
make PLATFORM=metal

# Clean
make clean
```

### Requirements

| Platform | Toolchain |
|----------|-----------|
| CUDA | NVIDIA CUDA toolkit (`nvcc`) |
| ROCm | AMD ROCm (`hipcc`) |
| Metal | Xcode command-line tools (`clang++`) |

## Run

```bash
./polybench_gramschmidt                    # default: M=512, N=512, 3 passes
./polybench_gramschmidt --m 1024 --n 512  # larger matrix
./polybench_gramschmidt --passes 5        # more timed passes
```

## Output Format

**stderr** — device info, per-pass timing, orthogonality check result, summary

**stdout** — CSV result line:

```
gramschmidt,<M>,<N>,<time_ms>,<GBs>
```

Example:
```
gramschmidt,512,512,45.2318,1.08
```

## Performance Metric

```
GB/s = (3 × M × N × sizeof(float)) / (avg_time_s × 10⁹)
```

The factor of 3 accounts for the dominant memory traffic in the projection
step: one read of `Q[:,k]`, one read of `A[:,j]`, and one write to `A[:,j]`
per column j > k, summed across all N column iterations.

## Orthogonality Verification

After the final timed pass, the benchmark computes `Qᵀ Q` on the CPU for the
first `min(8, N)` columns and checks:

- **Diagonal** elements: `|dot(Q[:,i], Q[:,i]) − 1| < 0.01`
- **Off-diagonal** elements: `|dot(Q[:,i], Q[:,j])| < 0.01`

A `PASS` / `FAIL` result is printed to stderr.
