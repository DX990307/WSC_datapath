package rdma

import (
	"github.com/sarchlab/akita/v3/mem/cache/writeback"
	"github.com/sarchlab/akita/v3/mem/mem"
	"github.com/sarchlab/akita/v3/mem/vm"
	"github.com/sarchlab/akita/v3/sim"
)

// ISCA'27 comparison designs (writeback/cmp_coherence.go): the network side.
//
// HMG: an L2 miss to a remote address goes to the next hop of the
// hierarchy (region home, or system home); a region home receives such a
// read from another GPM of its region as a relay: it is handled by the same
// requester remote data path as the GPM's own L1 misses (probe of its L2,
// merge with reads in flight, its own window to the system home, fill of its
// L2), and the data goes back to the requester over the network.
// Invalidations travel as CohInvalMsg between RDMA engines.
//
// CARVE: after an L2 probe miss, the line is looked up in the GPM's Remote
// Data Cache in local DRAM (one 128-B DRAM read at the RDC set; tag and data
// in one access). A hit serves the waiters and fills the L2; a miss goes to
// the home, and the 128-B RDC line is completed by fetching the other 64-B
// half from the home; the line is then written to the RDC (one 128-B DRAM
// write).

// CohInvalMsg is a coherence invalidation of one line group.
type CohInvalMsg struct {
	sim.MsgMeta
	Group uint64
}

// Meta returns the meta data of the message.
func (m *CohInvalMsg) Meta() *sim.MsgMeta { return &m.MsgMeta }

// CmpStats counts the network-side work of the comparison design at one GPM.
type CmpStats struct {
	InvalSent, InvalRecv                       uint64
	RoutedRegion, RoutedHome                   uint64
	RelaysIn, RelayServedL2, RelayServedRemote uint64
	RDCProbes, RDCHits, RDCMisses              uint64
	RDCSiblingFetches, RDCInsertsDone          uint64
	RDCInsertSkips, RDCDRAMWrites              uint64
}

type cohOut struct {
	to sim.Port
	g  uint64
}

type rdcFill struct {
	grp    uint64
	pid    vm.PID
	owner  sim.Port
	halves [2][]byte
	age    sim.VTimeInSec
	sib    uint64
	info   interface{}
}

type cmpState struct {
	t       *writeback.CmpCoherence
	self    int
	ports   map[int]sim.Port
	fillLRU bool
	out     []cohOut
	relayQ  []*mem.ReadReq
	stats   CmpStats
	// CARVE
	toDRAM     sim.Port
	dram       mem.LowModuleFinder
	rdcBase    uint64
	dramQ      []mem.AccessReq
	rdcProbes  map[string]*remoteLineEntry
	rdcFills   map[uint64]*rdcFill
	sibQ       []*rdcFill
	sibFlight  map[string]*rdcFill
	dramWrites map[string]bool
}

// EnableCmp turns on the comparison design at this engine. ports maps every
// GPM id to its RDMA ToOutside port.
func (c *Comp) EnableCmp(t *writeback.CmpCoherence, self int, ports map[int]sim.Port, fillLRU bool) {
	c.cmp = &cmpState{
		t: t, self: self, ports: ports, fillLRU: fillLRU,
		rdcProbes:  make(map[string]*remoteLineEntry),
		rdcFills:   make(map[uint64]*rdcFill),
		sibFlight:  make(map[string]*rdcFill),
		dramWrites: make(map[string]bool),
	}
	if c.rdcPort != nil && c.rdcFinder != nil {
		c.cmp.toDRAM = c.rdcPort
		c.cmp.dram = c.rdcFinder
		c.cmp.rdcBase = c.rdcBase
	}
}

// EnableRDCPort creates the port that accesses the GPM's DRAM for the RDC.
func (c *Comp) EnableRDCPort(bufferSize int) sim.Port {
	p := sim.NewLimitNumMsgPort(c, bufferSize, c.Name()+".ToDRAM")
	c.AddPort("ToDRAM", p)
	c.rdcPort = p
	return p
}

// SetRDC sets the DRAM finder and the base address of the RDC carve-out
// (taken over by EnableCmp).
func (c *Comp) SetRDC(dram mem.LowModuleFinder, base uint64) {
	c.rdcFinder = dram
	c.rdcBase = base
}

