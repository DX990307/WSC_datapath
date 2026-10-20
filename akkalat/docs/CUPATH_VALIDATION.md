# CuPath validation record

> **HISTORICAL V5 RECORD — INVALID FOR THE FINAL PAPER.**  This preserved
> validation describes the old pre-dispatch-limited V5 binary and results.
> It is not evidence for runtime-stop V6; the active validation status is in
> `CUPATH_V6_GOAL_AUDIT.md`.

## Current Filter-coupled implementation

Every logical demand and candidate is one ordinary 64-B cacheline request. A
bounded real-demand-only predictor generates at most one candidate per demand.
One physical Typed Cuckoo Filter per L2 slice shares PATTERN, RESIDENT,
PENDING, and SEEN metadata. M1 performs a RESIDENT known-miss shortcut and
best-effort local candidate issue; M2 permits a remote candidate only to
piggyback an existing owner/page batch; M3 uses real recurrence/use feedback
to admit clean data into the existing requester L2. There is no new cache,
timeout, speculative waiting queue, or global scheduler.

The current frozen binary is `/tmp/cupath-filter-prefetch-frozen-v5`, SHA-256
`d4cc91359237de6293ad6931fe0216651091b0c3405828f47b5471f9e4d9b901`.
V5 models bounded Filter ports and latency, exposes the local and requester
predictor capacities, fixes demand takeover of a not-yet-sent remote
candidate, and reserves no MSHR or other demand resource for prediction.
The seven-workload, eight-configuration screen is
`results/2026-07-17-filter-prefetch-v5-stage2-wg192`; all 56 cells use this
binary and complete successfully. The current 14-workload formal campaign is
`results/2026-07-17-filter-prefetch-v5-formal14`. It has all 70 cells. Every
cell returns zero, retires every
admitted workgroup, and use the same admitted-workgroup count across
configurations for each benchmark under the common 76,800 cap. KMeans admits
73,728 workgroups because its paper input completes below that cap.
`cupath_workload_footprints.csv` derives evaluated workgroups and allocation
footprints directly from the baseline metrics; the formal analyzer also
requires identical 4-KB allocation counters in all five configurations.

All fourteen Baseline/Complete pairs are complete. Complete is 1.5339x
geometric mean, with fourteen positive workloads under the fixed 1.005x
threshold. The All Local, Mixed, and Remote group geometric means are 1.5840x,
1.7567x, and 1.1875x. M1, M2, and M3 are complete at 1.0079x, 1.3454x, and
1.2325x; M3 has six positive and eight neutral workloads.
Across the Complete cells, physical DRAM reads fall by 1.38% and writes by
0.71% relative to Baseline; prediction therefore does not obtain the formal
speedup by increasing aggregate physical memory work.

The M1-only attribution is kept separately in
`results/2026-07-17-filter-prefetch-v5-formal14/cupath_m1_attribution.csv`.
M1 issues 261,615 local candidates, of which 252,850 fill after reaching the
DRAM-issue point and 8,765 lose a race to an ordinary demand; 176,447 become
useful. The explicit additional-issue count is not a net work delta because
demand merging and timing also change ordinary DRAM requests. Across the
fourteen workloads, total physical reads fall by 0.35%, physical writes are
essentially unchanged, and L2 MSHR-full cycles rise by 0.22%. Thus the much
larger MSHR-full counter observed in Complete is not attributable to local
prediction alone; M2 changes remote arrival and merge timing. FIR remains the
M1 exception and is reported rather than hidden.

The representative screen gives the following geometric means. Diagnostic
configurations are retained to explain coupling and do not enter the formal
ablation.

| Configuration | 7-workload geomean |
|---|---:|
| Baseline | 1.0000x |
| Filter only | 1.0145x |
| Predictor only | 1.0000x |
| Ungated prefetch | 1.0059x |
| Filter-coupled local prefetch | 1.0188x |
| M2 | 1.2140x |
| M3 | 0.9992x |
| Complete | 1.2325x |

Predictor-only records 15,249 candidates without issuing any request and has
the same Driver time as Baseline for every screened workload. This checks that
candidate observation by itself is not being counted as a timing benefit and
that disabled issue does not perturb the modeled request path.

