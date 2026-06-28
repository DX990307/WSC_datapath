# WSG Baseline Model

This repository contains a research prototype for studying memory-system
behavior on a wafer-scale GPU (WSG) model.  The current work builds on
MGPUSim/Akkalat-style GPU simulation, Photon-style sampled execution, and a set
of added tracing/analysis tools for data sharing, remote accesses, L1V cache
paths, RDMA/network paths, and per-GPU execution imbalance.

The main experiment driver lives in `akkalat/`.  The simulator implementation
and benchmark code live in `mgpusim/` and `akita/`.  The `photon/` directory is
kept for comparison against the vanilla Photon implementation.

## Repository Layout

```text
.
|-- akita/                 Akita simulation framework.
|-- mgpusim/               GPU simulator, benchmarks, memory hierarchy changes.
|-- akkalat/               Main experiment drivers, tracing, and analysis scripts.
|-- photon/                Vanilla Photon reference code.
|-- bottleneck analysis/   Earlier copied sandbox for bottleneck experiments.
|-- weeklyreport/          Weekly report LaTeX sources and generated figures.
`-- README.md
```

Important `akkalat/` files:

```text
akkalat/runall2.py                    Main traditional/LLM-like benchmark runner.
akkalat/run_m1_clean_compare.py       Baseline vs current Mechanism 1 runner.
akkalat/runllm_decomposed.py          Runs decomposed GPT/BERT operators.
akkalat/baseline/runner/flag.go       Benchmark binary flags.
akkalat/baseline/runner/shaderarray.go
                                      WSG cache/CU configuration.
```

## Prerequisites

The codebase is primarily Go plus Python analysis scripts.

Recommended environment:

```sh
go version
python3 --version
python3 -m pip install --user numpy pandas
```

The simulator is large, so keep `GOCACHE` outside the repo if disk churn becomes
annoying:

```sh
export GOCACHE=/tmp/gocache
```

## Build

Most experiment scripts build the target binary automatically.  To build the
main Akkalat baseline binary manually:

```sh
cd /home/daoxuanxu/dataSharing/WSG-baseline-model/akkalat/baseline
GOCACHE=/tmp/gocache go build -buildvcs=false
```

## Running Traditional Workloads

Run the traditional benchmark set with the baseline configuration:

```sh
cd /home/daoxuanxu/dataSharing/WSG-baseline-model

python3 akkalat/runall2.py \
  --benchmarks traditional \
  --configs baseline \
  --max-workers 10
```

Run a Photon-style sampled configuration:

```sh
python3 akkalat/runall2.py \
  --benchmarks traditional \
  --configs sample_all \
  --sampled-warmups 512 \
  --sampled-granularities 512 \
  --max-workers 10
```

Useful benchmark presets are defined in `akkalat/runall2.py`:

```text
traditional
traditional-lite
llm-like-bottleneck
llm
experimental
all
```

Useful configs are:

```text
baseline
sample_all
sample_all_loop
sample_wf
sample_branch
sample_kernel
sample_loop
```

## Running LLM-Like Bottleneck Workloads

The LLM-like benchmark set contains matrix multiplication and Conv2D shapes
that mimic prefill/decode-style pressure points:

```sh
python3 akkalat/runall2.py \
  --benchmarks llm-like-bottleneck \
  --configs baseline \
  --max-workers 4
```

For decomposed GPT/BERT operators:

```sh
cd /home/daoxuanxu/dataSharing/WSG-baseline-model/akkalat

python3 runllm_decomposed.py \
  --model gpt \
  --profile gpt-7b \
  --layers 1 \
  --seq-len 128 \
  --split-k 8 \
  --configs baseline \
  --max-workers 1 \
  --summarize
```

## Mechanism 1: Cache/DRAM-Friendly L2 Miss Batching

Mechanism 1 is the current simulator-side mechanism for improving the L2-to-DRAM
datapath. It focuses on locality that already exists in the request stream and
tries to expose it to the simulated L2 cache and DRAM timing path.

Mechanism 1 currently has two parts:

1. L2 directory same-set batching.
2. L2 miss to DRAM 128B access-unit coalescing.

The intended claim is not that Mechanism 1 always improves total runtime. The
intended lower-level claim is:

```text
Mechanism 1 can reduce DRAM command/response pressure and improve the L2/DRAM
datapath latency. End-to-end speedup depends on whether that latency is on the
critical path and whether the 128B access unit causes extra read traffic.
```

### Design Intuition

The cache and DRAM timing model expose two natural batching opportunities.

First, directory lookup selects a cache set and scans multiple ways. If several
pending requests target the same directory set, the simulator can process them
as a small same-set group instead of treating every request as an isolated
lookup. This is modeled by the L2 directory same-set batching path.

Second, a 64B L2 miss is serviced by DRAM timing at a 128B access-unit
granularity. Two neighboring 64B cachelines can therefore share one 128B DRAM
access unit. Mechanism 1 coalesces such adjacent L2 miss fills when possible,
reducing DRAM read transaction count and DRAM response pressure.

The 128B coalescing path is currently the stronger part of Mechanism 1. The L2
directory batching path is often weak in the current workloads because the
average observed batch size is close to 1.

### Implementation Locations

Important implementation files:

```text
akita/mem/cache/writeback/directorystage.go
  L2 directory same-set batch collection and accounting.

