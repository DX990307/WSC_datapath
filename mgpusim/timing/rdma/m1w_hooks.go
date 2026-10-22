package rdma

import (
	"github.com/sarchlab/akita/v3/mem/mem"
	"github.com/sarchlab/akita/v3/sim"
)

// Hooks of the ISCA'27 wafer M1 bound (m1w_bound.go). Every hook is a nil
// check when the bound is off.

// SetM1WTopology gives the RDMA engine the platform's m1w topology.
func (c *Comp) SetM1WTopology(topo *M1WTopology) {
	c.m1wTopo = topo
	c.m1wIdx = topo.Index(c.ToOutside)
}

// M1WKernelBoundary records a kernel launch at this GPM.
func (c *Comp) M1WKernelBoundary(now sim.VTimeInSec) {
	if m1wBound != nil {
		m1wBound.KernelBoundary(m1wNS(now))
	}
}

func m1wEntryLine(entry *remoteLineEntry, now sim.VTimeInSec) M1WLine {
	need := now
	if len(entry.waiters) > 0 {
		need = entry.waiters[0].arrival
	}
	return M1WLine{
		PID:    uint32(entry.key.pid),
		Line:   remoteLineAddress(entry.key.lineAddr),
		NeedNS: m1wNS(need),
		Demand: !entry.speculative,
	}
}

// m1wSendBatch: a requester batch (one or several lines) left as netID.
func (c *Comp) m1wSendBatch(netID string, batch *remoteBatch, now sim.VTimeInSec) {
	if m1wBound == nil || c.m1wTopo == nil {
		return
	}
	lines := make([]M1WLine, 0, len(batch.lineOrder))
	for _, line := range batch.lineOrder {
		if e := batch.lines[line]; e != nil {
			lines = append(lines, m1wEntryLine(e, now))
		}
	}
	created := batch.createdAt
	if batch.oldest > 0 && batch.oldest < created {
		created = batch.oldest
	}
	m1wBound.Send(netID, c.m1wIdx, c.m1wTopo.Index(batch.dst), lines,
		m1wNS(created), m1wNS(now))
}

// m1wSendEntry: a single line entry left directly as netID.
func (c *Comp) m1wSendEntry(netID string, entry *remoteLineEntry, now sim.VTimeInSec) {
	if m1wBound == nil || c.m1wTopo == nil {
		return
	}
	l := m1wEntryLine(entry, now)
	m1wBound.Send(netID, c.m1wIdx, c.m1wTopo.Index(entry.owner),
		[]M1WLine{l}, l.NeedNS, m1wNS(now))
}

// m1wSendLegacy: a request left on the legacy (baseline) path.
func (c *Comp) m1wSendLegacy(netID string, req mem.AccessReq, dst sim.Port,
	firstSeen, now sim.VTimeInSec) {
	if m1wBound == nil || c.m1wTopo == nil {
		return
	}
	switch r := req.(type) {
	case *mem.ReadReq:
		first := remoteLineAddress(r.Address)
		last := first
		if r.AccessByteSize > 0 {
			last = remoteLineAddress(r.Address + r.AccessByteSize - 1)
		}
		var lines []M1WLine
		for line := first; line <= last; line += remoteLineBytes {
			lines = append(lines, M1WLine{PID: uint32(r.PID), Line: line,
				NeedNS: m1wNS(firstSeen), Demand: true})
		}
		m1wBound.Send(netID, c.m1wIdx, c.m1wTopo.Index(dst), lines,
			m1wNS(firstSeen), m1wNS(now))
	case *mem.WriteReq:
		c.m1wWrite(req)
	}
}

func (c *Comp) m1wWrite(req mem.AccessReq) {
	first := remoteLineAddress(req.GetAddress())
	last := first
	if req.GetByteSize() > 0 {
		last = remoteLineAddress(req.GetAddress() + req.GetByteSize() - 1)
	}
	for line := first; line <= last; line += remoteLineBytes {
		m1wBound.Write(uint32(req.GetPID()), line)
	}
}

// m1wOwnerArrive: a request reached this GPM as the owner.
func (c *Comp) m1wOwnerArrive(msg sim.Msg) {
	if m1wBound == nil {
		return
	}
	m1wBound.OwnerArrive(msg.Meta().ID, m1wNS(msg.Meta().RecvTime))
}

// m1wComplete: the requester transaction netID completed.
func (c *Comp) m1wComplete(netID string, now sim.VTimeInSec) {
	if m1wBound == nil {
		return
	}
	m1wBound.Complete(netID, m1wNS(now))
}