The coupling comparison is in
`results/2026-07-17-filter-prefetch-v5-stage4-exact-wg192`. Relative to
ungated issue, the Cuckoo-gated path removes 34.7% of issued candidates while
retaining 96.7% of useful candidates; useful-per-issued accuracy rises from
24.0% to 35.5%. Exact metadata is only 0.03% faster in geometric mean, and the
measured Cuckoo false-positive rate is 0.0015% (4 of 273,146 queries).

The four-workload sensitivity campaign is
`results/2026-07-17-filter-prefetch-v5-sensitivity`. Its tested capacity,
fingerprint, predictor-capacity, and zero/one/two-cycle lookup points span
1.2713x--1.2817x, versus 1.2757x at the main point. Halving the Filter to 16K
slots causes 156,726 insertion failures and is therefore rejected despite its
small timing difference. The selected 32K-slot, 13-bit, 64-entry-predictor,
one-cycle point has only 27 insertion failures and avoids choosing an ideal
zero-cycle lookup.

That short-screen failure count does not generalize to the long formal runs.
The fourteen Complete cells record 9,422,707 failed low-priority PATTERN
insertion attempts and 5,806,866 failed SEEN attempts, concentrated in
KMeans, MatrixTranspose, and PageRank. RESIDENT and PENDING record zero
failures. These low-priority failures suppress speculation or reuse in the
affected slice and fail closed; exact tags, MSHRs, and RDMA line entries still
protect every demand. The paper reports this saturation as a limitation.

All v16 and pure-64B result directories below predate this redesign. They are
retained as legacy or rejected-direction evidence only and must not be mixed
with the current frozen binary.

## Legacy frozen v16 implementation

- Branch: `observation`
- Starting repository revision: `dc1dac768de285851662b14e168fc1f8545db794`
- Formal binary: `/tmp/cupath-baseline-v16`
- SHA-256: `8530e21b34aca1dbc5f9fef078de88b1adb32e6a8f241d7c93d042883a77e7ee`
- L1 vector-cache MSHRs: 16 in the platform builder, GPU builder, command-line
  report, representative runs, and formal run.
- Formal configurations: Baseline, M1, M2, M3, and Complete from one binary.
- Core row-aware DRAM reordering: disabled in all formal configurations.

The worktree already contained extensive uncommitted simulator and paper
changes before this validation pass. No reset, checkout, or cleanup command was
used. The binary hash, command, environment, and per-run flags are retained in
each result directory's experiment metadata.

## Functional checks

The following focused packages passed after introducing the shared typed
Filter and request-lifecycle changes:

- `akita/mem/cache/writeback`: typed-filter insertion/deletion, counted
  fingerprints, per-type fail-open, low-priority capacity protection,
  latency/width retry, local fast miss, exact MSHR merge, requester-L2 clean
  fill, write/invalidation, and flush/reset behavior.
- `akita/mem/dram`: ordinary 64-B access splitting plus command-order-
  preserving same-row continuation, including no-peer and conflicting-row
  fallback behavior.
- `mgpusim/timing/rdma`: fixed pipeline width/latency/outstanding limits,
  PENDING-guided exact deduplication, owner/page bitmap formation, partial
  response handling, line/waiter capacity, owner expansion, requester fanout,
  SEEN admission, requester-L2 fill, and write epochs.
- `akkalat`: ablation configuration generation, result parsing, missing-cell
  behavior, and experiment process handling.

Multi-cycle metadata smoke tests with a four-cycle lookup completed for AES and
KMeans after all metadata consumers were changed to schedule continued ticks
until their tickets become visible. This specifically checks the prior tail-
request hang condition.

V5 also includes a regression for demand takeover of a remote candidate that
has joined a batch but has not yet been sent. The demand appends its waiter,
turns the exact line entry into non-speculative work, and preserves that entry
until the ordinary response fanout completes. This fixes the prior KMeans and
Im2Col hang in which standalone-speculation cleanup could otherwise delete a
newly attached real waiter.

All three Go modules pass `go build ./...`. Repository-wide `go test ./...` is
not green because of pre-existing, out-of-scope suites: Akita has stale
translation tests in `mmu`, `mmuTLB`, `mmuCache`, and `l2tlb`, plus one legacy
TLB assertion; MGPUSim has API-stale `TensorParallelismSample` tests and an
MCCL PCIe-switch routing panic. The cache, DRAM, protocol, driver, CP/CU, and
RDMA packages used by CuPath pass. These failures are reported rather than
silently excluding the repository-wide commands.

## Legacy representative same-binary experiment

