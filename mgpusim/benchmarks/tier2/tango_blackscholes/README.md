# tier2/tango_blackscholes — Black-Scholes European Option Pricing

Benchmarks the Black-Scholes closed-form pricing of European call and put options. Measures throughput in **M options/sec**.

Derived from the [Tango benchmark suite](https://github.com/AkshayUsha/Tango) Black-Scholes workload.

## Algorithm

- **N** options (default: N=4,194,304 = 4M)
- Each thread prices one option, computing both call and put prices
- Uses the Black-Scholes formula:
  - **d1** = (ln(S/K) + (r + σ²/2)·T) / (σ·√T)
  - **d2** = d1 − σ·√T
  - **Call** = S·N(d1) − K·e^(−rT)·N(d2)
  - **Put** = K·e^(−rT)·(1−N(d2)) − S·(1−N(d1))
- Cumulative normal distribution N(x) approximated via Abramowitz & Stegun polynomial (26.2.17)
- Input parameters randomized: S∈[5,200], K∈[1,300], T∈[0.25,10], σ∈[0.1,1.0], r=0.02
- Metric: M options/sec = N / time\_sec / 10⁶

## Files

| File | Description |
|------|-------------|
| `tango_blackscholes.hip` | HIP source — Black-Scholes kernel (one thread per option) |
| `tango_blackscholes.metal` | Metal compute shader — Black-Scholes kernel |
| `tango_blackscholes_metal.mm` | ObjC++ Metal host code |
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
./tango_blackscholes                              # defaults: N=4194304, 5 iterations
./tango_blackscholes --size 1048576               # fewer options
./tango_blackscholes --size 4194304 --iterations 10
```

## Output

**stdout** — CSV timing row:
```
tango_blackscholes,4194304,2.3456,1789.0000
```

Format: `tango_blackscholes,<N>,<time_ms>,<Moptions_per_sec>`

**stderr** — human-readable results:
```
Device: AMD Radeon RX 7900 XTX (id=0)
Options: 4194304  |  Iterations: 5 warmup + 5 timed

Average time: 2.3456 ms
Performance:  1789.0000 M options/sec
PASS
```

## Verification

- Compares GPU results against CPU reference for the first 10,000 options
- Checks both call and put prices with relative tolerance

## Platform Notes

- **CUDA/ROCm**: compiled with `nvcc`/`hipcc` from `tango_blackscholes.hip`; one kernel launch, each thread prices one option
- **Metal**: compiled with `clang++` from `tango_blackscholes_metal.mm`; the Metal shader (`tango_blackscholes.metal`) is loaded and compiled at runtime from the same directory as the binary
