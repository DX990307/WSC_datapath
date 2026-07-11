package writeback

import (
	"github.com/sarchlab/akita/v3/mem/mem"
	"github.com/sarchlab/akita/v3/mem/vm"
	"github.com/sarchlab/akita/v3/sim"
	"github.com/sarchlab/akita/v3/tracing"
)

const dramAdapterPredictionThreshold = 2

type dramAdapterLineKey struct {
	pid     vm.PID
	address uint64
}

type dramPrefetchAdapter struct {
	confidence int
	capacity   int

	seen      map[dramBatchKey]uint64
	seenOrder []dramBatchKey

	inflight     map[dramAdapterLineKey]*mem.ReadReq
	readPrefetch map[string]dramAdapterLineKey

	buffered    map[dramAdapterLineKey][]byte
	bufferOrder []dramAdapterLineKey
}

func newDRAMPrefetchAdapter(capacity int) *dramPrefetchAdapter {
	if capacity < 1 {
		capacity = 16
	}
	return &dramPrefetchAdapter{
		capacity:     capacity,
		seen:         make(map[dramBatchKey]uint64),
		inflight:     make(map[dramAdapterLineKey]*mem.ReadReq),
		readPrefetch: make(map[string]dramAdapterLineKey),
		buffered:     make(map[dramAdapterLineKey][]byte),
	}
}

func (a *dramPrefetchAdapter) reset(c *Cache) {
	a.confidence = 0
	a.seen = make(map[dramBatchKey]uint64)
	a.seenOrder = nil
	a.inflight = make(map[dramAdapterLineKey]*mem.ReadReq)
	a.readPrefetch = make(map[string]dramAdapterLineKey)
	a.buffered = make(map[dramAdapterLineKey][]byte)
	a.bufferOrder = nil
	a.publishConfidence(c)
}

func (a *dramPrefetchAdapter) publishConfidence(c *Cache) {
	c.dramBatchStats.AdapterConfidence = uint64(a.confidence)
}

func (a *dramPrefetchAdapter) reward(c *Cache) {
	if a.confidence < 3 {
		a.confidence++
	}
	c.dramBatchStats.AdapterUseful++
	a.publishConfidence(c)
}

func (a *dramPrefetchAdapter) penalize(c *Cache) {
	if a.confidence > 0 {
		a.confidence--
	}
	c.dramBatchStats.AdapterUnused++
	a.publishConfidence(c)
}

func (a *dramPrefetchAdapter) observeDemand(
	c *Cache,
	pid vm.PID,
	address uint64,
) bool {
	key, _ := c.dramBatchKeyForAddress(pid, address)
	c.dramBatchStats.AdapterObservations++
	if first, ok := a.seen[key]; ok && first != address {
		delete(a.seen, key)
		a.removeSeenOrder(key)
		a.reward(c)
		return true
	}
	if _, ok := a.seen[key]; !ok {
		a.seen[key] = address
		a.seenOrder = append(a.seenOrder, key)
	}
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

func (a *dramPrefetchAdapter) removeSeenOrder(key dramBatchKey) {
	for i, candidate := range a.seenOrder {
		if candidate == key {
			a.seenOrder = append(a.seenOrder[:i], a.seenOrder[i+1:]...)
			return
		}
	}
}

func (a *dramPrefetchAdapter) forgetWindow(c *Cache, pid vm.PID, address uint64) {
	key, _ := c.dramBatchKeyForAddress(pid, address)
	delete(a.seen, key)
	a.removeSeenOrder(key)
}

func (a *dramPrefetchAdapter) consume(
	c *Cache,
	pid vm.PID,
	address uint64,
) ([]byte, bool) {
	key := dramAdapterLineKey{pid: pid, address: address}
	data, ok := a.buffered[key]
	if !ok {
		return nil, false
	}
	delete(a.buffered, key)
	a.removeBufferOrder(key)
	c.dramBatchStats.AdapterBufferHits++
	a.reward(c)
	return data, true
}

func (a *dramPrefetchAdapter) joinInflight(
	c *Cache,
	trans *transaction,
) bool {
	key := dramAdapterLineKey{pid: trans.fetchPID, address: trans.fetchAddress}
	read := a.inflight[key]
	if read == nil {
		return false
	}
	if len(c.writeBuffer.inflightFetch) >= c.writeBuffer.maxInflightFetch {
		return false
	}
	delete(a.inflight, key)
	delete(a.readPrefetch, read.ID)
	trans.fetchReadReq = read
	c.writeBuffer.inflightFetch = append(c.writeBuffer.inflightFetch, trans)
	c.writeBufferBuffer.Pop()
	c.dramBatchStats.AdapterInflightHits++
	a.reward(c)
	return true
}

func (a *dramPrefetchAdapter) shouldPredict() bool {
	return a.confidence >= dramAdapterPredictionThreshold
}

func (a *dramPrefetchAdapter) captureResponse(
	c *Cache,
	rsp *mem.DataReadyRsp,
	read *mem.ReadReq,
) {
	key, ok := a.readPrefetch[rsp.RespondTo]
	if !ok {
		return
	}
	delete(a.readPrefetch, rsp.RespondTo)
	delete(a.inflight, key)
	lineBytes := uint64(1) << c.log2BlockSize
	if key.address < read.Address {
		return
	}
	offset := key.address - read.Address
	if offset+lineBytes > uint64(len(rsp.Data)) {
		return
	}
	data := make([]byte, lineBytes)
	copy(data, rsp.Data[offset:offset+lineBytes])
	a.insert(c, key, data)
}

func (a *dramPrefetchAdapter) insert(
	c *Cache,
	key dramAdapterLineKey,
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
			a.penalize(c)
		}
	}
	a.buffered[key] = data
	a.bufferOrder = append(a.bufferOrder, key)
}