// QueueCohInval queues an invalidation of group to GPM to.
func (c *Comp) QueueCohInval(now sim.VTimeInSec, to int, g uint64) {
	c.cmp.out = append(c.cmp.out, cohOut{to: c.cmp.ports[to], g: g})
	c.TickLater(now)
}

// GetCmpStats returns the counters (zero when off).
func (c *Comp) GetCmpStats() CmpStats {
	if c.cmp == nil {
		return CmpStats{}
	}
	return c.cmp.stats
}

func (c *Comp) cmpHMG() bool { return c.cmp != nil && c.cmp.t.Mode() == writeback.CmpHMG }

func (c *Comp) cmpCARVE() bool {
	return c.cmp != nil && c.cmp.t.Mode() == writeback.CmpCARVE && c.cmp.dram != nil
}

// cmpRoute returns the network destination of a read of line owned by owner.
func (c *Comp) cmpRoute(owner sim.Port, line uint64) sim.Port {
	if !c.cmpHMG() {
		return owner
	}
	n := c.cmp.t.NextHop(c.cmp.self, line)
	if n == c.cmp.t.Home(line) {
		c.cmp.stats.RoutedHome++
		return owner
	}
	c.cmp.stats.RoutedRegion++
	return c.cmp.ports[n]
}

// cmpFromOutside handles a message from the network that belongs to the
// comparison design.
func (c *Comp) cmpFromOutside(now sim.VTimeInSec, msg sim.Msg) bool {
	if c.cmp == nil {
		return false
	}
	switch m := msg.(type) {
	case *CohInvalMsg:
		c.ToOutside.Retrieve(now)
		c.cmp.stats.InvalRecv++
		c.cmp.t.OnInval(now, c.cmp.self, m.Group)
		return true
	case *mem.ReadReq:
		if !c.cmpHMG() || c.cmp.t.Home(m.Address) == c.cmp.self {
			return false
		}
		c.ToOutside.Retrieve(now)
		c.cmp.stats.RelaysIn++
		c.cmp.relayQ = append(c.cmp.relayQ, m)
		return true
	}
	return false
}

func (c *Comp) cmpTick(now sim.VTimeInSec) bool {
	s := c.cmp
	progress := false
	for len(s.out) > 0 {
		o := s.out[0]
		msg := &CohInvalMsg{Group: o.g}
		msg.ID = sim.GetIDGenerator().Generate()
		msg.Src = c.ToOutside
		msg.Dst = o.to
		msg.SendTime = now
		msg.TrafficBytes = 12
		if err := c.ToOutside.Send(msg); err != nil {
			break
		}
		s.out = s.out[1:]
		s.stats.InvalSent++
		progress = true
	}
	for i := 0; i < c.effectivePipelineWidth() && len(s.relayQ) > 0; i++ {
		read := s.relayQ[0]
		dst := c.RemoteRDMAAddressTable.Find(read.Address)
		c.relayActive = true
		handled, p := c.tryProcessRemoteReqFromL1(now, read, dst, now)
		c.relayActive = false
		if !handled {
			panic("HMG relay: read not handled by the remote data path")
		}
		if !p {
			break
		}
		progress = true
	}
	if s.dram != nil {
		progress = c.rdcTick(now) || progress
	}
	return progress
}

// consumeRelay removes the relay being processed (the L1 path retrieves
// from ToL1 instead).
func (c *Comp) consumeRelay() {
	c.cmp.relayQ = c.cmp.relayQ[1:]
}

// sendRelayRsp answers a relayed read over the network; it reports the age
// of the data with the response.
func (c *Comp) sendRelayRsp(now sim.VTimeInSec, entry *remoteLineEntry, w remoteWaiter) bool {
	rsp := mem.DataReadyRspBuilder{}.
		WithSendTime(now).
		WithSrc(c.ToOutside).
		WithDst(w.req.Src).
		WithRspTo(w.req.ID).
		WithData(append([]byte(nil), entry.data...)).
		Build()
	if err := c.ToOutside.Send(rsp); err != nil {
		return false
	}
	age := entry.created
	if entry.hasAge {
		age = entry.age
	}
	if !entry.fromRemote {
		c.cmp.stats.RelayServedL2++
		if a, ok := c.cmpReplicaAge(entry); ok {
			age = a
		}
	} else {
		c.cmp.stats.RelayServedRemote++
	}
	c.cmp.t.Age[rsp.ID] = age
	return true
}

