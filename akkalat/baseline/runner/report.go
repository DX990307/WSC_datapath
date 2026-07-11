package runner

import (
	"fmt"
	"sort"

	"github.com/sarchlab/akita/v3/mem/cache/writeback"
	"github.com/sarchlab/akita/v3/mem/dram"
	memtrace "github.com/sarchlab/akita/v3/mem/trace"
	"github.com/sarchlab/mgpusim/v3/timing/cu"
)

func (r *Runner) reportStats() {
	r.reportExecutionTime()
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
	r.reportDRAMBatchStats()
	r.reportDRAMRowReorderStats()
	r.reportRemoteDataPathStats()
	if err := memtrace.DumpMemoryPathTrace(); err != nil {
		panic(err)
	}
	if err := memtrace.DumpL2SourceStats(); err != nil {
		panic(err)
	}
	// r.reportGMMUCounts()
	// r.reportL2TLBCounts()
	r.dumpMetrics()
}

func (r *Runner) reportDRAMRowReorderStats() {
	if !r.Timing {
		return
	}
	for _, gpu := range r.platform.GPUs {
		for _, component := range gpu.MemControllers {
			controller, ok := component.(*dram.MemController)
			if !ok {
				continue
			}
			stats := controller.GetRowAwareStats()
			where := controller.Name()
			enabled := 0.0
			if stats.Enabled {
				enabled = 1
			}
			r.metricsCollector.Collect(where,
				"dram_row_reorder_enabled", enabled)
			r.metricsCollector.Collect(where,
				"dram_row_commands_issued", float64(stats.CommandsIssued))
			r.metricsCollector.Collect(where,
				"dram_row_column_commands", float64(stats.ColumnCommands))
			r.metricsCollector.Collect(where,
				"dram_row_reuse_hits", float64(stats.RowReuseHits))
			r.metricsCollector.Collect(where,
				"dram_row_activate_commands", float64(stats.ActivateCommands))
			r.metricsCollector.Collect(where,
				"dram_row_precharge_commands", float64(stats.PrechargeCommands))
			r.metricsCollector.Collect(where,
				"dram_row_aged_priority_issues",
				float64(stats.AgedPriorityIssues))
			r.metricsCollector.Collect(where,
				"dram_row_max_queue_age_cycles",
				float64(stats.MaxQueueAgeCycles))
		}
	}
}

