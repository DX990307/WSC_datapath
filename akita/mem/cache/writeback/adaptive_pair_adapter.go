package writeback

import (
	"github.com/sarchlab/akita/v3/mem/mem"
	memtrace "github.com/sarchlab/akita/v3/mem/trace"
	"github.com/sarchlab/akita/v3/mem/vm"
	"github.com/sarchlab/akita/v3/sim"
	"github.com/sarchlab/akita/v3/tracing"
)

// adaptivePairPredictionThreshold is deliberately identical to the threshold
// used by the best-performing July-12 M1 adapter.
const adaptivePairPredictionThreshold = 2

type adaptivePairWindowKey struct {
	pid           vm.PID
	lowModuleName string
	windowID      uint64
}

type adaptivePairLineKey struct {
	pid     vm.PID
	address uint64
}

// AdaptivePairStats preserves the useful counters of the historical 128-B
// adapter.
type AdaptivePairStats struct {
	Enabled                    bool
	RegionLines                uint64
	MissLinesSeen              uint64
	Observations               uint64
	Useful                     uint64
	Predictions                uint64
	InflightHits               uint64
	BufferHits                 uint64
	PrefetchUnused             uint64
	PrefetchUnusedEvictions    uint64
	PrefetchUnusedInvalidates  uint64
	PrefetchUnusedResetRetires uint64
	CurrentPrefetchOnlyLines   uint64
	PeakPrefetchOnlyLines      uint64
	Unused                     uint64
	Confidence                 uint64
	Wide128BReads              uint64
	ExpandedRegionReads        uint64
	PrefetchedRegionLines      uint64
	FilterCandidates           uint64
	FilterLookups              uint64
	FilterBusyFallbacks        uint64
	FilterNotReadyFallbacks    uint64
	FilterUnreliableFallbacks  uint64
	ResidentFilterPositives    uint64
	ResidentExactSuppressions  uint64
	ResidentFalsePositives     uint64
	PendingFilterPositives     uint64
	PendingExactSuppressions   uint64
	PendingFalsePositives      uint64
	PendingFilterInsertFailure uint64
}

const (
	adaptivePairResidentLookup = iota
	adaptivePairPendingLookup
	adaptivePairLookupCount
)

// adaptivePairAdapter is the historical M1 state machine: adjacent real
// demands train a 2-bit confidence counter; a confident demand immediately
// requests its sibling; a later sibling demand joins the in-flight request or
// consumes a bounded response buffer.  The prediction policy is unchanged.
type adaptivePairAdapter struct {
	confidence  int
	capacity    int
	regionLines int

	// seen stores a bitmap of real-demand lines observed in each aligned
	// region. Prediction becomes useful only after two adjacent bits appear.
	seen      map[adaptivePairWindowKey]uint64
	seenOrder []adaptivePairWindowKey

	inflight     map[adaptivePairLineKey]*mem.ReadReq
	readPrefetch map[string][]adaptivePairLineKey

	buffered    map[adaptivePairLineKey][]byte
	bufferOrder []adaptivePairLineKey
}

func newAdaptivePairAdapter(capacity, regionLines int) *adaptivePairAdapter {
	if capacity < 1 {
		capacity = 16
	}
	if regionLines != 2 && regionLines != 4 &&
		regionLines != 8 && regionLines != 16 {
		panic("adaptive-pair region lines must be one of 2, 4, 8, or 16")
	}
	return &adaptivePairAdapter{
		capacity:     capacity,
		regionLines:  regionLines,
		seen:         make(map[adaptivePairWindowKey]uint64),
		inflight:     make(map[adaptivePairLineKey]*mem.ReadReq),
		readPrefetch: make(map[string][]adaptivePairLineKey),
		buffered:     make(map[adaptivePairLineKey][]byte),
	}
}

func (a *adaptivePairAdapter) reset(c *Cache) {
	resetRetires := len(a.inflight) + len(a.buffered)
	if resetRetires > 0 {
		c.adaptivePairStats.PrefetchUnused += uint64(resetRetires)
		c.adaptivePairStats.PrefetchUnusedResetRetires += uint64(resetRetires)
	}
	a.confidence = 0
	a.seen = make(map[adaptivePairWindowKey]uint64)
	a.seenOrder = nil
	a.inflight = make(map[adaptivePairLineKey]*mem.ReadReq)
	a.readPrefetch = make(map[string][]adaptivePairLineKey)
	a.buffered = make(map[adaptivePairLineKey][]byte)
	a.bufferOrder = nil
	a.publishOccupancy(c)
	a.publishConfidence(c)
}

