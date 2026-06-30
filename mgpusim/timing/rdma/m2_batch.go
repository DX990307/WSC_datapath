package rdma

import (
	"sort"

	"github.com/sarchlab/akita/v3/mem/mem"
	memtrace "github.com/sarchlab/akita/v3/mem/trace"
	"github.com/sarchlab/akita/v3/mem/vm"
	"github.com/sarchlab/akita/v3/sim"
)

const (
	m2RDMAPageSize         uint64 = 4096
	m2RDMALineSize         uint64 = 64
	m2RDMABitmapOverhead          = 20
	m2RDMAResponseOverhead        = 4
)

// BatchReadReq is an RDMA-private request that carries a bitmap-style list of
// cache lines from one physical page.
type BatchReadReq struct {
	sim.MsgMeta

	PageAddress          uint64
	PID                  vm.PID
	LineSize             uint64
	Lines                []uint64
	OriginalRequestCount int
	Info                 interface{}
}

// Meta returns the message meta.
func (r *BatchReadReq) Meta() *sim.MsgMeta {
	return &r.MsgMeta
}

// BatchReadRsp returns multiple cache lines for one BatchReadReq.
type BatchReadRsp struct {
	sim.MsgMeta

	RespondTo string
	LineSize  uint64
	Lines     []uint64
	LineData  map[uint64][]byte
}

// Meta returns the message meta.
func (r *BatchReadRsp) Meta() *sim.MsgMeta {
	return &r.MsgMeta
}

// GetRspTo returns the ID of the request that the response replies to.
func (r *BatchReadRsp) GetRspTo() string {
	return r.RespondTo
}

// RDMABatchStats summarizes requester-side RDMA batching behavior.
type RDMABatchStats struct {
	Enabled bool

	BatchableRequests uint64
	BypassedRequests  uint64

	BatchPackets      uint64
	BatchedLines      uint64
	DuplicateRequests uint64

	FlushFull     uint64
	FlushTimeout  uint64
	FlushCapacity uint64
	FlushDrain    uint64

	OwnerBatchRequests   uint64
	OwnerLocalReadReqs   uint64
	OwnerBatchResponses  uint64
	RequesterBatchRsps   uint64
	RequesterUnbatchRsps uint64

	TotalWaitNS float64
	MaxWaitNS   float64
}

// Add merges another stats object into this one.
func (s *RDMABatchStats) Add(other RDMABatchStats) {
	s.Enabled = s.Enabled || other.Enabled
	s.BatchableRequests += other.BatchableRequests
	s.BypassedRequests += other.BypassedRequests
	s.BatchPackets += other.BatchPackets
	s.BatchedLines += other.BatchedLines
	s.DuplicateRequests += other.DuplicateRequests
	s.FlushFull += other.FlushFull
	s.FlushTimeout += other.FlushTimeout
	s.FlushCapacity += other.FlushCapacity
	s.FlushDrain += other.FlushDrain
	s.OwnerBatchRequests += other.OwnerBatchRequests
	s.OwnerLocalReadReqs += other.OwnerLocalReadReqs
	s.OwnerBatchResponses += other.OwnerBatchResponses
	s.RequesterBatchRsps += other.RequesterBatchRsps
	s.RequesterUnbatchRsps += other.RequesterUnbatchRsps
	s.TotalWaitNS += other.TotalWaitNS
	if other.MaxWaitNS > s.MaxWaitNS {
		s.MaxWaitNS = other.MaxWaitNS
	}
}

type m2RDMABatchKey struct {
	dstName     string
	pageAddress uint64
	pid         vm.PID
}

type m2RequesterReq struct {
	req       mem.AccessReq
	firstSeen sim.VTimeInSec
}

type m2RequesterBatch struct {
	key         m2RDMABatchKey
	dst         sim.Port
	pageAddress uint64
	pid         vm.PID
	lineSize    uint64
	firstSeen   sim.VTimeInSec

	lineOrder []uint64
	requests  map[uint64][]m2RequesterReq
}

