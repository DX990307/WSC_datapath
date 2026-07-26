package runner

import (
	"fmt"
	"sort"

	"github.com/sarchlab/akita/v3/mem/cache/writeback"
	"github.com/sarchlab/akita/v3/mem/dram"
	memtrace "github.com/sarchlab/akita/v3/mem/trace"
	"github.com/sarchlab/mgpusim/v3/driver"
	"github.com/sarchlab/mgpusim/v3/timing/cu"
)

func (r *Runner) reportStats() {
	if r.observationDRAM != nil {
		if err := r.observationDRAM.Close(); err != nil {
			panic(err)
		}
	}
	r.reportExecutionTime()
	r.metricsCollector.Collect("System", "config_l1v_mshr_entries",
		float64(*l1vMSHREntriesFlag))
	r.metricsCollector.Collect("System", "config_l1v_max_concurrent_transactions",
		float64(*l1vMaxConcurrentTransFlag))
	r.metricsCollector.Collect("System", "config_l2_slices_per_gpm",
		float64(*numMemBankFlag))
	r.reportDriverAllocationStats()
	r.reportInstCount()
	r.reportWGCount()
	r.reportCPIStack()
	r.reportCacheLatency()
	r.reportRDMALatency()
	r.reportGMMULatency()
	r.reportMMULatency()
	r.reportCacheHitRate()
	r.reportTLBHitRate()
	r.reportL2TLBHitRate()
	r.reportTLBLatency()
	r.reportL2TLBLatency()
	r.reportRDMATransactionCount()
	r.reportGMMUTransactionCount()
	r.reportMMUTransactionCount()
	r.reportDRAMTransactionCount()
	r.reportIOMMUTLBStats()
	r.reportMMUCoalescingStats()
	r.reportLocalMemoryPathStats()
	r.reportResidentFilterStats()
	r.reportLocalFilterPrefetchStats()
	r.reportGranularityAdaptationStats()
	r.reportAdaptivePairStats()
	r.reportFillForwardingStats()
	r.reportDRAMRowContinuationStats()
	r.reportRemoteDataPathStats()
	if err := memtrace.DumpMemoryPathTrace(); err != nil {
		panic(err)
	}
	if err := memtrace.DumpObservationTrace(); err != nil {
		panic(err)
	}
	if err := memtrace.DumpObservationRemoteTrace(); err != nil {
		panic(err)
	}
	if err := memtrace.DumpRemoteOriginTrace(); err != nil {
		panic(err)
	}
	if err := memtrace.DumpL2SourceStats(); err != nil {
		panic(err)
	}
	// r.reportGMMUCounts()
	// r.reportL2TLBCounts()
	r.dumpMetrics()
}

func (r *Runner) reportAdaptivePairStats() {
	if !r.Timing {
		return
	}
	for _, gpu := range r.platform.GPUs {
		for _, component := range gpu.L2Caches {
			l2, ok := component.(*writeback.Cache)
			if !ok {
				continue
			}
			stats := l2.GetAdaptivePairStats()
			where := l2.Name()
			r.metricsCollector.Collect(
				where, "adaptive_pair_enabled", boolMetric(stats.Enabled))
			values := map[string]uint64{
				"region_lines":                   stats.RegionLines,
				"miss_lines_seen":                stats.MissLinesSeen,
				"observations":                   stats.Observations,
				"useful":                         stats.Useful,
				"predictions":                    stats.Predictions,
				"inflight_hits":                  stats.InflightHits,
				"buffer_hits":                    stats.BufferHits,
				"prefetch_unused":                stats.PrefetchUnused,
				"prefetch_unused_evictions":      stats.PrefetchUnusedEvictions,
				"prefetch_unused_invalidates":    stats.PrefetchUnusedInvalidates,
				"prefetch_unused_reset_retires":  stats.PrefetchUnusedResetRetires,
				"current_prefetch_only_lines":    stats.CurrentPrefetchOnlyLines,
				"peak_prefetch_only_lines":       stats.PeakPrefetchOnlyLines,
				"unused":                         stats.Unused,
				"confidence":                     stats.Confidence,
				"wide_128b_reads":                stats.Wide128BReads,
				"expanded_region_reads":          stats.ExpandedRegionReads,
				"prefetched_region_lines":        stats.PrefetchedRegionLines,
				"filter_candidates":              stats.FilterCandidates,
				"filter_lookups":                 stats.FilterLookups,
				"filter_busy_fallbacks":          stats.FilterBusyFallbacks,
				"filter_not_ready_fallbacks":     stats.FilterNotReadyFallbacks,
				"filter_unreliable_fallbacks":    stats.FilterUnreliableFallbacks,
				"resident_filter_positives":      stats.ResidentFilterPositives,
				"resident_exact_suppressions":    stats.ResidentExactSuppressions,
				"resident_false_positives":       stats.ResidentFalsePositives,
				"pending_filter_positives":       stats.PendingFilterPositives,
				"pending_exact_suppressions":     stats.PendingExactSuppressions,
				"pending_false_positives":        stats.PendingFalsePositives,
				"pending_filter_insert_failures": stats.PendingFilterInsertFailure,
			}
			for name, value := range values {
				r.metricsCollector.Collect(
					where, "adaptive_pair_"+name, float64(value))
			}
		}
	}
}

