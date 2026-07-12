# Baseline observation tracing

The observation tracer is independent of the legacy `-trace-memory-path`
implementation. It records only a mechanisms-off baseline and fails fast if
local batching/reordering, forced-local routing, or the remote datapath is
enabled.

## Collect the 14 traditional benchmarks

```bash
python3 akkalat/runall2.py \
  --trace-observation \
  --benchmarks=traditional \
  --max-workers=8 \
  --disable-servers \
  --trace-observation-warmup-accesses=100000 \
  --trace-observation-max-records=100000 \
  --trace-observation-dram-warmup-accesses=100000 \
  --trace-observation-dram-max-records=100000 \
  --trace-observation-remote-warmup-requests=0 \
  --trace-observation-remote-max-records=100000 \
  --trace-observation-l2-sample-max=100000
```

When used alone, `--trace-observation` automatically selects the single
`baseline` configuration. `--trace-observation-warmup-accesses` counts
post-coalescing L1 **demand reads**, not all memory operations. It delays
recording until a later dynamic window; the simulator still executes the
prefix so that cache, MSHR, DRAM-row, and network state are correct.

## Collect baseline observations with the ablation sweep

The tracer can share one launch with the remote ablation study:

```bash
python3 akkalat/runall2.py \
  --remote-ablation \
  --trace-observation \
  --benchmarks=traditional \
  --max-workers=8 \
  --disable-servers
```

For every benchmark this launches the normal five ablation configurations,
but injects the observation flags only into the exact mechanisms-off
`baseline`. The four mechanisms-on experiments produce performance metrics
without observation files. The existing ablation baseline is reused, so no
duplicate baseline or output-name collision is created.

Do not combine this mode with `--trace-observation-exit-on-complete`: an early
baseline exit would make its performance metric incomparable with the other
ablation configurations, so the runner rejects that combination.

The formal command above intentionally has no early exit: the O1/O2
demand-read window, O3 physical-DRAM window, and O4--O6 remote/L2 windows have
independent warmups and limits, so the complete benchmark is the safe paper
collection default. For a quick instrumentation check, early exit can be
enabled with `--trace-observation-exit-on-complete` and a nonzero path-record
limit. It is triggered by completion of the O1/O2 path window; the tracer
freezes new remote admissions and drains requests already admitted to that
remote window. Even after that drain, O3 and O4--O6 may contain only partial
independent windows, so quick early-exit output is not the complete paper run.

## Files per benchmark

- `*_observation_paths.csv.gz`: one selected post-coalescing L1 demand-read
  path per row, with immutable path identity, parent IDs, physical message
  IDs, route/source, event boundaries, and a mutually-exclusive picosecond
  latency stack.
- `*_observation_validation.csv`: accounting and route invariants. Paper runs
  require every error invariant to pass.
- `*_observation_dram_physical.csv.gz`: mapper-derived physical DRAM lifecycle
  events and real channel/rank/bank-group/bank/row/column locations.
- `*_observation_dram_locality.csv`: bounded nearest-prior CDFs for same 128-B
  access unit, same row/different column, same bank/different row, and
  different bank within the same DRAM controller.
- `*_observation_remote_requests.csv.gz`: requester/owner, line, write epoch,
  requester-local read/read same-line inflight count captured at RDMA
  admission (before wire issue), wire bytes, hops, and completion latency.
- `*_observation_l2_utilization.csv.gz`: sparse per-slice valid/dirty/locked
  block and MSHR samples, collected inside the L2 event domain.
- `*_observation_{summary,remote_summary}.csv`: compact online aggregates.
- `*_observation_remote_validation.csv`: remote-emitter invariant status;
  together with the path validation file, it is normalized into
  `emitter_instrumentation_validation.csv` by the analyzer.

## Generate paper-ready O1--O6 CSVs

```bash
python3 akkalat/analyze_observations.py \
  akkalat/results/YOUR-OBSERVATION-RUN \
  --output-dir akkalat/results/YOUR-OBSERVATION-RUN/analysis
```

See `OBSERVATION_ANALYSIS.md` for output definitions and strict validation
rules.
