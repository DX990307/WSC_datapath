# tier2/lonestar_dmr — Delaunay Mesh Refinement (LoneStar DMR)

Benchmarks worklist-based irregular mesh refinement on GPU. Generates a synthetic Delaunay-like triangulation, identifies "bad" triangles (minimum angle below a quality threshold), and refines them by moving vertices toward circumcenters (simplified Ruppert's algorithm).

Derived from the [LoneStar GPU benchmark suite](https://iss.oden.utexas.edu/?p=projects/galois/lonestar) DMR workload.

## Algorithm

1. **dmr_check_kernel** — Scan all triangles in parallel, compute the minimum interior angle of each. Triangles with min angle < threshold are appended to a worklist via atomic operations.
2. **dmr_refine_kernel** — Process worklist entries in parallel. For each bad triangle, compute the circumcenter and blend vertex positions toward it (atomic float adds for concurrent safety).
3. Repeat check→refine for `max_refine_iterations` rounds (default: 5).

The benchmark exercises irregular memory access patterns (worklist, atomic operations), which are characteristic of graph and mesh algorithms on GPUs.

## Files

| File | Description |
|------|-------------|
| `lonestar_dmr.cu` | CUDA source — check + refine kernels |
| `lonestar_dmr.hip` | HIP source — with HIP/CUDA compat layer |
| `lonestar_dmr.metal` | Metal compute shaders |
| `lonestar_dmr_metal.mm` | ObjC++ Metal host code |
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
./lonestar_dmr                          # defaults: 100000 triangles, 5 iters
./lonestar_dmr --size 200000
./lonestar_dmr --size 50000 --min_angle 25
BENCH_PARAM_num_triangles=200000 ./lonestar_dmr
```

## Output

**stdout** — JSON-lines:
```json
{"type":"kernel","name":"dmr_check_kernel","time_ms":1.234,"params":{"num_triangles":100000,...}}
{"type":"kernel","name":"dmr_refine_kernel","time_ms":1.234,"params":{"num_triangles":100000,...}}
{"type":"summary","total_time_ms":2.468,"metrics":[{"name":"mtri_per_sec","value":123.45}]}
```

**stderr** — human-readable device info and timing details.

## Key Metrics

- **Mtri/sec** — millions of triangles processed per second (input triangles / wall time)