func (r *Runner) reportGranularityAdaptationStats() {
	if !r.Timing {
		return
	}
	for _, gpu := range r.platform.GPUs {
		for _, component := range gpu.L2Caches {
			l2, ok := component.(*writeback.Cache)
			if !ok {
				continue
			}
			stats := l2.GetGranularityAdaptationStats()
			where := l2.Name()
			r.metricsCollector.Collect(where,
				"granularity_adaptation_enabled", boolMetric(stats.Enabled))
			r.metricsCollector.Collect(where,
				"granularity_without_filter", boolMetric(stats.WithoutFilter))
			r.metricsCollector.Collect(where,
				"granularity_always_expand", boolMetric(stats.AlwaysExpand))
			r.metricsCollector.Collect(where,
				"granularity_predictor_only", boolMetric(stats.PredictorOnly))
			values := map[string]uint64{
				"real_read_demands":               stats.RealReadDemands,
				"patterns_established":            stats.PatternsEstablished,
				"pattern_insert_drops":            stats.PatternInsertDrops,
				"pattern_filter_positives":        stats.PatternFilterPositives,
				"pattern_negative_drops":          stats.PatternNegativeDrops,
				"predictor_throttled_drops":       stats.PredictorThrottledDrops,
				"predicted_candidates":            stats.PredictedCandidates,
				"sibling_candidates":              stats.SiblingCandidates,
				"predictor_only_candidates":       stats.PredictorOnlyCandidates,
				"candidate_not_sibling_drops":     stats.CandidateNotSiblingDrops,
				"page_boundary_drops":             stats.PageBoundaryDrops,
				"wrong_slice_drops":               stats.WrongSliceDrops,
				"wrong_controller_drops":          stats.WrongControllerDrops,
				"filter_busy_drops":               stats.FilterBusyDrops,
				"filter_not_ready_drops":          stats.FilterNotReadyDrops,
				"filter_unreliable_drops":         stats.FilterUnreliableDrops,
				"resident_filter_positives":       stats.ResidentFilterPositives,
				"pending_filter_positives":        stats.PendingFilterPositives,
				"resident_negative_lookup_skips":  stats.ResidentNegativeLookupSkips,
				"pending_negative_lookup_skips":   stats.PendingNegativeLookupSkips,
				"resident_exact_lookups":          stats.ResidentExactLookups,
				"pending_exact_lookups":           stats.PendingExactLookups,
				"resident_exact_suppressions":     stats.ResidentExactSuppressions,
				"pending_exact_suppressions":      stats.PendingExactSuppressions,
				"resident_false_positives":        stats.ResidentFalsePositives,
				"pending_false_positives":         stats.PendingFalsePositives,
				"mshr_pressure_drops":             stats.MSHRPressureDrops,
				"inflight_capacity_drops":         stats.InflightCapacityDrops,
				"dram_queue_pressure_drops":       stats.DRAMQueuePressureDrops,
				"victim_unavailable_drops":        stats.VictimUnavailableDrops,
				"remote_victim_protection_drops":  stats.RemoteVictimProtectionDrops,
				"clean_victim_displacements":      stats.CleanVictimDisplacements,
				"pending_insert_drops":            stats.PendingInsertDrops,
				"demand_pending_insert_failures":  stats.DemandPendingInsertFailures,
				"expansion_attempts":              stats.ExpansionAttempts,
				"accepted_aggregates":             stats.AcceptedExpansions,
				"demand_pair_ready_opportunities": stats.DemandPairReadyOpportunities,
				"demand_pair_filter_probes":       stats.DemandPairFilterProbes,
				"demand_pair_filter_positives":    stats.DemandPairFilterPositives,
				"demand_pair_filter_negatives":    stats.DemandPairFilterNegatives,
				"demand_pair_aggregates":          stats.DemandPairAggregates,
				"demand_pair_resource_drops":      stats.DemandPairResourceDrops,
				"frontend_single_64b_descriptors": stats.FrontendSingle64Descriptors,
				"frontend_paired_read_aggregates": stats.FrontendPairedReadAggregates,
				"frontend_read_bytes":             stats.FrontendReadBytes,
				"sibling_fills":                   stats.SiblingFills,
				"sibling_inflight_merges":         stats.SiblingInflightMerges,
				"sibling_l2_hits":                 stats.SiblingL2Hits,
				"useful_sibling_lines":            stats.UsefulSiblingLines,
				"timely_sibling_lines":            stats.TimelySiblingLines,
				"late_sibling_lines":              stats.LateSiblingLines,
				"unused_sibling_lines":            stats.UnusedSiblingLines,
				"unused_sibling_evictions":        stats.UnusedSiblingEvictions,
				"unused_sibling_reset_retires":    stats.UnusedSiblingResetRetires,
				"current_sibling_only_lines":      stats.CurrentSiblingOnlyLines,
				"peak_sibling_only_lines":         stats.PeakSiblingOnlyLines,
				"useful_sibling_bytes":            stats.UsefulSiblingBytes,
				"wasted_sibling_bytes":            stats.WastedSiblingBytes,
			}
			for name, value := range values {
				r.metricsCollector.Collect(
					where, "granularity_"+name, float64(value))
			}
			if predictor, report :=
				l2.GetGranularityAdaptationPredictorStats(); report {
				predictorValues := map[string]uint64{
					"capacity":                  predictor.Capacity,
					"real_demands":              predictor.RealDemands,
					"candidates":                predictor.CandidatesGenerated,
					"patterns":                  predictor.PatternsEstablished,
					"pattern_replacements":      predictor.PatternReplacements,
					"pattern_insert_fails":      predictor.PatternInsertFails,
					"evidence_one":              predictor.EvidenceOne,
					"evidence_two":              predictor.EvidenceTwo,
					"useful_feedback":           predictor.UsefulFeedback,
					"unused_feedback":           predictor.UnusedFeedback,
					"timely_feedback":           predictor.TimelyFeedback,
					"late_feedback":             predictor.LateFeedback,
					"stale_feedback_ignored":    predictor.StaleFeedbackIgnored,
					"candidate_lookahead_total": predictor.CandidateLookaheadTotal,
					"candidate_lookahead_max":   predictor.CandidateLookaheadMax,
					"stride_one":                predictor.CandidateStrideOne,
					"stride_small":              predictor.CandidateStrideSmall,
					"stride_medium":             predictor.CandidateStrideMedium,
					"stride_large":              predictor.CandidateStrideLarge,
					"stride_negative":           predictor.CandidateStrideNegative,
				}
				for name, value := range predictorValues {
					r.metricsCollector.Collect(where,
						"granularity_predictor_"+name, float64(value))
				}
			}
		}
	}
}

func (r *Runner) reportLocalFilterPrefetchStats() {
	if !r.Timing {
		return
	}
	for _, gpu := range r.platform.GPUs {
		for _, component := range gpu.L2Caches {
			l2, ok := component.(*writeback.Cache)
			if !ok {
				continue
			}
			stats := l2.GetLocalFilterPrefetchStats()
			where := l2.Name()
			values := map[string]uint64{
				"real_demands":                stats.RealReadDemands,
				"candidates":                  stats.Candidates,
				"pattern_installs":            stats.PatternInstalls,
				"pattern_install_drops":       stats.PatternInstallDrops,
				"candidate_busy_drops":        stats.CandidateBusyDrops,
				"pattern_negative_drops":      stats.PatternNegativeDrops,
				"resident_positive_drops":     stats.ResidentPositiveDrops,
				"pending_positive_drops":      stats.PendingPositiveDrops,
				"wrong_slice_drops":           stats.WrongSliceDrops,
				"demand_priority_drops":       stats.DemandPriorityDrops,
				"controller_busy_drops":       stats.ControllerBusyDrops,
				"output_busy_drops":           stats.OutputBusyDrops,
				"mshr_drops":                  stats.MSHRDrops,
				"victim_drops":                stats.VictimDrops,
				"pending_insert_drops":        stats.PendingInsertDrops,
				"issued":                      stats.Issued,
				"outstanding":                 stats.Outstanding,
				"redundant_races":             stats.RedundantRaces,
				"useful":                      stats.Useful,
				"timely":                      stats.Timely,
				"late":                        stats.Late,
				"late_after_dram_issue":       stats.LateAfterDRAMIssue,
				"demand_merges":               stats.DemandMerges,
				"demand_won_races":            stats.DemandWonRaces,
				"unused":                      stats.Unused,
				"unused_evictions":            stats.UnusedEvictions,
				"unused_reset_retirements":    stats.UnusedResetRetirements,
				"current_prefetch_only_lines": stats.CurrentPrefetchOnlyLines,
				"peak_prefetch_only_lines":    stats.PeakPrefetchOnlyLines,
				"fills":                       stats.Fills,
				"additional_dram_reads":       stats.AdditionalDRAMReads,
				"demand_delay_events":         stats.DemandDelayEvents,
				"mshr_headroom_drops":         stats.MSHRHeadroomDrops,
				"demand_path_busy_drops":      stats.DemandPathBusyDrops,
				"timely_proof_busy_issues":    stats.TimelyProofBusyIssues,
				"demand_mshr_covered_drops":   stats.DemandMSHRCoveredDrops,
				"prefetch_mshr_covered_drops": stats.PrefetchMSHRCoveredDrops,
				"training_pending_drops":      stats.TrainingPendingDrops,
				"outstanding_capacity_drops":  stats.OutstandingCapacityDrops,
				"predictor_only_candidates":   stats.PredictorOnlyCandidates,
				"ungated_candidates":          stats.UngatedCandidates,
			}
			r.metricsCollector.Collect(where,
				"filter_prefetch_enabled", boolMetric(stats.Enabled))
			for name, value := range values {
				r.metricsCollector.Collect(where,
					"filter_prefetch_"+name, float64(value))
			}
			if predictor, report := l2.GetLocalFilterPrefetchPredictorStats(); report {
				predictorValues := map[string]uint64{
					"predictor_capacity":                   predictor.Capacity,
					"predictor_real_demands":               predictor.RealDemands,
					"predictor_candidates":                 predictor.CandidatesGenerated,
					"predictor_patterns":                   predictor.PatternsEstablished,
					"predictor_evidence_one":               predictor.EvidenceOne,
					"predictor_evidence_two":               predictor.EvidenceTwo,
					"predictor_timely_feedback":            predictor.TimelyFeedback,
					"predictor_late_feedback":              predictor.LateFeedback,
					"predictor_late_distance_increases":    predictor.LateDistanceIncreases,
					"predictor_demand_covered_feedback":    predictor.DemandCoveredFeedback,
					"predictor_covered_distance_increases": predictor.CoveredDistanceIncreases,
					"predictor_horizon_distance_increases": predictor.HorizonDistanceIncreases,
					"predictor_page_frontier_clamps":       predictor.PageFrontierClamps,
					"predictor_stale_distance_feedback":    predictor.StaleDistanceFeedback,
					"predictor_lookahead_total":            predictor.CandidateLookaheadTotal,
					"predictor_lookahead_max":              predictor.CandidateLookaheadMax,
					"stride_one":                           predictor.CandidateStrideOne,
					"stride_small":                         predictor.CandidateStrideSmall,
					"stride_medium":                        predictor.CandidateStrideMedium,
					"stride_large":                         predictor.CandidateStrideLarge,
					"stride_negative":                      predictor.CandidateStrideNegative,
				}
				for name, value := range predictorValues {
					r.metricsCollector.Collect(where,
						"filter_prefetch_"+name, float64(value))
				}
			}
		}
	}
}

