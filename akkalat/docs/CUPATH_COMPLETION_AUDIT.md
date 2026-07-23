# CuPath goal completion audit

> **HISTORICAL V5 AUDIT — INVALID FOR THE FINAL PAPER.**  This document is
> retained only as a record of the pre-dispatch-limited V5 campaign.  Its
> 70/70 completion, 1.5339x geomean, workload grouping, and paper-completion
> claims do not validate the runtime-stop V6 methodology and must not be
> cited as current results.  The authoritative active status is
> `CUPATH_V6_GOAL_AUDIT.md`; V6 remains incomplete until its representative
> and formal campaigns, analysis, paper update, and final audit all pass.

This file maps every final deliverable in the Filter-Coupled Prefetcher goal
to authoritative evidence. A checked item means the artifact exists and has
been inspected.

## Fixed invariants

- [x] Every logical cacheline and prediction request is 64 B. The local and
  remote implementations use the ordinary cacheline path; focused unit tests
  reject widened or standalone speculative traffic.
- [x] L1V uses 16 MSHRs. Formal metrics are rejected by
  `plot_cupath_typed_ablation.py` unless every loaded cell records 16.
- [x] L2 is 4 MiB/GPM, four 1-MiB slices, 16-way, 64 MSHRs/slice,
  16 requests/cycle/slice, and a ten-cycle hit-or-miss lookup.
- [x] Formal M1--M3 and Complete add no cacheline-data store or L1.5 cache.
  M3 fills only the existing requester L2.
- [x] Frozen V5 allocates one 64-entry local predictor in each L2-slice
  builder and one 64-entry requester-RDMA predictor per GPM. The paper and
  cost model charge all five instances/GPM (960 KiB wafer-wide under the
  conservative 64-B/entry bound), rather than the unused grouping helper.
- [x] Formal configurations disable fill forwarding and DRAM row
  continuation. There is no HLQ, fixed batching timeout, fixed prefetch wait,
  global scheduler, or reserved demand resource.
- [x] Searches of the cache, DRAM, RDMA, and runner hot paths find no
  benchmark-name condition.
- [x] M1's raw `controller_busy_drops` name denotes the L2 write-buffer/lower-
  input gate retained for metric compatibility. The implementation does not
  inspect or override the DRAM command queue; controller command rate, queue
  age, and physical work are measured separately.

## Deliverables and evidence

1. **Design document -- complete.**
   `FILTER_COUPLED_PREFETCH_BASELINE.md` defines the fixed baseline and
   lifecycle; `typed_key_filter_design.md` defines the shared physical Filter.

2. **Unified workflow and state transitions -- complete.**
   `typed_key_filter_state_transitions.md` specifies PATTERN, RESIDENT,
   PENDING, and SEEN creation, confirmation, failure, and deletion. The paper
   Design section and four raster flow figures present the same lifecycle.

3. **Implementation -- complete for frozen V5.**
   Local predictor/filter issue is in `akita/mem/cache/writeback`; remote
   prediction, exact deduplication, owner/page aggregation, response fanout,
   and requester-L2 reuse are in `mgpusim/timing/rdma`. The frozen binary is
   `/tmp/cupath-filter-prefetch-frozen-v5`, SHA-256
   `d4cc91359237de6293ad6931fe0216651091b0c3405828f47b5471f9e4d9b901`.

