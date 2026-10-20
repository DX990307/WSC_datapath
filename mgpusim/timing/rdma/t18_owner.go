package rdma

import (
	"fmt"

	"github.com/sarchlab/akita/v3/mem/mem"
	memtrace "github.com/sarchlab/akita/v3/mem/trace"
	"github.com/sarchlab/akita/v3/sim"
)

// T18 owner side.
//
// The merge table (a bounded cuckoo hash table keyed by the 64-B line) holds
// one entry per line whose owner-L2 read is pending: fingerprint/tag, a
// 48-bit requester bitmap (here the list of requesters with their
// transaction tags), and the state. A remote full-line read that finds an open
// entry and whose requester is not yet in the bitmap joins it: the line is
// read once from L2/HBM. When the data returns the entry leaves the table and
// the response goes out as
//   - one unicast DataReadyRsp when there is a single requester,
//   - one multicast packet along the mesh routing tree (OwnerMulticast), or
//   - one unicast packet per requester (multicast disabled).
// There is no hold timer: the merge window is the owner-L2 read latency, so a
// response is never delayed to wait for more requesters.
//
// Fallback to the baseline unicast path: table full (insert failure), the
// requester is already in the bitmap, or the request is not a full-line read.
//
// Writes. A write to a line closes its open entry before the write is
// forwarded to L2: a read that arrives after the write never joins a read
// issued to L2 before it. Reads merged before the write return the pre-write
// value, exactly what they would get if each had been forwarded to L2 on
// arrival (all of them arrived before the write).

type t18OwnerReq struct {
	src   sim.Port
	rspTo string
	sub   *remoteOwnerSubReq // M1+M2: a child line of an M2 bitmap request
}

type t18OwnerEntry struct {
	key       t18Key
	open      bool
	reqs      []t18OwnerReq
	l2Req     *mem.ReadReq
	data      []byte
	nextSend  int
	multicast bool
	// live: the entry holds an owner window slot (opened by a single-line
	// read). Entries opened by an M2 bitmap child use the bitmap's slot.
	live bool
}

func t18OwnerOpen(e *t18OwnerEntry) bool { return e.open }

func (e *t18OwnerEntry) hasRequester(src sim.Port) bool {
	for _, r := range e.reqs {
		if r.src == src {
			return true
		}
	}
	return false
}

// t18OwnerReqFromOutside handles a remote request at the owner. handled is
// false when the baseline path must process it.
func (c *Comp) t18OwnerReqFromOutside(
	now sim.VTimeInSec,
	req mem.AccessReq,
) (handled, progress bool) {
	s := c.t18
	if _, isWrite := req.(*mem.WriteReq); isWrite {
		c.t18OwnerCloseLines(req)
		return false, false
	}
	if !t18Mergeable(req) {
		return false, false
	}
	read := req.(*mem.ReadReq)
	key := t18LineKey(req)
	if e := s.ownerTable.Lookup(key, t18OwnerOpen); e != nil {
		if e.hasRequester(read.Src) {
			if s.ownerFallbackID != read.ID {
				s.ownerFallbackID = read.ID
				s.stats.OwnerReads++
				s.stats.OwnerSameRequester++
			}
			return false, false
		}
		e.reqs = append(e.reqs, t18OwnerReq{src: read.Src, rspTo: read.ID})
		if memtrace.ObsEnabled() {
			l2ID := ""
			if e.l2Req != nil {
				l2ID = e.l2Req.ID
			}
			memtrace.ObsOwnerRecv(read.ID, read.Address, read.RecvTime)
			memtrace.ObsOwnerL2(read.ID, read.Address, now, l2ID, true)
		}
		s.stats.OwnerReads++
		s.stats.OwnerMerged++
		c.ToOutside.Retrieve(now)
		c.forgetSeen(c.firstSeenFromOutsideReq, read.ID)
		return true, true
	}
	if !c.canAcceptOwnerOutstanding(1) {
		return true, false
	}
	e := &t18OwnerEntry{key: key, open: true, live: true}
	if !s.ownerTable.Insert(key, e) {
		if s.ownerFallbackID != read.ID {
			s.ownerFallbackID = read.ID
			s.stats.OwnerReads++
			s.stats.OwnerTableFullFallbacks++
		}
		return false, false
	}
	l2Req := mem.ReadReqBuilder{}.
		WithSendTime(now).
		WithSrc(c.ToL2).
		WithDst(c.localModules.Find(read.Address)).
		WithAddress(read.Address).
		WithByteSize(read.AccessByteSize).
		WithPID(read.PID).
		WithInfo(read.Info).
		Build()
	if err := c.ToL2.Send(l2Req); err != nil {
		s.ownerTable.Delete(e)
		return true, false
	}
	e.l2Req = l2Req
	e.reqs = append(e.reqs, t18OwnerReq{src: read.Src, rspTo: read.ID})
	if memtrace.ObsEnabled() {
		memtrace.ObsOwnerRecv(read.ID, read.Address, read.RecvTime)
		memtrace.ObsOwnerL2(read.ID, read.Address, now, l2Req.ID, false)
	}
	s.ownerByL2ID[l2Req.ID] = e
	s.ownerLive++
	if uint64(s.ownerLive) > s.stats.OwnerPeakLive {
		s.stats.OwnerPeakLive = uint64(s.ownerLive)
	}
	c.recordOwnerOutstandingPeak()
	s.stats.OwnerReads++
	s.stats.OwnerL2Reads++
	c.ToOutside.Retrieve(now)
	c.forgetSeen(c.firstSeenFromOutsideReq, read.ID)
	return true, true
}

