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