akita/mem/cache/writeback/writebufferstage.go
  L2 miss to DRAM 128B access-unit coalescing.

akita/mem/cache/writeback/writebackcache.go
  L2BatchStats definition and per-cache statistic storage.

akita/mem/cache/writeback/builder.go
  Cache builder options for Mechanism 1.

akkalat/baseline/runner/flag.go
  User-facing benchmark flags:
    -l2-dir-batch-window
    -l2-dram-access-unit-coalesce

akkalat/baseline/runner/report.go
  Reports Mechanism 1 counters into *_metrics.csv.

akkalat/baseline/runner/runner.go
akkalat/baseline/runner/r9nanobuilder.go
akkalat/baseline/runner/timingplatform.go
  Propagate Mechanism 1 configuration into the simulated platform.

akkalat/runall2.py
  Generic experiment runner; forwards Mechanism 1 flags.

akkalat/run_m1_clean_compare.py
  Baseline vs Mechanism 1 paired experiment runner.
```

### Simulator Flags

Mechanism 1 is disabled by default. Enable it with:

```sh
-l2-dir-batch-window=4
-l2-dram-access-unit-coalesce
```

The Python runner exposes the same settings as:

```sh
--l2-dir-batch-window 4
--l2-dram-access-unit-coalesce
```

The current default Mechanism 1 experiment uses a directory batch window of 4.
The baseline arm does not receive either flag.

### Clean Baseline vs Mechanism 1 Run

Use `run_m1_clean_compare.py` for the clean paired experiment. It reuses the
same experiment construction and process runner as `runall2.py`, but creates two
arms:

- `baseline`
- `mechanism1`

Both arms use identical benchmark/config/sample/max-wg settings. The only
difference is that the `mechanism1` arm additionally enables:

```text
-l2-dir-batch-window=<N>
-l2-dram-access-unit-coalesce
```

Recommended command:

```sh
python3 akkalat/run_m1_clean_compare.py \
  --arm baseline,mechanism1 \
  --benchmarks all \
  --configs sample_all \
  --output-root akkalat/results/m1-baseline-vs-mechanism1 \
  --max-workers 10 \
  --max-wg 78600 \
  --sampled-warmups 512 \
  --sampled-granularities 512
```

If the run is interrupted, resume missing metrics:

```sh
python3 akkalat/run_m1_clean_compare.py \
  --arm baseline,mechanism1 \
  --benchmarks all \
  --configs sample_all \
  --output-root akkalat/results/m1-baseline-vs-mechanism1 \
  --max-workers 10 \
  --max-wg 78600 \
  --sampled-warmups 512 \
  --sampled-granularities 512 \
  --resume
```

After a run finishes, regenerate only the comparison CSV:

```sh
python3 akkalat/run_m1_clean_compare.py \
  --arm baseline,mechanism1 \
  --output-root akkalat/results/m1-baseline-vs-mechanism1 \
  --compare-only
```

Output files are written into one directory with arm-prefixed metric names:

```text
akkalat/results/m1-baseline-vs-mechanism1/
  commands.txt
  summary.csv
  baseline_<benchmark>_baseline_<config>_metrics.csv
  baseline_<benchmark>_mechanism1_<config>_metrics.csv
```

### Mechanism 1 Metrics

Mechanism 1 emits per-L2 and Driver-level counters into `*_metrics.csv`.
Important Driver-level fields include:

```text
l2_batch_dir_groups
l2_batch_dir_requests
l2_batch_dir_avg_size
l2_batch_dir_max_size
l2_batch_access_unit_reads
l2_batch_access_unit_coalesced
```

Interpretation:

- `l2_batch_dir_avg_size`: average number of directory requests per same-set
  batch. Values close to 1 mean directory batching is mostly ineffective.
- `l2_batch_dir_max_size`: largest observed directory batch.
- `l2_batch_access_unit_reads`: number of DRAM 128B access-unit reads issued
  after coalescing.
- `l2_batch_access_unit_coalesced`: number of 64B L2 miss fills that were
  coalesced into an existing 128B access-unit read.

A useful derived metric is:

```text
access_unit_saved_pct =
  l2_batch_access_unit_coalesced /
  (l2_batch_access_unit_reads + l2_batch_access_unit_coalesced)
