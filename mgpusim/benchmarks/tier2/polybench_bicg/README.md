# polybench_bicg — PolyBench BiCG Sub-kernel Benchmark

Benchmarks the BiCG sub-kernel of the BiCGStab iterative linear solver on GPU.

## Algorithm

Computes two matrix-vector products simultaneously:

```
s = A^T * r   (transpose matrix-vector product)
q = A   * p   (matrix-vector product)
```

where:
- **A** is an M×N matrix (default M=N=4096)
- **p** is an N-vector (input)
- **r** is an M-vector (input)
- **s** is an N-vector (output)
- **q** is an M-vector (output)

### GPU Kernels

| Kernel | Operation | Parallelism |
|--------|-----------|-------------|
| `bicg_kernel1` | `s[j] = Σᵢ A[i,j] * r[i]` | One thread per column j |
| `bicg_kernel2` | `q[i] = Σⱼ A[i,j] * p[j]` | One thread per row i |

## Files

| File | Description |
|------|-------------|
| `polybench_bicg.hip` | HIP/CUDA implementation (NVIDIA + AMD) |
| `polybench_bicg.metal` | Metal compute shaders (Apple GPU) |
| `polybench_bicg_metal.mm` | Metal host code (Objective-C++) |
| `Makefile` | Build system with platform auto-detection |

## Build

```bash
# Auto-detect platform (Darwin→metal, /opt/rocm→rocm, else→cuda)
make

# Explicit platform
make PLATFORM=cuda
make PLATFORM=rocm
make PLATFORM=metal

# Clean
make clean
```

## Run

```bash
# Default: M=N=4096, 5 iterations
./polybench_bicg

# Custom size and iterations
./polybench_bicg --m 8192 --n 8192 --iterations 10
```

## Output

**stderr** — device info, correctness check, timing details:
```
Device: Apple M2 Pro
Matrix size: 4096×4096  |  Iterations: 5

Correctness check (first 8 s, 8 q elements): PASS

Bandwidth: 412.50 GB/s  (avg 0.3201 ms, min 0.3145 ms, max 0.3312 ms, stddev 0.0068 ms)
```

**stdout** — CSV result:
```
bicg,<N>,<time_ms>,<GBs>
```

Example:
```
bicg,4096,0.3201,412.50
```

## Performance Metric

Memory bandwidth (GB/s) is computed as:

```
GB/s = (2 × M × N × 4 bytes) / time_s / 1e9
```

Matrix A is read once by each kernel, totalling 2 reads.

## Correctness Verification

GPU results for `s` and `q` are compared against a CPU reference. The first 8 elements of each output vector are checked. Maximum relative error threshold: **1e-4**.

## References

- [PolyBench/GPU](https://web.cse.ohio-state.edu/~pouchet.2/software/polybench/)
- BiCGStab iterative solver: Barrett et al., *Templates for the Solution of Linear Systems*, SIAM 1994.
