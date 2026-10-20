package writeback

import (
	"math"
	"testing"

	"github.com/sarchlab/akita/v3/mem/mem"
	"github.com/sarchlab/akita/v3/sim"
)

func TestDemandReadCompletionLatencyExcludesSpeculationAndLookupOnly(t *testing.T) {
	cache := &Cache{}
	read := mem.ReadReqBuilder{}.
		WithAddress(0x1000).
		WithByteSize(64).
		Build()
	demand := &transaction{read: read, l2Arrival: sim.VTimeInSec(2e-9)}
	cache.recordDemandReadCompletion(sim.VTimeInSec(7e-9), demand)

	prefetch := &transaction{
		read: read, prefetch: true, l2Arrival: sim.VTimeInSec(2e-9),
	}
	cache.recordDemandReadCompletion(sim.VTimeInSec(9e-9), prefetch)
	lookup := mem.ReadReqBuilder{}.
		WithAddress(0x2000).
		WithByteSize(64).
		WithLookupOnly().
		Build()
	cache.recordDemandReadCompletion(sim.VTimeInSec(9e-9), &transaction{
		read: lookup, l2Arrival: sim.VTimeInSec(2e-9),
	})

	stats := cache.GetLocalMemoryPathStats()
	if stats.DemandReadLatencySamples != 1 ||
		math.Abs(stats.DemandReadLatencyTotalNS-5) > 1e-9 ||
		math.Abs(stats.DemandReadLatencyMaxNS-5) > 1e-9 {
		t.Fatalf("demand latency accounting = %+v", stats)
	}
}
