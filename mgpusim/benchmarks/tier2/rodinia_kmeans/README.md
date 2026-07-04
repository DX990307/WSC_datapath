# rodinia_kmeans — Rodinia K-Means Clustering Benchmark

GPU implementation of the K-Means clustering algorithm, derived from the
[Rodinia benchmark suite](https://rodinia.cs.virginia.edu/).

K-Means partitions **N** D-dimensional data points into **K** clusters by
iteratively assigning each point to the nearest cluster center and
recomputing the centers.

## Algorithm

1. **Initialize** N random points in [0,1)^D; use the first K as initial centers.
2. **Assign** — GPU kernel assigns each point to its nearest cluster center
   (one thread per point; computes squared Euclidean distance to all K centers).
3. **Update** — GPU kernel accumulates per-cluster point sums using atomics;
   host divides to get new centers.
4. **Repeat** until fewer than 0.01% of assignments change or `max_iter` is reached.
5. **Verify** — run one extra assign pass and check final delta < 1%.

## Files

| File | Description |
|------|-------------|
| `rodinia_kmeans.hip` | HIP/CUDA implementation (two GPU kernels + host loop) |
| `rodinia_kmeans.metal` | Metal compute shaders (assign + update kernels) |
| `rodinia_kmeans_metal.mm` | Objective-C++ Metal host code |
| `Makefile` | Platform auto-detect build system |
| `README.md` | This file |

## Build

```bash
# Auto-detect platform (Darwin → metal, /opt/rocm → rocm, else → cuda)
make

# Explicit platform
make PLATFORM=cuda
make PLATFORM=rocm
make PLATFORM=metal

# Clean
make clean
```

## Run

```bash
# Default: N=16384 points, D=16 dims, K=8 clusters, max_iter=10
./rodinia_kmeans

# Custom parameters
./rodinia_kmeans --points 65536 --dims 32 --clusters 16 --max_iter 20
```

## Output

Per-iteration convergence info is printed to **stderr**:

```
  iter  1: changed=12345   delta=0.752930
  iter  2: changed=1234    delta=0.075317
  ...
Final stability check: changed=0  delta=0.000000  PASS
```

Performance metrics are printed to **stdout** in CSV format:

```
kmeans,N,K,D,<iterations>,<time_ms>,<Gpoints_per_s>
```

Example:

```
kmeans,16384,8,16,7,12.3456,0.009302
```

### Columns

| Column | Description |
|--------|-------------|
| `N` | Number of data points |
| `K` | Number of clusters |
| `D` | Number of dimensions |
| `iterations` | Actual iterations run |
| `time_ms` | Total GPU time in milliseconds |
| `Gpoints_per_s` | Throughput: N × iterations / time_s / 1e9 |

## Platform Notes

- **CUDA/ROCm**: Uses `hipEvent_t` for timing; atomicAdd for center updates.
- **Metal**: Uses `atomic_fetch_add_explicit` with `memory_order_relaxed` on
  device-side `atomic_float` / `atomic_int`. Shader source is loaded at
  runtime from the same directory as the binary.
- The Metal `.metal` file must be present alongside the binary at runtime.

## Reference

Rodinia: A Benchmark Suite for Heterogeneous Computing.
S. Che et al., IEEE IISWC 2009.
