# M1 paired-read experiment runbook (historical)

> **Do not use the launch commands in this file for the current paper
> campaign.**  They predate the fixed `--max-wg=78600` protocol and include
> natural-completion and Baseline launches that are no longer authorized.
> The current authoritative protocol is
> `M1_CURRENT_EXPERIMENT_PROTOCOL_20260721.md`.  The material below is retained
> only to explain old binaries and result directories.

This historical runbook describes an earlier two-independent-64-B M1.
Historical V5/V6 commands and results are not current paper data.

## Frozen diagnostic binary

- Path: `akkalat/baseline/baseline`
- SHA-256:
  `ee6df3a1ceddcae3866727dd38d85916f06e5c99ce6c66d09466ab80e297472a`
- L1V MSHRs: 16
- Modeled full-channel accounting unit: 64 B (128-bit full channel, BL4),
  equivalent to two 32-B pseudo-channel bursts

Before any resume, verify the binary against the directory's
`EXPERIMENT_BINARIES.json`. The current launcher snapshots each supplied build
output into `<RESULT_DIR>/frozen-binaries/<target>-<full-sha256>` before it
writes commands or starts a simulator. New campaigns and resumes therefore do
not depend on a mutable Go build output. The corrected representative campaign
uses this snapshot path; later Go builds cannot alter its recorded binary.

## Exact-hash correctness smoke

The final binary completed all ten named configurations in
`akkalat/results/2026-07-20-m1-final-ee6df3-correctness-smoke10`.  The launch
used `aes-pipeline-smoke` and an explicit positive `max-wg=1`, so it proves
termination and accounting only and is never performance evidence.  Its
strict audit command was:

```bash
python3 akkalat/analyze_m1_paired_read.py \
  akkalat/results/2026-07-20-m1-final-ee6df3-correctness-smoke10 \
  --strict \
  --expected-benchmarks=aes-pipeline-smoke \
  --expected-configs=baseline,old_m1_independent_prefetch,cuckoo_filter_only,always_pair,predictor_only,paired_read_without_filter,new_m1,m2,m3,complete \
  --expected-sha256=ee6df3a1ceddcae3866727dd38d85916f06e5c99ce6c66d09466ab80e297472a \
  --output=akkalat/results/2026-07-20-m1-final-ee6df3-correctness-smoke10/m1_paired_read_summary.csv
```

All ten cells passed the two-physical-64-B read/byte relation, independent
paired-member relation, sibling terminal/timeliness partitions, 64-B access
unit check, generic-row-disabled check, PairID-only aggregate-continuation
check, and runtime configuration matrix.  `new_m1` issued 903 paired
descriptors, which the controller counted as 1,806 independent members.  Its
439 terminally unused sibling lines are retained by the analyzer as 28,096 B
of terminal waste even though they had not yet been evicted when reporting
occurred.  This distinction prevents a short run from reporting zero waste.

## Representative diagnostic

Two stopped directories are retained only as audit evidence:

- `...w12-v2` used the correct pair-only mechanism: generic row continuation
  was false and `AggregateContinuation` was enabled internally by each
  granularity diagnostic.  It was nevertheless stopped at 0/35 after the two
  independent controls were mistakenly treated as one.  It is incomplete and
  must not be used for results.
- `...w12-v3-rowcontinuation` incorrectly set generic row continuation to true
  in 15 paired cells.  That additionally preserves rows for unrelated
  same-row requests, so the directory is policy-contaminated and invalid.  It
  was also stopped at 0/35.

The corrected campaign keeps `-dram-row-continuation-enable=false` in every
cell.  Pair-producing diagnostics receive the strictly narrower PairID-only
`AggregateContinuation` automatically from granularity mode.  The original
natural-completion launch used 12 host workers:

```bash
python3 akkalat/runall2.py \
  --remote-ablation \
  --benchmarks=aes,fft,fir,kmeans,spmv \
  --configs=baseline,old_m1_independent_prefetch,cuckoo_filter_only,always_pair,predictor_only,paired_read_without_filter,new_m1 \
  --max-workers=12 \
  --memory-reserve-gib=50 \
  --memory-per-worker-gib=16 \
  --skip-build \
  --binary-path=/home/daoxuanxu/datapath/WSC_datapath/akkalat/baseline/baseline \
  --disable-servers \
  --output-dir=akkalat/results/2026-07-20-m1-final-paired64-representative5-sampled-w12-v4-paironly \
  --extra-benchmark-flags='-sampled -branch-sampled -kernel-sampled'
```

No `--max-wg` flag is present, so `max-wg=0` and natural completion are
required.  Twelve simultaneously growing natural workloads reduced host
`MemAvailable` to approximately 63 GiB before any cell completed (individual
RSS reached approximately 20.7 GiB).  The launcher was cleanly interrupted
before swap/OOM, all children exited, and no partial result JSON or metrics
were accepted.  The exact recorded commands and immutable binary were then
resumed at eight workers:

