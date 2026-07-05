package writeback

import (
	"strings"

	"github.com/sarchlab/akita/v3/mem/mem"
	memtrace "github.com/sarchlab/akita/v3/mem/trace"
	"github.com/sarchlab/akita/v3/mem/vm"
	"github.com/sarchlab/akita/v3/sim"
	"github.com/sarchlab/akita/v3/tracing"
)

// M1Config controls the local L2/DRAM batch helpers.
type M1Config struct {
	CacheHelperEnabled bool
	DRAMHelperEnabled  bool

	CacheBatchEntries int
	CacheBatchLines   int
	CacheBatchWaitNS  uint64
	CacheWindowLines  int

	DRAMBatchEntries int
	DRAMBatchLines   int
	DRAMBatchWaitNS  uint64
	DRAMWindowLines  int
}

// M1Stats reports whether the local-path batch helpers found and used
// batching opportunities.
type M1Stats struct {
	CacheHelperEnabled bool
	DRAMHelperEnabled  bool

	LocalRequestsSeen      uint64
	LocalBatchableReads    uint64
	CacheBypassRequests    uint64
	CacheBatchesCreated    uint64
	CacheBatchesDrained    uint64
	CacheLinesInBatches    uint64
	CacheRequestsInBatches uint64
	CacheDuplicateWaiters  uint64
	CacheCoalescedWaiters  uint64
	CacheFullDrains        uint64
	CacheTimeoutDrains     uint64
	CacheCapacityDrains    uint64
	CacheDrainDrains       uint64
	CacheMaxLinesPerBatch  uint64
	CacheTotalWaitNS       float64
	CacheWaitSamples       uint64

	L2ProbeLines                 uint64
	L2ProbeHits                  uint64
	L2ProbeMisses                uint64
	L2ProbeMSHRHits              uint64
	L2HitLinesCompletedFromBatch uint64
	L2MissLinesSentToDRAMHelper  uint64

	DRAMMissLinesSeen      uint64
	DRAMBatchesCreated     uint64
	DRAMBatchesDrained     uint64
	DRAMLinesInBatches     uint64
	DRAMSingletonFallbacks uint64
	DRAMFullDrains         uint64
	DRAMTimeoutDrains      uint64
	DRAMCapacityDrains     uint64
	DRAMDrainDrains        uint64
	DRAMMaxLinesPerBatch   uint64
	DRAMTotalWaitNS        float64
	DRAMWaitSamples        uint64
	DRAMMultiLineReads     uint64
	DRAMSingleLineReads    uint64
}

type m1CacheBatchKey struct {
	pid   vm.PID
	setID uint64
}

type m1CacheBatchEntry struct {
	id            uint64
	key           m1CacheBatchKey
	oldestArrival sim.VTimeInSec
	newestArrival sim.VTimeInSec
	lineSet       map[uint64]bool
	transactions  []*transaction
	requestCount  uint64
}

type m1DRAMBatchKey struct {
	pid           vm.PID
	lowModuleName string
	windowID      uint64
}

type m1DRAMBatchEntry struct {
	id            uint64
	key           m1DRAMBatchKey
	oldestArrival sim.VTimeInSec
	newestArrival sim.VTimeInSec
	lineSet       map[uint64]bool
	transactions  []*transaction
}

const (
	m1DrainFull = iota
	m1DrainTimeout
	m1DrainCapacity
	m1DrainManual
)

func normalizeM1Config(c M1Config) M1Config {
	if c.CacheBatchEntries <= 0 {
		c.CacheBatchEntries = 16
	}
	if c.CacheBatchLines <= 0 {
		c.CacheBatchLines = 4
	}
	if c.CacheBatchWaitNS == 0 {
		c.CacheBatchWaitNS = 25
	}
	if c.CacheWindowLines <= 0 {
		c.CacheWindowLines = c.CacheBatchLines
	}
	if c.DRAMBatchEntries <= 0 {
		c.DRAMBatchEntries = 16
	}
	if c.DRAMBatchLines <= 0 {
		c.DRAMBatchLines = 2
	}
	if c.DRAMBatchWaitNS == 0 {
		c.DRAMBatchWaitNS = 25
	}
	if c.DRAMWindowLines <= 0 {
		c.DRAMWindowLines = 2
	}
	if c.DRAMBatchLines > c.DRAMWindowLines {
		c.DRAMBatchLines = c.DRAMWindowLines
	}
	return c
}

