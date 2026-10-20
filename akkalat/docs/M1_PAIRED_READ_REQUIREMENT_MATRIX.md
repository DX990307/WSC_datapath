# M1 Paired-Read Goal Requirement Matrix

Authoritative goal: the 403-line Goal-Mode prompt supplied on 2026-07-20.
This matrix distinguishes implemented behavior from evidence that is still
required. A passing smoke test is never treated as formal performance proof.

## Source and environment audit

- Branch: `m1-granularity-adaptation`.
- Starting commit: `dc1dac768de285851662b14e168fc1f8545db794`.
- The pre-existing dirty worktree is preserved; no reset or broad cleanup was
  performed.
- No experiment process was active at the first audit of this goal.
- Free space at that audit: approximately 201 GiB.
- Final diagnostic binary SHA-256 (independent responses, Pattern-filter
  coupling, explicit timely/late counters, and passive full-workload identity
  tracing):
  `ee6df3a1ceddcae3866727dd38d85916f06e5c99ce6c66d09466ab80e297472a`.
  A clean `go build -buildvcs=false` from the current source reproduced this
  exact hash; the mutable build output and campaign-local frozen snapshot also
  match it byte for byte.

## Frozen M2/M3 boundary

The following hashes define the post-audit freeze boundary. They must remain
unchanged unless a concrete correctness or interface bug is documented.

- Combined `mgpusim/timing/rdma/*.go` manifest hash:
  `4c95a6b9012fcd40362241f19099afb6a2ad3e7ae1b3e59c4511d7f18f4f3e29`.
- `remote_replica.go`:
  `584af2a852619ec0b00421cc93f18cbd68cac44ac27b3059f42a4ccf619d8ba3`.
- `remote_replica_test.go`:
  `db22eda8482b0c8681c0f6e6e478cbc7f9fbed60760f0c7eec5bded577109ff1`.

## Requirement status

