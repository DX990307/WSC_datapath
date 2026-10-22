package rdma

import (
	"github.com/sarchlab/akita/v3/mem/mem"
	memtrace "github.com/sarchlab/akita/v3/mem/trace"
	"github.com/sarchlab/akita/v3/sim"
)

// ISCA'27 M1 + M2 composition (M1 = T18 two-sided merge, M2 = the HPCA
// "Remote Aggregation" remote data path: requester dedup + same-page batching
// + filter-coupled prefetch, optionally with M3 requester-L2 reuse).
//
// When both mechanisms are on, the requester uses M2's request-formation path
// (line table with waiters, typed-filter dedup, page bitmap batching, prefetch
// piggyback, M3 requester-L2 probes/fills) and M1 supplies everything that
// sits around it:
//
//   - In-flight table (M1 "new 1"): every M2 line entry is indexed in M1's
//     cuckoo table (12-bit fingerprint + full key). Its size is
//     max(2W, M2's 2 x batches x batch-lines), so the line table and the
//     waiter pool scale with the window like standalone T18 (W=64 keeps M2's
//     sizes). Entries are "open" (mergeable) from creation until their data
//     returns; a write closes them (as in standalone T18).
//   - Requester merge (dedup owner m1 / both): a full-line read that finds an
//     open entry joins its waiter list, inline at the RDMA input (no typed
//     filter port). It runs before M2's filter dedup; with "both", M2's
//     filter dedup still runs on an M1 miss (it additionally merges into
//     entries whose data has returned but are still fanning out / filling).
//   - Window and credits: a network transaction (single line, bitmap batch =
//     one transaction carrying several lines, or a legacy write) needs one of
//     the W window slots (with distance credits, the destination's credit or
//     a borrowable idle one). The slot is released when the transaction
//     completes (a bitmap batch when its last line has returned). Batches
//     whose destination has no credit are skipped, so one destination does
//     not block the others (standalone M2 issues strictly in batch order).
//   - Owner (M1 "new 2"): single-line reads use the T18 owner merge table as
//     in standalone T18. The child line reads of an M2 bitmap request also go
//     through the owner merge table: a child joins a pending read of the same
//     line (from another GPM), or opens an entry other GPMs' reads can join.
//     A line with several requesters is sent once as a multicast packet; the
//     leaf for a bitmap requester is a one-line BitmapReadRsp (M2 already
//     handles partial bitmap responses). A child that nobody joined returns
//     through M2's normal bitmap response. Owner bitmap capacity scales to the
//     owner window like the rest of M1.

type t18ComposedTxn struct {
	dst      int
	sentAt   sim.VTimeInSec
	nearZone bool // holds a near-zone slot (credit policy near)
}

