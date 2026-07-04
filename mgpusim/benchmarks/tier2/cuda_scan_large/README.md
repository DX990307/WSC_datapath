# tier2/cuda_scan_large — Work-Efficient Parallel Prefix Sum (Blelloch Scan)

Benchmarks a **work-efficient parallel exclusive prefix sum** (Blelloch scan) on a large array of unsigned integers. Measures throughput in GB/s.

Derived from the [CUDA SDK scanLargeArray](https://github.com/NVIDIA/cuda-samples) sample.

## Algorithm

- **N** unsigned integers (default: N=16,777,216 = 16M)
- Three-phase hierarchical Blelloch scan:
  1. **Block-level scan** — each block of 512 elements performs an in-place exclusive scan using shared memory (up-sweep + down-sweep), stores block total
  2. **Scan of block sums** — recursively scan the array of block totals
  3. **Add block sums back** — add each scanned block total to all elements in the corresponding block
- Handles arbitrarily large arrays via recursive decomposition
- GB/s = 2 · N · sizeof(uint) / time\_sec / 10⁹

## Files

| File | Description |
|------|-------------|
| `cuda_scan_large.hip` | HIP source — block scan + add-back kernels with recursive dispatch |
| `cuda_scan_large.metal` | Metal compute shaders — block scan + add-back kernels |
| `cuda_scan_large_metal.mm` | ObjC++ Metal host code with recursive scan |
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
./cuda_scan_large                              # defaults: N=16777216, 5 iterations
./cuda_scan_large --size 1048576               # smaller array (1M)
./cuda_scan_large --size 16777216 --iterations 10
```

## Output

**stdout** — CSV timing row:
```
cuda_scan_large,16777216,3.4567,78.0000
```

Format: `cuda_scan_large,<N>,<time_ms>,<GBs>`

**stderr** — human-readable results:
```
Device: NVIDIA GeForce RTX 4090 (id=0)
Scan size: 16777216  |  Iterations: 5 warmup + 5 timed

Average time: 3.4567 ms
Throughput:   78.0000 GB/s
PASS
```

## Verification

- Full element-wise comparison against CPU exclusive prefix sum
- Exact match required (integer arithmetic, no floating-point tolerance)

## Platform Notes

- **CUDA/ROCm**: compiled with `nvcc`/`hipcc` from `cuda_scan_large.hip`; uses shared memory for block-level scan, recursive kernel launches for hierarchical decomposition
- **Metal**: compiled with `clang++` from `cuda_scan_large_metal.mm`; the Metal shader (`cuda_scan_large.metal`) is loaded and compiled at runtime from the same directory as the binary; recursive scan uses separate command buffer submissions per level
