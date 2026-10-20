package rdma

import (
	"fmt"

	"github.com/sarchlab/akita/v3/mem/mem"
	memtrace "github.com/sarchlab/akita/v3/mem/trace"
	"github.com/sarchlab/akita/v3/sim"
)

// T18 requester side.
//
// The in-flight table (a bounded cuckoo hash table keyed by the 64-B line) is
// the requester's only transaction-tracking structure while T18 is on: every
// accepted remote request takes one slot until its response arrives. Full-line
// reads are "open" (mergeable); writes and other requests take a closed slot.
//
// A new full-line read that finds an open entry for its line (full-key check,
// never on a fingerprint alone) joins that entry's waiter list and is not
// sent. A miss allocates a slot and queues the request in its destination's
// FIFO until the window (and, with distance credits, the destination's credit
// or a borrowable idle credit) admits it. The response wakes all waiters and
// deletes the entry.
//
// Ordering. Requests of one destination leave in arrival order (per-destination
// FIFO), so same-line requests keep their order. A write to a line closes the
// line's open entry: reads that arrive after the write never join a read that
// was accepted before it, so they observe the write as in the baseline.

type t18Waiter struct {
	req     mem.AccessReq
	arrival sim.VTimeInSec
}

type t18ReqEntry struct {
	key     t18Key
	dst     sim.Port
	dstIdx  int
	open    bool
	inTable bool
	sent    bool
	seq     uint64
	sentAt  sim.VTimeInSec
	netReq  mem.AccessReq
	waiters []t18Waiter
}

type t18L1Rsp struct {
	msg     sim.Msg
	arrival sim.VTimeInSec
	owner   string
	bytes   uint64
	op      string
}

const t18FanoutCap = 64

type t18State struct {
	cfg  T18Config
	topo *t18Topology
	hops []int

	reqActive   bool
	reqTable    *t18CuckooTable[t18ReqEntry]
	reqQueues   [][]*t18ReqEntry
	queued      int
	credits     *t18CreditAllocator
	reqByNetID  map[string]*t18ReqEntry
	waiters     int
	waiterCap   int
	seq         uint64
	fanout      []t18L1Rsp
	lastStallID string
	lastIntegNS sim.VTimeInSec
	hopInflight [t18MaxHops]int

	ownerActive   bool
	ownerWindow   int
	ownerTable    *t18CuckooTable[t18OwnerEntry]
	ownerByL2ID   map[string]*t18OwnerEntry
	ownerLive     int
	ownerRspQueue []*t18OwnerEntry
	// last read counted as a fallback (a blocked head is retried every cycle)
	ownerFallbackID string

	stats T18Stats
}

