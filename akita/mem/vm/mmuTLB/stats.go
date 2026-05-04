package mmuTLB

// IncomingRequestCount reports how many translation requests entered the
// IOMMU-side TLB from the GMMU path.
func (tlb *TLB) IncomingRequestCount() int {
	return tlb.incomingReqCount
}

// DownstreamRequestCount reports how many translation requests the IOMMU-side
// TLB sent toward MMUCache/MMU.
func (tlb *TLB) DownstreamRequestCount() int {
	return tlb.downstreamReqCount
}

// LookupLatencyCycles reports the fixed MMUTLB/IOTLB lookup delay applied to
// each buffered request before tag lookup/hit-miss handling proceeds.
func (tlb *TLB) LookupLatencyCycles() int {
	return tlb.lookupLatencyCycles
}
