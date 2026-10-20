package writeback

import (
	memtrace "github.com/sarchlab/akita/v3/mem/trace"
	"github.com/sarchlab/akita/v3/sim"
)

const observationL2SampleEveryAccesses = uint64(256)

// recordObservationL2Utilization samples inside the L2 component's own event
// domain, avoiding cross-component directory reads in parallel simulation.
// The first accepted access and every 256th access thereafter are sampled.
func (c *Cache) recordObservationL2Utilization(now sim.VTimeInSec) {
	c.observationL2Accesses++
	if c.observationL2Accesses != 1 &&
		c.observationL2Accesses%observationL2SampleEveryAccesses != 0 {
		return
	}
	if !memtrace.ObservationRemoteTraceEnabled() {
		return
	}

	valid, total, dirty, locked := 0, 0, 0, 0
	for _, set := range c.directory.GetSets() {
		for _, block := range set.Blocks {
			total++
			if !block.IsValid {
				continue
			}
			valid++
			if block.IsDirty {
				dirty++
			}
			if block.IsLocked {
				locked++
			}
		}
	}
	memtrace.RecordObservationL2Sample(
		c.Name(), now, valid, total, dirty, locked,
		len(c.mshr.AllEntries()))
}
