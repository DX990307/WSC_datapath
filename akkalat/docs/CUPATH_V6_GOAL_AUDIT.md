# CuPath V6 goal audit

This checklist is intentionally evidence-based.  A row is `Complete` only
when the named artifact exists and its verifier accepts the required scope.
The active Goal must not be closed while any row remains `Pending` or
`In progress`.

| # | Required deliverable | Status | Authoritative evidence |
|---:|---|---|---|
| 1 | Runtime-only max-WG stopper | Complete | `akkalat/baseline/runner/wgtracer.go`; ReLU and FIR end-to-end sidecars described in `CUPATH_V6_MAX_WG_AUDIT.md` |
| 2 | Removal of Driver admission limiter | Complete | `test_max_wg_never_appears_in_driver_partitioning`; no removed limiter symbol is present in Driver dispatch |
| 3 | Max-WG unit and source-invariant tests | Complete | Runner/Driver Go tests and CuPath Python source-invariant suite |
| 4 | Passive WG mapping reporter and audit | Complete | `wg_mapping.go`, `analyze_wg_mapping.py`; `2026-07-19-cupath-v6-runtime-stop-fir-audit/CUPATH_WG_MAPPING_AUDIT.md` accepts all four FIR cells |
| 5 | FIR remote-path analysis | Complete | `remote_origin.go`, `analyze_fir_remote_origin.py`, `CUPATH_V6_FIR_REMOTE_AUDIT.md`; the full-grid/76,800-observed-WG Baseline provenance run completed with return code 0 |
| 6 | Frozen V6 binary and SHA-256 | Complete | `/tmp/cupath-runtime-stop-frozen-v6`; SHA-256 `cbb5f1d6737d913ce0cbf01600244339c9d1d52855a5fe1bfb7c932a25ff391e` |
| 7 | Representative 7-by-5 result | In progress | `2026-07-19-cupath-v6-runtime-stop-representative7` has 9/35 accepted cells (AES Baseline/Complete, FWT Baseline/Complete, FFT Baseline/Complete, PageRank Complete, SPMV Complete, and MatrixTranspose Complete) and seven active workers using the frozen V6 binary; completion still requires the full mapping, return-code, allocation, and mechanism-counter audits |
| 8 | Formal 14-by-5 result | Pending | Must contain 70 accepted cells in a new V6 directory |
| 9 | Overall and per-mechanism geomeans | Pending | Generated only from the accepted V6 70-cell campaign |
| 10 | Measured local/remote ratio and grouping | In progress | Threshold protocol is frozen in `CUPATH_V6_TRAFFIC_CLASSIFICATION_PROTOCOL.md`; the 14-cell Baseline provenance campaign is 3/14 accepted (BitonicSort, FWT, and FIR) with two workers in `2026-07-19-cupath-v6-runtime-stop-baseline-traffic14` |
| 11 | Work, traffic, latency, pollution, and runtime analysis | Pending | Analysis scripts exist, but final values require the accepted V6 campaign |
| 12 | V5-versus-V6 comparison | Pending | `compare_cupath_v5_v6.py` exists; V6 inputs do not yet exist |
| 13 | Updated Design/Evaluation/Results | Pending | Paper must not be updated with performance claims before V6 formal audit |
| 14 | Final tests, binary/metadata audit, paper compile/layout, and residual-risk report | Pending | Must be rerun after all results and paper edits are final |

## Fixed experiment contract

- One frozen binary for all representative and formal performance cells.
- L1V has 16 MSHRs.  Each GPM has one 4-MiB L2 divided across four
  memory-bank-indexed slices; every slice is 16-way with 64 MSHRs,
  16 requests/cycle, and a 10-cycle directory lookup for both hits and
  misses.  `test_fixed_l2_geometry_and_latency_remain_explicit` locks the
  corresponding builder settings (`WithL2CacheSize`, associativity, MSHRs,
  request width, and directory latency) against accidental drift.