func (a *adaptivePairAdapter) publishOccupancy(c *Cache) {
	current := uint64(len(a.inflight) + len(a.buffered))
	c.adaptivePairStats.CurrentPrefetchOnlyLines = current
	if current > c.adaptivePairStats.PeakPrefetchOnlyLines {
		c.adaptivePairStats.PeakPrefetchOnlyLines = current
	}
}

func (a *adaptivePairAdapter) publishConfidence(c *Cache) {
	c.adaptivePairStats.Confidence = uint64(a.confidence)
}

func (a *adaptivePairAdapter) reward(c *Cache) {
	if a.confidence < 3 {
		a.confidence++
	}
	c.adaptivePairStats.Useful++
	a.publishConfidence(c)
}

func (a *adaptivePairAdapter) penalize(c *Cache) {
	if a.confidence > 0 {
		a.confidence--
	}
	c.adaptivePairStats.Unused++
	a.publishConfidence(c)
}

func (c *Cache) adaptivePairKeyForAddress(
	pid vm.PID,
	address uint64,
) adaptivePairWindowKey {
	lineBytes := uint64(1) << c.log2BlockSize
	regionLines := uint64(c.adaptivePairAdapter.regionLines)
	lowModule := c.lowModuleFinder.Find(address)
	lowModuleName := ""
	if lowModule != nil {
		lowModuleName = lowModule.Name()
	}
	return adaptivePairWindowKey{
		pid: pid, lowModuleName: lowModuleName,
		windowID: address / (regionLines * lineBytes),
	}
}

func (a *adaptivePairAdapter) regionBase(address, lineBytes uint64) uint64 {
	regionBytes := uint64(a.regionLines) * lineBytes
	return address / regionBytes * regionBytes
}

func (a *adaptivePairAdapter) adjacentAddress(
	address, lineBytes uint64,
) uint64 {
	base := a.regionBase(address, lineBytes)
	index := (address - base) / lineBytes
	if index%2 == 0 {
		return address + lineBytes
	}
	return address - lineBytes
}

func (a *adaptivePairAdapter) observeDemand(
	c *Cache,
	pid vm.PID,
	address uint64,
) bool {
	key := c.adaptivePairKeyForAddress(pid, address)
	c.adaptivePairStats.Observations++
	lineBytes := uint64(1) << c.log2BlockSize
	base := a.regionBase(address, lineBytes)
	index := (address - base) / lineBytes
	bit := uint64(1) << index
	adjacentIndex := index ^ 1
	if observed := a.seen[key]; observed&(uint64(1)<<adjacentIndex) != 0 {
		delete(a.seen, key)
		a.removeSeenOrder(key)
		a.reward(c)
		return true
	}
	if _, ok := a.seen[key]; !ok {
		a.seenOrder = append(a.seenOrder, key)
	}
	a.seen[key] |= bit
	limit := a.capacity * 4
	for len(a.seenOrder) > limit {
		oldest := a.seenOrder[0]
		a.seenOrder = a.seenOrder[1:]
		if _, ok := a.seen[oldest]; ok {
			delete(a.seen, oldest)
			a.penalize(c)
		}
	}
	return false
}

func (a *adaptivePairAdapter) removeSeenOrder(key adaptivePairWindowKey) {
	for i, candidate := range a.seenOrder {
		if candidate == key {
			a.seenOrder = append(a.seenOrder[:i], a.seenOrder[i+1:]...)
			return
		}
	}
}

func (a *adaptivePairAdapter) forgetWindow(
	c *Cache,
	pid vm.PID,
	address uint64,
) {
	key := c.adaptivePairKeyForAddress(pid, address)
	delete(a.seen, key)
	a.removeSeenOrder(key)
}

func (a *adaptivePairAdapter) consume(
	c *Cache,
	pid vm.PID,
	address uint64,
) ([]byte, bool) {
	key := adaptivePairLineKey{pid: pid, address: address}
	data, ok := a.buffered[key]
	if !ok {
		return nil, false
	}
	delete(a.buffered, key)
	a.removeBufferOrder(key)
	a.publishOccupancy(c)
	c.adaptivePairStats.BufferHits++
	a.reward(c)
	return data, true
}

