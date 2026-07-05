package writearound

import (
	"github.com/sarchlab/akita/v3/mem/cache"
	"github.com/sarchlab/akita/v3/mem/mem"
	"github.com/sarchlab/akita/v3/sim"
)

func (d *directory) tryM1DirectDramBypass(
	now sim.VTimeInSec,
	trans *transaction,
	victim *cache.Block,
) bool {
	if !d.cache.m1DirectDramBypassEnabled ||
		trans == nil ||
		trans.read == nil ||
		victim == nil ||
		d.cache.directDramPort == nil ||
		d.cache.m1DirectDramFinder == nil {
		return false
	}

	addr := trans.Address()
	pid := trans.PID()
	blockSize := uint64(1 << d.cache.log2BlockSize)
	cacheLineID := addr / blockSize * blockSize

	l2Port := d.cache.lowModuleFinder.Find(cacheLineID)
	if l2Port == nil || d.cache.isRemoteBottomModule(l2Port) {
		return false
	}

	d.cache.m1Stats.L1VDirectBypassLookups++
	target, ok := d.cache.m1DirectBypassTargets[l2Port.Name()]
	if !ok || target.Predictor == nil || target.CleanFillPort == nil {
		d.cache.m1Stats.L1VDirectBypassBlocked++
		return false
	}
	if !target.Predictor.PredictM1DirectDramBypass(pid, cacheLineID) {
		d.cache.m1Stats.L1VDirectBypassBlocked++
		return false
	}
	d.cache.m1Stats.L1VDirectBypassPredAbsent++

	dramPort := d.cache.m1DirectDramFinder.Find(cacheLineID)
	if dramPort == nil {
		d.cache.m1Stats.L1VDirectBypassBlocked++
		return false
	}

	trans.directDramBypass = true
	trans.directDramPort = d.cache.directDramPort
	trans.l2FillPort = target.CleanFillPort
	if !d.cache.enqueueM1DirectDRAMBatch(
		now, trans, cacheLineID, dramPort) {
		d.cache.m1Stats.L1VDirectBypassBlocked++
		trans.directDramBypass = false
		trans.directDramPort = nil
		trans.l2FillPort = nil
		return false
	}

	trans.block = victim

	mshrEntry := d.cache.mshr.Add(pid, cacheLineID)
	mshrEntry.Requests = append(mshrEntry.Requests, trans)
	mshrEntry.Block = victim

	victim.Tag = cacheLineID
	victim.PID = pid
	victim.IsValid = true
	victim.IsLocked = true
	d.cache.directory.Visit(victim)

	return true
}

func (p *bottomParser) sendM1DirectL2Fill(
	now sim.VTimeInSec,
	trans *transaction,
	data []byte,
) {
	if trans == nil || !trans.directDramBypass || trans.l2FillPort == nil {
		return
	}
	if trans.directDramPort == nil || !trans.directDramPort.CanSend() {
		p.cache.m1Stats.L1VDirectL2FillDropped++
		return
	}

	cachelineID := (trans.Address() >> p.cache.log2BlockSize) <<
		p.cache.log2BlockSize
	fill := mem.CleanDataFillBuilder{}.
		WithSendTime(now).
		WithSrc(trans.directDramPort).
		WithDst(trans.l2FillPort).
		WithPID(trans.PID()).
		WithAddress(cachelineID).
		WithData(append([]byte(nil), data...)).
		WithInfo(accessReqInfo(trans.accessReq())).
		Build()
	if err := trans.directDramPort.Send(fill); err != nil {
		p.cache.m1Stats.L1VDirectL2FillDropped++
		return
	}
	p.cache.m1Stats.L1VDirectL2FillIssued++
}
