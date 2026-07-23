# CuPath V6 experiment runbook

> **HISTORICAL V6 / PAUSED.** All launchers described below were stopped on
> request. The V6 executable predates the current M1 source and must not be
> resumed as if it represented the new candidate. Completed cells remain
> preserved for diagnosis. Use `CUPATH_M1_V7_SCREEN_RUNBOOK.md` only after the
> user explicitly permits experiments to resume.

This historical runbook is subordinate to `CUPATH_V6_GOAL_AUDIT.md`.  A command finishing
is not acceptance: its result directory must pass the corresponding strict
auditor.  All performance cells use this frozen executable:

```text
/tmp/cupath-runtime-stop-frozen-v6
SHA-256 cbb5f1d6737d913ce0cbf01600244339c9d1d52855a5fe1bfb7c932a25ff391e
```

Do not rebuild or substitute the binary between campaigns.  Use the explicit
`--rerun-missing` interface to resume an interrupted directory; do not rerun
the original configuration command against that directory.  The runner skips
only a cell whose result JSON, simulator return code, metrics, and runtime-stop
mapping sidecar all validate.

The observation count is `min(max-wg, full requested WG count)`.  Thus,
`max-wg=76800` stops a larger grid after 76,800 normally mapped WGs, while a
smaller grid runs to natural completion without duplicating, redistributing,
or manufacturing WGs.  The host audit records the latter as
`workload_completed_before_max_wg`; both cases retain the complete original
partition as evidence.

The formerly running representative/provenance launchers imported the older
host auditor before this natural-completion rule was corrected.  If they mark
only such a simulator-success cell as `returncode=-2`, revalidate its immutable
stdout, metrics, and mapping sidecar without rerunning it:

```sh
python3 akkalat/revalidate_mapping_results.py RESULTS_DIR \
  --benchmarks=matrixtranspose
```

The tool accepts only old mapping-audit failures with simulator return code
zero; it cannot convert a simulator failure, timeout, missing metric, partial
grid, or forbidden WGFilter into a success.

The standard missing-cell resume command is:

```sh
python3 akkalat/runall2.py \
  --rerun-missing=akkalat/results/2026-07-19-cupath-v6-runtime-stop-representative7 \
  --max-workers=7 \
  --memory-reserve-gib=50 \
  --memory-per-worker-gib=40 \
  --skip-build \
  --binary-path=/tmp/cupath-runtime-stop-frozen-v6
```

For a new formal directory, request up to fourteen workers but let the memory
admission cap select the safe effective count.  This limit comes from the live
V6 representative measurements: MatrixTranspose Complete reached a 35.75-GiB
resident-set high-water mark, which invalidates the earlier 32-GiB estimate.
Admission now budgets 40 GiB per cell.  With the
host's observed idle memory, the effective standalone formal count is five;
it can rise only if launch-time `MemAvailable` proves that the 50-GiB reserve
still holds.  Recheck `MemAvailable` before every resume and never bypass the
cap.
For every new formal launch and resume, pass
`--memory-reserve-gib=50 --memory-per-worker-gib=40`; the runner records the
startup `MemAvailable` and computed cap in campaign metadata.  The already
running two-worker provenance campaign retains its recorded 21.5-GiB launch
budget; its actual host reserve is monitored directly.  If that campaign must
be resumed, use the revised 40-GiB per-worker budget; the separate rerun
metadata records the safer cap while the immutable primary metadata continues
to describe the original launch.

## Representative gate

```sh
python3 akkalat/runall2.py \
  --remote-ablation \
  --benchmarks=aes,fastwalshtransform,fft,kmeans,pagerank,matrixtranspose,spmv \
  --configs=baseline,m1,m2,m3,complete \
  --max-wg=76800 \
  --max-workers=7 \
  --skip-build \
  --binary-path=/tmp/cupath-runtime-stop-frozen-v6 \
  --disable-servers \
  --output-dir=akkalat/results/2026-07-19-cupath-v6-runtime-stop-representative7 \
  --extra-benchmark-flags='-sampled -branch-sampled -kernel-sampled'
```

Accept the gate only after both commands below succeed.  Parallel wall times
are resource-safety diagnostics, not isolated simulator-overhead results.

```sh
python3 akkalat/plot_cupath_typed_ablation.py \
  akkalat/results/2026-07-19-cupath-v6-runtime-stop-representative7

python3 akkalat/analyze_cupath_runtime_screen.py \
  akkalat/results/2026-07-19-cupath-v6-runtime-stop-representative7 \
  --expected-workers=7 \
  --expected-sha256=cbb5f1d6737d913ce0cbf01600244339c9d1d52855a5fe1bfb7c932a25ff391e
```

