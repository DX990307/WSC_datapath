# tier2/tango_binomial_options — Binomial Option Pricing (CRR Model)

Benchmarks the Cox-Ross-Rubinstein (CRR) binomial tree model for **American put option** pricing with early exercise. Measures throughput in **options/sec**.

Derived from the [Tango benchmark suite](https://github.com/AkshayUsha/Tango) binomial options workload.

## Algorithm

- **N** options (default: N=512), each with **S** steps (default: S=1024)
- Each threadblock/threadgroup processes one option:
  1. **Terminal payoffs**: compute put payoff max(K − S·u^j·d^(S−j), 0) at each leaf node
  2. **Backward induction**: walk backwards through S tree levels, at each node computing max(discounted continuation value, early exercise value)
- Uses shared/threadgroup memory for the option value array at each level
- CRR parameters: u = e^(σ√Δt), d = 1/u, p = (e^(rΔt) − d) / (u − d)
- Input parameters randomized: S∈[5,200], K∈[1,300], T∈[0.25,10], σ∈[0.1,1.0], r=0.02
- Metric: options/sec = N / time\_sec

## Files

| File | Description |
|------|-------------|
| `tango_binomial_options.hip` | HIP source — binomial tree kernel (one block per option, shared memory) |
| `tango_binomial_options.metal` | Metal compute shader — binomial tree kernel (threadgroup memory) |
| `tango_binomial_options_metal.mm` | ObjC++ Metal host code |
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
./tango_binomial_options                                  # defaults: 512 options, 1024 steps
./tango_binomial_options --options 256 --steps 512        # fewer options and steps
./tango_binomial_options --options 512 --steps 1024 --iterations 10
```

## Output

**stdout** — CSV timing row:
```
tango_binomial_options,512x1024,45.6789,11205.0000
```

Format: `tango_binomial_options,<N_options>x<steps>,<time_ms>,<options_per_sec>`

**stderr** — human-readable results:
```
Device: AMD Radeon RX 7900 XTX (id=0)
Options: 512  |  Steps: 1024  |  Iterations: 5 warmup + 5 timed

Average time: 45.6789 ms
Performance:  11205.0000 options/sec
PASS
```

## Verification

- Compares GPU results against CPU reference for the first 10 options
- CPU reference uses identical backward induction algorithm
- Tolerance: relative error with absolute floor

## Platform Notes

- **CUDA/ROCm**: compiled with `nvcc`/`hipcc` from `tango_binomial_options.hip`; one block per option, shared memory for tree levels, `__syncthreads()` between levels
- **Metal**: compiled with `clang++` from `tango_binomial_options_metal.mm`; the Metal shader (`tango_binomial_options.metal`) is loaded and compiled at runtime from the same directory as the binary; uses threadgroup memory and `threadgroup_barrier`