4. **Unit tests and correctness checks -- complete.**
   Focused Akita and MGPUSim tests cover typed metadata, fail-open behavior,
   demand takeover, candidate drops, exact waiter aggregation, requester-L2
   fill/eviction, and ordinary 64-B transport. The Python runner/analysis
   suite currently contains 92 passing tests. In addition to behavioral
   tests, `test_cupath_source_invariants.py` rejects a widened 128-B candidate
   path, changed fixed L2 geometry/latency, paper-benchmark names in the
   cache/DRAM/RDMA hot paths, a per-RDMA Filter allocation, or a separate
   remote-data store. A documentation audit also verifies that every named Go
   test below exists in the current tree. The final rerun passes all 92
   Python tests and every focused Goal-relevant Go package listed below.

   A repository-wide `go test ./...` audit on 2026-07-19 also exposed
   unrelated legacy failures outside the CuPath path: stale VM mock/test APIs
   in Akita, stale TensorParallelismSample test APIs in MGPUSim, and the MCCL
   acceptance test's incomplete PCIe route.  None of those failing files is a
   CuPath change.  All three Go modules still pass `go build ./...`, and the
   cache, DRAM, RDMA, driver, workgroup-limit, KMeans, and CSR packages named
   below pass their focused tests.  The final handoff therefore reports the
   focused Goal-relevant test set separately and does not claim that the
   repository's unrelated legacy test suite is clean.

   The Goal's explicit unit-test requirements map to named tests as follows:

   | Required behavior | Authoritative focused tests |
   |---|---|
   | 64-B logical candidates | `TestLocalFilterPrefetchNeedsPatternAndIssuesOne64BLine`; `test_local_and_remote_candidate_paths_are_64b_only` |
   | PATTERN real-demand build/feedback/delete | `TestDemandStridePredictorRequiresRealRepeatedStride`; `TestDemandStridePredictorUsesOnlyRealDemandTraining`; `TestDemandStridePredictorUnusedFeedbackRetiresPattern`; `TestUsefulSpeculativeRemoteFillRetainsPatternInSameSliceFilter`; `TestUnusedSpeculativeRemoteFillRetiresPatternInSameSliceFilter` |
   | RESIDENT fill/replacement/eviction | `TestResidentFilterTracksReplacementWithoutFalseNegative`; `TestRemoteReplicaReplacementKeepsResidentAndReplicaFiltersConsistent` |
   | PENDING latency/fail-open/lifecycle | `TestTypedFilterScheduledLifecycleUpdatesFailOpenUntilVisible`; `TestRemoteInflightFilterInsertFailureFallsBackToExactTable`; `TestFlushAccountsUnusedPrefetchFeedback` |
   | SEEN only from real remote activity | `TestRemoteDataPathSecondCompletedTransactionAdmitsFill`; `TestWriteUncacheableLineDoesNotPolluteRemoteReuseHistory` |
   | Useful, late, and unused outcomes | `TestLocalFilterPrefetchDemandFeedbackMarksInflightLineUseful`; `TestLocalDemandMergeReportsLatePrefetch`; `TestFlushAccountsUnusedPrefetchFeedback` |
   | Demand takeover/merge | `TestRemoteDemandTakesOverUnsentPrefetchWithoutLosingWaiter`; `TestRemotePiggybackedPrefetchMergesLaterDemandWithoutSecondPacket`; `TestRemoteDataPathMergesCollectingAndInflightReads` |
   | Immediate resource drops | `TestLocalCandidateDropsWhenControllerHasDemandWork`; `TestLocalCandidateDropsWhenMSHRIsFull`; `TestLocalCandidateDropsWhenOutputIsBusy` |
   | Invalid-victim-only speculation | `TestLocalPrefetchNeverReplacesAValidLine`; `TestRemoteFirstTouchPrefetchFillRequiresInvalidVictim`; `TestRemoteDataFillDoesNotDisplaceLocalCleanLine` |
   | Existing-batch-only remote candidate | `TestRemoteFilterPrefetchPiggybacksDemandBatchOnly`; `TestRemoteFilterPrefetchNeverCreatesStandaloneBatch` |
   | Bounded RDMA and waiter fanout | `TestRemoteDataPathAppliesBackpressureAtOutstandingLimit`; `TestRemoteDataPathBoundsDuplicateWaiters`; pipeline width/capacity tests |
   | Requester-L2 exact probe and admission | `TestRemoteSeenPositiveStillRequiresExactL2Lookup`; `TestRemoteDisabledFilterFailsOpenToExactRequesterL2Probe`; `TestRemoteDataFillInstallsCleanLineAndUpdatesFilter`; stale-generation/write tests |
   | Baseline/ablation isolation | `TestFilterPrefetchDisabledKeepsBaselineStateUnallocated`; `TestRemoteDataPathDefaultOffKeepsBaselineStateUnallocated`; `test_each_standalone_enables_only_its_mechanism`; metadata 14-by-5 audit |
   | stop/resume/frozen binary | all eight `BinaryManifestTest` cases in `test_runall2_process.py`; deterministic workgroup-limit tests |