func (r *Runner) reportResidentFilterStats() {
	if !r.Timing {
		return
	}
	for _, gpu := range r.platform.GPUs {
		for _, component := range gpu.L2Caches {
			l2, ok := component.(*writeback.Cache)
			if !ok {
				continue
			}
			stats := l2.GetResidentFilterStats()
			where := l2.Name()
			r.metricsCollector.Collect(where, "l2_resident_filter_enabled", boolMetric(stats.Enabled))
			r.metricsCollector.Collect(where, "l2_resident_filter_reliable", boolMetric(stats.Reliable))
			r.metricsCollector.Collect(where, "l2_resident_filter_authoritative_audit_enabled", boolMetric(stats.AuthoritativeAuditEnabled))
			r.metricsCollector.Collect(where, "l2_resident_filter_queries", float64(stats.Queries))
			r.metricsCollector.Collect(where, "l2_resident_filter_positives", float64(stats.Positives))
			r.metricsCollector.Collect(where, "l2_resident_filter_negatives", float64(stats.Negatives))
			r.metricsCollector.Collect(where, "l2_resident_filter_read_filter_eligible", float64(stats.ReadFilterEligible))
			r.metricsCollector.Collect(where, "l2_resident_filter_read_issued_bypasses", float64(stats.ReadIssuedBypasses))
			r.metricsCollector.Collect(where, "l2_resident_filter_read_exact_tag_lookups", float64(stats.ReadExactTagLookups))
			r.metricsCollector.Collect(where, "l2_resident_filter_read_authoritative_checks", float64(stats.ReadAuthoritativeChecks))
			r.metricsCollector.Collect(where, "l2_resident_filter_read_verified_safe_bypasses", float64(stats.ReadVerifiedSafeBypasses))
			r.metricsCollector.Collect(where, "l2_resident_filter_read_authoritative_false_negatives", float64(stats.ReadAuthoritativeFalseNegatives))
			r.metricsCollector.Collect(where, "l2_resident_filter_read_authoritative_mshr_hits", float64(stats.ReadAuthoritativeMSHRHits))
			r.metricsCollector.Collect(where, "l2_resident_filter_read_bypasses", float64(stats.ReadNegativeBypasses))
			r.metricsCollector.Collect(where, "l2_resident_filter_read_positive_fast_paths", float64(stats.ReadPositiveFastPaths))
			r.metricsCollector.Collect(where, "l2_resident_filter_read_busy_fallbacks", float64(stats.ReadBusyFallbacks))
			r.metricsCollector.Collect(where, "l2_resident_filter_read_negative_mshr_merges", float64(stats.ReadNegativeMSHRMerges))
			r.metricsCollector.Collect(where, "l2_resident_filter_read_parallel_mshr_merges", float64(stats.ReadParallelMSHRMerges))
			r.metricsCollector.Collect(where, "l2_resident_filter_primed_lookups", float64(stats.PrimedLookups))
			r.metricsCollector.Collect(where, "l2_resident_filter_write_bypasses", float64(stats.WriteNegativeBypasses))
			r.metricsCollector.Collect(where, "l2_resident_filter_write_full_line_bypasses", float64(stats.WriteFullLineBypasses))
			r.metricsCollector.Collect(where, "l2_resident_filter_write_partial_bypasses", float64(stats.WritePartialBypasses))
			r.metricsCollector.Collect(where, "l2_resident_filter_false_positives", float64(stats.FalsePositives))
			r.metricsCollector.Collect(where, "l2_resident_filter_insert_failures", float64(stats.InsertFailures))
			r.metricsCollector.Collect(where, "l2_mshr_full_stall_cycles", float64(stats.MSHRFullStalls))
		}
	}
}

func boolMetric(value bool) float64 {
	if value {
		return 1
	}
	return 0
}

func (r *Runner) reportFillForwardingStats() {
	if !r.Timing {
		return
	}
	for _, gpu := range r.platform.GPUs {
		for _, component := range gpu.L2Caches {
			l2, ok := component.(*writeback.Cache)
			if !ok {
				continue
			}
			stats := l2.GetFillForwardingStats()
			where := l2.Name()
			r.metricsCollector.Collect(where,
				"l2_fill_forwarding_enabled", boolMetric(stats.Enabled))
			r.metricsCollector.Collect(where,
				"l2_fill_forwarding_eligible_read_entries",
				float64(stats.EligibleReadEntries))
			r.metricsCollector.Collect(where,
				"l2_fill_forwarding_forwarded_read_entries",
				float64(stats.ForwardedReadEntries))
			r.metricsCollector.Collect(where,
				"l2_fill_forwarding_forwarded_reads",
				float64(stats.ForwardedReads))
			r.metricsCollector.Collect(where,
				"l2_fill_forwarding_buffer_fallbacks",
				float64(stats.BufferFallbacks))
		}
	}
}

func (r *Runner) reportDriverAllocationStats() {
	stats := r.platform.Driver.AllocationStatsSnapshot()
	where := r.platform.Driver.Name()
	r.metricsCollector.Collect(where, "allocation_page_size", float64(stats.PageSize))

	groups := []struct {
		name  string
		stats driver.AllocationStats
	}{
		{"overall", stats.Overall},
		{"workload", stats.Workload},
		{"runtime", stats.Runtime},
	}
	for _, group := range groups {
		prefix := "allocation_" + group.name + "_"
		r.metricsCollector.Collect(where, prefix+"calls", float64(group.stats.AllocationCalls))
		r.metricsCollector.Collect(where, prefix+"requested_bytes", float64(group.stats.RequestedBytes))
		r.metricsCollector.Collect(where, prefix+"allocated_pages", float64(group.stats.AllocatedPages))
		r.metricsCollector.Collect(where, prefix+"rounded_bytes", float64(group.stats.RoundedBytes))
		r.metricsCollector.Collect(where, prefix+"normal_calls", float64(group.stats.NormalCalls))
		r.metricsCollector.Collect(where, prefix+"unified_calls", float64(group.stats.UnifiedCalls))
	}
}

func (r *Runner) reportDRAMRowContinuationStats() {
	if !r.Timing {
		return
	}
	for _, gpu := range r.platform.GPUs {
		for _, component := range gpu.MemControllers {
			controller, ok := component.(*dram.MemController)
			if !ok {
				continue
			}
			stats := controller.GetRowContinuationStats()
			physical := controller.GetPhysicalAccessStats()
			where := controller.Name()
			r.metricsCollector.Collect(where,
				"dram_physical_access_bytes",
				float64(physical.AccessBytes))
			r.metricsCollector.Collect(where,
				"dram_physical_read_accesses",
				float64(physical.ReadAccesses))
			r.metricsCollector.Collect(where,
				"dram_physical_write_accesses",
				float64(physical.WriteAccesses))
			r.metricsCollector.Collect(where,
				"dram_frontend_read_requests",
				float64(physical.FrontEndReadRequests))
			r.metricsCollector.Collect(where,
				"dram_frontend_write_requests",
				float64(physical.FrontEndWriteRequests))
			r.metricsCollector.Collect(where,
				"dram_frontend_read_bytes",
				float64(physical.FrontEndReadBytes))
			r.metricsCollector.Collect(where,
				"dram_frontend_write_bytes",
				float64(physical.FrontEndWriteBytes))
			r.metricsCollector.Collect(where,
				"dram_paired_read_descriptors",
				float64(physical.PairedReadDescriptors))
			r.metricsCollector.Collect(where,
				"dram_paired_read_members",
				float64(physical.PairedReadMembers))
			r.metricsCollector.Collect(where,
				"dram_paired_read_demand_members",
				float64(physical.PairedDemandMembers))
			r.metricsCollector.Collect(where,
				"dram_paired_read_sibling_members",
				float64(physical.PairedSiblingMembers))
			enabled := 0.0
			if stats.Enabled {
				enabled = 1
			}
			r.metricsCollector.Collect(where,
				"dram_row_continuation_enabled", enabled)
			r.metricsCollector.Collect(where,
				"dram_row_commands_issued", float64(stats.CommandsIssued))
			r.metricsCollector.Collect(where,
				"dram_row_column_commands", float64(stats.ColumnCommands))
			r.metricsCollector.Collect(where,
				"dram_row_reuse_hits", float64(stats.RowReuseHits))
			r.metricsCollector.Collect(where,
				"dram_row_auto_precharge_stops",
				float64(stats.AutoPrechargeStops))
			r.metricsCollector.Collect(where,
				"dram_aggregate_continuation_enabled",
				boolMetric(stats.AggregateEnabled))
			r.metricsCollector.Collect(where,
				"dram_aggregate_auto_precharge_stops",
				float64(stats.AggregateAutoPrechargeStops))
			r.metricsCollector.Collect(where,
				"dram_aggregate_immediate_continuations",
				float64(stats.AggregateImmediateContinues))
			r.metricsCollector.Collect(where,
				"dram_row_activate_commands", float64(stats.ActivateCommands))
			r.metricsCollector.Collect(where,
				"dram_row_precharge_commands", float64(stats.PrechargeCommands))
			r.metricsCollector.Collect(where,
				"dram_row_max_queue_age_cycles",
				float64(stats.MaxQueueAgeCycles))
		}
	}
}