## Formal 14-by-5 campaign

Launch this only after the representative gate passes.  The output directory
must be new and must not reuse the invalid V5 directory.

```sh
python3 akkalat/runall2.py \
  --remote-ablation \
  --benchmarks=traditional \
  --configs=baseline,m1,m2,m3,complete \
  --max-wg=76800 \
  --max-workers=14 \
  --memory-reserve-gib=50 \
  --memory-per-worker-gib=40 \
  --skip-build \
  --binary-path=/tmp/cupath-runtime-stop-frozen-v6 \
  --disable-servers \
  --output-dir=akkalat/results/2026-07-19-cupath-v6-runtime-stop-formal14 \
  --extra-benchmark-flags='-sampled -branch-sampled -kernel-sampled'
```

## Baseline traffic provenance

Traffic classification is a separate 14-cell Baseline-only diagnostic.  It
uses the same binary, hardware flags, sampling flags, complete requested grid,
and runner stop limit, while all CuPath mechanisms remain disabled.
It was launched with two workers alongside the seven-worker representative
screen.  This is a disjoint result directory and keeps the combined campaign
within the 50-GiB reserve; provenance wall times are not performance results.
Because these two already-running launchers predate the revised 40-GiB
budget, monitor host `MemAvailable` directly.  Pause the auxiliary provenance
launcher first if `MemAvailable` approaches 60 GiB; preserve its accepted
cells and later resume only through `--rerun-missing`.  Never interrupt or
discard accepted representative cells merely to increase concurrency.

```sh
python3 akkalat/runall2.py \
  --remote-ablation \
  --benchmarks=traditional \
  --configs=baseline \
  --max-wg=76800 \
  --max-workers=2 \
  --memory-reserve-gib=50 \
  --memory-per-worker-gib=21.5 \
  --skip-build \
  --binary-path=/tmp/cupath-runtime-stop-frozen-v6 \
  --disable-servers \
  --trace-remote-origin \
  --trace-remote-origin-max-records=100000 \
  --output-dir=akkalat/results/2026-07-19-cupath-v6-runtime-stop-baseline-traffic14 \
  --extra-benchmark-flags='-sampled -branch-sampled -kernel-sampled'
```

## Accepted-result analysis order

The classification output is intentionally written into the formal summary
directory before the formal tables are generated.  The final auditor consumes
both the aggregate tables and the per-controller/per-slice audit.

```sh
python3 akkalat/analyze_baseline_traffic.py \
  akkalat/results/2026-07-19-cupath-v6-runtime-stop-baseline-traffic14 \
  --output-dir=akkalat/results/2026-07-19-cupath-v6-runtime-stop-formal14 \
  --expected-workers=2 \
  --expected-memory-reserve-gib=50 \
  --expected-memory-per-worker-gib=21.5 \
  --expected-sha256=cbb5f1d6737d913ce0cbf01600244339c9d1d52855a5fe1bfb7c932a25ff391e

python3 akkalat/plot_cupath_typed_ablation.py \
  akkalat/results/2026-07-19-cupath-v6-runtime-stop-formal14

python3 akkalat/plot_cupath_work_reduction.py \
  --work-csv=akkalat/results/2026-07-19-cupath-v6-runtime-stop-formal14/cupath_work_reduction.csv \
  --filter-csv=akkalat/results/2026-07-19-cupath-v6-runtime-stop-formal14/cupath_filter_statistics.csv \
  --output-dir=akkalat/results/2026-07-19-cupath-v6-runtime-stop-formal14

python3 akkalat/analyze_cupath_controller_balance.py \
  akkalat/results/2026-07-19-cupath-v6-runtime-stop-formal14

python3 akkalat/analyze_cupath_formal.py \
  --summary-dir=akkalat/results/2026-07-19-cupath-v6-runtime-stop-formal14 \
  --expected-workers=5 \
  --expected-memory-reserve-gib=50 \
  --expected-memory-per-worker-gib=40 \
  --expected-sha256=cbb5f1d6737d913ce0cbf01600244339c9d1d52855a5fe1bfb7c932a25ff391e

python3 akkalat/compare_cupath_v5_v6.py \
  akkalat/results/2026-07-17-filter-prefetch-v5-formal14 \
  akkalat/results/2026-07-19-cupath-v6-runtime-stop-formal14
```

The paper may be updated only after these auditors accept all 70 performance
cells and all 14 provenance profiles.  The V5 comparison remains explicitly
methodological; it never revalidates V5 as a paper result.