// ConfigureT18 enables T18 on this RDMA engine. It must be called before the
// simulation starts and after SetT18Topology when the requester side is on.
func (c *Comp) ConfigureT18(cfg T18Config) {
	if !cfg.Enabled() {
		c.t18 = nil
		return
	}
	if c.remoteConfig.Enabled {
		panic("T18 and the old remote data path (-remote-data-path-enable) " +
			"are mutually exclusive")
	}
	if cfg.Ways <= 0 {
		cfg.Ways = 4
	}
	if cfg.FingerprintBits <= 0 {
		cfg.FingerprintBits = 12
	}
	if cfg.MaxKicks <= 0 {
		cfg.MaxKicks = 32
	}
	base := c.maxOutstanding
	if base <= 0 {
		base = 64
	}
	window := cfg.Window
	if window <= 0 {
		window = base
	}
	ownerWindow := cfg.OwnerWindow
	if ownerWindow <= 0 {
		if cfg.Window > 0 {
			ownerWindow = cfg.Window
		} else {
			ownerWindow = base
		}
	}
	s := &t18State{cfg: cfg, ownerWindow: ownerWindow}
	s.stats.Enabled = true
	s.stats.ReqMergeEnabled = cfg.ReqMerge
	s.stats.OwnerMergeEnabled = cfg.OwnerMerge
	s.stats.MulticastEnabled = cfg.OwnerMerge && cfg.OwnerMulticast
	s.stats.DistCredits = cfg.DistCredits
	s.stats.OwnerWindow = uint64(ownerWindow)

	if cfg.RequesterActive() {
		s.reqActive = true
		s.stats.RequesterActive = true
		s.stats.Window = uint64(window)
		entries := cfg.ReqEntries
		if entries <= 0 {
			entries = 2 * window
		}
		buckets := (entries + cfg.Ways - 1) / cfg.Ways
		s.reqTable = newT18CuckooTable[t18ReqEntry](
			buckets, cfg.Ways, uint(cfg.FingerprintBits), cfg.MaxKicks)
		s.waiterCap = cfg.ReqWaiters
		if s.waiterCap <= 0 {
			s.waiterCap = 4 * s.reqTable.Capacity()
		}
		s.reqByNetID = make(map[string]*t18ReqEntry)
		s.stats.ReqTableSlots = uint64(s.reqTable.Capacity())
		s.stats.ReqWaiterCapacity = uint64(s.waiterCap)
		c.t18Window = window
	}
	if cfg.OwnerMerge {
		s.ownerActive = true
		entries := cfg.OwnerEntries
		if entries <= 0 {
			entries = 2 * ownerWindow
		}
		buckets := (entries + cfg.Ways - 1) / cfg.Ways
		s.ownerTable = newT18CuckooTable[t18OwnerEntry](
			buckets, cfg.Ways, uint(cfg.FingerprintBits), cfg.MaxKicks)
		s.ownerByL2ID = make(map[string]*t18OwnerEntry)
		s.stats.OwnerTableSlots = uint64(s.ownerTable.Capacity())
	}
	c.t18 = s
	if c.t18Topo != nil {
		c.t18BuildCredits()
	}
}

// SetT18Topology records the mesh coordinates of this engine and of every
// RDMA engine it can reach (keyed by their ToOutside ports).
func (c *Comp) SetT18Topology(self [2]int, ports []sim.Port, coords [][2]int) {
	if len(ports) != len(coords) {
		panic("t18 topology: ports and coordinates differ in length")
	}
	topo := &t18Topology{
		self:  self,
		ports: append([]sim.Port(nil), ports...),
		coord: append([][2]int(nil), coords...),
		index: make(map[sim.Port]int, len(ports)),
	}
	for i, p := range ports {
		topo.index[p] = i
	}
	c.t18Topo = topo
	if c.t18 != nil {
		c.t18BuildCredits()
	}
}

func (c *Comp) t18BuildCredits() {
	s := c.t18
	topo := c.t18Topo
	s.topo = topo
	s.hops = make([]int, len(topo.ports))
	for i, p := range topo.ports {
		if p == c.ToOutside {
			s.hops[i] = -1
			continue
		}
		s.hops[i] = t18Hops(topo.self, topo.coord[i])
	}
	if !s.reqActive {
		return
	}
	s.credits = newT18CreditAllocator(c.t18Window, s.cfg.DistCredits, s.hops)
	s.reqQueues = make([][]*t18ReqEntry, len(topo.ports))
	s.stats.HopDests = [t18MaxHops]uint64{}
	s.stats.HopCredits = [t18MaxHops]uint64{}
	for i, h := range s.hops {
		if h < 0 {
			continue
		}
		s.stats.HopDests[h]++
		s.stats.HopCredits[h] += uint64(s.credits.credits[i])
	}
}

// GetT18Stats returns the T18 counters; Enabled is false when T18 is off.
func (c *Comp) GetT18Stats() T18Stats {
	if c.t18 == nil {
		return T18Stats{}
	}
	s := c.t18
	if s.reqActive {
		c.t18Integrate(c.Engine.CurrentTime())
		s.stats.ReqTable = s.reqTable.Stats()
	}
	if s.ownerActive {
		s.stats.OwnerTable = s.ownerTable.Stats()
	}
	return s.stats
}

