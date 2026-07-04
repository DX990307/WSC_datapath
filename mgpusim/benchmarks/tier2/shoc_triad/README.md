# tier2/shoc_triad — SHOC Stream Triad Benchmark

## Overview

The **Stream Triad** benchmark measures effective GPU memory bandwidth by executing the classic operation:

```
a[i] = b[i] + scalar * c[i]
```

This pattern reads two arrays (`b`, `c`) and writes one (`a`), for a total of **3 × N × 4 bytes** of memory traffic per iteration.  It is derived from the [SHOC benchmark suite](https://github.com/vetter/shoc).

## Files

| File | Description |
|------|-------------|
| `shoc_triad.hip` | Self-contained HIP kernel + host (NVIDIA/AMD) |
| `shoc_triad.metal` | Metal compute shader (Apple) |
| `shoc_triad_metal.mm` | Objective-C++ Metal host (Apple) |
| `Makefile` | Auto-detects platform; supports `cuda`, `rocm`, `metal` |

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
./shoc_triad [--array_size N] [--block_size B] [--iterations I]
```

| Option | Default | Description |
|--------|---------|-------------|
| `--array_size N` | `4194304` | Number of `float` elements (4 M ≈ 16 MiB per array) |
| `--block_size B` | `256` | GPU thread-block size (HIP/CUDA only) |
| `--iterations I` | `20` | Number of timed kernel launches |

> Note: `--block_size` is ignored on Metal (thread-group size is set automatically).

## Output

**stdout** — CSV row with timing statistics:
```
kernel_name,problem_size,iterations,avg_ms,min_ms,max_ms,stddev_ms
triad,4194304,20,0.3142,0.3098,0.3301,0.0042
```

**stderr** — human-readable summary:
```
Device: Apple M4 Pro
Array size: 4194304 floats (16.0 MiB)
Iterations: 20

Effective bandwidth: 153.47 GB/s  (avg 0.3142 ms)
PASS
```

## Bandwidth Formula

```
Bandwidth (GB/s) = 3 × N × sizeof(float) / avg_time_seconds / 1e9
```

The factor of 3 accounts for two reads (`b`, `c`) and one write (`a`).

## Example Results

| Platform | Array Size | Avg (ms) | Bandwidth |
|----------|-----------|----------|-----------|
| Apple M4 Pro (Metal) | 4 M floats | ~0.31 | ~153 GB/s |
| NVIDIA RTX 4090 (CUDA) | 4 M floats | ~0.08 | ~600 GB/s |
| AMD RX 7900 XTX (ROCm) | 4 M floats | ~0.11 | ~450 GB/s |

*(Results are indicative and vary by system configuration.)*
