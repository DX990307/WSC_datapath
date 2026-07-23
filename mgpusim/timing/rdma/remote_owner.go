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
	if len(lines) > c.remoteConfig.MaxBatchLines {
		panic("RDMA bitmap request exceeds the configured batch-line limit")
	}
	if c.remoteOwnerOccupancy()+1 > c.remoteConfig.MaxBatches {
		c.ownerFullStalls++
		return false
	}
	if len(c.remoteOwnerSubReqs)+len(lines) >
		c.remoteOwnerChildLineCapacity() {
		c.RemoteDataPathStats.OwnerChildLineFullStalls++
		return false
	}
	if c.maxOutstanding > 0 {
		// A bitmap is one RDMA transaction descriptor. Its line bitmap and
		// bounded child array carry the internal L2 fanout state; charging one
		// outstanding slot per child would erase the tracking benefit of
		// batching and require all child slots to become free atomically.
		if !c.canAcceptOwnerOutstanding(1) {
			return false
		}
	}
	batch := &remoteOwnerBatch{
		req:       req,
		readyData: make(map[uint64][]byte),
	}
	c.remoteOwnerBatches[req.ID] = batch
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
	if uint64(len(c.remoteOwnerSubReqs)) >
		c.RemoteDataPathStats.OwnerPeakChildLines {
		c.RemoteDataPathStats.OwnerPeakChildLines =
			uint64(len(c.remoteOwnerSubReqs))
	}
	c.recordOwnerOutstandingPeak()
	c.ToOutside.Retrieve(now)
	return true
}

func (c *Comp) remoteOwnerOccupancy() int {
	return len(c.remoteOwnerBatches)
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
	sub.batch.readyData[sub.line] = append([]byte(nil), dataRsp.Data...)
	sub.batch.remaining--
	delete(c.remoteOwnerSubReqs, rsp.GetRspTo())
	c.ToL2.Retrieve(now)
	if !sub.batch.readyQueued {
		sub.batch.readyQueued = true
		c.remoteOwnerPendingRsp = append(
			c.remoteOwnerPendingRsp, sub.batch)
	}
	return true
}

func (c *Comp) processRemoteOwnerPendingRsps(now sim.VTimeInSec) bool {
	if len(c.remoteOwnerPendingRsp) == 0 {
		return false
	}
	batch := c.remoteOwnerPendingRsp[0]
	trafficBytes := bitmapRspOverhead +
		len(batch.readyData)*int(remoteLineBytes)
	rsp := &BitmapReadRsp{
		MsgMeta: sim.MsgMeta{
			ID:           sim.GetIDGenerator().Generate(),
			Src:          c.ToOutside,
			Dst:          batch.req.Src,
			SendTime:     now,
			TrafficBytes: trafficBytes,
		},
		RespondTo: batch.req.ID,
		LineData:  batch.readyData,
	}
	rsp.SendTime = now
	if err := c.ToOutside.Send(rsp); err != nil {
		return false
	}
	memtrace.RegisterMemoryPathNetworkMessage(
		nil, rsp.RespondTo, rsp.ID, "return")
	c.RemoteDataPathStats.BitmapResponsePackets++
	c.RemoteDataPathStats.BitmapResponseLines += uint64(len(batch.readyData))
	if batch.remaining > 0 {
		c.RemoteDataPathStats.EarlyBitmapResponses++
	}
	c.remoteOwnerPendingRsp = c.remoteOwnerPendingRsp[1:]
	batch.readyData = make(map[uint64][]byte)
	batch.readyQueued = false
	if batch.remaining == 0 {
		delete(c.remoteOwnerBatches, batch.req.ID)
	}
	return true
}
