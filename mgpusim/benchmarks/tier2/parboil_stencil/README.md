# tier2/parboil_stencil — 3D Jacobi Stencil (Parboil)

7-point 3D Jacobi stencil computation over a cubic grid using ping-pong buffers. Each interior cell is updated from its six face-adjacent neighbors. Boundary cells remain fixed. A classic memory-bandwidth-bound benchmark.

Derived from the [Parboil benchmark suite](http://impact.crhc.illinois.edu/parboil/parboil.aspx) stencil workload.

## Algorithm

For each interior cell `(ix, iy, iz)` of an `N×N×N` grid:
```
out[i] = c0 * in[i]
       + c1 * (in[i-1] + in[i+1]          // x neighbors
             + in[i-nx] + in[i+nx]         // y neighbors
             + in[i-nx*ny] + in[i+nx*ny])  // z neighbors
```
where `c0 = 0.6`, `c1 = (1 - c0) / 6 ≈ 0.0667`.

Boundary cells (1-cell border) are skipped. Multiple time-steps are performed per benchmark iteration using ping-pong buffers.

## Files

| File | Description |
|------|-------------|
| `parboil_stencil.hip` | HIP source — 3D stencil kernel + self-contained host utilities |
| `parboil_stencil.metal` | Metal compute shader — 3D stencil kernel |
| `parboil_stencil_metal.mm` | ObjC++ Metal host code |
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
./parboil_stencil                                          # defaults: 64×64×64, 10 timesteps, 5 iters
./parboil_stencil --grid_dim 128
./parboil_stencil --grid_dim 64 --num_timesteps 20 --iterations 10
```

## Output

**stdout** — CSV timing row followed by CSV summary:
```
kernel_name,problem_size,iterations,avg_ms,min_ms,max_ms,stddev_ms
stencil3d,64x64x64,5,1.2345,1.2000,1.3000,0.0234
parboil_stencil,64x64x64,3.4567
```

**stderr** — human-readable results:
```
Device: Apple M4 Pro
Grid: 64×64×64  |  Timesteps/launch: 10  |  Iterations: 5

GFLOPS: 3.46  |  GB/s: 13.83  (avg 1.2345 ms, 10 timesteps/iter)
```

## Performance Metrics

- **GFLOPS** = 8 × interior_cells × num_timesteps / time_s / 1e9
- **GB/s** = 8 × sizeof(float) × interior_cells × num_timesteps / time_s / 1e9
  (7 reads + 1 write per interior cell per time-step)

## Platform Notes

- **CUDA/ROCm**: compiled with `nvcc`/`hipcc` from `parboil_stencil.hip`
- **Metal**: compiled with `clang++` from `parboil_stencil_metal.mm`; the Metal shader is loaded at runtime from the same directory as the binary
- Input data is generated synthetically — no external input files required