func (c *Comp) cmpReplicaAge(entry *remoteLineEntry) (sim.VTimeInSec, bool) {
	if c.ReplicaAgeFunc == nil {
		return 0, false
	}
	return c.ReplicaAgeFunc(entry.key.pid, entry.key.lineAddr)
}

// cmpNoteRsp takes the age reported with a network response.
func (c *Comp) cmpNoteRsp(entry *remoteLineEntry, rsp sim.Msg) {
	if c.cmp == nil {
		return
	}
	if a, ok := c.cmp.t.Age[rsp.Meta().ID]; ok {
		delete(c.cmp.t.Age, rsp.Meta().ID)
		entry.age = a
		entry.hasAge = true
	}
	if f := entry.rdcFill; f != nil {
		entry.rdcFill = nil
		c.rdcHalf(f, entry.key.lineAddr, entry.data, entry.created)
	}
}

// --- CARVE RDC ---

func (c *Comp) rdcAddr(line uint64) uint64 {
	return c.cmp.rdcBase + c.cmp.t.RDCSetOf(line)*128
}

// rdcMayProbe tells whether a pending entry must first look up the RDC.
func (c *Comp) rdcMayProbe(entry *remoteLineEntry) bool {
	return c.cmpCARVE() && !entry.rdcProbed && !entry.boundaryStale &&
		!entry.speculative && !c.remoteUncacheable[entry.key.remoteLineIdentity]
}

// rdcStartProbe issues the RDC lookup of entry (one 128-B DRAM read).
func (c *Comp) rdcStartProbe(now sim.VTimeInSec, entry *remoteLineEntry) {
	entry.rdcProbed = true
	entry.state = remoteLineProbing
	addr := c.rdcAddr(entry.key.lineAddr)
	req := mem.ReadReqBuilder{}.
		WithSendTime(now).
		WithSrc(c.cmp.toDRAM).
		WithDst(c.cmp.dram.Find(addr)).
		WithAddress(addr).
		WithByteSize(128).
		Build()
	c.cmp.rdcProbes[req.ID] = entry
	c.cmp.dramQ = append(c.cmp.dramQ, req)
	c.cmp.stats.RDCProbes++
}

func (c *Comp) rdcTick(now sim.VTimeInSec) bool {
	s := c.cmp
	progress := false
	for len(s.dramQ) > 0 {
		req := s.dramQ[0]
		req.Meta().SendTime = now
		if err := s.toDRAM.Send(req); err != nil {
			break
		}
		s.dramQ = s.dramQ[1:]
		progress = true
	}
	for i := 0; i < c.effectivePipelineWidth(); i++ {
		msg := s.toDRAM.Peek()
		if msg == nil {
			break
		}
		switch rsp := msg.(type) {
		case *mem.DataReadyRsp:
			entry := s.rdcProbes[rsp.RespondTo]
			if entry == nil {
				panic("RDC: unknown DRAM read response")
			}
			delete(s.rdcProbes, rsp.RespondTo)
			c.rdcProbeDone(now, entry)
		case *mem.WriteDoneRsp:
			delete(s.dramWrites, rsp.RespondTo)
		default:
			panic("RDC: unexpected DRAM message")
		}
		s.toDRAM.Retrieve(now)
		progress = true
	}
	for len(s.sibQ) > 0 {
		f := s.sibQ[0]
		if c.m1Composed() && !c.m1CanSend(f.owner) {
			break
		}
		req := mem.ReadReqBuilder{}.
			WithSendTime(now).
			WithSrc(c.ToOutside).
			WithDst(f.owner).
			WithAddress(f.sib).
			WithByteSize(remoteLineBytes).
			WithPID(f.pid).
			WithInfo(f.info).
			Build()
		if err := c.ToOutside.Send(req); err != nil {
			break
		}
		if c.m1Composed() {
			c.m1Acquire(now, f.owner, req.ID)
		}
		s.sibQ = s.sibQ[1:]
		s.sibFlight[req.ID] = f
		s.stats.RDCSiblingFetches++
		if f.age == 0 || now < f.age {
			f.age = now
		}
		progress = true
	}
	return progress
}

