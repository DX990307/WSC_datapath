# tier2/rodinia_hotspot — 2D Thermal Simulation (Rodinia Hotspot)

Iterative 2D stencil thermal simulation for chip temperature estimation. Each cell's temperature is updated based on its four neighbors (N/S/E/W), local power density, and thermal resistances. Classic structured-grid benchmark.

Derived from the [Rodinia benchmark suite](https://rodinia.cs.virginia.edu/) Hotspot workload.

## Algorithm

For each cell `(row, col)` in an `N×N` grid:
```
T_new[i][j] = T[i][j] + step_div_cap * (
    power[i][j]
    + (T[i-1][j] + T[i+1][j] - 2*T[i][j]) * Ry_1    // N-S neighbors
    + (T[i][j-1] + T[i][j+1] - 2*T[i][j]) * Rx_1    // E-W neighbors
    + (AMB_TEMP  - T[i][j]) * Rz_1                    // heat to substrate
)
```
Boundary cells use clamped (no-flux) boundary conditions.

Multiple time-steps are run per benchmark iteration using ping-pong buffers.

## Files

| File | Description |
|------|-------------|
| `rodinia_hotspot.hip` | HIP source — stencil kernel + self-contained utilities |
| `rodinia_hotspot.metal` | Metal compute shader — stencil kernel |
| `rodinia_hotspot_metal.mm` | ObjC++ Metal host code |
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
./rodinia_hotspot                                         # defaults: 512×512, 10 steps/iter, 20 iters
./rodinia_hotspot --grid_size 1024
./rodinia_hotspot --grid_size 256 --num_iterations 20 --iterations 50
```

## Output

**stdout** — CSV timing row:
```
kernel_name,problem_size,iterations,avg_ms,min_ms,max_ms,stddev_ms
hotspot,512,20,8.7654,8.6000,9.1000,0.1234
```

**stderr** — human-readable results:
```
Device: Apple M4 Pro
Grid: 512×512  |  Steps/launch: 10  |  Iterations: 20

Effective bandwidth: 245.67 GB/s  (avg 8.7654 ms, 10 steps/iter)
```

## Platform Notes

- **CUDA/ROCm**: compiled with `nvcc`/`hipcc` from `rodinia_hotspot.hip`
- **Metal**: compiled with `clang++` from `rodinia_hotspot_metal.mm`; the Metal shader is loaded at runtime from the same directory as the binary
- Input data is generated synthetically — no external input files required
