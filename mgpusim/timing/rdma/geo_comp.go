package rdma

import (
	"github.com/sarchlab/akita/v3/mem/mem"
	"github.com/sarchlab/akita/v3/mem/vm"
	"github.com/sarchlab/akita/v3/sim"
)

// SetGeoTopology gives the RDMA engine the platform's geo topology (geo-push
// and its upper-bound statistics). Without it every geo hook is a no-op.
func (c *Comp) SetGeoTopology(topo *GeoTopology) {
	c.geoTopo = topo
	c.geoIdx = topo.Index(c.ToOutside)
}

// GeoKernelBoundary records a kernel launch at this GPM (geo statistics and
// geo-push staleness checks).
func (c *Comp) GeoKernelBoundary(now sim.VTimeInSec) {
	c.geoLastBoundary = now
	c.geoBoundarySet = true
	if geoBound != nil {
		geoBound.KernelBoundary(c.geoIdx, vtNS(now))
	}
}

// geoBoundDeliver: this GPM received line (pid, line) from owner.
func (c *Comp) geoBoundDeliver(
	owner sim.Port, pid vm.PID, line uint64, send, now sim.VTimeInSec,
) {
	if geoBound == nil || c.geoTopo == nil {
		return
	}
	geoBound.Deliver(c.geoIdx, c.geoTopo.Index(owner), uint32(pid),
		remoteLineAddress(line), vtNS(send), vtNS(now))
}

// geoBoundNetRead: this GPM sent a demand read of (pid, line) to owner.
func (c *Comp) geoBoundNetRead(
	owner sim.Port, pid vm.PID, line uint64, need, now sim.VTimeInSec,
) {
	if geoBound == nil || c.geoTopo == nil {
		return
	}
	geoBound.NetRead(c.geoIdx, c.geoTopo.Index(owner), uint32(pid),
		remoteLineAddress(line), vtNS(need), vtNS(now))
}

// geoBoundEntryNetRead records the demand line of an entry put on the wire.
func (c *Comp) geoBoundEntryNetRead(
	owner sim.Port, entry *remoteLineEntry, now sim.VTimeInSec,
) {
	if geoBound == nil || entry == nil || entry.speculative {
		return
	}
	need := now
	if len(entry.waiters) > 0 {
		need = entry.waiters[0].arrival
	}
	c.geoBoundNetRead(owner, entry.key.pid, entry.key.lineAddr, need, now)
}

// geoBoundWrite: a remote write left this GPM.
func (c *Comp) geoBoundWrite(req mem.AccessReq) {
	if geoBound == nil {
		return
	}
	first := remoteLineAddress(req.GetAddress())
	last := first
	if req.GetByteSize() > 0 {
		last = remoteLineAddress(req.GetAddress() + req.GetByteSize() - 1)
	}
	for line := first; line <= last; line += remoteLineBytes {
		geoBound.Write(uint32(req.GetPID()), line)
	}
}