Directory:
`akkalat/results/2026-07-16-cupath-typed-v16-representative`

This 4-workload, 20-cell campaign completed with the frozen v16 binary. It is a
short design/diagnostic run, not the paper geomean.

| Configuration | 4-workload geomean |
|---|---:|
| Baseline | 1.0000x |
| M1 | 1.0214x |
| M2 | 1.2646x |
| M3 | 1.0005x |
| Complete | 1.2971x |

Complete speedups were 1.1317x for AES, 1.0975x for KM, 1.6780x for PR, and
1.3581x for MT. All four were positive under the predeclared threshold of
1.005x. M1 alone was positive for AES, PR, and MT and neutral for KM.

An additional PageRank end-to-end smoke run in
`2026-07-16-cupath-typed-v16-prefetch-link-smoke` observed 1,442 sibling lines
carried by bitmap owners and 2,576 speculative sibling lines received across
bitmap and single responses. Requester L2 admitted 2,527 and safely dropped 49.
These are real simulator-path counters, not a unit-test-only connection.

## Legacy metadata and M1 diagnostics

Directory:
`akkalat/results/2026-07-16-cupath-typed-v16-diagnostics`

All 44 cells completed for AES, KMeans, PageRank, and MatrixTranspose. The
four-workload diagnostic geomeans are 1.0102x for Cuckoo fast-miss only,
1.0276x for ungated adjacent prefetch, 1.0114x for Cuckoo-gated prefetch,
1.0214x for Complete M1, and 1.0209x for exact-metadata M1. The full-path
comparison is 1.3356x with metadata disabled and aggressive speculation,
1.2906x with exact metadata, 1.2967x with Cuckoo metadata, and 1.0102x for the
Cuckoo Filter alone.

The no-filter upper bound is 3.01% faster than Cuckoo metadata on this short
sample, so CuPath does not claim that the Filter maximizes unconstrained raw
speed. It issues 8.85x as many predictive sibling requests and speculative
bytes, however, with an 18.6% useful/predicted ratio versus 29.5% under Cuckoo
gating. Exact and Cuckoo metadata differ by only +0.47%. These results support
the narrower conclusion that the shared Filter is a compact, low-error way to
coordinate and bound the transformations; the transformations perform the
data movement. The complete machine-readable table and this trade-off are in
`CUPATH_DIAGNOSTIC_SUMMARY.md`.

## Legacy filter sensitivity

Directory:
`akkalat/results/2026-07-16-cupath-typed-v16-sensitivity`

All 48 non-main-point cells completed with the same four representative
workloads. Eleven through sixteen fingerprint bits span 1.2909x--1.2971x;
two, four, and eight slots per bucket span 1.2940x--1.2971x. Zero/one/two/four
lookup cycles produce 1.3038x/1.2971x/1.2896x/1.2170x. Width one incurs more
than three million lookup stalls and falls to 1.1961x, while widths four,
eight, and sixteen are within 0.14% of one another. The formal point therefore
uses 13 bits, four slots, a modeled one-cycle access, and width 16 rather than
the zero-latency upper bound.

## Legacy formal experiment

Directory:
`akkalat/results/2026-07-16-cupath-typed-v16-formal14`

The formal command uses all fourteen paper workloads, five configurations,
76,800 workgroups, identical sampling flags, 16 L1V MSHRs, and the frozen v16
binary. A 14-workload geomean is emitted only when all 70 cells are present;
partial geomeans are explicitly marked non-paper results. Positive, neutral,
and negative classes use thresholds fixed before the run:

- positive: speedup > 1.005x;
- neutral: 0.995x through 1.005x; and
- negative: speedup < 0.995x.

The final table must include every paper workload and any regression. No
benchmark is removed, renamed, or replaced to improve the geomean.

## Required evidence chain

`plot_cupath_typed_ablation.py` validates the 16-MSHR/four-slice invariants and
exports auditable tables rather than relying on values embedded in figures:

1. `cupath_speedup_table.csv`: driver time and speedup for all 70 cells;
2. `cupath_filter_statistics.csv`: physical configuration, occupancy, port
   stalls, and per-type outcomes/fail-open events; and
3. `cupath_work_reduction.csv`: skipped L2 lookups, preserved MSHR merges,
   64-B L2-to-DRAM requests, sample-weighted local/remote path latency,
   exact remote merges, packets, finite-capacity stalls, requester-L2 fills,
   hits, and eliminated repeated traversals;
