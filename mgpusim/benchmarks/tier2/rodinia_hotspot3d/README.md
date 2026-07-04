# tier2/rodinia_hotspot3d — 3D Thermal Simulation (Rodinia HotSpot3D)

3D stencil thermal simulation extending the classic 2D HotSpot benchmark to three dimensions. Each cell's temperature is updated based on its six neighbors (±x, ±y, ±z), local power density, and thermal resistances in an N×N×N grid.

Derived from the [Rodinia benchmark suite](https://rodinia.cs.virginia.edu/) HotSpot3D workload.

## Algorithm

For each cell `(x, y, z)` in an `N×N×N` grid:
```
T_new[z][y][x] = T[z][y][x] + step_div_cap * (
    power[z][y][x]
    + (T[z][y][x-1] + T[z][y][x+1] - 2*T[z][y][x]) * Rx_1   // ±x neighbors
    + (T[z][y-1][x] + T[z][y+1][x] - 2*T[z][y][x]) * Ry_1   // ±y neighbors
    + (T[z-1][y][x] + T[z+1][y][x] - 2*T[z][y][x]) * Rz_1   // ±z neighbors
    + (AMB_TEMP - T[z][y][x]) * Ra_1                           // ambient heat loss
)
```
Boundary cells use clamped (no-flux) boundary conditions.

Multiple time-steps are run per benchmark iteration using ping-pong buffers.

## Files

| File | Description |
|------|-------------|
| `rodinia_hotspot3d.cu` | CUDA source — 3D stencil kernel + self-contained utilities |
| `rodinia_hotspot3d.hip` | HIP source — 3D stencil kernel + self-contained utilities |
| `rodinia_hotspot3d.metal` | Metal compute shader — 3D stencil kernel |
| `rodinia_hotspot3d_metal.mm` | ObjC++ Metal host code |
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
./rodinia_hotspot3d                                         # defaults: 64×64×64, 10 steps, 5 iters
BENCH_PARAM_grid_size=128 ./rodinia_hotspot3d
BENCH_PARAM_grid_size=32 BENCH_PARAM_num_iterations=20 ./rodinia_hotspot3d
```

## Output

**stdout** — JSON-lines:
```
{"type":"kernel","name":"hotspot3d_kernel","time_ms":1.234,"params":{"grid_size":64,...}}
{"type":"summary","total_time_ms":6.170,"metrics":[{"name":"bandwidth_gbps","value":123.45}]}
```

**stderr** — human-readable results:
```
Device: Apple M4 Pro
Grid: 64×64×64  |  Steps/launch: 10  |  Iterations: 5
Effective bandwidth: 123.45 GB/s  (avg 1.2340 ms, 10 steps/iter)
```

## Platform Notes

- **CUDA/ROCm**: compiled with `nvcc`/`hipcc`
- **Metal**: compiled with `clang++`; the Metal shader is loaded at runtime from the same directory as the binary
- Input data is generated synthetically — no external input files required