func (a *adaptivePairAdapter) joinInflight(
	c *Cache,
	trans *transaction,
) bool {
	key := adaptivePairLineKey{
		pid: trans.fetchPID, address: trans.fetchAddress,
	}
	read := a.inflight[key]
	if read == nil {
		return false
	}
	if len(c.writeBuffer.inflightFetch) >= c.writeBuffer.maxInflightFetch {
		return false
	}
	delete(a.inflight, key)
	a.removeReadPrefetch(read.ID, key)
	a.publishOccupancy(c)
	trans.fetchReadReq = read
	c.writeBuffer.inflightFetch = append(c.writeBuffer.inflightFetch, trans)
	c.writeBufferBuffer.Pop()
	c.adaptivePairStats.InflightHits++
	a.reward(c)
	return true
}

func (a *adaptivePairAdapter) removeReadPrefetch(
	readID string,
	key adaptivePairLineKey,
) {
	keys := a.readPrefetch[readID]
	for i, candidate := range keys {
		if candidate != key {
			continue
		}
		keys = append(keys[:i], keys[i+1:]...)
		if len(keys) == 0 {
			delete(a.readPrefetch, readID)
		} else {
			a.readPrefetch[readID] = keys
		}
		return
	}
}

func (a *adaptivePairAdapter) shouldPredict() bool {
	return a.confidence >= adaptivePairPredictionThreshold
}

// primeAdaptivePairLookups starts the sibling lifecycle queries while the
// demand is still entering the ordinary L2 pipeline. The normal L2 lookup
// therefore covers the modeled Cuckoo-Filter latency. A busy Filter never
// holds the demand; the historical adapter remains the fail-open fallback.
func (c *Cache) primeAdaptivePairLookups(
	now sim.VTimeInSec,
	trans *transaction,
) {
	if !c.adaptivePairEnabled || c.adaptivePairAdapter == nil ||
		c.requestFilter == nil || trans == nil || trans.read == nil ||
		trans.read.LookupOnly || !c.adaptivePairAdapter.shouldPredict() {
		return
	}
	lineBytes := uint64(1) << c.log2BlockSize
	line, _ := getCacheLineID(trans.read.Address, c.log2BlockSize)
	sibling := c.adaptivePairAdapter.adjacentAddress(line, lineBytes)
	if !c.ownsAddress(sibling) || c.lowModuleFinder == nil ||
		c.lowModuleFinder.Find(line) != c.lowModuleFinder.Find(sibling) {
		return
	}
	keys := [adaptivePairLookupCount]TypedFilterKey{
		{PID: trans.read.PID, Address: sibling, Type: FilterResident},
		{PID: trans.read.PID, Address: sibling, Type: FilterGranularityPending},
	}
	for i, key := range keys {
		lookup, accepted := c.requestFilter.StartLookup(now, key)
		if !accepted {
			c.adaptivePairStats.FilterBusyFallbacks++
			continue
		}
		trans.adaptivePairLookups[i] = lookup
		trans.adaptivePairLookupSet[i] = true
		c.adaptivePairStats.FilterLookups++
	}
}

// adaptivePairFilterAllows preserves the historical policy unless the shared
// per-slice Filter identifies a possible sibling conflict and an exact lookup
// confirms it. False positives and unavailable metadata never suppress a
// useful 128-B access.
func (c *Cache) adaptivePairFilterAllows(
	now sim.VTimeInSec,
	trans *transaction,
) bool {
	c.adaptivePairStats.FilterCandidates++
	if c.requestFilter == nil {
		c.adaptivePairStats.FilterUnreliableFallbacks++
		return true
	}
	lineBytes := uint64(1) << c.log2BlockSize
	sibling := c.adaptivePairAdapter.adjacentAddress(
		trans.fetchAddress, lineBytes)
	for i := 0; i < adaptivePairLookupCount; i++ {
		if !trans.adaptivePairLookupSet[i] {
			// The lookup port was busy or confidence became high only after L2
			// admission. Neither case is allowed to slow the old fast path.
			return true
		}
		possible, reliable, ready := c.requestFilter.CompleteLookup(
			now, trans.adaptivePairLookups[i])
		if !ready {
			c.adaptivePairStats.FilterNotReadyFallbacks++
			return true
		}
		if !reliable {
			c.adaptivePairStats.FilterUnreliableFallbacks++
			return true
		}
		if !possible {
			continue
		}
		switch i {
		case adaptivePairResidentLookup:
			c.adaptivePairStats.ResidentFilterPositives++
			block := c.directory.Lookup(trans.fetchPID, sibling)
			if block != nil && block.IsValid && !block.IsLocked {
				c.adaptivePairStats.ResidentExactSuppressions++
				return false
			}
			c.adaptivePairStats.ResidentFalsePositives++
		case adaptivePairPendingLookup:
			c.adaptivePairStats.PendingFilterPositives++
			if c.mshr.Query(trans.fetchPID, sibling) != nil {
				c.adaptivePairStats.PendingExactSuppressions++
				return false
			}
			c.adaptivePairStats.PendingFalsePositives++
		}
	}
	return true
}