| Requirement | Current evidence | Status |
|---|---|---|
| HBM full-channel DQ width is 128 bits and BL4 gives a 64-B modeled full-channel unit | AMD PG313 documents 32/64-B read and 64-B write transaction sizes plus a 32-B pseudo-channel burst; Micron documents `DQ[127:0]` and two 64-bit pseudo-channels. `r9nanobuilder.go` sets bus/device width to 128 and burst length to 4, while the paper explicitly describes the 64-B accounting unit as two 32-B pseudo-channel bursts; source-invariant test passes. | Implemented, primary-source-grounded, and tested |
| A paired descriptor is two independent physical 64-B reads | L2 emits two request IDs and one shared Pair ID; controller accounting requires `physical reads = frontend reads` and two paired members per descriptor | Implemented and tested; repeat on representative/formal results |
| Demand response does not wait for sibling | L2 matches each response by its independent request ID; controller test retires a completed demand while sibling remains inflight | Implemented and tested |
| No global row reorder; only same-pair row continuation | command queue requires a non-empty shared Pair ID, opposite roles, and adjacent columns; related and unrelated tests pass | Implemented and tested |
| Predictor trains only on real demands and proposes a direct sibling | page-local demand predictor, direct-sibling admission, and write/speculative exclusion tests pass | Implemented and tested |
| Per-slice Filter is reused by M1/M2/M3 | one physical typed Filter per slice; M1 uses the `PATTERN`, `RESIDENT`, and `GRANULARITY_PENDING` logical types and shares the physical Filter with the remote logical types. Final smoke reports 192 physical Filters and 132,120,576 total bits across the 48-GPM wafer. `akkalat/cost/FILTER_COST.md` preserves the CACTI inputs/outputs and reports 86,016 B and 0.5900 mm² per slice, 15.75 MiB and 113.3 mm² wafer-array totals, and a matched 1.29% Filter/L2 array-area ratio. | Implemented, runtime-counted, and cost-model consistency tested |
| Reliable negative skips exact sibling lookup; positive is confirmed | Lookup-plan tests pass; final `new_m1` prefix reports 1,838 negative skips and 229 exact checks, or 88.9% exact-lookup avoidance | Implemented and measured on prefix; representative evidence pending |
| Busy/not-ready/unreliable Filter falls back without waiting | The lookup tickets start in `topParser` when the real demand enters L2. They are consumed only after the ordinary miss reaches the write-buffer stage; a missing/not-ready/unreliable result clears the candidate and `fetchFromBottom` immediately calls the ordinary single-64-B path. Busy, not-ready, and unreliable tests pass. | Implemented, code-audited, and tested |
| Ordinary-demand and paired MSHRs are summarized as pending | ordinary pending insert/delete lifecycle and paired-sibling-leader tests pass; final runtime prefix observes 148 sibling inflight merges | Implemented and tested; representative evidence pending |
| Read-only, page/slice/controller/bank-row safe paired admission | partial writes/speculative reads do not train; page, cross-slice, cross-controller, and configured DRAM address-map tests pass. Exact-hash ten-config smoke `2026-07-20-m1-final-ee6df3-correctness-smoke10` completed normally and passed strict physical-read, paired-member, runtime-mode, and aggregate-local-continuation checks. | Implemented, unit-tested, and end-to-end smoke-tested; natural-workload evidence pending |
| Sibling uses existing L2 block/MSHR/fill storage | sibling has its own 64-B response and existing MSHR/block/fill path; no new data array | Implemented; follower integration needs runtime evidence |
| M3 remote-reuse victims are protected | admission rejects blocks tracked by M3; clean/unsafe/remote victim tests pass | Implemented and tested |
| Formal default never truncates WG execution | `DEFAULT_MAX_WG = 0`; positive values are diagnostic-only. A zero limit now retains a passive mapping tracer with no stop callback and reports kernel launches, exact mapped-WG identity, completed WGs, and observed/completed sampling coverage. Natural-completion smoke reports 1 kernel and 1,024/1,024 mapped and completed WGs with both coverage values equal to one. | Implemented, Go/Python-tested, and runtime-verified |
| Formal five configs only by default | Baseline/M1/M2/M3/Complete returned when no explicit diagnostic is selected | Implemented and Python-tested |
| Formal 14x5 analysis rejects historical capped/old-M1 methodology | `analyze_cupath_paired_formal.py` requires the exact 14-workload/five-config grid, the new granularity-adaptation mechanism matrix, no positive `max-wg`, one frozen binary, natural completion, 100% mapped/completed coverage, and two-physical-64-B accounting. Formal analysis now rejects rather than merely reports any launch-descriptor, original partition, global WG-set, or per-GPU WG-set difference from Baseline. It also requires a strict natural-completion 5x7 diagnostic grid from the exact same binary: Filter/usefulness/row-reuse gates come from the diagnostic controls, while all-workload M1 positive-majority and Complete non-regression gates come from formal. It reports campaign validity separately from the M1 `retain`/`reject` decision and preserves every failed reason instead of labeling valid negative results as mechanism success. The historical V5 analyzer remains quarantined rather than being relabeled. | Implemented and Python-tested; final diagnostic and 70-cell evidence pending |
| Campaign-local immutable binary | New launches copy each built or supplied binary into `<result>/frozen-binaries/<target>-<full-sha256>`, update every command to that path, and then write the manifest. Resume reconstructs the recorded command and refuses a changed or missing snapshot. | Implemented and Python-tested; the corrected representative and all later campaigns use immutable snapshots |
| Ten named diagnostic/formal configs | explicit old-M1, Filter-only, always-pair, predictor-only, no-Filter, new-M1 plus formal cells | Implemented. All ten completed exact-hash campaign `2026-07-20-m1-final-ee6df3-correctness-smoke10` using binary `ee6df3a...`; `analyze_m1_paired_read.py --strict` accepted all physical-read, physical-byte, paired-member, timeliness-partition, aggregate-row-reuse-bound, access-unit, generic-row-disabled, PairID-only continuation, and runtime-mode invariants. The run used `max-wg=1` and is explicitly correctness-only, not performance evidence. |
| Layered Filter and DRAM statistics | exact skips, all 61 reported granularity-mode/predictor/admission/usefulness counters, frontend/physical requests and bytes, and command/ACT/PRE/row-reuse work enter the strict summary. For all five logical Filter types, the summary also retains reliable state, queries/results, false positives, insertions/deletes, insertion failures, fail-open events, lookup/update busy drops, and current/peak occupancy. The analyzer derives predictor and sibling coverage, aggregate acceptance, physical bytes, terminal waste, lookup avoidance, speedup, and geomean, and validates the runtime granularity-mode matrix. Its explicit diagnostic gate passes only if useful exceeds unused, pair row reuse is observed, Filter gating reduces wasted sibling bytes versus the correctness-preserving no-Filter run, and more than half of applicable workloads exceed 1.005x. Plotting emits work-reduction, Filter-contribution, and DRAM-work rasters. | Implemented and analyzer/plot-tested; representative audit pending |
| Regression suites | On 2026-07-20, all Akita cache packages plus all DRAM packages, MGPUSim RDMA, and the Akkalat runner pass with Go test-result caching disabled via `-count=1`. Python discovery reports exactly 167/167 passing tests, including passive-full-workload resume, campaign-local binary snapshots, directly tested paired-formal/diagnostic hash coupling and retain/reject separation, strict launch/partition/global-WG/per-GPU identity rejection, analyzer, plot, source-invariant coverage, and executable M2/M3 freeze hashes. | Passed for the current source; repeat once more after any later M1 change and before the final freeze |
| Explicit sibling timeliness partition | final `new_m1` prefix reports 464 useful siblings = 314 timely + 150 late; the strict partition invariant passes | Implemented and runtime-tested on prefix; representative evidence pending |
| Filter-gated M1 sends less useless data than no-Filter expansion | Correctness-preserving no-Filter uses exact resident/MSHR checks; the final prefix still has identical 903 paired reads in both versions, while the Filter removes metadata work. Source audit confirms that both paths make the same exact resident/pending admission decision whenever Filter lookup/update resources do not force a fallback, so a systematic byte reduction is not expected by construction. | Traffic reduction is not yet demonstrated. The untruncated representative screen must quantify any fallback-induced difference, but the honest independent Filter value may be exact metadata-work elimination rather than lower data traffic. A retain decision must not reinterpret identical bytes as traffic suppression. |
| Representative diagnostic results | The first full-workload attempt exposed missing passive WG identity. After that fix, `...w12-v2` was stopped at 0/35 because generic row continuation was mistakenly conflated with the separate PairID-only aggregate control; its commands were mechanically correct but it is incomplete. `...w12-v3-rowcontinuation` then incorrectly enabled the generic same-row policy in 15 paired cells and was stopped at 0/35. Both directories carry explicit audit markers and are excluded. Runtime metrics and strict analyzers now require generic row continuation off in every cell and PairID-only aggregate continuation on exactly for all granularity modes. The corrected v4 campaign initially used 12 workers, but natural cells reached approximately 20.7 GiB RSS and reduced `MemAvailable` to approximately 63 GiB before any completion. It was cleanly interrupted with no accepted partial cell and resumed from its recorded immutable commands at eight workers/20 GiB per worker; rerun metadata `EXPERIMENT_RERUN_METADATA_20260720-223813-538595.json` preserves the exact 35-cell grid and hash. | Corrected campaign `2026-07-20-m1-final-paired64-representative5-sampled-w12-v4-paironly` uses binary hash `ee6df3a...`, campaign-local immutable snapshotting, eight active workers, and natural completion; evidence pending. |
| Full 14-workload formal results without WG prefix | none from the final binary | Missing |
| Figures and paper update | Design/Methodology use paired-descriptor/two-physical-64-B wording, five Filter types, full-workload execution, and corrected 256-entry predictor cost; the raster overview/M1/M2/M3 figures were regenerated with all five logical types and M1's two independent reads. The three diagnostic rasters separately expose issued/usefulness work, Filter effects on exact probes/wasted bytes/physical bytes, and physical DRAM bytes/ACT/PRE/pair-row reuse. The paper compiles. `plot_cupath_paired_formal.py` now strictly requires the final 14x5 grid and emits the Baseline-inclusive, grouped, single-column 300-dpi raster automatically from the formal analyzer. | Partial: old numerical performance figures/results remain frozen until new formal evidence exists. Observation O2/O3 still reference the historical V5 `stage2-wg192` figures and must be replaced or removed after the untruncated diagnostic/formal evidence is available. |

## Completion rule

The goal is not complete until every missing or partial row above has direct
evidence, the M2/M3 hashes are rechecked, all five formal cells use identical
full workloads, and the paper contains no native-128-B HBM claim.
