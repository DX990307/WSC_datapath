package writeback

import (
	"github.com/sarchlab/akita/v3/sim"
)

// LocalMemoryPathStats reports the ordinary 64-byte L2-to-DRAM path. CuPath
// never widens a cache-line request; optional prefetches remain independent
// 64-byte requests and are classified separately by the M1 reporter.
type LocalMemoryPathStats struct {
	DRAMReadRequests         uint64
	MissToDRAMIssueSamples   uint64
	MissToDRAMIssueTotalNS   float64
	MissToDRAMIssueMaxNS     float64
	FastMissIssueSamples     uint64
	FastMissIssueTotalNS     float64
	FastMissIssueMaxNS       float64
	DemandReadLatencySamples uint64
	DemandReadLatencyTotalNS float64
	DemandReadLatencyMaxNS   float64
}

// FillForwardingStats reports best-effort demand response forwarding at the
// point where a local DRAM fill returns. The L2 bank fill still always occurs.
type FillForwardingStats struct {
	Enabled              bool
	EligibleReadEntries  uint64
	ForwardedReadEntries uint64
	ForwardedReads       uint64
	BufferFallbacks      uint64
}

// GetLocalMemoryPathStats returns a snapshot of the local 64-byte read path.
func (c *Cache) GetLocalMemoryPathStats() LocalMemoryPathStats {
	return c.localMemoryPathStats
}

func (c *Cache) recordDemandReadCompletion(
	now sim.VTimeInSec,
	trans *transaction,
) {
	if trans == nil || trans.read == nil || trans.prefetch ||
		trans.read.LookupOnly || now < trans.l2Arrival {
		return
	}
	latencyNS := float64(now-trans.l2Arrival) * 1e9
	stats := &c.localMemoryPathStats
	stats.DemandReadLatencySamples++
	stats.DemandReadLatencyTotalNS += latencyNS
	if latencyNS > stats.DemandReadLatencyMaxNS {
		stats.DemandReadLatencyMaxNS = latencyNS
	}
}

// GetFillForwardingStats returns local fill-and-forward counters.
func (c *Cache) GetFillForwardingStats() FillForwardingStats {
	stats := c.fillForwardingStats
	stats.Enabled = c.fillForwarding
	return stats
}