func (r *Runner) reportLocalMemoryPathStats() {
	if !r.Timing {
		return
	}
	for _, gpu := range r.platform.GPUs {
		for _, component := range gpu.L2Caches {
			l2, ok := component.(*writeback.Cache)
			if !ok {
				continue
			}
			stats := l2.GetLocalMemoryPathStats()
			where := l2.Name()
			r.metricsCollector.Collect(where, "l2_to_dram_64b_requests", float64(stats.DRAMReadRequests))
			r.metricsCollector.Collect(where, "l2_miss_to_dram_issue_samples", float64(stats.MissToDRAMIssueSamples))
			r.metricsCollector.Collect(where, "l2_miss_to_dram_issue_total_ns", stats.MissToDRAMIssueTotalNS)
			r.metricsCollector.Collect(where, "l2_miss_to_dram_issue_max_ns", stats.MissToDRAMIssueMaxNS)
			r.metricsCollector.Collect(where, "l2_fast_miss_to_dram_issue_samples", float64(stats.FastMissIssueSamples))
			r.metricsCollector.Collect(where, "l2_fast_miss_to_dram_issue_total_ns", stats.FastMissIssueTotalNS)
			r.metricsCollector.Collect(where, "l2_fast_miss_to_dram_issue_max_ns", stats.FastMissIssueMaxNS)
			r.metricsCollector.Collect(where, "l2_demand_read_latency_samples", float64(stats.DemandReadLatencySamples))
			r.metricsCollector.Collect(where, "l2_demand_read_latency_total_ns", stats.DemandReadLatencyTotalNS)
			r.metricsCollector.Collect(where, "l2_demand_read_latency_max_ns", stats.DemandReadLatencyMaxNS)
		}
	}
}