func (c *Comp) rdcProbeDone(now sim.VTimeInSec, entry *remoteLineEntry) {
	s := c.cmp
	data, age, hit := s.t.RDCLookup(s.self, entry.key.lineAddr, remoteLineBytes)
	if hit && !entry.boundaryStale {
		s.stats.RDCHits++
		entry.data = data
		entry.state = remoteLineReady
		entry.fromRemote = false
		entry.fromRDC = true
		entry.age = age
		entry.hasAge = true
		c.m1UnindexLine(entry)
		c.queueRemoteReady(entry)
		if c.RemoteReplicaHitHook != nil {
			n := len(entry.waiters)
			if n == 0 {
				n = 1
			}
			for i := 0; i < n; i++ {
				c.RemoteReplicaHitHook(now, entry.key.lineAddr)
			}
		}
		return
	}
	s.stats.RDCMisses++
	entry.state = remoteLinePendingBatch
	c.remotePendingBatch = append(c.remotePendingBatch, entry)
	grp := s.t.Group(entry.key.lineAddr)
	if s.rdcFills[grp] != nil || entry.boundaryStale {
		return
	}
	sib := grp + 64
	if entry.key.lineAddr != grp {
		sib = grp
	}
	f := &rdcFill{grp: grp, pid: entry.key.pid, owner: entry.owner, sib: sib, info: entry.info}
	s.rdcFills[grp] = f
	entry.rdcFill = f
	s.sibQ = append(s.sibQ, f)
}

// cmpSiblingRsp handles the response of a sibling fetch; false = not one.
func (c *Comp) cmpSiblingRsp(now sim.VTimeInSec, rsp mem.AccessRsp) bool {
	if c.cmp == nil {
		return false
	}
	f := c.cmp.sibFlight[rsp.GetRspTo()]
	if f == nil {
		return false
	}
	data, ok := rsp.(*mem.DataReadyRsp)
	if !ok {
		panic("RDC sibling fetch returned no data")
	}
	delete(c.cmp.sibFlight, rsp.GetRspTo())
	if c.m1Composed() {
		c.m1Release(now, rsp.GetRspTo())
	}
	c.ToOutside.Retrieve(now)
	c.rdcHalf(f, f.sib, data.Data, f.age)
	return true
}

func (c *Comp) rdcHalf(f *rdcFill, line uint64, data []byte, age sim.VTimeInSec) {
	h := 0
	if line != f.grp {
		h = 1
	}
	f.halves[h] = append([]byte(nil), data...)
	if f.age == 0 || age < f.age {
		f.age = age
	}
	if f.halves[0] == nil || f.halves[1] == nil {
		return
	}
	s := c.cmp
	delete(s.rdcFills, f.grp)
	now := c.Engine.CurrentTime()
	id0 := remoteLineIdentity{pid: f.pid, lineAddr: f.grp, ownerName: f.owner.Name()}
	id1 := id0
	id1.lineAddr = f.grp + 64
	if f.age < s.t.Boundary(s.self) || c.remoteUncacheable[id0] || c.remoteUncacheable[id1] {
		s.stats.RDCInsertSkips++
		return
	}
	full := append(append([]byte(nil), f.halves[0]...), f.halves[1]...)
	s.t.RDCInsert(s.self, f.grp, f.pid, full, f.age)
	s.stats.RDCInsertsDone++
	addr := c.rdcAddr(f.grp)
	w := mem.WriteReqBuilder{}.
		WithSendTime(now).
		WithSrc(s.toDRAM).
		WithDst(s.dram.Find(addr)).
		WithAddress(addr).
		WithData(full).
		Build()
	s.dramWrites[w.ID] = true
	s.dramQ = append(s.dramQ, w)
	s.stats.RDCDRAMWrites++
	c.TickLater(now)
}

func (c *Comp) cmpHasPendingWork() bool {
	if c.cmp == nil {
		return false
	}
	s := c.cmp
	return len(s.out) > 0 || len(s.relayQ) > 0 || len(s.dramQ) > 0 ||
		len(s.rdcProbes) > 0 || len(s.sibQ) > 0 || len(s.sibFlight) > 0
}
