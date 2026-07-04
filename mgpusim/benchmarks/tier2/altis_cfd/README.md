# tier2/altis_cfd — Computational Fluid Dynamics (Altis)

Benchmarks a compressible Euler equations solver on an unstructured mesh using a finite-volume method. Each GPU thread processes one mesh cell, computing fluxes from neighbor data and performing Runge-Kutta time integration. Measures throughput in Mcells/sec.

Derived from the [Altis benchmark suite](https://github.com/utcs-scea/altis) CFD workload.

## Algorithm

- **N** mesh cells (default: N=97,000) with 4 neighbors each
- Conserved variables per cell: density (ρ), momentum (mx, my, mz), energy (e)
- Finite-volume method with Rusanov (local Lax-Friedrichs) numerical flux:
  1. **Flux computation** — each thread reads its cell and 4 neighbors, computes interface fluxes using Rusanov dissipation
  2. **Runge-Kutta update** — 2-stage scheme: U_new = rk_coeff × U_old + (1 − rk_coeff) × (U − dt/vol × flux)
- Synthetic unstructured mesh with random connectivity and face geometry
- Mcells/sec = N / time\_sec / 10⁶

## Files

| File | Description |
|------|-------------|
| `altis_cfd.hip` | HIP source — Euler solver kernels (flux + RK), verification |
| `altis_cfd.metal` | Metal compute shaders — flux and RK update kernels |
| `altis_cfd_metal.mm` | ObjC++ Metal host code |
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
./altis_cfd                              # defaults: N=97000, 5 iterations
./altis_cfd --size 50000                 # fewer cells
./altis_cfd --size 97000 --iterations 10
```

## Output

**stdout** — CSV timing row:
```
altis_cfd,97000,2.3456,41.3500
```

Format: `altis_cfd,<N_cells>,<time_ms>,<Mcells_per_sec>`

**stderr** — human-readable results:
```
Device: AMD Radeon RX 7900 XTX (id=0)
CFD Euler solver  |  Cells: 97000  |  Neighbors: 4  |  Iterations: 5 warmup + 5 timed

Average time: 2.3456 ms
Throughput:   41.3500 Mcells/sec
PASS
```

## Verification

- Flux computation for first 256 cells is compared against a CPU reference
- Reports PASS/FAIL with mismatch details (relative tolerance 0.1%)

## Platform Notes

- **CUDA/ROCm**: compiled with `nvcc`/`hipcc` from `altis_cfd.hip`; uses Structure-of-Arrays layout for coalesced memory access
- **Metal**: compiled with `clang++` from `altis_cfd_metal.mm`; the Metal shader (`altis_cfd.metal`) is loaded and compiled at runtime from the same directory as the binary
