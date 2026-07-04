# tier2/chai_hsto — Histogram (Chai)

Benchmarks a 256-bin histogram computation on the GPU using shared memory privatization and global atomic reduction. Measures throughput in GB/s.

Derived from the [Chai benchmark suite](https://github.com/chai-benchmarks/chai) HSTO (histogram) collaborative computing pattern.

## Algorithm

- **N** random bytes as input (default: N=16,777,216 = 16M bytes = 16 MB)
- 256 histogram bins (one per byte value 0–255)
- GPU implementation:
  1. Each threadgroup initializes a private histogram in shared/threadgroup memory (256 bins)
  2. Threads iterate over input data (grid-stride loop), atomically incrementing shared bins
  3. After barrier, each threadgroup atomically adds its private bins to the global histogram
- This "privatization + reduction" pattern reduces global atomic contention
- GB/s = N × sizeof(uint8\_t) / time\_sec / 10⁹

## Files

| File | Description |
|------|-------------|
| `chai_hsto.hip` | HIP source — histogram kernel, CPU reference, verification |
| `chai_hsto.metal` | Metal compute shader — histogram kernel |
| `chai_hsto_metal.mm` | ObjC++ Metal host code |
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
./chai_hsto                          # defaults: N=16777216 (16M), 5 iterations
./chai_hsto --size 1048576           # smaller input (1M)
./chai_hsto --size 16777216 --iterations 10
```

## Output

**stdout** — CSV timing row:
```
chai_hsto,16777216,1.2345,13.5900
```

Format: `chai_hsto,<N>,<time_ms>,<GBs>`

**stderr** — human-readable results:
```
Device: Apple M2
Histogram  |  N: 16777216 (16.00 MB)  |  Bins: 256  |  Iterations: 5 warmup + 5 timed

Average time: 1.2345 ms
Throughput:   13.5900 GB/s
PASS
```

## Verification

- All 256 bins are compared exactly against a CPU reference histogram
- Reports PASS/FAIL with per-bin mismatch details

## Platform Notes

- **CUDA/ROCm**: compiled with `nvcc`/`hipcc` from `chai_hsto.hip`; uses `__shared__` memory and `atomicAdd`
- **Metal**: compiled with `clang++` from `chai_hsto_metal.mm`; the Metal shader (`chai_hsto.metal`) is loaded and compiled at runtime from the same directory as the binary; uses `threadgroup atomic_uint` and `atomic_fetch_add_explicit`