- Every logical request/cache line remains 64 B.
- RDMA is 8 transfers/cycle, 10 fixed cycles, and 64 outstanding.
- Sampled, branch-sampled, and kernel-sampled are identical across cells.
- The full requested grid uses the original Driver partition.  `max-wg` only
  ends the simulator after the configured number of naturally completed
  `MapWGReq` lifetimes; it never admits, filters, quotas, or rebalances WGs.
  If the complete launched grid contains fewer WGs than the configured limit,
  the workload instead completes all requested WGs naturally; the audited
  observation target is therefore `min(max-wg, requested_total_wg)`.
- No 128-B access, HLQ, fixed batching timeout, row continuation, fill
  forwarding, force-local formal result, or benchmark-specific tuning.

## Current execution gates

Stage 2 completed Baseline, M2, M3, and Complete for FIR with
`max-wg=76800`.  All four cells launched the full 524,288-WG grid, observed
exactly 76,800 WG lifetimes, returned zero, and used the same original 48-GPU
partition hash.  Their naturally observed WG sets differ and are reported
rather than equalized.  Stage 3's separate Baseline provenance profile passed
at the same observation limit and proves that FIR's remote traffic is repeated
access to the unified-device filter allocation on owner GPU 1, not a max-WG
placement artifact.  These diagnostic runs use candidate SHA-256
`f00062aeb8d67d8aa85f894f46d3ef2aa2a9da0e1438b390a894d1696bfe972c`.
The later candidate differs only by a trace-enabled FIR allocation diagnostic,
has separately passed the ReLU runtime-stop smoke, and is now frozen as
`/tmp/cupath-runtime-stop-frozen-v6` with SHA-256
`cbb5f1d673d913ce0cbf01600244339c9d1d52855a5fe1bfb7c932a25ff391e`.
No production-behavior edit is permitted after this freeze.

Stage 4 started the seven-workload by five-configuration representative
screen in `2026-07-19-cupath-v6-runtime-stop-representative7` with seven host
workers. Its 35-command metadata and frozen-binary SHA passed preflight. The
screen remains incomplete until every cell produces a successful result JSON,
metrics, and an accepted mapping sidecar and the full cross-configuration
allocation/mechanism audit passes.

On user request, the incomplete representative and Baseline-provenance
campaigns were stopped on 2026-07-19. Completed cells remain preserved and no
paper result is inferred from interrupted cells. The corrected V6 evidence
below motivated a separate M1 optimization candidate documented in
`CUPATH_M1_OPTIMIZATION_AUDIT.md`; `/tmp/cupath-runtime-stop-frozen-v6` itself
was not modified.

The first matching pair is FWT: Baseline is 62.318 us and Complete is
62.224 us, or 1.00151x (0.151%) speedup.  Both use the full 524,288-WG
partition, stop after 76,800 observed WGs, have identical allocation
footprints, and return zero.  Their naturally scheduled observed WG sets
differ, as expected under a runtime-only stop; no quota is used to equalize
them.
FWT exposes no remote requests, so M2/M3 cannot help.  Complete issues 18,691
M1 prefetches (11,418 useful and 7,227 late), but physical DRAM reads fall by
only 3,103/2,769,253 (0.112%) while aggregate L2 MSHR-full cycles rise by
580,114/169,797,864 (0.342%).  These counters are consistent with its nearly
neutral end-to-end result; they are not used to tune FWT.

FFT Complete is also accepted independently: Driver time is 241.925 us, the
full requested grid remains 262,144 WGs, and the runner stops at 76,800
observed WGs with zero return codes.  All 196 required Complete/execution
metrics are present.  It has no remote reads, issues 254,092 M1 prefetches,
records 238,584 useful and 175,967 late events, and adds 239,314 prefetch DRAM
reads.  Its matching Baseline is 242.855 us, so Complete is 1.003844x
(+0.3844%).  Complete reduces physical DRAM reads by 8,658 (0.1560%),
physical DRAM writes by 9,516 (0.3982%), and L2-to-DRAM 64-B requests by
8,527 (0.1536%), while aggregate L2 MSHR-full cycles increase by 1,619,583
(0.5395%).  Allocation footprints are identical.  The Baseline and Complete
cells use the same full original partition but have different observed WG-set
and per-GPU hashes at the runtime stop; this difference is reported and is
not repaired with pre-admission selection or per-GPU quotas.

