package runner

import (
	"sort"

	"github.com/sarchlab/akita/v3/mem/cache/writearound"
	"github.com/sarchlab/akita/v3/mem/cache/writeback"
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
	r.reportM1Stats()
	r.reportM2M3Stats()
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
		readRemoteDataHit := tracer.tracer.GetStepCount("read-remote-data-hit")
		readRemoteDataMiss := tracer.tracer.GetStepCount("read-remote-data-miss")
		readMSHRHit := tracer.tracer.GetStepCount("read-mshr-miss")
		writeHit := tracer.tracer.GetStepCount("write-hit")
		writeMiss := tracer.tracer.GetStepCount("write-miss")
		writeMSHRHit := tracer.tracer.GetStepCount("write-mshr-miss")

		totalTransaction := readHit + readMiss + readRemoteDataHit +
			readRemoteDataMiss + readMSHRHit +
			writeHit + writeMiss + writeMSHRHit

		if totalTransaction == 0 {
			continue
		}

		r.metricsCollector.Collect(
			tracer.cache.Name(), "read-hit", float64(readHit))
		r.metricsCollector.Collect(
			tracer.cache.Name(), "read-miss", float64(readMiss))
		r.metricsCollector.Collect(
			tracer.cache.Name(), "read-remote-data-hit",
			float64(readRemoteDataHit))
		r.metricsCollector.Collect(
			tracer.cache.Name(), "read-remote-data-miss",
			float64(readRemoteDataMiss))
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

func (r *Runner) reportM1Stats() {
	if r.platform == nil {
		return
	}

	for _, gpu := range r.platform.GPUs {
		for _, component := range gpu.L1VCaches {
			l1v, ok := component.(*writearound.Cache)
			if !ok {
				continue
			}
			m1 := l1v.GetM1Stats()
			r.metricsCollector.Collect(
				l1v.Name(), "m1_l1v_batch_enabled",
				boolMetric(m1.L1VBatchEnabled))
			r.metricsCollector.Collect(
				l1v.Name(), "m1_l1v_adaptive_enabled",
				boolMetric(m1.L1VAdaptiveEnabled))
			r.metricsCollector.Collect(
				l1v.Name(), "m1_l1v_requests_seen",
				float64(m1.L1VRequestsSeen))
			r.metricsCollector.Collect(
				l1v.Name(), "m1_l1v_batchable_reads",
				float64(m1.L1VBatchableReads))
			r.metricsCollector.Collect(
				l1v.Name(), "m1_l1v_bypass_requests",
				float64(m1.L1VBypassRequests))
			r.metricsCollector.Collect(
				l1v.Name(), "m1_l1v_batches_created",
				float64(m1.L1VBatchesCreated))
			r.metricsCollector.Collect(
				l1v.Name(), "m1_l1v_batches_drained",
				float64(m1.L1VBatchesDrained))
			r.metricsCollector.Collect(
				l1v.Name(), "m1_l1v_lines_in_batches",
				float64(m1.L1VLinesInBatches))
			r.metricsCollector.Collect(
				l1v.Name(), "m1_l1v_requests_in_batches",
				float64(m1.L1VRequestsInBatches))
			r.metricsCollector.Collect(
				l1v.Name(), "m1_l1v_duplicate_line_waiters",
				float64(m1.L1VDuplicateWaiters))
			r.metricsCollector.Collect(
				l1v.Name(), "m1_l1v_avg_lines_per_batch",
				safeDiv(float64(m1.L1VLinesInBatches),
					float64(m1.L1VBatchesDrained)))
			r.metricsCollector.Collect(
				l1v.Name(), "m1_l1v_avg_wait_ns",
				safeDiv(m1.L1VTotalWaitNS,
					float64(m1.L1VWaitSamples)))
			r.metricsCollector.Collect(
				l1v.Name(), "m1_l1v_max_lines_per_batch",
				float64(m1.L1VMaxLinesPerBatch))
			r.metricsCollector.Collect(
				l1v.Name(), "m1_l1v_full_drains",
				float64(m1.L1VFullDrains))
			r.metricsCollector.Collect(
				l1v.Name(), "m1_l1v_timeout_drains",
				float64(m1.L1VTimeoutDrains))
			r.metricsCollector.Collect(
				l1v.Name(), "m1_l1v_capacity_drains",
				float64(m1.L1VCapacityDrains))
			r.metricsCollector.Collect(
				l1v.Name(), "m1_l1v_manual_drains",
				float64(m1.L1VManualDrains))
			r.metricsCollector.Collect(
				l1v.Name(), "m1_l1v_adaptive_bypass_requests",
				float64(m1.L1VAdaptiveBypassRequests))
			r.metricsCollector.Collect(
				l1v.Name(), "m1_l1v_adaptive_disable_events",
				float64(m1.L1VAdaptiveDisableEvents))
			r.metricsCollector.Collect(
				l1v.Name(), "m1_l1v_adaptive_bad_drains",
				float64(m1.L1VAdaptiveBadDrains))
			r.metricsCollector.Collect(
				l1v.Name(), "m1_l1v_adaptive_good_drains",
				float64(m1.L1VAdaptiveGoodDrains))
		}

		for _, component := range gpu.L2Caches {
			l2, ok := component.(*writeback.Cache)
			if !ok {
				continue
			}
			m1 := l2.GetM1Stats()
			r.metricsCollector.Collect(
				l2.Name(), "m1_cache_helper_enabled",
				boolMetric(m1.CacheHelperEnabled))
			r.metricsCollector.Collect(
				l2.Name(), "m1_dram_helper_enabled",
				boolMetric(m1.DRAMHelperEnabled))
			r.metricsCollector.Collect(
				l2.Name(), "m1_local_requests_seen",
				float64(m1.LocalRequestsSeen))
			r.metricsCollector.Collect(
				l2.Name(), "m1_local_batchable_reads",
				float64(m1.LocalBatchableReads))
			r.metricsCollector.Collect(
				l2.Name(), "m1_cache_bypass_requests",
				float64(m1.CacheBypassRequests))
			r.metricsCollector.Collect(
				l2.Name(), "m1_cache_batches_created",
				float64(m1.CacheBatchesCreated))
			r.metricsCollector.Collect(
				l2.Name(), "m1_cache_batches_drained",
				float64(m1.CacheBatchesDrained))
			r.metricsCollector.Collect(
				l2.Name(), "m1_cache_lines_in_batches",
				float64(m1.CacheLinesInBatches))
			r.metricsCollector.Collect(
				l2.Name(), "m1_cache_requests_in_batches",
				float64(m1.CacheRequestsInBatches))
			r.metricsCollector.Collect(
				l2.Name(), "m1_cache_duplicate_line_waiters",
				float64(m1.CacheDuplicateWaiters))
			r.metricsCollector.Collect(
				l2.Name(), "m1_cache_coalesced_waiters",
				float64(m1.CacheCoalescedWaiters))
			r.metricsCollector.Collect(
				l2.Name(), "m1_cache_avg_lines_per_batch",
				safeDiv(float64(m1.CacheLinesInBatches),
					float64(m1.CacheBatchesDrained)))
			r.metricsCollector.Collect(
				l2.Name(), "m1_cache_avg_wait_ns",
				safeDiv(m1.CacheTotalWaitNS,
					float64(m1.CacheWaitSamples)))
			r.metricsCollector.Collect(
				l2.Name(), "m1_cache_max_lines_per_batch",
				float64(m1.CacheMaxLinesPerBatch))
			r.metricsCollector.Collect(
				l2.Name(), "m1_cache_full_drains",
				float64(m1.CacheFullDrains))
			r.metricsCollector.Collect(
				l2.Name(), "m1_cache_timeout_drains",
				float64(m1.CacheTimeoutDrains))
			r.metricsCollector.Collect(
				l2.Name(), "m1_cache_capacity_drains",
				float64(m1.CacheCapacityDrains))
			r.metricsCollector.Collect(
				l2.Name(), "m1_l2_probe_lines",
				float64(m1.L2ProbeLines))
			r.metricsCollector.Collect(
				l2.Name(), "m1_l2_probe_hits",
				float64(m1.L2ProbeHits))
			r.metricsCollector.Collect(
				l2.Name(), "m1_l2_probe_misses",
				float64(m1.L2ProbeMisses))
			r.metricsCollector.Collect(
				l2.Name(), "m1_l2_probe_mshr_hits",
				float64(m1.L2ProbeMSHRHits))
			r.metricsCollector.Collect(
				l2.Name(), "m1_l2_hit_lines_completed_from_batch",
				float64(m1.L2HitLinesCompletedFromBatch))
			r.metricsCollector.Collect(
				l2.Name(), "m1_l2_miss_lines_sent_to_dram_helper",
				float64(m1.L2MissLinesSentToDRAMHelper))
			r.metricsCollector.Collect(
				l2.Name(), "m1_dram_miss_lines_seen",
				float64(m1.DRAMMissLinesSeen))
			r.metricsCollector.Collect(
				l2.Name(), "m1_dram_batches_created",
				float64(m1.DRAMBatchesCreated))
			r.metricsCollector.Collect(
				l2.Name(), "m1_dram_batches_drained",
				float64(m1.DRAMBatchesDrained))
			r.metricsCollector.Collect(
				l2.Name(), "m1_dram_lines_in_batches",
				float64(m1.DRAMLinesInBatches))
			r.metricsCollector.Collect(
				l2.Name(), "m1_dram_singleton_fallbacks",
				float64(m1.DRAMSingletonFallbacks))
			r.metricsCollector.Collect(
				l2.Name(), "m1_dram_avg_lines_per_batch",
				safeDiv(float64(m1.DRAMLinesInBatches),
					float64(m1.DRAMBatchesDrained)))
			r.metricsCollector.Collect(
				l2.Name(), "m1_dram_avg_wait_ns",
				safeDiv(m1.DRAMTotalWaitNS,
					float64(m1.DRAMWaitSamples)))
			r.metricsCollector.Collect(
				l2.Name(), "m1_dram_max_lines_per_batch",
				float64(m1.DRAMMaxLinesPerBatch))
			r.metricsCollector.Collect(
				l2.Name(), "m1_dram_full_drains",
				float64(m1.DRAMFullDrains))
			r.metricsCollector.Collect(
				l2.Name(), "m1_dram_timeout_drains",
				float64(m1.DRAMTimeoutDrains))
			r.metricsCollector.Collect(
				l2.Name(), "m1_dram_capacity_drains",
				float64(m1.DRAMCapacityDrains))
			r.metricsCollector.Collect(
				l2.Name(), "m1_dram_multiline_reads",
				float64(m1.DRAMMultiLineReads))
			r.metricsCollector.Collect(
				l2.Name(), "m1_dram_singleline_reads",
				float64(m1.DRAMSingleLineReads))
			r.metricsCollector.Collect(
				l2.Name(), "m1_dram_request_reduction_pct",
				packetReductionPct(
					float64(m1.DRAMMissLinesSeen),
					float64(m1.DRAMMultiLineReads+
						m1.DRAMSingleLineReads)))
		}
	}
}

func (r *Runner) reportM2M3Stats() {
	if r.platform == nil {
		return
	}

	for _, gpu := range r.platform.GPUs {
		if gpu.RDMAEngine == nil {
			continue
		}
		rdmaEngine := gpu.RDMAEngine
		m2 := rdmaEngine.GetM2Stats()
		m3 := rdmaEngine.GetM3Stats()

		r.metricsCollector.Collect(
			rdmaEngine.Name(), "m2_enabled", boolMetric(m2.Enabled))
		r.metricsCollector.Collect(
			rdmaEngine.Name(), "m2_original_remote_requests",
			float64(m2.OriginalRemoteRequests))
		r.metricsCollector.Collect(
			rdmaEngine.Name(), "m2_batchable_remote_reads",
			float64(m2.BatchableRemoteReads))
		r.metricsCollector.Collect(
			rdmaEngine.Name(), "m2_bypass_requests",
			float64(m2.BypassRequests))
		r.metricsCollector.Collect(
			rdmaEngine.Name(), "m2_batched_packets",
			float64(m2.BatchedPackets))
		r.metricsCollector.Collect(
			rdmaEngine.Name(), "m2_lines_in_batched_packets",
			float64(m2.LinesInBatchedPackets))
		r.metricsCollector.Collect(
			rdmaEngine.Name(), "m2_requests_in_batched_packets",
			float64(m2.RequestsInBatchedPackets))
		r.metricsCollector.Collect(
			rdmaEngine.Name(), "m2_duplicate_line_reads",
			float64(m2.DuplicateLineReads))
		r.metricsCollector.Collect(
			rdmaEngine.Name(), "m2_au_prefetch_enabled",
			boolMetric(m2.AUPrefetchEnabled))
		r.metricsCollector.Collect(
			rdmaEngine.Name(), "m2_au_prefetch_lines_requested",
			float64(m2.AUPrefetchLinesRequested))
		r.metricsCollector.Collect(
			rdmaEngine.Name(), "m2_au_prefetch_lines_returned",
			float64(m2.AUPrefetchLinesReturned))
		r.metricsCollector.Collect(
			rdmaEngine.Name(), "m2_au_prefetch_demand_hits",
			float64(m2.AUPrefetchDemandHits))
		r.metricsCollector.Collect(
			rdmaEngine.Name(), "m2_full_flushes", float64(m2.FullFlushes))
		r.metricsCollector.Collect(
			rdmaEngine.Name(), "m2_timeout_flushes",
			float64(m2.TimeoutFlushes))
		r.metricsCollector.Collect(
			rdmaEngine.Name(), "m2_capacity_flushes",
			float64(m2.CapacityFlushes))
		r.metricsCollector.Collect(
			rdmaEngine.Name(), "m2_conflict_flushes",
			float64(m2.ConflictFlushes))
		r.metricsCollector.Collect(
			rdmaEngine.Name(), "m2_drain_flushes",
			float64(m2.DrainFlushes))
		r.metricsCollector.Collect(
			rdmaEngine.Name(), "m2_max_batch_lines",
			float64(m2.MaxBatchLines))
		r.metricsCollector.Collect(
			rdmaEngine.Name(), "m2_max_requests_per_batch",
			float64(m2.MaxRequestsPerBatch))
		r.metricsCollector.Collect(
			rdmaEngine.Name(), "m2_avg_wait_ns",
			safeDiv(m2.TotalWaitNS, float64(m2.WaitSamples)))
		r.metricsCollector.Collect(
			rdmaEngine.Name(), "m2_avg_batch_size",
			safeDiv(float64(m2.LinesInBatchedPackets),
				float64(m2.BatchedPackets)))
		r.metricsCollector.Collect(
			rdmaEngine.Name(), "m2_packet_reduction_pct",
			packetReductionPct(
				float64(m2.BatchableRemoteReads),
				float64(m2.BatchedPackets)))
		r.metricsCollector.Collect(
			rdmaEngine.Name(), "m2_owner_batch_requests",
			float64(m2.OwnerBatchRequests))
		r.metricsCollector.Collect(
			rdmaEngine.Name(), "m2_owner_local_read_reqs",
			float64(m2.OwnerLocalReadReqs))
		r.metricsCollector.Collect(
			rdmaEngine.Name(), "m2_owner_batch_responses",
			float64(m2.OwnerBatchResponses))
		r.metricsCollector.Collect(
			rdmaEngine.Name(), "m2_requester_batch_rsps",
			float64(m2.RequesterBatchRsps))
		r.metricsCollector.Collect(
			rdmaEngine.Name(), "m2_requester_unbatch_rsps",
			float64(m2.RequesterUnbatchRsps))

		r.metricsCollector.Collect(
			rdmaEngine.Name(), "m3_enabled", boolMetric(m3.Enabled))
		r.metricsCollector.Collect(
			rdmaEngine.Name(), "m3_owner_received_packets",
			float64(m3.OwnerReceivedPackets))
		r.metricsCollector.Collect(
			rdmaEngine.Name(), "m3_owner_served_packets",
			float64(m3.OwnerServedPackets))
		r.metricsCollector.Collect(
			rdmaEngine.Name(), "m3_owner_served_lines",
			float64(m3.OwnerServedLines))
		r.metricsCollector.Collect(
			rdmaEngine.Name(), "m3_consecutive_cap_hits",
			float64(m3.ConsecutiveCapHits))
		r.metricsCollector.Collect(
			rdmaEngine.Name(), "m3_hard_age_escapes",
			float64(m3.HardAgeEscapes))
		r.metricsCollector.Collect(
			rdmaEngine.Name(), "m3_fallback_oldest",
			float64(m3.FallbackOldest))
		r.metricsCollector.Collect(
			rdmaEngine.Name(), "m3_max_consecutive_service",
			float64(m3.MaxConsecutive))
		r.metricsCollector.Collect(
			rdmaEngine.Name(), "m3_avg_owner_wait_ns",
			safeDiv(m3.TotalOwnerWaitNS, float64(m3.OwnerWaitSamples)))

		for _, cacheComponent := range gpu.L1VCaches {
			l1v, ok := cacheComponent.(*writearound.Cache)
			if !ok {
				continue
			}
			stats := l1v.RemoteDataStats()
			r.metricsCollector.Collect(
				l1v.Name(), "m3_l1_remote_cache_enabled",
				boolMetric(stats.Enabled))
			r.metricsCollector.Collect(
				l1v.Name(), "m3_l1_remote_cache_entries",
				float64(stats.Entries))
			r.metricsCollector.Collect(
				l1v.Name(), "m3_l1_remote_cache_demand_hits",
				float64(stats.DemandHits))
			r.metricsCollector.Collect(
				l1v.Name(), "m3_l1_remote_cache_demand_misses",
				float64(stats.DemandMisses))
			r.metricsCollector.Collect(
				l1v.Name(), "m3_l1_remote_cache_demand_fills",
				float64(stats.DemandFills))
			r.metricsCollector.Collect(
				l1v.Name(), "m3_l1_remote_cache_prefetch_fills",
				float64(stats.PrefetchFills))
			r.metricsCollector.Collect(
				l1v.Name(), "m3_l1_remote_cache_prefetch_fill_drops",
				float64(stats.PrefetchFillDrops))
			r.metricsCollector.Collect(
				l1v.Name(), "m3_l1_remote_cache_prefetch_demand_hits",
				float64(stats.PrefetchDemandHits))
			r.metricsCollector.Collect(
				l1v.Name(), "m3_l1_remote_cache_invalidations",
				float64(stats.Invalidations))
			r.metricsCollector.Collect(
				l1v.Name(), "m3_l1_remote_cache_evictions",
				float64(stats.Evictions))
			r.metricsCollector.Collect(
				l1v.Name(), "m3_l1_remote_cache_max_occupancy",
				float64(stats.MaxOccupancy))
		}
	}
}

func boolMetric(v bool) float64 {
	if v {
		return 1
	}
	return 0
}

func safeDiv(num, den float64) float64 {
	if den == 0 {
		return 0
	}
	return num / den
}

func packetReductionPct(original, packets float64) float64 {
	if original == 0 {
		return 0
	}
	return 100 * (1 - packets/original)
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