func (r *Runner) reportRemoteDataPathStats() {
	if !r.Timing {
		return
	}
	for _, gpu := range r.platform.GPUs {
		if gpu.RDMAEngine == nil {
			continue
		}
		stats := gpu.RDMAEngine.GetRemoteDataPathStats()
		pipelineStats := gpu.RDMAEngine.GetPipelineStats()
		where := gpu.RDMAEngine.Name()
		r.metricsCollector.Collect(where, "rdma_pipeline_width",
			float64(pipelineStats.Width))
		r.metricsCollector.Collect(where, "rdma_pipeline_latency_cycles",
			float64(pipelineStats.LatencyCycles))
		r.metricsCollector.Collect(where, "rdma_max_outstanding",
			float64(pipelineStats.MaxOutstanding))
		r.metricsCollector.Collect(where, "rdma_peak_outstanding",
			float64(pipelineStats.PeakOutstanding))
		r.metricsCollector.Collect(where, "rdma_pipeline_wait_cycles",
			float64(pipelineStats.PipelineWaitCycles))
		r.metricsCollector.Collect(where, "rdma_outstanding_full_stalls",
			float64(pipelineStats.OutstandingFullStalls))
		r.metricsCollector.Collect(where, "rdma_requester_peak_outstanding",
			float64(pipelineStats.RequesterPeakOutstanding))
		r.metricsCollector.Collect(where, "rdma_owner_peak_outstanding",
			float64(pipelineStats.OwnerPeakOutstanding))
		r.metricsCollector.Collect(where, "rdma_requester_outstanding_full_stalls",
			float64(pipelineStats.RequesterOutstandingFullStalls))
		r.metricsCollector.Collect(where, "rdma_owner_outstanding_full_stalls",
			float64(pipelineStats.OwnerOutstandingFullStalls))
		enabled := 0.0
		if stats.Enabled {
			enabled = 1
		}
		r.metricsCollector.Collect(where, "remote_data_path_enabled", enabled)
		r.metricsCollector.Collect(where, "rdma_observed_remote_reads", float64(stats.ObservedRemoteReads))
		r.metricsCollector.Collect(where, "rdma_observed_remote_writes", float64(stats.ObservedRemoteWrites))
		r.reportTypedFilterStats(gpu)
		if !stats.Enabled {
			continue
		}
		batchingEnabled := 0.0
		if stats.BatchingEnabled {
			batchingEnabled = 1
		}
		dedupEnabled := 0.0
		if stats.DedupEnabled {
			dedupEnabled = 1
		}
		requesterL2Enabled := 0.0
		if stats.RequesterL2Enabled {
			requesterL2Enabled = 1
		}
		filterPrefetchEnabled := 0.0
		if stats.FilterPrefetchEnabled {
			filterPrefetchEnabled = 1
		}
		r.metricsCollector.Collect(where, "remote_dedup_enabled", dedupEnabled)
		r.metricsCollector.Collect(where, "remote_batching_enabled", batchingEnabled)
		r.metricsCollector.Collect(where, "remote_requester_l2_enabled", requesterL2Enabled)
		r.metricsCollector.Collect(where, "remote_filter_prefetch_enabled", filterPrefetchEnabled)
		r.metricsCollector.Collect(where, "remote_config_batch_lines", float64(stats.MaxBatchLines))
		r.metricsCollector.Collect(where, "remote_config_max_batches", float64(stats.MaxBatches))
		r.metricsCollector.Collect(where, "remote_config_line_entries", float64(stats.LineEntryCapacity))
		r.metricsCollector.Collect(where, "remote_peak_line_entries", float64(stats.PeakLineEntries))
		r.metricsCollector.Collect(where, "remote_line_entry_full_stalls", float64(stats.LineEntryFullStalls))
		r.metricsCollector.Collect(where, "remote_config_waiter_entries", float64(stats.WaiterEntryCapacity))
		r.metricsCollector.Collect(where, "remote_peak_waiter_entries", float64(stats.PeakWaiterEntries))
		r.metricsCollector.Collect(where, "remote_waiter_entry_full_stalls", float64(stats.WaiterEntryFullStalls))
		r.metricsCollector.Collect(where, "remote_config_owner_child_lines", float64(stats.OwnerChildLineCapacity))
		r.metricsCollector.Collect(where, "remote_owner_peak_child_lines", float64(stats.OwnerPeakChildLines))
		r.metricsCollector.Collect(where, "remote_owner_child_line_full_stalls", float64(stats.OwnerChildLineFullStalls))
		r.metricsCollector.Collect(where, "remote_logical_reads", float64(stats.LogicalRemoteReads))
		r.metricsCollector.Collect(where, "remote_wire_lines", float64(stats.WireLines))
		r.metricsCollector.Collect(where, "remote_demand_wire_lines", float64(stats.DemandWireLines))
		r.metricsCollector.Collect(where, "remote_prefetch_wire_lines", float64(stats.PrefetchWireLines))
		prefetchMetrics := map[string]uint64{
			"real_demands":            stats.PrefetchRealDemands,
			"candidates":              stats.PrefetchCandidates,
			"pattern_installs":        stats.PrefetchPatternInstalls,
			"pattern_install_drops":   stats.PrefetchPatternInstallDrops,
			"filter_drops":            stats.PrefetchFilterDrops,
			"same_group_drops":        stats.PrefetchSameGroupDrops,
			"capacity_drops":          stats.PrefetchCapacityDrops,
			"no_existing_batch_drops": stats.PrefetchNoExistingBatchDrops,
			"batch_full_drops":        stats.PrefetchBatchFullDrops,
			"piggyback_lines":         stats.PrefetchPiggybackLines,
			"useful":                  stats.PrefetchUseful,
			"unused":                  stats.PrefetchUnused,
			"standalone_prevented":    stats.PrefetchStandalonePrevented,
			"avoided_remote_requests": stats.PrefetchUseful,
			"added_response_bytes":    stats.PrefetchWireLines * 64,
			"additional_owner_reads":  stats.PrefetchWireLines,
		}
		for name, value := range prefetchMetrics {
			r.metricsCollector.Collect(where,
				"remote_prefetch_"+name, float64(value))
		}
		predictor := stats.PrefetchPredictor
		predictorMetrics := map[string]uint64{
			"predictor_evidence_one":            predictor.EvidenceOne,
			"predictor_evidence_two":            predictor.EvidenceTwo,
			"predictor_timely_feedback":         predictor.TimelyFeedback,
			"predictor_late_feedback":           predictor.LateFeedback,
			"predictor_late_distance_increases": predictor.LateDistanceIncreases,
			"predictor_stale_distance_feedback": predictor.StaleDistanceFeedback,
			"predictor_lookahead_total":         predictor.CandidateLookaheadTotal,
			"predictor_lookahead_max":           predictor.CandidateLookaheadMax,
			"stride_one":                        predictor.CandidateStrideOne,
			"stride_small":                      predictor.CandidateStrideSmall,
			"stride_medium":                     predictor.CandidateStrideMedium,
			"stride_large":                      predictor.CandidateStrideLarge,
			"stride_negative":                   predictor.CandidateStrideNegative,
		}
		for name, value := range predictorMetrics {
			r.metricsCollector.Collect(where,
				"remote_prefetch_"+name, float64(value))
		}
		r.metricsCollector.Collect(where, "remote_duplicate_reads", float64(stats.DuplicateReads))
		r.metricsCollector.Collect(where, "remote_inflight_filter_queries", float64(stats.InflightFilterQueries))
		r.metricsCollector.Collect(where, "remote_inflight_filter_positives", float64(stats.InflightFilterPositives))
		r.metricsCollector.Collect(where, "remote_inflight_filter_negatives", float64(stats.InflightFilterNegatives))
		r.metricsCollector.Collect(where, "remote_inflight_filter_false_positives", float64(stats.InflightFilterFalsePositives))
		r.metricsCollector.Collect(where, "remote_inflight_filter_insert_failures", float64(stats.InflightFilterInsertFailures))
		r.metricsCollector.Collect(where, "remote_authoritative_audit_enabled", boolMetric(stats.AuthoritativeAuditEnabled))
		r.metricsCollector.Collect(where, "remote_pending_authoritative_checks", float64(stats.PendingAuthoritativeChecks))
		r.metricsCollector.Collect(where, "remote_pending_verified_safe_bypasses", float64(stats.PendingVerifiedSafeBypasses))
		r.metricsCollector.Collect(where, "remote_pending_authoritative_false_negatives", float64(stats.PendingAuthoritativeFalseNegatives))
		r.metricsCollector.Collect(where, "remote_exact_table_lookups", float64(stats.ExactTableLookups))
		r.metricsCollector.Collect(where, "remote_exact_table_lookups_avoided", float64(stats.ExactTableLookupsAvoided))
		r.metricsCollector.Collect(where, "remote_pre_send_merges", float64(stats.CollectingMerges))
		r.metricsCollector.Collect(where, "remote_inflight_merges", float64(stats.InflightMerges))
		r.metricsCollector.Collect(where, "remote_ready_merges", float64(stats.ReadyMerges))
		r.metricsCollector.Collect(where, "remote_l2_probe_hits", float64(stats.L2ProbeHits))
		r.metricsCollector.Collect(where, "remote_l2_probe_misses", float64(stats.L2ProbeMisses))
		r.metricsCollector.Collect(where, "remote_requester_l2_filter_negative_decisions", float64(stats.RequesterL2FilterNegativeDecisions))
		r.metricsCollector.Collect(where, "remote_l2_one_touch_probe_bypasses", float64(stats.L2OneTouchProbeBypasses))
		r.metricsCollector.Collect(where, "remote_requester_l2_authoritative_checks", float64(stats.RequesterL2AuthoritativeChecks))
		r.metricsCollector.Collect(where, "remote_requester_l2_verified_safe_bypasses", float64(stats.RequesterL2VerifiedSafeBypasses))
		r.metricsCollector.Collect(where, "remote_requester_l2_authoritative_false_negatives", float64(stats.RequesterL2AuthoritativeFalseNegatives))
		r.metricsCollector.Collect(where, "remote_requester_l2_authoritative_unavailable", float64(stats.RequesterL2AuthoritativeUnavailable))
		r.metricsCollector.Collect(where, "remote_resident_queries", float64(stats.ResidentQueries))
		r.metricsCollector.Collect(where, "remote_resident_positives", float64(stats.ResidentPositives))
		r.metricsCollector.Collect(where, "remote_resident_negatives", float64(stats.ResidentNegatives))
		r.metricsCollector.Collect(where, "remote_seen_queries", float64(stats.SeenQueries))
		r.metricsCollector.Collect(where, "remote_seen_hits", float64(stats.SeenHits))
		r.metricsCollector.Collect(where, "remote_seen_negatives", float64(stats.SeenNegatives))
		r.metricsCollector.Collect(where, "remote_seen_false_positives", float64(stats.SeenFalsePositives))
		r.metricsCollector.Collect(where, "remote_seen_insert_failures", float64(stats.SeenInsertFailures))
		r.metricsCollector.Collect(where, "remote_first_touch_lines", float64(stats.FirstTouchRemoteLines))
		r.metricsCollector.Collect(where, "remote_second_touch_admissions", float64(stats.SecondTouchAdmissions))
		r.metricsCollector.Collect(where, "remote_multiple_demand_admissions", float64(stats.MultipleDemandAdmissions))
		r.metricsCollector.Collect(where, "remote_reuse_write_uncacheable_skips", float64(stats.ReuseWriteUncacheableSkips))
		r.metricsCollector.Collect(where, "remote_l2_logical_responses", float64(stats.L2LogicalResponses))
		r.metricsCollector.Collect(where, "remote_single_packets", float64(stats.SingleReadPackets))
		r.metricsCollector.Collect(where, "remote_bitmap_packets", float64(stats.BitmapPackets))
		r.metricsCollector.Collect(where, "remote_bitmap_lines", float64(stats.BitmapLines))
		r.metricsCollector.Collect(where, "remote_bitmap_response_packets", float64(stats.BitmapResponsePackets))
		r.metricsCollector.Collect(where, "remote_bitmap_response_lines", float64(stats.BitmapResponseLines))
		r.metricsCollector.Collect(where, "remote_early_bitmap_responses", float64(stats.EarlyBitmapResponses))
		for lines := 1; lines < len(stats.BatchSizeHistogram); lines++ {
			packets := stats.BatchSizeHistogram[lines]
			if packets == 0 {
				continue
			}
			r.metricsCollector.Collect(where,
				fmt.Sprintf("remote_batch_size_%02d_packets", lines),
				float64(packets))
		}
		r.metricsCollector.Collect(where, "remote_two_touch_candidates", float64(stats.TwoTouchCandidates))
		r.metricsCollector.Collect(where, "remote_two_touch_fill_attempts", float64(stats.TwoTouchFillAttempts))
		r.metricsCollector.Collect(where, "remote_two_touch_installed_fills", float64(stats.TwoTouchInstalledFills))
		r.metricsCollector.Collect(where, "remote_fanout_responses", float64(stats.FanoutResponses))
		r.metricsCollector.Collect(where, "remote_network_request_bytes", float64(stats.NetworkRequestBytes))
		r.metricsCollector.Collect(where, "remote_network_response_bytes", float64(stats.NetworkResponseBytes))
		r.metricsCollector.Collect(where, "remote_batch_queue_wait_samples", float64(stats.BatchQueueWaitSamples))
		r.metricsCollector.Collect(where, "remote_batch_queue_wait_total_ns", stats.BatchQueueWaitTotalNS)
		r.metricsCollector.Collect(where, "remote_batch_queue_wait_max_ns", stats.BatchQueueWaitMaxNS)
		r.metricsCollector.Collect(where, "remote_pre_network_wait_samples", float64(stats.PreNetworkWaitSamples))
		r.metricsCollector.Collect(where, "remote_pre_network_wait_total_ns", stats.PreNetworkWaitTotalNS)
		r.metricsCollector.Collect(where, "remote_pre_network_wait_max_ns", stats.PreNetworkWaitMaxNS)
		r.metricsCollector.Collect(where, "remote_probe_latency_samples", float64(stats.ProbeLatencySamples))
		r.metricsCollector.Collect(where, "remote_probe_latency_total_ns", stats.ProbeLatencyTotalNS)
		r.metricsCollector.Collect(where, "remote_probe_latency_max_ns", stats.ProbeLatencyMaxNS)
		r.metricsCollector.Collect(where, "remote_logical_read_latency_samples", float64(stats.FanoutResponses))
		r.metricsCollector.Collect(where, "remote_logical_read_latency_total_ns", stats.LogicalReadLatencyTotalNS)
		r.metricsCollector.Collect(where, "remote_logical_read_latency_max_ns", stats.LogicalReadLatencyMaxNS)
		r.metricsCollector.Collect(where, "remote_full_flushes", float64(stats.FullFlushes))
		r.metricsCollector.Collect(where, "remote_work_conserving_flushes", float64(stats.WorkConservingFlushes))
		r.metricsCollector.Collect(where, "remote_capacity_flushes", float64(stats.CapacityFlushes))
		r.metricsCollector.Collect(where, "remote_conflict_flushes", float64(stats.ConflictFlushes))
		r.metricsCollector.Collect(where, "remote_drain_flushes", float64(stats.DrainFlushes))
		r.metricsCollector.Collect(where, "remote_requester_issue_width_stalls", float64(stats.RequesterIssueWidthStalls))
		r.metricsCollector.Collect(where, "remote_response_fanout_width_stalls", float64(stats.ResponseFanoutWidthStalls))
		r.metricsCollector.Collect(where, "remote_owner_issue_width_stalls", float64(stats.OwnerIssueWidthStalls))
		r.metricsCollector.Collect(where, "remote_owner_response_width_stalls", float64(stats.OwnerResponseWidthStalls))

		for _, component := range gpu.L2Caches {
			l2, ok := component.(*writeback.Cache)
			if !ok {
				continue
			}
			l2Stats := l2.GetRemoteReplicaStats()
			l2Where := l2.Name()
			r.metricsCollector.Collect(l2Where, "remote_filter_queries", float64(l2Stats.FilterQueries))
			r.metricsCollector.Collect(l2Where, "remote_filter_positives", float64(l2Stats.FilterPositives))
			r.metricsCollector.Collect(l2Where, "remote_filter_negatives", float64(l2Stats.FilterNegatives))
			r.metricsCollector.Collect(l2Where, "remote_filter_false_positives", float64(l2Stats.FilterFalsePositives))
			r.metricsCollector.Collect(l2Where, "remote_filter_true_positive_unavailable", float64(l2Stats.FilterTruePositiveUnavailable))
			r.metricsCollector.Collect(l2Where, "remote_clean_fills", float64(l2Stats.CleanFills))
			r.metricsCollector.Collect(l2Where, "remote_l2_two_touch_fill_attempts", float64(l2Stats.TwoTouchFillAttempts))
			r.metricsCollector.Collect(l2Where, "remote_installed_fills", float64(l2Stats.InstalledFills))
			r.metricsCollector.Collect(l2Where, "remote_l2_two_touch_installed_fills", float64(l2Stats.TwoTouchInstalledFills))
			r.metricsCollector.Collect(l2Where, "remote_dropped_fills", float64(l2Stats.DroppedFills))
			r.metricsCollector.Collect(l2Where, "remote_l2_two_touch_dropped_fills", float64(l2Stats.TwoTouchDroppedFills))
			r.metricsCollector.Collect(l2Where, "remote_filter_insert_failures", float64(l2Stats.FilterInsertFails))
			r.metricsCollector.Collect(l2Where, "remote_tracked_evictions", float64(l2Stats.TrackedEvictions))
			r.metricsCollector.Collect(l2Where, "remote_unused_two_touch_retirements", float64(l2Stats.UnusedTwoTouchRetirements))
			r.metricsCollector.Collect(l2Where, "remote_replica_probe_hits", float64(l2Stats.ReplicaProbeHits))
			r.metricsCollector.Collect(l2Where, "remote_two_touch_replica_hits", float64(l2Stats.TwoTouchReplicaHits))
			r.metricsCollector.Collect(l2Where, "remote_useful_two_touch_fills", float64(l2Stats.UsefulTwoTouchFills))
			r.metricsCollector.Collect(l2Where, "remote_fill_into_invalid", float64(l2Stats.FillIntoInvalid))
			r.metricsCollector.Collect(l2Where, "remote_fill_replaced_remote", float64(l2Stats.FillReplacedRemote))
			r.metricsCollector.Collect(l2Where, "remote_fill_displaced_local_clean", float64(l2Stats.FillDisplacedLocalClean))
			r.metricsCollector.Collect(l2Where, "remote_two_touch_local_displacements", float64(l2Stats.TwoTouchLocalDisplacements))
			r.metricsCollector.Collect(l2Where, "remote_local_clean_protection_drops", float64(l2Stats.LocalCleanProtectionDrops))
			r.metricsCollector.Collect(l2Where, "remote_current_replicas", float64(l2Stats.CurrentRemoteReplicas))
			r.metricsCollector.Collect(l2Where, "remote_peak_replicas", float64(l2Stats.PeakRemoteReplicas))
			// Paper-facing aliases name the existing requester L2 directly; no
			// separate replica cache or data store is instantiated.
			r.metricsCollector.Collect(l2Where, "remote_requester_l2_hits", float64(l2Stats.ReplicaProbeHits))
			r.metricsCollector.Collect(l2Where, "remote_requester_l2_current_lines", float64(l2Stats.CurrentRemoteReplicas))
			r.metricsCollector.Collect(l2Where, "remote_requester_l2_peak_lines", float64(l2Stats.PeakRemoteReplicas))
			r.metricsCollector.Collect(l2Where, "remote_requester_l2_unused_fills", float64(l2Stats.UnusedTwoTouchRetirements))
			r.metricsCollector.Collect(l2Where, "remote_requester_l2_unused_pattern_retirements", float64(l2Stats.UnusedPatternRetirements))
			r.metricsCollector.Collect(l2Where, "remote_speculative_invalid_only_attempts", float64(l2Stats.SpeculativeInvalidOnlyAttempts))
			r.metricsCollector.Collect(l2Where, "remote_speculative_invalid_only_drops", float64(l2Stats.SpeculativeInvalidOnlyDrops))
		}
	}
}