// ConfigureM1 configures local L2/DRAM batch helpers.
func (c *Cache) ConfigureM1(config M1Config) {
	c.m1Config = normalizeM1Config(config)
	c.m1Stats.CacheHelperEnabled = c.m1Config.CacheHelperEnabled
	c.m1Stats.DRAMHelperEnabled = c.m1Config.DRAMHelperEnabled
}

// GetM1Stats returns a copy of the M1 counters.
func (c *Cache) GetM1Stats() M1Stats {
	return c.m1Stats
}

func (c *Cache) ensureM1CacheState() {
	if c.m1CacheBatches == nil {
		c.m1CacheBatches = make(map[m1CacheBatchKey]*m1CacheBatchEntry)
	}
}

func (c *Cache) ensureM1DRAMState() {
	if c.m1DRAMBatches == nil {
		c.m1DRAMBatches = make(map[m1DRAMBatchKey]*m1DRAMBatchEntry)
	}
}

func (c *Cache) m1CacheEnabled() bool {
	return c.m1Config.CacheHelperEnabled
}

func (c *Cache) m1DRAMEnabled() bool {
	return c.m1Config.DRAMHelperEnabled
}

func (c *Cache) m1HasCacheBatches() bool {
	return len(c.m1CacheBatchOrder) > 0
}

func (c *Cache) m1HasDRAMBatches() bool {
	return len(c.m1DRAMBatchOrder) > 0
}

func (c *Cache) m1CacheReadBatchable(read *mem.ReadReq) bool {
	if read == nil {
		return false
	}
	if !c.m1IsLocalSource(read.Meta().Src) {
		return false
	}
	lineBytes := uint64(1 << c.log2BlockSize)
	if read.AccessByteSize == 0 || read.AccessByteSize > lineBytes {
		return false
	}
	return true
}

func (c *Cache) m1DRAMFetchBatchable(trans *transaction) bool {
	if trans == nil || trans.read == nil {
		return false
	}
	if trans.fetchReadReq != nil {
		return false
	}
	return c.m1IsLocalSource(trans.read.Meta().Src)
}

func (c *Cache) m1IsLocalSource(src sim.Port) bool {
	if src == nil {
		return true
	}
	name := src.Name()
	return !strings.Contains(name, ".RDMA.")
}

func (c *Cache) m1CacheKey(read *mem.ReadReq) m1CacheBatchKey {
	cacheLineID, _ := getCacheLineID(read.Address, c.log2BlockSize)
	return m1CacheBatchKey{
		pid:   read.PID,
		setID: c.m1DirectorySetID(cacheLineID),
	}
}

type m1DirectorySetIDProvider interface {
	SetID(reqAddr uint64) int
}

func (c *Cache) m1DirectorySetID(cacheLineID uint64) uint64 {
	if dir, ok := c.directory.(m1DirectorySetIDProvider); ok {
		return uint64(dir.SetID(cacheLineID))
	}

	sets := c.directory.GetSets()
	if len(sets) == 0 {
		return 0
	}
	return (cacheLineID >> c.log2BlockSize) % uint64(len(sets))
}

func (c *Cache) m1CacheLineID(read *mem.ReadReq) uint64 {
	cacheLineID, _ := getCacheLineID(read.Address, c.log2BlockSize)
	return cacheLineID
}

func (c *Cache) enqueueM1CacheRead(
	now sim.VTimeInSec,
	trans *transaction,
) bool {
	c.ensureM1CacheState()
	key := c.m1CacheKey(trans.read)
	entry := c.m1CacheBatches[key]
	if entry == nil {
		for len(c.m1CacheBatchOrder) >= c.m1Config.CacheBatchEntries {
			if !c.processM1CacheBatches(now, true, m1DrainCapacity) {
				return false
			}
		}
		entry = &m1CacheBatchEntry{
			id:            c.m1NextCacheBatchID,
			key:           key,
			oldestArrival: now,
			newestArrival: now,
			lineSet:       make(map[uint64]bool),
		}
		c.m1NextCacheBatchID++
		c.m1CacheBatches[key] = entry
		c.m1CacheBatchOrder = append(c.m1CacheBatchOrder, key)
		c.m1Stats.CacheBatchesCreated++
	}

	cacheLineID := c.m1CacheLineID(trans.read)
	if entry.lineSet[cacheLineID] {
		c.m1Stats.CacheDuplicateWaiters++
	}
	entry.lineSet[cacheLineID] = true
	entry.transactions = append(entry.transactions, trans)
	entry.requestCount++
	entry.newestArrival = now
	trans.m1FromCacheBatch = true
	trans.m1CacheBatchID = entry.id

	c.m1Stats.LocalBatchableReads++
	if len(entry.lineSet) >= c.m1Config.CacheBatchLines {
		_ = c.processM1CacheBatches(now, true, m1DrainFull)
	}
	return true
}