func (c *Comp) t18OwnerCloseLines(req mem.AccessReq) {
	s := c.t18
	first := remoteLineAddress(req.GetAddress())
	last := first
	if req.GetByteSize() > 0 {
		last = remoteLineAddress(req.GetAddress() + req.GetByteSize() - 1)
	}
	for line := first; line <= last; line += remoteLineBytes {
		key := t18Key{pid: uint64(req.GetPID()), line: line}
		if e := s.ownerTable.Lookup(key, t18OwnerOpen); e != nil {
			e.open = false
			s.stats.OwnerClosedByWrite++
		}
	}
}

func (c *Comp) t18IsOwnerL2Rsp(rsp mem.AccessRsp) bool {
	s := c.t18
	return s != nil && s.ownerActive && s.ownerByL2ID[rsp.GetRspTo()] != nil
}

func (c *Comp) t18OwnerL2Rsp(now sim.VTimeInSec, rsp mem.AccessRsp) bool {
	s := c.t18
	e := s.ownerByL2ID[rsp.GetRspTo()]
	dataRsp, ok := rsp.(*mem.DataReadyRsp)
	if !ok {
		panic(fmt.Sprintf("T18 owner: unexpected L2 response %T", rsp))
	}
	e.data = append([]byte(nil), dataRsp.Data...)
	if memtrace.ObsEnabled() {
		memtrace.ObsOwnerL2Rsp(rsp.GetRspTo(), rsp.Meta().RecvTime)
	}
	delete(s.ownerByL2ID, rsp.GetRspTo())
	if s.ownerTable.Contains(e) {
		s.ownerTable.Delete(e)
	}
	e.open = false
	n := len(e.reqs)
	if n > 48 {
		n = 48
	}
	s.stats.OwnerFanoutHistogram[n]++
	c.ToL2.Retrieve(now)
	if !e.live {
		// M1+M2: opened by an M2 bitmap child.
		if len(e.reqs) == 1 {
			c.t18DeliverChild(e.reqs[0].sub, e.data)
			return true
		}
	}
	if !s.cfg.OwnerMulticast && e.hasChild() {
		// Unicast fan-out: bitmap children return in their bitmap responses.
		singles := e.reqs[:0]
		for _, r := range e.reqs {
			if r.sub != nil {
				c.t18DeliverChild(r.sub, e.data)
				continue
			}
			singles = append(singles, r)
		}
		e.reqs = singles
		if len(e.reqs) == 0 {
			return true
		}
	}
	e.multicast = len(e.reqs) > 1 && s.cfg.OwnerMulticast
	s.ownerRspQueue = append(s.ownerRspQueue, e)
	return true
}

func (e *t18OwnerEntry) hasChild() bool {
	for _, r := range e.reqs {
		if r.sub != nil {
			return true
		}
	}
	return false
}

// t18OwnerSend sends owner responses, one packet per pipeline slot.
func (c *Comp) t18OwnerSend(now sim.VTimeInSec) bool {
	s := c.t18
	madeProgress := false
	for i := 0; i < c.effectivePipelineWidth() && len(s.ownerRspQueue) > 0; i++ {
		e := s.ownerRspQueue[0]
		if e.multicast {
			dsts := make([]sim.Port, len(e.reqs))
			rspTos := make([]string, len(e.reqs))
			var lines []int
			if e.hasChild() {
				lines = make([]int, len(e.reqs))
			}
			for j, r := range e.reqs {
				dsts[j] = r.src
				rspTos[j] = r.rspTo
				if lines != nil {
					lines[j] = -1
					if r.sub != nil {
						lines[j] = int(r.sub.line)
					}
				}
			}
			msg := &T18MulticastRsp{
				MsgMeta: sim.MsgMeta{
					ID:       sim.GetIDGenerator().Generate(),
					Src:      c.ToOutside,
					Dst:      dsts[0],
					SendTime: now,
					TrafficBytes: len(e.data) + 4 +
						t18MulticastHeaderBytes,
				},
				Data:   e.data,
				Dsts:   dsts,
				RspTos: rspTos,
				Lines:  lines,
			}
			if err := c.ToOutside.Send(msg); err != nil {
				break
			}
			for _, r := range e.reqs {
				if memtrace.ObsEnabled() {
					memtrace.ObsOwnerSend(r.rspTo, e.key.line, now, true)
				}
				if r.sub != nil {
					c.t18ChildSentByMulticast(r.sub)
				}
			}
			s.stats.OwnerMulticastPackets++
			s.stats.OwnerMulticastDests += uint64(len(dsts))
			e.nextSend = len(e.reqs)
		} else {
			r := e.reqs[e.nextSend]
			if r.sub != nil {
				panic("T18 owner: bitmap child on the unicast path")
			}
			msg := mem.DataReadyRspBuilder{}.
				WithSendTime(now).
				WithSrc(c.ToOutside).
				WithDst(r.src).
				WithRspTo(r.rspTo).
				WithData(append([]byte(nil), e.data...)).
				Build()
			if err := c.ToOutside.Send(msg); err != nil {
				break
			}
			if memtrace.ObsEnabled() {
				memtrace.ObsOwnerSend(r.rspTo, e.key.line, now, false)
			}
			if len(e.reqs) == 1 {
				s.stats.OwnerSingleResponses++
			} else {
				s.stats.OwnerUnicastFanout++
			}
			e.nextSend++
		}
		madeProgress = true
		if e.nextSend >= len(e.reqs) {
			s.ownerRspQueue[0] = nil
			s.ownerRspQueue = s.ownerRspQueue[1:]
			if e.live {
				s.ownerLive--
			}
		}
	}
	return madeProgress
}
