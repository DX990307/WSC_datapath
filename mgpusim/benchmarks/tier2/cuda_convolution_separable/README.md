# tier2/cuda_convolution_separable — Separable 2D Convolution

Benchmarks a **separable 2D convolution** (row pass + column pass) on a floating-point image. Measures throughput in GB/s.

Derived from the [CUDA SDK convolutionSeparable](https://github.com/NVIDIA/cuda-samples) sample.

## Algorithm

- **Image size**: W×H floats (default: 4096×4096)
- **Kernel radius**: R (default: 8, producing a 17-tap filter)
- **Gaussian kernel** weights generated from σ = R/3
- Two-pass separable convolution:
  1. **Row pass** — each thread loads a row segment (with halo) into shared memory, applies 1D horizontal convolution
  2. **Column pass** — each thread loads a column segment (with halo) into shared memory, applies 1D vertical convolution
- GB/s = 2 · W · H · sizeof(float) / time\_sec / 10⁹

## Files

| File | Description |
|------|-------------|
| `cuda_convolution_separable.hip` | HIP source — row + column convolution kernels with shared memory |
| `cuda_convolution_separable.metal` | Metal compute shaders — row + column convolution kernels |
| `cuda_convolution_separable_metal.mm` | ObjC++ Metal host code |
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
./cuda_convolution_separable                                    # defaults: 4096x4096, radius=8, 5 iterations
./cuda_convolution_separable --width 2048 --height 2048         # smaller image
./cuda_convolution_separable --radius 16 --iterations 10        # wider kernel, more iterations
```

## Output

**stdout** — CSV timing row:
```
cuda_convolution_separable,4096x4096,5.1234,25.6000
```

Format: `cuda_convolution_separable,<WxH>,<time_ms>,<GBs>`

**stderr** — human-readable results:
```
Device: NVIDIA GeForce RTX 4090 (id=0)
Image: 4096x4096  |  Kernel radius: 8 (17-tap)  |  Iterations: 5 warmup + 5 timed

Average time: 5.1234 ms
Throughput:   25.6000 GB/s
PASS
```

## Verification

- Compares GPU output against CPU separable convolution reference
- Checks a 512×512 sub-region (or full image if smaller) with relative tolerance

## Platform Notes

- **CUDA/ROCm**: compiled with `nvcc`/`hipcc` from `cuda_convolution_separable.hip`; uses constant memory for kernel weights, shared memory for row/column tiles
- **Metal**: compiled with `clang++` from `cuda_convolution_separable_metal.mm`; the Metal shader (`cuda_convolution_separable.metal`) is loaded and compiled at runtime from the same directory as the binary
