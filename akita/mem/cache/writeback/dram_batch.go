package writeback

import (
	"github.com/sarchlab/akita/v3/mem/mem"
	"github.com/sarchlab/akita/v3/mem/vm"
	"github.com/sarchlab/akita/v3/sim"
	"github.com/sarchlab/akita/v3/tracing"
)

// DRAMBatchConfig controls batching after a read has missed in the L2.
//
// With the default 64-byte cache line, WindowLines=2 groups requests in an
// aligned 128-byte window. At most two adjacent cache lines are combined into
// one 128-byte request to the low module. MaxWaitNS=0 requests an immediate
// timeout drain.
type DRAMBatchConfig struct {
	Enabled     bool
	MaxEntries  int
	MaxLines    int
	MaxWaitNS   uint64
	WindowLines int
}

// DRAMBatchStats reports batching opportunities and their cost.
type DRAMBatchStats struct {
	Enabled bool

	MissLinesSeen       uint64
	BatchesCreated      uint64
	BatchesDrained      uint64
	LinesInBatches      uint64
	SingletonFallbacks  uint64
	FullDrains          uint64
	TimeoutDrains       uint64
	CapacityDrains      uint64
	DrainDrains         uint64
	MaxLinesPerBatch    uint64
	TotalWaitNS         float64
	WaitSamples         uint64
	MultiLineReads      uint64
	SingleLineReads     uint64
	AdapterObservations uint64
	AdapterUseful       uint64
	AdapterPredictions  uint64
	AdapterInflightHits uint64
	AdapterBufferHits   uint64
	AdapterUnused       uint64
	AdapterConfidence   uint64
}

type dramBatchKey struct {
	pid           vm.PID
	lowModuleName string
	windowID      uint64
}

type dramBatchEntry struct {
	key           dramBatchKey
	lowModule     sim.Port
	oldestArrival sim.VTimeInSec
	lineSet       map[uint64]struct{}
	transactions  []*transaction
}

type dramBatchDrainReason int

const (
	dramBatchDrainFull dramBatchDrainReason = iota
	dramBatchDrainTimeout
	dramBatchDrainCapacity
	dramBatchDrainManual
)

func normalizeDRAMBatchConfig(config DRAMBatchConfig) DRAMBatchConfig {
	if config.MaxEntries <= 0 {
		config.MaxEntries = 16
	}
	if config.MaxLines <= 0 {
		config.MaxLines = 2
	}
	// This mechanism deliberately supports only one or two 64-byte lines.
	if config.MaxLines > 2 {
		config.MaxLines = 2
	}
	if config.WindowLines <= 0 {
		config.WindowLines = 2
	}
	// A window wider than two lines would no longer be the intended 128-byte
	// window and could collect non-adjacent lines.
	if config.WindowLines > 2 {
		config.WindowLines = 2
	}
	if config.MaxLines > config.WindowLines {
		config.MaxLines = config.WindowLines
	}
	return config
}

// ConfigureDRAMBatch configures confirmed-L2-miss DRAM batching.
func (c *Cache) ConfigureDRAMBatch(config DRAMBatchConfig) {
	c.dramBatchConfig = normalizeDRAMBatchConfig(config)
	c.dramBatchStats.Enabled = c.dramBatchConfig.Enabled
	if c.dramBatchConfig.Enabled {
		c.dramAdapter = newDRAMPrefetchAdapter(c.dramBatchConfig.MaxEntries)
	} else {
		c.dramAdapter = nil
		c.resetDRAMBatchState()
	}
}

// GetDRAMBatchStats returns a copy of the DRAM batching counters.
func (c *Cache) GetDRAMBatchStats() DRAMBatchStats {
	return c.dramBatchStats
}

func (c *Cache) dramBatchEnabled() bool {
	return c.dramBatchConfig.Enabled
}

func (c *Cache) dramBatchHasEntries() bool {
	return len(c.dramBatchOrder) > 0
}

func (c *Cache) dramBatchFetchBatchable(trans *transaction) bool {
	return trans != nil &&
		trans.action == writeBufferFetch &&
		trans.read != nil &&
		trans.fetchReadReq == nil
}

func (c *Cache) ensureDRAMBatchState() {
	if c.dramBatches == nil {
		c.dramBatches = make(map[dramBatchKey]*dramBatchEntry)
	}
}