```bash
python3 akkalat/runall2.py \
  --rerun-missing=akkalat/results/2026-07-20-m1-final-paired64-representative5-sampled-w12-v4-paironly \
  --max-workers=8 \
  --memory-reserve-gib=50 \
  --memory-per-worker-gib=20 \
  --skip-build
```

The resume manifest is
`EXPERIMENT_RERUN_METADATA_20260720-223813-538595.json`; it changes only host
parallelism and retains all 35 original cell identities and binary SHA-256.
After all 35 cells finish:

```bash
python3 akkalat/analyze_m1_paired_read.py \
  akkalat/results/2026-07-20-m1-final-paired64-representative5-sampled-w12-v4-paironly \
  --strict \
  --require-full-workload \
  --expected-benchmarks=aes,fft,fir,kmeans,spmv \
  --expected-configs=baseline,old_m1_independent_prefetch,cuckoo_filter_only,always_pair,predictor_only,paired_read_without_filter,new_m1 \
  --expected-sha256=ee6df3a1ceddcae3866727dd38d85916f06e5c99ce6c66d09466ab80e297472a \
  --output=akkalat/results/2026-07-20-m1-final-paired64-representative5-sampled-w12-v4-paironly/m1_paired_read_summary.csv

python3 akkalat/analyze_wg_mapping.py \
  akkalat/results/2026-07-20-m1-final-paired64-representative5-sampled-w12-v4-paironly \
  --benchmarks=aes,fft,fir,kmeans,spmv \
  --configs=baseline,old_m1_independent_prefetch,cuckoo_filter_only,always_pair,predictor_only,paired_read_without_filter,new_m1 \
  --require-baseline-match

python3 akkalat/plot_m1_paired_read.py \
  --summary=akkalat/results/2026-07-20-m1-final-paired64-representative5-sampled-w12-v4-paironly/m1_paired_read_summary.csv \
  --output-dir=akkalat/results/2026-07-20-m1-final-paired64-representative5-sampled-w12-v4-paironly/figures
```

Do not launch the formal campaign until the strict diagnostic accounting and
M1 retain/reject audit have been inspected. If a source change is required,
build a new immutable binary and rerun all affected cells; never mix hashes.

## Final formal campaign

Replace `<FINAL_DIR>` with a new directory and `<FINAL_BINARY>` with the
post-diagnostic frozen binary. The command contains no explicit config list,
so `--remote-ablation` produces exactly Baseline/M1/M2/M3/Complete.

```bash
python3 akkalat/runall2.py \
  --remote-ablation \
  --benchmarks=traditional \
  --max-workers=8 \
  --memory-reserve-gib=50 \
  --memory-per-worker-gib=20 \
  --skip-build \
  --binary-path=<FINAL_BINARY> \
  --disable-servers \
  --output-dir=<FINAL_DIR> \
  --extra-benchmark-flags='-sampled -branch-sampled -kernel-sampled'
```

The formal launch uses a more conservative host-only parallelism budget than
the representative screen. During the representative run, several natural
workload processes exceeded 16 GiB RSS (approximately 17 GiB was observed),
so 20 GiB/worker and eight workers preserve headroom above the 50-GiB reserve.
This changes only host throughput; simulator flags, workload identity, and
reported driver time are unchanged.

The metadata must contain exactly 14 workloads times five configurations.
Every cell must report `max_wg=0`, `natural_completion`, equal requested,
mapped, and completed WG counts, 100% coverage, and the same per-workload
launch/WG-set signature across configurations.

## Final analysis and raster figures

```bash
python3 akkalat/analyze_cupath_paired_formal.py \
  <FINAL_DIR> \
  --diagnostic-results=<FINAL_DIAGNOSTIC_DIR> \
  --expected-sha256=<FINAL_BINARY_SHA256>
```

`<FINAL_DIAGNOSTIC_DIR>` must be the strict natural-completion 5x7 diagnostic
grid produced by the exact same immutable binary as `<FINAL_DIR>`. The formal
14x5 grid intentionally has no no-Filter cell, so the analyzer takes
Filter/usefulness/row-reuse retention evidence from that diagnostic grid and
the all-workload positive-majority/Complete-regression evidence from the
formal grid. It rejects a hash mismatch rather than silently combining runs.

That command creates the strict formal CSVs, group geomeans, WG audit,
Markdown/JSON audit, and the Baseline-inclusive 300-dpi PNG:

- `cupath_paired_overall_speedup.png`

The representative diagnostic creates:

- `m1_work_reduction.png`
- `m1_filter_contribution.png`
- `m1_dram_work.png`

Only figures produced after all strict analyzers pass may replace the paper's
historical numerical results.