func (r *Runner) reportTypedFilterStats(gpu *GPU) {
	for _, component := range gpu.L2Caches {
		l2, ok := component.(*writeback.Cache)
		if !ok {
			continue
		}
		where := l2.Name()
		stats := l2.GetTypedFilterStats()
		r.metricsCollector.Collect(where, "typed_filter_mode", float64(stats.Mode))
		r.metricsCollector.Collect(where, "typed_filter_buckets", float64(stats.Buckets))
		r.metricsCollector.Collect(where, "typed_filter_slots", float64(stats.Slots))
		r.metricsCollector.Collect(where, "typed_filter_slots_per_bucket", float64(stats.SlotsPerBucket))
		r.metricsCollector.Collect(where, "typed_filter_fingerprint_bits", float64(stats.FingerprintBits))
		r.metricsCollector.Collect(where, "typed_filter_reference_bits", float64(stats.ReferenceBits))
		r.metricsCollector.Collect(where, "typed_filter_low_priority_limit", float64(stats.LowPriorityLimit))
		r.metricsCollector.Collect(where, "typed_filter_storage_bits", float64(stats.EstimatedStorageBits))
		r.metricsCollector.Collect(where, "typed_filter_occupancy", float64(stats.Occupancy))
		r.metricsCollector.Collect(where, "typed_filter_peak_occupancy", float64(stats.PeakOccupancy))
		r.metricsCollector.Collect(where, "typed_filter_lookup_latency_cycles", float64(stats.LookupLatencyCycles))
		r.metricsCollector.Collect(where, "typed_filter_update_latency_cycles", float64(stats.UpdateLatencyCycles))
		r.metricsCollector.Collect(where, "typed_filter_lookup_width", float64(stats.LookupWidth))
		r.metricsCollector.Collect(where, "typed_filter_update_width", float64(stats.UpdateWidth))
		r.metricsCollector.Collect(where, "typed_filter_lookup_port_stalls", float64(stats.LookupPortStalls))
		r.metricsCollector.Collect(where, "typed_filter_update_port_stalls", float64(stats.UpdatePortStalls))
		r.metricsCollector.Collect(where, "typed_filter_kick_attempts", float64(stats.KickAttempts))
		r.metricsCollector.Collect(where, "typed_filter_kicked_insertions", float64(stats.KickedInsertions))
		r.metricsCollector.Collect(where, "typed_filter_kick_rollbacks", float64(stats.KickRollbacks))
		typeNames := [...]string{
			"resident", "pending", "seen", "pattern",
			"granularity_pending",
		}
		for kind, typeStats := range stats.ByType {
			prefix := "typed_filter_" + typeNames[kind] + "_"
			reliable := 0.0
			if typeStats.Reliable {
				reliable = 1
			}
			r.metricsCollector.Collect(where, prefix+"reliable", reliable)
			r.metricsCollector.Collect(where, prefix+"queries", float64(typeStats.Queries))
			r.metricsCollector.Collect(where, prefix+"positives", float64(typeStats.Positives))
			r.metricsCollector.Collect(where, prefix+"negatives", float64(typeStats.Negatives))
			r.metricsCollector.Collect(where, prefix+"false_positives", float64(typeStats.FalsePositives))
			r.metricsCollector.Collect(where, prefix+"active_false_negatives", float64(typeStats.ActiveFalseNegatives))
			r.metricsCollector.Collect(where, prefix+"insertions", float64(typeStats.Insertions))
			r.metricsCollector.Collect(where, prefix+"deletes", float64(typeStats.Deletes))
			r.metricsCollector.Collect(where, prefix+"insert_failures", float64(typeStats.InsertFailures))
			r.metricsCollector.Collect(where, prefix+"reference_count_saturations", float64(typeStats.ReferenceCountSaturations))
			r.metricsCollector.Collect(where, prefix+"fail_open", float64(typeStats.FailOpen))
			r.metricsCollector.Collect(where, prefix+"lookup_busy_drops", float64(typeStats.LookupBusyDrops))
			r.metricsCollector.Collect(where, prefix+"update_busy_drops", float64(typeStats.UpdateBusyDrops))
			r.metricsCollector.Collect(where, prefix+"occupancy", float64(typeStats.Occupancy))
			r.metricsCollector.Collect(where, prefix+"peak_occupancy", float64(typeStats.PeakOccupancy))
		}
	}
}