func (b *m2RequesterBatch) uniqueLineCount() int {
	return len(b.lineOrder)
}

func (b *m2RequesterBatch) requestCount() int {
	count := 0
	for _, reqs := range b.requests {
		count += len(reqs)
	}
	return count
}

func (b *m2RequesterBatch) hasLine(line uint64) bool {
	_, ok := b.requests[line]
	return ok
}

func (b *m2RequesterBatch) add(req mem.AccessReq, firstSeen sim.VTimeInSec) {
	line := m2LineOffset(req.GetAddress())
	if !b.hasLine(line) {
		b.lineOrder = append(b.lineOrder, line)
	}
	b.requests[line] = append(b.requests[line], m2RequesterReq{
		req:       req,
		firstSeen: firstSeen,
	})
}

func (b *m2RequesterBatch) sortedLines() []uint64 {
	lines := append([]uint64(nil), b.lineOrder...)
	sort.Slice(lines, func(i, j int) bool {
		return lines[i] < lines[j]
	})
	return lines
}

type m2PendingRequesterRsp struct {
	origin           mem.AccessReq
	rsp              *mem.DataReadyRsp
	batchRspID       string
	batchRspSendTime sim.VTimeInSec
	firstSeen        sim.VTimeInSec
	remoteSrc        sim.Port
	remoteDst        sim.Port
	providerName     string
}

type m2OwnerBatch struct {
	req       *BatchReadReq
	lineData  map[uint64][]byte
	remaining int
}

type m2OwnerSubReq struct {
	batch *m2OwnerBatch
	line  uint64
	req   *mem.ReadReq
}

func (c *Comp) ConfigureM2RDMABatch(
	enable bool,
	maxBatchLines int,
	maxWaitNS uint64,
	batchTableEntries int,
) {
	if maxBatchLines <= 0 {
		maxBatchLines = 8
	}
	if batchTableEntries <= 0 {
		batchTableEntries = 32
	}

	c.m2RDMABatchEnabled = enable
	c.m2RDMAMaxBatchLines = maxBatchLines
	c.m2RDMAMaxWait = sim.VTimeInSec(float64(maxWaitNS) * 1e-9)
	c.m2RDMABatchTableEntries = batchTableEntries
	c.M2RDMABatchStats.Enabled = enable
}

// GetM2RDMABatchStats returns a copy of the batch stats.
func (c *Comp) GetM2RDMABatchStats() RDMABatchStats {
	return c.M2RDMABatchStats
}

func (c *Comp) ensureM2RDMABatchState() {
	if c.m2RequesterBatches == nil {
		c.m2RequesterBatches = make(map[m2RDMABatchKey]*m2RequesterBatch)
	}
	if c.m2RequesterInflight == nil {
		c.m2RequesterInflight = make(map[string]*m2RequesterBatch)
	}
	if c.m2OwnerSubReqs == nil {
		c.m2OwnerSubReqs = make(map[string]*m2OwnerSubReq)
	}
}

func (c *Comp) m2HasPendingWork() bool {
	return len(c.m2RequesterBatchOrder) > 0 ||
		len(c.m2RequesterInflight) > 0 ||
		len(c.m2RequesterPendingRsps) > 0 ||
		len(c.m2OwnerPendingLocalReqs) > 0 ||
		len(c.m2OwnerSubReqs) > 0 ||
		len(c.m2OwnerPendingBatchRsps) > 0
}

