# CuPath V6 pre-change audit

Captured on 2026-07-19 before repairing `--max-wg`.

## Repository state

- Branch: `observation`
- HEAD: `dc1dac768de285851662b14e168fc1f8545db794`
- Worktree: dirty; pre-existing user work is preserved
- Tracked diff summary: 73 files, 6,478 insertions, 7,320 deletions
- Free space: approximately 204 GiB on the filesystem containing both the
  repository and `/tmp`
- `git diff --check`: passed

The authoritative pre-change diff is the dirty worktree relative to the HEAD
above.  Its file-level status and diff summary were captured before edits;
this repair does not use `git reset`, `git checkout`, or a stash and does not
discard unrelated changes.

## Pre-change build and test state

- `GOCACHE=/tmp/gocache go test ./baseline/runner` in `akkalat`: passed
- `GOCACHE=/tmp/gocache go test ./driver` in `mgpusim`: passed
- `python3 -m unittest discover -s akkalat -p 'test_cupath*.py'`: 34 passed
- `GOCACHE=/tmp/gocache go build -buildvcs=false` in
  `akkalat/baseline`: passed

These tests describe the pre-change state; several currently encode the
incorrect admitted-and-drained limiter and therefore must be replaced rather
than treated as evidence for the corrected semantics.

## Historical V5 campaign

- Results: `akkalat/results/2026-07-17-filter-prefetch-v5-formal14`
- Metrics files: 70/70
- Successful stdout records: 70/70
- Frozen binary SHA-256:
  `d4cc91359237de6293ad6931fe0216651091b0c3405828f47b5471f9e4d9b901`
- Status: **INVALID FOR FINAL PAPER** because Driver-side pre-admission
  truncation changed unified-GPU WG placement

The V5 directory is retained without deleting or overwriting raw results.
Its invalid performance values remain available only for the required V5/V6
comparison.
