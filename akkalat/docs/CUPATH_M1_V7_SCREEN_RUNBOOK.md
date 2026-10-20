# CuPath deadline M1 screen runbook

## Status

The user resumed M1 optimization on 2026-07-20 with a ten-day deadline.  The
old V7 binaries predate the selected local exponential late-feedback policy
and must not be used.

The deadline screen tests one architectural candidate across all paper workloads; it
does not tune a benchmark, change Baseline, reintroduce 128-B requests, or
alter the runner-side runtime-stop semantics.  Host admission always preserves
50 GiB of `MemAvailable` and budgets 32 GiB per active simulator.

## Source gate before building

Run the focused unit tests, Python auditors, three module builds, formatting
check, and no-128B/no-benchmark-specific audits.  Record the dirty-worktree
status.  A failure blocks the screen; it must not be hidden by reusing an older
binary.

## Candidate build

From `akkalat/baseline`, build one candidate executable:

```sh
GOCACHE=/tmp/gocache go build -buildvcs=false \
  -o /tmp/cupath-m1-deadline-candidate-v8-20260720a .
sha256sum /tmp/cupath-m1-deadline-candidate-v8-20260720a
```

Write the SHA-256 and exact build time into the new result directory.  This is
still a candidate, not the paper binary.  Any production-code change after the
build invalidates it and requires a new path and hash.

## All-workload counter smoke

The first resumed experiment covers all fourteen paper benchmarks, but only
Baseline and M1.  `max-wg=192` is a functional and counter screen, not a paper
performance result: independently stopped runs may observe different normally
scheduled WG sets.

```sh
python3 akkalat/runall2.py \
  --remote-ablation \
  --benchmarks=traditional \
  --configs=baseline,m1 \
  --max-wg=192 \
  --max-workers=14 \
  --memory-reserve-gib=50 \
  --memory-per-worker-gib=32 \
  --skip-build \
  --binary-path=/tmp/cupath-m1-deadline-candidate-v8-20260720a \
  --disable-servers \
  --output-dir=akkalat/results/2026-07-20-cupath-v8-deadline-m1-screen14 \
  --extra-benchmark-flags='-sampled -branch-sampled -kernel-sampled'
```

The memory admission cap, rather than `--max-workers`, selects the safe actual
parallelism.  Resume only with `--rerun-missing`; never overwrite successful
cells or reuse the stopped 2026-07-19 directory.

## Required M1 decision table

For every Baseline/M1 pair report:

- speedup and actual L2 demand-read latency;
- predictor coverage (`candidates / real demands`);
- accurate prefetch usefulness (`useful / issued`);
- timeliness (`timely / useful`), prefetch-led late merges, and demand-won
  races;
- lifecycle partition (`issued = fills + redundant races + outstanding`),
  with runtime-stop outstanding requests reported as right-censored rather
  than unused;
- Baseline demand L2-to-DRAM reads versus M1 demand reads, M1 prefetch reads,
  and M1 total reads;
- physical DRAM reads and writes;
- L2 MSHR-full cycles, demand-delay exposure, and headroom drops;
- runtime unused-prefetch evictions, reset/end-of-run retirements, and peak
  prefetch-only L2 occupancy;
- Filter false positives, insert failures, port stalls, and occupancy;
- observed WG count, original partition, WG-set hash, return code, and runtime.

The smoke is accepted only as a functioning measurement if every successful
cell uses the same binary and fixed flags, reports the new latency/pollution
counters, preserves the original full-grid partition, and exits normally.
Performance and work conclusions require a longer screen.  A high accuracy
alone is explicitly insufficient.

## Promotion rule

Do not promote the candidate merely because a subset of workloads improves.  First inspect
all fourteen rows and explain every regression.  Promote only if earlier
candidate issue and immediate late feedback materially improve timeliness
without a material aggregate increase in physical DRAM work, L2 demand
latency, MSHR pressure, or runtime unused evictions.  If the evidence does not
support this, retain the truthful negative result and revise M1 rather than
changing individual benchmark parameters.

After promotion, build a new immutable paper-candidate path and SHA, then run
the required representative 7-by-5 gate followed by the formal 14-by-5
campaign in new directories.  Paper text remains unchanged until all formal
audits pass.
