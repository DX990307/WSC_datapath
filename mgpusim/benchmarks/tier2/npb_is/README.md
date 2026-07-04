# tier2/npb_is — Integer Sort (NAS IS)

Benchmarks a **parallel bucket sort** of N random integers in range [0, 2¹⁹). Measures throughput in Mkeys/sec.

Derived from the [NAS Parallel Benchmarks](https://www.nas.nasa.gov/software/npb.html) IS kernel.

## Algorithm

- **N** random integer keys (default: N=8,388,608 = 2²³) in range [0, 524,288 = 2¹⁹)
- **NUM_BUCKETS** = 1,024 (2¹⁰), each covering a range of 512 key values
- Three phases:
  1. **Histogram** — count keys per bucket (atomic increments)
  2. **Prefix sum** — exclusive scan on the histogram (Blelloch algorithm, single threadgroup)
  3. **Scatter** — place each key at its sorted position (atomic offset increment)
- Result: keys sorted by bucket (bucket-level ordering)

## Files

| File | Description |
|------|-------------|
| `npb_is.hip` | HIP source — histogram, prefix sum, and scatter kernels |
| `npb_is.metal` | Metal compute shaders — histogram, prefix sum, and scatter |
| `npb_is_metal.mm` | ObjC++ Metal host code |
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
./npb_is                              # defaults: N=8388608, 5 iterations
./npb_is --size 1048576               # smaller workload
./npb_is --size 8388608 --iterations 10
```

## Output

**stdout** — CSV timing row:
```
npb_is,8388608,5.1234,1637.5000
```

Format: `npb_is,<N>,<time_ms>,<Mkeys_per_sec>`

**stderr** — human-readable results:
```
Device: AMD Radeon RX 7900 XTX (id=0)
IS keys: 8388608 (2^23)  |  Buckets: 1024  |  Iterations: 5 warmup + 5 timed

Average time: 5.1234 ms
Performance:  1637.5000 Mkeys/sec
PASS
```

## Verification

- Checks that the sorted output is non-decreasing at bucket boundaries (bucket-ordered)
- Each key's bucket index = key / (MAX_KEY / NUM_BUCKETS); consecutive elements must have non-decreasing bucket indices

## Platform Notes

- **CUDA/ROCm**: compiled with `nvcc`/`hipcc` from `npb_is.hip`; three kernel launches per sort (histogram + prefix sum + scatter)
- **Metal**: compiled with `clang++` from `npb_is_metal.mm`; the Metal shader (`npb_is.metal`) is loaded and compiled at runtime from the same directory as the binary
