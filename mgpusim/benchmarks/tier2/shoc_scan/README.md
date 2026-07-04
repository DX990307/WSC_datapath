# tier2/shoc_scan — SHOC Exclusive Prefix Scan Benchmark

## Overview

The **Scan** benchmark measures the effective GPU memory bandwidth for an
**exclusive prefix sum** (scan) operation:

```
output[i] = input[0] + input[1] + … + input[i-1]   (output[0] = 0)
```

This is a classic parallel-computing primitive with read–modify–write memory
access patterns.  The implementation uses a **work-efficient (Blelloch)
two-phase scan** (up-sweep + down-sweep) with shared/threadgroup memory, and
supports arrays of arbitrary size via recursive multi-block scan.  Derived from
the [SHOC benchmark suite](https://github.com/vetter/shoc).

## Files

| File | Description |
|------|-------------|
| `shoc_scan.hip` | Self-contained HIP kernel + host (NVIDIA/AMD) |
| `shoc_scan.metal` | Metal compute shader — `scan_block_kernel` + `add_block_sums_kernel` (Apple) |
| `shoc_scan_metal.mm` | Objective-C++ Metal host with multi-pass recursive scan (Apple) |
| `Makefile` | Auto-detects platform; supports `cuda`, `rocm`, `metal` |

## Algorithm

The Blelloch work-efficient scan runs in O(n) work and O(log n) depth:

1. **Up-sweep (reduce)**: tree reduction building partial sums.
2. **Clear + Down-sweep**: zero the last element and propagate the exclusive
   prefix down the tree.

For arrays larger than a single block:
- **Phase 1** — each block performs a local Blelloch scan and saves its block
  total to `block_sums[]`.
- **Phase 2** — recursively scan `block_sums[]` to get `scanned_block_sums[]`.
- **Phase 3** — add `scanned_block_sums[i]` to every element in block `i`.

## Building

```bash
# Auto-detect (Darwin → metal, /opt/rocm present → rocm, else → cuda)
make

# Explicit platform
make PLATFORM=metal
make PLATFORM=rocm
make PLATFORM=cuda
```

**Requirements:**
- **CUDA**: `nvcc` (CUDA toolkit ≥ 10)
- **ROCm/HIP**: `hipcc` (ROCm ≥ 4.0)
- **Metal**: macOS 12+, Xcode Command Line Tools

## Running

```bash
./shoc_scan [--array_size N] [--block_size B] [--iterations I]
```

| Option | Default | Description |
|--------|---------|-------------|
| `--array_size N` | `1048576` | Number of `float` elements (1 M ≈ 4 MiB) |
| `--block_size B` | `256` | Threads per block — elements per block = 2× (HIP/CUDA only) |
| `--iterations I` | `20` | Number of timed kernel launches |

> Note: `--block_size` is ignored on Metal (thread-group size is set automatically).

## Output

**stdout** — CSV row with timing statistics:
```
kernel_name,problem_size,iterations,avg_ms,min_ms,max_ms,stddev_ms
scan,1048576,20,0.2150,0.2108,0.2301,0.0038
```

**stderr** — human-readable summary:
```
Device: Apple M4 Pro
Array size: 1048576 floats (4.0 MiB, padded to 1048576)
Threadgroup size: 256  |  Iterations: 20

Effective bandwidth: 38.96 GB/s  (avg 0.2150 ms)
PASS
```

## Bandwidth Formula

```
Bandwidth (GB/s) = 2 × N × sizeof(float) / avg_time_seconds / 1e9
```

The factor of 2 accounts for one read (input) and one write (output).

## Example Results

| Platform | Array Size | Avg (ms) | Bandwidth |
|----------|-----------|----------|-----------|
| Apple M4 Pro (Metal) | 1 M floats | ~0.22 | ~38 GB/s |
| NVIDIA RTX 4090 (CUDA) | 1 M floats | ~0.05 | ~160 GB/s |
| AMD RX 7900 XTX (ROCm) | 1 M floats | ~0.07 | ~114 GB/s |

*(Results are indicative and vary by system configuration.)*
