# tier2/parboil_histogram — Atomic Histogram Benchmark

Computes a 256-bin histogram of random 32-bit integers using GPU atomic operations.
Derived from the Parboil benchmark suite histogram workload.

## Algorithm

1. Generate N random `uint32_t` values on the host (seeded at 42).
2. Upload the data array to the GPU (once, reused across iterations).
3. Launch a GPU kernel: each thread reads one element, computes `bin = val & 0xFF`, and performs `atomicAdd(&hist[bin], 1)`.
4. Run **5 timed iterations** — the histogram buffer is zeroed before each iteration.
5. Copy the result histogram back and verify against a CPU reference (exact integer comparison of all 256 bins).
6. Report average bandwidth (GB/s) and throughput (Melements/s).

**Kernel configuration**: 256 threads per block, ⌈N/256⌉ blocks.

## Files

| File | Description |
|------|-------------|
| `parboil_histogram.hip` | HIP/CUDA kernel + host code (NVIDIA & AMD) |
| `parboil_histogram.metal` | Metal compute shader (Apple GPU) |
| `parboil_histogram_metal.mm` | Objective-C++ Metal host (Apple GPU) |
| `Makefile` | Build system with auto-platform detection |
| `README.md` | This file |

## Build

Platform is auto-detected but can be overridden:

```bash
# Auto-detect (Darwin → metal, /opt/rocm present → rocm, else → cuda)
make

# Explicit platform
make PLATFORM=cuda    # NVIDIA CUDA
make PLATFORM=rocm    # AMD ROCm/HIP
make PLATFORM=metal   # Apple Metal

make clean
```

## Run

```bash
./parboil_histogram                    # default: N=16M, 5 iterations
./parboil_histogram --n 33554432       # custom N (32M elements)
./parboil_histogram --iterations 10    # custom iteration count
```

## Output Format

**stderr** (human-readable):
```
Device: Apple M2 Pro
N = 16777216 elements  |  Bins = 256  |  Iterations = 5

Correctness check (all 256 bins): PASS

Bandwidth:     XX.XX GB/s
Throughput:    XXXX.XX Melements/s
Timing:        avg X.XXXX ms  min X.XXXX ms  max X.XXXX ms  stddev X.XXXX ms
```

**stdout** (CSV for automated collection):
```
histogram,<N>,<time_ms>,<GBs>
```

Example:
```
histogram,16777216,3.2145,20.87
```

## Performance Metric

```
GB/s = N × sizeof(uint32_t) / time_s / 1e9
     = N × 4 / time_s / 1e9

Melements/s = N / time_s / 1e6
```

where `time_s` is the average kernel execution time in seconds across all iterations.

## Correctness Verification

A CPU reference histogram is computed using the same input data and random seed.
All 256 bins are compared exactly (integer equality). Prints `PASS` or `FAIL` to stderr.

## Default Parameters

| Parameter | Value |
|-----------|-------|
| N (elements) | 16,777,216 (16 × 1024 × 1024) |
| Bins | 256 |
| Block size | 256 threads |
| Iterations | 5 |
| Bin mapping | `val & 0xFF` (lower 8 bits) |
