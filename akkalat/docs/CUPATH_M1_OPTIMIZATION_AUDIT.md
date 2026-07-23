# CuPath M1 optimization audit

## Scope and status

The corrected V6 runtime-stop screen showed that the original M1 executes but
does not create material end-to-end speedup.  The long V6 representative and
Baseline-provenance campaigns were stopped on request without deleting their
completed cells.  Optimization now proceeds in a separate candidate binary;
the frozen V6 binary and its results remain unchanged.

Obsolete pre-optimization binary: `/tmp/cupath-m1-adaptive-candidate-v7`

SHA-256: `0ec9e867ea91409cb9c18d583acc4ca06abe67723e4aa05d4c282f7622010dbc`

The source changed after this binary was built, so it must not represent the
current candidate.  The replacement below must pass the all-workload screen,
full representative screen, and final audit before formal experiments.

Completed V8 deadline-screen binary (built 2026-07-20):
`/tmp/cupath-m1-deadline-candidate-v8-20260720a`

SHA-256: `9a2383542a0b3972c1f037670419f1116ad9d7de5d28e2858cd9f56eee50a67f`

Its 14-workload Baseline/M1 runtime-stop screen completed 28/28 cells.  M1
reaches only 0.9927x geomean, increases physical reads by 1.834%, and provides
timely data for only 0.107% of real demands.  It is therefore rejected as the
paper M1 rather than promoted because its tests pass.

Active PC-separated, timeliness-trained V9 screen binary:
`/tmp/cupath-m1-pc-trained-candidate-v9-20260720a`

SHA-256: `9bf0e2621acc65317f5202e539e2db7cb10ea59bf561177fbb85f37edd5d3e7b`

V8 shows that 34.06 million of 44.55 million screen demands changed the
source-only predictor's observed stride.  One L1 source therefore aliases
independent memory instructions into a single unstable stream.  V9 carries
the originating instruction PC through ordinary read requests and indexes
the bounded predictor by PID, L1 source, and PC.  This metadata does not alter
routing or correctness.  V9 also permits only one training prefetch for a
stream until that stream demonstrates a timely hit.  A late trial advances
the page-bounded lookahead and releases one farther trial; a timely trial
enables continuous issue.  This is feedback-derived throttling, not a fixed
timeout, occupancy watermark, or benchmark-specific parameter.

Demand-covered-distance V10 candidate (built 2026-07-20):
`/tmp/cupath-m1-demand-covered-candidate-v10-20260720a`

SHA-256: `af20e058816afc6c0dbb403feb7c61e88a8103c08291f9e5f8631a82addfe405`

The V9 FWT screen exposes a second missed feedback channel: 68,354 candidates
reach admission after an ordinary demand has already allocated their MSHR.
V9 discards these redundant candidates without changing its lead distance, so
the same stream can repeatedly predict too near. V10 treats a demand-led MSHR
match as free evidence that the current lead is insufficient and advances the
same page-bounded exponential lookahead. It does not issue a request for the
covered line, wait for a timeout, or add a fixed distance. A prefetch-led MSHR
does not trigger this feedback. The V10 ReLU smoke completed both cells and
observed 125 such demand-covered events without a lifecycle failure; its
representative performance screen is still in progress.

V10 is rejected. Its demand-covered feedback did not survive the unstable
within-instruction stride stream often enough to improve timeliness: FWT was
unchanged at 1.0264x, while FFT/FIR/SPMV reached only 0.9878x/0.9936x/0.7898x.

V12 separates the cacheline positions emitted by one coalesced vector load so
the predictor learns across dynamic executions rather than mistaking the
already-visible footprint for a temporal stream. With a 256-entry screen table,
FWT and FFT improve to 1.0411x and 1.0045x, but FIR and SPMV remain 0.9853x and
0.7957x. In SPMV, 5,517 of 6,530 useful prefetches are still late and L2
MSHR-full stalls rise 22.09%. Position separation is therefore insufficient by
itself.

Rejected demand-validated V13 candidate:
`/tmp/cupath-m1-demand-validated-candidate-v13-20260720a`

SHA-256: `024bb6b13e4ec779157e8ab9261c2c0cdfa361115776f9048086a80a5ef88afe`

