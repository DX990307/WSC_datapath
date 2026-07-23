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

	if fill, ok := req.(*mem.RemoteDataFill); ok {
		return p.processRemoteFill(now, fill)
	}

	if read, ok := req.(*mem.ReadReq); ok && read.LookupOnly &&
		!p.cache.remoteReplicaMayContain(read.PID, read.Address) {
		if !p.cache.topSender.CanSend(1) {
			return false
		}
		rsp := mem.CacheLookupRspBuilder{}.
			WithSendTime(now).
			WithSrc(p.cache.topPort).
			WithDst(read.Src).
			WithRspTo(read.ID).
			WithHit(false).
			WithGeneration(p.cache.remoteReplicaGeneration).
			Build()
		p.cache.topSender.Send(rsp)
		p.cache.remoteReplicaStats.FilterNegatives++
		p.cache.topPort.Retrieve(now)
		return true
	}

	if !p.cache.dirStageBuffer.CanPush() {
		return false
	}
	trans := &transaction{id: sim.GetIDGenerator().Generate()}
	switch req := req.(type) {
	case *mem.ReadReq:
		trans.read = req
	case *mem.WriteReq:
		trans.write = req
	}
	if trans.read != nil && !trans.read.LookupOnly {
		// Train exactly once, when the real demand is accepted. A request that
		// is backpressured at the top port must not manufacture spatial evidence.
		p.cache.observeGranularityDemand(now, trans)
		p.cache.observeLocalReadDemand(now, trans.read)
		p.cache.primeAdaptivePairLookups(now, trans)
	}
	if !trans.prefetch {
		p.cache.primeResidentLookup(now, trans)
	}
	p.cache.dirStageBuffer.Push(trans)
	p.acceptTransaction(now, trans)

	return true
}

func (p *topParser) acceptTransaction(
	now sim.VTimeInSec,
	trans *transaction,
) {
	trans.l2Arrival = now
	req := trans.req()

	p.cache.inFlightTransactions = append(p.cache.inFlightTransactions, trans)

	if accessReq := trans.accessReq(); accessReq != nil {
		p.cache.recordObservationL2Utilization(now)
		memtrace.ObservationTransitionByRequest(
			accessReq.Meta().ID, "l2_top_receive", "l2_queue", now)
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
}

func (p *topParser) processRemoteFill(
	now sim.VTimeInSec,
	fill *mem.RemoteDataFill,
) bool {
	if !p.cache.topSender.CanSend(1) {
		return false
	}
	installed := false
	if p.cache.state == cacheStateRunning {
		installed = p.cache.installRemoteDataFill(fill)
	} else {
		p.cache.recordRemoteFillAttempt(fill)
		p.cache.recordRemoteFillDropped(fill)
	}
	rsp := mem.RemoteDataFillRspBuilder{}.
		WithSendTime(now).
		WithSrc(p.cache.topPort).
		WithDst(fill.Src).
		WithRspTo(fill.ID).
		WithInstalled(installed).
		Build()
	p.cache.topSender.Send(rsp)
	p.cache.topPort.Retrieve(now)
	return true
}

func (p *topParser) processRemoteFillWhileStopped(now sim.VTimeInSec) bool {
	msg := p.cache.topPort.Peek()
	fill, ok := msg.(*mem.RemoteDataFill)
	if !ok {
		return false
	}
	return p.processRemoteFill(now, fill)
}