PageRank Complete also passed the per-cell gate: both return codes are zero,
all 196 required Complete/execution metrics are present, the full requested
grid remains 1,048,576 WGs, and the runner stops at 76,800 observed WGs.  Its
Driver time is 5.623036 ms.  It records 13,978,098 remote logical reads,
10,684,416 remote wire lines, 3,230,619 requester-L2 hits, and 69,454/33,509
M1 issued/useful prefetches, while displacing zero ordinary local-clean L2
lines.  Performance remains unclaimed until the matching Baseline completes.

SPMV Complete independently passes the same per-cell gate: both return codes
are zero, all 196 required Complete/execution metrics are present, the full
requested grid remains 696,320 WGs, and the runner-side stopper reports
exactly 76,800 observed WGs.  Its Driver time is 3.097719 ms.  It records
979,643 remote logical reads and 979,636 wire lines, only five exact duplicate
reads, two requester-L2 hits, and 20,307/12,188 M1 issued/useful prefetches;
zero ordinary local-clean L2 lines are displaced.  As with PageRank, no
performance claim is made before its matching Baseline is accepted.

AES Complete was the seventh accepted representative cell.  It returns zero
from both launcher and simulator, contains all 196 required
Complete/execution metrics, launches the full 1,048,576-WG grid, and stops
through the runner-side MapWGReq limit at exactly 76,800 observed WGs.  Its
Driver time is 681.871 us.  It records 12,836 remote logical reads but only
508 wire lines.  The demand accounting closes exactly: 8,725 requester-L2
hits plus 3,641 exact duplicate merges plus 470 demand wire lines equals all
12,836 logical reads.  Only 38 speculative lines piggyback existing batches,
which explains the 470 + 38 total wire lines.  AES also records
283,127/51,410 M1 issued/useful prefetches.  The requester-L2 mechanism
displaces zero ordinary local-clean lines.  Its 4:08:42 host wall time is
retained only as simulator-cost evidence.  Its matching Baseline is now also
accepted at 681.479 us versus Complete's 681.871 us: Complete is 0.999425x
(-0.0575%), a neutral-to-slightly-negative result.  The allocation footprint
and original partition match exactly.  Complete reduces physical DRAM reads
by only 45/1,649,080 (0.0027%) while aggregate L2 MSHR-full cycles rise from
27,385,247 to 28,323,297 (+3.43%).  Although M2/M3 reduce 12,836 remote
logical reads to 508 wire lines, that remote work is too small a fraction of
AES's path to offset the local pressure.  This negative evidence is retained
without benchmark-specific tuning.

MatrixTranspose Complete is the eighth accepted cell.  Its complete workload
contains 32,400 WGs, fewer than the 76,800 upper bound, so it naturally runs
all 32,400 rather than duplicating or redistributing work.  The original
48-GPU partition covers exactly 0--32,400, the per-GPU observation count sums
to 32,400, the simulator returns zero, and no max-WG-specific filter is
present.  The host audit records `workload_completed_before_max_wg`; its
Driver time is 12.634316 ms.  Performance remains unclaimed until the matching
Baseline is accepted.

Only AES, FWT, and FFT currently have both Baseline and Complete.  Their
three-pair diagnostic geomean is 1.001592x (+0.1592%).  This partial value is
not an overall representative or paper geomean and must not be extrapolated to
the four workloads whose matching Baselines are still running.

The large gap from invalid V5 is primarily methodological, not evidence that
one production mechanism silently changed.  V5 reduced the launched grid to
76,800 WGs (`WG Per CU 50`) and drained that admitted sub-grid; V6 launches the
original grids (AES 1,048,576, FWT 524,288, FFT 262,144 WGs) and stops on the
76,800th normal Runner `MapWGReq`.  Consequently V6's Baseline observation
time falls relative to V5 by 1.59x for AES, 38.58x for FWT, and 11.12x for
FFT, while Complete falls by only 1.46x, 32.65x, and 8.34x.  The invalid
Baseline artifact therefore disappears faster than the Complete time and the
old relative speedups collapse.  V5 also attributed 1.179x FWT and 1.306x FFT
speedups to M2 although the corrected V6 window records no remote requests for
either workload, direct evidence that those old remote gains cannot be treated
as mechanism performance.