// captureResponse extracts the unused half of one 128-B response. If a real
// sibling demand already joined the same read, joinInflight removed this
// mapping and both demands are completed directly from the shared response.
func (a *adaptivePairAdapter) captureResponse(
	c *Cache,
	rsp *mem.DataReadyRsp,
	read *mem.ReadReq,
) {
	keys, ok := a.readPrefetch[rsp.RespondTo]
	if !ok {
		return
	}
	delete(a.readPrefetch, rsp.RespondTo)
	lineBytes := uint64(1) << c.log2BlockSize
	for _, key := range keys {
		delete(a.inflight, key)
		if key.address < read.Address {
			c.adaptivePairStats.PrefetchUnused++
			continue
		}
		offset := key.address - read.Address
		if offset+lineBytes > uint64(len(rsp.Data)) {
			c.adaptivePairStats.PrefetchUnused++
			continue
		}
		data := make([]byte, lineBytes)
		copy(data, rsp.Data[offset:offset+lineBytes])
		a.insert(c, key, data)
	}
	a.publishOccupancy(c)
}

func (a *adaptivePairAdapter) insert(
	c *Cache,
	key adaptivePairLineKey,
	data []byte,
) {
	if _, exists := a.buffered[key]; exists {
		a.buffered[key] = data
		return
	}
	for len(a.bufferOrder) >= a.capacity {
		victim := a.bufferOrder[0]
		a.bufferOrder = a.bufferOrder[1:]
		if _, ok := a.buffered[victim]; ok {
			delete(a.buffered, victim)
			c.adaptivePairStats.PrefetchUnused++
			c.adaptivePairStats.PrefetchUnusedEvictions++
			a.penalize(c)
		}
	}
	a.buffered[key] = data
	a.bufferOrder = append(a.bufferOrder, key)
	a.publishOccupancy(c)
}

func (a *adaptivePairAdapter) removeBufferOrder(key adaptivePairLineKey) {
	for i, candidate := range a.bufferOrder {
		if candidate == key {
			a.bufferOrder = append(a.bufferOrder[:i], a.bufferOrder[i+1:]...)
			return
		}
	}
}

func (a *adaptivePairAdapter) invalidateAddress(
	c *Cache,
	pid vm.PID,
	address uint64,
) {
	lineBytes := uint64(1) << c.log2BlockSize
	address = address / lineBytes * lineBytes
	key := adaptivePairLineKey{pid: pid, address: address}
	if _, ok := a.buffered[key]; ok {
		delete(a.buffered, key)
		a.removeBufferOrder(key)
		c.adaptivePairStats.PrefetchUnused++
		c.adaptivePairStats.PrefetchUnusedInvalidates++
		a.penalize(c)
	}
	if read := a.inflight[key]; read != nil {
		delete(a.inflight, key)
		a.removeReadPrefetch(read.ID, key)
		c.adaptivePairStats.PrefetchUnused++
		c.adaptivePairStats.PrefetchUnusedInvalidates++
		a.penalize(c)
	}
	a.publishOccupancy(c)
}

func (a *adaptivePairAdapter) invalidateTransaction(c *Cache, trans *transaction) {
	if req := trans.accessReq(); req != nil {
		a.invalidateAddress(c, req.GetPID(), req.GetAddress())
	}
	switch trans.action {
	case writeBufferEvictAndFetch, writeBufferEvictAndWrite, writeBufferFlush:
		a.invalidateAddress(c, trans.evictingPID, trans.evictingAddr)
	}
}