V13 permits a physical prefetch only after the same stream has produced a
candidate that an ordinary demand already covers. This uses real recurrence as
confidence before creating DRAM work; the covered candidate itself is never
issued. The rule is global, page-bounded, and has no timeout, workload switch,
or fixed lead distance. It suppresses nearly all physical prefetches (including
zero issued requests in several local workloads), so it is safe but fails to
cover the latency-hiding opportunity and is rejected as the final M1.

### Bank-safe and demand-horizon candidates

V14 removes demand validation from the production path and begins with at most
one outstanding speculative 64-B line per physical L2 slice.  This maps the
unproven issue budget to the existing sliced memory path rather than a timeout
or workload parameter.  Its four-workload 192-WG screen reaches 1.0159x,
0.9911x, and 0.9942x on FWT, FFT, and FIR.  It is safe but provides too little
coverage; only 572 FWT prefetches are issued.

V15 derives a stream's minimum lookahead from the ordinary demand MSHRs already
visible across the four L2 slices.  The candidate is placed just beyond that
observed demand horizon and remains page bounded.  Timeliness improves sharply:
FWT records 100 timely of 153 useful prefetches, FFT 5,818 of 6,984, and FIR
20,414 of 20,690.  Nevertheless, independent prefetch reads slightly increase
physical work and FIR remains at 0.9778x, so timeliness alone is not sufficient.

V16 clamps an otherwise out-of-page horizon candidate to the farthest
stride-aligned line still inside the current physical page.  This policy is
local-M1-only; remote prediction retains strict page-boundary rejection.  The
four-workload screen reaches 1.0235x FWT, 1.0136x FFT, and 0.9775x FIR.  FFT
issues 10,202 prefetches, 8,160 are useful, and 6,570 are timely, but physical
reads still rise from 803,311 to 803,781 and L2 MSHR-full stall cycles rise from
39.75M to 41.12M.  FIR similarly has 20,245 timely prefetches but higher demand
latency and lower performance.  This isolates issue pressure, rather than
prediction accuracy, as the remaining control problem.

Rejected V17 closes the loop around existing resource feedback.  Every slice
starts with one speculative issue credit; a timely real-demand consumption earns
one credit, while a late/unused outcome or demand-visible MSHR pressure removes
one.  The credit is bounded by the existing 64-entry slice MSHR capacity, and
the demand-headroom rule remains in force.  This is a parameter-free feedback
controller, not a fixed degree or benchmark-specific throttle.  The screen
rejects the hypothesis: FFT falls to 0.9795x and FIR to 0.9730x.  FIR records
32,768 timely predictions, yet physical reads increase from 179,552 to 181,712
and MSHR-full stalls increase from 3.13M to 3.45M.  Timely consumption does not
prove critical-path value, so rewarding it with more concurrency is removed
from production rather than retained as unnecessary state.

The V19 candidate returns to one outstanding speculative 64-B line per
physical L2 slice and removes two duplicated Filter operations.  PATTERN now
remains in the slice that installed the shared stream metadata instead of
migrating on every interleaved cacheline; RESIDENT and PENDING remain in the
candidate's target slice.  A reliable RESIDENT-negative result obtained during
admission is carried into the existing fast-miss directory path instead of
querying the same Filter again.  This changes neither membership semantics nor
cache allocation, and adds no storage.  V19 is screened as
`/tmp/cupath-m1-filterpath-banksafe-candidate-v19-20260720a`, SHA-256
`759738980ae4dda26f4ab5771a9989df7e25e1d31de1001bed074efaa19d357d`.

The final V20 candidate removes the rejected V13 demand-validation state and
the rejected V17 adaptive-credit state.  It also counts the existing live MSHR
entries directly when deriving the average four-slice horizon, avoiding a
per-demand scan of up to 256 request lists without adding simulated behavior.
The one shared predictor per GPM has 256 entries, matching the existing
aggregate four-slice MSHR count (4 x 64); this is one global hardware setting.
The frozen binary is `/tmp/cupath-m1-final-candidate-v20-20260720a`, SHA-256
`e8a0db4777350660d3573c3a6bdf71fb2265b466857a26b1a1bdcb0fb8465ff8`.

