package rdma

import (
	"github.com/sarchlab/akita/v3/mem/mem"
	memtrace "github.com/sarchlab/akita/v3/mem/trace"
	"github.com/sarchlab/akita/v3/sim"
)

func (c *Comp) processBitmapReqFromOutside(
	now sim.VTimeInSec,
	req *BitmapReadReq,
) bool {
	if req.LineBitmap == 0 {
		panic("RDMA received an empty bitmap read request")
	}
	c.ensureRemoteDataPathState()
	lines := bitmapLines(req.LineBitmap)
	lineCount := len(lines)
	if c.maxOutstanding > 0 {
		if !c.canAcceptOwnerOutstanding(lineCount) {
			return false
		}
	} else if c.remoteOwnerOccupancy()+lineCount >
		c.remoteOwnerOutstandingCapacity() {
		return false
	}
	batch := &remoteOwnerBatch{
		req:      req,
		lineData: make(map[uint64][]byte),
	}
	for _, line := range lines {
		address := req.PagePAddr + line*remoteLineBytes
		read := mem.ReadReqBuilder{}.
			WithSendTime(now).
			WithSrc(c.ToL2).
			WithDst(c.localModules.Find(address)).
			WithAddress(address).
			WithByteSize(remoteLineBytes).
			WithPID(req.PID).
			WithInfo(req.Info).
			Build()
		sub := &remoteOwnerSubReq{batch: batch, line: line, read: read}
		batch.remaining++
		c.remoteOwnerPendingReq = append(c.remoteOwnerPendingReq, sub)
		c.remoteOwnerSubReqs[read.ID] = sub
	}
	c.recordOwnerOutstandingPeak()
	c.ToOutside.Retrieve(now)
	return true
}

func (c *Comp) remoteOwnerOccupancy() int {
	occupancy := len(c.remoteOwnerSubReqs)
	for _, rsp := range c.remoteOwnerPendingRsp {
		occupancy += len(rsp.LineData)
	}
	return occupancy
}

func (c *Comp) processRemoteOwnerPendingReqs(now sim.VTimeInSec) bool {
	if len(c.remoteOwnerPendingReq) == 0 {
		return false
	}
	sub := c.remoteOwnerPendingReq[0]
	sub.read.SendTime = now
	if err := c.ToL2.Send(sub.read); err != nil {
		return false
	}
	c.remoteOwnerPendingReq = c.remoteOwnerPendingReq[1:]
	return true
}

func (c *Comp) isRemoteOwnerSubRsp(rsp mem.AccessRsp) bool {
	return c.remoteOwnerSubReqs[rsp.GetRspTo()] != nil
}

func (c *Comp) processRemoteOwnerSubRsp(
	now sim.VTimeInSec,
	rsp mem.AccessRsp,
) bool {
	sub := c.remoteOwnerSubReqs[rsp.GetRspTo()]
	dataRsp, ok := rsp.(*mem.DataReadyRsp)
	if !ok || sub == nil {
		panic("bitmap owner received an invalid L2 response")
	}
	if len(dataRsp.Data) != int(remoteLineBytes) {
		panic("bitmap owner received an invalid cache line")
	}
	sub.batch.lineData[sub.line] = append([]byte(nil), dataRsp.Data...)
	sub.batch.remaining--
	delete(c.remoteOwnerSubReqs, rsp.GetRspTo())
	c.ToL2.Retrieve(now)
	if sub.batch.remaining == 0 {
		trafficBytes := bitmapRspOverhead +
			len(sub.batch.lineData)*int(remoteLineBytes)
		response := &BitmapReadRsp{
			MsgMeta: sim.MsgMeta{
				ID:           sim.GetIDGenerator().Generate(),
				Src:          c.ToOutside,
				Dst:          sub.batch.req.Src,
				SendTime:     now,
				TrafficBytes: trafficBytes,
			},
			RespondTo: sub.batch.req.ID,
			LineData:  sub.batch.lineData,
		}
		c.remoteOwnerPendingRsp = append(c.remoteOwnerPendingRsp, response)
	}
	return true
}

func (c *Comp) processRemoteOwnerPendingRsps(now sim.VTimeInSec) bool {
	if len(c.remoteOwnerPendingRsp) == 0 {
		return false
	}
	rsp := c.remoteOwnerPendingRsp[0]
	rsp.SendTime = now
	if err := c.ToOutside.Send(rsp); err != nil {
		return false
	}
	memtrace.RegisterMemoryPathNetworkMessage(
		nil, rsp.RespondTo, rsp.ID, "return")
	c.remoteOwnerPendingRsp = c.remoteOwnerPendingRsp[1:]
	return true
}
