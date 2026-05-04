package l2tlb

// DownstreamRequestCounts reports how many translation requests the L2 TLB
// issued downstream in total, to the local MMU, and to the IOMMU path.
func (tlb *L2TLB) DownstreamRequestCounts() (total, local, iommu int) {
	return tlb.downstreamReqCount, tlb.localReqCount, tlb.iommuReqCount
}
