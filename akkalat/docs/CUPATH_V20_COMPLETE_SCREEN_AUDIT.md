# CuPath V20 M1 and Complete Screen Audit

## Scope

This document records the first 14-benchmark Complete screen after the V20
M1 cleanup.  It is a mechanism screen, not a paper result: every cell stops
after the runner observes 192 normally scheduled `MapWGReq` events.  The
full grid and original unified-GPU partition are retained, but configuration
timing changes which 192 workgroups start first.  Consequently, all 14
Baseline/Complete WG-set hashes differ and small per-benchmark speedups can
contain substantial scheduling-sample noise.

Same-binary V20 Baseline screen:

`akkalat/results/2026-07-20-cupath-v20-baseline-screen14`

V20 Complete screen:

`akkalat/results/2026-07-20-cupath-v20-complete-screen14`

Frozen binary:

`/tmp/cupath-m1-final-candidate-v20-20260720a`

SHA-256:

`e8a0db4777350660d3573c3a6bdf71fb2265b466857a26b1a1bdcb0fb8465ff8`

All 28 V20 Baseline/Complete cells returned zero, reported
`runner_map_wg_observed_limit`, and recorded exactly 192 observed workgroups.
Both campaigns use the same frozen binary.  As an additional regression
check, every V20 Baseline cell exactly reproduces both the driver time and
WG-set hash of the earlier V8 Baseline reference at
`akkalat/results/2026-07-20-cupath-v8-deadline-m1-screen14`.

## Performance screen

| Benchmark | Baseline driver time (us) | Complete driver time (us) | Speedup | Remote reads | Demand wire lines | Wire reduction |
|---|---:|---:|---:|---:|---:|---:|
| AES | 56.524 | 49.508 | 1.142x | 11,020 | 477 | 95.7% |
| BitonicSort | 3.237 | 2.927 | 1.106x | 0 | 0 | -- |
| FastWalshTransform | 9.767 | 9.662 | 1.011x | 0 | 0 | -- |
| FFT | 37.278 | 37.388 | 0.997x | 0 | 0 | -- |
| FIR | 20.229 | 19.844 | 1.019x | 376 | 47 | 87.5% |
| FloydWarshall | 3.496 | 3.438 | 1.017x | 11,782 | 4,977 | 57.8% |
| Im2Col | 5.391 | 5.190 | 1.039x | 21,978 | 10,387 | 52.7% |
| KMeans | 510.505 | 505.353 | 1.010x | 0 | 0 | -- |
| MatrixMultiplication | 127.204 | 122.224 | 1.041x | 898,020 | 71,578 | 92.0% |
| MatrixTranspose | 4,916.859 | 4,847.510 | 1.014x | 3,692,864 | 3,567,980 | 3.4% |
| PageRank | 1,459.298 | 1,305.264 | 1.118x | 3,963,492 | 3,042,593 | 23.2% |
| ReLU | 2.266 | 2.227 | 1.018x | 0 | 0 | -- |
| SimpleConvolution | 5.749 | 5.817 | 0.988x | 376 | 47 | 87.5% |
| SPMV | 229.114 | 289.538 | 0.791x | 90,550 | 90,550 | 0.0% |

The 14-benchmark geometric mean is **1.019x**.  Using a 1% screen
threshold gives 11 positive, one neutral, and two negative benchmarks.
These counts are descriptive only because the WG sets differ.  SPMV is the
clearest warning: V20 M1 and Complete are nearly identical for SPMV, while
both select a substantially different 192-WG sample from the Baseline.

## M1 evidence

Across the 14 Complete cells, M1 observes 41,722,699 real read demands,
generates 9,390,257 candidates, issues 434,083 ordinary 64-B prefetches, and
records 258,011 useful lines.  This gives:

- issued-prefetch accuracy: 59.4%;
- timely issued-prefetch fraction: 32.3%;
- useful coverage of real read demands: 0.62%;
- additional physical prefetch reads: 370,063.

The V20 policy therefore fixes runaway speculation and preserves demand
priority, but it cannot by itself deliver a large average speedup.  Its
coverage is too small, and each useful prefetch advances an independent 64-B
transaction rather than eliminating that transaction.  The Cuckoo Filter is
still useful for the reliable L2-resident negative shortcut and for rejecting
duplicate speculative candidates; it does not create DRAM work reduction.

## Complete-path evidence

Across the Complete cells, the RDMA path observes 8,690,458 remote reads and
sends 6,788,636 demand wire lines, a within-run weighted reduction of 21.9%.
The existing requester L2 supplies 1,269,938 hits, equal to 14.6% of observed
remote reads.  Exact duplicate work is removed through 207,682 pre-send and
422,122 in-flight merges (the aggregate duplicate counter is 631,776).

This work reduction produces visible gains in AES, MatrixMultiplication, and
PageRank, but does not dominate the end-to-end critical path across the full
suite.  MatrixTranspose reduces demand wire lines by only 3.4%, while SPMV
exposes no wire-line reduction in this sample.  Local-only workloads cannot
benefit from the RDMA stages.

## Decision

The honest same-binary V20 screen result is 1.019x, not the requested 1.5x.  Reaching
1.5x cannot be justified by further parameter tuning of the current M1
prefetcher: its fundamental action is latency shifting, not transaction
elimination.  The next valid step is a larger-WG same-binary validation of
representative workloads, preceded by simulator profiling because KMeans and
MatrixTranspose required 1:28:27 and 1:46:18 of host time even at 192 observed
workgroups.  No paper number should be updated from this screen.