func (r *Runner) reportInstCount() {
	// kernelTime := float64(r.kernelTimeCounter.BusyTime())
	for _, t := range r.instCountTracers {
		// kernelTime := float64(r.kernelTimeCounter.BusyTime())
		// float64(r.kernelTimeCounter.BusyTime())
		computeUnit, ok := t.cu.(*cu.ComputeUnit)
		if !ok {
			continue
		}
		gpuIndex := int(computeUnit.GPUID) - 1
		if gpuIndex < 0 || gpuIndex >= len(r.perGPUKernelTimeCounter) {
			continue
		}
		kernelTime := float64(r.perGPUKernelTimeCounter[gpuIndex].BusyTime())

		cuFreq := float64(computeUnit.Freq)
		numCycle := kernelTime * cuFreq

		r.metricsCollector.Collect(
			t.cu.Name(), "cu_inst_count", float64(t.tracer.count))

		r.metricsCollector.Collect(
			t.cu.Name(), "cu_CPI", numCycle/float64(t.tracer.count))
	}
}

func (r *Runner) reportWGCount() {
	var total uint64
	for _, t := range r.wgCountTracers {
		r.metricsCollector.Collect(
			t.cu.Name(), "cu_wg_count", float64(t.tracer.count))
		total += t.tracer.count
	}
	if r.maxWGStopper != nil && r.maxWGStopper.count > total {
		total = r.maxWGStopper.count
	}

	r.metricsCollector.Collect(
		r.platform.Driver.Name(), "total_wg_count", float64(total))
	if *maxWGCount > 0 {
		r.metricsCollector.Collect(
			r.platform.Driver.Name(), "max_wg_limit", float64(*maxWGCount))
		r.metricsCollector.Collect(
			r.platform.Driver.Name(), "max_wg_stop_completed", 1)
		r.metricsCollector.Collect(
			r.platform.Driver.Name(), "max_wg_launch_limited", 0)
		r.metricsCollector.Collect(
			r.platform.Driver.Name(), "max_wg_kernel_drained", 0)
		r.metricsCollector.Collect(
			r.platform.Driver.Name(), "max_wg_completed", float64(total))
		reached := 0.0
		if total >= *maxWGCount {
			reached = 1.0
		}
		r.metricsCollector.Collect(
			r.platform.Driver.Name(), "max_wg_reached", reached)
	}
}

func (r *Runner) reportCPIStack() {
	for _, t := range r.cuCPITraces {
		cu := t.cu
		hook := t.tracer

		r.reportCPIStackEntries(hook, cu, false)
		// r.reportCPIStackEntries(hook, cu, true)
	}
}

