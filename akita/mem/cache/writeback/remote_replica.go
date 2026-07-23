package writeback

import (
	cachepkg "github.com/sarchlab/akita/v3/mem/cache"
	"github.com/sarchlab/akita/v3/mem/mem"
	"github.com/sarchlab/akita/v3/mem/vm"
	"github.com/sarchlab/akita/v3/sim"
)

type remoteReplicaKey struct {
	pid  vm.PID
	line uint64
}

type remoteReplicaRecord struct {
	key        remoteReplicaKey
	used       bool
	hits       uint64
	patternKey *TypedFilterKey
}

// RemoteReplicaStats reports only the work performed by the requester-side
// remote replica path. All counters remain zero when the feature is disabled.
type RemoteReplicaStats struct {
	FilterQueries                  uint64
	FilterPositives                uint64
	FilterNegatives                uint64
	FilterFalsePositives           uint64
	FilterTruePositiveUnavailable  uint64
	CleanFills                     uint64
	TwoTouchFillAttempts           uint64
	InstalledFills                 uint64
	TwoTouchInstalledFills         uint64
	DroppedFills                   uint64
	TwoTouchDroppedFills           uint64
	FilterInsertFails              uint64
	TrackedEvictions               uint64
	UnusedTwoTouchRetirements      uint64
	ReplicaProbeHits               uint64
	TwoTouchReplicaHits            uint64
	UsefulTwoTouchFills            uint64
	FillIntoInvalid                uint64
	FillReplacedRemote             uint64
	FillDisplacedLocalClean        uint64
	TwoTouchLocalDisplacements     uint64
	LocalCleanProtectionDrops      uint64
	CurrentRemoteReplicas          uint64
	PeakRemoteReplicas             uint64
	UnusedPatternRetirements       uint64
	SpeculativeInvalidOnlyAttempts uint64
	SpeculativeInvalidOnlyDrops    uint64
}

// GetRemoteReplicaStats returns a snapshot of this L2 slice's counters.
func (c *Cache) GetRemoteReplicaStats() RemoteReplicaStats {
	return c.remoteReplicaStats
}

func (c *Cache) remoteReplicaMayContain(pid vm.PID, addr uint64) bool {
	c.remoteReplicaStats.FilterQueries++
	if c.remoteReplicaFilter == nil {
		return false
	}
	line := addr >> c.log2BlockSize << c.log2BlockSize
	if c.remoteReplicaFilter.Contains(pid, line) {
		c.remoteReplicaStats.FilterPositives++
		return true
	}
	return false
}

func (c *Cache) recordRemoteFillAttempt(fill *mem.RemoteDataFill) {
	c.remoteReplicaStats.CleanFills++
	c.remoteReplicaStats.TwoTouchFillAttempts++
}

func (c *Cache) recordRemoteFillDropped(fill *mem.RemoteDataFill) {
	c.remoteReplicaStats.DroppedFills++
	c.remoteReplicaStats.TwoTouchDroppedFills++
}

func (c *Cache) installRemoteDataFill(fill *mem.RemoteDataFill) bool {
	c.recordRemoteFillAttempt(fill)
	if c.remoteReplicaFilter == nil || fill == nil {
		c.recordRemoteFillDropped(fill)
		return false
	}
	if fill.Generation != c.remoteReplicaGeneration {
		c.recordRemoteFillDropped(fill)
		return false
	}

	lineBytes := uint64(1 << c.log2BlockSize)
	if fill.Address%lineBytes != 0 || uint64(len(fill.Data)) != lineBytes {
		c.recordRemoteFillDropped(fill)
		return false
	}

	line := fill.Address
	if c.mshr.Query(fill.PID, line) != nil {
		c.recordRemoteFillDropped(fill)
		return false
	}

	// Never let a late remote response overwrite a newer resident line.
	if c.directory.Lookup(fill.PID, line) != nil {
		c.recordRemoteFillDropped(fill)
		return false
	}

	block := c.directory.FindVictim(line)
	if block == nil || block.IsLocked || block.ReadCount > 0 ||
		(block.IsValid && block.IsDirty) {
		c.recordRemoteFillDropped(fill)
		return false
	}

	victimWasValid := block.IsValid
	oldRecord, replacedRemote := c.remoteReplicaBlocks[block]
	if fill.RequireInvalidVictim {
		c.remoteReplicaStats.SpeculativeInvalidOnlyAttempts++
		if victimWasValid {
			c.remoteReplicaStats.SpeculativeInvalidOnlyDrops++
			c.recordRemoteFillDropped(fill)
			return false
		}
	}
	// Remote clean fills are opportunistic. Never evict an ordinary local clean
	// line for one. Invalid ways and existing remote-clean lines remain eligible
	// under the benchmark-independent victim policy.
	if victimWasValid && !replacedRemote {
		c.remoteReplicaStats.LocalCleanProtectionDrops++
		c.recordRemoteFillDropped(fill)
		return false
	}
	if replacedRemote {
		delete(c.remoteReplicaBlocks, block)
	}
	c.untrackResidentBlock(block)
	if replacedRemote {
		c.recordRemoteReplicaRetirement(oldRecord)
		c.remoteReplicaStats.TrackedEvictions++
		c.remoteReplicaStats.FillReplacedRemote++
	} else {
		c.remoteReplicaStats.FillIntoInvalid++
	}
	data := append([]byte(nil), fill.Data...)
	if err := c.storage.Write(block.CacheAddress, data); err != nil {
		panic(err)
	}

	block.PID = fill.PID
	block.Tag = line
	block.IsValid = true
	block.IsDirty = false
	block.IsLocked = false
	block.DirtyMask = nil
	c.directory.Visit(block)
	c.trackResidentBlock(block)

	record := &remoteReplicaRecord{
		key: remoteReplicaKey{pid: fill.PID, line: line},
	}
	if fill.HasPattern {
		patternKey := TypedFilterKey{
			PID: fill.PID, Owner: fill.PatternOwner,
			Address: fill.PatternAddress, Type: FilterPattern,
		}
		record.patternKey = &patternKey
	}
	c.remoteReplicaBlocks[block] = record
	c.remoteReplicaStats.InstalledFills++
	c.remoteReplicaStats.TwoTouchInstalledFills++
	c.updateRemoteReplicaOccupancy()
	return true
}