func (c *Comp) tryProcessM2ReqFromL1(
	now sim.VTimeInSec,
	req mem.AccessReq,
) (handled bool, madeProgress bool) {
	if !c.m2RDMABatchEnabled {
		return false, false
	}

	read, ok := req.(*mem.ReadReq)
	if !ok || !m2ReadReqBatchable(read) {
		c.M2RDMABatchStats.BypassedRequests++
		return false, false
	}

	dst := c.RemoteRDMAAddressTable.Find(req.GetAddress())
	if dst == c.ToOutside {
		panic("RDMA loop back detected")
	}

	c.ensureM2RDMABatchState()
	firstSeen := c.firstSeen(&c.firstSeenFromL1Req, req.Meta().ID, now)
	pageAddress := m2PageAddress(req.GetAddress())
	key := m2RDMABatchKey{
		dstName:     m2PortName(dst),
		pageAddress: pageAddress,
		pid:         req.GetPID(),
	}

	batch := c.m2RequesterBatches[key]
	line := m2LineOffset(req.GetAddress())
	if batch != nil &&
		!batch.hasLine(line) &&
		batch.uniqueLineCount() >= c.m2RDMAMaxBatchLines {
		if !c.flushM2RequesterBatch(now, batch, "full") {
			return true, false
		}
		batch = nil
	}

	if batch == nil {
		if len(c.m2RequesterBatchOrder) >= c.m2RDMABatchTableEntries {
			if !c.flushOldestM2RequesterBatch(now, "capacity") {
				return true, false
			}
		}
		batch = &m2RequesterBatch{
			key:         key,
			dst:         dst,
			pageAddress: pageAddress,
			pid:         req.GetPID(),
			lineSize:    m2RDMALineSize,
			firstSeen:   now,
			requests:    make(map[uint64][]m2RequesterReq),
		}
		c.m2RequesterBatches[key] = batch
		c.m2RequesterBatchOrder = append(c.m2RequesterBatchOrder, batch)
	}

	batch.add(req, firstSeen)
	c.M2RDMABatchStats.BatchableRequests++
	c.ToL1.Retrieve(now)

	if batch.uniqueLineCount() >= c.m2RDMAMaxBatchLines {
		c.flushM2RequesterBatch(now, batch, "full")
	}

	return true, true
}

func (c *Comp) processM2RequesterBatches(now sim.VTimeInSec, force bool) bool {
	if !c.m2RDMABatchEnabled || len(c.m2RequesterBatchOrder) == 0 {
		return false
	}

	madeProgress := false
	for len(c.m2RequesterBatchOrder) > 0 {
		batch := c.m2RequesterBatchOrder[0]
		reason := "timeout"
		if force {
			reason = "drain"
		} else if !c.m2RequesterBatchDue(now, batch) {
			break
		}

		if !c.flushM2RequesterBatch(now, batch, reason) {
			return madeProgress
		}
		madeProgress = true
	}

	if madeProgress {
		return true
	}

	// Keep ticking until max-wait expires. Otherwise a lone buffered batch can
	// wait forever if no later message arrives at this RDMA engine.
	return len(c.m2RequesterBatchOrder) > 0
}

func (c *Comp) m2RequesterBatchDue(
	now sim.VTimeInSec,
	batch *m2RequesterBatch,
) bool {
	if c.m2RDMAMaxWait <= 0 {
		return true
	}
	return now-batch.firstSeen >= c.m2RDMAMaxWait
}

func (c *Comp) flushOldestM2RequesterBatch(
	now sim.VTimeInSec,
	reason string,
) bool {
	if len(c.m2RequesterBatchOrder) == 0 {
		return true
	}
	return c.flushM2RequesterBatch(now, c.m2RequesterBatchOrder[0], reason)
}