func (c *Comp) t18Integrate(now sim.VTimeInSec) {
	s := c.t18
	if now <= s.lastIntegNS {
		return
	}
	dt := float64(now-s.lastIntegNS) * 1e9
	s.lastIntegNS = now
	s.stats.ReqTimeNS += dt
	s.stats.ReqQueueAreaNS += float64(s.queued) * dt
	if s.credits != nil {
		s.stats.ReqInflightAreaNS += float64(s.credits.total) * dt
		if s.credits.total >= s.credits.window {
			s.stats.ReqWindowFullNS += dt
		}
	}
	for h := 0; h < t18MaxHops; h++ {
		if s.hopInflight[h] > 0 {
			s.stats.HopInflightAreaNS[h] += float64(s.hopInflight[h]) * dt
		}
	}
}

func (c *Comp) t18DstIndex(dst sim.Port) int {
	if c.t18Topo == nil {
		panic("T18 requester needs the mesh topology (SetT18Topology)")
	}
	idx, ok := c.t18Topo.index[dst]
	if !ok {
		panic(fmt.Sprintf("T18: %s is not a known RDMA destination", dst.Name()))
	}
	return idx
}

func t18LineKey(req mem.AccessReq) t18Key {
	return t18Key{pid: uint64(req.GetPID()), line: remoteLineAddress(req.GetAddress())}
}

func t18Mergeable(req mem.AccessReq) bool {
	read, ok := req.(*mem.ReadReq)
	return ok && !read.LookupOnly && remoteReadBatchable(read)
}

func t18OpenEntry(e *t18ReqEntry) bool { return e.open }

// t18ReqFromL1 handles the head ToL1 request; it returns true when the request
// was accepted (and retrieved from ToL1).
func (c *Comp) t18ReqFromL1(
	now sim.VTimeInSec,
	req mem.AccessReq,
	dst sim.Port,
) bool {
	s := c.t18
	if s.credits == nil {
		panic("T18 requester needs the mesh topology (SetT18Topology)")
	}
	key := t18LineKey(req)
	mergeable := t18Mergeable(req)
	if mergeable && s.cfg.ReqMerge {
		if e := s.reqTable.Lookup(key, t18OpenEntry); e != nil {
			if s.waiters >= s.waiterCap {
				return c.t18Stall(req, &s.stats.ReqWaiterFullStalls)
			}
			e.waiters = append(e.waiters, t18Waiter{req: req, arrival: now})
			c.t18AddWaiter()
			s.stats.ReqAccepted++
			s.stats.ReqMergeableReads++
			s.stats.ReqMerged++
			if e.sent {
				s.stats.ReqMergedInflight++
			} else {
				s.stats.ReqMergedQueued++
			}
			if h := s.hops[e.dstIdx]; h >= 0 {
				s.stats.HopMerged[h]++
			}
			c.ToL1.Retrieve(now)
			c.forgetSeen(c.firstSeenFromL1Req, req.Meta().ID)
			return true
		}
	}
	if s.waiters >= s.waiterCap {
		return c.t18Stall(req, &s.stats.ReqWaiterFullStalls)
	}
	dstIdx := c.t18DstIndex(dst)
	e := &t18ReqEntry{
		key:    key,
		dst:    dst,
		dstIdx: dstIdx,
		open:   mergeable && s.cfg.ReqMerge,
	}
	// A table at capacity is full without a cuckoo walk; below capacity an
	// insert may still fail after MaxKicks displacements.
	if s.reqTable.Len() < s.reqTable.Capacity() && s.reqTable.Insert(key, e) {
		e.inTable = true
	} else {
		if !s.cfg.BypassWhenFull ||
			len(s.reqQueues[dstIdx]) > 0 || !c.t18CanSendNow(dstIdx) {
			return c.t18Stall(req, &s.stats.ReqTableFullStalls)
		}
		e.open = false
		s.stats.ReqBypasses++
	}
	if _, isWrite := req.(*mem.WriteReq); isWrite && s.cfg.ReqMerge {
		c.t18CloseLines(req)
	}
	cloned := c.cloneReq(req)
	cloned.Meta().Src = c.ToOutside
	cloned.Meta().Dst = dst
	c.markReqAsRemote(cloned, dst)
	e.netReq = cloned
	e.waiters = append(e.waiters, t18Waiter{req: req, arrival: now})
	s.seq++
	e.seq = s.seq
	c.t18Integrate(now)
	s.reqQueues[dstIdx] = append(s.reqQueues[dstIdx], e)
	s.queued++
	if uint64(s.queued) > s.stats.ReqPeakQueued {
		s.stats.ReqPeakQueued = uint64(s.queued)
	}
	c.t18AddWaiter()
	s.stats.ReqAccepted++
	s.stats.ReqNewEntries++
	if mergeable {
		s.stats.ReqMergeableReads++
	}
	c.ToL1.Retrieve(now)
	c.forgetSeen(c.firstSeenFromL1Req, req.Meta().ID)
	return true
}