func (c *Cache) processM1CacheBatches(
	now sim.VTimeInSec,
	force bool,
	forceReason int,
) bool {
	if !c.m1CacheEnabled() || len(c.m1CacheBatchOrder) == 0 {
		return false
	}
	if !c.dirStageBuffer.CanPush() {
		return false
	}

	index, reason, ok := c.selectM1CacheBatch(now, force, forceReason)
	if !ok {
		return false
	}
	key := c.m1CacheBatchOrder[index]
	entry := c.m1CacheBatches[key]
	if entry == nil || len(entry.transactions) == 0 {
		c.removeM1CacheBatchAt(index)
		return false
	}

	trans := c.popM1CacheProbe(entry)
	if trans == nil {
		c.removeM1CacheBatchAt(index)
		return false
	}
	c.dirStageBuffer.Push(trans)

	if len(entry.transactions) == 0 {
		c.recordM1CacheDrain(now, entry, reason)
		delete(c.m1CacheBatches, key)
		c.m1CacheBatchOrder = append(
			c.m1CacheBatchOrder[:index],
			c.m1CacheBatchOrder[index+1:]...)
	}
	return true
}

func (c *Cache) popM1CacheProbe(entry *m1CacheBatchEntry) *transaction {
	if entry == nil || len(entry.transactions) == 0 {
		return nil
	}

	trans := entry.transactions[0]
	entry.transactions = entry.transactions[1:]
	if trans.read == nil || len(entry.transactions) == 0 {
		return trans
	}

	lineID := c.m1CacheLineID(trans.read)
	maxPeers := c.m1MaxCoalescedReadPeers()
	if maxPeers <= 0 {
		return trans
	}

	remaining := entry.transactions[:0]
	for _, candidate := range entry.transactions {
		if len(trans.m1CoalescedReads) < maxPeers &&
			candidate.read != nil &&
			c.m1CacheLineID(candidate.read) == lineID {
			trans.m1CoalescedReads = append(
				trans.m1CoalescedReads, candidate)
			continue
		}
		remaining = append(remaining, candidate)
	}
	entry.transactions = remaining

	if len(trans.m1CoalescedReads) > 0 {
		c.m1Stats.CacheCoalescedWaiters +=
			uint64(len(trans.m1CoalescedReads))
	}
	return trans
}

func (c *Cache) m1MaxCoalescedReadPeers() int {
	maxResponses := c.numReqPerCycle * 4
	if maxResponses <= 1 {
		return 0
	}
	return maxResponses - 1
}

