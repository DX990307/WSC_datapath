# tier2/shoc_reduction — SHOC Parallel Sum Reduction Benchmark

## Overview

The **Reduction** benchmark measures effective GPU memory bandwidth by performing a parallel sum reduction over a large float array, collapsing it to a single scalar result.  It uses a **tree-based shared-memory (threadgroup) reduction**:

- Each thread block (threadgroup) processes **2 × block_size** elements.
- Threads cooperatively reduce to a single partial sum per block.
- Multiple passes are applied until one value remains.

This benchmark is derived from the [SHOC benchmark suite](https://github.com/vetter/shoc).

## Files

| File | Description |
|------|-------------|
| `shoc_reduction.hip` | Self-contained HIP kernel + host (NVIDIA/AMD) |
| `shoc_reduction.metal` | Metal compute shader (Apple) |
| `shoc_reduction_metal.mm` | Objective-C++ Metal host (Apple) |
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
./shoc_reduction [--size N] [--block_size B] [--iterations I]
```

| Option | Default | Description |
|--------|---------|-------------|
| `--size N` | `4194304` | Number of `float` elements (4 M ≈ 16 MiB) |
| `--block_size B` | `256` | GPU thread-block size (HIP/CUDA only) |
| `--iterations I` | `20` | Number of timed kernel launches |

> Note: `--block_size` is ignored on Metal (threadgroup size is set automatically from the pipeline).

## Output

**stdout** — CSV row with timing statistics:
```
kernel_name,problem_size,iterations,avg_ms,min_ms,max_ms,stddev_ms
reduction,4194304,20,0.1821,0.1798,0.1952,0.0031
```

**stderr** — human-readable summary:
```
Device: Apple M4 Pro
Array size: 4194304 floats (16.0 MiB)
Iterations: 20

Effective bandwidth: 184.22 GB/s  (avg 0.1821 ms)
GPU sum: 499500.000000, CPU sum: 499500.000000, relative error: 0.000000e+00
PASS
```

## Bandwidth Formula

```
Bandwidth (GB/s) = 2 × N × sizeof(float) / avg_time_seconds / 1e9
```

The factor of 2 accounts for reading N floats and writing the reduced result.

## Algorithm

### HIP / CUDA

```
Pass 1: reduce_kernel<<<num_blocks, B, B*sizeof(float)>>>(input, partial, N)
Pass 2: reduce_kernel<<<1, B, B*sizeof(float)>>>(partial, result, num_blocks)
```

Each block loads two elements per thread into shared memory, then performs
a binary tree reduction until one partial sum per block is produced.

### Metal

The same two-pass strategy is applied using `threadgroup` memory.  The
`reduce_kernel` shader is compiled at runtime from `shoc_reduction.metal`.
Passes are chained using ping-pong scratch buffers until a single value
remains.

## Example Results

| Platform | Array Size | Avg (ms) | Bandwidth |
|----------|-----------|----------|-----------|
| Apple M4 Pro (Metal) | 4 M floats | ~0.18 | ~184 GB/s |
| NVIDIA RTX 4090 (CUDA) | 4 M floats | ~0.04 | ~820 GB/s |
| AMD RX 7900 XTX (ROCm) | 4 M floats | ~0.06 | ~545 GB/s |

*(Results are indicative and vary by system configuration.)*
