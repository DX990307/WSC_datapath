# tier2/parboil_spmv — Sparse Matrix-Vector Multiplication (Parboil SpMV)

CSR (Compressed Sparse Row) Sparse Matrix-Vector Multiplication: **y = A × x**

Each GPU thread computes one output row, performing a dot product of the corresponding sparse row of A with the dense input vector x. Measures effective GFLOPS for irregular memory-access patterns common in scientific computing.

Derived from the [Parboil benchmark suite](http://impact.crhc.illinois.edu/parboil.aspx) SpMV workload.

## Algorithm

Matrix A is stored in CSR format:
- `row_ptr[i]` and `row_ptr[i+1]` give the range of non-zeros for row `i`
- `col_idx[j]` is the column of the j-th non-zero element
- `values[j]` is the value of the j-th non-zero element

**Kernel** (`spmv_csr`): For each row, iterate over its non-zeros and accumulate:

```
y[row] = sum(values[j] * x[col_idx[j]])  for j in [row_ptr[row], row_ptr[row+1])
```

The matrix is generated randomly (no file I/O required):
- `num_rows` rows, each with a random number of non-zeros uniformly distributed around `avg_nnz_per_row`
- Column indices are spread across `[0, num_rows)` and sorted per row

## Files

| File | Description |
|------|-------------|
| `parboil_spmv.hip`       | HIP source — SpMV kernel + self-contained host code |
| `parboil_spmv.metal`     | Metal compute shader — `spmv_csr_kernel` |
| `parboil_spmv_metal.mm`  | ObjC++ Metal host code |
| `Makefile`               | Auto-detecting build (Darwin→metal, ROCm→rocm, else→cuda) |

## Build

```bash
make               # auto-detect platform
make PLATFORM=cuda
make PLATFORM=rocm
make PLATFORM=metal
```

## Run

```bash
./parboil_spmv                                          # defaults: 65536 rows, 10 nnz/row, 10 iters
./parboil_spmv --num_rows 131072
./parboil_spmv --num_rows 65536 --avg_nnz_per_row 20 --iterations 20
```

## Output

**stdout** — CSV timing row:
```
kernel_name,problem_size,iterations,avg_ms,min_ms,max_ms,stddev_ms
parboil_spmv,65536,10,0.8234,0.8100,0.8600,0.0150
```

**stderr** — human-readable results:
```
Device: Apple M4 Pro
Rows: 65536  |  NNZ: 655360  |  Iterations: 10

parboil_spmv,65536,1.5900
GFLOPS: 1.5900  (avg 0.8234 ms)
PASS
```

The `stderr` line `parboil_spmv,<num_rows>,<GFLOPS>` is the canonical CSV metric.

## Parameters

| Flag | Default | Description |
|------|---------|-------------|
| `--num_rows N` | 65536 | Number of matrix rows (= number of columns) |
| `--avg_nnz_per_row K` | 10 | Average non-zeros per row |
| `--block_size B` | 256 | GPU thread-block size (CUDA/ROCm only) |
| `--iterations I` | 10 | Number of timed kernel launches |
| `--seed S` | 42 | RNG seed for matrix generation |

## Performance Metric

```
GFLOPS = 2 × total_nnz / avg_time_seconds / 1e9
```

Factor of 2 accounts for one multiply and one add per non-zero element.

## Platform Notes

- **CUDA/ROCm**: compiled with `nvcc`/`hipcc` from `parboil_spmv.hip`
- **Metal**: compiled with `clang++` from `parboil_spmv_metal.mm`; the Metal shader `parboil_spmv.metal` is loaded at runtime from the same directory as the binary
- Input data is generated synthetically — no external input files required
- Verification checks 100 sampled rows against CPU reference output
