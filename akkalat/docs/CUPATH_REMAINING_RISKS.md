# CuPath remaining risks and current limitations

> **HISTORICAL V5 RECORD — INVALID FOR THE FINAL PAPER.**  The performance
> values below came from the pre-dispatch-limited V5 campaign and are retained
> only for methodological comparison.  Runtime-stop V6 results must replace
> them before this document can again describe current evidence; see
> `CUPATH_V6_GOAL_AUDIT.md`.

## Current deadline M1 candidate is not performance-validated

The current source repairs optimistic useful accounting, uses page-bounded
1-to-2-to-4-to-8 late convergence for local M1, prevents repeated stale
feedback from inflating one distance, reserves demand MSHR headroom, and lets
local M1 propose its first 64-B candidate when two equal real-demand strides
establish PATTERN.  Early issue and exponential feedback are deliberately not
enabled in requester RDMA, which retains its conservative linear policy.
Focused tests pass, but the previous screens were stopped before this policy
had a valid Baseline/M1 result.  The previous candidate binary is therefore
obsolete relative to source.

The production platform now shares one local predictor across the four L2
slices in a GPM, plus the existing requester-RDMA predictor.  Historical V5
text below charges four local predictors and must be recalculated only after a
candidate is selected; it is not current hardware-cost evidence.

## The requested 1.5x target is supported by the completed formal campaign

All fourteen Baseline/Complete pairs from V5 are complete and measure a
1.5339x geometric mean, with fourteen positive workloads under the fixed
1.005x threshold. M1, M2, and M3 are complete at 1.0079x, 1.3454x, and
1.2325x. The five-configuration campaign contains all 70 cells. The frozen
binary, workload set, admitted-workgroup cap, and sampling controls are not
changed in response to these results.

Legacy ideal-L2/DRAM experiments remain useful only as diagnostic bounds and
must stay separate from V5. They already suggest that lookup and row timing
alone cannot create a 1.5x result. The current design's strongest source of
gain is exact remote deduplication and aggregation; prediction supplies extra
coverage but also creates physical work. Final retention therefore depends on
the formal demand-delay and DRAM-work counters, not on the story alone.

## M3 is currently weak

M3 alone is 0.9992x on the V5 representative screen. Its admission policy is
deliberately conservative: a first-touch predicted line cannot evict an
ordinary valid line, and ordinary remote data require multiple real waiters or
prior SEEN evidence. This prevents capacity gains from being confused with a
new cache but leaves little standalone speedup. Complete nevertheless records
582 requester-L2 hits in the screen. The formal work counters must distinguish
invalid-victim attempts, recurrence-qualified fills, useful hits, protection
drops, and unused retirements before attributing a performance effect to M3.

The completed long cells show that M3's benefit is concentrated rather than
uniform: FIR, MM, SC, and I2C gain 2.152x, 2.725x, 2.325x, and 1.338x,
respectively, while most other M3 cells are close to 1x. The final M3 result
has six positive and eight neutral workloads, so this concentration remains
visible in the paper.

Across the fourteen Complete runs, the existing requester L2 installs
7,061,285 remote-clean lines and retires 996,373 (14.1%) without a useful
hit. This percentage is an observed lower bound, not a final unused rate:
4,966,727 installed lines remain resident at the ends of their independent
runs and are not classified as useful or unused. The 7,061,285 installs
partition exactly into 5,353,515 invalid-way fills and 1,707,770 replacements
of older remote-clean lines; they also equal 2,094,558 tracked evictions plus
the 4,966,727 final resident lines. The largest workload-level sum of
per-slice occupancy peaks is 1,178,835
lines, a conservative non-simultaneous upper bound equal to 37.5% of wafer L2
data lines. This is not a negligible footprint. The protection rule is doing
real work, however: remote fills displace zero ordinary local-clean lines and
zero two-touch local lines; 1,707,770 replacements evict only older
remote-clean lines. The paper must report both the occupancy bound and the
zero-local-displacement evidence rather than claiming pollution is absent
from unused-fill count alone.

V5 uses SEEN as a pre-admission two-touch hint and removes it once an exact
requester-L2 hit or successful fill supersedes that hint. A useful retained
line is marked used and keeps an associated real-demand-established PATTERN
eligible, but V5 does not create a second persistent SEEN record while the
line is RESIDENT. Consequently, a non-predicted line may need to expose
recurrence again after it is later evicted. This saves a redundant history
entry but limits M3's reach; the paper must not claim reuse history across
arbitrary resident-line evictions. Complete separately records 160,257 unused
patterned retirements that delete PATTERN. The aggregate useful-hit counter
does not isolate the subset carrying a pattern key, so the paper also does
not attribute every requester-L2 hit to prediction feedback.

## Low-priority Filter metadata saturates in long runs

The four-workload sensitivity screen records only 27 insertion failures at
32K slots, but the fourteen long Complete runs record 9.42 million failed
PATTERN insertion attempts and 5.81 million failed SEEN attempts. This is
bounded, fail-closed loss of speculative coverage: RESIDENT and PENDING record
zero failures and all demand correctness remains protected by exact tags,
MSHRs, and RDMA line state. Still, 422 of 2688 slice-runs finish with PATTERN
fail-closed and 200 finish with SEEN fail-closed. The paper must report this
long-run saturation and cannot call 32K a universally low-failure point.

## M1 is workload-dependent and can delay demand

