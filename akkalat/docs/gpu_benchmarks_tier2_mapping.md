# GPU Benchmarks Tier2 Mapping

`mgpusim/benchmarks/tier2` contains the copied tier2 CUDA/HIP workloads from
`gpu_benchmarks/tier2`. The current Akita/MGPUSim runner cannot execute the
CUDA/HIP kernels directly, so the selected tier2 names are exposed as separate
benchmark entries and backed by local tier2 Go packages that embed
`kernels.hsaco`.

Each name is kept separate even when two names use the same current MGPUSim
implementation. This keeps result files separate and makes later exact ports or
per-workload sizes easy to add.

For simulator support, use
`mgpusim/benchmarks/tier2/compile_hsaco_all.py`. It compiles OpenCL simulator
sources from `sim/*.cl`, `native/*.cl`, or `*.cl` into `.hsaco` files. The 11
benchmarks below now have `sim/kernels.cl`, generated `kernels.hsaco`, and Go
wrappers that load the local `kernels.hsaco`. The remaining tier2 workloads
still report `missing_source` until a `sim/kernels.cl` or equivalent OpenCL
simulator source is added.

```bash
python3 mgpusim/benchmarks/tier2/compile_hsaco_all.py --jobs 8
```

| Tier2 name | Simulator package | Status |
| --- | --- | --- |
| `rodinia_bfs` | `tier2/rodinia_bfs` | local BFS hsaco wrapper |
| `graph_pr` | `tier2/graph_pr` | local PageRank hsaco wrapper |
| `heteromark_pagerank` | `tier2/heteromark_pagerank` | local PageRank hsaco wrapper |
| `lonestar_sssp` | `tier2/lonestar_sssp` | BFS-style graph traversal proxy |
| `shoc_spmv` | `tier2/shoc_spmv` | local SpMV hsaco wrapper |
| `parboil_spmv` | `tier2/parboil_spmv` | SpMV proxy |
| `npb_cg` | `tier2/npb_cg` | sparse matrix/vector proxy for CG |
| `altis_gups` | `tier2/altis_gups` | irregular memory proxy; no native GUPS port yet |
| `cuda_transpose` | `tier2/cuda_transpose` | local transpose hsaco wrapper |
| `shoc_stencil2d` | `tier2/shoc_stencil2d` | local stencil hsaco wrapper |
| `rodinia_hotspot` | `tier2/rodinia_hotspot` | 2D stencil-like proxy |

Run the full group with:

```bash
python3 akkalat/runall2.py --benchmarks tier2 --configs sample_all --mechanisms baseline
```
