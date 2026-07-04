# tier2/graph_tc — Triangle Counting (Graph Analytics)

Counts the number of triangles in an undirected graph using GPU-accelerated sorted adjacency list intersection. Each GPU thread processes one edge and counts common neighbors via a merge-based intersection of sorted neighbor lists.

Derived from common graph analytics triangle counting workloads.

## Algorithm

- Generate a random undirected graph with **N** vertices and average degree **D** (default: N=16,384, D=32)
- Store graph in CSR (Compressed Sparse Row) format with sorted adjacency lists
- For each directed edge (u, v) with u < v, count common neighbors of u and v by intersecting their sorted neighbor lists
- Each triangle (a, b, c) is counted 3 times (once per edge), so divide total by 3
- triangles/sec = total\_triangles / time\_sec

## Files

| File | Description |
|------|-------------|
| `graph_tc.hip` | HIP source — triangle counting kernel, graph generation, verification |
| `graph_tc.metal` | Metal compute shader — triangle counting kernel |
| `graph_tc_metal.mm` | ObjC++ Metal host code |
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
./graph_tc                    # defaults: N=16384, D=32
./graph_tc N=8192 D=16        # smaller graph
./graph_tc N=32768 D=64       # larger graph
```

## Output

**stdout** — CSV timing row:
```
graph_tc,16384,12.3456,78901234.5678
```

Format: `graph_tc,<N_vertices>,<time_ms>,<triangles_per_sec>`

**stderr** — human-readable results:
```
Device: AMD Radeon RX 7900 XTX (id=0)
Triangle Counting  |  Vertices: 16384  |  Avg Degree: 32  |  Iterations: 5 warmup + 5 timed

Generating random graph...
Graph: 16384 vertices, 524288 directed edges (CSR nnz)
Undirected edges (u<v): 262144
Triangles found:  12345
Average time:     12.3456 ms
Throughput:       78901234.5678 triangles/sec
Running CPU verification...
PASS (CPU=12345, GPU=12345 triangles)
```

## Verification

- GPU triangle count is compared against a CPU reference implementation
- Both use the same sorted adjacency list intersection algorithm
- Reports PASS/FAIL with counts

## Platform Notes

- **CUDA/ROCm**: compiled with `nvcc`/`hipcc` from `graph_tc.hip`; one thread per edge
- **Metal**: compiled with `clang++` from `graph_tc_metal.mm`; the Metal shader (`graph_tc.metal`) is loaded and compiled at runtime from the same directory as the binary