func (c *Comp) flushM2RequesterBatch(
	now sim.VTimeInSec,
	batch *m2RequesterBatch,
	reason string,
) bool {
	if batch == nil || batch.uniqueLineCount() == 0 {
		return true
	}

	lines := batch.sortedLines()
	req := &BatchReadReq{
		MsgMeta: sim.MsgMeta{
			ID:           sim.GetIDGenerator().Generate(),
			Src:          c.ToOutside,
			Dst:          batch.dst,
			SendTime:     now,
			TrafficBytes: m2RDMABitmapOverhead,
		},
		PageAddress:          batch.pageAddress,
		PID:                  batch.pid,
		LineSize:             batch.lineSize,
		Lines:                lines,
		OriginalRequestCount: batch.requestCount(),
		Info:                 m2FirstReqInfo(batch),
	}

	err := c.ToOutside.Send(req)
	if err != nil {
		return false
	}

	c.removeM2RequesterBatch(batch)
	c.m2RequesterInflight[req.ID] = batch

	c.M2RDMABatchStats.BatchPackets++
	c.M2RDMABatchStats.BatchedLines += uint64(len(lines))
	if duplicates := batch.requestCount() - len(lines); duplicates > 0 {
		c.M2RDMABatchStats.DuplicateRequests += uint64(duplicates)
	}
	switch reason {
	case "full":
		c.M2RDMABatchStats.FlushFull++
	case "capacity":
		c.M2RDMABatchStats.FlushCapacity++
	case "drain":
		c.M2RDMABatchStats.FlushDrain++
	default:
		c.M2RDMABatchStats.FlushTimeout++
	}

	for _, line := range batch.lineOrder {
		for _, original := range batch.requests[line] {
			waitNS := m2VTimeToNS(now - original.firstSeen)
			c.M2RDMABatchStats.TotalWaitNS += waitNS
			if waitNS > c.M2RDMABatchStats.MaxWaitNS {
				c.M2RDMABatchStats.MaxWaitNS = waitNS
			}
			memtrace.RegisterMemoryPathNetworkMessage(
				rdmaAccessReqInfo(original.req),
				original.req.Meta().ID,
				req.Meta().ID,
				"request",
			)
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
			c.forgetSeen(c.firstSeenFromL1Req, original.req.Meta().ID)
		}
	}

	return true
}

func (c *Comp) removeM2RequesterBatch(batch *m2RequesterBatch) {
	delete(c.m2RequesterBatches, batch.key)
	for i, candidate := range c.m2RequesterBatchOrder {
		if candidate == batch {
			c.m2RequesterBatchOrder = append(
				c.m2RequesterBatchOrder[:i],
				c.m2RequesterBatchOrder[i+1:]...,
			)
			return
		}
	}
}

func (c *Comp) processM2BatchReqFromOutside(
	now sim.VTimeInSec,
	req *BatchReadReq,
) bool {
	c.ensureM2RDMABatchState()
	c.firstSeen(&c.firstSeenFromOutsideReq, req.Meta().ID, now)

	batch := &m2OwnerBatch{
		req:       req,
		lineData:  make(map[uint64][]byte),
		remaining: len(req.Lines),
	}
	for _, line := range req.Lines {
		addr := req.PageAddress + line*req.LineSize
		dst := c.localModules.Find(addr)
		read := mem.ReadReqBuilder{}.
			WithSendTime(now).
			WithSrc(c.ToL2).
			WithDst(dst).
			WithAddress(addr).
			WithByteSize(req.LineSize).
			WithPID(req.PID).
			WithInfo(req.Info).
			Build()
		subReq := &m2OwnerSubReq{
			batch: batch,
			line:  line,
			req:   read,
		}
		c.m2OwnerPendingLocalReqs = append(c.m2OwnerPendingLocalReqs, subReq)
		c.m2OwnerSubReqs[read.ID] = subReq
	}

	c.M2RDMABatchStats.OwnerBatchRequests++
	c.M2RDMABatchStats.OwnerLocalReadReqs += uint64(len(req.Lines))
	c.ToOutside.Retrieve(now)
	c.forgetSeen(c.firstSeenFromOutsideReq, req.Meta().ID)

	c.processM2OwnerPendingLocalReqs(now)
	return true
}

func (c *Comp) processM2OwnerPendingLocalReqs(now sim.VTimeInSec) bool {
	madeProgress := false
	for len(c.m2OwnerPendingLocalReqs) > 0 {
		subReq := c.m2OwnerPendingLocalReqs[0]
		subReq.req.Meta().SendTime = now
		err := c.ToL2.Send(subReq.req)
		if err != nil {
			return madeProgress
		}
		c.m2OwnerPendingLocalReqs = c.m2OwnerPendingLocalReqs[1:]
		madeProgress = true
	}
	return madeProgress
}

