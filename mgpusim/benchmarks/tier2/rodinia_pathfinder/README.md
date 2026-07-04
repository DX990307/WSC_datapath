# rodinia_pathfinder — Minimum-Cost Path Through a 2D Grid

Rodinia PathFinder benchmark: computes the minimum-cost path through a 2D grid
from top to bottom using dynamic programming with double-buffered row sweeps on GPU.

## Algorithm

- Grid of `ROWS × COLS` integer costs (randomly initialized to `[0, 10)`)
- Initialize `src[col] = wall[0][col]` (first row)
- For each row `t` from `1` to `ROWS-1`:
  - Each GPU thread (one per column) computes:
    ```
    dst[col] = wall[t][col] + min(src[col-1], src[col], src[col+1])
    ```
  - Swap `src` ↔ `dst` (double buffering)
- The minimum value in the final `src` array is the overall minimum path cost

## Files

| File | Description |
|------|-------------|
| `rodinia_pathfinder.hip` | HIP/CUDA implementation (NVIDIA + AMD) |
| `rodinia_pathfinder.metal` | Metal compute shader (Apple) |
| `rodinia_pathfinder_metal.mm` | ObjC++ Metal host (Apple) |
| `Makefile` | Platform auto-detect build system |
| `README.md` | This file |

## Build

```bash
# Auto-detect platform
make

# Explicit platform
make PLATFORM=cuda    # NVIDIA CUDA
make PLATFORM=rocm    # AMD ROCm/HIP
make PLATFORM=metal   # Apple Metal

# Clean
make clean
```

**Requirements:**
- CUDA: NVIDIA CUDA toolkit + `nvcc`
- ROCm: AMD ROCm 5.x+ + `hipcc`
- Metal: macOS 12+ + Xcode command-line tools

## Run

```bash
./rodinia_pathfinder [--rows R] [--cols C] [--iterations I]
```

| Option | Default | Description |
|--------|---------|-------------|
| `--rows R` | 100000 | Number of grid rows |
| `--cols C` | 100 | Number of grid columns |
| `--iterations I` | 3 | Number of timed benchmark iterations |

## Output

```
benchmark,rows,cols,time_ms,GBs
pathfinder,100000,100,45.2318,0.0267
```

Informational output (stderr):
- GPU device name
- Grid dimensions
- Verification result (against small CPU reference)
- Minimum cost in the final row

## Performance Notes

- Memory access pattern: each row sweep reads `COLS` ints (src) + `COLS` ints (wall),
  writes `COLS` ints (dst) → `3 × ROWS × COLS × 4` bytes per iteration
- The kernel is bandwidth-bound with minimal compute per element
- Metal implementation batches `1000` row dispatches per command buffer to reduce
  host-side overhead
- For very large grids (e.g., 1M rows), reduce `--rows` or increase batch size in code

## References

- [Rodinia Benchmark Suite](https://rodinia.cs.virginia.edu/)
- S. Che et al., "Rodinia: A benchmark suite for heterogeneous computing," IISWC 2009
