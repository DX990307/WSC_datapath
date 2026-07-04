# tier2/chai_cedd — Canny Edge Detection (Chai)

Benchmarks Canny Edge Detection (CEDD) on the GPU using a 5-stage pipeline. Processes a synthetic grayscale image and measures throughput in megapixels per second.

Derived from the [Chai benchmark suite](https://github.com/chai-benchmarks/chai) CEDD workload.

## Algorithm

- **Input**: W×H grayscale image (default: 2048×2048, synthetic random)
- 5 GPU kernels launched sequentially:
  1. **Gaussian Blur** — 5×5 Gaussian smoothing to reduce noise
  2. **Sobel Gradient** — compute horizontal (Gx) and vertical (Gy) gradients
  3. **Magnitude + Direction** — compute gradient magnitude and quantize direction to 0°/45°/90°/135°
  4. **Non-Maximum Suppression** — thin edges by suppressing non-local-maxima along gradient direction
  5. **Hysteresis Thresholding** — double-threshold edge classification (low=50, high=100)
- Mpixels/sec = W × H / time\_sec / 10⁶

## Files

| File | Description |
|------|-------------|
| `chai_cedd.hip` | HIP source — 5 CEDD kernels, CPU reference, verification |
| `chai_cedd.metal` | Metal compute shaders — 5 CEDD kernels |
| `chai_cedd_metal.mm` | ObjC++ Metal host code |
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
./chai_cedd                       # defaults: W=2048, H=2048
./chai_cedd W=1024 H=1024         # smaller image
./chai_cedd W=4096 H=4096         # larger image
```

## Output

**stdout** — CSV timing row:
```
chai_cedd,2048x2048,5.1234,819.2000
```

Format: `chai_cedd,<WxH>,<time_ms>,<Mpixels_per_sec>`

**stderr** — human-readable results:
```
Device: AMD Radeon RX 7900 XTX (id=0)
Canny Edge Detection  |  Image: 2048x2048  |  Iterations: 5 warmup + 5 timed

Average time: 5.1234 ms
Throughput:   819.2000 Mpixels/sec
PASS (0.3% pixel mismatch in 122x122 interior, 45/14884)
```

## Verification

- A 128×128 sub-region is processed on both GPU and CPU
- Interior pixels (excluding 3-pixel border) are compared
- Reports PASS if mismatch rate < 5%, FAIL otherwise
- Small differences are expected due to border handling differences between full-image GPU and sub-image CPU

## Platform Notes

- **CUDA/ROCm**: compiled with `nvcc`/`hipcc` from `chai_cedd.hip`; Gaussian kernel in `__constant__` memory; 16×16 thread blocks
- **Metal**: compiled with `clang++` from `chai_cedd_metal.mm`; the Metal shader (`chai_cedd.metal`) is loaded and compiled at runtime; all 5 kernels dispatched in a single command buffer
