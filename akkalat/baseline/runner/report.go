package runner

import (
	"sort"

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
	r.reportL2BatchStats()
	r.reportIOMMUTLBStats()
	r.reportMMUCoalescingStats()
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

type l2BatchStatProvider interface {
	Name() string
	L2BatchStats() writeback.L2BatchStats
}

func (r *Runner) reportL2BatchStats() {
	total := writeback.L2BatchStats{}
	for _, gpu := range r.platform.GPUs {
		for _, cache := range gpu.L2Caches {
			provider, ok := cache.(l2BatchStatProvider)
			if !ok {
				continue
			}
			stats := provider.L2BatchStats()
			if !l2BatchStatsNonZero(stats) {
				continue
			}
			r.collectL2BatchStats(provider.Name(), stats)
			total = combineL2BatchStats(total, stats)
		}
	}
	if l2BatchStatsNonZero(total) {
		r.collectL2BatchStats(r.platform.Driver.Name(), total)
	}
}

func l2BatchStatsNonZero(stats writeback.L2BatchStats) bool {
	return stats.DirBatchRequests+stats.AccessUnitReads+
		stats.AccessUnitCoalesced+stats.DRAMReadIssuedBytes+
		stats.DRAMReadUsefulBytes > 0
}

func combineL2BatchStats(
	a, b writeback.L2BatchStats,
) writeback.L2BatchStats {
	a.DirBatchGroups += b.DirBatchGroups
	a.DirBatchRequests += b.DirBatchRequests
	if b.DirMaxBatchSize > a.DirMaxBatchSize {
		a.DirMaxBatchSize = b.DirMaxBatchSize
	}
	a.AccessUnitReads += b.AccessUnitReads
	a.AccessUnitCoalesced += b.AccessUnitCoalesced
	a.DRAMReadIssuedBytes += b.DRAMReadIssuedBytes
	a.DRAMReadUsefulBytes += b.DRAMReadUsefulBytes
	return a
}

func (r *Runner) collectL2BatchStats(
	where string,
	stats writeback.L2BatchStats,
) {
	r.metricsCollector.Collect(
		where, "l2_batch_dir_groups", float64(stats.DirBatchGroups))
	r.metricsCollector.Collect(
		where, "l2_batch_dir_requests", float64(stats.DirBatchRequests))
	r.metricsCollector.Collect(
		where, "l2_batch_dir_max_size", float64(stats.DirMaxBatchSize))
	if stats.DirBatchGroups > 0 {
		avg := float64(stats.DirBatchRequests) /
			float64(stats.DirBatchGroups)
		r.metricsCollector.Collect(where, "l2_batch_dir_avg_size", avg)
	}
	r.metricsCollector.Collect(
		where, "l2_batch_access_unit_reads",
		float64(stats.AccessUnitReads))
	r.metricsCollector.Collect(
		where, "l2_batch_access_unit_coalesced",
		float64(stats.AccessUnitCoalesced))
	r.metricsCollector.Collect(
		where, "l2_batch_dram_read_issued_bytes",
		float64(stats.DRAMReadIssuedBytes))
	r.metricsCollector.Collect(
		where, "l2_batch_dram_read_useful_bytes",
		float64(stats.DRAMReadUsefulBytes))
	if stats.DRAMReadIssuedBytes > 0 {
		usefulRatio := float64(stats.DRAMReadUsefulBytes) /
			float64(stats.DRAMReadIssuedBytes)
		r.metricsCollector.Collect(
			where, "l2_batch_dram_read_useful_ratio", usefulRatio)

		overfetchBytes := uint64(0)
		if stats.DRAMReadIssuedBytes > stats.DRAMReadUsefulBytes {
			overfetchBytes = stats.DRAMReadIssuedBytes -
				stats.DRAMReadUsefulBytes
		}
		r.metricsCollector.Collect(
			where, "l2_batch_dram_read_overfetch_bytes",
			float64(overfetchBytes))
		overfetchPct := float64(overfetchBytes) /
			float64(stats.DRAMReadIssuedBytes) * 100
		r.metricsCollector.Collect(
			where, "l2_batch_dram_read_overfetch_pct", overfetchPct)
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
