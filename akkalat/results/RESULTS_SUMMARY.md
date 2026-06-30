# Results Summary

Updated: 2026-06-30

## Cleanup

The results directory was cleaned to keep final results and remove raw/detail data that is not needed for summary analysis.

Kept:
- `*_metrics.csv`
- high-level `*_summary.csv`
- figures: `*.png`, `*.pdf`
- reports: `*.md`
- logs/stdout for run audit

Removed:
- raw traces: `*_raw.csv.gz`
- detailed L2 source tables: `*_l2_source_data_source.csv`, `*_l2_source_remote_fill_reuse.csv`, `*_l2_source_remote_matrix.csv`, `*_l2_source_page_source.csv`
- detailed L1V path tables: `*_memory_path_l1v_path_summary.csv`, `*_memory_path_l1v_path_stage_summary.csv`
- sharing trace tables: `*_sharing.csv.gz`, `*_sharing_pages.csv`
- per-cacheline/page detail tables: `*_memory_path_specific_cacheline_summary.csv`, `*_memory_path_specific_page_summary.csv`
- empty dry-run directories and smoke-only M2 directories

After cleanup:
- `akkalat/results` size: about 1.3 GB
- top-level result directories: 39
- metrics CSV files: 409
- summary CSV files: 391

## Current M1/M2 Results

Primary directory:

`2026-06-29-00-33-57-runall`

This is the current baseline/M1/M2/M1+M2 comparison directory. It contains 46 metrics files and 4 figures. The sweep is not complete: traditional benchmarks x 4 mechanisms should produce 56 metrics files.

| benchmark | baseline ms | M1 improvement | M2 improvement | M1+M2 improvement | M1 AU coalesced | M2 batchable |
|---|---:|---:|---:|---:|---:|---:|
| bitonicsort | 0.084 | +1.23% | +0.00% | +1.23% | 1,704,008 | 0 |
| fastwalshtransform | 0.367 | +1.76% | +0.17% | +2.15% | 8,403,690 | 795,473 |
| fft | 0.967 | +0.38% | +0.00% | +0.38% | 2,094,686 | 0 |
| fir | 1.151 | +467.26% | missing | +476.61% | 41,814 | missing |
| floydwarshall | 0.093 | -0.55% | +6.76% | +6.88% | 289,303 | 735,435 |
| im2col | 0.116 | +0.57% | +0.48% | +0.43% | 63,110 | 263,450 |
| matrixmultiplication | 10.086 | +37.68% | +38.44% | +40.33% | 640 | 2,211,162 |
| matrixtranspose | 27.154 | missing | missing | +0.02% | missing | missing |
| pagerank | 11.352 | -3.74% | +1.10% | -8.02% | 525,452 | 11,743,705 |
| relu | 0.083 | +0.05% | +0.00% | +0.05% | 1,583,072 | 0 |
| resnet | 1.824 | missing | -0.01% | missing | missing | 894,355 |
| simpleconvolution | 0.061 | +44.00% | -0.15% | +36.03% | 55,031 | 376 |
| spmv | 139.115 | missing | +6.77% | -1.06% | missing | 68,106,949 |

Missing traditional combinations:

| benchmark | missing mechanisms |
|---|---|
| aes | baseline, m1, m2, m1_m2 |
| fir | m2 |
| kmeans | baseline, m1, m2, m1_m2 |
| matrixtranspose | m1, m2 |
| spmv | m1 |

Interpretation notes:
- M1 is clearly active in workloads with nonzero AU coalescing.
- M2 is only active when there are remote RDMA batchable reads.
- Some large improvements, especially `fir` and `simpleconvolution`, should be treated carefully until the missing arms are rerun and compared against repeat runs.
- `pagerank` shows M2 alone helps slightly, but M1+M2 is worse in this run.

## Important Directories

| directory | status | notes |
|---|---|---|
| `2026-06-29-00-33-57-runall` | keep | current M1/M2 comparison; incomplete but most important current result |
| `m1-baseline-vs-mechanism1` | keep | older M1-only comparison with broader benchmark coverage |
| `m1-dir-au-batch-no-trace` | keep | M1 L2 directory/AU batching evidence without raw trace |
| `m2-trace-remote-heavy-300k` | keep | M2 remote-heavy trace analysis; raw trace removed, summaries kept |
| `m2-trace-smoke` | keep | small M2 trace sanity result; raw trace removed, summaries kept |
| `2026-06-26-datapath-batching-report` | keep | datapath batching report and figures |
| `dram_saturation_analysis` | keep | DRAM saturation plots and summary |
| `2026-06-10-matmul-progress-wafer` | keep | matrix multiplication progress plots |
| `Baseline`, `Baseline1Netlatency`, `BaselineL1V160160`, `BaselineL1V1601601Netlatency` | keep | historical baseline variants, summaries only |
| `2026-06-06*` to `2026-06-25* sampled-validation` | archive | older sampled validation and L1V/path evidence; raw details removed |

## Next Useful Step

Rerun the missing combinations in `2026-06-29-00-33-57-runall` before treating the M1/M2 comparison as a final ablation table.