The corrected three-pair counters expose a second, genuine limitation: M1
currently removes very little physical DRAM work and often adds L2 pressure.
AES removes only 45/1,649,080 physical reads while L2 MSHR-full cycles rise
3.43%; FWT removes 0.112% of reads while MSHR-full cycles rise 0.342%; FFT
removes 0.156% of reads while MSHR-full cycles rise 0.540%.  M2/M3 cannot
materially accelerate FWT or FFT because their remote exposure is zero, and
AES's 12,836 remote logical reads are small relative to its local memory work.

The live representative and provenance campaigns currently run nine simulator
cells together.  A 2026-07-19 health snapshot measured 113.9 GiB host
`MemAvailable`, no swap use, and 136.1 GiB aggregate simulator RSS.  The
latest health sample measured 91.5 GiB `MemAvailable`, 153.92 GiB aggregate
simulator RSS, no swap use, and a later 31.38-GiB largest active-cell high-water
mark.  A subsequent sample measured 108.42 GiB `MemAvailable`, 137.21 GiB
aggregate simulator RSS, no swap use, and a 31.45-GiB MatrixTranspose
high-water mark; all nine simulators remained CPU-active.  The mixed nine-cell
campaign therefore still retains more than the required 50 GiB.  A later
sample observed MatrixTranspose at 34.10 GiB while it was still growing; its
final resident-set high-water mark was 35.75 GiB, invalidating the earlier
32-GiB admission estimate.  Future admission now uses a 40-GiB per-cell
budget and a 50-GiB reserve.  The formal command may
request all fourteen workers, but the launch-time memory cap is expected to
admit five on this host and can admit more only when actual `MemAvailable`
proves it safe.  This changes only host-side experiment
parallelism, not the frozen simulator or modeled hardware.

Host wall time is used only as a hang diagnostic, never as paper performance.
At the latest health check the four long-running first-wave Complete cells had
run for about 3.51 hours and every simulator still consumed a full CPU core or
more.  For rough context, the invalid V5 campaign required 4.29 hours for AES
Complete, 7.94 hours for KMeans Complete, 4.78 hours for MatrixTranspose
Complete, and 2.46 hours for PageRank Complete.  The V6 processes therefore
remain within the prior broad runtime envelope under heavier nine-cell host
co-scheduling; the V5 numbers are not reused as architectural results.

The separate fourteen-Baseline traffic-provenance campaign started alongside
the representative screen with two workers.  Its launch recorded 136.741 GiB
`MemAvailable`, a four-worker memory cap, and an intentionally lower requested
count of two so the existing seven cells retain growth headroom.  Its exact
14-command grid, mechanisms-off flags, trace destinations, frozen SHA, and
50/21.5-GiB memory contract passed the strict protocol auditor before any
profile was accepted.

BitonicSort is the first accepted provenance profile.  It launches the full
2,097,152-WG grid, stops after 76,800 naturally observed WGs, and reports
17,513,740 data requests: 17,513,740 local and zero remote.  Under the frozen
classification protocol it is `Exact-local`.  This is a complete aggregate
over the observation interval; the 100,000-row cap applies only to saved raw
examples.

FWT is the second accepted provenance profile.  It launches the full
524,288-WG grid, stops after 76,800 naturally observed WGs, and returns zero
from both the launcher and simulator.  The complete aggregate contains
44,486,792 data requests (26,494,680 reads and 17,992,112 writes), all local,
for a zero remote fraction and the frozen `Exact-local` classification.  Its
42:46 host wall time is diagnostic only.  The two-profile strict partial
protocol audit passed with the frozen binary SHA and the original 2-worker,
50/21.5-GiB launch contract.  Its stable partial output is
`2026-07-19-cupath-v6-runtime-stop-baseline-traffic14/partial_analysis/`
and will be replaced by the complete fourteen-profile analysis, not treated
as a final paper table.