// m1Composed reports whether the M1 requester side runs on top of M2.
func (c *Comp) m1Composed() bool {
	return c.t18 != nil && c.t18.compose
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func (c *Comp) m2BaseLineCapacity() int {
	capacity := c.remoteConfig.MaxBatches * c.remoteConfig.MaxBatchLines * 2
	if capacity < 1 {
		return 1
	}
	return capacity
}

func (c *Comp) m1ComposeInit(window int) {
	s := c.t18
	cfg := s.cfg
	m2Lines := c.m2BaseLineCapacity()
	entries := cfg.ReqEntries
	if entries <= 0 {
		entries = 2 * window
	}
	s.composedLineCap = maxInt(entries, m2Lines)
	waiters := cfg.ReqWaiters
	if waiters <= 0 {
		waiters = 4 * entries
	}
	s.composedWaiterCap = maxInt(waiters, m2Lines)
	if cfg.ComposeLineEntries > 0 {
		// Explicit table size (v3 configs: 128 = 2W, the same as M1
		// alone). The waiter pool follows the table like standalone T18
		// (4 waiters per slot unless -t18-req-waiters is set); it is not
		// raised to M2's line count.
		s.composedLineCap = cfg.ComposeLineEntries
		s.composedWaiterCap = 4 * cfg.ComposeLineEntries
		if cfg.ReqWaiters > 0 {
			s.composedWaiterCap = cfg.ReqWaiters
		}
	}
	buckets := (s.composedLineCap + cfg.Ways - 1) / cfg.Ways
	s.m1Index = newT18CuckooTable[remoteLineEntry](
		buckets, cfg.Ways, uint(cfg.FingerprintBits), cfg.MaxKicks)
	s.composedSent = make(map[string]t18ComposedTxn)
	s.stats.ReqTableSlots = uint64(s.m1Index.Capacity())
	s.stats.ReqWaiterCapacity = uint64(s.composedWaiterCap)
	c.RemoteDataPathStats.LineEntryCapacity = uint64(s.composedLineCap)
	c.RemoteDataPathStats.WaiterEntryCapacity = uint64(s.composedWaiterCap)
	c.RemoteDataPathStats.OwnerChildLineCapacity =
		uint64(c.remoteOwnerChildLineCapacity())
}

func (c *Comp) remoteWaiterCapacity() int {
	if c.m1Composed() {
		return c.t18.composedWaiterCap
	}
	return c.remoteOutstandingCapacity()
}

// remoteRequesterBatchCapacity bounds the collecting page batches.
func (c *Comp) remoteRequesterBatchCapacity() int {
	if c.m1Composed() {
		limit := maxInt(c.remoteConfig.MaxBatches, c.t18Window)
		if c.t18.cfg.ComposeLineEntries > 0 &&
			limit > c.t18.composedLineCap {
			// Every open batch holds at least one line entry.
			limit = c.t18.composedLineCap
		}
		return limit
	}
	return c.remoteConfig.MaxBatches
}

// remoteOwnerBatchCapacity bounds the bitmap requests served at the owner.
func (c *Comp) remoteOwnerBatchCapacity() int {
	if c.t18 != nil {
		return maxInt(c.remoteConfig.MaxBatches, c.t18.ownerWindow)
	}
	return c.remoteConfig.MaxBatches
}

func t18OpenLine(e *remoteLineEntry) bool { return e.m1Open }

func m1KeyOf(entry *remoteLineEntry) t18Key {
	return t18Key{pid: uint64(entry.key.pid), line: entry.key.lineAddr}
}

// m1IndexLine records a new M2 line entry in the M1 in-flight table.
func (c *Comp) m1IndexLine(entry *remoteLineEntry) {
	if !c.m1Composed() || entry.m1Indexed {
		return
	}
	s := c.t18
	if s.m1Index.Len() < s.m1Index.Capacity() &&
		s.m1Index.Insert(m1KeyOf(entry), entry) {
		entry.m1Indexed = true
		entry.m1Open = true
		s.stats.ReqNewEntries++
		return
	}
	// Not indexed: the line is still tracked by M2's table and sent, but M1
	// cannot merge into it (the composed analogue of T18's bypass).
	s.stats.ReqBypasses++
}

// m1UnindexLine frees the M1 slot (the line's data has returned, or the entry
// is gone).
func (c *Comp) m1UnindexLine(entry *remoteLineEntry) {
	if !c.m1Composed() || !entry.m1Indexed {
		return
	}
	c.t18.m1Index.Delete(entry)
	entry.m1Indexed = false
	entry.m1Open = false
}

// m1CloseLines closes the open M1 entries of every line a write touches.
func (c *Comp) m1CloseLines(req mem.AccessReq) {
	if !c.m1Composed() {
		return
	}
	s := c.t18
	first := remoteLineAddress(req.GetAddress())
	last := first
	if req.GetByteSize() > 0 {
		last = remoteLineAddress(req.GetAddress() + req.GetByteSize() - 1)
	}
	for line := first; line <= last; line += remoteLineBytes {
		key := t18Key{pid: uint64(req.GetPID()), line: line}
		for {
			e := s.m1Index.Lookup(key, t18OpenLine)
			if e == nil {
				break
			}
			e.m1Open = false
			s.stats.ReqClosedByWrite++
		}
	}
}

// m1ComposeTryMerge is M1's requester merge on top of M2. handled is true
// when the read was merged (progress) or must wait (no progress).
func (c *Comp) m1ComposeTryMerge(
	now sim.VTimeInSec,
	read *mem.ReadReq,
	firstSeen sim.VTimeInSec,
) (handled, progress bool) {
	s := c.t18
	key := t18Key{pid: uint64(read.PID), line: remoteLineAddress(read.Address)}
	entry := s.m1Index.Lookup(key, t18OpenLine)
	if entry == nil {
		return false, false
	}
	if c.remoteOutstandingReads >= c.remoteWaiterCapacity() {
		c.RemoteDataPathStats.WaiterEntryFullStalls++
		c.t18Stall(read, &s.stats.ReqWaiterFullStalls)
		return true, false
	}
	// Metadata lookups primed for this request are no longer needed.
	c.clearRemoteBlockedNegative(read.ID)
	delete(c.remoteFilterLookups, read.ID)
	c.clearRemoteHintResults(read.ID)
	c.addRemoteWaiter(entry, read, now, firstSeen, 0)
	c.queueRemotePrefetchCandidate(now, read, c.takeRemotePrefetchCandidate(read.ID))
	c.consumeRemoteRead(now, read)
	s.stats.ReqAccepted++
	s.stats.ReqMergeableReads++
	s.stats.ReqMerged++
	if entry.state == remoteLineInflight {
		s.stats.ReqMergedInflight++
	} else {
		s.stats.ReqMergedQueued++
	}
	if entry.owner != nil {
		if h := s.hops[c.t18DstIndex(entry.owner)]; h >= 0 {
			s.stats.HopMerged[h]++
		}
	}
	return true, true
}

func (c *Comp) m1CanSend(dst sim.Port) bool {
	s := c.t18
	ok, _ := s.credits.CanSend(c.t18DstIndex(dst), func(e int) bool {
		return s.composedOpen[e] > 0
	})
	return ok
}

// m1Acquire takes a window slot for a network transaction just sent.
func (c *Comp) m1Acquire(now sim.VTimeInSec, dst sim.Port, netID string) {
	s := c.t18
	d := c.t18DstIndex(dst)
	_, borrow := s.credits.CanSend(d, func(e int) bool {
		return s.composedOpen[e] > 0
	})
	c.t18Integrate(now)
	c.t18NoteSend(d, borrow)
	nearZone := s.credits.Acquire(d)
	s.composedSent[netID] = t18ComposedTxn{dst: d, sentAt: now, nearZone: nearZone}
	s.stats.ReqSent++
	if borrow {
		s.stats.ReqSentBorrowed++
	}
	if h := s.hops[d]; h >= 0 {
		s.stats.HopSent[h]++
		s.hopInflight[h]++
	}
	a := s.credits
	if uint64(a.total) > s.stats.ReqPeakInflight {
		s.stats.ReqPeakInflight = uint64(a.total)
	}
	if a.total > c.peakRequesterOutstanding {
		c.peakRequesterOutstanding = a.total
	}
}

// m1Release returns the window slot of a completed network transaction.
func (c *Comp) m1Release(now sim.VTimeInSec, netID string) {
	s := c.t18
	txn, ok := s.composedSent[netID]
	if !ok {
		panic("M1+M2: completed transaction " + netID + " holds no window slot")
	}
	c.t18Integrate(now)
	delete(s.composedSent, netID)
	s.credits.Release(txn.dst, txn.nearZone)
	s.stats.ReqResponses++
	if h := s.hops[txn.dst]; h >= 0 {
		s.hopInflight[h]--
		s.stats.HopRTTTotalNS[h] += remoteDurationNS(now - txn.sentAt)
	}
}

func (c *Comp) m1BatchOpened(now sim.VTimeInSec, batch *remoteBatch) {
	if !c.m1Composed() {
		return
	}
	s := c.t18
	c.t18Integrate(now)
	d := c.t18DstIndex(batch.dst)
	s.composedOpen[d]++
	c.t18HopQueued(d, 1)
	s.queued++
	if uint64(s.queued) > s.stats.ReqPeakQueued {
		s.stats.ReqPeakQueued = uint64(s.queued)
	}
}

func (c *Comp) m1BatchClosed(batch *remoteBatch) {
	if !c.m1Composed() {
		return
	}
	s := c.t18
	c.t18Integrate(c.Engine.CurrentTime())
	d := c.t18DstIndex(batch.dst)
	s.composedOpen[d]--
	c.t18HopQueued(d, -1)
	s.queued--
}

// m1ProcessRemoteBatches issues collecting batches, oldest first, skipping
// batches whose destination has no credit, up to the pipeline width.
func (c *Comp) m1ProcessRemoteBatches(now sim.VTimeInSec, force bool) bool {
	a := c.t18.credits
	madeProgress := false
	issued := 0
	i := 0
	for issued < c.effectivePipelineWidth() && i < len(c.remoteBatchOrder) {
		if a.total >= a.window {
			break
		}
		key := c.remoteBatchOrder[i]
		batch := c.remoteBatches[key]
		if batch == nil {
			c.remoteBatchOrder = append(
				c.remoteBatchOrder[:i], c.remoteBatchOrder[i+1:]...)
			madeProgress = true
			continue
		}
		if !c.m1CanSend(batch.dst) {
			i++
			continue
		}
		if c.m1wHold != nil && !force && c.m1wHeld(now, batch) {
			i++
			continue
		}
		reason := flushReasonIssue
		if force {
			reason = flushReasonDrain
		} else if batch.lineCount() >= c.remoteConfig.MaxBatchLines {
			reason = flushReasonFull
		}
		if !c.flushRemoteBatch(now, batch, reason) {
			break // output port backpressure
		}
		madeProgress = true
		issued++
	}
	if issued >= c.effectivePipelineWidth() && len(c.remoteBatchOrder) > 0 {
		c.RemoteDataPathStats.RequesterIssueWidthStalls++
	}
	if !madeProgress && len(c.remoteBatchOrder) > 0 && a.total >= a.window {
		c.requesterFullStalls++
	}
	return madeProgress || len(c.remoteBatchOrder) > 0
}

// ---------------------------------------------------------------- owner side

// t18OwnerChildRead routes one child line read of an M2 bitmap request
// through the M1 owner merge table. handled is false when M2's own path must
// send the read (fallback: same requester already merged, or table full).
func (c *Comp) t18OwnerChildRead(
	now sim.VTimeInSec,
	sub *remoteOwnerSubReq,
) (handled, progress bool) {
	s := c.t18
	read := sub.read
	src := sub.batch.req.Src
	key := t18Key{pid: uint64(read.PID), line: remoteLineAddress(read.Address)}
	if e := s.ownerTable.Lookup(key, t18OwnerOpen); e != nil {
		if e.hasRequester(src) {
			s.stats.OwnerReads++
			s.stats.OwnerSameRequester++
			return false, false
		}
		e.reqs = append(e.reqs, t18OwnerReq{src: src, rspTo: sub.batch.req.ID, sub: sub})
		if memtrace.ObsEnabled() {
			l2ID := ""
			if e.l2Req != nil {
				l2ID = e.l2Req.ID
			}
			memtrace.ObsOwnerL2(sub.batch.req.ID, read.Address, now, l2ID, true)
		}
		s.stats.OwnerReads++
		s.stats.OwnerMerged++
		s.stats.OwnerBatchChildMerged++
		return true, true
	}
	e := &t18OwnerEntry{key: key, open: true}
	c.t18OwnerIntegrate(now)
	if !s.ownerTable.Insert(key, e) {
		s.stats.OwnerReads++
		s.stats.OwnerTableFullFallbacks++
		return false, false
	}
	read.SendTime = now
	if err := c.ToL2.Send(read); err != nil {
		c.t18OwnerIntegrate(now)
		s.ownerTable.Delete(e)
		return true, false
	}
	e.l2Req = read
	e.reqs = append(e.reqs, t18OwnerReq{src: src, rspTo: sub.batch.req.ID, sub: sub})
	if memtrace.ObsEnabled() {
		memtrace.ObsOwnerL2(sub.batch.req.ID, read.Address, now, read.ID, false)
	}
	s.ownerByL2ID[read.ID] = e
	s.stats.OwnerReads++
	s.stats.OwnerL2Reads++
	s.stats.OwnerBatchChildReads++
	return true, true
}

// t18DeliverChild hands a line to its M2 bitmap batch for the normal bitmap
// response (no packet of its own).
func (c *Comp) t18DeliverChild(sub *remoteOwnerSubReq, data []byte) {
	sub.batch.readyData[sub.line] = append([]byte(nil), data...)
	sub.batch.remaining--
	delete(c.remoteOwnerSubReqs, sub.read.ID)
	if !sub.batch.readyQueued {
		sub.batch.readyQueued = true
		c.remoteOwnerPendingRsp = append(c.remoteOwnerPendingRsp, sub.batch)
	}
}

// t18ChildSentByMulticast completes a child whose line left in a multicast.
func (c *Comp) t18ChildSentByMulticast(sub *remoteOwnerSubReq) {
	sub.batch.remaining--
	delete(c.remoteOwnerSubReqs, sub.read.ID)
	if sub.batch.remaining == 0 && !sub.batch.readyQueued {
		delete(c.remoteOwnerBatches, sub.batch.req.ID)
	}
}
