package writeback

import (
	"github.com/sarchlab/akita/v3/mem/mem"
	memtrace "github.com/sarchlab/akita/v3/mem/trace"
	"github.com/sarchlab/akita/v3/sim"
	"github.com/sarchlab/akita/v3/tracing"
)

type topParser struct {
	cache *Cache
}

func (p *topParser) Tick(now sim.VTimeInSec) bool {
	if p.cache.state != cacheStateRunning {
		return false
	}

	if p.cache.m1CacheEnabled() &&
		p.cache.processM1CacheBatches(now, false, m1DrainManual) {
		return true
	}

	req := p.cache.topPort.Peek()
	if req == nil {
		if p.cache.m1CacheEnabled() && p.cache.m1HasCacheBatches() {
			return p.cache.processM1CacheBatches(
				now, true, m1DrainManual)
		}
		return false
	}

	if p.cache.m1CacheEnabled() {
		return p.processWithM1Cache(now, req)
	}

	return p.processNormalRequest(now, req)
}

func (p *topParser) processWithM1Cache(
	now sim.VTimeInSec,
	req sim.Msg,
) bool {
	if accessReq, ok := req.(mem.AccessReq); ok &&
		p.cache.m1IsLocalSource(accessReq.Meta().Src) {
		p.cache.m1Stats.LocalRequestsSeen++
	}

	if read, ok := req.(*mem.ReadReq); ok &&
		p.cache.m1CacheReadBatchable(read) {
		trans := p.createTransaction(req)
		if !p.cache.enqueueM1CacheRead(now, trans) {
			return false
		}
		p.recordReceive(now, req, trans)
		p.cache.topPort.Retrieve(now)
		return true
	}

	if p.cache.m1HasCacheBatches() {
		return p.cache.processM1CacheBatches(now, true, m1DrainManual)
	}

	if accessReq, ok := req.(mem.AccessReq); ok &&
		p.cache.m1IsLocalSource(accessReq.Meta().Src) {
		p.cache.m1Stats.CacheBypassRequests++
	}

	return p.processNormalRequest(now, req)
}

func (p *topParser) processNormalRequest(
	now sim.VTimeInSec,
	req sim.Msg,
) bool {
	if !p.cache.dirStageBuffer.CanPush() {
		return false
	}

	trans := p.createTransaction(req)
	p.cache.dirStageBuffer.Push(trans)

	p.recordReceive(now, req, trans)
	p.cache.topPort.Retrieve(now)

	return true
}

func (p *topParser) createTransaction(req sim.Msg) *transaction {
	trans := &transaction{
		id: sim.GetIDGenerator().Generate(),
	}
	switch req := req.(type) {
	case *mem.ReadReq:
		trans.read = req
	case *mem.WriteReq:
		trans.write = req
	default:
		panic("unsupported request type")
	}
	return trans
}

func (p *topParser) recordReceive(
	now sim.VTimeInSec,
	req sim.Msg,
	trans *transaction,
) {
	p.cache.inFlightTransactions = append(p.cache.inFlightTransactions, trans)

	if accessReq := trans.accessReq(); accessReq != nil {
		memtrace.RecordMemoryPathL2TopReceive(
			p.cache.Name(),
			accessReq.Meta().ID,
			accessReqInfo(accessReq),
			accessReq.Meta().SendTime,
			now,
			accessReq.Meta().Src,
			accessReq.Meta().Dst,
		)
		memtrace.RecordMemoryPathCacheStart(
			p.cache.Name(),
			accessReq.Meta().ID,
			accessReqInfo(accessReq),
			accessReq.GetAddress(),
			accessReq.GetByteSize(),
			uint64(accessReq.GetPID()),
			accessReqOp(accessReq),
			now,
		)
	}
	tracing.TraceReqReceive(req, p.cache)
}