## V21 demand-idle M1 follow-up

V21 adds one workload-independent admission condition: because the L2 slice
and its DRAM bank use the same configured interleave, a candidate issues only when
its target slice has no live MSHR. A busy slice drops the candidate
immediately. This keeps the ordinary 64-B request, the one-prefetch-per-slice
bound, and all fixed hardware parameters unchanged; it adds no timeout,
queue, cache, or tuned watermark.

Binary used for the first three-workload check:

`/tmp/cupath-m1-demand-idle-v21-20260720a`

SHA-256:

`2165106038fdc276229ce36538f997ad90218838fff7d7d5f34b7e93db545cc8`

Result directory:

`akkalat/results/2026-07-20-cupath-v21-demand-idle-m1-screen3`

| Benchmark | V20 Baseline (us) | V21 M1 (us) | Nominal speedup | Issued | Useful | Timely | Busy-bank drops |
|---|---:|---:|---:|---:|---:|---:|---:|
| AES | 56.524 | 56.614 | 0.998x | 295 | 192 | 137 | 26,170 |
| FFT | 37.278 | 36.340 | 1.026x | 6,590 | 4,712 | 3,858 | 90,130 |
| SPMV | 229.114 | 286.181 | 0.801x | 609 | 123 | 67 | 15,485 |

The gate recovers AES from the V20 768-WG M1 result of 0.972x and improves
the 192-WG FFT result from 0.997x to 1.026x. For SPMV it cuts speculative
issues from 2,319 to 609 and improves the V20 M1 driver time from 290.455 us
to 286.181 us, but the nominal baseline comparison remains invalid as a
causal estimate: the Baseline cell executes 453,503 demand-read samples while
V21 M1 executes 491,411, and their naturally observed WG hashes differ. A
768-WG V21 check is required before freezing the new behavior.

## Larger-WG and demand-shortcut controls

The 768-WG V20 Baseline/M1/Complete check completed for AES, FFT, PageRank,
and SPMV in `akkalat/results/2026-07-20-cupath-v20-representative-wg768`.
Complete speedups are respectively 1.145x, 1.001x, 1.010x, and 0.899x; their
geometric mean is 1.010x. The naturally observed WG sets and demand samples
still differ between configurations, so these values are descriptive rather
than paired-request causal estimates.

The 768-WG V21 M1 check in
`akkalat/results/2026-07-20-cupath-v21-demand-idle-m1-wg768-screen3` reports
1.007x for FFT and 0.897x for SPMV. The larger window therefore does not
reveal a hidden local-M1 speedup.

Two controls isolate the cause:

- RESIDENT definite-miss shortcut without prefetch:
  `akkalat/results/2026-07-20-cupath-v20-filter-only-screen3`; nominal AES,
  FFT, and SPMV speedups are 0.969x, 0.994x, and 0.777x.
- Filter-gated prefetch without the demand shortcut:
  `akkalat/results/2026-07-20-cupath-v21-filter-gated-prefetch-only-screen3`;
  nominal speedups are 1.014x, 0.986x, and 0.956x. It issues 486/7,345/897
  prefetches, of which 410/5,608/887 are useful.

The second control is materially safer. In particular, SPMV improves from
294.897 us with only the shortcut to 239.738 us with only Filter-gated
prefetch. Its demand count is 5.3% above the Baseline count, so the remaining
nominal loss is not a clean mechanism penalty and is near parity after simple
demand-count normalization.

V23 tested whether allowing a negative shortcut only while the target slice
had no live MSHR could preserve the gain without disturbing a busy bank. It
did not: SPMV takes 294.254 us and FFT is neutral at 37.290 us. V24 further
restricted the shortcut to a stable real-demand stride; SPMV still takes
289.135 us despite only 4,431 read bypasses. Both partial diagnostic
directories are retained:

- `akkalat/results/2026-07-20-cupath-v23-adaptive-idle-m1-screen3`
- `akkalat/results/2026-07-20-cupath-v24-pattern-gated-m1-screen3`

These controls show that an L2-negative result is not sufficient evidence
that earlier lower-path injection will shorten the critical path. A
momentarily empty L2 MSHR does not prove that the downstream DRAM command
queue and return path are empty, and even a small number of reordered demand
misses can change the tail.

## V25 M1 decision

The formal M1 and Complete configurations therefore disable the ordinary
demand-side resident-filter shortcut. The per-slice Cuckoo Filter is retained
as the physical admission structure for PATTERN, RESIDENT, and PENDING
metadata. It removes already-resident, already-pending, and unqualified
speculative work, while every ordinary demand follows the unchanged 10-cycle
L2 lookup path. Prefetch remains work-conserving, one independent 64-B line,
and is dropped immediately when no structural opportunity exists.

Frozen V25 binary:

`/tmp/cupath-m1-filter-admission-v25-20260720a`

SHA-256:

`9b4558ff06576d0c3d5c4dcd0eb02d7edd6ac7dcae7d007595ec2b297dc9f19f`

The same-binary Baseline/M1/Complete representative campaign is
`akkalat/results/2026-07-20-cupath-v25-representative7-wg192`.
