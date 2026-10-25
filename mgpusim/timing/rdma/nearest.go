package rdma

import (
	"github.com/sarchlab/akita/v3/mem/mem"
	"github.com/sarchlab/akita/v3/sim"
)

// ISCA27 r5-distance experiment A: timing upper bound of "read from the
// nearest copy". When a requester sends a single-line remote read over the
// network and another GPM's L2 currently holds the line as an M3 replica that
// the oracle tracks (fetched in this kernel, after the last write to the line,
// and still valid in that L2), the read goes to the holder nearest to the
// requester (only if strictly closer than the home) instead of the home. The
// request and the data cross the real mesh with real queueing; finding the
// holder is free (oracle). At the holder the RDMA engine reads its own L2 with
// a lookup-only read (a normal L2 access); on a miss (the line was evicted in
// between) the holder forwards the read to the home and relays the data.
// Without an oracle (nil) nothing changes.

// NearestOracle is shared by every RDMA engine of the platform.
type NearestOracle struct {
	// Holds reports whether engine e's L2 holds a valid remote replica of
	// line (set by the platform builder).
	Holds func(e *Comp, line uint64) bool

	engines  []*Comp
	index    map[*Comp]int
	byPort   map[sim.Port]int
	lines    map[uint64][]uint64 // line -> holder bitmap (engine index)
	lastW    map[uint64]sim.VTimeInSec
	boundary sim.VTimeInSec
	kernels  int
	holderRq map[string]bool // network request IDs addressed to a holder

	Stats NearestOracleStats
}

// NearestOracleStats are the oracle's counters (platform-wide).
type NearestOracleStats struct {
	Picks          uint64  // single-line network reads considered
	Redirected     uint64  // reads sent to a holder
	HopsHome       float64 // hops to the home of all considered reads
	HopsServed     float64 // hops to where they were sent
	HolderHits     uint64  // holder L2 lookups that hit
	HolderMisses   uint64  // ... missed (forwarded to the home)
	FillsNoted     uint64
	FillsStale     uint64 // fills not registered (read opened before a write)
	WritesNoted    uint64
	Kernels        uint64
	ServedByEngine []uint64 // reads served per GPM (home reads + holder hits)
	HomeByEngine   []uint64 // reads that would have gone to each home
}

// NewNearestOracle creates the oracle over the platform's RDMA engines (in
// GPM order) and attaches it to them.
func NewNearestOracle(engines []*Comp) *NearestOracle {
	o := &NearestOracle{
		engines:  engines,
		index:    make(map[*Comp]int, len(engines)),
		byPort:   make(map[sim.Port]int, len(engines)),
		lines:    make(map[uint64][]uint64),
		lastW:    make(map[uint64]sim.VTimeInSec),
		holderRq: make(map[string]bool),
	}
	o.Stats.ServedByEngine = make([]uint64, len(engines))
	o.Stats.HomeByEngine = make([]uint64, len(engines))
	for i, e := range engines {
		o.index[e] = i
		o.byPort[e.ToOutside] = i
		e.nearest = o
	}
	return o
}

// KernelBoundary is called at every GPM's kernel launch; launches less than
// 1 us apart count as one boundary. The holder sets are per kernel.
func (o *NearestOracle) KernelBoundary(now sim.VTimeInSec) {
	if o.kernels > 0 && float64(now-o.boundary) < 1e-6 {
		o.boundary = now
		return
	}
	o.kernels++
	o.Stats.Kernels++
	o.boundary = now
	o.lines = make(map[uint64][]uint64)
	o.lastW = make(map[uint64]sim.VTimeInSec)
}

// NoteWrite drops the holders of every line in [addr, addr+size) (a write
// at the home, by any writer).
func (o *NearestOracle) NoteWrite(now sim.VTimeInSec, addr, size uint64) {
	if size == 0 {
		size = 1
	}
	o.Stats.WritesNoted++
	for a := addr &^ (remoteLineBytes - 1); a < addr+size; a += remoteLineBytes {
		delete(o.lines, a)
		o.lastW[a] = now
	}
}

// NoteFill registers engine e as a holder of line: a remote fill arrived at
// its L2 for a read opened at reqTime.
func (o *NearestOracle) NoteFill(e *Comp, reqTime sim.VTimeInSec, line uint64) {
	i, ok := o.index[e]
	if !ok {
		return
	}
	line &^= remoteLineBytes - 1
	o.Stats.FillsNoted++
	if reqTime < o.boundary || reqTime < o.lastW[line] {
		o.Stats.FillsStale++
		return
	}
	h := o.lines[line]
	if h == nil {
		h = make([]uint64, (len(o.engines)+63)/64)
		o.lines[line] = h
	}
	h[i>>6] |= 1 << uint(i&63)
}

func (o *NearestOracle) hops(a, b int) int {
	return t18Hops(o.engines[a].t18Topo.self, o.engines[b].t18Topo.self)
}