```

DRAM-side evidence comes from the standard DRAM metrics:

```text
read_trans_count
read_avg_latency
read_size
write_trans_count
write_avg_latency
write_size
```

For DRAM response time, use a transaction-weighted average over DRAM
components:

```text
sum(read_avg_latency * read_trans_count) / sum(read_trans_count)
```

### What To Check When Analyzing Results

Do not judge Mechanism 1 only by `total_time`. Check the datapath first:

1. Did both baseline and mechanism reach `max_wg`?
   Check `max_wg_reached` and `total_wg_count`.
2. Did DRAM read transaction count decrease?
   Compare summed `read_trans_count` across DRAM components.
3. Did DRAM read response time decrease?
   Compare transaction-weighted `read_avg_latency`.
4. Did L1V and L2 request latency decrease?
   Compare request-count-weighted `req_average_latency` for L1V and L2.
5. Did DRAM read bytes increase?
   Compare summed `read_size`. Increased bytes can offset fewer transactions.
6. Did directory batching actually batch?
   Check `l2_batch_dir_avg_size`; values near 1 mean almost no useful batch.

### Current Observed Behavior

The current partial comparison shows clear datapath improvement but weak and
mixed runtime behavior.

Available cross-directory datapath comparison:

```text
baseline source:
  akkalat/results/m1-baseline-vs-mechanism1

Mechanism 1 source:
  akkalat/results/m1-dir-au-batch-no-trace
```

Caveat: this is not a strict paired runtime comparison because
`m1-baseline-vs-mechanism1` currently has only completed baseline metrics, while
`m1-dir-au-batch-no-trace` contains older Mechanism 1 runs. Use it for datapath
evidence only. A clean paired run should be used for final runtime claims.

Observed datapath changes:

| Benchmark | DRAM read trans | DRAM read response | L1V avg latency | L2 avg latency | 128B access-unit saved | L2 dir batch avg |
|---|---:|---:|---:|---:|---:|---:|
| bitonicsort | -50.69% | -40.35% | -23.79% | -24.88% | 49.98% | 1.00 |
| floydwarshall | -20.01% | -14.30% | -4.12% | -12.19% | 19.77% | 1.06 |
| im2col | -19.09% | -17.94% | -4.89% | -12.87% | 19.00% | 1.08 |
| relu | -50.14% | -38.29% | -23.43% | -26.37% | 49.96% | 1.00 |

DRAM read bytes are the main warning sign:

| Benchmark | Baseline DRAM read MB | Mechanism DRAM read MB | Byte change | Runtime change |
|---|---:|---:|---:|---:|
| bitonicsort | 43.24 | 42.64 | -1.39% | +1.61% |
| floydwarshall | 38.51 | 61.62 | +59.99% | -0.62% |
| im2col | 19.83 | 32.09 | +61.81% | -0.31% |
| relu | 21.55 | 21.49 | -0.28% | +0.49% |

For relu, the mechanism reduces DRAM transactions by about 50% and DRAM
response time by about 38%, but total runtime improves by only about 0.5%. This
means the optimized latency is likely hidden by parallel execution or is not on
the end-to-end critical path.

For floydwarshall and im2col, the mechanism reduces transaction count but
increases DRAM read bytes by about 60%. The extra bytes can offset the response
time improvement.

### Current Supported Conclusions

Supported:

- Mechanism 1 reduces DRAM read transaction count.
- Mechanism 1 reduces DRAM read response time.
- Mechanism 1 reduces observed L1V and L2 request latency.
- The 128B access-unit coalescing path is the main effective component.

Not yet supported:

- Mechanism 1 provides broad end-to-end runtime speedup.
- L2 directory batching is a major contributor.
- relu is a strong positive runtime case.

Good current wording:

```text
Mechanism 1 improves the L2-to-DRAM datapath by reducing DRAM access-unit
transactions and DRAM response time. Runtime speedup depends on whether this
latency is on the critical path and whether the 128B access unit causes
additional read traffic.
```

For more detail and key code snippets, see:

```text
akkalat/reports/m1_current_results_analysis.md
```

### Possible Next Mechanism Improvement

The current 128B access-unit behavior is useful when both adjacent 64B
cachelines are demanded close together. It is less useful when it fetches extra
data that is not needed.

A future version should distinguish:

- true coalescing: two demanded 64B cachelines share one 128B access unit;
- overfetch: one demanded 64B cacheline triggers a 128B access alone.

The mechanism should prefer true coalescing and avoid overfetch-heavy cases.

## Tracing Modes

### Page-sharing trace

Use this when studying which pages are accessed by which GPU and whether remote
pages are also locally used by their owner GPU.

```sh
python3 akkalat/runall2.py \
  --benchmarks traditional \
  --configs baseline \
  --trace-sharing \
  --trace-sharing-sample 1 \
  --trace-sharing-max-records 100000 \
  --max-workers 1