func (c *Comp) isM2OwnerSubRsp(rsp mem.AccessRsp) bool {
	if c.m2OwnerSubReqs == nil {
		return false
	}
	_, ok := c.m2OwnerSubReqs[rsp.GetRspTo()]
	return ok
}

func (c *Comp) processM2OwnerSubRspFromL2(
	now sim.VTimeInSec,
	rsp mem.AccessRsp,
) bool {
	c.ensureM2RDMABatchState()
	firstSeen := c.firstSeen(&c.firstSeenFromL2Rsp, rsp.Meta().ID, now)
	_ = firstSeen

	subReq := c.m2OwnerSubReqs[rsp.GetRspTo()]
	dataRsp, ok := rsp.(*mem.DataReadyRsp)
	if !ok {
		panic("M2 RDMA batch read received a non-data response")
	}

	subReq.batch.lineData[subReq.line] = append([]byte(nil), dataRsp.Data...)
	subReq.batch.remaining--
	delete(c.m2OwnerSubReqs, rsp.GetRspTo())
	c.ToL2.Retrieve(now)
	c.forgetSeen(c.firstSeenFromL2Rsp, rsp.Meta().ID)

	if subReq.batch.remaining == 0 {
		batchRsp := c.buildM2BatchReadRsp(now, subReq.batch)
		c.m2OwnerPendingBatchRsps = append(c.m2OwnerPendingBatchRsps, batchRsp)
	}

	c.processM2OwnerPendingBatchRsps(now)
	return true
}

func (c *Comp) buildM2BatchReadRsp(
	now sim.VTimeInSec,
	batch *m2OwnerBatch,
) *BatchReadRsp {
	trafficBytes := m2RDMAResponseOverhead
	for _, line := range batch.req.Lines {
		if data, ok := batch.lineData[line]; ok && len(data) > 0 {
			trafficBytes += len(data)
		} else {
			trafficBytes += int(batch.req.LineSize)
		}
	}

	return &BatchReadRsp{
		MsgMeta: sim.MsgMeta{
			ID:           sim.GetIDGenerator().Generate(),
			Src:          c.ToOutside,
			Dst:          batch.req.Meta().Src,
			SendTime:     now,
			TrafficBytes: trafficBytes,
		},
		RespondTo: batch.req.Meta().ID,
		LineSize:  batch.req.LineSize,
		Lines:     append([]uint64(nil), batch.req.Lines...),
		LineData:  batch.lineData,
	}
}

func (c *Comp) processM2OwnerPendingBatchRsps(now sim.VTimeInSec) bool {
	madeProgress := false
	for len(c.m2OwnerPendingBatchRsps) > 0 {
		rsp := c.m2OwnerPendingBatchRsps[0]
		rsp.Meta().SendTime = now
		err := c.ToOutside.Send(rsp)
		if err != nil {
			return madeProgress
		}
		c.M2RDMABatchStats.OwnerBatchResponses++
		c.m2OwnerPendingBatchRsps = c.m2OwnerPendingBatchRsps[1:]
		madeProgress = true
	}
	return madeProgress
}

func (c *Comp) processM2BatchRspFromOutside(
	now sim.VTimeInSec,
	rsp *BatchReadRsp,
) bool {
	c.ensureM2RDMABatchState()
	firstSeen := c.firstSeen(&c.firstSeenFromOutsideRsp, rsp.Meta().ID, now)

	batch := c.m2RequesterInflight[rsp.GetRspTo()]
	if batch == nil {
		panic("M2 RDMA batch response has no matching requester batch")
	}

	for _, line := range batch.lineOrder {
		data := rsp.LineData[line]
		for _, original := range batch.requests[line] {
			dataReady := mem.DataReadyRspBuilder{}.
				WithSendTime(now).
				WithSrc(c.ToL1).
				WithDst(original.req.Meta().Src).
				WithRspTo(original.req.Meta().ID).
				WithData(data).
				Build()
			c.m2RequesterPendingRsps = append(c.m2RequesterPendingRsps,
				m2PendingRequesterRsp{
					origin:           original.req,
					rsp:              dataReady,
					batchRspID:       rsp.Meta().ID,
					batchRspSendTime: rsp.Meta().SendTime,
					firstSeen:        firstSeen,
					remoteSrc:        rsp.Meta().Src,
					remoteDst:        rsp.Meta().Dst,
					providerName:     batch.key.dstName,
				})
			memtrace.RegisterMemoryPathNetworkMessage(
				rdmaAccessReqInfo(original.req),
				original.req.Meta().ID,
				rsp.Meta().ID,
				"return",
			)
		}
	}

	c.M2RDMABatchStats.RequesterBatchRsps++
	c.M2RDMABatchStats.RequesterUnbatchRsps +=
		uint64(batch.requestCount())
	delete(c.m2RequesterInflight, rsp.GetRspTo())
	c.ToOutside.Retrieve(now)
	c.forgetSeen(c.firstSeenFromOutsideRsp, rsp.Meta().ID)

	c.processM2RequesterPendingRsps(now)
	return true
}