M1 removes definite-miss L2 lookups and issues a low-priority predicted line
only when the checked resources are immediately available. It does not reserve
an MSHR for prediction, because doing so would violate the fixed design rule;
once issued, however, a candidate necessarily occupies an ordinary MSHR until
completion. In the representative screen, Cuckoo coupling reduces measured
demand-delay events from 145,466 under ungated issue to 33,066, while improving
the geomean from 1.0059x to 1.0188x. In the long formal run, M1 is only
1.0079x and FIR regresses to 0.8984x. Complete issues 246,659 local
candidates, 161,403 of which reach the lower-module DRAM-issue point, and
records 6.76 million demand-delay exposure events. These Complete counters
cannot attribute the pressure to M1 because M2 changes remote arrival timing.
In the M1-only configuration, the fourteen workloads issue 261,615 local
candidates: 252,850 fill after reaching the DRAM-issue point and 8,765 lose a
race to an ordinary demand. Aggregate physical reads nevertheless fall from
39.25 million to 39.11 million, physical writes remain essentially flat
(20.83 million in both), and L2 MSHR-full stall cycles rise by only 0.22%.
The explicit additional-issue count is not a net physical-read delta because
demand merging and timing also change which ordinary reads reach DRAM. FIR is
the exception: its physical reads and writes rise by 2.93% and 6.08%, despite
only 2,942 prefetch issues, which
implicates the definite-miss/full-line-write tag-lookup shortcut and changed
request timing rather than global MSHR pressure. The shortcut still allocates
the full-line write in L2; it does not bypass the cache data path. M1 must
therefore be presented as selective supporting coverage,
not the main performance source. A demand-delay exposure event means only
that a demand encounters MSHR-full while a local prefetch is outstanding; it
is a correlation counter, not proof that the prefetch caused the delay.

## Approximate metadata is not itself the speedup

On the V5 coupling experiment, Filter-only is 1.0145x, ungated prediction is
1.0059x, Cuckoo-coupled prediction is 1.0188x, and exact-metadata prediction is
1.0192x. Cuckoo gating removes 34.7% of issued candidates while retaining
96.7% of useful candidates. Exact metadata is only 0.03% faster in geometric
mean; four false positives occur in 273,146 queries. The defensible role of the
Filter is therefore compact admission and lifecycle coordination. Data
movement, merging, and reuse create the speedup; the Filter bounds their waste.

Remote `PrefetchUseful` counts a real demand consuming a speculative entry,
including takeover before that entry joins a batch. It is therefore not a
subset of `PrefetchPiggybackLines`, and V5 does not preserve their exact
intersection. The legacy reporter alias that equates this count with avoided
remote requests is not used in the final analysis. Candidate-specific evidence
is limited to generated, Filter-dropped, actually piggybacked, demand-consumed,
and retired-unused populations; avoided demand transactions and packets are
attributed only to the separate exact-dedup and packet counters. This is an
instrumentation limit, not silently reconstructed evidence. The focused
`TestRemotePiggybackedPrefetchMergesLaterDemandWithoutSecondPacket` test does
prove the intended lifecycle---a real demand for an already piggybacked line
joins exact state and creates neither a second request nor a second packet---
but the long-run counter cannot quantify how often that exact intersection
occurs.

## Cost-model limitations

The Filter's 7.81% metadata overhead is nontrivial; a conservative 64-byte
bound for four local-slice predictors plus one requester-RDMA predictor per
GPM raises total metadata to 8.30% of L2 data capacity. CACTI estimates the
physical bucket array, not all hash
logic, metadata routing from RDMA, clocking, or placement near every L2 slice.
Its area is therefore a lower-bound implementation point.
The paper reports the storage calculation and CACTI assumptions explicitly.
V5 varies the Filter capacity, fingerprint bits, predictor capacity, and
lookup latency. Port width remains fixed at the modeled L2 throughput because
the current goal deliberately avoids a broad magic-number sweep.
The 16K-slot point is not a viable low-cost substitute in the short screen:
it records 156,726 insertion failures across four workloads, compared with 27
at 32K slots. The long-run saturation above remains a separate limitation.

## Methodology limitations

- The completed V5 screen uses 192 workgroups for seven representative
  workloads and is diagnostic only. The same-binary 76,800-workgroup,
  consistently sampled 14-by-5 campaign has all 70 cells and passes the
  formal execution audit.
- KMeans and SimpleConvolution use the paper's reduced, still hundreds-of-MB
  inputs for tractability. Their footprint and workgroup counts must remain
  visible in the workload table.
- The exact shadow map used to measure false positives exists only for
  analysis and testing; it is excluded from the hardware storage claim and
  never supplies a simulated data-path decision.
- Exact MSHRs, RDMA line tables, waiter lists, and packet
  descriptors are baseline or explicitly bounded control structures, not
  Cuckoo entries.
- The worktree contains prior user changes outside CuPath. A submission commit
  should be created only after the user reviews the complete diff and final
  results.

## Correctness and integration risks to recheck

1. Rerun writeback, protocol, and RDMA tests after the formal campaign
   completes.
2. Repository-wide tests remain blocked by stale translation test APIs,
   stale TensorParallelismSample APIs, and an MCCL PCIe-switch panic. Full
   builds and all CuPath-path packages pass; repairing unrelated legacy suites
   is outside this redesign and remains a repository-maintenance risk.
3. Confirm no mechanism source contains a paper benchmark name or
   benchmark-dependent threshold.
4. Confirm every 14-workload result reports 16 L1V MSHRs, four L2 slices, the
   same binary hash, and the expected Filter configuration.
5. Recompile the paper after replacing provisional Results text and figures;
   reject any stale 1.60x, separate-filter, row-reorder, or reuse-history claim.
