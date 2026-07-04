# tier2/rodinia_lavamd — Short-Range Molecular Dynamics (Rodinia LavaMD)

Short-range molecular dynamics simulation using cell-list decomposition. Computes Lennard-Jones type particle interactions within neighboring cells (26 neighbors + self = 27 cells per box) on an N×N×N grid of boxes, each containing a fixed number of particles.

Derived from the [Rodinia benchmark suite](https://rodinia.cs.virginia.edu/) LavaMD workload.

## Algorithm

For each box `(bx, by, bz)` in an `N×N×N` grid:
```
for each particle i in box:
    for each of 27 neighboring boxes (including self):
        for each particle j in neighbor box:
            r = distance(pos_i, pos_j)
            if r > 0:
                // Lennard-Jones potential: V(r) = 4*eps*((sigma/r)^12 - (sigma/r)^6)
                r2inv = 1.0 / (r*r)
                r6inv = r2inv * r2inv * r2inv
                force = r2inv * r6inv * (LJ_A * r6inv - LJ_B)
                energy += r6inv * (LJ_A * r6inv - LJ_B)
                f_i += force * (pos_i - pos_j) / r
```

Each thread block processes one box, and threads within the block compute forces for particles in that box.

## Files

| File | Description |
|------|-------------|
| `rodinia_lavamd.cu` | CUDA source — LJ kernel + self-contained utilities |
| `rodinia_lavamd.hip` | HIP source — LJ kernel + self-contained utilities |
| `rodinia_lavamd.metal` | Metal compute shader — LJ kernel |
| `rodinia_lavamd_metal.mm` | ObjC++ Metal host code |
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
./rodinia_lavamd                                              # defaults: 10³ boxes, 100 particles/box
BENCH_PARAM_num_boxes=15 ./rodinia_lavamd
BENCH_PARAM_num_boxes=8 BENCH_PARAM_particles_per_box=200 ./rodinia_lavamd
```

## Output

**stdout** — JSON-lines:
```
{"type":"kernel","name":"lavamd_kernel","time_ms":12.345,"params":{"num_boxes":10,...}}
{"type":"summary","total_time_ms":61.725,"metrics":[{"name":"gflops","value":45.67}]}
```

**stderr** — human-readable results:
```
Device: Apple M4 Pro
Boxes: 10×10×10 (1000 total)  |  Particles/box: 100  |  Iterations: 5
Average time: 12.345 ms  |  Throughput: 45.67 GFLOPS
```

## Platform Notes

- **CUDA/ROCm**: compiled with `nvcc`/`hipcc`
- **Metal**: compiled with `clang++`; the Metal shader is loaded at runtime from the same directory as the binary
- Input data is generated synthetically — no external input files required
