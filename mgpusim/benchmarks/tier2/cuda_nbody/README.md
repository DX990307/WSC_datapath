# tier2/cuda_nbody — All-Pairs N-Body Simulation (CUDA N-Body)

Benchmarks a gravitational N-body simulation using the **classic tile-based shared memory optimization**. Each thread computes the force on one body from all others. Measures throughput in GFLOPS.

Derived from the [CUDA N-Body sample](https://developer.nvidia.com/gpugems/gpugems3/part-v-physics-simulation/chapter-31-fast-n-body-simulation-cuda).

## Algorithm

- **N** bodies (default: 16,384), 10 timesteps per run
- All-pairs gravitational force computation with O(N²) interactions
- **Tile-based shared memory optimization**: bodies loaded in tiles of 256 into threadgroup/shared memory for data reuse
- Each interaction: distance computation, softening (ε=1e-5), inverse cube law → ~20 FLOPs
- After force accumulation: velocity and position integration (leapfrog, dt=0.01)
- GFLOPS = 20 · N² · timesteps / time\_sec / 10⁹

## Files

| File | Description |
|------|-------------|
| `cuda_nbody.hip` | HIP source — tile-based N-body kernel with shared memory |
| `cuda_nbody.metal` | Metal compute shader — tile-based N-body kernel with threadgroup memory |
| `cuda_nbody_metal.mm` | ObjC++ Metal host code |
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
./cuda_nbody                                   # defaults: N=16384, 10 timesteps, 5 timed iters
./cuda_nbody --bodies 8192                     # fewer bodies
./cuda_nbody --bodies 16384 --iterations 10 --timesteps 20
```

## Output

**stdout** — CSV timing row:
```
cuda_nbody,16384,234.5678,46.1234
```

Format: `cuda_nbody,<N>,<time_ms>,<GFLOPS>`

**stderr** — human-readable results:
```
Device: NVIDIA A100 (id=0)
Bodies: 16384  |  Timesteps: 10  |  Iterations: 5 warmup + 5 timed

Average time: 234.5678 ms
Performance:  46.1234 GFLOPS
PASS (verified 4096 bodies, 1 timestep)
```

## Verification

Runs 1 timestep on a subset of bodies (up to 4096) and compares GPU results against a CPU reference implementation with relative tolerance of 1e-2.

## Platform Notes

- **CUDA/ROCm**: compiled with `nvcc`/`hipcc` from `cuda_nbody.hip`; tile size = 256 threads with `__shared__` memory
- **Metal**: compiled with `clang++` from `cuda_nbody_metal.mm`; tile size = 256 threads with `threadgroup` memory; the Metal shader (`cuda_nbody.metal`) is loaded and compiled at runtime from the same directory as the binary