func (c *Comp) processM2RequesterPendingRsps(now sim.VTimeInSec) bool {
	madeProgress := false
	for len(c.m2RequesterPendingRsps) > 0 {
		pending := c.m2RequesterPendingRsps[0]
		pending.rsp.Meta().SendTime = now
		err := c.ToL1.Send(pending.rsp)
		if err != nil {
			return madeProgress
		}

		memtrace.RecordMemoryPathRDMARemoteToLocalResponse(
			c.Name(),
			rdmaAccessReqInfo(pending.origin),
			pending.origin.Meta().ID,
			pending.batchRspID,
			pending.batchRspSendTime,
			pending.firstSeen,
			pending.remoteSrc,
			pending.remoteDst,
		)
		memtrace.RecordMemoryPathRDMALocalResponseOutputWait(
			c.Name(),
			rdmaAccessReqInfo(pending.origin),
			pending.origin.Meta().ID,
			pending.batchRspID,
			pending.firstSeen,
			now,
		)
		c.recordM2RemoteGPMAccess(now, pending)

		c.m2RequesterPendingRsps = c.m2RequesterPendingRsps[1:]
		madeProgress = true
	}
	return madeProgress
}

func (c *Comp) recordM2RemoteGPMAccess(
	now sim.VTimeInSec,
	pending m2PendingRequesterRsp,
) {
	if !memtrace.L2SourceStatsEnabled() && !memtrace.MemoryPathTraceEnabled() {
		return
	}

	bytes := uint64(len(pending.rsp.Data))
	if bytes == 0 {
		bytes = pending.origin.GetByteSize()
	}
	latency := now - pending.origin.Meta().SendTime

	memtrace.RecordRemoteGPMAccess(
		c.Name(),
		pending.providerName,
		bytes,
		latency,
		now,
		"read",
	)
	memtrace.RecordMemoryPathRemoteGPM(
		rdmaAccessReqInfo(pending.origin),
		c.Name(),
		pending.providerName,
		bytes,
		latency,
		now,
		"read",
	)
}

func m2ReadReqBatchable(req *mem.ReadReq) bool {
	if req.AccessByteSize != m2RDMALineSize {
		return false
	}
	return m2PageAddress(req.Address) ==
		m2PageAddress(req.Address+req.AccessByteSize-1)
}

func m2PageAddress(addr uint64) uint64 {
	return addr & ^(m2RDMAPageSize - 1)
}

func m2LineOffset(addr uint64) uint64 {
	return (addr - m2PageAddress(addr)) / m2RDMALineSize
}

func m2VTimeToNS(t sim.VTimeInSec) float64 {
	return float64(t) * 1e9
}

func m2PortName(port sim.Port) string {
	if port == nil {
		return ""
	}
	return port.Name()
}

func m2FirstReqInfo(batch *m2RequesterBatch) interface{} {
	for _, line := range batch.lineOrder {
		if reqs := batch.requests[line]; len(reqs) > 0 {
			return rdmaAccessReqInfo(reqs[0].req)
		}
	}
	return nil
}
