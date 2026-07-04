# tier2/heteromark_pagerank — PageRank (HeteroMark)

Benchmarks iterative PageRank (power method) on a **synthetic Erdős–Rényi graph** stored in CSR format. Measures throughput in edges processed per second.

Derived from the [HeteroMark benchmark suite](https://github.com/NUCAR-DEV/Hetero-Mark) PageRank workload.

## Algorithm

- **N** vertices (default: 65,536), average out-degree 16, random Erdős–Rényi graph generated at runtime with a deterministic PRNG (seed=42)
- Graph stored in CSR format (incoming edges for gather-based update)
- Iterative power method (20 iterations by default):
  - `PR[v] = (1-d)/N + d * sum(PR[u]/degree[u])` for all in-neighbors u of v
  - Damping factor d = 0.85
- Metric: `edges/sec = N_edges × PR_iterations / time_sec`

## Files

| File | Description |
|------|-------------|
| `heteromark_pagerank.hip` | HIP source — PageRank gather kernel |
| `heteromark_pagerank.metal` | Metal compute shader — PageRank gather kernel |
| `heteromark_pagerank_metal.mm` | ObjC++ Metal host code |
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
./heteromark_pagerank                                  # defaults: N=65536, 20 PR iters, 5 timed iters
./heteromark_pagerank --vertices 32768                 # smaller graph
./heteromark_pagerank --vertices 65536 --iterations 10 --pr-iterations 30
```

## Output

**stdout** — CSV timing row:
```
heteromark_pagerank,65536,45.6789,4.5678e+08
```

Format: `heteromark_pagerank,<N_vertices>,<time_ms>,<edges_per_sec>`

**stderr** — human-readable results:
```
Device: AMD Radeon RX 7900 XTX (id=0)
Vertices: 65536  |  PR iterations: 20  |  Timed iterations: 5 warmup + 5 timed

Graph: 65536 vertices, 1048576 edges (avg in-degree: 16.0)
Average time: 45.6789 ms
Throughput:   4.5678e+08 edges/sec
PASS
```

## Verification

Element-wise comparison of GPU PageRank values against CPU reference implementation with relative tolerance of 1e-4.

## Platform Notes

- **CUDA/ROCm**: compiled with `nvcc`/`hipcc` from `heteromark_pagerank.hip`; one kernel launch per PageRank iteration
- **Metal**: compiled with `clang++` from `heteromark_pagerank_metal.mm`; the Metal shader (`heteromark_pagerank.metal`) is loaded and compiled at runtime from the same directory as the binary
