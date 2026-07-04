# tier2/polybench_3dconv — 3D Convolution (PolyBench)

Benchmarks 3D convolution over an **N×N×N** volume with a small 3D filter (e.g., 3×3×3). Each output voxel is the weighted sum of its spatial neighborhood. Measures compute throughput in GFLOPS.

Derived from the [PolyBench/GPU benchmark suite](https://sourceforge.net/projects/polybench/).

## Algorithm

- 3D convolution: for each output point `(i,j,k)`, sum `input[i±r][j±r][k±r] * filter[fi][fj][fk]` where `r = filter_size/2`
- Default parameters: N=128, filter_size=3
- Boundary handling: zero-padding (out-of-bounds reads treated as 0)
- GFLOPS = 2 × N³ × filter_size³ / time_sec / 1e9

## Files

| File | Description |
|------|-------------|
| `polybench_3dconv.cu` | CUDA implementation |
| `polybench_3dconv.hip` | HIP/ROCm implementation |
| `polybench_3dconv_metal.mm` | ObjC++ Metal host code |
| `polybench_3dconv.metal` | Metal compute shader |
| `Makefile` | Auto-detecting build (Darwin→metal, ROCm→rocm, else→cuda) |
| `params.json` | Parameter specification |

## Build

```bash
make               # auto-detect platform
make PLATFORM=cuda
make PLATFORM=rocm
make PLATFORM=metal
```

## Run

```bash
./polybench_3dconv                                      # defaults: 128³, filter 3³
BENCH_PARAM_size=256 ./polybench_3dconv                 # 256³ volume
BENCH_PARAM_filter_size=5 ./polybench_3dconv            # 5×5×5 filter
```

## Output

**stdout** — JSON-lines:
```json
{"type":"kernel","name":"conv3d_kernel","time_ms":12.345678,"params":{"size":128,"iterations":5,"block_size":8,"filter_size":3}}
{"type":"summary","total_time_ms":12.345678,"metrics":[{"name":"gflops","value":45.67}]}
```

**stderr** — human-readable:
```
Device: NVIDIA GeForce RTX 4090 (id=0)
Volume: 128x128x128  |  Filter: 3x3x3  |  Iterations: 5

Performance: 45.67 GFLOPS  (avg 12.3457 ms, min 12.0000 ms, max 12.8000 ms, stddev 0.2500 ms)
```

## Platform Notes

- **CUDA/ROCm**: 3D thread blocks (block_size³), compiled with `nvcc`/`hipcc`
- **Metal**: compiled with `clang++`; the Metal shader (`polybench_3dconv.metal`) is loaded at runtime from the same directory as the binary
