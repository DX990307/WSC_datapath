package writearound

import (
	memtrace "github.com/sarchlab/akita/v3/mem/trace"
	"github.com/sarchlab/akita/v3/mem/vm"
	"github.com/sarchlab/akita/v3/sim"
)

// M1Config controls the L1V post-coalescer reorder/batch helper.
type M1Config struct {
	L1VBatchEnabled bool
	L1VBatchEntries int
	L1VBatchLines   int
	L1VBatchWaitNS  uint64
	L1VWindowLines  int

	L1VAdaptiveEnabled           bool
	L1VAdaptiveBadDrainThreshold int
	L1VAdaptiveCooldownNS        uint64
}

// M1Stats reports whether the L1V helper found useful queueing opportunities.
type M1Stats struct {
	L1VBatchEnabled bool

	L1VRequestsSeen      uint64
	L1VBatchableReads    uint64
	L1VBypassRequests    uint64
	L1VBatchesCreated    uint64
	L1VBatchesDrained    uint64
	L1VLinesInBatches    uint64
	L1VRequestsInBatches uint64
	L1VDuplicateWaiters  uint64
	L1VFullDrains        uint64
	L1VTimeoutDrains     uint64
	L1VCapacityDrains    uint64
	L1VManualDrains      uint64
	L1VMaxLinesPerBatch  uint64
	L1VTotalWaitNS       float64
	L1VWaitSamples       uint64

	L1VAdaptiveEnabled        bool
	L1VAdaptiveBypassRequests uint64
	L1VAdaptiveDisableEvents  uint64
	L1VAdaptiveBadDrains      uint64
	L1VAdaptiveGoodDrains     uint64
}

type m1BatchKey struct {
	pid           vm.PID
	lowModuleName string
	windowID      uint64
}

type m1BatchEntry struct {
	id            uint64
	key           m1BatchKey
	oldestArrival sim.VTimeInSec
	newestArrival sim.VTimeInSec
	lineSet       map[uint64]bool
	transactions  []*transaction
	requestCount  uint64
}

const (
	m1DrainFull = iota
	m1DrainTimeout
	m1DrainCapacity
	m1DrainManual
)

func normalizeM1Config(c M1Config) M1Config {
	if c.L1VBatchEntries <= 0 {
		c.L1VBatchEntries = 32
	}
	if c.L1VBatchLines <= 0 {
		c.L1VBatchLines = 2
	}
	if c.L1VBatchWaitNS == 0 {
		c.L1VBatchWaitNS = 10
	}
	if c.L1VWindowLines <= 0 {
		c.L1VWindowLines = 2
	}
	if c.L1VBatchLines > c.L1VWindowLines {
		c.L1VBatchLines = c.L1VWindowLines
	}
	if c.L1VAdaptiveBadDrainThreshold <= 0 {
		c.L1VAdaptiveBadDrainThreshold = 4
	}
	if c.L1VAdaptiveCooldownNS == 0 {
		c.L1VAdaptiveCooldownNS = 200
	}
	return c
}

// ConfigureM1 configures the L1V M1 batch helper.
func (c *Cache) ConfigureM1(config M1Config) {
	c.m1Config = normalizeM1Config(config)
	c.m1Stats.L1VBatchEnabled = c.m1Config.L1VBatchEnabled
	c.m1Stats.L1VAdaptiveEnabled = c.m1Config.L1VAdaptiveEnabled
}

// GetM1Stats returns a copy of the L1V M1 counters.
func (c *Cache) GetM1Stats() M1Stats {
	return c.m1Stats
}

func (c *Cache) m1Enabled() bool {
	return c.m1Config.L1VBatchEnabled
}

func (c *Cache) ensureM1State() {
	if c.m1Batches == nil {
		c.m1Batches = make(map[m1BatchKey]*m1BatchEntry)
	}
}

func (c *Cache) resetM1Batches() {
	c.m1Batches = nil
	c.m1BatchOrder = nil
	c.m1BadDrainRun = 0
	c.m1BypassUntil = 0
}

func (c *Cache) m1ReadBatchable(trans *transaction) bool {
	if trans == nil || trans.read == nil {
		return false
	}
	lineBytes := uint64(1 << c.log2BlockSize)
	if trans.read.AccessByteSize == 0 || trans.read.AccessByteSize > lineBytes {
		return false
	}
	return true
}

func (c *Cache) m1AdaptiveBypass(now sim.VTimeInSec) bool {
	return c.m1Config.L1VAdaptiveEnabled && now < c.m1BypassUntil
}

func (c *Cache) enqueueM1Batch(now sim.VTimeInSec, trans *transaction) bool {
	if !c.m1ReadBatchable(trans) {
		return false
	}

	c.ensureM1State()
	key := c.m1Key(trans)
	entry := c.m1Batches[key]
	if entry == nil {
		for len(c.m1BatchOrder) >= c.m1Config.L1VBatchEntries {
			if !c.processM1Batches(now, true, m1DrainCapacity) {
				return false
			}
		}
		entry = &m1BatchEntry{
			id:            c.m1NextBatchID,
			key:           key,
			oldestArrival: now,
			newestArrival: now,
			lineSet:       make(map[uint64]bool),
		}
		c.m1NextBatchID++
		c.m1Batches[key] = entry
		c.m1BatchOrder = append(c.m1BatchOrder, key)
		c.m1Stats.L1VBatchesCreated++
	}

	lineID := c.m1LineID(trans)
	if entry.lineSet[lineID] {
		c.m1Stats.L1VDuplicateWaiters++
	}
	entry.lineSet[lineID] = true
	entry.transactions = append(entry.transactions, trans)
	entry.requestCount++
	entry.newestArrival = now
	trans.m1BatchArrival = now
	trans.m1BatchID = entry.id

	c.m1Stats.L1VBatchableReads++
	if len(entry.lineSet) >= c.m1Config.L1VBatchLines {
		_ = c.processM1Batches(now, true, m1DrainFull)
	}
	return true
}

