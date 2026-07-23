# Current M1 experiment protocol (2026-07-21)

This file is the authoritative launch policy for the current CuPath M1 work.
It supersedes natural-completion and Baseline-rerun commands in older
runbooks.

## Non-negotiable invariants

- Every performance cell uses exactly `--max-wg=78600`.
- Do not change WG selection, ordering, partitioning, placement, or stopping
  semantics.  In particular, do not divide 78,600 across kernels or GPUs.
- L1V has 16 MSHRs.
- M1 uses a typed Cuckoo Filter and paired-read scheduling.  Every accepted
  pair remains two independent physical 64-B DRAM transactions.
- No native widened transaction, timeout, HLQ, global DRAM reorder, extra
  cache, enlarged resource, or workload-specific tuning is allowed.
- M2 and M3 source, parameters, and runtime behavior are frozen.
- Preserve at least 50 GiB host `MemAvailable`; host parallelism may change
  but simulator configuration may not.
- Do not launch Baseline merely because a candidate uses a different binary.
  A successful prior Baseline is reusable when the modeled configuration and
  workload protocol match.  Record provenance rather than recomputing it.
- A cross-configuration speedup is formal only when the workload protocol and
  WG-set evidence match.  Never repair a mismatch by changing WG code or by
  averaging separately run subsets.

## Candidate screening

Current screens use only `m1`; they never request a Baseline cell:

```bash
python3 akkalat/runall2.py \
  --remote-ablation \
  --configs=m1 \
  --benchmarks=<SCREEN_WORKLOADS> \
  --max-wg=78600 \
  --max-workers=<HOST_ONLY_LIMIT> \
  --memory-reserve-gib=50 \
  --memory-per-worker-gib=<SAFE_RSS_BUDGET> \
  --skip-build \
  --binary-path=<IMMUTABLE_CANDIDATE> \
  --output-dir=<NEW_RESULT_DIR> \
  --extra-benchmark-flags='-sampled -branch-sampled -kernel-sampled'
```

Record the binary SHA-256, exact command, M2/M3 frozen-source hashes, result
directory, and live memory admission in `SCREEN_NOTES.md`.  Retain a candidate
only after checking predictor coverage and accuracy, timely/late/unused
sibling partitions, physical DRAM reads, DRAM queue pressure, L2 MSHR pressure,
demand latency, paired-read accounting, normal exit, and deadlock absence.

Use `compare_m1_candidate_screens.py` to assemble those counters across
candidate directories.  The tool emits a time ratio only when the observed WG
count and nonempty WG-set hash match the selected reference; otherwise it
retains the mechanism counters but marks the time comparison non-formal.

## Final M1 and Complete runs

After one M1 implementation is frozen, run `m1` and `complete` for the 14 paper
benchmarks with the same immutable binary and the invariants above.  Do not
include `baseline` in the config list.  Reuse matching successful Baseline
cells through an explicit provenance table.

```bash
python3 akkalat/runall2.py \
  --remote-ablation \
  --configs=m1,complete \
  --benchmarks=traditional \
  --max-wg=78600 \
  --max-workers=<HOST_ONLY_LIMIT> \
  --memory-reserve-gib=50 \
  --memory-per-worker-gib=<SAFE_RSS_BUDGET> \
  --skip-build \
  --binary-path=<FINAL_BINARY> \
  --output-dir=<NEW_RESULT_DIR> \
  --extra-benchmark-flags='-sampled -branch-sampled -kernel-sampled'
```

Only after Complete is validated, run `m2,m3` with the same frozen binary and
protocol.  M2/M3 are measurement-only configurations; their implementation is
not changed during M1 optimization.

## Final evidence

The final report must contain the Baseline provenance for every benchmark,
per-cell binary SHA-256 and flags, WG-set evidence, driver time, per-benchmark
speedups, all-14 geomean, Local/Mixed/Remote geomeans, M1 mechanism counters,
full Baseline/M1/M2/M3/Complete ablation, overall and ablation PNGs, result
directories, and exact reproduction commands.  If the verified Complete
geomean is below 1.5x, report the best verified value and its generic
bottleneck without deleting or selectively replacing workloads.
