# graph_pr — Graph PageRank (Pull-Based)

## Algorithm

Computes PageRank on a random sparse directed graph using the iterative
pull-based method. The graph is stored in Compressed Sparse Column (CSC) format
for efficient pull access.

Each iteration launches one thread per vertex. Each thread computes its new
rank by summing contributions from all incoming neighbors:

```
PR[v] = (1 - d) / N + d * Σ (PR[u] / out_degree[u])  for all u → v
```

where `d = 0.85` is the damping factor.

## Parameters

| Parameter          | Default | Description                    |
|-------------------|---------|--------------------------------|
| `--vertices`       | 131072  | Number of graph vertices       |
| `--degree`         | 16      | Average edges per vertex       |
| `--iterations`     | 5       | Number of timed iterations     |
| `--pr-iterations`  | 20      | PageRank iterations per run    |

## Build & Run

```bash
# Auto-detect platform
make
./graph_pr

# Explicit platform
make PLATFORM=metal
make PLATFORM=cuda
make PLATFORM=rocm

# Custom parameters
./graph_pr --vertices 65536 --degree 8 --pr-iterations 30
```

## Output Format

**CSV to stdout:**
```
graph_pr,<N_vertices>,<time_ms>,<GTEPS>
```

**Human-readable to stderr:**
```
Device: Apple M1
PageRank (pull-based)  |  Vertices: 131072  |  Edges: 2097152  |  PR iterations: 20  |  Damping: 0.85  |  Iterations: 5 warmup + 5 timed

Average time: 45.1234 ms
Throughput:   0.9300 GTEPS
PASS
```

## Metric

**GTEPS** (Giga Traversed Edges Per Second) = `N_edges × PR_iterations / time_seconds / 1e9`

Measures the effective edge traversal throughput across all PageRank iterations.

## Verification

Compares GPU PageRank values against a sequential CPU reference implementation
for the first 1024 vertices. Reports PASS if all values match within 1e-4
absolute tolerance.
