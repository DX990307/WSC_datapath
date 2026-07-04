# tier2/rodinia_gaussian — Gaussian Elimination (Rodinia)

Gaussian elimination with back-substitution to solve `Ax = b` for an `N×N` dense matrix. Derived from the [Rodinia benchmark suite](https://rodinia.cs.virginia.edu/) Gaussian workload.

## Algorithm

Forward elimination using two GPU kernels per pivot step `k = 0..N-2`:

1. **fan1** — Compute multipliers for pivot column `k`:
   ```
   m[i][k] = a[i][k] / a[k][k]   for i = k+1..N-1
   ```
2. **fan2** — Eliminate pivot column from submatrix:
   ```
   a[i][j] -= m[i][k] * a[k][j]  for i,j > k
   b[i]    -= m[i][k] * b[k]      for i > k
   ```

After forward elimination, **back-substitution** is performed on the CPU:
```
x[i] = (b[i] - sum_{j>i} a[i][j]*x[j]) / a[i][i]
```

The matrix `A` is initialized as diagonally dominant (`A_ii = N`, `A_ij ~ Uniform[0, 0.9]`) to ensure numerical stability without partial pivoting.

## Files

| File | Description |
|------|-------------|
| `rodinia_gaussian.hip` | HIP/CUDA source — fan1 + fan2 kernels, self-contained |
| `rodinia_gaussian.metal` | Metal compute shaders — fan1_kernel + fan2_kernel |
| `rodinia_gaussian_metal.mm` | ObjC++ Metal host code |
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
./rodinia_gaussian                         # defaults: N=512, 3 iterations
./rodinia_gaussian --size 256
./rodinia_gaussian --size 1024 --iterations 5
```

## Output

**stdout** — CSV result row:
```
gaussian,<N>,<time_ms>,<GFLOPS>
```
Example:
```
gaussian,512,45.1234,1.97
```

**stderr** — human-readable details:
```
Device: Apple M4 Pro
Matrix size: 512×512  |  Iterations: 3

Verification: rel_err = 1.23e-06 — PASS

Performance: 1.97 GFLOPS  (avg 45.1234 ms, min 44.9000 ms, max 45.5000 ms, stddev 0.3012 ms)
```

## Metrics

- **Time**: wall-clock time for GPU forward elimination (fan1 + fan2 kernels across all `N-1` pivot steps), averaged over iterations
- **GFLOPS**: `(2/3 × N³) / time_s / 1e9`
- **Verification**: relative residual `‖Ax − b‖ / ‖b‖ < 1e-4`

## Platform Notes

- **CUDA/ROCm**: compiled with `nvcc`/`hipcc` from `rodinia_gaussian.hip`; uses `hipEvent_t` for timing
- **Metal**: compiled with `clang++` from `rodinia_gaussian_metal.mm`; the Metal shader (`rodinia_gaussian.metal`) is loaded at runtime from the same directory as the binary; uses `mach_absolute_time()` for timing
- Input data is generated synthetically — no external input files required
- Back-substitution runs on the CPU after the GPU forward elimination phase
