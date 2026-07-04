# tier2/rodinia_bfs — Breadth-First Search (Rodinia BFS)

Iterative BFS on a randomly-generated CSR (Compressed Sparse Row) graph.
Two GPU kernels are launched per BFS level, iterated until the traversal completes. Measures effective memory bandwidth of irregular graph traversal.

Derived from the [Rodinia benchmark suite](https://rodinia.cs.virginia.edu/) BFS workload.

## Algorithm

The graph is stored in Compressed Sparse Row (CSR) format:
- `row_offsets[i]` and `row_offsets[i+1]` delimit the edges of node `i`
- `col_indices[e]` gives the destination node of edge `e`
- `cost[v]` holds the BFS level of node `v` (−1 if not yet visited)

Each BFS level executes two kernel passes:

**Pass 1 — bfs_kernel**: For each node in the current frontier, write `cost[neighbor] = cost[node] + 1` for all unvisited neighbors.

**Pass 2 — bfs_update**: Promote nodes that were just discovered (`cost ≥ 0` but not yet `visited`) into the next frontier and set `visited[v] = 1`.

Both passes are iterated until no new nodes are added to the frontier.

## Files

| File | Description |
|------|-------------|
| `rodinia_bfs.hip`        | HIP source — BFS kernels + self-contained utilities |
| `rodinia_bfs.metal`      | Metal compute shaders — `bfs_kernel` and `bfs_update` |
| `rodinia_bfs_metal.mm`   | ObjC++ Metal host code |
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
./rodinia_bfs                                        # defaults: 65536 nodes, 20 edges/node, 20 iters
./rodinia_bfs --num_nodes 131072
./rodinia_bfs --num_nodes 65536 --edges_per_node 10 --iterations 50
```

## Output

**stdout** — CSV timing row:
```
kernel_name,problem_size,iterations,avg_ms,min_ms,max_ms,stddev_ms
rodinia_bfs,65536,20,4.1234,4.0000,4.5000,0.1100
```

**stderr** — human-readable results:
```
Device: Apple M4 Pro
Nodes: 65536  |  Edges: 1310720  |  Iterations: 20

Effective bandwidth: 12.34 GB/s  (avg 4.1234 ms/BFS)
```

## Platform Notes

- **CUDA/ROCm**: compiled with `nvcc`/`hipcc` from `rodinia_bfs.hip`
- **Metal**: compiled with `clang++` from `rodinia_bfs_metal.mm`; the Metal shader is loaded at runtime from the same directory as the binary
- Input data is generated synthetically — no external input files required
- The benchmark measures traversal bandwidth, not correctness; concurrent cost writes for the same neighbor are benign (all threads write the same value)
