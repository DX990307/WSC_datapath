# O1--O6 observation tracing and analysis

`analyze_observations.py` consumes the independent observation tracer output;
it does not read the legacy memory-path trace and does not reconstruct DRAM
bank/row locations from addresses.

## Collect the 14-benchmark baseline

`--trace-observation` intentionally runs only the mechanisms-off baseline. The
runner rejects local batching/reorder, remote data-path mechanisms, forced
local routing, and the legacy memory-path tracer so characterization data
cannot silently mix configurations.

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

The observation mode automatically selects `baseline`; `--configs=baseline`
is optional. Do not add sampled-execution flags for paper characterization:
they alter request timing and short-window locality. The formal collection
command deliberately runs each full benchmark without early exit because the
O1/O2 demand-read, O3 physical-DRAM, and O4--O6 remote/L2 windows have
independent warmups and limits.

For a quick instrumentation check only, `--trace-observation-exit-on-complete`
may be added with a nonzero `--trace-observation-max-records`. Completion of
the O1/O2 demand-read window then controls termination. The runner drains
remote requests already admitted to the remote window, but the independent O3
and O4--O6 windows can still be only partially populated. Do not use that quick
mode for the paper's complete characterization.

Each experiment emits:

- `*_observation_paths.csv.gz`: one selected post-coalescing L1 demand-read
  path per row, with immutable path identity;
- `*_observation_dram_physical.csv.gz`: bounded real DRAM lifecycle events;
- `*_observation_dram_locality.csv`: online O3 locality CDFs;
- `*_observation_remote_requests.csv.gz`: baseline logical remote requests;
- `*_observation_l2_utilization.csv.gz`: bounded access-weighted L2 snapshots;
- `*_observation_validation.csv` and
  `*_observation_remote_validation.csv`: emitter/instrumentation invariants;
- path/remote summary CSVs.

All path time is integer picoseconds. Exclusive stages are adjacent state
transitions, so every completed row must satisfy
`end_ps - start_ps = total_ps = accounted_ps` and `residual_ps = 0`. L1/L2
MSHR followers point to their leader and own only their wait interval; they do
not duplicate the leader's DRAM or network work.

The L2 snapshots are sampled on the first accepted request in each slice and
every 256 accepted requests thereafter. Therefore O6 must describe them as
*access-weighted headroom*, not uniform-time occupancy.

## Analyze results

Run it on one result directory:

```bash
python3 akkalat/analyze_observations.py \
  akkalat/results/2026-07-12-observation \
  --output-dir akkalat/results/2026-07-12-observation/analysis
```

It also accepts individual files and quoted recursive globs:

```bash
python3 akkalat/analyze_observations.py \
  'akkalat/results/*-observation/*_observation_*.csv*' \
  --output-dir akkalat/results/observation-paper-csv
```

An individual file may use any custom runner prefix; it is recognized from the
new tracer schema rather than a required `_observation_` substring:

```bash
python3 akkalat/analyze_observations.py \
  /tmp/observation_relu_l2fixed_paths.csv.gz \
  /tmp/observation_relu_l2fixed_validation.csv \
  /tmp/observation_relu_l2fixed_dram_locality.csv \
  --output-dir /tmp/observation_relu_l2fixed_analysis
```

Strict analysis requires the matching emitter-validation file whenever a path
or remote raw trace is supplied. This prevents an accidentally omitted
invariant report from being interpreted as a clean run.

Recursive directory discovery remains intentionally stricter and only accepts
the standard path/locality/remote/L2-utilization files plus the two standard
emitter-validation names. This prevents legacy memory-path, physical-event,
metrics, and summary CSVs in the same directory from being misclassified.

The generated files are:

- `o1_exclusive_stage_breakdown.csv`: demand-read exclusive stages grouped by
  benchmark, route, source, and path class, with exact mean/p50/p95 values and
  each stage's fraction of group latency. Its analysis unit is one
  post-coalescing L1 cache-line transaction (shown explicitly in the CSV).
- `o1_validation.csv`: accounting and event-integrity checks. The default mode
  stops if `total_ps != accounted_ps`, `residual_ps != 0`, stage sums differ,
  a demand read is incomplete, or duplicate/regressing events exist.
- `o2_adjacent_line_short_window_cdf.csv`: nearest-prior CDF for the other 64-B
  half of the same aligned 128-B access unit, within the same requester L1.
  Only local, DRAM-bound L2 read misses are included by default. Identical
  `(requester, line, event time)` records are removed.
- `o2_validation.csv`: selection counts, missing timing boundaries, and exact
  duplicates removed.
- `o3_physical_locality_cdf.csv`: merged physical-DRAM relation CDFs using the
  controller mapper's actual bank/row/column identity.
- `o3_physical_locality_heatmap_long.csv`: long-form version ready for a
  relation-by-window heatmap.
- `o4_remote_amplification.csv`: logical bytes versus forward/return network
  bytes, Manhattan hops, byte-hops, and remote latency percentiles.
- `o5_exact_inflight_dedup.csv`: requester-local reads that arrived while a
  matching same-line read from the current write epoch was already active;
  this count is captured at RDMA admission, independently of later issue
  order.
- `o5_remote_page_spatial_cdf.csv`: the orthogonal opportunity among different
  64-B lines in the same requester/owner/4-KB page and short arrival window;
  exact inflight duplicates are excluded.
- `o6_remote_reuse_summary.csv`, `o6_remote_reuse_frequency.csv`, and
  `o6_remote_reuse_heavy_hitters.csv`: reuse after separating PID, requester,
  owner, cacheline, and write epoch.
- `o6_l2_headroom.csv`: access-weighted occupancy/free-space and MSHR
  mean/p50/p95 plus the fraction of samples below 50% occupancy.
- `o4_o5_o6_validation.csv`: remote timestamp/byte accounting and L2 snapshot
  checks.
- `emitter_instrumentation_validation.csv`: every invariant/status reported by
  the path and remote emitters, including its source file and normalized
  `strict_pass`. In strict mode, any path `pass=false` or remote
  `status=error` stops analysis.

In O3, `distance=cycles` means cycles of the physical DRAM controller (500 MHz
in the current baseline), while `distance=intervening_requests` counts physical
read subtransactions arriving at that same controller between the pair. The
`different_bank_same_controller` relation is bank-level parallelism within one
controller, not an arbitrary pair of banks across controllers.

O2 uses `l2_lookup_result` as its arrival boundary and a 1 GHz L1 clock by
default. Change these explicitly if the simulated configuration differs:

```bash
python3 akkalat/analyze_observations.py RESULTS \
  --o2-event l2_lookup_result \
  --o2-cycle-ps 1000 \
  --o2-windows-cycles 0,1,2,4,8,16,32,64
```

O5 defaults to a 1-ns remote scheduling cycle. Override it only if the RDMA
frequency changes:

```bash
python3 akkalat/analyze_observations.py RESULTS \
  --remote-cycle-ps 1000 \
  --remote-heavy-hitters 100
```

Use `--no-strict` only while debugging an incomplete trace. A standard-library
synthetic regression test is available with:

```bash
python3 akkalat/analyze_observations.py --self-test
```
