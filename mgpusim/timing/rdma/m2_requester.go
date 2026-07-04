package rdma

import (
	"sort"

	"github.com/sarchlab/akita/v3/mem/mem"
	memtrace "github.com/sarchlab/akita/v3/mem/trace"
	"github.com/sarchlab/akita/v3/sim"
)

// ConfigureM2 enables or disables requester-side RDMA bitmap batching.
func (c *Comp) ConfigureM2(
	enable bool,
	auPrefetchEnable bool,
	maxBatchLines int,
	maxWaitNS uint64,
	batchTableEntries int,
) {
	if maxBatchLines <= 0 {
		maxBatchLines = 8
	}
	if batchTableEntries <= 0 {
		batchTableEntries = 64
	}

	c.m2Enabled = enable
	c.m2AUPrefetchEnabled = auPrefetchEnable
	c.m2MaxBatchLines = maxBatchLines
	c.m2MaxWait = sim.VTimeInSec(float64(maxWaitNS) * 1e-9)
	c.m2BatchTableEntries = batchTableEntries
	c.M2Stats.Enabled = enable
	c.M2Stats.AUPrefetchEnabled = auPrefetchEnable
}

// GetM2Stats returns a copy of the M2 counters.
func (c *Comp) GetM2Stats() M2Stats {
	return c.M2Stats
}

func (c *Comp) ensureM2State() {
	if c.m2Batches == nil {
		c.m2Batches = make(map[m2BatchKey]*m2RequesterBatch)
	}
	if c.m2RequesterInflight == nil {
		c.m2RequesterInflight = make(map[string]*m2RequesterBatch)
	}
	if c.m2OwnerSubReqs == nil {
		c.m2OwnerSubReqs = make(map[string]*m2OwnerSubReq)
	}
}

func (c *Comp) tryProcessM2ReqFromL1(
	now sim.VTimeInSec,
	req mem.AccessReq,
	dst sim.Port,
	firstSeen sim.VTimeInSec,
) (bool, bool) {
	if !c.m2Enabled {
		return false, false
	}

	c.M2Stats.OriginalRemoteRequests++

	read, ok := req.(*mem.ReadReq)
	if !ok || !m2ReadBatchable(read) {
		c.M2Stats.BypassRequests++
		if c.flushM2ConflictingBatches(now, req, dst) {
			return true, true
		}
		if c.hasM2Conflict(req, dst) {
			return true, false
		}
		return false, false
	}

	c.M2Stats.BatchableRemoteReads++
	c.ensureM2State()

	if c.flushExpiredM2Batches(now) {
		return true, true
	}

	key := m2BatchKey{
		requesterName: c.Name(),
		ownerName:     m2PortName(dst),
		pid:           read.PID,
		pagePAddr:     m2PageAddress(read.Address),
	}

	batch := c.m2Batches[key]
	line := m2LineOffset(read.Address)
	if batch != nil &&
		!batch.hasLine(line) &&
		batch.uniqueLineCount() >= c.m2MaxBatchLines {
		if !c.flushM2Batch(now, batch, m2FlushReasonFull) {
			return true, false
		}
		batch = nil
	}

	if batch == nil {
		if len(c.m2BatchOrder) >= c.m2BatchTableEntries {
			if !c.flushOldestM2Batch(now, m2FlushReasonCapacity) {
				return true, false
			}
		}
		c.m2NextBatchID++
		batch = &m2RequesterBatch{
			id:          c.m2NextBatchID,
			key:         key,
			dst:         dst,
			requests:    make(map[uint64][]m2OriginalReq),
			prefetch:    make(map[uint64]bool),
			prefetchDst: make(map[uint64]sim.Port),
			oldest:      now,
			newest:      now,
			info:        read.Info,
		}
		c.m2Batches[key] = batch
		c.m2BatchOrder = append(c.m2BatchOrder, key)
		c.M2Stats.BatchesCreated++
	}

	if batch.hasLine(line) {
		c.M2Stats.DuplicateLineReads++
		if batch.isPrefetchLine(line) {
			c.M2Stats.AUPrefetchDemandHits++
		}
	}
	batch.add(req, now, firstSeen)
	c.addM2AUPrefetchLine(batch, line, req.Meta().Src)

	c.ToL1.Retrieve(now)
	c.forgetSeen(c.firstSeenFromL1Req, req.Meta().ID)

	if batch.uniqueLineCount() >= c.m2MaxBatchLines {
		c.flushM2Batch(now, batch, m2FlushReasonFull)
	}

	return true, true
}