Its 192-WG four-workload lifecycle screen completes 4/4 cells with correct
runner-side stopping.  Against the retained Baseline cells, the small-screen
speedups are 1.0109x FWT, 0.9971x FFT, 0.9920x FIR, and 0.7888x SPMV.  The
SPMV Baseline and M1 WG-set hashes differ, so the last value is not a formal
performance claim.  Mechanism evidence is stable: FFT records 10,121 issued,
8,057 useful, and 6,717 timely prefetches; FIR records 27,825 issued, 22,340
useful, and 22,019 timely.  FIR nevertheless increases physical reads from
179,552 to 180,457 and MSHR-full stalls from 3.13M to 3.46M.  Thus V20 is
frozen as a bounded M1 implementation, not claimed to provide 1.5x by itself.

## V6 failure evidence

| Benchmark | Issued | Useful (old definition) | Late after DRAM issue | Redundant races | Net physical-read reduction | L2 MSHR-full change |
|---|---:|---:|---:|---:|---:|---:|
| AES | 283,127 | 51,410 | 45,027 | 230,975 | 0.0027% | +3.43% |
| FWT | 18,691 | 11,418 | 7,227 | 9,964 | 0.112% | +0.342% |
| FFT | 254,092 | 238,584 | 175,967 | 14,778 | 0.156% | +0.540% |

The old `useful` counter was optimistic: it rewarded a candidate as soon as a
matching demand entered the L2, even if the candidate had not yet allocated an
MSHR and later lost the race to the demand.  V7 counts useful only when the
demand finds a prefetch-led MSHR or a completed prefetched cache line.

## Trace-based lookahead bound

`analyze_pure64_prefetch_upper.py` analyzes page-local, requester-side demand
streams without changing the simulator.  The retained 2026-07-12 observation
traces are capped at 100,000 records and predate the corrected runtime stopper,
so they are an opportunity screen rather than final evidence.  Truncated
KMeans, MatrixTranspose, and SPMV gzip files are explicitly rejected.

| Benchmark | Best tested lookahead | Strict accuracy | Coverage with at least 34 ns lead |
|---|---:|---:|---:|
| AES | 4 | 68.22% | 11.72% |
| BitonicSort | 2 | 63.10% | 3.84% |
| FWT | 2 | 66.06% | 0.29% |
| FFT | 2 | 65.46% | 8.61% |
| FIR | 2 | 3.18% | 0.74% |
| FloydWarshall | 1 | 45.93% | 0.72% |
| PageRank | 1 | 65.62% | 3.06% |
| ReLU | 1 | 50.00% | 1.09% |
| SimpleConvolution | 1 | 61.09% | 1.14% |

No single fixed distance is both timely and accurate across workloads.  This
rules out selecting a global 2- or 4-line magic number as the V7 policy.

## Deadline candidate policy

V7 retains one 64-B candidate per real demand and introduces no 128-B access,
timeout, wait window, benchmark-specific parameter, or new cache.

1. A repeated real-demand stride starts at one-line lookahead.
2. M1 proposes its first candidate as soon as two equal real-demand strides
   establish PATTERN, rather than waiting for one additional demand.  The
   candidate still passes the modeled PATTERN/RESIDENT/PENDING Filter lookups.
3. A demand that reaches L2 before its predicted line is complete records late
   feedback immediately, before that demand generates its next candidate.
   Repeated useful-but-late evidence advances local M1 geometrically through
   1, 2, 4, and 8 learned strides, so a long-latency stream does not require
   one feedback round per extra line of lead.
4. Multiple outstanding candidates from the same lookahead can advance that
   distance only once; later feedback from the old distance is recorded but
   cannot inflate lookahead repeatedly.
5. If the late prediction later supplies the demand through a prefetch-led
   MSHR, useful feedback is recorded without applying the same late evidence a
   second time.
6. A completed-line hit is timely and keeps the smallest proven distance.
7. An unused line retires the pattern and returns it to training.
8. Candidates cannot cross the configured GPU page.  The page boundary, not a
   tuned distance constant or a benchmark-specific setting, bounds lookahead.
9. M1 continues to consult PATTERN, RESIDENT, and PENDING in the per-slice
   Cuckoo Filter and drops immediately on unavailable resources.
10. Speculative MSHRs reserve demand headroom proportional to the number of
   outstanding prefetches.

Early pattern-establishment issue and exponential late convergence are both
local-M1-only.  The shared requester-RDMA predictor retains its conservative
next-demand validation and linear feedback behavior, so this optimization
does not silently alter M2/M3 accounting.

