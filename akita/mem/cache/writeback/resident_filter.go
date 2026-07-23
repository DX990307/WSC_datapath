package writeback

import (
	"github.com/sarchlab/akita/v3/mem/cache"
	"github.com/sarchlab/akita/v3/mem/vm"
	"github.com/sarchlab/akita/v3/sim"
)

type residentFilterKey struct {
	pid  vm.PID
	line uint64
}

// ResidentFilterStats describes the per-L2-slice Cuckoo Filter. A negative
// result is used only to skip the tag-lookup pipeline; allocation, MSHR
// merging, fills, and write combining remain in the L2 cache.
type ResidentFilterStats struct {
	Enabled                bool
	Reliable               bool
	Queries                uint64
	Positives              uint64
	Negatives              uint64
	ReadNegativeBypasses   uint64
	ReadPositiveFastPaths  uint64
	ReadBusyFallbacks      uint64
	ReadNegativeMSHRMerges uint64
	ReadParallelMSHRMerges uint64
	PrimedLookups          uint64
	WriteNegativeBypasses  uint64
	WriteFullLineBypasses  uint64
	WritePartialBypasses   uint64
	FalsePositives         uint64
	InsertFailures         uint64
	MSHRFullStalls         uint64
}

// primeResidentLookup starts the metadata access when a request enters the L2
// front end. With the paper's one-cycle Filter, the result is ready when the
// directory stage next examines the request, so a possible resident line does
// not pay Filter latency in front of the normal tag pipeline.
func (c *Cache) primeResidentLookup(
	now sim.VTimeInSec,
	trans *transaction,
) {
	if !c.residentFilterEnabled || trans == nil ||
		trans.residentFilterChecked || trans.residentFilterLookup != nil {
		return
	}
	req := trans.accessReq()
	if req == nil || trans.read != nil && trans.read.LookupOnly {
		return
	}
	line, _ := getCacheLineID(req.GetAddress(), c.log2BlockSize)
	// Avoid consuming a Filter port for an already-authoritative follower.
	// The directory stage rechecks the MSHR to cover races after this point.
	if c.mshr.Query(req.GetPID(), line) != nil {
		return
	}
	lookup, accepted := c.startResidentLookup(
		now, req.GetPID(), line)
	if !accepted || lookup == nil {
		return
	}
	trans.residentFilterLookup = lookup
	c.residentFilterStats.PrimedLookups++
}

// GetResidentFilterStats returns a snapshot of the local resident filter.
func (c *Cache) GetResidentFilterStats() ResidentFilterStats {
	stats := c.residentFilterStats
	stats.Enabled = c.residentFilterEnabled
	stats.Reliable = c.residentFilterReliable
	return stats
}

// residentMayContain returns true whenever the filter cannot safely prove a
// miss. This fail-open rule prevents an insertion failure from creating a
// false-negative cache lookup.
func (c *Cache) residentMayContain(pid vm.PID, line uint64) bool {
	if !c.residentFilterEnabled || c.requestFilter == nil ||
		!c.residentFilterReliable {
		return true
	}
	return c.recordResidentLookupResult(c.requestFilter.Query(TypedFilterKey{
		PID: pid, Address: line, Type: FilterResident,
	}))
}

func (c *Cache) recordResidentLookupResult(
	possible bool,
	reliable bool,
) bool {
	if !reliable {
		// A same-key update may still be traversing the modeled update port.
		// That lookup fails open for this request but must not permanently
		// disable RESIDENT. Only an actual class insertion failure does so.
		if c.requestFilter == nil ||
			!c.requestFilter.Stats().ByType[FilterResident].Reliable {
			c.residentFilterReliable = false
		}
		return true
	}
	c.residentFilterStats.Queries++
	if possible {
		c.residentFilterStats.Positives++
		return true
	}
	c.residentFilterStats.Negatives++
	return false
}

func (c *Cache) startResidentLookup(
	now sim.VTimeInSec,
	pid vm.PID,
	line uint64,
) (*TypedFilterLookup, bool) {
	if c.requestFilter == nil ||
		c.requestFilter.Mode() == TypedFilterDisabled ||
		!c.residentFilterReliable {
		return nil, true
	}
	lookup, accepted := c.requestFilter.StartLookup(now, TypedFilterKey{
		PID: pid, Address: line, Type: FilterResident,
	})
	if !accepted {
		c.TickLater(now)
		return nil, false
	}
	return &lookup, true
}

func (c *Cache) completeResidentLookup(
	now sim.VTimeInSec,
	lookup *TypedFilterLookup,
) (possible bool, reliable bool, ready bool) {
	if lookup == nil || c.requestFilter == nil || !c.residentFilterReliable {
		return true, false, true
	}
	possible, reliable, ready = c.requestFilter.CompleteLookup(now, *lookup)
	if !ready {
		c.TickLater(now)
		return false, reliable, false
	}
	return c.recordResidentLookupResult(possible, reliable), reliable, true
}

func (c *Cache) trackResidentBlock(block *cache.Block) {
	if c.residentFilter == nil || block == nil ||
		!block.IsValid || block.IsLocked {
		return
	}

	newKey := residentFilterKey{pid: block.PID, line: block.Tag}
	if oldKey, ok := c.residentFilterBlocks[block]; ok {
		if oldKey == newKey {
			return
		}
		c.scheduleResidentUpdate(oldKey, true)
	}

	if !c.scheduleResidentUpdate(newKey, false) {
		c.residentFilterReliable = false
		c.residentFilterStats.InsertFailures++
		delete(c.residentFilterBlocks, block)
		return
	}
	c.residentFilterBlocks[block] = newKey
}

func (c *Cache) untrackResidentBlock(block *cache.Block) {
	c.retireUnusedLocalPrefetch(block)
	c.retireUnusedGranularitySibling(block)
	if c.residentFilter == nil || block == nil {
		return
	}
	oldKey, ok := c.residentFilterBlocks[block]
	if !ok {
		return
	}
	c.scheduleResidentUpdate(oldKey, true)
	delete(c.residentFilterBlocks, block)
}

func (c *Cache) scheduleResidentUpdate(
	key residentFilterKey,
	deleteKey bool,
) bool {
	if c.requestFilter == nil {
		return false
	}
	typedKey := TypedFilterKey{
		PID: key.pid, Address: key.line, Type: FilterResident,
	}
	if c.requestFilter.Mode() == TypedFilterDisabled {
		return true
	}
	return c.requestFilter.ScheduleUpdate(
		c.Engine.CurrentTime(), typedKey, deleteKey)
}

func (c *Cache) resetResidentFilter() {
	if c.residentFilter == nil {
		return
	}
	c.residentFilter.Reset()
	clear(c.residentFilterBlocks)
	c.residentFilterReliable = true
}