func (c *Comp) addM2AUPrefetchLine(
	batch *m2RequesterBatch,
	line uint64,
	dst sim.Port,
) {
	if !c.m2AUPrefetchEnabled || batch == nil {
		return
	}
	if batch.uniqueLineCount() >= c.m2MaxBatchLines {
		return
	}

	mate := line ^ 1
	if mate >= 64 {
		return
	}
	if batch.addPrefetchLine(mate, dst) {
		c.M2Stats.AUPrefetchLinesRequested++
	}
}

func (c *Comp) processM2RequesterBatches(
	now sim.VTimeInSec,
	force bool,
) bool {
	if !c.m2Enabled || len(c.m2BatchOrder) == 0 {
		return false
	}

	madeProgress := false
	for len(c.m2BatchOrder) > 0 {
		key := c.m2BatchOrder[0]
		batch := c.m2Batches[key]
		if batch == nil {
			c.m2BatchOrder = c.m2BatchOrder[1:]
			madeProgress = true
			continue
		}

		reason := ""
		if force {
			reason = m2FlushReasonDrain
		} else if c.m2MaxWait > 0 && now-batch.oldest >= c.m2MaxWait {
			reason = m2FlushReasonTimeout
		}

		if reason == "" {
			break
		}
		if !c.flushM2Batch(now, batch, reason) {
			break
		}
		madeProgress = true
	}

	// Keep the component ticking while a timeout-governed batch is active.
	return madeProgress || len(c.m2BatchOrder) > 0
}

func (c *Comp) flushExpiredM2Batches(now sim.VTimeInSec) bool {
	if c.m2MaxWait <= 0 {
		return false
	}

	for _, key := range append([]m2BatchKey(nil), c.m2BatchOrder...) {
		batch := c.m2Batches[key]
		if batch == nil || now-batch.oldest < c.m2MaxWait {
			continue
		}
		if c.flushM2Batch(now, batch, m2FlushReasonTimeout) {
			return true
		}
		return false
	}
	return false
}

func (c *Comp) flushOldestM2Batch(now sim.VTimeInSec, reason string) bool {
	if len(c.m2BatchOrder) == 0 {
		return true
	}

	key := c.m2BatchOrder[0]
	batch := c.m2Batches[key]
	if batch == nil {
		c.m2BatchOrder = c.m2BatchOrder[1:]
		return true
	}
	return c.flushM2Batch(now, batch, reason)
}