5. **Formal runall2 ablation -- implemented.**
   Baseline, M1, M2, M3, and Complete are generated by the paper ablation in
   `runall2_config.py`; missing-cell reruns consume the recorded command and
   binary manifest rather than rebuilding or mixing results.

6. **Experiment metadata and binary hash -- complete.**
   The formal directory contains `EXPERIMENT_METADATA.json`,
   `EXPERIMENT_BINARIES.json`, and timestamped rerun metadata. The analyzer
   also exports return codes and admitted/retired workgroups for each cell.
   Before producing a final report, it verifies the unique 14-by-5 command
   grid, frozen-binary identity and SHA-256, 16-MSHR/four-bank/sampling flags,
   exact M1--M3 mechanism matrix, and disabled fill-forwarding/row-continuation
   controls.

   The execution audit also preserves the 4-KB page size, workload and total
   allocation-page counts for every cell. The final analyzer rejects a
   configuration-dependent footprint. The paper workload table now reports
   measured allocation footprints and evaluated workgroups rather than stale
   pre-resize input values; KMeans is 390 MiB/73,728 WGs and
   MatrixTranspose is 1,013 MiB/32,400 WGs. The directly reproducible compact
   source is `cupath_workload_footprints.csv` in the formal directory.

7. **Fourteen-benchmark formal campaign -- complete (70/70).**
   `results/2026-07-17-filter-prefetch-v5-formal14` contains all fourteen
   workloads and five configurations. KMeans/M3 completed on 2026-07-19 with
   return code zero after admitting and retiring 73,728 workgroups.

8. **Geomean and group results -- complete.**
   All Baseline/Complete pairs establish 1.5339x overall, 1.5840x All Local,
   1.7567x Mixed, and 1.1875x Remote geomeans, with 14/14 Complete workloads
   above the predeclared 1.005x positive threshold. M1 and M2 are 1.0079x and
   1.3454x and M3 is 1.2325x. M3 has six positive, eight neutral, and zero
   negative workloads.