func (wb *writeBufferStage) processAdaptivePairFetch(
	now sim.VTimeInSec,
	trans *transaction,
) bool {
	a := wb.cache.adaptivePairAdapter
	wb.cache.adaptivePairStats.MissLinesSeen++
	if data, ok := a.consume(
		wb.cache, trans.fetchPID, trans.fetchAddress); ok {
		trans.fetchedData = data
		return wb.sendFetchedDataToBank(now, trans)
	}
	if a.joinInflight(wb.cache, trans) {
		return true
	}

	paired := a.observeDemand(
		wb.cache, trans.fetchPID, trans.fetchAddress)
	if !paired && a.shouldPredict() &&
		wb.cache.adaptivePairFilterAllows(now, trans) &&
		wb.issueAdaptivePair(now, trans) {
		wb.cache.writeBufferBuffer.Pop()
		return true
	}
	if !wb.issueSingleFetch(now, trans) {
		return false
	}
	wb.cache.writeBufferBuffer.Pop()
	return true
}

func (wb *writeBufferStage) issueAdaptivePair(
	now sim.VTimeInSec,
	trans *transaction,
) bool {
	if wb.tooManyInflightFetches() ||
		!wb.cache.bottomSender.CanSend(1) {
		return false
	}
	lineBytes := uint64(1) << wb.cache.log2BlockSize
	a := wb.cache.adaptivePairAdapter
	regionBytes := uint64(a.regionLines) * lineBytes
	base := a.regionBase(trans.fetchAddress, lineBytes)
	lowModule := wb.cache.lowModuleFinder.Find(base)
	if lowModule == nil || lowModule != wb.cache.lowModuleFinder.Find(
		base+regionBytes-lineBytes) {
		return false
	}
	prefetched := make([]adaptivePairLineKey, 0, a.regionLines-1)
	for address := base; address < base+regionBytes; address += lineBytes {
		if address == trans.fetchAddress {
			continue
		}
		key := adaptivePairLineKey{pid: trans.fetchPID, address: address}
		if wb.cache.directory.Lookup(trans.fetchPID, address) != nil ||
			wb.cache.mshr.Query(trans.fetchPID, address) != nil ||
			a.inflight[key] != nil {
			return false
		}
		if _, buffered := a.buffered[key]; buffered {
			return false
		}
		prefetched = append(prefetched, key)
	}
	read := mem.ReadReqBuilder{}.
		WithSrc(wb.cache.bottomPort).
		WithDst(lowModule).
		WithPID(trans.fetchPID).
		WithAddress(base).
		WithByteSize(regionBytes).
		WithStreamID(accessReqStreamID(trans.accessReq())).
		WithLocalStreamID(accessReqLocalStreamID(trans.accessReq())).
		WithInfo(accessReqInfo(trans.accessReq())).
		Build()
	wb.cache.bottomSender.Send(read)

	trans.fetchReadReq = read
	wb.recordDRAMReadSend(now, read, trans)
	wb.inflightFetch = append(wb.inflightFetch, trans)
	for _, key := range prefetched {
		a.inflight[key] = read
	}
	a.readPrefetch[read.ID] = prefetched
	a.publishOccupancy(wb.cache)
	a.forgetWindow(
		wb.cache, trans.fetchPID, trans.fetchAddress)
	wb.cache.adaptivePairStats.Predictions++
	wb.cache.adaptivePairStats.ExpandedRegionReads++
	wb.cache.adaptivePairStats.PrefetchedRegionLines += uint64(len(prefetched))
	if a.regionLines == 2 {
		wb.cache.adaptivePairStats.Wide128BReads++
	}
	wb.cache.localMemoryPathStats.DRAMReadRequests++

	memtrace.LinkObservationRequestFromRequest(
		trans.accessReq().Meta().ID, read.Meta().ID, "l2_dram_read")
	memtrace.ObservationTransitionByRequest(
		trans.accessReq().Meta().ID,
		"l2_dram_request_issued", "l2_to_dram", now)
	tracing.TraceReqInitiate(read, wb.cache,
		tracing.MsgIDAtReceiver(trans.req(), wb.cache))
	return true
}
