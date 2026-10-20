# Filter-coupled prefetch redesign baseline

> **HISTORICAL V5 DESIGN CHECKPOINT — INVALID FOR THE FINAL PAPER.**  This
> file preserves the pre-runtime-stop design baseline and its old frozen
> binary reference.  Use `CUPATH_V6_GOAL_AUDIT.md` and
> `CUPATH_V6_EXPERIMENT_RUNBOOK.md` for the active V6 contract.

Captured before implementing the unified filter-coupled prefetcher.

## Source state

- Branch: `observation`
- HEAD: `dc1dac768de285851662b14e168fc1f8545db794`
- HEAD subject: `Add fast allocation page profiling`
- Worktree: intentionally dirty with the existing CuPath, observation, runner,
  benchmark, and paper changes. No reset, checkout, or cleanup was performed.
- `git diff --check`: clean.

## Machine and results state

- Free filesystem space at capture: approximately 198 GB.
- `akkalat/results` size: approximately 4.8 GB.
- Result directories: 179.
- Running `runall2`, simulator, Go build, or Go test processes: none.
- No result directory was deleted.

## Build and focused test baseline

- `akkalat/baseline`: `go build -buildvcs=false` passed.
- Frozen pre-redesign binary: `/tmp/cupath-filter-prefetch-baseline`.
- Frozen binary SHA-256:
  `def75e782f53b90a45cf8fcce2c3e8b3ef20be13f533a339426223395dadedd7`.
- `akita/mem/cache/writeback`: package tests passed.
- `akita/mem/dram/...`: package tests passed.
- `mgpusim/timing/rdma`: package tests passed.
- `test_runall2_process`, `test_runall2_config`, and
  `test_cupath_analysis`: 18 tests passed.

The Go repositories use separate modules under `akita` and `mgpusim`; tests
must be launched from those module roots.

## Stable measured reference

The completed 14-workload, five-configuration mechanism screen is
`akkalat/results/2026-07-16-pure64-no128-current-ablation-wg48`.

| Configuration | Geomean speedup |
|---|---:|
| Baseline | 1.0000x |
| M1 | 1.0511x |
| M2 | 1.0434x |
| M3 | 1.0032x |
| Complete | 1.0944x |

Complete has thirteen positive workloads, one neutral workload, and no
negative workload under the predeclared thresholds. This is a mechanism
screen, not the final consistently sampled or unbounded paper campaign.

## Current implementation boundary

Before this redesign, M1 contains the per-slice RESIDENT negative fast-miss
path, fill forwarding, and immediate same-row continuation. M2 contains exact
remote same-line deduplication and owner/page bitmap aggregation. M3 contains
reuse-qualified clean fills into the existing requester L2. There is no active
local prefetcher or PATTERN key class in the source state captured here.

This file is the rollback and comparison reference for the new implementation.

## Implemented redesign checkpoint

- Frozen post-redesign binary: `/tmp/cupath-filter-prefetch-frozen-v5`.
- SHA-256:
  `d4cc91359237de6293ad6931fe0216651091b0c3405828f47b5471f9e4d9b901`.
- Logical request size: 64 B in local and remote candidate tests and paths.
- Predictor: bounded real-demand-only last-address/stride state, one candidate
  per demand at most, shared by a GPM's four L2 front-end slices; requester
  RDMA uses the same bounded state design.
- Typed Filter: one physical instance per L2 slice, with PATTERN, RESIDENT,
  PENDING, and SEEN types.
- `akita/mem/cache/...`, `akita/mem/dram/...`, `mgpusim/timing/rdma`,
  `mgpusim/driver`, and `akkalat/baseline/runner` tests pass.
- All 81 current `akkalat/test_*.py` tests pass, including metadata,
  raw-metric coverage, and source-invariant audits added after the frozen V5
  binary was produced.
- The v1 eight-configuration ReLU end-to-end smoke in
  `results/2026-07-17-filter-prefetch-stage1-smoke` completed without a hang.
- V5 includes the required first-touch rule: a piggybacked speculative response
  without real reuse evidence may use an invalid requester-L2 victim, but
  cannot replace any valid line. Cache and RDMA tests cover both the fill
  attempt and invalid-only enforcement.
- V5 also fixes real-demand takeover of a not-yet-sent remote candidate,
  exposes both predictor capacities, models the shared Filter ports and
  latency, and reserves no demand MSHR or port for prefetch.

The V5 checkpoint supersedes the earlier redesign binaries for new
experiments.
Historical result directories remain immutable evidence and are not mixed with
the new binary.