func (r *Runner) reportCPIStackEntries(
	hook *cu.CPIStackTracer,
	cu TraceableComponent,
	simdStack bool,
) {
	cpiStack := hook.GetCPIStack()
	if simdStack {
		cpiStack = hook.GetSIMDCPIStack()
	}

	keys := make([]string, 0, len(cpiStack))
	for k := range cpiStack {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	stackTypeName := "CPIStack"
	if simdStack {
		stackTypeName = "SIMDCPIStack"
	}

	for _, name := range keys {
		value := cpiStack[name]
		r.metricsCollector.Collect(cu.Name(), stackTypeName+"."+name, value)
	}
}

func (r *Runner) reportExecutionTime() {
	if r.Timing {
		r.metricsCollector.Collect(
			r.platform.Driver.Name(),
			"kernel_time", float64(r.kernelTimeCounter.BusyTime()))
		r.metricsCollector.Collect(
			r.platform.Driver.Name(),
			"total_time", float64(r.platform.Engine.CurrentTime()))

		for i, c := range r.perGPUKernelTimeCounter {
			if i >= len(r.platform.GPUs) {
				break
			}
			r.metricsCollector.Collect(
				r.platform.GPUs[i].CommandProcessor.Name(),
				"kernel_time", float64(c.BusyTime()))
		}
	}
}

func (r *Runner) reportCacheLatency() {
	for _, tracer := range r.cacheLatencyTracers {
		if tracer.tracer.AverageTime() == 0 {
			continue
		}

		r.metricsCollector.Collect(
			tracer.cache.Name(),
			"req_average_latency",
			float64(tracer.tracer.AverageTime()),
		)
	}
}

func (r *Runner) reportRDMALatency() {
	for _, tracer := range r.rdmaLatencyTracers {
		if tracer.tracer.AverageTime() == 0 {
			continue
		}

		r.metricsCollector.Collect(
			tracer.rdma.Name(),
			"req_average_latency",
			float64(tracer.tracer.AverageTime()),
		)
	}
}

func (r *Runner) reportTLBLatency() {
	for _, tracer := range r.tlbLatencyTracers {
		if tracer.tracer.AverageTime() == 0 {
			continue
		}

		r.metricsCollector.Collect(
			tracer.tlb.Name(),
			"req_average_latency",
			float64(tracer.tracer.AverageTime()),
		)
	}
}

func (r *Runner) reportCacheHitRate() {
	for _, tracer := range r.cacheHitRateTracers {
		readHit := tracer.tracer.GetStepCount("read-hit")
		readMiss := tracer.tracer.GetStepCount("read-miss")
		readMSHRHit := tracer.tracer.GetStepCount("read-mshr-miss")
		writeHit := tracer.tracer.GetStepCount("write-hit")
		writeMiss := tracer.tracer.GetStepCount("write-miss")
		writeMSHRHit := tracer.tracer.GetStepCount("write-mshr-miss")

		totalTransaction := readHit + readMiss + readMSHRHit +
			writeHit + writeMiss + writeMSHRHit

		if totalTransaction == 0 {
			continue
		}

		r.metricsCollector.Collect(
			tracer.cache.Name(), "read-hit", float64(readHit))
		r.metricsCollector.Collect(
			tracer.cache.Name(), "read-miss", float64(readMiss))
		r.metricsCollector.Collect(
			tracer.cache.Name(), "read-mshr-hit", float64(readMSHRHit))
		r.metricsCollector.Collect(
			tracer.cache.Name(), "write-hit", float64(writeHit))
		r.metricsCollector.Collect(
			tracer.cache.Name(), "write-miss", float64(writeMiss))
		r.metricsCollector.Collect(
			tracer.cache.Name(), "write-mshr-hit", float64(writeMSHRHit))
	}
}

func (r *Runner) reportTLBHitRate() {
	for _, tracer := range r.tlbHitRateTracers {
		hit := tracer.tracer.GetStepCount("hit")
		miss := tracer.tracer.GetStepCount("miss")
		mshrHit := tracer.tracer.GetStepCount("mshr-hit")

		totalTransaction := hit + miss + mshrHit

		if totalTransaction == 0 {
			continue
		}

		r.metricsCollector.Collect(
			tracer.tlb.Name(), "hit", float64(hit))
		r.metricsCollector.Collect(
			tracer.tlb.Name(), "miss", float64(miss))
		r.metricsCollector.Collect(
			tracer.tlb.Name(), "mshr-hit", float64(mshrHit))
	}
}

func (r *Runner) reportRDMATransactionCount() {
	for _, t := range r.rdmaTransactionCounters {
		r.metricsCollector.Collect(
			t.rdmaEngine.Name(),
			"outgoing_trans_count",
			float64(t.outgoingTracer.TotalCount()),
		)
		r.metricsCollector.Collect(
			t.rdmaEngine.Name(),
			"incoming_trans_count",
			float64(t.incomingTracer.TotalCount()),
		)
	}
}

func (r *Runner) reportGMMUTransactionCount() {
	for _, t := range r.gmmuTransactionCounters {
		r.metricsCollector.Collect(
			t.gmmuEngine.Name(),
			"outgoing_trans_count",
			float64(t.outgoingTracer.TotalCount()),
		)
		r.metricsCollector.Collect(
			t.gmmuEngine.Name(),
			"incoming_trans_count",
			float64(t.incomingTracer.TotalCount()),
		)
	}
}

func (r *Runner) reportMMUTransactionCount() {
	for _, t := range r.mmuTransactionCounters {
		r.metricsCollector.Collect(
			t.mmuEngine.Name(),
			"outgoing_trans_count",
			float64(t.outgoingTracer.TotalCount()),
		)
		r.metricsCollector.Collect(
			t.mmuEngine.Name(),
			"incoming_trans_count",
			float64(t.incomingTracer.TotalCount()),
		)
	}
}

func (r *Runner) reportDRAMTransactionCount() {
	for _, t := range r.dramTracers {
		r.metricsCollector.Collect(
			t.dram.Name(),
			"read_trans_count",
			float64(t.tracer.readCount),
		)
		r.metricsCollector.Collect(
			t.dram.Name(),
			"write_trans_count",
			float64(t.tracer.writeCount),
		)
		r.metricsCollector.Collect(
			t.dram.Name(),
			"read_avg_latency",
			float64(t.tracer.readAvgLatency),
		)
		r.metricsCollector.Collect(
			t.dram.Name(),
			"write_avg_latency",
			float64(t.tracer.writeAvgLatency),
		)
		r.metricsCollector.Collect(
			t.dram.Name(),
			"read_size",
			float64(t.tracer.readSize),
		)
		r.metricsCollector.Collect(
			t.dram.Name(),
			"write_size",
			float64(t.tracer.writeSize),
		)
	}
}

func (r *Runner) reportL2TLBHitRate() {
	for _, tracer := range r.l2TLBHitRateTracers {
		totalDownstream, localDownstream, iommuDownstream :=
			tracer.l2TLB.DownstreamRequestCounts()
		r.metricsCollector.Collect(
			tracer.l2TLB.Name(),
			"downstream_req_count",
			float64(totalDownstream),
		)
		r.metricsCollector.Collect(
			tracer.l2TLB.Name(),
			"local_req_count",
			float64(localDownstream),
		)
		r.metricsCollector.Collect(
			tracer.l2TLB.Name(),
			"iommu_req_count",
			float64(iommuDownstream),
		)

		hit := tracer.tracer.GetStepCount("hit")
		miss := tracer.tracer.GetStepCount("miss")
		mshrHit := tracer.tracer.GetStepCount("mshr-hit")

		totalTransaction := hit + miss + mshrHit

		if totalTransaction == 0 {
			continue
		}

		r.metricsCollector.Collect(
			tracer.l2TLB.Name(), "hit", float64(hit))
		r.metricsCollector.Collect(
			tracer.l2TLB.Name(), "miss", float64(miss))
		r.metricsCollector.Collect(
			tracer.l2TLB.Name(), "mshr-hit", float64(mshrHit))
	}
}

func (r *Runner) reportL2TLBLatency() {
	for _, tracer := range r.l2TLBLatencyTracers {
		if tracer.tracer.AverageTime() == 0 {
			continue
		}

		r.metricsCollector.Collect(
			tracer.l2TLB.Name(),
			"req_average_latency",
			float64(tracer.tracer.AverageTime()),
		)
	}
}

func (r *Runner) reportGMMULatency() {
	for _, tracer := range r.gmmuLatencyTracers {
		if tracer.tracer.AverageTime() == 0 {
			continue
		}

		r.metricsCollector.Collect(
			tracer.gmmu.Name(),
			"req_average_latency",
			float64(tracer.tracer.AverageTime()),
		)
	}
}

func (r *Runner) reportMMULatency() {
	for _, tracer := range r.mmuLatencyTracers {
		if tracer.tracer.AverageTime() == 0 {
			continue
		}

		r.metricsCollector.Collect(
			"MMU",
			"req_average_latency",
			float64(tracer.tracer.AverageTime()),
		)
	}
}

func (r *Runner) reportIOMMUTLBStats() {
	if r.platform == nil || r.platform.IOMMUTLB == nil {
		return
	}

	r.metricsCollector.Collect(
		r.platform.IOMMUTLB.Name(),
		"incoming_req_count",
		float64(r.platform.IOMMUTLB.IncomingRequestCount()),
	)
	r.metricsCollector.Collect(
		r.platform.IOMMUTLB.Name(),
		"req_to_mmu_count",
		float64(r.platform.IOMMUTLB.DownstreamRequestCount()),
	)
	r.metricsCollector.Collect(
		r.platform.IOMMUTLB.Name(),
		"lookup_latency_cycles",
		float64(r.platform.IOMMUTLB.LookupLatencyCycles()),
	)
}

func (r *Runner) reportMMUCoalescingStats() {
	if r.platform == nil || len(r.platform.GPUs) == 0 || r.platform.GPUs[0].MMUEngine == nil {
		return
	}

	enabled := 0.0
	if r.platform.GPUs[0].MMUEngine.WalkCoalescingEnabled() {
		enabled = 1.0
	}
	lastLevel, twoLevel := r.platform.GPUs[0].MMUEngine.CoalescingStats()
	r.metricsCollector.Collect(
		"MMU",
		"coalescing_enabled",
		enabled,
	)
	r.metricsCollector.Collect(
		"MMU",
		"last_level_coalesced_reqs",
		float64(lastLevel),
	)
	r.metricsCollector.Collect(
		"MMU",
		"two_level_coalesced_reqs",
		float64(twoLevel),
	)
}

// func (r *Runner) reportGMMUCounts() {
// 	for _, t := range r.gmmuCountTracers {
// 		r.metricsCollector.Collect(
// 			t.gmmu.Name(),
// 			"total_ats_count",
// 			float64(t.tracer.GetTotalATSCount()),
// 		)
// 		r.metricsCollector.Collect(
// 			t.gmmu.Name(),
// 			"local_ats_count",
// 			float64(t.tracer.GetLocalATSCount()),
// 		)
// 		r.metricsCollector.Collect(
// 			t.gmmu.Name(),
// 			"remote_ats_count",
// 			float64(t.tracer.GetRemoteATSCount()),
// 		)
// 	}
// }

// func (r *Runner) reportL2TLBCounts() {
// 	for _, t := range r.L2TLBTracers {
// 		r.metricsCollector.Collect(
// 			t.tlb.Name(),
// 			"AverageLocalAccessCounts",
// 			float64(t.tracer.ReportAverageLocalAccessCounts()),
// 		)
// 		r.metricsCollector.Collect(
// 			t.tlb.Name(),
// 			"AverageRemoteAccessCounts",
// 			float64(t.tracer.ReportAverageRemoteAccessCounts()),
// 		)
// 	}
// }
