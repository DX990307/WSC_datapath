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
	key      remoteReplicaKey
	prefetch bool
	used     bool
	hits     uint64
}

// RemoteReplicaStats reports only the work performed by the requester-side
// remote replica path. All counters remain zero when the feature is disabled.
type RemoteReplicaStats struct {
	FilterQueries                 uint64
	FilterPositives               uint64
	FilterNegatives               uint64
	FilterFalsePositives          uint64
	FilterTruePositiveUnavailable uint64
	CleanFills                    uint64
	TwoTouchFillAttempts          uint64
	PrefetchFillAttempts          uint64
	InstalledFills                uint64
	TwoTouchInstalledFills        uint64
	PrefetchInstalledFills        uint64
	DroppedFills                  uint64
	TwoTouchDroppedFills          uint64
	PrefetchDroppedFills          uint64
	FilterInsertFails             uint64
	TrackedEvictions              uint64
	UnusedTwoTouchRetirements     uint64
	UnusedPrefetchRetirements     uint64
	ReplicaProbeHits              uint64
	TwoTouchReplicaHits           uint64
	PrefetchReplicaHits           uint64
	UsefulTwoTouchFills           uint64
	UsefulPrefetchFills           uint64
	FillIntoInvalid               uint64
	FillReplacedRemote            uint64
	FillDisplacedLocalClean       uint64
	TwoTouchLocalDisplacements    uint64
	PrefetchLocalDisplacements    uint64
	CurrentRemoteReplicas         uint64
	PeakRemoteReplicas            uint64
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
	if fill != nil && fill.Prefetch {
		c.remoteReplicaStats.PrefetchFillAttempts++
	} else {
		c.remoteReplicaStats.TwoTouchFillAttempts++
	}
}

func (c *Cache) recordRemoteFillDropped(fill *mem.RemoteDataFill) {
	c.remoteReplicaStats.DroppedFills++
	if fill != nil && fill.Prefetch {
		c.remoteReplicaStats.PrefetchDroppedFills++
	} else {
		c.remoteReplicaStats.TwoTouchDroppedFills++
	}
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
	if replacedRemote {
		c.remoteReplicaFilter.Delete(oldRecord.key.pid, oldRecord.key.line)
		delete(c.remoteReplicaBlocks, block)
	}
	if !c.remoteReplicaFilter.Insert(fill.PID, line) {
		if replacedRemote {
			if !c.remoteReplicaFilter.Insert(
				oldRecord.key.pid, oldRecord.key.line,
			) {
				panic("failed to restore remote replica filter entry")
			}
			c.remoteReplicaBlocks[block] = oldRecord
		}
		c.remoteReplicaStats.FilterInsertFails++
		c.recordRemoteFillDropped(fill)
		return false
	}
	if replacedRemote {
		c.recordRemoteReplicaRetirement(oldRecord)
		c.remoteReplicaStats.TrackedEvictions++
		c.remoteReplicaStats.FillReplacedRemote++
	} else if victimWasValid {
		c.remoteReplicaStats.FillDisplacedLocalClean++
		if fill.Prefetch {
			c.remoteReplicaStats.PrefetchLocalDisplacements++
		} else {
			c.remoteReplicaStats.TwoTouchLocalDisplacements++
		}
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

	c.remoteReplicaBlocks[block] = &remoteReplicaRecord{
		key:      remoteReplicaKey{pid: fill.PID, line: line},
		prefetch: fill.Prefetch,
	}
	c.remoteReplicaStats.InstalledFills++
	if fill.Prefetch {
		c.remoteReplicaStats.PrefetchInstalledFills++
	} else {
		c.remoteReplicaStats.TwoTouchInstalledFills++
	}
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
	if record.prefetch {
		c.remoteReplicaStats.PrefetchReplicaHits++
		if !record.used {
			c.remoteReplicaStats.UsefulPrefetchFills++
		}
	} else {
		c.remoteReplicaStats.TwoTouchReplicaHits++
		if !record.used {
			c.remoteReplicaStats.UsefulTwoTouchFills++
		}
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
	if record.prefetch {
		c.remoteReplicaStats.UnusedPrefetchRetirements++
	} else {
		c.remoteReplicaStats.UnusedTwoTouchRetirements++
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
	c.remoteReplicaFilter.Delete(record.key.pid, record.key.line)
	c.recordRemoteReplicaRetirement(record)
	delete(c.remoteReplicaBlocks, block)
	c.remoteReplicaStats.TrackedEvictions++
	c.updateRemoteReplicaOccupancy()
}

func (c *Cache) resetRemoteReplicas() {
	for block := range c.remoteReplicaBlocks {
		c.untrackRemoteReplica(block)
	}
	if c.remoteReplicaFilter != nil {
		c.remoteReplicaFilter.Reset()
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