func (c *Cache) enqueueDRAMBatchMiss(
	now sim.VTimeInSec,
	trans *transaction,
) bool {
	c.ensureDRAMBatchState()
	key, lowModule := c.dramBatchKeyFor(trans)
	entry := c.dramBatches[key]
	if entry == nil {
		for len(c.dramBatchOrder) >= c.dramBatchConfig.MaxEntries {
			if !c.processDRAMBatches(now, true, dramBatchDrainCapacity) {
				return false
			}
		}

		entry = &dramBatchEntry{
			key:           key,
			lowModule:     lowModule,
			oldestArrival: now,
			lineSet:       make(map[uint64]struct{}),
		}
		c.dramBatches[key] = entry
		c.dramBatchOrder = append(c.dramBatchOrder, key)
		c.dramBatchStats.BatchesCreated++
	}

	entry.lineSet[trans.fetchAddress] = struct{}{}
	entry.transactions = append(entry.transactions, trans)
	c.dramBatchStats.MissLinesSeen++

	if len(entry.lineSet) >= c.dramBatchConfig.MaxLines {
		_ = c.processDRAMBatches(now, true, dramBatchDrainFull)
	}

	return true
}

func (c *Cache) dramBatchKeyFor(
	trans *transaction,
) (dramBatchKey, sim.Port) {
	return c.dramBatchKeyForAddress(trans.fetchPID, trans.fetchAddress)
}

func (c *Cache) dramBatchKeyForAddress(
	pid vm.PID,
	address uint64,
) (dramBatchKey, sim.Port) {
	lineBytes := uint64(1) << c.log2BlockSize
	windowBytes := lineBytes * uint64(c.dramBatchConfig.WindowLines)
	lowModule := c.lowModuleFinder.Find(address)
	lowModuleName := ""
	if lowModule != nil {
		lowModuleName = lowModule.Name()
	}

	return dramBatchKey{
		pid:           pid,
		lowModuleName: lowModuleName,
		windowID:      address / windowBytes,
	}, lowModule
}

func (c *Cache) findConflictingDRAMBatch(
	trans *transaction,
) (int, bool) {
	if trans == nil || !c.dramBatchHasEntries() {
		return 0, false
	}

	if req := trans.accessReq(); req != nil {
		if index, ok := c.findDRAMBatchByAddress(
			req.GetPID(), req.GetAddress()); ok {
			return index, true
		}
	}

	switch trans.action {
	case writeBufferEvictAndFetch,
		writeBufferEvictAndWrite,
		writeBufferFlush:
		return c.findDRAMBatchByAddress(
			trans.evictingPID, trans.evictingAddr)
	default:
		return 0, false
	}
}

func (c *Cache) findDRAMBatchByAddress(
	pid vm.PID,
	address uint64,
) (int, bool) {
	key, _ := c.dramBatchKeyForAddress(pid, address)
	for i, pendingKey := range c.dramBatchOrder {
		if pendingKey == key {
			return i, true
		}
	}
	return 0, false
}

func (c *Cache) processDRAMBatches(
	now sim.VTimeInSec,
	force bool,
	forceReason dramBatchDrainReason,
) bool {
	if !c.dramBatchEnabled() || len(c.dramBatchOrder) == 0 {
		return false
	}

	index, reason, ok := c.selectDRAMBatch(now, force, forceReason)
	if !ok {
		return false
	}
	return c.processDRAMBatchAt(now, index, reason)
}

func (c *Cache) processDRAMBatchAt(
	now sim.VTimeInSec,
	index int,
	reason dramBatchDrainReason,
) bool {
	if index < 0 || index >= len(c.dramBatchOrder) {
		return false
	}
	key := c.dramBatchOrder[index]
	entry := c.dramBatches[key]
	if entry == nil || len(entry.transactions) == 0 {
		c.removeDRAMBatchAt(index)
		return false
	}
	if !c.writeBuffer.drainDRAMBatch(now, entry) {
		return false
	}

	c.recordDRAMBatchDrain(now, entry, reason)
	c.removeDRAMBatchAt(index)
	return true
}

func (c *Cache) selectDRAMBatch(
	now sim.VTimeInSec,
	force bool,
	forceReason dramBatchDrainReason,
) (int, dramBatchDrainReason, bool) {
	selected := -1
	for i, key := range c.dramBatchOrder {
		entry := c.dramBatches[key]
		if entry == nil {
			continue
		}
		if len(entry.lineSet) >= c.dramBatchConfig.MaxLines {
			return i, dramBatchDrainFull, true
		}
		if c.dramBatchExpired(now, entry.oldestArrival) {
			return i, dramBatchDrainTimeout, true
		}
		if selected < 0 {
			selected = i
		}
	}

	if force && selected >= 0 {
		return selected, forceReason, true
	}
	return 0, 0, false
}