func (r *Runner) reportDRAMBatchStats() {
	if !r.Timing {
		return
	}
	for _, gpu := range r.platform.GPUs {
		for _, component := range gpu.L2Caches {
			l2, ok := component.(*writeback.Cache)
			if !ok {
				continue
			}
			stats := l2.GetDRAMBatchStats()
			where := l2.Name()
			enabled := 0.0
			if stats.Enabled {
				enabled = 1
			}
			r.metricsCollector.Collect(where, "dram_batch_enabled", enabled)
			r.metricsCollector.Collect(where, "dram_batch_miss_lines", float64(stats.MissLinesSeen))
			r.metricsCollector.Collect(where, "dram_batches_created", float64(stats.BatchesCreated))
			r.metricsCollector.Collect(where, "dram_batches_drained", float64(stats.BatchesDrained))
			r.metricsCollector.Collect(where, "dram_batch_lines", float64(stats.LinesInBatches))
			r.metricsCollector.Collect(where, "dram_batch_singleton_fallbacks", float64(stats.SingletonFallbacks))
			r.metricsCollector.Collect(where, "dram_batch_full_drains", float64(stats.FullDrains))
			r.metricsCollector.Collect(where, "dram_batch_timeout_drains", float64(stats.TimeoutDrains))
			r.metricsCollector.Collect(where, "dram_batch_capacity_drains", float64(stats.CapacityDrains))
			r.metricsCollector.Collect(where, "dram_batch_drain_drains", float64(stats.DrainDrains))
			r.metricsCollector.Collect(where, "dram_batch_max_lines", float64(stats.MaxLinesPerBatch))
			r.metricsCollector.Collect(where, "dram_batch_wait_total_ns", stats.TotalWaitNS)
			r.metricsCollector.Collect(where, "dram_batch_wait_samples", float64(stats.WaitSamples))
			r.metricsCollector.Collect(where, "dram_batch_multiline_reads", float64(stats.MultiLineReads))
			r.metricsCollector.Collect(where, "dram_batch_singleline_reads", float64(stats.SingleLineReads))
			r.metricsCollector.Collect(where, "dram_adapter_observations", float64(stats.AdapterObservations))
			r.metricsCollector.Collect(where, "dram_adapter_useful", float64(stats.AdapterUseful))
			r.metricsCollector.Collect(where, "dram_adapter_predictions", float64(stats.AdapterPredictions))
			r.metricsCollector.Collect(where, "dram_adapter_inflight_hits", float64(stats.AdapterInflightHits))
			r.metricsCollector.Collect(where, "dram_adapter_buffer_hits", float64(stats.AdapterBufferHits))
			r.metricsCollector.Collect(where, "dram_adapter_unused", float64(stats.AdapterUnused))
			r.metricsCollector.Collect(where, "dram_adapter_confidence", float64(stats.AdapterConfidence))
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
		if !stats.Enabled {
			continue
		}
		prefetchEnabled := 0.0
		if stats.AUPrefetchEnabled {
			prefetchEnabled = 1
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
		r.metricsCollector.Collect(where, "remote_au_prefetch_enabled", prefetchEnabled)
		r.metricsCollector.Collect(where, "remote_dedup_enabled", dedupEnabled)
		r.metricsCollector.Collect(where, "remote_batching_enabled", batchingEnabled)
		r.metricsCollector.Collect(where, "remote_requester_l2_enabled", requesterL2Enabled)
		r.metricsCollector.Collect(where, "remote_config_batch_lines", float64(stats.MaxBatchLines))
		r.metricsCollector.Collect(where, "remote_config_wait_ns", float64(stats.MaxWaitNS))
		r.metricsCollector.Collect(where, "remote_config_max_batches", float64(stats.MaxBatches))
		r.metricsCollector.Collect(where, "remote_config_reuse_entries", float64(stats.ReuseTableEntries))
		r.metricsCollector.Collect(where, "remote_logical_reads", float64(stats.LogicalRemoteReads))
		r.metricsCollector.Collect(where, "remote_wire_lines", float64(stats.WireLines))
		r.metricsCollector.Collect(where, "remote_demand_wire_lines", float64(stats.DemandWireLines))
		r.metricsCollector.Collect(where, "remote_prefetch_wire_lines", float64(stats.PrefetchWireLines))
		r.metricsCollector.Collect(where, "remote_duplicate_reads", float64(stats.DuplicateReads))
		r.metricsCollector.Collect(where, "remote_pre_send_merges", float64(stats.CollectingMerges))
		r.metricsCollector.Collect(where, "remote_inflight_merges", float64(stats.InflightMerges))
		r.metricsCollector.Collect(where, "remote_ready_merges", float64(stats.ReadyMerges))
		r.metricsCollector.Collect(where, "remote_l2_probe_hits", float64(stats.L2ProbeHits))
		r.metricsCollector.Collect(where, "remote_l2_probe_misses", float64(stats.L2ProbeMisses))
		r.metricsCollector.Collect(where, "remote_l2_logical_responses", float64(stats.L2LogicalResponses))
		r.metricsCollector.Collect(where, "remote_single_packets", float64(stats.SingleReadPackets))
		r.metricsCollector.Collect(where, "remote_bitmap_packets", float64(stats.BitmapPackets))
		r.metricsCollector.Collect(where, "remote_bitmap_lines", float64(stats.BitmapLines))
		for lines := 1; lines < len(stats.BatchSizeHistogram); lines++ {
			packets := stats.BatchSizeHistogram[lines]
			if packets == 0 {
				continue
			}
			r.metricsCollector.Collect(where,
				fmt.Sprintf("remote_batch_size_%02d_packets", lines),
				float64(packets))
		}
		r.metricsCollector.Collect(where, "remote_au_prefetch_candidates", float64(stats.AUPrefetchCandidates))
		r.metricsCollector.Collect(where, "remote_au_prefetch_converted_demand", float64(stats.AUPrefetchConvertedDemand))
		r.metricsCollector.Collect(where, "remote_au_prefetch_demand_merges", float64(stats.AUPrefetchDemandMerges))
		r.metricsCollector.Collect(where, "remote_two_touch_candidates", float64(stats.TwoTouchCandidates))
		r.metricsCollector.Collect(where, "remote_two_touch_fill_attempts", float64(stats.TwoTouchFillAttempts))
		r.metricsCollector.Collect(where, "remote_prefetch_fill_attempts", float64(stats.PrefetchFillAttempts))
		r.metricsCollector.Collect(where, "remote_two_touch_installed_fills", float64(stats.TwoTouchInstalledFills))
		r.metricsCollector.Collect(where, "remote_prefetch_installed_fills", float64(stats.PrefetchInstalledFills))
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
		r.metricsCollector.Collect(where, "remote_timeout_flushes", float64(stats.TimeoutFlushes))
		r.metricsCollector.Collect(where, "remote_capacity_flushes", float64(stats.CapacityFlushes))
		r.metricsCollector.Collect(where, "remote_conflict_flushes", float64(stats.ConflictFlushes))
		r.metricsCollector.Collect(where, "remote_drain_flushes", float64(stats.DrainFlushes))

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
			r.metricsCollector.Collect(l2Where, "remote_l2_prefetch_fill_attempts", float64(l2Stats.PrefetchFillAttempts))
			r.metricsCollector.Collect(l2Where, "remote_installed_fills", float64(l2Stats.InstalledFills))
			r.metricsCollector.Collect(l2Where, "remote_l2_two_touch_installed_fills", float64(l2Stats.TwoTouchInstalledFills))
			r.metricsCollector.Collect(l2Where, "remote_l2_prefetch_installed_fills", float64(l2Stats.PrefetchInstalledFills))
			r.metricsCollector.Collect(l2Where, "remote_dropped_fills", float64(l2Stats.DroppedFills))
			r.metricsCollector.Collect(l2Where, "remote_l2_two_touch_dropped_fills", float64(l2Stats.TwoTouchDroppedFills))
			r.metricsCollector.Collect(l2Where, "remote_l2_prefetch_dropped_fills", float64(l2Stats.PrefetchDroppedFills))
			r.metricsCollector.Collect(l2Where, "remote_filter_insert_failures", float64(l2Stats.FilterInsertFails))
			r.metricsCollector.Collect(l2Where, "remote_tracked_evictions", float64(l2Stats.TrackedEvictions))
			r.metricsCollector.Collect(l2Where, "remote_unused_two_touch_retirements", float64(l2Stats.UnusedTwoTouchRetirements))
			r.metricsCollector.Collect(l2Where, "remote_unused_prefetch_retirements", float64(l2Stats.UnusedPrefetchRetirements))
			r.metricsCollector.Collect(l2Where, "remote_replica_probe_hits", float64(l2Stats.ReplicaProbeHits))
			r.metricsCollector.Collect(l2Where, "remote_two_touch_replica_hits", float64(l2Stats.TwoTouchReplicaHits))
			r.metricsCollector.Collect(l2Where, "remote_prefetch_replica_hits", float64(l2Stats.PrefetchReplicaHits))
			r.metricsCollector.Collect(l2Where, "remote_useful_two_touch_fills", float64(l2Stats.UsefulTwoTouchFills))
			r.metricsCollector.Collect(l2Where, "remote_useful_prefetch_fills", float64(l2Stats.UsefulPrefetchFills))
			r.metricsCollector.Collect(l2Where, "remote_fill_into_invalid", float64(l2Stats.FillIntoInvalid))
			r.metricsCollector.Collect(l2Where, "remote_fill_replaced_remote", float64(l2Stats.FillReplacedRemote))
			r.metricsCollector.Collect(l2Where, "remote_fill_displaced_local_clean", float64(l2Stats.FillDisplacedLocalClean))
			r.metricsCollector.Collect(l2Where, "remote_two_touch_local_displacements", float64(l2Stats.TwoTouchLocalDisplacements))
			r.metricsCollector.Collect(l2Where, "remote_prefetch_local_displacements", float64(l2Stats.PrefetchLocalDisplacements))
			r.metricsCollector.Collect(l2Where, "remote_current_replicas", float64(l2Stats.CurrentRemoteReplicas))
			r.metricsCollector.Collect(l2Where, "remote_peak_replicas", float64(l2Stats.PeakRemoteReplicas))
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