// pick returns the holder engine nearest to requester r for line homed at
// the engine with port home, if one is strictly closer than the home.
func (o *NearestOracle) pick(r *Comp, line uint64, home sim.Port) (sim.Port, bool) {
	ri, ok := o.index[r]
	hi, ok2 := o.byPort[home]
	if !ok || !ok2 || r.t18Topo == nil {
		return nil, false
	}
	line &^= remoteLineBytes - 1
	o.Stats.Picks++
	o.Stats.HomeByEngine[hi]++
	hHome := o.hops(ri, hi)
	o.Stats.HopsHome += float64(hHome)
	best, bh := -1, hHome
	for w, x := range o.lines[line] {
		for ; x != 0; x &= x - 1 {
			g := w*64 + trailingZeros64(x)
			if g == ri || g == hi {
				continue
			}
			if d := o.hops(ri, g); d < bh && o.Holds(o.engines[g], line) {
				best, bh = g, d
			}
		}
	}
	o.Stats.HopsServed += float64(bh)
	if best < 0 {
		return nil, false
	}
	o.Stats.Redirected++
	return o.engines[best].ToOutside, true
}

func trailingZeros64(x uint64) int {
	n := 0
	for x&1 == 0 {
		x >>= 1
		n++
	}
	return n
}

func (c *Comp) nearestLive() int {
	return len(c.nearProbes) + len(c.nearFwd)
}

// nearestServe accepts a request addressed to this engine as a holder: it
// reads the line from this GPM's own L2 (lookup only).
func (c *Comp) nearestServe(now sim.VTimeInSec, req *mem.ReadReq) bool {
	if !c.canAcceptOwnerOutstanding(1) {
		return false
	}
	probe := mem.ReadReqBuilder{}.
		WithSendTime(now).
		WithSrc(c.ToL2).
		WithDst(c.remoteCacheModules.Find(req.Address)).
		WithAddress(req.Address).
		WithByteSize(req.AccessByteSize).
		WithPID(req.PID).
		WithInfo(req.Info).
		WithLookupOnly().
		Build()
	if err := c.ToL2.Send(probe); err != nil {
		return false
	}
	delete(c.nearest.holderRq, req.ID)
	if c.nearProbes == nil {
		c.nearProbes = make(map[string]*mem.ReadReq)
		c.nearFwd = make(map[string]*mem.ReadReq)
	}
	c.nearProbes[probe.ID] = req
	c.ToOutside.Retrieve(now)
	return true
}

// nearestProbeRsp handles this holder's L2 lookup result.
func (c *Comp) nearestProbeRsp(now sim.VTimeInSec, rsp *mem.CacheLookupRsp) bool {
	req := c.nearProbes[rsp.RespondTo]
	o := c.nearest
	if rsp.Hit {
		out := mem.DataReadyRspBuilder{}.
			WithSendTime(now).
			WithSrc(c.ToOutside).
			WithDst(req.Src).
			WithRspTo(req.ID).
			WithData(append([]byte(nil), rsp.Data...)).
			Build()
		if err := c.ToOutside.Send(out); err != nil {
			return false
		}
		o.Stats.HolderHits++
		o.Stats.ServedByEngine[o.index[c]]++
	} else {
		fwd := mem.ReadReqBuilder{}.
			WithSendTime(now).
			WithSrc(c.ToOutside).
			WithDst(c.RemoteRDMAAddressTable.Find(req.Address)).
			WithAddress(req.Address).
			WithByteSize(req.AccessByteSize).
			WithPID(req.PID).
			WithInfo(req.Info).
			Build()
		if err := c.ToOutside.Send(fwd); err != nil {
			return false
		}
		o.Stats.HolderMisses++
		o.Stats.ServedByEngine[o.byPort[fwd.Dst]]++
		c.nearFwd[fwd.ID] = req
	}
	delete(c.nearProbes, rsp.RespondTo)
	c.ToL2.Retrieve(now)
	return true
}

// nearestFwdRsp relays the home's data of a forwarded read.
func (c *Comp) nearestFwdRsp(now sim.VTimeInSec, rsp *mem.DataReadyRsp) bool {
	req := c.nearFwd[rsp.RespondTo]
	out := mem.DataReadyRspBuilder{}.
		WithSendTime(now).
		WithSrc(c.ToOutside).
		WithDst(req.Src).
		WithRspTo(req.ID).
		WithData(append([]byte(nil), rsp.Data...)).
		Build()
	if err := c.ToOutside.Send(out); err != nil {
		return false
	}
	delete(c.nearFwd, rsp.RespondTo)
	c.ToOutside.Retrieve(now)
	return true
}

// NearestOracleOf returns the oracle attached to this engine (nil when off).
func (c *Comp) NearestOracleOf() *NearestOracle { return c.nearest }

// RemoteCacheSlice is the port of this GPM's L2 slice that holds (or would
// hold) addr, local or remote.
func (c *Comp) RemoteCacheSlice(addr uint64) sim.Port {
	if c.remoteCacheModules == nil {
		return nil
	}
	return c.remoteCacheModules.Find(addr)
}