func (c *Cache) selectM1CacheBatch(
	now sim.VTimeInSec,
	force bool,
	forceReason int,
) (int, int, bool) {
	selected := -1
	reason := forceReason
	for i, key := range c.m1CacheBatchOrder {
		entry := c.m1CacheBatches[key]
		if entry == nil {
			continue
		}
		if len(entry.lineSet) >= c.m1Config.CacheBatchLines {
			return i, m1DrainFull, true
		}
		if c.m1Expired(now, entry.oldestArrival, c.m1Config.CacheBatchWaitNS) {
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

func (c *Cache) recordM1CacheDrain(
	now sim.VTimeInSec,
	entry *m1CacheBatchEntry,
	reason int,
) {
	c.m1Stats.CacheBatchesDrained++
	c.m1Stats.CacheLinesInBatches += uint64(len(entry.lineSet))
	c.m1Stats.CacheRequestsInBatches += entry.requestCount
	if uint64(len(entry.lineSet)) > c.m1Stats.CacheMaxLinesPerBatch {
		c.m1Stats.CacheMaxLinesPerBatch = uint64(len(entry.lineSet))
	}
	c.m1Stats.CacheTotalWaitNS += m1VTimeToNS(now - entry.oldestArrival)
	c.m1Stats.CacheWaitSamples++

	switch reason {
	case m1DrainFull:
		c.m1Stats.CacheFullDrains++
	case m1DrainTimeout:
		c.m1Stats.CacheTimeoutDrains++
	case m1DrainCapacity:
		c.m1Stats.CacheCapacityDrains++
	default:
		c.m1Stats.CacheDrainDrains++
	}
}

func (c *Cache) removeM1CacheBatchAt(index int) {
	key := c.m1CacheBatchOrder[index]
	delete(c.m1CacheBatches, key)
	c.m1CacheBatchOrder = append(
		c.m1CacheBatchOrder[:index],
		c.m1CacheBatchOrder[index+1:]...)
}

func (c *Cache) recordM1L2ProbeResult(trans *transaction, result string) {
	if trans == nil || !trans.m1FromCacheBatch {
		return
	}
	c.m1Stats.L2ProbeLines++
	switch result {
	case "hit":
		c.m1Stats.L2ProbeHits++
		c.m1Stats.L2HitLinesCompletedFromBatch++
	case "miss":
		c.m1Stats.L2ProbeMisses++
		if c.m1DRAMEnabled() {
			c.m1Stats.L2MissLinesSentToDRAMHelper++
		}
	case "mshr-hit":
		c.m1Stats.L2ProbeMSHRHits++
	}
}

func (c *Cache) enqueueM1DRAMMiss(
	now sim.VTimeInSec,
	trans *transaction,
) bool {
	c.ensureM1DRAMState()
	key := c.m1DRAMKey(trans)
	entry := c.m1DRAMBatches[key]
	if entry == nil {
		for len(c.m1DRAMBatchOrder) >= c.m1Config.DRAMBatchEntries {
			if !c.processM1DRAMBatches(now, true, m1DrainCapacity) {
				return false
			}
		}
		entry = &m1DRAMBatchEntry{
			id:            c.m1NextDRAMBatchID,
			key:           key,
			oldestArrival: now,
			newestArrival: now,
			lineSet:       make(map[uint64]bool),
		}
		c.m1NextDRAMBatchID++
		c.m1DRAMBatches[key] = entry
		c.m1DRAMBatchOrder = append(c.m1DRAMBatchOrder, key)
		c.m1Stats.DRAMBatchesCreated++
	}

	entry.lineSet[trans.fetchAddress] = true
	entry.transactions = append(entry.transactions, trans)
	entry.newestArrival = now
	trans.m1DRAMBatchID = entry.id
	c.m1Stats.DRAMMissLinesSeen++

	if len(entry.lineSet) >= c.m1Config.DRAMBatchLines {
		_ = c.processM1DRAMBatches(now, true, m1DrainFull)
	}
	return true
}

func (c *Cache) m1DRAMKey(trans *transaction) m1DRAMBatchKey {
	lineBytes := uint64(1 << c.log2BlockSize)
	windowBytes := lineBytes * uint64(c.m1Config.DRAMWindowLines)
	if windowBytes == 0 {
		windowBytes = lineBytes
	}
	lowModule := c.lowModuleFinder.Find(trans.fetchAddress)
	lowModuleName := ""
	if lowModule != nil {
		lowModuleName = lowModule.Name()
	}
	return m1DRAMBatchKey{
		pid:           trans.fetchPID,
		lowModuleName: lowModuleName,
		windowID:      trans.fetchAddress / windowBytes,
	}
}

func (c *Cache) processM1DRAMBatches(
	now sim.VTimeInSec,
	force bool,
	forceReason int,
) bool {
	if !c.m1DRAMEnabled() || len(c.m1DRAMBatchOrder) == 0 {
		return false
	}

	index, reason, ok := c.selectM1DRAMBatch(now, force, forceReason)
	if !ok {
		return false
	}
	key := c.m1DRAMBatchOrder[index]
	entry := c.m1DRAMBatches[key]
	if entry == nil || len(entry.transactions) == 0 {
		c.removeM1DRAMBatchAt(index)
		return false
	}

	if !c.writeBuffer.drainM1DRAMBatch(now, entry) {
		return false
	}

	c.recordM1DRAMDrain(now, entry, reason)
	delete(c.m1DRAMBatches, key)
	c.m1DRAMBatchOrder = append(
		c.m1DRAMBatchOrder[:index],
		c.m1DRAMBatchOrder[index+1:]...)
	return true
}

func (c *Cache) selectM1DRAMBatch(
	now sim.VTimeInSec,
	force bool,
	forceReason int,
) (int, int, bool) {
	selected := -1
	reason := forceReason
	for i, key := range c.m1DRAMBatchOrder {
		entry := c.m1DRAMBatches[key]
		if entry == nil {
			continue
		}
		if len(entry.lineSet) >= c.m1Config.DRAMBatchLines {
			return i, m1DrainFull, true
		}
		if c.m1Expired(now, entry.oldestArrival, c.m1Config.DRAMBatchWaitNS) {
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

func (c *Cache) recordM1DRAMDrain(
	now sim.VTimeInSec,
	entry *m1DRAMBatchEntry,
	reason int,
) {
	lines := len(entry.lineSet)
	c.m1Stats.DRAMBatchesDrained++
	c.m1Stats.DRAMLinesInBatches += uint64(lines)
	if uint64(lines) > c.m1Stats.DRAMMaxLinesPerBatch {
		c.m1Stats.DRAMMaxLinesPerBatch = uint64(lines)
	}
	c.m1Stats.DRAMTotalWaitNS += m1VTimeToNS(now - entry.oldestArrival)
	c.m1Stats.DRAMWaitSamples++
	if lines <= 1 {
		c.m1Stats.DRAMSingletonFallbacks++
	}

	switch reason {
	case m1DrainFull:
		c.m1Stats.DRAMFullDrains++
	case m1DrainTimeout:
		c.m1Stats.DRAMTimeoutDrains++
	case m1DrainCapacity:
		c.m1Stats.DRAMCapacityDrains++
	default:
		c.m1Stats.DRAMDrainDrains++
	}
}

func (c *Cache) removeM1DRAMBatchAt(index int) {
	key := c.m1DRAMBatchOrder[index]
	delete(c.m1DRAMBatches, key)
	c.m1DRAMBatchOrder = append(
		c.m1DRAMBatchOrder[:index],
		c.m1DRAMBatchOrder[index+1:]...)
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

func (wb *writeBufferStage) drainM1DRAMBatch(
	now sim.VTimeInSec,
	entry *m1DRAMBatchEntry,
) bool {
	minAddr, maxAddr := entry.m1AddressRange()
	lineBytes := uint64(1 << wb.cache.log2BlockSize)
	spanLines := int((maxAddr-minAddr)/lineBytes) + 1
	if len(entry.lineSet) <= 1 || spanLines != len(entry.lineSet) {
		return wb.drainM1DRAMBatchAsSingles(now, entry)
	}
	if len(wb.inflightFetch)+len(entry.transactions) > wb.maxInflightFetch {
		return false
	}
	if !wb.cache.bottomSender.CanSend(1) {
		return false
	}

	lowModulePort := wb.cache.lowModuleFinder.Find(minAddr)
	infos := make([]interface{}, 0, len(entry.transactions))
	for _, trans := range entry.transactions {
		infos = append(infos, accessReqInfo(trans.accessReq()))
	}
	read := mem.ReadReqBuilder{}.
		WithSrc(wb.cache.bottomPort).
		WithDst(lowModulePort).
		WithPID(entry.transactions[0].fetchPID).
		WithAddress(minAddr).
		WithByteSize(uint64(spanLines) * lineBytes).
		WithInfo(memtrace.WithMemoryPathBatchInfo(infos...)).
		Build()

	for _, trans := range entry.transactions {
		trans.fetchReadReq = read
	}

	wb.cache.bottomSender.Send(read)
	for _, trans := range entry.transactions {
		wb.recordM1DRAMReadSend(now, read, trans)
		wb.inflightFetch = append(wb.inflightFetch, trans)
	}
	wb.cache.m1Stats.DRAMMultiLineReads++
	tracing.TraceReqInitiate(read, wb.cache,
		tracing.MsgIDAtReceiver(entry.transactions[0].req(), wb.cache))

	return true
}

func (wb *writeBufferStage) drainM1DRAMBatchAsSingles(
	now sim.VTimeInSec,
	entry *m1DRAMBatchEntry,
) bool {
	if len(wb.inflightFetch)+len(entry.transactions) > wb.maxInflightFetch {
		return false
	}
	if !wb.cache.bottomSender.CanSend(len(entry.transactions)) {
		return false
	}
	for _, trans := range entry.transactions {
		if !wb.issueSingleFetch(now, trans) {
			return false
		}
	}
	return true
}

func (entry *m1DRAMBatchEntry) m1AddressRange() (uint64, uint64) {
	var minAddr, maxAddr uint64
	first := true
	for addr := range entry.lineSet {
		if first || addr < minAddr {
			minAddr = addr
		}
		if first || addr > maxAddr {
			maxAddr = addr
		}
		first = false
	}
	return minAddr, maxAddr
}
