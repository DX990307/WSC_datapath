# tier2/altis_particlefilter — Particle Filter (Altis)

Benchmarks a Sequential Importance Resampling (SIR) particle filter for Monte Carlo localization on a 2D grid. Tracks a moving target over multiple time steps using particles with position updates, weight computation, and resampling. Measures throughput in particles/sec.

Derived from the [Altis benchmark suite](https://github.com/utcs-scea/altis) Particle Filter workload.

## Algorithm

- **N** particles (default: N=100,000) on a 128×128 grid, 10 time steps
- Three GPU kernels per time step:
  1. **Update positions** — random walk: each particle moves by Gaussian noise (σ=2.0)
  2. **Compute weights** — Gaussian likelihood: w = exp(−d² / 2σ²) where d is distance to observation
  3. **Resample** — systematic resampling: parallel prefix sum of weights, then binary search in CDF
- Synthetic observations: target moves in a circle (radius 20) around grid center
- particles/sec = N × timesteps / time\_sec

## Files

| File | Description |
|------|-------------|
| `altis_particlefilter.hip` | HIP source — particle filter kernels (update, weights, scan, resample) |
| `altis_particlefilter.metal` | Metal compute shaders — all particle filter kernels |
| `altis_particlefilter_metal.mm` | ObjC++ Metal host code |
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
./altis_particlefilter                              # defaults: N=100000, 5 iterations
./altis_particlefilter --size 50000                 # fewer particles
./altis_particlefilter --size 100000 --iterations 10
```

## Output

**stdout** — CSV timing row:
```
altis_particlefilter,100000,15.2345,65663500.0000
```

Format: `altis_particlefilter,<N_particles>,<time_ms>,<particles_per_sec>`

**stderr** — human-readable results:
```
Device: AMD Radeon RX 7900 XTX (id=0)
Particle Filter  |  Particles: 100000  |  Grid: 128x128  |  Steps: 10  |  Iterations: 5 warmup + 5 timed

Average time: 15.2345 ms
Throughput:   65663500.0000 particles/sec
GPU mean: (64.1234, 63.8765)  CPU mean: (64.2345, 63.7654)
PASS
```

## Verification

- Runs both GPU and CPU particle filter on same initial data (up to 10,000 particles)
- Compares mean particle positions — both should converge to the observation region
- Reports PASS if distance between GPU and CPU mean < 20 grid units (generous tolerance for stochastic algorithm)

## Platform Notes

- **CUDA/ROCm**: compiled with `nvcc`/`hipcc` from `altis_particlefilter.hip`; uses xorshift32 PRNG per particle; Blelloch scan for prefix sum with host-side block sum fixup
- **Metal**: compiled with `clang++` from `altis_particlefilter_metal.mm`; the Metal shader (`altis_particlefilter.metal`) is loaded and compiled at runtime from the same directory as the binary