FIR is the third accepted provenance profile.  Both return codes are zero,
the full requested grid remains 524,288 WGs, and the runner stops after
76,800 naturally observed WGs.  Its complete aggregate contains 27,836,021
data requests: 23,593,258 local and 4,242,763 remote, for a 15.242% remote
fraction and the preregistered `Local-dominant` class.  Every remote request
is a read of the shared `filter` allocation on owner GPU 1; input and output
traffic remains local to each requester.  This independently reproduces the
earlier FIR path diagnosis under the frozen performance binary.  The strict
three-profile partial protocol audit passed and regenerated the partial CSV
and CDF artifacts; the campaign immediately admitted FFT into the freed
worker slot.

## Preflight evidence (not completion evidence)

- `CUPATH_V6_EXPERIMENT_RUNBOOK.md` records the frozen SHA, exact
  representative/formal/provenance commands, strict `--rerun-missing`
  procedure, and the required analysis order.  The future formal and traffic
  directories were confirmed absent before launch.
- Runner dry-run expands the representative screen to 35 unique cells: seven
  named workloads by Baseline, M1, M2, M3, and Complete.
- Runner dry-run expands the formal campaign to 70 unique cells: all fourteen
  paper workloads by the same five configurations.
- Every dry-run command carries `max-wg=76800`, 16 L1V MSHRs, four memory
  banks, identical sampled/branch-sampled/kernel-sampled flags, and disables
  row continuation and fill forwarding.  None carries 128-B or force-local
  behavior.
- The separate Baseline provenance dry-run expands to fourteen trace-only
  profiles with every CuPath mechanism disabled.  These profiles are the
  future input to the preregistered traffic classification; they are not
  performance cells.  Its auditor rejects duplicate or extra command flags
  and requires every result JSON to report success, launcher return code zero,
  and simulator return code zero.  It also binds each result to the exact
  metric and WG-mapping artifacts before consuming the trace summaries.
- The final analyzer requires all 70 return codes, runtime-stop sidecars,
  full-grid partitions, identical allocation footprints, 192 DRAM instances,
  192 L2 slices, and the frozen binary hash before producing a paper report.
  Its report explicitly covers M1 issued/useful/late/unused, M2 exact merges,
  packets/wire lines/width stalls, M3 probe/fill/unused/pollution counters,
  L2 tag and MSHR changes, physical DRAM reads/writes, demand latency, Filter
  accuracy/occupancy, simulator runtime, and counter-supported negative
  evidence.
- The representative, Baseline-provenance, and formal metadata audits require
  their exact grids, declared worker counts, the exact allowed command-flag
  set (including the benchmark and resolved metric destination), the recorded
  frozen SHA, and configuration-correct mechanism switches.  Formal cells
  explicitly reject 128-B, HLQ, fixed wait/timeout, global scheduler,
  force-local, and provenance-trace flags.  Missing-cell resume skips a cell
  only after validating both zero simulator return code and a complete
  runtime-stop mapping sidecar; an empty or corrupt sidecar is rerun.  The
  final analyzer independently requires exactly 70 successful result JSONs,
  verifies both runner and simulator return codes, and binds each result to
  its exact metric and WG-mapping artifacts.
- Formal metadata additionally records and audits the 50-GiB host-memory
  reserve, 40-GiB per-cell admission budget, launch-time
  `MemAvailable`, and the resulting worker cap.  These are host-execution
  safeguards and never enter a simulator command.
- The already running two-worker Baseline-provenance metadata retains its
  recorded 21.5-GiB launch budget; direct host monitoring shows the combined
  nine-cell campaigns remain well above the 50-GiB reserve.
- `compare_cupath_v5_v6.py` preserves the complete 14-by-4 comparison, reports
  invalid-V5 versus runtime-stop-V6 geomeans, isolates FIR, and labels the old
  results as methodological evidence only.  It cannot be executed for the
  final comparison until the V6 speedup table exists.
