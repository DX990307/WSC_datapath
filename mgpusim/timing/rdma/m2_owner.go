package rdma

import (
	"sort"

	"github.com/sarchlab/akita/v3/mem/mem"
	memtrace "github.com/sarchlab/akita/v3/mem/trace"
	"github.com/sarchlab/akita/v3/sim"
)

func (c *Comp) processBitmapReqFromOutside(
	now sim.VTimeInSec,
	req *BitmapReadReq,
) bool {
	firstSeen := c.firstSeen(&c.firstSeenFromOutsideReq, req.Meta().ID, now)
	if c.m3Enabled {
		c.enqueueM3OwnerRequest(now, req, firstSeen)
		c.ToOutside.Retrieve(now)
		c.forgetSeen(c.firstSeenFromOutsideReq, req.Meta().ID)
		return true
	}

	c.startM2OwnerBatch(now, req, firstSeen)
	c.ToOutside.Retrieve(now)
	c.forgetSeen(c.firstSeenFromOutsideReq, req.Meta().ID)
	return true
}

func (c *Comp) startM2OwnerBatch(
	now sim.VTimeInSec,
	req *BitmapReadReq,
	firstSeen sim.VTimeInSec,
) {
	c.ensureM2State()
	batch := &m2OwnerBatch{
		req:      req,
		lineData: make(map[uint64][]byte),
	}

	for _, line := range bitmapLines(req.LineBitmap) {
		readAddr := req.PagePAddr + line*req.LineSize
		dst := c.localModules.Find(readAddr)
		read := mem.ReadReqBuilder{}.
			WithSendTime(now).
			WithSrc(c.ToL2).
			WithDst(dst).
			WithAddress(readAddr).
			WithByteSize(req.LineSize).
			WithPID(req.PID).
			WithInfo(req.Info).
			Build()
		subReq := &m2OwnerSubReq{
			batch: batch,
			line:  line,
			read:  read,
		}
		batch.remaining++
		c.m2OwnerPendingReqs = append(c.m2OwnerPendingReqs, subReq)
		c.m2OwnerSubReqs[read.ID] = subReq
	}

	c.M2Stats.OwnerBatchRequests++
	c.M2Stats.OwnerLocalReadReqs += uint64(batch.remaining)
	memtrace.RecordMemoryPathRDMALocalToRemoteRequest(
		c.Name(),
		rdmaAccessReqInfo(req),
		req.Meta().ID,
		req.Meta().SendTime,
		firstSeen,
		req.Meta().Src,
		req.Meta().Dst,
	)
	memtrace.RecordMemoryPathRDMARemoteRequestOutputWait(
		c.Name(),
		rdmaAccessReqInfo(req),
		req.Meta().ID,
		firstSeen,
		now,
	)
}

func (c *Comp) processM2OwnerPendingLocalReqs(now sim.VTimeInSec) bool {
	madeProgress := false
	for len(c.m2OwnerPendingReqs) > 0 {
		subReq := c.m2OwnerPendingReqs[0]
		subReq.read.Meta().SendTime = now
		err := c.ToL2.Send(subReq.read)
		if err != nil {
			return madeProgress
		}
		c.m2OwnerPendingReqs = c.m2OwnerPendingReqs[1:]
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
	c.ensureM2State()
	subReq := c.m2OwnerSubReqs[rsp.GetRspTo()]
	if subReq == nil {
		return false
	}

	dataRsp, ok := rsp.(*mem.DataReadyRsp)
	if !ok {
		panic("M2 bitmap owner received a non-data response")
	}

	subReq.batch.lineData[subReq.line] = append([]byte(nil), dataRsp.Data...)
	subReq.batch.remaining--
	delete(c.m2OwnerSubReqs, rsp.GetRspTo())
	c.ToL2.Retrieve(now)

	if subReq.batch.remaining == 0 {
		c.m2OwnerPendingRsps = append(
			c.m2OwnerPendingRsps,
			c.buildM2BitmapRsp(now, subReq.batch),
		)
	}
	return true
}

func (c *Comp) buildM2BitmapRsp(
	now sim.VTimeInSec,
	batch *m2OwnerBatch,
) *BitmapReadRsp {
	trafficBytes := m2BitmapResponseOverhead
	for _, line := range bitmapLines(batch.req.LineBitmap) {
		trafficBytes += len(batch.lineData[line])
	}

	return &BitmapReadRsp{
		MsgMeta: sim.MsgMeta{
			ID:           sim.GetIDGenerator().Generate(),
			Src:          c.ToOutside,
			Dst:          batch.req.Meta().Src,
			SendTime:     now,
			TrafficBytes: trafficBytes,
		},
		BatchID:       batch.req.BatchID,
		RequesterName: batch.req.RequesterName,
		OwnerName:     batch.req.OwnerName,
		PID:           batch.req.PID,
		PagePAddr:     batch.req.PagePAddr,
		LineBitmap:    batch.req.LineBitmap,
		LineCount:     batch.req.LineCount,
		LineSize:      batch.req.LineSize,
		LineData:      batch.lineData,
		RespondTo:     batch.req.Meta().ID,
	}
}

func (c *Comp) processM2OwnerPendingBatchRsps(now sim.VTimeInSec) bool {
	madeProgress := false
	for len(c.m2OwnerPendingRsps) > 0 {
		rsp := c.m2OwnerPendingRsps[0]
		rsp.Meta().SendTime = now
		err := c.ToOutside.Send(rsp)
		if err != nil {
			return madeProgress
		}
		c.M2Stats.OwnerBatchResponses++
		memtrace.RegisterMemoryPathNetworkMessage(
			rdmaAccessReqInfo(rsp),
			rsp.GetRspTo(),
			rsp.Meta().ID,
			"return",
		)
		c.m2OwnerPendingRsps = c.m2OwnerPendingRsps[1:]
		madeProgress = true
	}
	return madeProgress
}

func bitmapLines(bitmap uint64) []uint64 {
	lines := make([]uint64, 0, bitsLen(bitmap))
	for i := uint64(0); i < 64; i++ {
		if bitmap&(uint64(1)<<i) != 0 {
			lines = append(lines, i)
		}
	}
	sort.Slice(lines, func(i, j int) bool { return lines[i] < lines[j] })
	return lines
}

func bitsLen(bitmap uint64) int {
	count := 0
	for bitmap != 0 {
		count += int(bitmap & 1)
		bitmap >>= 1
	}
	return count
}
