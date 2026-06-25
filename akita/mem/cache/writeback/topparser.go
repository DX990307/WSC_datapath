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

	req := p.cache.topPort.Peek()
	if req == nil {
		return false
	}

	if !p.cache.dirStageBuffer.CanPush() {
		return false
	}

	trans := &transaction{
		id: sim.GetIDGenerator().Generate(),
	}
	switch req := req.(type) {
	case *mem.ReadReq:
		trans.read = req
	case *mem.WriteReq:
		trans.write = req
	}
	p.cache.dirStageBuffer.Push(trans)

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

	p.cache.topPort.Retrieve(now)

	return true
}
