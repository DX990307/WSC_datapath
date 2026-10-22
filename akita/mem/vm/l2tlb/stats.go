package l2tlb

// DownstreamRequestCounts reports how many translation requests the L2 TLB
// issued downstream in total, to the local MMU, and to the IOMMU path.
func (tlb *L2TLB) DownstreamRequestCounts() (total, local, iommu int) {
	return tlb.downstreamReqCount, tlb.localReqCount, tlb.iommuReqCount
}

// IOMMUSendStats reports how many sends to the IOMMU failed because the
// connection was full, and how many times such a failure was ended by the
// connection's NotifyAvailable.
func (tlb *L2TLB) IOMMUSendStats() (failed, wakeups uint64) {
	return tlb.iommuSendFailCount, tlb.iommuWakeupCount
}

// MissLatencyStats reports, for misses served by the local GMMU (index 0) and
// by the IOMMU (index 1): the summed round trip of the downstream request and
// how many there were, and the summed latency of every request that waited on
// such a miss, from its arrival in the TopPort to its response, and how many.
func (tlb *L2TLB) MissLatencyStats() (rttSum [2]float64, rttCount [2]uint64,
	reqSum [2]float64, reqCount [2]uint64,
) {
	return tlb.missRTTSum, tlb.missRTTCount, tlb.missReqSum, tlb.missReqCount
}