// t18Stall records a request that cannot be accepted this cycle: counter
// counts distinct stalled requests, ReqStallRetries every failed attempt.
func (c *Comp) t18Stall(req mem.AccessReq, counter *uint64) bool {
	s := c.t18
	s.stats.ReqStallRetries++
	if s.lastStallID != req.Meta().ID {
		s.lastStallID = req.Meta().ID
		*counter++
	}
	return false
}

func (c *Comp) t18AddWaiter() {
	s := c.t18
	s.waiters++
	if uint64(s.waiters) > s.stats.ReqPeakWaiters {
		s.stats.ReqPeakWaiters = uint64(s.waiters)
	}
}

// t18CloseLines closes the open entries of every line a write touches.
func (c *Comp) t18CloseLines(req mem.AccessReq) {
	s := c.t18
	first := remoteLineAddress(req.GetAddress())
	last := first
	if req.GetByteSize() > 0 {
		last = remoteLineAddress(req.GetAddress() + req.GetByteSize() - 1)
	}
	for line := first; line <= last; line += remoteLineBytes {
		key := t18Key{pid: uint64(req.GetPID()), line: line}
		if e := s.reqTable.Lookup(key, t18OpenEntry); e != nil {
			e.open = false
			s.stats.ReqClosedByWrite++
		}
	}
}

// t18ReservedCredits is the sum of unused own credits of destinations that
// have queued requests (what a borrower must leave free).
func (c *Comp) t18ReservedCredits() int {
	s := c.t18
	a := s.credits
	reserved := 0
	for e := range a.credits {
		if len(s.reqQueues[e]) == 0 || a.inflight[e] >= a.credits[e] {
			continue
		}
		reserved += a.credits[e] - a.inflight[e]
	}
	return reserved
}

func (c *Comp) t18CanSendNow(d int) bool {
	ok, _ := c.t18.credits.CanSend(d, func(e int) bool {
		return len(c.t18.reqQueues[e]) > 0
	})
	return ok
}

// t18ReqIssue sends queued requests, oldest admissible destination head first,
// up to the pipeline width per cycle.
func (c *Comp) t18ReqIssue(now sim.VTimeInSec) bool {
	s := c.t18
	if s == nil || !s.reqActive || s.queued == 0 {
		return false
	}
	a := s.credits
	madeProgress := false
	for issued := 0; issued < c.effectivePipelineWidth(); issued++ {
		if a.total >= a.window {
			break
		}
		reserved := 0
		if a.distCredits {
			reserved = c.t18ReservedCredits()
		}
		free := a.window - a.total
		best := -1
		borrowBest := false
		for d, q := range s.reqQueues {
			if len(q) == 0 {
				continue
			}
			own := !a.distCredits || a.inflight[d] < a.credits[d]
			if !own && free <= reserved {
				continue
			}
			if best < 0 || q[0].seq < s.reqQueues[best][0].seq {
				best = d
				borrowBest = !own
			}
		}
		if best < 0 {
			break
		}
		e := s.reqQueues[best][0]
		e.netReq.Meta().SendTime = now
		if err := c.ToOutside.Send(e.netReq); err != nil {
			break
		}
		c.t18Integrate(now)
		s.reqQueues[best][0] = nil
		s.reqQueues[best] = s.reqQueues[best][1:]
		s.queued--
		a.Acquire(best)
		e.sent = true
		e.sentAt = now
		s.reqByNetID[e.netReq.Meta().ID] = e
		s.stats.ReqSent++
		if borrowBest {
			s.stats.ReqSentBorrowed++
		}
		if h := s.hops[best]; h >= 0 {
			s.stats.HopSent[h]++
			s.hopInflight[h]++
		}
		if uint64(a.total) > s.stats.ReqPeakInflight {
			s.stats.ReqPeakInflight = uint64(a.total)
		}
		if uint64(a.total) > uint64(c.peakRequesterOutstanding) {
			c.peakRequesterOutstanding = a.total
		}
		madeProgress = true
	}
	if !madeProgress && s.queued > 0 && a.total >= a.window {
		c.requesterFullStalls++
	}
	return madeProgress
}