4. `cupath_prefetch_statistics.csv`: real-demand coverage, stride classes,
   issue/drop causes, useful/late/unused outcomes, requester-L2 occupancy,
   replacement, and pollution counters;
5. `cupath_m1_attribution.csv`: Baseline-versus-M1 DRAM, MSHR, local-issue
   latency, and prediction pressure without attributing Complete's remote
   timing to M1;
6. `cupath_simulator_runtime.csv` and `cupath_execution_audit.csv`: wall-clock
   simulator cost, process return code, and admitted/retired workgroups; and
7. `cupath_controller_slice_detail.csv` plus its summary: all 192 modeled
   DRAM controller/bank instances and 192 L2 slices in every completed cell.

`analyze_cupath_formal.py` additionally audits the 14-by-5 command metadata,
frozen-binary SHA-256, fixed mechanism flags, and component coverage. It
refuses to emit `CUPATH_FORMAL_ANALYSIS.md` while any cell or controller/slice
summary is missing.  Once all 70 cells exist,
`plot_cupath_typed_ablation.py` also requires the raw predictor, per-type
Filter, M1 resource-drop, M2 aggregation/width, M3 admission/pollution, and
physical-DRAM reporters before it will regenerate the paper tables; a missing
reporter cannot silently become a zero.

Formal wall times are reproducibility records, not a controlled overhead
comparison: most cells were co-scheduled, whereas the final KMeans/M3 cell was
resumed alone.  The completed
`2026-07-19-filter-prefetch-v5-runtime-screen-wg192` campaign instead runs the
same seven representative paper workloads and five configurations serially
with one frozen binary and one host worker.  Its protocol and normalized
`wall time / simulated GPU time` are checked by
`analyze_cupath_runtime_screen.py`. All 35 cells return zero. Complete's
normalized host cost is 1.1897x Baseline; M1, M2, and M3 are 1.0160x,
1.1313x, and 0.9933x. These are simulator costs, not hardware-performance
results.

The interpretation order is removed work, reduced traffic or latency, and
then end-to-end speedup. Speedup alone is not used to claim that a particular
transformation occurred.

## Hardware-cost checks

