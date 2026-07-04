# tier2/npb_ep — Embarrassingly Parallel (NAS EP)

Benchmarks the generation of **N pairs of Gaussian random deviates** using the Box-Muller transform with a per-thread linear congruential RNG. Counts results into 10 annular bins. Purely compute-bound workload with minimal memory traffic.

Derived from the [NAS Parallel Benchmarks](https://www.nas.nasa.gov/software/npb.html) EP kernel.

## Algorithm

- **N** pairs of Gaussian random deviates (default: N=16,777,216 = 2²⁴)
- Per-thread linear congruential RNG (a = 5¹³ = 1,220,703,125, mod 2³²)
- Box-Muller transform: uniform (u₁, u₂) → Gaussian (x₁, x₂)
  - r = √(−2 ln u₁), θ = 2π u₂
  - x₁ = r cos θ, x₂ = r sin θ
- Bin index = floor(√(x₁² + x₂²)), capped at 9 (10 bins total)
- Atomic increment of bin counters

## Files

| File | Description |
|------|-------------|
| `npb_ep.hip` | HIP source — EP kernel with LCG + Box-Muller + binning |
| `npb_ep.metal` | Metal compute shader — EP kernel |
| `npb_ep_metal.mm` | ObjC++ Metal host code |
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
./npb_ep                              # defaults: N=16777216, 5 iterations
./npb_ep --size 1048576               # smaller workload
./npb_ep --size 16777216 --iterations 10
```

## Output

**stdout** — CSV timing row:
```
npb_ep,16777216,8.2345,2.0380
```

Format: `npb_ep,<N>,<time_ms>,<Gpairs_per_sec>`

**stderr** — human-readable results:
```
Device: AMD Radeon RX 7900 XTX (id=0)
EP pairs: 16777216 (2^24)  |  Iterations: 5 warmup + 5 timed

Average time: 8.2345 ms
Performance:  2.0380 Gpairs/sec

Bin counts (GPU vs CPU, N=1048576):
  Bin 0: GPU=697254  CPU=697254
  Bin 1: GPU=315462  CPU=315462
  ...
PASS
```

## Verification

- Runs CPU reference EP with same RNG and compares bin counts exactly
- For large N, verification uses a capped subset (N ≤ 1,048,576) for tractable CPU time

## Platform Notes

- **CUDA/ROCm**: compiled with `nvcc`/`hipcc` from `npb_ep.hip`; one kernel launch, atomicAdd for bin counting
- **Metal**: compiled with `clang++` from `npb_ep_metal.mm`; the Metal shader (`npb_ep.metal`) is loaded and compiled at runtime from the same directory as the binary
