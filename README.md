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
|-- akkalat/               Main experiment drivers, tracing, and plotting scripts.
|-- photon/                Vanilla Photon reference code.
|-- bottleneck analysis/   Earlier copied sandbox for bottleneck experiments.
|-- weeklyreport/          Weekly report LaTeX sources and generated figures.
`-- README.md
```

Important `akkalat/` files:

```text
akkalat/runall2.py                    Main traditional/LLM-like benchmark runner.
akkalat/runllm_decomposed.py          Runs decomposed GPT/BERT operators.
akkalat/runall2_constants.py          Benchmark sets and run configurations.
akkalat/runall2_config.py             Common flags and config expansion.
akkalat/baseline/runner/flag.go       Benchmark binary flags.
akkalat/baseline/runner/shaderarray.go
                                      WSG cache/CU configuration.
akkalat/plot_l1v_path_analysis.py     L1V path breakdown plots.
akkalat/plot_rdma_path_breakdown.py   RDMA request/response breakdown plots.
akkalat/plot_traditional_remote_movement.py
                                      Traditional remote-flow maps.
```

## Prerequisites

The codebase is primarily Go plus Python analysis scripts.

Recommended environment:

```sh
go version
python3 --version
python3 -m pip install --user numpy pandas matplotlib seaborn
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

Useful benchmark presets are defined in `akkalat/runall2_constants.py`:

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

Plot traditional remote movement:

```sh
python3 akkalat/plot_traditional_remote_movement.py \
  akkalat/results/<run-dir>
```

Plot L1V path breakdowns:

```sh
python3 akkalat/plot_l1v_path_analysis.py \
  --traditional-dir akkalat/results/<traditional-run-dir> \
  --llm-dir akkalat/results/<llm-like-run-dir>
```

Plot RDMA request/response breakdowns:

```sh
python3 akkalat/plot_rdma_path_breakdown.py \
  akkalat/results/<run-dir>
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