// t18ReqRsp handles a response from the network. handled is false when the
// response does not belong to the T18 requester table.
func (c *Comp) t18ReqRsp(
	now sim.VTimeInSec,
	rsp mem.AccessRsp,
) (handled, progress bool) {
	s := c.t18
	e := s.reqByNetID[rsp.GetRspTo()]
	if e == nil {
		return false, false
	}
	if len(s.fanout) > 0 && len(s.fanout)+len(e.waiters) > t18FanoutCap {
		return true, false
	}
	var data []byte
	switch r := rsp.(type) {
	case *mem.DataReadyRsp:
		data = r.Data
	case *mem.WriteDoneRsp:
	default:
		panic(fmt.Sprintf("T18 requester: unexpected response %T", rsp))
	}
	c.t18Integrate(now)
	delete(s.reqByNetID, rsp.GetRspTo())
	if e.inTable {
		s.reqTable.Delete(e)
		e.inTable = false
	}
	s.credits.Release(e.dstIdx)
	if h := s.hops[e.dstIdx]; h >= 0 {
		s.hopInflight[h]--
		s.stats.HopRTTTotalNS[h] += remoteDurationNS(now - e.sentAt)
	}
	s.stats.ReqResponses++
	owner := e.dst.Name()
	for _, w := range e.waiters {
		var msg sim.Msg
		op := "write"
		bytes := w.req.GetByteSize()
		if data != nil {
			if len(data) != int(w.req.GetByteSize()) {
				panic(fmt.Sprintf("T18 requester: %d-B response for a %d-B read",
					len(data), w.req.GetByteSize()))
			}
			msg = mem.DataReadyRspBuilder{}.
				WithSrc(c.ToL1).
				WithDst(w.req.Meta().Src).
				WithRspTo(w.req.Meta().ID).
				WithData(append([]byte(nil), data...)).
				Build()
			op = "read"
		} else {
			msg = mem.WriteDoneRspBuilder{}.
				WithSrc(c.ToL1).
				WithDst(w.req.Meta().Src).
				WithRspTo(w.req.Meta().ID).
				Build()
		}
		s.fanout = append(s.fanout, t18L1Rsp{
			msg: msg, arrival: w.arrival, owner: owner, bytes: bytes, op: op,
		})
	}
	e.waiters = nil
	c.ToOutside.Retrieve(now)
	return true, true
}

// t18Fanout sends ready responses to L1, up to the pipeline width per cycle.
func (c *Comp) t18Fanout(now sim.VTimeInSec) bool {
	s := c.t18
	madeProgress := false
	for i := 0; i < c.effectivePipelineWidth() && len(s.fanout) > 0; i++ {
		r := s.fanout[0]
		r.msg.Meta().SendTime = now
		if err := c.ToL1.Send(r.msg); err != nil {
			break
		}
		s.fanout[0] = t18L1Rsp{}
		s.fanout = s.fanout[1:]
		s.waiters--
		s.stats.ReqFanoutResponses++
		s.stats.ReqLatencyTotalNS += remoteDurationNS(now - r.arrival)
		if memtrace.L2SourceStatsEnabled() {
			memtrace.RecordRemoteGPMAccess(
				c.Name(), r.owner, r.bytes, now-r.arrival, now, r.op)
		}
		madeProgress = true
	}
	return madeProgress
}

func (c *Comp) t18HasPendingWork() bool {
	s := c.t18
	if s == nil {
		return false
	}
	return s.queued > 0 || len(s.reqByNetID) > 0 || len(s.fanout) > 0 ||
		s.ownerLive > 0 || len(s.ownerRspQueue) > 0
}