func (c *Cache) dramBatchExpired(
	now sim.VTimeInSec,
	arrival sim.VTimeInSec,
) bool {
	wait := sim.VTimeInSec(float64(c.dramBatchConfig.MaxWaitNS) * 1e-9)
	// Tick times are floating-point seconds. Allow a tiny tolerance so an
	// exact cycle boundary such as 10 ns -> 11 ns does not slip by one cycle.
	const cycleBoundaryTolerance = sim.VTimeInSec(1e-15)
	return now-arrival+cycleBoundaryTolerance >= wait
}

func (c *Cache) recordDRAMBatchDrain(
	now sim.VTimeInSec,
	entry *dramBatchEntry,
	reason dramBatchDrainReason,
) {
	lines := uint64(len(entry.lineSet))
	c.dramBatchStats.BatchesDrained++
	c.dramBatchStats.LinesInBatches += lines
	if lines > c.dramBatchStats.MaxLinesPerBatch {
		c.dramBatchStats.MaxLinesPerBatch = lines
	}
	c.dramBatchStats.TotalWaitNS +=
		float64(now-entry.oldestArrival) * 1e9
	c.dramBatchStats.WaitSamples++
	if lines <= 1 {
		c.dramBatchStats.SingletonFallbacks++
	}

	switch reason {
	case dramBatchDrainFull:
		c.dramBatchStats.FullDrains++
	case dramBatchDrainTimeout:
		c.dramBatchStats.TimeoutDrains++
	case dramBatchDrainCapacity:
		c.dramBatchStats.CapacityDrains++
	default:
		c.dramBatchStats.DrainDrains++
	}
}

func (c *Cache) removeDRAMBatchAt(index int) {
	key := c.dramBatchOrder[index]
	delete(c.dramBatches, key)
	c.dramBatchOrder = append(
		c.dramBatchOrder[:index],
		c.dramBatchOrder[index+1:]...,
	)
}

func (c *Cache) resetDRAMBatchState() {
	c.dramBatches = nil
	c.dramBatchOrder = nil
	if c.dramAdapter != nil {
		c.dramAdapter.reset(c)
	}
}

func (entry *dramBatchEntry) addressRange() (uint64, uint64) {
	var minAddress, maxAddress uint64
	first := true
	for address := range entry.lineSet {
		if first || address < minAddress {
			minAddress = address
		}
		if first || address > maxAddress {
			maxAddress = address
		}
		first = false
	}
	return minAddress, maxAddress
}

func (wb *writeBufferStage) drainDRAMBatch(
	now sim.VTimeInSec,
	entry *dramBatchEntry,
) bool {
	minAddress, maxAddress := entry.addressRange()
	lineBytes := uint64(1) << wb.cache.log2BlockSize
	spanLines := int((maxAddress-minAddress)/lineBytes) + 1
	if len(entry.lineSet) != 2 || spanLines != 2 {
		return wb.drainDRAMBatchAsSingles(now, entry)
	}
	if len(wb.inflightFetch)+len(entry.transactions) > wb.maxInflightFetch {
		return false
	}
	if !wb.cache.bottomSender.CanSend(1) {
		return false
	}

	read := mem.ReadReqBuilder{}.
		WithSrc(wb.cache.bottomPort).
		WithDst(entry.lowModule).
		WithPID(entry.transactions[0].fetchPID).
		WithAddress(minAddress).
		WithByteSize(2 * lineBytes).
		WithInfo(accessReqInfo(entry.transactions[0].accessReq())).
		Build()
	for _, trans := range entry.transactions {
		trans.fetchReadReq = read
	}

	wb.cache.bottomSender.Send(read)
	for _, trans := range entry.transactions {
		wb.recordDRAMReadSend(now, read, trans)
		wb.inflightFetch = append(wb.inflightFetch, trans)
	}
	wb.cache.dramBatchStats.MultiLineReads++

	tracing.TraceReqInitiate(
		read,
		wb.cache,
		tracing.MsgIDAtReceiver(entry.transactions[0].req(), wb.cache),
	)
	return true
}

func (wb *writeBufferStage) drainDRAMBatchAsSingles(
	now sim.VTimeInSec,
	entry *dramBatchEntry,
) bool {
	if len(wb.inflightFetch)+len(entry.transactions) > wb.maxInflightFetch {
		return false
	}
	if !wb.cache.bottomSender.CanSend(len(entry.transactions)) {
		return false
	}

	for _, trans := range entry.transactions {
		if !wb.issueSingleFetch(now, trans, true) {
			return false
		}
	}
	return true
}