func (a *dramPrefetchAdapter) removeBufferOrder(key dramAdapterLineKey) {
	for i, candidate := range a.bufferOrder {
		if candidate == key {
			a.bufferOrder = append(a.bufferOrder[:i], a.bufferOrder[i+1:]...)
			return
		}
	}
}

func (a *dramPrefetchAdapter) invalidateAddress(
	c *Cache,
	pid vm.PID,
	address uint64,
) {
	lineBytes := uint64(1) << c.log2BlockSize
	address = address / lineBytes * lineBytes
	key := dramAdapterLineKey{pid: pid, address: address}
	if _, ok := a.buffered[key]; ok {
		delete(a.buffered, key)
		a.removeBufferOrder(key)
		a.penalize(c)
	}
	if read := a.inflight[key]; read != nil {
		delete(a.inflight, key)
		delete(a.readPrefetch, read.ID)
		a.penalize(c)
	}
}

func (a *dramPrefetchAdapter) invalidateTransaction(c *Cache, trans *transaction) {
	if req := trans.accessReq(); req != nil {
		a.invalidateAddress(c, req.GetPID(), req.GetAddress())
	}
	switch trans.action {
	case writeBufferEvictAndFetch, writeBufferEvictAndWrite, writeBufferFlush:
		a.invalidateAddress(c, trans.evictingPID, trans.evictingAddr)
	}
}

func (wb *writeBufferStage) processAdaptiveDRAMFetch(
	now sim.VTimeInSec,
	trans *transaction,
) bool {
	a := wb.cache.dramAdapter
	wb.cache.dramBatchStats.MissLinesSeen++
	if data, ok := a.consume(wb.cache, trans.fetchPID, trans.fetchAddress); ok {
		trans.fetchedData = data
		return wb.sendFetchedDataToBank(now, trans)
	}
	if a.joinInflight(wb.cache, trans) {
		return true
	}

	paired := a.observeDemand(wb.cache, trans.fetchPID, trans.fetchAddress)
	if !paired && a.shouldPredict() && wb.issueAdaptivePrefetch(now, trans) {
		wb.cache.writeBufferBuffer.Pop()
		return true
	}
	if !wb.issueSingleFetch(now, trans, true) {
		return false
	}
	wb.cache.writeBufferBuffer.Pop()
	return true
}

func (wb *writeBufferStage) issueAdaptivePrefetch(
	now sim.VTimeInSec,
	trans *transaction,
) bool {
	if wb.tooManyInflightFetches() || !wb.cache.bottomSender.CanSend(1) {
		return false
	}
	lineBytes := uint64(1) << wb.cache.log2BlockSize
	base := trans.fetchAddress / (2 * lineBytes) * (2 * lineBytes)
	sibling := base
	if sibling == trans.fetchAddress {
		sibling += lineBytes
	}
	lowModule := wb.cache.lowModuleFinder.Find(base)
	if lowModule != wb.cache.lowModuleFinder.Find(sibling) {
		return false
	}
	read := mem.ReadReqBuilder{}.
		WithSrc(wb.cache.bottomPort).
		WithDst(lowModule).
		WithPID(trans.fetchPID).
		WithAddress(base).
		WithByteSize(2 * lineBytes).
		WithInfo(accessReqInfo(trans.accessReq())).
		Build()
	trans.fetchReadReq = read
	wb.cache.bottomSender.Send(read)
	wb.recordDRAMReadSend(now, read, trans)
	wb.inflightFetch = append(wb.inflightFetch, trans)
	key := dramAdapterLineKey{pid: trans.fetchPID, address: sibling}
	wb.cache.dramAdapter.inflight[key] = read
	wb.cache.dramAdapter.readPrefetch[read.ID] = key
	wb.cache.dramAdapter.forgetWindow(
		wb.cache, trans.fetchPID, trans.fetchAddress)
	wb.cache.dramBatchStats.AdapterPredictions++
	wb.cache.dramBatchStats.BatchesCreated++
	wb.cache.dramBatchStats.BatchesDrained++
	wb.cache.dramBatchStats.LinesInBatches += 2
	wb.cache.dramBatchStats.MultiLineReads++
	tracing.TraceReqInitiate(
		read,
		wb.cache,
		tracing.MsgIDAtReceiver(trans.req(), wb.cache),
	)
	return true
}
