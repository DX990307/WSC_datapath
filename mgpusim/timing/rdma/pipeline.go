package rdma

// PipelineStats reports the configured RDMA service limits and the pressure
// observed at runtime. The limits apply to the baseline and all remote-data
// mechanism ablations.
type PipelineStats struct {
	Width                          int
	LatencyCycles                  int
	MaxOutstanding                 int
	PeakOutstanding                int
	PipelineWaitCycles             uint64
	OutstandingFullStalls          uint64
	RequesterPeakOutstanding       int
	OwnerPeakOutstanding           int
	RequesterOutstandingFullStalls uint64
	OwnerOutstandingFullStalls     uint64
}

// GetPipelineStats returns the current RDMA pipeline configuration and
// pressure counters.
func (c *Comp) GetPipelineStats() PipelineStats {
	peak := c.peakRequesterOutstanding
	if c.peakOwnerOutstanding > peak {
		peak = c.peakOwnerOutstanding
	}
	return PipelineStats{
		Width:                          c.pipelineWidth,
		LatencyCycles:                  c.pipelineLatency,
		MaxOutstanding:                 c.maxOutstanding,
		PeakOutstanding:                peak,
		PipelineWaitCycles:             c.pipelineWaitCycles,
		OutstandingFullStalls:          c.requesterFullStalls + c.ownerFullStalls,
		RequesterPeakOutstanding:       c.peakRequesterOutstanding,
		OwnerPeakOutstanding:           c.peakOwnerOutstanding,
		RequesterOutstandingFullStalls: c.requesterFullStalls,
		OwnerOutstandingFullStalls:     c.ownerFullStalls,
	}
}