The current paper configuration has one 32K-slot typed Cuckoo Filter per 1-MB L2
slice. A slot contains a 13-bit fingerprint, two type bits, one valid bit, and
a four-bit counted reference. The array is 80 KiB/slice, or 15 MiB across 192
slices (7.81% of the wafer's 192-MiB L2 data capacity). The removed M1
provenance bit is not included.

Each GPM also has four 64-entry local predictors, one per L2 slice, and one
64-entry requester-RDMA predictor. Even charging a conservative 64 bytes per
entry adds only 20 KiB per GPM, or 960 KiB wafer-wide. The Filter plus this
conservative predictor bound is 8.30% of baseline L2 data capacity. The
predictors contain metadata only; they do not buffer cachelines.

The CACTI input/output and reproducible wrapper are in `akkalat/cost`. The
current 32-nm high-performance array point reports 0.6493 ns access, a
0.20731-ns random cycle, 0.0323376 nJ/read, 0.0347458 nJ/write, and 0.561476
mm2 for an 80-KiB array. These values
are array-level estimates and are not presented as a post-layout implementation.

## Paper artifacts

- Architecture source: `weeklyreport/hpca2027-latex-template 2/main.tex` and
  `sections/design.tex`.
- Raster figure generator: `Figure/generate_cupath_flows.py`.
- Raster figures: `CuPathoverall.png`, `M1.png`, `M2.png`, and `M3.png`.
- Compiled paper: `weeklyreport/hpca2027-latex-template 2/main.pdf`.

All four design figures show one shared typed Filter per L2 slice. They do not
show a separate RDMA filter, reuse-history table, new cache, row-aware queue,
or timeout-based batching structure.

## Historical rejected pure-64B precursor (superseded)

After removing logical 128-B accesses, we evaluated a default-off candidate
that trained a per-page PATTERN only after two real demands separated by the
same L2-slice stride. It then issued an independent 64-B request only after
parallel PATTERN, RESIDENT, and PENDING probes and an exact directory/MSHR
confirmation. It never waited for demand and used only invalid L2 victims.

At 48 workgroups, adding this candidate to the retained local path produced
incremental speedups of 0.9998x (AES), 0.9998x (FFT), 1.0038x (KMeans),
1.0000x (SimpleConvolution), and 1.0097x (MatrixTranspose), for a five-workload
geomean of 1.0026x. KMeans and FFT also increased L2-MSHR-full stall cycles.
That precursor was removed at the time because its improvement was too small.
The current redesign is not allowed to cite those numbers as its result: it
adds real-demand generation tokens, explicit default-off diagnostics,
Filter-port/resource drop accounting, existing-batch-only remote coupling,
and requester-L2 feedback, and is evaluated from a different frozen binary.

## Current pure-64B local-path validation

Directory:
`akkalat/results/2026-07-16-cupath-64b-current-local14-wg48`

The post-removal binary contains no logical 128-B request, sibling-pair
coalescer, or sibling prefetch path. L2-to-DRAM traffic remains a stream of
ordinary 64-B requests; the DRAM model's physical 128-B device organization is
unchanged. The local configuration retains only the RESIDENT negative lookup,
L2 fill forwarding, and work-conserving same-row DRAM continuation.

All 28 baseline/local cells completed with the same frozen binary (SHA-256
`9ee46dd75d063273abd2202006d0223298102eafd1fa7cc2fa2eec856768af54`), 16
L1V MSHRs, four L2 slices, and 48 workgroups. The 14-workload geomean is
1.0511x. Using the predeclared thresholds, 12 workloads are positive, two are
neutral (MatrixMultiplication at 1.0022x and MatrixTranspose at 0.9967x), and
none are negative. FFT is the largest gain at 1.2699x. The complete per-cell
times and speedups are in `cupath_diagnostic_speedup.csv`.

## Rejected non-blocking fill fallback

We tested a narrow work-conserving fallback that would return a read-only DRAM
response and discard its clean L2 fill only when the L2 fill-bank buffer was
full. Across AES, FWT, FIR, FFT, KMeans, and MatrixTranspose at 48 workgroups,
the fallback triggered zero times and every driver time was bit-for-bit
unchanged from the retained pure-64B local path. This rules out L2 fill-bank
backpressure as the source of the remaining MSHR pressure. The implementation
and counters were removed; raw negative-control metrics remain in
`akkalat/results/2026-07-16-cupath-64b-nonblocking-fill-screen-wg48`.

## Rejected RDMA pipeline-window collection

We also tested collecting remote 64-B reads while they traversed the existing
fixed RDMA pipeline. The first request was not delayed beyond its configured
pipeline completion time, and every response remained gated by its own normal
ready time. Across AES, FWT, MatrixMultiplication, PageRank, and
MatrixTranspose, the Complete geomean changed only from 1.1238x to 1.1245x.
Although packet counts fell for all five workloads, FWT fell from 1.2060x to
1.1973x. The roughly 0.06% geomean change does not justify the additional
request-ready bookkeeping, so the candidate was removed. Raw results remain
in `akkalat/results/2026-07-16-cupath-64b-pipeline-collect-screen-wg48`.

## Rejected DRAM ready-column promotion

We tested a bounded command-scheduling rule that allowed one already-ready
64-B column command to pass an ACT/PRE command, after which the deferred
management command regained priority. This used no timeout, request widening,
or unbounded row-hit run. Across AES, FWT, FFT, FIR, KMeans,
SimpleConvolution, MatrixTranspose, and SpMV, the local-path geomean changed
only from 1.0574x to 1.0586x. KMeans gained an additional 0.74%, but FIR and
MatrixTranspose regressed slightly. The rule was removed because the roughly
0.11% incremental geomean does not justify another scheduler state. Raw data
remain in
`akkalat/results/2026-07-16-cupath-64b-ready-first-screen-wg48`.

## Rejected queued-peer row continuation

We relaxed immediate row continuation only enough to keep a row open when the
next command already queued for that physical bank used the same row but was
not yet column-ready. It did not wait for a future arrival and did not reorder
around a conflicting row. All eight screened local-path driver times were
bit-for-bit identical to the retained ready-peer policy, so the broader check
was removed. Raw results remain in
`akkalat/results/2026-07-16-cupath-64b-queued-continuation-screen-wg48`.

## Rejected bounded row-hit snapshot drain

We tested whether a conflicting PRE command could expose a bounded snapshot of
already-ready same-bank, same-row 64-B columns. The snapshot admitted no future
arrival, had no timeout, and could not widen or merge requests. Across AES,
BitonicSort, FWT, FFT, FIR, KMeans, Im2Col, MatrixTranspose, and SpMV, the
candidate and control driver times were bit-identical and the snapshot formed
zero batches. Current auto-precharge plus immediate ready-peer continuation
already consumes the reachable opportunity; later conflicts surface as ACT,
not as a PRE with row-hit columns left to rescue. The implementation and flag
were removed. Same-binary A/B evidence remains in
`2026-07-16-pure64-row-snapshot-control-wg48` and
`2026-07-16-pure64-row-snapshot-candidate-wg48`.

## L2-MSHR capacity upper bound

As a diagnostic only, we built a temporary binary with 256 rather than 64 L2
MSHRs per slice while retaining the pure-64B local path. Source configuration
was restored to the formal 64-entry value immediately after the binary was
built. Across the same eight local-path workloads, speedup versus the formal
64-entry Baseline was 1.0585x, only about 0.10% above the retained local path.
Even this fourfold, non-paper capacity upper bound therefore does not explain
the remaining gap. Raw diagnostic results are in
`akkalat/results/2026-07-16-cupath-64b-l2mshr256-upper-wg48`.

## Removed DRAM physical-unit coalescing experiment

An offline check of the completed observation traces found that 10.7%--50.0%
of local 64-B DRAM read leaders had a different 64-B line in the same fixed
physical access unit while the first read was still in flight. The analysis
is in `akkalat/analyze_pure64_dram_coalescing_upper.py`, with all fourteen
rows in `akkalat/results/2026-07-16-pure64-dram-coalescing-upper.csv`.

We temporarily implemented a controller rule that merged a later, real 64-B
read into an already-running physical read with the same controller, PID, and
physical-access-unit identity. Although it did not widen the logical request,
it still made performance depend on sharing a fixed 128-B physical access.

Across all fourteen 48-workgroup screens, adding this rule to the retained
local path gives an incremental 1.0148x geomean, with eight workloads above
the predeclared 1.005x positive threshold and no workload below 0.995x. The
largest incremental gains are FIR (1.0457x), SpMV (1.0457x), BitonicSort
(1.0295x), and FWT (1.0283x). The combined local-path geomean versus Baseline
becomes 1.0666x. The two result directories are
`2026-07-16-pure64-dram-coalesce-candidate-wg48` and
`2026-07-16-pure64-dram-coalesce-candidate-rest-wg48`. A rebuilt snapshot
binary produced bit-identical FIR and MatrixTranspose driver times in
`2026-07-16-pure64-dram-coalesce-v2-smoke-wg48`.

The experiment closed only a small part of the requested 1.5x gap. Following
the decision to remove the 128-B access feature completely, its controller
state, runner flag, statistics, fanout path, and ablation configuration were
deleted. The result directories remain only as rejected-candidate evidence.
The current implementation sends every 64-B demand through an independent
DRAM transaction.

## Rejected late-victim-allocation direction

After the fourfold L2-MSHR upper bound failed to improve driver time, we added
temporary counters for a different hypothesis: misses might reserve a victim
way for the full DRAM latency and serialize the set. Across AES, FWT, FFT,
FIR, KMeans, MatrixTranspose, PageRank, and SpMV, all read-victim-locked,
evicting-line, and victim-bank stall counters were exactly zero, even though
some MSHR-full counters were large. Late victim allocation therefore cannot
remove observed work in these runs. The counters were removed; the diagnostic
metrics remain in `2026-07-16-pure64-victim-diagnostic-wg48`.

## L1V-MSHR capacity upper bound

The formal L1V remains fixed at 16 MSHRs. As a diagnostic upper bound only, we
reran nine representative workloads with 160 entries using the same frozen
pure-64B binary. The 160-over-16 geomean was 1.00006x: seven workloads and FFT
were bit-identical, FWT improved 0.29%, and KMeans regressed 0.23%. This rules
out L1V-MSHR capacity and MSHR-occupancy reduction as a broad explanation for
the remaining performance gap. The non-paper upper-bound data remain in
`2026-07-16-pure64-l1mshr16-control-wg48` and
`2026-07-16-pure64-l1mshr160-upper-wg48`; all formal configurations continue
to use 16 entries.

## Rejected RESIDENT way-hint candidate

We tested a pure-64B alternative that attached a four-bit L2-way payload and a
valid bit to each shared Cuckoo slot. A positive RESIDENT lookup first checked
the predicted way's full PID and tag; any collision, stale payload, or failed
exact comparison fell back to the ordinary 10-cycle directory lookup. Thus the
candidate never used an approximate positive as a cache hit.

Same-binary 48-workgroup A/B across all fourteen workloads produced only a
1.0054x incremental geomean over the retained M1. SimpleConvolution regressed
to 0.9896x and Im2Col to 0.9962x, while the largest gains were FWT (1.0256x)
and MatrixMultiplication (1.0252x). The payload also increased each shared
slot by five bits, about 25% over the retained 20-bit slot. This cost and the
negative workload fail the retention rule, so the way-hint implementation and
flag were removed. The auditable summary is
`akkalat/results/2026-07-16-pure64-wayhint-screen.csv`; raw control/candidate
metrics remain in the four `2026-07-16-pure64-wayhint-*` directories.

## Pure-64B ideal L2-and-DRAM diagnostic upper bound

To determine whether more local scheduling work could plausibly close the
1.5x target, we built a temporary diagnostic binary that retained independent
64-B transactions and the formal cache/bank organization but reduced the L2
directory from 10 to 1 cycle and the principal HBM timing parameters to their
near-minimal values. Refresh was moved beyond the measured interval. The
source was restored immediately after freezing the diagnostic binary; none of
these non-paper timing values is present in the retained implementation.

Relative to the formal 16-MSHR Baseline, this deliberately optimistic bound is
only 1.0800x geomean over all fourteen workloads. MatrixTranspose is 1.0015x,
KMeans 1.0185x, SpMV 1.0312x, and only FFT reaches 1.3646x. Therefore no
optimization that merely shortens L2 lookup or schedules the same set of 64-B
DRAM transactions can deliver a 1.5x geomean. The table is
`akkalat/results/2026-07-16-pure64-ideal-l2-dram-upper.csv`, with raw metrics in
`2026-07-16-pure64-ideal-l2-dram-upper-wg48`.

We also enabled all retained Complete transformations in the same optimistic
timing binary. Even this combined diagnostic reaches only 1.1358x geomean
relative to the formal Baseline, versus 1.0944x for the realizable Complete
design. MatrixTranspose remains 1.0025x and KMeans 1.0219x; FFT is the largest
at 1.4546x. The same-request-stream latency headroom left beyond current
CuPath is therefore only about 3.8% in geomean terms. Reaching 1.5x requires a
new source of actual work elimination rather than another lookup or scheduling
shortcut. The summary is
`akkalat/results/2026-07-16-pure64-ideal-complete-upper.csv`, with raw metrics
in `2026-07-16-pure64-ideal-complete-upper-wg48`.

To check that the 48-workgroup screen was not hiding a larger latency
opportunity, we repeated standard-versus-ideal timing at 192 workgroups for
AES, BitonicSort, FWT, FFT, KMeans, MatrixTranspose, and SpMV. The seven-workload
geomean is only 1.0711x, slightly below the 48-workgroup all-suite result.
Longer execution therefore does not rescue the same-transaction latency
direction; additional concurrency hides rather than amplifies much of the
remaining local latency. The summary is
`akkalat/results/2026-07-16-pure64-ideal-l2-dram-upper-wg192.csv`.

## Pure-64B local opportunity accounting

`akkalat/analyze_pure64_local_frontier.py` audits Baseline and M1 directly from
the clean 70-cell campaign. Across all fourteen workloads, M1 executes 35,737
physical reads versus 35,717 in Baseline; the 20-read difference is an FFT
timing-path variation, not work elimination. Thus the aggregate physical-read
reduction is effectively zero. M1 does filter 43,002 definite read misses,
preserve exact L2-MSHR merging, accelerate full-line writes without removing
partial-write RFOs, forward eligible fills, and reuse an already-open row for
8.7%--83.3% of column commands depending on the workload.

The largest sum of per-slice RESIDENT peaks is MatrixTranspose's 35,932 lines,
only 0.782% of the wafer's 3,145,728 L2-line capacity. Physical DRAM writes are
zero in these drained 48-workgroup metric windows, so a more elaborate write
scheduler cannot affect their measured critical path. The audit confirms that
the remaining allowed actions reorder or shorten the same requests; distinct
line elimination requires spatial fetch/coalescing or another explicitly new
source of reuse. Outputs are `analysis/pure64_local_frontier.csv` and
`analysis/PURE64_LOCAL_FRONTIER.md` under the clean campaign directory.
