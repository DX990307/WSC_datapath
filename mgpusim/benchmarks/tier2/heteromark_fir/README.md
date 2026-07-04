# tier2/heteromark_fir — FIR Filter (HeteroMark)

Benchmarks a 1D FIR (Finite Impulse Response) filter on the GPU. Each thread computes one output sample. Measures throughput in GB/s.

Derived from the [HeteroMark benchmark suite](https://github.com/NUCAR-DEV/Hetero-Mark) FIR workload.

## Algorithm

- **N** input samples (default: N=1,048,576 = 1M floats)
- **NUM_TAPS** = 128 filter coefficients
- 1D convolution: `output[i] = Σ coeff[k] × input[i−k]` for k = 0..NUM\_TAPS−1
- Each GPU thread computes one output sample
- Filter coefficients loaded into shared memory (HIP) / threadgroup memory (Metal) for fast reuse
- GB/s = (input\_size + output\_size) × sizeof(float) / time\_sec / 10⁹

## Files

| File | Description |
|------|-------------|
| `heteromark_fir.hip` | HIP source — FIR kernel with shared memory, CPU reference, verification |
| `heteromark_fir.metal` | Metal compute shader — FIR kernel with threadgroup memory |
| `heteromark_fir_metal.mm` | ObjC++ Metal host code |
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
./heteromark_fir                              # defaults: N=1048576, 5 iterations
./heteromark_fir --size 65536                 # fewer samples
./heteromark_fir --size 1048576 --iterations 10
```

## Output

**stdout** — CSV timing row:
```
heteromark_fir,1048576,3.2145,2.6100
```

Format: `heteromark_fir,<N>,<time_ms>,<GBs>`

**stderr** — human-readable results:
```
Device: AMD Radeon RX 7900 XTX (id=0)
FIR filter  |  Samples: 1048576  |  Taps: 128  |  Iterations: 5 warmup + 5 timed

Average time: 3.2145 ms
Throughput:   2.6100 GB/s
PASS
```

## Verification

- First 1024 output samples are compared against a CPU reference FIR implementation
- Tolerance: relative 1e-4 + absolute 1e-6
- Reports PASS/FAIL with mismatch details

## Platform Notes

- **CUDA/ROCm**: compiled with `nvcc`/`hipcc` from `heteromark_fir.hip`; coefficients loaded into `__shared__` memory per block
- **Metal**: compiled with `clang++` from `heteromark_fir_metal.mm`; the Metal shader (`heteromark_fir.metal`) is loaded and compiled at runtime from the same directory as the binary; coefficients loaded into `threadgroup` memory