func (c *Cache) remoteReplicaMatches(
	block *cachepkg.Block,
	pid vm.PID,
	line uint64,
) bool {
	if block == nil {
		return false
	}
	record := c.remoteReplicaBlocks[block]
	return record != nil && record.key.pid == pid && record.key.line == line
}

func (c *Cache) recordRemoteReplicaHit(
	block *cachepkg.Block,
	pid vm.PID,
	line uint64,
) bool {
	if !c.remoteReplicaMatches(block, pid, line) {
		return false
	}
	record := c.remoteReplicaBlocks[block]
	record.hits++
	c.remoteReplicaStats.ReplicaProbeHits++
	c.remoteReplicaStats.TwoTouchReplicaHits++
	if !record.used {
		c.remoteReplicaStats.UsefulTwoTouchFills++
	}
	record.used = true
	return true
}

func (c *Cache) recordRemoteReplicaRetirement(
	record *remoteReplicaRecord,
) {
	if record == nil || record.used {
		return
	}
	c.remoteReplicaStats.UnusedTwoTouchRetirements++
	if record.patternKey != nil && c.requestFilter != nil {
		c.requestFilter.ScheduleUpdate(
			c.Engine.CurrentTime(), *record.patternKey, true)
		c.remoteReplicaStats.UnusedPatternRetirements++
	}
}

func (c *Cache) updateRemoteReplicaOccupancy() {
	current := uint64(len(c.remoteReplicaBlocks))
	c.remoteReplicaStats.CurrentRemoteReplicas = current
	if current > c.remoteReplicaStats.PeakRemoteReplicas {
		c.remoteReplicaStats.PeakRemoteReplicas = current
	}
}

func (c *Cache) untrackRemoteReplica(block *cachepkg.Block) {
	if block == nil || c.remoteReplicaFilter == nil {
		return
	}
	record, ok := c.remoteReplicaBlocks[block]
	if !ok {
		return
	}
	c.recordRemoteReplicaRetirement(record)
	delete(c.remoteReplicaBlocks, block)
	c.remoteReplicaStats.TrackedEvictions++
	c.updateRemoteReplicaOccupancy()
}

func (c *Cache) resetRemoteReplicas() {
	for block := range c.remoteReplicaBlocks {
		c.untrackRemoteReplica(block)
	}
}

func (c *Cache) removeInflightTransaction(trans *transaction) {
	for i, candidate := range c.inFlightTransactions {
		if candidate != trans {
			continue
		}
		c.inFlightTransactions = append(
			c.inFlightTransactions[:i],
			c.inFlightTransactions[i+1:]...,
		)
		return
	}
	panic("transaction not found")
}

func (c *Cache) discardTopPort(now sim.VTimeInSec) {
	for {
		msg := c.topPort.Retrieve(now)
		if msg == nil {
			return
		}
		fill, ok := msg.(*mem.RemoteDataFill)
		if !ok {
			lookup, isLookup := msg.(*mem.ReadReq)
			if isLookup && lookup.LookupOnly {
				if !c.topSender.CanSend(1) {
					panic("remote lookup acknowledgement buffer is unexpectedly full")
				}
				rsp := mem.CacheLookupRspBuilder{}.
					WithSendTime(now).
					WithSrc(c.topPort).
					WithDst(lookup.Src).
					WithRspTo(lookup.ID).
					WithHit(false).
					WithGeneration(c.remoteReplicaGeneration).
					Build()
				c.topSender.Send(rsp)
			}
			continue
		}
		c.recordRemoteFillAttempt(fill)
		c.recordRemoteFillDropped(fill)
		if !c.topSender.CanSend(1) {
			panic("remote fill acknowledgement buffer is unexpectedly full")
		}
		rsp := mem.RemoteDataFillRspBuilder{}.
			WithSendTime(now).
			WithSrc(c.topPort).
			WithDst(fill.Src).
			WithRspTo(fill.ID).
			WithInstalled(false).
			Build()
		c.topSender.Send(rsp)
	}
}
