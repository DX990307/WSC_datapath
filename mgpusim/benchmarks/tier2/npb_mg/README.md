# tier2/npb_mg — NPB Multi-Grid

Benchmarks a V-cycle multigrid solver from the NAS Parallel Benchmarks. Solves a 3D Poisson-like equation on a synthetic grid using Jacobi smoothing with restriction and prolongation operators. Measures throughput in Mcells/sec.

## Algorithm

- **N×N×N** 3D grid (default: 64³) with Dirichlet boundary conditions
- V-cycle multigrid hierarchy: N → N/2 → N/4 → ... → 4
- Each V-cycle consists of:
  1. **Pre-smooth** — Jacobi iterations on current level
  2. **Residual** — Compute r = rhs − A·u (discrete Laplacian)
  3. **Restrict** — Downsample residual to coarser grid (2:1 averaging)
  4. **Coarse solve** — Extra smoothing on coarsest level
  5. **Prolong** — Interpolate correction back to fine grid (injection)
  6. **Post-smooth** — Jacobi iterations after correction
- Default: 5 V-cycles per solve, 3 smooth steps per level, 5 warmup + 5 timed

## Files

| File | Description |
|------|-------------|
| `npb_mg.cu` | CUDA implementation |
| `npb_mg.hip` | HIP/ROCm implementation |
| `npb_mg_metal.mm` | Metal host code (Objective-C++) |
| `npb_mg.metal` | Metal compute shaders |
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
./npb_mg                                        # defaults: N=64
BENCH_PARAM_grid_size=128 ./npb_mg              # override via env
./npb_mg --size 128                              # override via CLI
```

## Output

**stdout** — JSON-lines:
```json
{"type":"kernel","name":"smooth_kernel","time_ms":5.234,"params":{"grid_size":64,...}}
{"type":"kernel","name":"restrict_kernel","time_ms":5.234,"params":{...}}
{"type":"kernel","name":"prolong_kernel","time_ms":5.234,"params":{...}}
{"type":"summary","total_time_ms":26.170,"metrics":[{"name":"avg_iter_ms","value":5.234},{"name":"mcells_per_sec","value":50.12}]}
```

**stderr** — human-readable timing and verification

## Verification

- Computes residual norm `||rhs − A·u|| / ||rhs||` after multigrid solve
- Reports PASS if relative residual < 10.0 (multigrid with few V-cycles provides approximate solution)

## Platform Notes

- **CUDA/ROCm**: Uses `cudaEvent` / `hipEvent` for GPU timing; shared memory reduction for norm computation
- **Metal**: Loads `npb_mg.metal` shader at runtime; uses `mach_absolute_time` for timing
- Grid size is automatically rounded up to the nearest power of 2 for proper multigrid hierarchy