func (c *Comp) flushM2Batch(
	now sim.VTimeInSec,
	batch *m2RequesterBatch,
	reason string,
) bool {
	if batch == nil || batch.uniqueLineCount() == 0 {
		return true
	}
	if !c.ToOutside.CanSend() {
		return false
	}

	lines := append([]uint64(nil), batch.lineOrder...)
	sort.Slice(lines, func(i, j int) bool { return lines[i] < lines[j] })

	req := &BitmapReadReq{
		MsgMeta: sim.MsgMeta{
			ID:           sim.GetIDGenerator().Generate(),
			Src:          c.ToOutside,
			Dst:          batch.dst,
			SendTime:     now,
			TrafficBytes: m2BitmapRequestOverhead,
		},
		BatchID:              batch.id,
		RequesterName:        batch.key.requesterName,
		OwnerName:            batch.key.ownerName,
		PID:                  batch.key.pid,
		PagePAddr:            batch.key.pagePAddr,
		LineBitmap:           batch.lineBitmap,
		LineCount:            batch.uniqueLineCount(),
		LineSize:             m2DefaultLineBytes,
		OriginalRequestCount: batch.requestCount(),
		Info:                 batch.info,
	}

	c.removeM2Batch(batch)
	c.ensureM2State()
	c.m2RequesterInflight[req.ID] = batch

	for _, original := range batch.requestList {
		wait := now - original.arrival
		c.M2Stats.TotalWaitNS += m2VTimeToNS(wait)
		c.M2Stats.WaitSamples++
		memtrace.RecordMemoryPathRDMARequestFromL1(
			c.Name(),
			rdmaAccessReqInfo(original.req),
			original.req.Meta().ID,
			original.req.Meta().SendTime,
			original.firstSeen,
			original.req.Meta().Src,
			original.req.Meta().Dst,
		)
		memtrace.RecordMemoryPathRDMALocalRequestOutputWait(
			c.Name(),
			rdmaAccessReqInfo(original.req),
			original.req.Meta().ID,
			original.firstSeen,
			now,
		)
	}

	err := c.ToOutside.Send(req)
	if err != nil {
		c.m2RequesterInflight[req.ID] = nil
		return false
	}

	c.M2Stats.BatchesFlushed++
	c.M2Stats.BatchedPackets++
	c.M2Stats.LinesInBatchedPackets += uint64(req.LineCount)
	c.M2Stats.RequestsInBatchedPackets += uint64(req.OriginalRequestCount)
	if uint64(req.LineCount) > c.M2Stats.MaxBatchLines {
		c.M2Stats.MaxBatchLines = uint64(req.LineCount)
	}
	if uint64(req.OriginalRequestCount) > c.M2Stats.MaxRequestsPerBatch {
		c.M2Stats.MaxRequestsPerBatch = uint64(req.OriginalRequestCount)
	}
	switch reason {
	case m2FlushReasonFull:
		c.M2Stats.FullFlushes++
	case m2FlushReasonTimeout:
		c.M2Stats.TimeoutFlushes++
	case m2FlushReasonCapacity:
		c.M2Stats.CapacityFlushes++
	case m2FlushReasonConflict:
		c.M2Stats.ConflictFlushes++
	case m2FlushReasonDrain:
		c.M2Stats.DrainFlushes++
	}

	_ = lines
	return true
}

func (c *Comp) removeM2Batch(batch *m2RequesterBatch) {
	delete(c.m2Batches, batch.key)
	for i, key := range c.m2BatchOrder {
		if key == batch.key {
			c.m2BatchOrder = append(c.m2BatchOrder[:i], c.m2BatchOrder[i+1:]...)
			return
		}
	}
}

func (c *Comp) hasM2Conflict(req mem.AccessReq, dst sim.Port) bool {
	key := m2BatchKey{
		requesterName: c.Name(),
		ownerName:     m2PortName(dst),
		pid:           req.GetPID(),
		pagePAddr:     m2PageAddress(req.GetAddress()),
	}
	return c.m2Batches[key] != nil
}

func (c *Comp) flushM2ConflictingBatches(
	now sim.VTimeInSec,
	req mem.AccessReq,
	dst sim.Port,
) bool {
	if !c.hasM2Conflict(req, dst) {
		return false
	}
	key := m2BatchKey{
		requesterName: c.Name(),
		ownerName:     m2PortName(dst),
		pid:           req.GetPID(),
		pagePAddr:     m2PageAddress(req.GetAddress()),
	}
	return c.flushM2Batch(now, c.m2Batches[key], m2FlushReasonConflict)
}

func (c *Comp) processBitmapRspFromOutside(
	now sim.VTimeInSec,
	rsp *BitmapReadRsp,
) bool {
	c.ensureM2State()
	batch := c.m2RequesterInflight[rsp.GetRspTo()]
	if batch == nil {
		panic("M2 bitmap response has no matching requester batch")
	}

	for _, line := range batch.lineOrder {
		data := rsp.LineData[line]
		if batch.isPrefetchLine(line) && len(batch.requests[line]) == 0 {
			c.m2PendingFills = append(c.m2PendingFills, m2PendingRemoteFill{
				pid:       batch.key.pid,
				dst:       batch.prefetchDst[line],
				address:   batch.key.pagePAddr + line*m2DefaultLineBytes,
				data:      append([]byte(nil), data...),
				firstSeen: now,
			})
			c.M2Stats.AUPrefetchLinesReturned++
			continue
		}
		for _, original := range batch.requests[line] {
			c.m2PendingRsps = append(c.m2PendingRsps, m2PendingRequesterRsp{
				original:         original,
				batchReq:         batchRequestFromContext(batch, rsp.GetRspTo()),
				data:             append([]byte(nil), data...),
				batchRspID:       rsp.Meta().ID,
				batchRspSendTime: rsp.Meta().SendTime,
				firstSeen:        now,
				providerName:     batch.key.ownerName,
			})
		}
	}

	c.M2Stats.RequesterBatchRsps++
	c.M2Stats.RequesterUnbatchRsps += uint64(batch.requestCount())
	if uint64(len(c.m2PendingRsps)) > c.M2Stats.RequesterPendingRspMax {
		c.M2Stats.RequesterPendingRspMax = uint64(len(c.m2PendingRsps))
	}
	delete(c.m2RequesterInflight, rsp.GetRspTo())
	c.ToOutside.Retrieve(now)
	return true
}

