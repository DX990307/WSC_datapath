# tier2/cuda_transpose — Shared-Memory Tiled Matrix Transpose

Benchmarks matrix transpose using shared memory tiling for a square N×N single-precision matrix (default 4096×4096). Compares naive vs. optimized (bank-conflict-free) transpose kernels. Measures effective memory bandwidth in GB/s.

Based on the classic [CUDA Matrix Transpose](https://developer.nvidia.com/blog/efficient-matrix-transpose-cuda-cc/) optimization pattern.

## Algorithm

Two transpose variants are benchmarked:

| Variant | Description |
|---------|-------------|
| **Naive** | Each thread reads one element (coalesced) and writes one element (strided). Simple but suffers from uncoalesced writes. |
| **Optimized** | Uses shared memory (threadgroup memory on Metal) with `TILE_DIM+1` padding to avoid bank conflicts. Both reads and writes are coalesced. |

- **TILE_DIM** = 32, **BLOCK_ROWS** = 8 — each threadblock handles a 32×32 tile, with 32×8 = 256 threads per block
- **Effective bandwidth** = 2 × N × N × sizeof(float) / time\_sec / 1e9 (GB/s)

## Files

| File | Description |
|------|-------------|
| `cuda_transpose.hip` | HIP source — naive + optimized transpose kernels |
| `cuda_transpose.metal` | Metal compute shaders — both variants using threadgroup memory |
| `cuda_transpose_metal.mm` | ObjC++ Metal host code |
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
./cuda_transpose                # default: 4096×4096
./cuda_transpose --size 2048    # custom matrix size
```

## Output

**stdout** — CSV timing rows (one per variant):
```
cuda_transpose,naive_4096,1.2345,103.45
cuda_transpose,optimized_4096,0.5678,224.89
```

Format: `cuda_transpose,<variant>_<N>,<time_ms>,<GB/s>`

**stderr** — human-readable results:
```
Device: Apple M2 (id=0)
Matrix size: 4096×4096  |  Warmup: 5  |  Timed: 5

Naive transpose:     avg 1.2345 ms  |  103.45 GB/s
  Verification: PASS
Optimized transpose: avg 0.5678 ms  |  224.89 GB/s
  Verification: PASS
```

## Platform Notes

- **CUDA/ROCm**: compiled with `nvcc`/`hipcc` from `cuda_transpose.hip`
- **Metal**: compiled with `clang++` from `cuda_transpose_metal.mm`; the Metal shader (`cuda_transpose.metal`) is loaded and compiled at runtime from the same directory as the binary
