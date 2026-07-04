# tier2/altis_raytracing — Simple Ray Tracing (Altis)

Benchmarks a simple ray tracer on the GPU. Each thread casts one ray from a camera through an image plane, tests all spheres for intersection, and shades the nearest hit with Phong lighting. Measures throughput in Mrays/sec.

Derived from the [Altis benchmark suite](https://github.com/utcs-scea/altis) ray tracing workload.

## Algorithm

- **W×H** image (default: 1024×1024 = 1M pixels/rays)
- **N** spheres randomly placed in the scene (default: 64)
- For each pixel:
  1. Compute ray direction from camera through pixel
  2. Test ray against all N spheres (ray-sphere intersection via quadratic formula)
  3. Shade nearest hit using Phong lighting model:
     - Ambient component (0.15)
     - Diffuse (Lambertian) shading
     - Specular highlight (exponent ~8)
  4. Write RGBA pixel to output
- Camera at (0, 0, 5), looking along -Z, 90° FOV
- Light direction: normalized (1, 1, 1)
- Mrays/sec = W × H / time\_sec / 10⁶

## Files

| File | Description |
|------|-------------|
| `altis_raytracing.hip` | HIP source — ray tracing kernel, CPU reference, verification |
| `altis_raytracing.metal` | Metal compute shader — ray tracing kernel |
| `altis_raytracing_metal.mm` | ObjC++ Metal host code |
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
./altis_raytracing                                # defaults: 1024x1024, 64 spheres
./altis_raytracing --width 512 --height 512       # smaller image
./altis_raytracing --spheres 128 --iterations 10  # more spheres, more iterations
```

## Output

**stdout** — CSV timing row:
```
altis_raytracing,1024x1024,2.3456,447.1234
```

Format: `altis_raytracing,<WxH>,<time_ms>,<Mrays_per_sec>`

**stderr** — human-readable results:
```
Device: Apple M2
Ray Tracing  |  Image: 1024x1024  |  Spheres: 64  |  Iterations: 5 warmup + 5 timed

Average time: 2.3456 ms
Throughput:   447.1234 Mrays/sec
PASS
```

## Verification

- 1024 randomly-selected pixels are compared against a CPU reference implementation
- Allows ±1 per channel for floating-point rounding differences
- Reports PASS/FAIL with mismatch details

## Platform Notes

- **CUDA/ROCm**: compiled with `nvcc`/`hipcc` from `altis_raytracing.hip`
- **Metal**: compiled with `clang++` from `altis_raytracing_metal.mm`; the Metal shader (`altis_raytracing.metal`) is loaded and compiled at runtime from the same directory as the binary