9. **Usefulness, latency, and traffic analysis -- implemented.**
   `plot_cupath_typed_ablation.py`, `plot_cupath_work_reduction.py`, and
   `analyze_cupath_formal.py` export raw counters, the fixed-work audit,
   standalone M1/M2/M3 resource attribution, sample-weighted local/remote demand
   latency, weighted removed work, and simulator runtime.
   `analyze_cupath_controller_balance.py` preserves all 192 modeled
   DRAM-controller/bank instances and all 192 L2 slices per cell, exporting
   command-rate balance, maximum queue age, candidate distribution, and
   resource-drop counters. The formal analyzer requires that component table,
   verifies all 192 DRAM instances and 192 L2 slices in every cell, and
   intentionally refuses a partial campaign. Requester-L2 reporting also
   preserves installed, unused, invalid-victim, remote-replacement,
   local-displacement, and per-slice peak-occupancy counters, so cache
   pollution is not inferred from hits alone.

   Once all 70 cells are present, the table generator also rejects missing
   raw reporters rather than converting them to zeros: 11 Baseline, 33 M1,
   100 M2, 93 M3, and 185 Complete fields are mandatory per workload.  The
   all 70 raw cells satisfy their applicable sets with no missing field.

   The required-metric groups map to preserved raw evidence as follows.  The
   named CSVs contain counters rather than only derived percentages, so every
   reported ratio can be recomputed.

   | Goal metric group | Preserved evidence |
   |---|---|
   | Predictor demand, candidates, evidence, and stride | `cupath_prefetch_statistics.csv`: local and remote real demands/candidates, evidence-one/evidence-two, and five signed-stride bins |
   | Candidate target slice/controller | `cupath_controller_slice_detail.csv`: one row per physical L2 slice/controller; `cupath_controller_slice_summary.csv`: active-slice count and candidate CV |
   | Typed Filter by key type | `cupath_filter_statistics.csv`: queries, positive/negative results, exact-shadow-confirmed false positives (the exact-confirmation rejects), inserts/deletes/failures, fail-open events, lookup/update busy drops, current/peak occupancy, and shared-port stalls |
   | M1 issue, outcome, and resource drops | `cupath_prefetch_statistics.csv` and `cupath_m1_attribution.csv`: issued/useful/late/unused/merged, exact redundant races, lower-path-busy/MSHR/victim/output/PENDING drops, added logical and physical DRAM work, and demand-delay events. The raw lower-path counter retains its legacy `controller_busy` field name. |
   | M1 controller and DRAM behavior | `cupath_controller_slice_{detail,summary}.csv` and `cupath_work_reduction.csv`: per-controller commands, queue age, balance, issue latency, and 64-B DRAM work |
   | M2 aggregation and bounded transport | `cupath_m2_attribution.csv` preserves the standalone stage; `cupath_prefetch_statistics.csv` preserves Complete: no-batch/full-batch drops, actual piggybacks, demand-consumed predictions, pre-send/inflight/ready exact merges, policy-prevented speculative standalone packets, demand/prefetch wire lines, packet counts, bytes, fanout, and requester/owner width stalls. Candidate usefulness and piggyback are not treated as nested populations. |
   | M3 admission, reuse, and pollution | `cupath_m3_attribution.csv` preserves the standalone stage; `cupath_prefetch_statistics.csv` and `cupath_work_reduction.csv` preserve Complete: installed/invalid-victim/dropped fills, requester-L2 hits, unused evictions, unused PATTERN retirements, remote replacement, local-clean displacement, current/peak lines, probe gating, and avoided wafer traversals |
   | Latency and simulator overhead | `cupath_work_reduction.csv` preserves local issue, remote logical, batch, pre-network, and probe latency samples/totals/maxima; `cupath_simulator_runtime.csv` preserves every cell's host runtime |

   The formal host runtimes are retained for reproducibility but are not a
   controlled overhead comparison because cells were co-scheduled and
   KMeans/M3 was resumed alone.  A separate seven-workload, five-configuration,
   192-WG screen completed with the same frozen binary and `max_workers=1`;
   `analyze_cupath_runtime_screen.py` audits its launcher metadata, complete
   7-by-5 grid, exact M1--M3 mechanism matrix, fixed flags, and binary hash,
   then reports wall time normalized by simulated GPU time. All 35 cells
   return zero; Complete's controlled normalized host cost is 1.1897x
   Baseline (M1 1.0160x, M2 1.1313x, M3 0.9933x).

10. **Cuckoo coupling analysis -- complete for candidate control.**
    `results/2026-07-17-filter-prefetch-v5-stage4-exact-wg192` compares
    Filter-only, ungated, Cuckoo-coupled, and exact-metadata prediction. Cuckoo
    gating removes 34.7% of issued candidates while retaining 96.7% of useful
    candidates; exact metadata is 0.03% faster. Remote Complete counters
    separately record 5.13 million Filter-rejected candidates and 23.41
    million bypassed requester-L2 probes. Across Complete, PENDING produces
    4.39 million positive hints, has balanced 91.69-million
    insertion/deletion lifecycles with zero insertion failure, and exact RDMA
    line state confirms 4.24 million merged real demands. The analysis does
    not credit an approximate hint as a removed demand without that exact
    confirmation. A no-Filter remote timing comparison remains desirable
    supporting evidence but is not mixed into formal V5.

