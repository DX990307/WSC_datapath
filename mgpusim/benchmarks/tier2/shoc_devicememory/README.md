# tier2/shoc_devicememory — Device Memory Bandwidth (SHOC)

Benchmarks GPU global memory bandwidth for three access patterns: read-only, write-only, and read-write. Reports GB/s for each pattern.

Derived from the [SHOC benchmark suite](https://github.com/vetter/shoc) DeviceMemory workload.

## Kernels

| Kernel | Access Pattern | Bandwidth Formula |
|--------|---------------|-------------------|
| `DeviceMemory_Read` | Read-only (all threads read; one write/thread) | bytes / time |
| `DeviceMemory_Write` | Write-only (strided writes) | bytes / time |
| `DeviceMemory_ReadWrite` | Read + scale + write (in-place) | 2 × bytes / time |

## Files

| File | Description |
|------|-------------|
| `shoc_devicememory.hip` | HIP source — 3 kernels + self-contained utilities |
| `shoc_devicememory.metal` | Metal compute shaders — 3 kernels |
| `shoc_devicememory_metal.mm` | ObjC++ Metal host code |
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
./shoc_devicememory                        # defaults: 64 MB, 20 iterations
./shoc_devicememory --array_size_mb 256
./shoc_devicememory --array_size_mb 128 --iterations 30
```

## Output

**stdout** — 3 CSV timing rows:
```
kernel_name,problem_size,iterations,avg_ms,min_ms,max_ms,stddev_ms
DeviceMemory_Read,64MB,20,2.3456,2.3000,2.4000,0.0123
DeviceMemory_Write,64MB,20,1.9876,1.9500,2.0200,0.0098
DeviceMemory_ReadWrite,64MB,20,4.1234,4.0800,4.2000,0.0156
```

**stderr** — human-readable bandwidth:
```
Device: Apple M4 Pro
Array size: 64 MB (16777216 floats)  |  Iterations: 20

Read-only bandwidth:  115.23 GB/s  (64 MB)
Write-only bandwidth: 136.45 GB/s  (64 MB)
Read-write bandwidth: 103.87 GB/s  (64 MB)
```

## Platform Notes

- **CUDA/ROCm**: compiled with `nvcc`/`hipcc` from `shoc_devicememory.hip`
- **Metal**: compiled with `clang++` from `shoc_devicememory_metal.mm`; the Metal shader is loaded at runtime from the same directory as the binary
