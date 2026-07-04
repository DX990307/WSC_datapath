# tier2/npb_cg — NPB Conjugate Gradient

Benchmarks a sparse iterative Conjugate Gradient (CG) solver from the NAS Parallel Benchmarks. Solves Ax = b where A is a synthetic sparse matrix in CSR format. Measures SpMV bandwidth and solve throughput.

## Algorithm

- **N×N** sparse matrix with configurable nonzeros per row (default: 7)
- Diagonally-dominant matrix ensures convergence
- CG iteration consists of:
  1. **SpMV** — sparse matrix-vector multiply `Ap = A * p` (CSR format)
  2. **Dot product** — `pAp = dot(p, Ap)` with block-level reduction
  3. **AXPY** — `x = x + alpha * p`, `r = r - alpha * Ap`
  4. **Dot product** — `rr_new = dot(r, r)` for convergence check
  5. **Scale + AXPY** — direction update `p = r + beta * p`
- Default: 25 CG iterations per solve, 5 warmup + 5 timed runs

## Files

| File | Description |
|------|-------------|
| `npb_cg.cu` | CUDA implementation |
| `npb_cg.hip` | HIP/ROCm implementation |
| `npb_cg_metal.mm` | Metal host code (Objective-C++) |
| `npb_cg.metal` | Metal compute shaders |
| `Makefile` | Auto-detecting build (Darwin→metal, ROCm→rocm, else→cuda) |
| `params.json` | Parameter specification |

## Build

```bash
make               # auto-detect platform
make PLATFORM=cuda
make PLATFORM=rocm
make PLATFORM=metal
```

## Run

```bash
./npb_cg                                        # defaults: N=50000
BENCH_PARAM_matrix_size=100000 ./npb_cg         # override via env
./npb_cg --size 100000                           # override via CLI
```

## Output

**stdout** — JSON-lines:
```json
{"type":"kernel","name":"spmv_kernel","time_ms":1.234,"params":{"matrix_size":50000,...}}
{"type":"kernel","name":"dot_product_kernel","time_ms":1.234,"params":{...}}
{"type":"kernel","name":"axpy_kernel","time_ms":1.234,"params":{...}}
{"type":"summary","total_time_ms":6.170,"metrics":[{"name":"avg_iter_ms","value":1.234},{"name":"spmv_bw_gb_s","value":45.67}]}
```

**stderr** — human-readable timing and verification

## Verification

- Computes residual `||b - Ax|| / ||b||` after CG solve
- Reports PASS if relative residual < 1.0 (coarse check — CG with few iterations may not fully converge)

## Platform Notes

- **CUDA/ROCm**: Uses `cudaEvent` / `hipEvent` for GPU timing; shared memory reduction for dot products
- **Metal**: Loads `npb_cg.metal` shader at runtime; uses `mach_absolute_time` for timing