11. **Retention/deletion evidence -- complete and retained.**
    `CUPATH_REMAINING_RISKS.md` records that M1 is supporting rather than the
    main speedup, M2 is dominant, M3 is concentrated, and low-priority
    PATTERN/SEEN state saturates in long runs. Legacy 128-B, HLQ, timeout,
    row-reorder, fill-forwarding, and provenance-bit paths are excluded from
    the formal configuration rather than credited to Complete.

12. **Paper Design/Evaluation/Results -- complete.**
    Design and Evaluation match frozen V5 and explicitly state that no new
    cache is added. Results contain the final overall and work-reduction
    figures, M3 geomean, coupling, sensitivity, limitations, and controlled
    simulator-runtime statement. The final letter-size PDF compiles to ten
    pages; rendered Results pages were inspected without overlap or clipping.

13. **Remaining risks and upper-bound conclusions -- complete.**
    `CUPATH_REMAINING_RISKS.md` and `CUPATH_VALIDATION.md` preserve
    negative evidence, cost assumptions, long-run Filter saturation, and
    legacy diagnostic bounds and the completed 70/70 execution audit.

## Retention-criteria audit

| Criterion | Current authoritative evidence | Decision |
|---|---|---|
| Stable 14-workload gain and broad benefit | All Baseline/Complete pairs give 1.5339x geometric mean and 14/14 positive results; all 70 cells use the frozen binary and fixed flag matrix. | Pass |
| Demand latency | Sample-weighted local issue latency is 38.12 ns in Baseline and 20.34 ns across Complete ordinary/fast issues; M1-only MSHR-full cycles rise 0.22%. Populations and the correlation-only delay counter are reported separately. | Pass with M1 limitation |
| Physical DRAM work | Complete physical reads fall 39,250,266 to 38,708,937 (1.38%); writes fall 20,832,774 to 20,684,557 (0.71%). | Pass |
| Filter coupling | Cuckoo-coupled prediction is 1.0188x versus 1.0059x ungated, removes 34.7% of issued candidates, and retains 96.7% of useful candidates. | Pass |
| Remote transformation | Complete records 2.52M existing-batch piggybacks, 4.24M exact demand merges, 48.2% fewer wire packets, and 27.23M requester-L2 hits. An integrated test proves that a later demand for a piggybacked line creates neither a second request nor packet; the long-run usefulness/piggyback intersection is not reconstructed. | Pass with instrumentation limit |
| Cache pollution | Remote-clean fills displace zero ordinary local-clean and zero two-touch local lines. The observed unused-retirement lower bound is 14.1%, and the conservative largest workload peak is 37.5% of wafer L2 lines. | Protection passes; footprint reported |
| Correctness and liveness | All 70 formal cells return zero and retire every admitted WG; focused lifecycle/fail-open/bounded-state tests pass. | Pass |
| Simulator overhead | The same-binary, single-worker 7-by-5 screen completes with 35 zero returns; Complete's normalized host cost is 1.1897x Baseline. | Pass; simulator cost only |

## Remaining completion gates

- [x] KMeans/M3 writes a zero-return metrics file and admits/retires the same
  73,728 workgroups as the other four configurations under the common 76,800
  cap.
- [x] Regenerate all formal tables and both paper figures from 70 cells.
- [x] Run `analyze_cupath_formal.py` successfully and inspect its causal audit.
- [x] Complete and audit the controlled single-worker simulator-runtime screen.
- [x] Replace provisional paper Results with final M3 and runtime values.
- [x] Compile and inspect the final PDF.
- [x] Refresh `RESULTS_MANIFEST.csv` (192 inventoried result directories).
- [x] Rerun relevant Go tests, all Python tests, builds, formatting, frozen
  hash/config/no-128/no-benchmark-specific audits, and `git diff --check`.