```

Outputs include:

```text
*_sharing.csv.gz
*_sharing_pages.csv
```

### L2 source report

Use this to record whether L2 service comes from local L2/DRAM, remote L2,
remote DRAM, or remote GPM paths.

```sh
python3 akkalat/runall2.py \
  --benchmarks traditional \
  --configs baseline \
  --report-l2-source \
  --max-workers 1
```

Outputs include:

```text
*_metrics_l2_source_summary.csv
*_metrics_l2_source_remote_matrix.csv
*_metrics_l2_source_page_source.csv
*_metrics_l2_source_remote_fill_reuse.csv
```

### Memory-path trace

Use this to trace L1V request paths and joint TLB/cache miss behavior.  The
default steady-state window skips the first 100K observed L1V accesses and
records the next 100K selected paths.

```sh
python3 akkalat/runall2.py \
  --benchmarks llm-like-bottleneck \
  --configs baseline \
  --trace-memory-path \
  --trace-memory-path-warmup-accesses 100000 \
  --trace-memory-path-max-records 100000 \
  --trace-memory-path-exit-on-complete \
  --max-workers 1
```

Outputs include:

```text
*_memory_path_raw.csv.gz
*_memory_path_summary.csv
*_memory_path_joint_miss.csv
*_memory_path_stage_latency.csv
*_memory_path_l1v_path_hops_raw.csv.gz
*_memory_path_l1v_path_summary.csv
*_memory_path_l1v_path_stage_summary.csv
*_memory_path_specific_page_summary.csv
*_memory_path_specific_cacheline_summary.csv
```

The L1V path trace follows a coalesced L1V cache-line transaction from L1V
entry, through local L2 or RDMA/remote L2/remote DRAM, and back to L1V.
The specific page/cacheline summaries are computed only from the selected
stable window after `--trace-memory-path-warmup-accesses` and within
`--trace-memory-path-max-records`.

## Common Analysis Commands

Summarize a run directory:

```sh
python3 akkalat/summarize_runall2.py \
  --results-dir akkalat/results/<run-dir> \
  --show
```

Analyze DRAM saturation:

```sh
python3 akkalat/analyze_dram_saturation.py \
  --traditional-dir akkalat/results/<traditional-run-dir> \
  --llm-dir akkalat/results/<llm-like-run-dir>
```

## Results

Experiment outputs are written under:

```text
akkalat/results/<timestamp>-sampled-validation/
```

Each benchmark normally generates:

```text
*_out.stdout      Captured stdout/stderr and runtime log.
*_metrics.csv     Main metrics.
*_*.csv           Optional trace/report outputs.
figures/          Generated plots.
```

Large result directories are not intended to be committed.  Keep generated
outputs local unless a specific report needs selected figures or summary CSVs.

## Current Research Questions

The repository currently supports several related investigations:

1. Whether remote data access is a bottleneck for traditional and ML-like
   workloads.
2. Whether shared remote pages are also used by their owner GPU.
3. Where L1V request latency is spent after the request enters L1V.
4. Whether L1V admission, L1V MSHR pressure, RDMA endpoints, or network return
   paths dominate the memory path.
5. Whether larger memory footprints create per-GPU execution imbalance across
   the 7-by-7 WSG layout.
6. How Photon-style sampling behaves under this WSG configuration.
7. Whether Mechanism 1's L2/DRAM batching reduces datapath latency and under
   what workload conditions that turns into end-to-end speedup.

## Notes

- The WSG layout used by most analysis scripts is a 7-by-7 tile grid with one
  non-GPU center/IOMMU tile and 48 GPU tiles.
- `--disable-servers` is optional.  It is useful for automated batch runs, but
  it is not required for correctness.
- `--max-workers` controls host-side experiment parallelism.  Large timing
  simulations can consume a lot of memory, so use conservative values when
  collecting detailed traces.
- Some scripts have historical default result directories.  Prefer passing the
  target result directory explicitly.
