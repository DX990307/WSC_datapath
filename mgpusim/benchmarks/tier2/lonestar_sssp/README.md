# lonestar_sssp — Single-Source Shortest Path (Bellman-Ford)

## Algorithm

Computes single-source shortest paths on a random sparse directed graph using
the Bellman-Ford algorithm on the GPU. The graph is stored in Compressed Sparse
Row (CSR) format with random integer edge weights (1–100).

Each iteration launches one thread per vertex. Each thread relaxes all outgoing
edges from its vertex using atomic min operations on the distance array. The
algorithm repeats until no distance is updated (convergence).

This is inspired by the Lonestar GPU benchmark suite's SSSP implementation.

## Parameters

| Parameter    | Default | Description                    |
|-------------|---------|--------------------------------|
| `--vertices` | 65536   | Number of graph vertices       |
| `--degree`   | 16      | Average edges per vertex       |
| `--iterations`| 5      | Number of timed iterations     |

## Build & Run

```bash
# Auto-detect platform
make
./lonestar_sssp

# Explicit platform
make PLATFORM=metal
make PLATFORM=cuda
make PLATFORM=rocm

# Custom parameters
./lonestar_sssp --vertices 131072 --degree 8
```

## Output Format

**CSV to stdout:**
```
lonestar_sssp,<N_vertices>,<time_ms>,<Medges_per_sec>
```

**Human-readable to stderr:**
```
Device: Apple M1
SSSP (Bellman-Ford)  |  Vertices: 65536  |  Edges: 1048576  |  Iterations: 5 warmup + 5 timed

Bellman-Ford converged in 8 iterations
Average time: 12.3456 ms
BF iterations (avg): 8.0
Throughput:   682.1234 Medges/sec
PASS
```

## Metric

**Medges/sec** = `N_edges × BF_iterations / time_seconds / 1e6`

Measures the effective edge processing throughput including all Bellman-Ford
iterations required for convergence.

## Verification

Compares GPU shortest-path distances against a sequential CPU Bellman-Ford
reference for the first 1024 vertices. Reports PASS/FAIL.
