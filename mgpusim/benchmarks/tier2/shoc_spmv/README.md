# shoc_spmv — SHOC CSR Sparse Matrix-Vector Multiply Benchmark

## Description

This benchmark implements CSR (Compressed Sparse Row) Sparse Matrix-Vector Multiplication (SpMV): **y = A × x**, derived from the SHOC (Scalable HeterOgeneous Computing) benchmark suite.

A synthetic sparse matrix is generated with exactly `NNZ_PER_ROW = 32` non-zeros per row and `NUM_ROWS = 16384` rows, giving a total of `NNZ = 524288` non-zero elements. The row-per-thread CSR SpMV kernel assigns one GPU thread to each output row.

## Algorithm

**CSR Format:**
- `row_ptr[NUM_ROWS+1]`: row i's non-zeros span `[row_ptr[i], row_ptr[i+1])`
- `col_idx[NNZ]`: column index of each non-zero
- `values[NNZ]`: floating-point value of each non-zero
- `x[NUM_ROWS]`: dense input vector
- `y[NUM_ROWS]`: dense output vector

**Kernel (one thread per row):**
```
row = blockIdx.x * blockDim.x + threadIdx.x
if row < NUM_ROWS:
    sum = 0.0
    for k = row_ptr[row] .. row_ptr[row+1]-1:
        sum += values[k] * x[col_idx[k]]
    y[row] = sum
```

**Matrix Generation:**
- `row_ptr[i] = i * NNZ_PER_ROW` (exactly 32 non-zeros per row)
- `col_idx[k]` = random column index in `[0, NUM_ROWS)`
- `values[k]` = random float in `[0, 1)`
- `x[i]` = random float in `[0, 1)`

## Files

| File | Description |
|------|-------------|
| `shoc_spmv.hip` | HIP/CUDA implementation (compiles with `hipcc` or `nvcc -x cu`) |
| `shoc_spmv.metal` | Metal compute shader (standalone reference) |
| `shoc_spmv_metal.mm` | Apple Metal host with embedded shader (self-contained) |
| `Makefile` | Build system supporting CUDA, ROCm, and Metal |
| `README.md` | This file |

## Build Instructions

```bash
# Auto-detect platform (Darwin→metal, /opt/rocm→rocm, else→cuda)
make

# Explicit platform selection
make PLATFORM=cuda
make PLATFORM=rocm
make PLATFORM=metal

# Clean
make clean
```

## Run Instructions

```bash
./shoc_spmv
```

The benchmark uses compile-time constants:
- `NUM_ROWS = 16384`
- `NNZ_PER_ROW = 32`
- `ITERATIONS = 5`

## Output Format

**stderr** — Human-readable progress:
```
Device: <device_name>
Rows: 16384  |  NNZ_per_row: 32  |  Total NNZ: 524288  |  Iterations: 5

  iter 0: X.XXXX ms
  iter 1: X.XXXX ms
  ...
Correctness check (first 32 elements): PASS

GB/s: XX.XX  GFLOPS: XX.XX  (avg X.XXXX ms, min X.XXXX ms, max X.XXXX ms, stddev X.XXXX ms)
```

**stdout** — CSV result:
```
spmv,<NUM_ROWS>,<NNZ_PER_ROW>,<avg_time_ms>,<GB/s>,<GFLOPS>
```

Example:
```
spmv,16384,32,0.2345,17.89,4.47
```

## Performance Metrics

**Memory Bandwidth (GB/s):**
```
bytes = NNZ * (sizeof(float) + sizeof(int))    # values + col_idx
      + (NUM_ROWS + 1) * sizeof(int)           # row_ptr
      + NUM_ROWS * sizeof(float)               # x (input vector)
      + NUM_ROWS * sizeof(float)               # y (output vector)
GB/s = bytes / time_s / 1e9
```

With the default parameters:
```
bytes = 524288 * 8 + 16385 * 4 + 16384 * 4 + 16384 * 4
      = 4194304 + 65540 + 65536 + 65536
      ≈ 4.39 MB
```

**Arithmetic Throughput (GFLOPS):**
```
GFLOPS = 2 * NNZ / time_s / 1e9
```
(One multiply and one add per non-zero element.)

## Correctness Verification

After the timed iterations, the GPU output `y` is compared against a CPU reference SpMV for the first 32 elements. A relative error threshold of `1e-4` is applied:
```
rel_err = |gpu - cpu| / (|cpu| + 1e-6)  < 1e-4
```

The benchmark exits with code `0` on PASS and `1` on FAIL.
