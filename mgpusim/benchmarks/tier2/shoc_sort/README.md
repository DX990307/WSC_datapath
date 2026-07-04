# shoc_sort — SHOC Sort Benchmark

GPU bitonic sort benchmark derived from the SHOC benchmark suite.

## Algorithm

Bitonic sort: a comparison-based parallel sorting network with O(n log² n)
compare-and-swap operations, fully parallelizable on the GPU. Each GPU
kernel pass performs one compare-and-swap step for all n elements
simultaneously.

## Default Parameters

| Parameter   | Default     | Description                         |
|-------------|-------------|-------------------------------------|
| `--size N`  | 4194304     | Number of elements (must be 2^k)    |
| `--iterations I` | 5      | Number of timed iterations          |

## Output

- **stdout** (CSV): `shoc_sort,<N>,<Melements/s>`
- **stderr**: device info, per-iteration timing, correctness check

## Build

### Auto-detect Platform
```bash
make
```

### NVIDIA CUDA
```bash
make PLATFORM=cuda
# Requires: nvcc (CUDA toolkit)
```

### AMD ROCm/HIP
```bash
make PLATFORM=rocm
# Requires: hipcc (ROCm installation at /opt/rocm)
```

### Apple Metal
```bash
make PLATFORM=metal
# Requires: Xcode command-line tools, macOS 10.14+
```

## Run

```bash
./shoc_sort
./shoc_sort --size 8388608 --iterations 10
```

### Example Output

```
Device: NVIDIA A100-SXM4-80GB (id=0)
Array size: 4194304 elements (16.0 MB) | Iterations: 5

  Iter 1: 45.2312 ms
  Iter 2: 44.9841 ms
  Iter 3: 45.1023 ms
  Iter 4: 45.0512 ms
  Iter 5: 45.2104 ms
Throughput: 92.98 Melements/s  (avg 45.1158 ms, ...)
Correctness: PASS

shoc_sort,4194304,92.98
```

## Files

| File | Description |
|------|-------------|
| `shoc_sort.hip` | HIP source — bitonic sort kernel + host code (CUDA & ROCm) |
| `shoc_sort.metal` | Metal compute shader — bitonic sort kernel |
| `shoc_sort_metal.mm` | Objective-C++ Metal host code (Apple) |
| `Makefile` | Platform-aware build system |
| `README.md` | This file |

## Notes

- **Self-contained**: all utilities are inlined; no shared headers required.
- The Metal implementation dispatches one command buffer per (j, k) pass.
  For very large arrays this creates many small dispatches; this is the
  straightforward Metal bitonic sort approach.
- N must be a power of 2. Non-power-of-2 values are rounded up automatically.