func (c *Comp) processM2RequesterPendingFills(now sim.VTimeInSec) bool {
	madeProgress := false
	for len(c.m2PendingFills) > 0 {
		pending := c.m2PendingFills[0]
		if pending.dst == nil {
			c.m2PendingFills = c.m2PendingFills[1:]
			madeProgress = true
			continue
		}
		fill := mem.RemoteDataFillBuilder{}.
			WithSendTime(now).
			WithSrc(c.ToL1).
			WithDst(pending.dst).
			WithPID(pending.pid).
			WithAddress(pending.address).
			WithData(pending.data).
			Build()
		err := c.ToL1.Send(fill)
		if err != nil {
			return madeProgress
		}
		c.m2PendingFills = c.m2PendingFills[1:]
		madeProgress = true
		_ = pending.firstSeen
	}
	return madeProgress
}

func batchRequestFromContext(batch *m2RequesterBatch, id string) *BitmapReadReq {
	return &BitmapReadReq{
		MsgMeta: sim.MsgMeta{
			ID:  id,
			Dst: batch.dst,
		},
		BatchID:    batch.id,
		OwnerName:  batch.key.ownerName,
		LineBitmap: batch.lineBitmap,
		LineCount:  batch.uniqueLineCount(),
		PagePAddr:  batch.key.pagePAddr,
		PID:        batch.key.pid,
	}
}

func (c *Comp) processM2RequesterPendingRsps(now sim.VTimeInSec) bool {
	madeProgress := false
	for len(c.m2PendingRsps) > 0 {
		pending := c.m2PendingRsps[0]
		req := pending.original.req
		rsp := mem.DataReadyRspBuilder{}.
			WithSendTime(now).
			WithSrc(c.ToL1).
			WithDst(req.Meta().Src).
			WithRspTo(req.Meta().ID).
			WithData(pending.data).
			Build()

		err := c.ToL1.Send(rsp)
		if err != nil {
			return madeProgress
		}

		memtrace.RecordMemoryPathRDMARemoteToLocalResponse(
			c.Name(),
			rdmaAccessReqInfo(req),
			req.Meta().ID,
			pending.batchRspID,
			pending.batchRspSendTime,
			pending.firstSeen,
			pending.batchReq.Meta().Dst,
			c.ToOutside,
		)
		memtrace.RecordMemoryPathRDMALocalResponseOutputWait(
			c.Name(),
			rdmaAccessReqInfo(req),
			req.Meta().ID,
			pending.batchRspID,
			pending.firstSeen,
			now,
		)
		memtrace.RecordRemoteGPMAccess(
			c.Name(),
			pending.providerName,
			uint64(len(pending.data)),
			now-req.Meta().SendTime,
			now,
			"read",
		)
		memtrace.RecordMemoryPathRemoteGPM(
			rdmaAccessReqInfo(req),
			c.Name(),
			pending.providerName,
			uint64(len(pending.data)),
			now-req.Meta().SendTime,
			now,
			"read",
		)

		c.m2PendingRsps = c.m2PendingRsps[1:]
		madeProgress = true
	}
	return madeProgress
}

func (c *Comp) m2HasPendingWork() bool {
	return len(c.m2BatchOrder) > 0 ||
		len(c.m2RequesterInflight) > 0 ||
		len(c.m2PendingRsps) > 0 ||
		len(c.m2PendingFills) > 0 ||
		len(c.m2OwnerPendingReqs) > 0 ||
		len(c.m2OwnerSubReqs) > 0 ||
		len(c.m2OwnerPendingRsps) > 0
}
