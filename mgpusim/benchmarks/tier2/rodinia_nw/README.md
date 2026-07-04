# tier2/rodinia_nw — Rodinia Needleman-Wunsch Benchmark

Dynamic programming sequence alignment using the Needleman-Wunsch algorithm.
The scoring matrix is filled with block-based anti-diagonal wavefront parallelism
and threadgroup/shared-memory tiling, based on the Rodinia NW benchmark.

## Algorithm

- Sequences of length **N** over an alphabet of size 10 (synthetic, reproducible).
- DP matrix is **(N+1) × (N+1)**; borders initialised on CPU.
- Scoring: **match = +1**, **mismatch = −1**, **gap = −penalty** (default 2).
- Two GPU kernel passes process the block-dependency graph's upper-left and
  lower-right triangles respectively; within each block a shared-memory
  anti-diagonal sweep fills a `block_size × block_size` tile.
- Reports **GCUPS** (giga cell-updates per second) and effective **GB/s**.

## Files

| File | Description |
|------|-------------|
| `rodinia_nw.hip` | HIP/CUDA source (self-contained, no external headers) |
| `rodinia_nw.metal` | Metal compute shader (two kernels) |
| `rodinia_nw_metal.mm` | ObjC++ Metal host |
| `Makefile` | Platform auto-detection + build rules |
| `README.md` | This file |

## Build

```bash
# Auto-detect (Darwin → metal, /opt/rocm present → rocm, else → cuda)
make

# Explicit platform
make PLATFORM=cuda
make PLATFORM=rocm
make PLATFORM=metal
```

## Run

```bash
./rodinia_nw                              # defaults: N=2048, block=16, penalty=2, iters=3
./rodinia_nw --sequence_length 1024
./rodinia_nw --sequence_length 4096 --block_size 32 --penalty 10 --iterations 5
```

## Output

```
kernel_name,problem_size,iterations,avg_ms,min_ms,max_ms,stddev_ms
rodinia_nw,2048,3,42.3210,41.9870,42.6540,0.3380
rodinia_nw,2048,42.3210        ← GCUPS metrics CSV line
```

Standard error shows human-readable GCUPS and GB/s.

## Platforms

| Platform | Compiler | Source file |
|----------|----------|-------------|
| CUDA     | `nvcc`   | `rodinia_nw.hip` (compiled as CUDA via `-x cu`) |
| ROCm     | `hipcc`  | `rodinia_nw.hip` |
| Metal    | `clang++`| `rodinia_nw_metal.mm` + `rodinia_nw.metal` |
