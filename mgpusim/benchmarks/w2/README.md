# W2 ports, group a (graph and sparse solver)

Ports of four HIP benchmarks from `sarchlab/gpu_benchmarks` tier2 to OpenCL C
for the CuPath simulator, following the T17 rules: unified kernels over the
48-GPM wafer, default allocator, no atomics, no scratch, only implemented DS
opcodes, fp32, host-built index structures, synthetic deterministic inputs.

| -benchmark | upstream | kernels | flags |
|---|---|---|---|
| `graphtc` | `tier2/graph_tc` | `tc_count` (a wave per edge) | `-tc-scale -tc-edge-factor -tc-seed` |
| `bfs` | `tier2/rodinia_bfs` | `bfs_expand` (a wave per vertex), `bfs_update` (a thread per vertex), per level | `-bfs-scale -bfs-edge-factor -bfs-seed` |
| `sssp` | `tier2/lonestar_sssp` | `sssp_pull` (a wave per vertex), per round | `-sssp-scale -sssp-edge-factor -sssp-seed -sssp-max-rounds` |
| `npbcg` | `tier2/npb_cg` | `cg_init/spmv/dot/reduce/axpy/direction/normalize` | `-cg-class -cg-outer -cg-inner` |

Defaults are small smoke sizes (scale 12, class S).

## Inputs

- Graphs (`rmatgraph`): R-MAT / Kronecker with the Graph500/GAP parameters
  (A=0.57, B=0.19, C=0.19), 2^scale vertices, edge-factor x 2^scale generated
  edges, made undirected, self loops and duplicates removed, adjacency sorted,
  vertex ids randomly permuted. Upstream uses uniform random graphs. SSSP
  weights are 1..100 per undirected edge (upstream: 1..100). The BFS/SSSP
  source is a random non-isolated vertex (GAP); upstream uses vertex 0.
- CG: the NAS `makea` matrix (ported from `nas_cg.h`; the fp64 Go reference
  reproduces the published class S zeta), x = 1.

## Deviations from upstream

- No atomics. BFS: `atomicExch` stores become plain stores (all writers store
  the same value); the `updated` flag is dropped. SSSP: push `atomicMin`
  becomes pull rounds (same round-by-round distances). CG: dot products are
  per-WG partials plus a one-WG `cg_reduce`; alpha and beta are computed by
  the kernels that use them, so there is no host round trip between kernels.
- Iteration counts (BFS levels, SSSP rounds) come from the host reference and
  equal upstream's loop counts, including the last iteration that changes
  nothing.
- Fresh buffers per iteration (BFS frontier/visited/next, SSSP dist, every CG
  vector, partial and scalar). With magic copies the timing model never
  invalidates the L1 caches between kernels, so a buffer that a later kernel
  rewrites could be read stale from an L1V.
- Mapping: a 64-lane wave per edge (TC) or per vertex (BFS expand, SSSP),
  instead of upstream's thread. On R-MAT a hub vertex becomes one serial loop
  of thousands of dependent loads, and the kernel turns into a latency-bound
  tail. TC intersects by binary search of the shorter list in the longer one
  (upstream: a merge; the count is the same). CG SpMV uses a wave per row
  (the pinned CUDA code uses a block per row).
- CG: fp32 (upstream fp64); default 1 outer iteration (26 SpMVs) instead of
  NAS's 15 or 75. TC: per-edge counts are 32-bit.
- Kernels with few WGs: BFS `bfs_update` has n/256 WGs, CG vector kernels
  n/64 (219 at class A), `cg_reduce` 1. They are short and local.

## Checks

`build_hsaco.sh` and `opcheck` from `../t17` (run `t17/build_hsaco.sh
$PWD/w2/<name>` on yss2). Unit tests: `go test ./benchmarks/w2/...` (host
references against brute force / Dijkstra / the kernels' algorithm on the
host; CG reproduces the NAS class S zeta). Emulation with `-verify` compares
with the Go CPU references (TC raw count, BFS and SSSP every distance, CG
zeta and x within fp32 tolerances).
