# graph_cc — Graph Connected Components (Label Propagation)

## Algorithm

Computes connected components on a random undirected sparse graph using
iterative label propagation. The graph is stored in Compressed Sparse Row
(CSR) format.

Each vertex starts with its own ID as its component label. In each iteration,
every vertex updates its label to the minimum of its own label and all
neighbor labels:

```
for each vertex v in parallel:
    new_label = labels[v]
    for each neighbor u of v:
        new_label = min(new_label, labels[u])
    labels[v] = new_label
```

Iterations continue until no labels change (convergence) or the maximum
number of iterations is reached.

## Parameters

| Parameter         | Default  | Description                              |
|------------------|----------|------------------------------------------|
| `num_vertices`    | 100000   | Number of graph vertices                 |
| `avg_degree`      | 16       | Average edges per vertex                 |
| `max_iterations`  | 100      | Max label propagation iterations per run |
| `block_size`      | 256      | Threads per block                        |
| `iterations`      | 5        | Number of timed benchmark iterations     |

Parameters are read from `BENCH_PARAM_*` environment variables.

## Build & Run

```bash
# Auto-detect platform
make
./graph_cc

# Explicit platform
make PLATFORM=metal
make PLATFORM=cuda
make PLATFORM=rocm

# Custom parameters
BENCH_PARAM_num_vertices=500000 BENCH_PARAM_avg_degree=8 ./graph_cc
```

## Output Format

**JSON-lines to stdout:**
```json
{"type":"kernel","name":"cc_propagate_kernel","time_ms":15.234,"params":{"num_vertices":100000,...}}
{"type":"summary","total_time_ms":76.17,"metrics":[{"name":"mteps","value":1234.56},{"name":"avg_cc_iterations","value":12.0}]}
```

**Human-readable to stderr:**
```
Device: Apple M1
Connected Components (label propagation)  |  Vertices: 100000  |  Edges: 1600000  |  ...

Average time: 15.2340 ms
Avg CC iterations: 12.0
Throughput:   1234.56 MTEPS
PASS
```

## Metrics

- **MTEPS** (Mega Traversed Edges Per Second) = `num_edges × avg_cc_iterations / time_seconds / 1e6`
- **avg_cc_iterations** — Average number of label propagation iterations to convergence.

## Verification

Compares GPU labels against a sequential CPU reference implementation for
the first 1024 vertices. Reports PASS if all labels match exactly.