The new metrics separate timely hits, prefetch-led late merges, demand-won
races, late-after-DRAM-issue events, headroom drops, and observed lookahead.
They also track current and summed-slice peak prefetch-only L2 occupancy, and
partition unused lines into runtime evictions versus reset/end-of-run
retirements.  Only the former is treated as direct cache-pollution evidence;
the latter is a censored reuse outcome.

Because `max-wg` stops at a runtime observation boundary, an issued prefetch
may still be in flight when metrics are reported.  V7 therefore exports the
live outstanding count and audits `issued = fills + redundant races +
outstanding`.  Outstanding requests are right-censored and are never folded
into the unused population.  Coverage is reported independently as
candidate, issued, useful, and timely requests divided by real demands; this
prevents accuracy among issued requests from hiding poor path coverage.

The M1 analysis also decomposes the L2-to-DRAM stream into real-demand reads
and independent 64-B prefetch reads.  It reports Baseline demand reads, M1
demand reads, M1 prefetch reads, and M1 total reads alongside physical DRAM
transactions.  This prevents a demand read merely shifted earlier and renamed
as a prefetch from being credited as net work reduction.

The cache now also reports real L2 demand-read latency from top-port
acceptance to response send for hits, misses, and MSHR followers.  Prefetch
transactions and requester-L2 lookup-only probes are excluded.  This is the
causal performance measure used beside speedup; `demand_delay_events` remains
explicitly labeled as correlation-only pressure exposure.

## Static issue-path audit

The local candidate is generated only while accepting a real L2 read.  It
then performs the modeled PATTERN, RESIDENT, and PENDING lookups.  Once those
lookups complete, it may enter the ordinary directory/MSHR path only if the
current demand-facing resources are idle; otherwise it is dropped.  The cache
ticks the top parser up to the configured 16 requests per cycle before this
last speculative admission step.  Consequently, a nonempty top port at that
point is not unused width: it means real demand remains after all modeled
front-end opportunities were offered to demand.  Removing that check would
let speculation compete with queued demand and is not an evidence-backed
timeliness fix.

An issued prefetch remains an independent 64-B lower-path request.  By itself
it normally advances the time of a future read rather than reducing the
number of cachelines DRAM must return.  M1 should therefore not claim that
prediction intrinsically removes DRAM transactions.  Its acceptance test is
instead conjunctive: the RESIDENT shortcut must remove L2 tag work; enough
predictions must finish before demand to reduce measured demand latency; and
M1 total L2-to-DRAM reads, physical reads/writes, MSHR pressure, and unused
line pollution must remain neutral or improve.  Demand reads displaced by
prefetch are reported separately from true net work reduction.

The remaining timeliness hypothesis is the learned lookahead, not an
unmodeled free issue slot.  The deadline candidate starts on pattern
establishment, applies late feedback before generating the next candidate,
and doubles only the local learned lead after distinct late distances.  This
single global policy is evaluated as one batched 14-workload Baseline/M1
screen; workloads are not tuned individually.  Acceptance still requires
timely coverage and demand-latency improvement without worse total DRAM work,
MSHR pressure, or runtime cache pollution.

## Verification

- Akita writeback focused tests: pass.
- MGPUSim RDMA focused tests: pass.
- Akkalat runner focused tests: pass.
- CuPath source-invariant, analysis, runtime-screen, and M1 frontier Python
  tests: 142 pass after adding the runtime-stop outstanding lifecycle and
  prefetch-export regression coverage.
- Akita, MGPUSim, and Akkalat baseline `go build ./...`: pass.
- `git diff --check`: pass.
- no 128-B or benchmark-specific production string found in the changed M1
  path.

A non-paper 7-benchmark Baseline/M1 screen at 192 normally mapped WGs
(`akkalat/results/2026-07-19-cupath-v7-m1-adaptive-smoke-wg192`) was stopped
on request.  Six Baseline cells completed successfully (BitonicSort,
FWT, FFT, FIR, ReLU, and SimpleConvolution).  AES Baseline and the only started
M1 cell (AES) exited with signal 15; therefore the directory contains no valid
Baseline/M1 pair and provides no performance claim.  No simulator is currently
running.

A later all-workload attempt in
`akkalat/results/2026-07-20-cupath-v7-m1-counter-smoke14` was also stopped when
the deadline policy above was selected.  Completed Baseline cells are retained
as historical diagnostics, but the old candidate binary predates exponential
local late convergence and must not be compared against the replacement M1.