func (c *Cache) m1Key(trans *transaction) m1BatchKey {
	lineBytes := uint64(1 << c.log2BlockSize)
	windowBytes := lineBytes * uint64(c.m1Config.L1VWindowLines)
	if windowBytes == 0 {
		windowBytes = lineBytes
	}
	lineID := c.m1LineID(trans)
	lowModule := c.lowModuleFinder.Find(lineID)
	lowModuleName := ""
	if lowModule != nil {
		lowModuleName = lowModule.Name()
	}
	return m1BatchKey{
		pid:           trans.PID(),
		lowModuleName: lowModuleName,
		windowID:      lineID / windowBytes,
	}
}

func (c *Cache) m1LineID(trans *transaction) uint64 {
	lineBytes := uint64(1 << c.log2BlockSize)
	return trans.Address() / lineBytes * lineBytes
}

func (c *Cache) processM1Batches(
	now sim.VTimeInSec,
	force bool,
	forceReason int,
) bool {
	if !c.m1Enabled() || len(c.m1BatchOrder) == 0 {
		return false
	}
	if !c.dirBuf.CanPush() {
		return false
	}

	index, reason, ok := c.selectM1Batch(now, force, forceReason)
	if !ok {
		return false
	}
	key := c.m1BatchOrder[index]
	entry := c.m1Batches[key]
	if entry == nil || len(entry.transactions) == 0 {
		c.removeM1BatchAt(index)
		return false
	}

	trans := entry.transactions[0]
	entry.transactions = entry.transactions[1:]
	memtrace.RecordMemoryPathL1VBatchWait(
		c.Name(), trans.id, trans.m1BatchArrival, now)
	c.dirBuf.Push(trans)

	if len(entry.transactions) == 0 {
		c.recordM1Drain(now, entry, reason)
		delete(c.m1Batches, key)
		c.m1BatchOrder = append(
			c.m1BatchOrder[:index],
			c.m1BatchOrder[index+1:]...)
	}
	return true
}

func (c *Cache) selectM1Batch(
	now sim.VTimeInSec,
	force bool,
	forceReason int,
) (int, int, bool) {
	selected := -1
	reason := forceReason
	for i, key := range c.m1BatchOrder {
		entry := c.m1Batches[key]
		if entry == nil {
			continue
		}
		if len(entry.lineSet) >= c.m1Config.L1VBatchLines {
			return i, m1DrainFull, true
		}
		if c.m1Expired(now, entry.oldestArrival, c.m1Config.L1VBatchWaitNS) {
			return i, m1DrainTimeout, true
		}
		if selected < 0 {
			selected = i
		}
	}
	if force && selected >= 0 {
		return selected, reason, true
	}
	return 0, 0, false
}

func (c *Cache) recordM1Drain(
	now sim.VTimeInSec,
	entry *m1BatchEntry,
	reason int,
) {
	c.m1Stats.L1VBatchesDrained++
	c.m1Stats.L1VLinesInBatches += uint64(len(entry.lineSet))
	c.m1Stats.L1VRequestsInBatches += entry.requestCount
	if uint64(len(entry.lineSet)) > c.m1Stats.L1VMaxLinesPerBatch {
		c.m1Stats.L1VMaxLinesPerBatch = uint64(len(entry.lineSet))
	}
	c.m1Stats.L1VTotalWaitNS += m1VTimeToNS(now - entry.oldestArrival)
	c.m1Stats.L1VWaitSamples++

	switch reason {
	case m1DrainFull:
		c.m1Stats.L1VFullDrains++
	case m1DrainTimeout:
		c.m1Stats.L1VTimeoutDrains++
	case m1DrainCapacity:
		c.m1Stats.L1VCapacityDrains++
	default:
		c.m1Stats.L1VManualDrains++
	}

	c.updateM1AdaptiveState(now, len(entry.lineSet), reason)
}

func (c *Cache) updateM1AdaptiveState(
	now sim.VTimeInSec,
	lines int,
	reason int,
) {
	if !c.m1Config.L1VAdaptiveEnabled {
		return
	}

	if reason == m1DrainFull && lines >= c.m1Config.L1VBatchLines {
		c.m1Stats.L1VAdaptiveGoodDrains++
		c.m1BadDrainRun = 0
		return
	}

	if lines > 1 {
		c.m1BadDrainRun = 0
		return
	}

	c.m1Stats.L1VAdaptiveBadDrains++
	c.m1BadDrainRun++
	if c.m1BadDrainRun < c.m1Config.L1VAdaptiveBadDrainThreshold {
		return
	}

	c.m1Stats.L1VAdaptiveDisableEvents++
	c.m1BypassUntil = now + sim.VTimeInSec(
		float64(c.m1Config.L1VAdaptiveCooldownNS)*1e-9)
	c.m1BadDrainRun = 0
}

func (c *Cache) removeM1BatchAt(index int) {
	key := c.m1BatchOrder[index]
	delete(c.m1Batches, key)
	c.m1BatchOrder = append(c.m1BatchOrder[:index], c.m1BatchOrder[index+1:]...)
}

func (c *Cache) m1Expired(
	now, arrival sim.VTimeInSec,
	waitNS uint64,
) bool {
	if waitNS == 0 {
		return true
	}
	return now-arrival >= sim.VTimeInSec(float64(waitNS)*1e-9)
}

func m1VTimeToNS(v sim.VTimeInSec) float64 {
	return float64(v) * 1e9
}
