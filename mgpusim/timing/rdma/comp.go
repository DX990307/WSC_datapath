// Package rdma provides the implementation of an RDMA engine.
package rdma

import (
	"log"
	"reflect"

	"github.com/sarchlab/akita/v3/mem/mem"
	memtrace "github.com/sarchlab/akita/v3/mem/trace"
	"github.com/sarchlab/akita/v3/sim"
	"github.com/sarchlab/akita/v3/tracing"
)

type transaction struct {
	fromInside  sim.Msg
	fromOutside sim.Msg
	toInside    sim.Msg
	toOutside   sim.Msg
}

// An Comp is a component that helps one GPU to access the memory on
// another GPU
type Comp struct {
	*sim.TickingComponent

	ToOutside sim.Port

	ToL1 sim.Port
	ToL2 sim.Port

	CtrlPort sim.Port

	isDraining              bool
	pauseIncomingReqsFromL1 bool
	currentDrainReq         *DrainReq

	localModules             mem.LowModuleFinder
	RemoteRDMAAddressTable   mem.LowModuleFinder
	remoteCacheModules       mem.LowModuleFinder
	pipelineWidth            int
	pipelineLatency          int
	maxOutstanding           int
	pipelineWaitCycles       uint64
	requesterFullStalls      uint64
	ownerFullStalls          uint64
	peakRequesterOutstanding int
	peakOwnerOutstanding     int

	transactionsFromOutside []transaction
	transactionsFromInside  []transaction

	remoteConfig           RemoteDataPathConfig
	remoteBatches          map[remoteBatchKey]*remoteBatch
	remoteBatchOrder       []remoteBatchKey
	remoteLines            map[remoteLineKey]*remoteLineEntry
	remoteProbes           map[string]*remoteProbe
	remotePendingBatch     []*remoteLineEntry
	remoteSingleInflight   map[string]*remoteLineEntry
	remoteBitmapInflight   map[string]*remoteBatch
	remoteFillInflight     map[string]*remoteLineEntry
	remoteReady            []*remoteLineEntry
	remoteEpochs           map[remoteLineIdentity]uint64
	remoteUncacheable      map[remoteLineIdentity]bool
	remoteReuse            *remoteReuseTable
	remoteOutstandingReads int
	remoteOwnerPendingReq  []*remoteOwnerSubReq
	remoteOwnerSubReqs     map[string]*remoteOwnerSubReq
	remoteOwnerPendingRsp  []*BitmapReadRsp
	RemoteDataPathStats    RemoteDataPathStats

	firstSeenFromL1Req      map[string]sim.VTimeInSec
	firstSeenFromOutsideReq map[string]sim.VTimeInSec
	firstSeenFromL2Rsp      map[string]sim.VTimeInSec
	firstSeenFromOutsideRsp map[string]sim.VTimeInSec
}

// SetLocalModuleFinder sets the table to lookup for local data.
func (c *Comp) SetLocalModuleFinder(lmf mem.LowModuleFinder) {
	c.localModules = lmf
}

// SetRemoteCacheModuleFinder sets the requester-local L2 slice finder. Unlike
// localModules, this finder must not reject addresses owned by another GPU.
func (c *Comp) SetRemoteCacheModuleFinder(lmf mem.LowModuleFinder) {
	c.remoteCacheModules = lmf
}

// Tick checks if make progress
func (c *Comp) Tick(now sim.VTimeInSec) bool {
	madeProgress := false

	madeProgress = c.processFromCtrlPort(now) || madeProgress
	if c.isDraining {
		madeProgress = c.drainRDMA(now) || madeProgress
	}
	if c.remoteConfig.Enabled || len(c.remoteOwnerPendingReq) > 0 ||
		len(c.remoteOwnerPendingRsp) > 0 {
		madeProgress = c.runPipelineWidth(
			func() bool { return c.processRemoteReady(now) }) || madeProgress
		madeProgress = c.runPipelineWidth(
			func() bool { return c.processRemoteOwnerPendingReqs(now) }) || madeProgress
		madeProgress = c.runPipelineWidth(
			func() bool { return c.processRemoteOwnerPendingRsps(now) }) || madeProgress
	}
	madeProgress = c.processFromL1(now) || madeProgress
	madeProgress = c.processFromL2(now) || madeProgress
	madeProgress = c.processFromOutside(now) || madeProgress
	if c.remoteConfig.Enabled || len(c.remotePendingBatch) > 0 ||
		len(c.remoteBatchOrder) > 0 {
		// First collect all requests admitted by this cycle's RDMA input
		// width, then immediately issue every ready batch allowed by the
		// output width. This forms a scheduling-quantum micro-batch without
		// waiting for requests that have not arrived yet.
		madeProgress = c.runPipelineWidth(
			func() bool { return c.processRemotePendingBatches(now) }) || madeProgress
		madeProgress = c.processRemoteBatches(now, false) || madeProgress
	}

	return madeProgress
}

func (c *Comp) effectivePipelineWidth() int {
	if c.pipelineWidth > 0 {
		return c.pipelineWidth
	}
	return int(^uint(0) >> 1)
}

func (c *Comp) runPipelineWidth(stage func() bool) bool {
	madeProgress := false
	for i := 0; i < c.effectivePipelineWidth(); i++ {
		if !stage() {
			break
		}
		madeProgress = true
	}
	return madeProgress
}

func (c *Comp) pipelineReady(now sim.VTimeInSec, msg sim.Msg) bool {
	if c.pipelineLatency <= 0 {
		return true
	}
	readyAt := c.Freq.NCyclesLater(c.pipelineLatency, msg.Meta().RecvTime)
	if now >= readyAt {
		return true
	}
	c.pipelineWaitCycles++
	return false
}

func (c *Comp) requesterOutstandingCount() int {
	return len(c.transactionsFromInside) + len(c.remoteLines)
}

func (c *Comp) ownerOutstandingCount() int {
	return len(c.transactionsFromOutside) + c.remoteOwnerOccupancy()
}

func (c *Comp) canAcceptRequesterOutstanding(n int) bool {
	if c.maxOutstanding <= 0 {
		return true
	}
	if c.requesterOutstandingCount()+n <= c.maxOutstanding {
		return true
	}
	c.requesterFullStalls++
	return false
}

func (c *Comp) canAcceptOwnerOutstanding(n int) bool {
	if c.maxOutstanding <= 0 {
		return true
	}
	if c.ownerOutstandingCount()+n <= c.maxOutstanding {
		return true
	}
	c.ownerFullStalls++
	return false
}

func (c *Comp) recordRequesterOutstandingPeak() {
	current := c.requesterOutstandingCount()
	if current > c.peakRequesterOutstanding {
		c.peakRequesterOutstanding = current
	}
}

func (c *Comp) recordOwnerOutstandingPeak() {
	current := c.ownerOutstandingCount()
	if current > c.peakOwnerOutstanding {
		c.peakOwnerOutstanding = current
	}
}

func (c *Comp) processFromCtrlPort(now sim.VTimeInSec) bool {
	req := c.CtrlPort.Peek()
	if req == nil {
		return false
	}

	req = c.CtrlPort.Retrieve(now)
	switch req := req.(type) {
	case *DrainReq:
		c.currentDrainReq = req
		c.isDraining = true
		c.pauseIncomingReqsFromL1 = true
		return true
	case *RestartReq:
		return c.processRDMARestartReq(now)
	default:
		log.Panicf("cannot process request of type %s", reflect.TypeOf(req))
		return false
	}
}

func (c *Comp) processRDMARestartReq(now sim.VTimeInSec) bool {
	restartCompleteRsp := RestartRspBuilder{}.
		WithSendTime(now).
		WithSrc(c.CtrlPort).
		WithDst(c.currentDrainReq.Src).
		Build()
	err := c.CtrlPort.Send(restartCompleteRsp)

	if err != nil {
		return false
	}
	c.currentDrainReq = nil
	c.pauseIncomingReqsFromL1 = false

	return true
}

func (c *Comp) drainRDMA(now sim.VTimeInSec) bool {
	if c.processRemoteBatches(now, true) {
		return true
	}
	if c.fullyDrained() {
		drainCompleteRsp := DrainRspBuilder{}.
			WithSendTime(now).
			WithSrc(c.CtrlPort).
			WithDst(c.currentDrainReq.Src).
			Build()

		err := c.CtrlPort.Send(drainCompleteRsp)
		if err != nil {
			return false
		}
		c.isDraining = false
		c.resetRemoteDataPathHistory()
		return true
	}
	return false
}

func (c *Comp) fullyDrained() bool {
	return len(c.transactionsFromOutside) == 0 &&
		len(c.transactionsFromInside) == 0 &&
		!c.remoteDataPathHasPendingWork()
}

func (c *Comp) firstSeen(
	seen *map[string]sim.VTimeInSec,
	id string,
	now sim.VTimeInSec,
) sim.VTimeInSec {
	if *seen == nil {
		*seen = make(map[string]sim.VTimeInSec)
	}
	if first, ok := (*seen)[id]; ok {
		return first
	}
	(*seen)[id] = now
	return now
}

func (c *Comp) forgetSeen(seen map[string]sim.VTimeInSec, id string) {
	if seen == nil {
		return
	}
	delete(seen, id)
}

func (c *Comp) processFromL1(now sim.VTimeInSec) bool {
	if c.pauseIncomingReqsFromL1 {
		return false
	}

	madeProgress := false
	for processed := 0; processed < c.effectivePipelineWidth(); processed++ {
		req := c.ToL1.Peek()
		if req == nil {
			return madeProgress
		}
		if !c.pipelineReady(now, req) {
			// Keep ticking until the head request completes its fixed-latency
			// RDMA pipeline traversal.
			return true
		}

		switch req := req.(type) {
		case mem.AccessReq:
			ret := c.processReqFromL1(now, req)
			if !ret {
				return madeProgress
			}

			madeProgress = true
		default:
			log.Panicf("cannot process request of type %s", reflect.TypeOf(req))
			return false
		}
	}
	return madeProgress
}

func (c *Comp) processFromL2(now sim.VTimeInSec) bool {
	madeProgress := false
	for processed := 0; processed < c.effectivePipelineWidth(); processed++ {
		req := c.ToL2.Peek()
		if req == nil {
			return madeProgress
		}
		if !c.pipelineReady(now, req) {
			return true
		}
		switch req := req.(type) {
		case mem.AccessRsp:
			if c.isRemoteProbeRsp(req) {
				if !c.processRemoteProbeRsp(now, req) {
					return madeProgress
				}
				madeProgress = true
				continue
			}
			if c.isRemoteFillRsp(req) {
				if !c.processRemoteFillRsp(now, req) {
					return madeProgress
				}
				madeProgress = true
				continue
			}
			if c.isRemoteOwnerSubRsp(req) {
				if !c.processRemoteOwnerSubRsp(now, req) {
					return madeProgress
				}
				madeProgress = true
				continue
			}
			ret := c.processRspFromL2(now, req)
			if !ret {
				return madeProgress
			}
			madeProgress = true
		default:
			panic("unknown req type")
		}
	}
	return madeProgress
}

func (c *Comp) processFromOutside(now sim.VTimeInSec) bool {
	madeProgress := false
	for processed := 0; processed < c.effectivePipelineWidth(); processed++ {
		// Preserve packet order at the owner. A bitmap request is unpacked over
		// several cycles; do not let a younger legacy write pass queued reads.
		if len(c.remoteOwnerPendingReq) > 0 {
			return madeProgress
		}
		req := c.ToOutside.Peek()
		if req == nil {
			return madeProgress
		}
		if !c.pipelineReady(now, req) {
			return true
		}
		switch req := req.(type) {
		case *BitmapReadReq:
			if !c.processBitmapReqFromOutside(now, req) {
				return madeProgress
			}
			madeProgress = true
		case *BitmapReadRsp:
			if !c.processBitmapRspFromOutside(now, req) {
				return madeProgress
			}
			madeProgress = true
		case mem.AccessReq:
			ret := c.processReqFromOutside(now, req)
			if !ret {
				return madeProgress
			}
			madeProgress = true
		case mem.AccessRsp:
			ret := c.processRspFromOutside(now, req)
			if !ret {
				return madeProgress
			}
			madeProgress = true
		default:
			log.Panicf("cannot process request of type %s", reflect.TypeOf(req))
			return false
		}
	}
	return madeProgress
}

func (c *Comp) processReqFromL1(
	now sim.VTimeInSec,
	req mem.AccessReq,
) bool {
	_, previouslySeen := c.firstSeenFromL1Req[req.Meta().ID]
	firstSeen := c.firstSeen(&c.firstSeenFromL1Req, req.Meta().ID, now)
	dst := c.RemoteRDMAAddressTable.Find(req.GetAddress())

	if dst == c.ToOutside {
		panic("RDMA loop back detected")
	}
	if !previouslySeen && memtrace.ObservationRemoteTraceEnabled() {
		// Admission is recorded on the first processing attempt rather than
		// after a successful output send. Output backpressure can therefore
		// reorder/delay issue without corrupting arrival-time overlap counts.
		ownerName := ""
		if dst != nil {
			ownerName = dst.Name()
		}
		memtrace.StartRemoteRequest(memtrace.ObservationRemoteRequestStart{
			LogicalRequestID: req.Meta().ID,
			PID:              uint64(req.GetPID()),
			Operation:        remoteObservationOperation(req),
			Address:          req.GetAddress(),
			ByteSize:         req.GetByteSize(),
			RequesterName:    c.Name(),
			OwnerName:        ownerName,
			ArrivalTime:      req.Meta().RecvTime,
		})
	}

	if c.remoteConfig.Enabled {
		if handled, progress := c.tryProcessRemoteReqFromL1(
			now, req, dst, firstSeen,
		); handled {
			return progress
		}
	}
	if !c.canAcceptRequesterOutstanding(1) {
		return false
	}

	cloned := c.cloneReq(req)
	cloned.Meta().Src = c.ToOutside
	cloned.Meta().Dst = dst
	cloned.Meta().SendTime = now
	c.markReqAsRemote(cloned, dst)

	err := c.ToOutside.Send(cloned)
	if err == nil {
		memtrace.IssueRemoteRequest(memtrace.ObservationRemoteRequestIssue{
			LogicalRequestID:    req.Meta().ID,
			IssueTime:           now,
			ForwardWireID:       cloned.Meta().ID,
			ForwardTrafficBytes: uint64(cloned.Meta().TrafficBytes),
		})
		memtrace.MarkObservationRemote(req.Meta().ID)
		memtrace.ObservationTransitionByRequest(
			req.Meta().ID, "requester_rdma_receive",
			"requester_rdma", req.Meta().RecvTime)
		memtrace.LinkObservationRequestFromRequest(
			req.Meta().ID, cloned.Meta().ID, "remote_forward_request")
		memtrace.ObservationTransitionByRequest(
			req.Meta().ID, "requester_rdma_send",
			"remote_request_network", now)
		memtrace.RegisterMemoryPathNetworkMessage(
			rdmaAccessReqInfo(req),
			req.Meta().ID,
			cloned.Meta().ID,
			"request",
		)
		memtrace.RecordMemoryPathRDMARequestFromL1(
			c.Name(),
			rdmaAccessReqInfo(req),
			req.Meta().ID,
			req.Meta().SendTime,
			firstSeen,
			req.Meta().Src,
			req.Meta().Dst,
		)
		memtrace.RecordMemoryPathRDMALocalRequestOutputWait(
			c.Name(),
			rdmaAccessReqInfo(req),
			req.Meta().ID,
			firstSeen,
			now,
		)
		c.ToL1.Retrieve(now)
		c.forgetSeen(c.firstSeenFromL1Req, req.Meta().ID)

		c.traceInsideOutStart(req, cloned)

		//fmt.Printf("%s req inside %s -> outside %s\n",
		//e.Name(), req.GetID(), cloned.GetID())

		trans := transaction{
			fromInside: req,
			toOutside:  cloned,
		}
		c.transactionsFromInside = append(c.transactionsFromInside, trans)
		c.recordRequesterOutstandingPeak()
		if c.remoteConfig.Enabled {
			c.noteLegacyRemoteReqSent(req, dst)
		}

		return true
	}

	return false
}

func (c *Comp) processReqFromOutside(
	now sim.VTimeInSec,
	req mem.AccessReq,
) bool {
	firstSeen := c.firstSeen(&c.firstSeenFromOutsideReq, req.Meta().ID, now)
	dst := c.localModules.Find(req.GetAddress())
	if !c.canAcceptOwnerOutstanding(1) {
		return false
	}

	cloned := c.cloneReq(req)
	cloned.Meta().Src = c.ToL2
	cloned.Meta().Dst = dst
	cloned.Meta().SendTime = now

	err := c.ToL2.Send(cloned)
	if err == nil {
		memtrace.ObservationTransitionByRequest(
			req.Meta().ID, "owner_rdma_receive",
			"owner_rdma_request", req.Meta().RecvTime)
		memtrace.LinkObservationRequestFromRequest(
			req.Meta().ID, cloned.Meta().ID, "owner_l2_request")
		memtrace.ObservationTransitionByRequest(
			req.Meta().ID, "owner_rdma_l2_send", "owner_l2_link", now)
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
		c.ToOutside.Retrieve(now)
		c.forgetSeen(c.firstSeenFromOutsideReq, req.Meta().ID)

		c.traceOutsideInStart(req, cloned)

		//fmt.Printf("%s req outside %s -> inside %s\n",
		//e.Name(), req.GetID(), cloned.GetID())

		trans := transaction{
			fromOutside: req,
			toInside:    cloned,
		}
		c.transactionsFromOutside =
			append(c.transactionsFromOutside, trans)
		c.recordOwnerOutstandingPeak()
		return true
	}
	return false
}

func (c *Comp) processRspFromL2(
	now sim.VTimeInSec,
	rsp mem.AccessRsp,
) bool {
	firstSeen := c.firstSeen(&c.firstSeenFromL2Rsp, rsp.Meta().ID, now)
	transactionIndex := c.findTransactionByRspToID(
		rsp.GetRspTo(), c.transactionsFromOutside)
	trans := c.transactionsFromOutside[transactionIndex]

	rspToOutside := c.cloneRsp(rsp, trans.fromOutside.Meta().ID)
	rspToOutside.Meta().SendTime = now
	rspToOutside.Meta().Src = c.ToOutside
	rspToOutside.Meta().Dst = trans.fromOutside.Meta().Src

	err := c.ToOutside.Send(rspToOutside)
	if err == nil {
		memtrace.ObservationTransitionByRequest(
			trans.toInside.Meta().ID, "owner_rdma_response_receive",
			"owner_rdma_response", rsp.Meta().RecvTime)
		memtrace.LinkObservationRequestFromRequest(
			trans.toInside.Meta().ID, rspToOutside.Meta().ID,
			"remote_return_response")
		memtrace.ObservationTransitionByRequest(
			trans.toInside.Meta().ID, "owner_rdma_response_send",
			"remote_response_network", now)
		memtrace.RegisterMemoryPathNetworkMessage(
			rdmaAccessReqInfo(trans.fromOutside),
			trans.fromOutside.Meta().ID,
			rspToOutside.Meta().ID,
			"return",
		)
		memtrace.RecordMemoryPathRDMAResponseFromL2(
			c.Name(),
			rdmaAccessReqInfo(trans.fromOutside),
			trans.fromOutside.Meta().ID,
			rsp.Meta().ID,
			rsp.Meta().SendTime,
			firstSeen,
			rsp.Meta().Src,
			rsp.Meta().Dst,
		)
		memtrace.RecordMemoryPathRDMARemoteResponseOutputWait(
			c.Name(),
			rdmaAccessReqInfo(trans.fromOutside),
			trans.fromOutside.Meta().ID,
			rsp.Meta().ID,
			firstSeen,
			now,
		)
		c.ToL2.Retrieve(now)
		c.forgetSeen(c.firstSeenFromL2Rsp, rsp.Meta().ID)

		//fmt.Printf("%s rsp inside %s -> outside %s\n",
		//e.Name(), rsp.GetID(), rspToOutside.GetID())

		c.traceOutsideInEnd(trans)

		c.transactionsFromOutside =
			append(c.transactionsFromOutside[:transactionIndex],
				c.transactionsFromOutside[transactionIndex+1:]...)
		return true
	}
	return false
}

func (c *Comp) processRspFromOutside(
	now sim.VTimeInSec,
	rsp mem.AccessRsp,
) bool {
	if c.isRemoteSingleRsp(rsp) {
		return c.processRemoteSingleRsp(now, rsp)
	}
	firstSeen := c.firstSeen(&c.firstSeenFromOutsideRsp, rsp.Meta().ID, now)
	transactionIndex := c.findTransactionByRspToID(
		rsp.GetRspTo(), c.transactionsFromInside)
	trans := c.transactionsFromInside[transactionIndex]

	rspToInside := c.cloneRsp(rsp, trans.fromInside.Meta().ID)
	rspToInside.Meta().SendTime = now
	rspToInside.Meta().Src = c.ToL1
	rspToInside.Meta().Dst = trans.fromInside.Meta().Src

	err := c.ToL1.Send(rspToInside)
	if err == nil {
		memtrace.CompleteRemoteRequest(
			memtrace.ObservationRemoteRequestCompletion{
				LogicalRequestID:   trans.fromInside.Meta().ID,
				CompletionTime:     now,
				ReturnWireID:       rsp.Meta().ID,
				ReturnTrafficBytes: uint64(rsp.Meta().TrafficBytes),
			})
		memtrace.ObservationTransitionByRequest(
			rsp.Meta().ID, "requester_rdma_response_receive",
			"requester_rdma_response", rsp.Meta().RecvTime)
		memtrace.ObservationTransitionByRequest(
			trans.fromInside.Meta().ID, "requester_rdma_l1_send",
			"requester_l1_link", now)
		memtrace.RecordMemoryPathRDMARemoteToLocalResponse(
			c.Name(),
			rdmaAccessReqInfo(trans.fromInside),
			trans.fromInside.Meta().ID,
			rsp.Meta().ID,
			rsp.Meta().SendTime,
			firstSeen,
			rsp.Meta().Src,
			rsp.Meta().Dst,
		)
		memtrace.RecordMemoryPathRDMALocalResponseOutputWait(
			c.Name(),
			rdmaAccessReqInfo(trans.fromInside),
			trans.fromInside.Meta().ID,
			rsp.Meta().ID,
			firstSeen,
			now,
		)
		c.ToOutside.Retrieve(now)
		c.forgetSeen(c.firstSeenFromOutsideRsp, rsp.Meta().ID)

		c.traceInsideOutEnd(trans)
		c.recordRemoteGPMAccess(now, trans, rsp)

		//fmt.Printf("%s rsp outside %s -> inside %s\n",
		//e.Name(), rsp.GetID(), rspToInside.GetID())

		c.transactionsFromInside =
			append(c.transactionsFromInside[:transactionIndex],
				c.transactionsFromInside[transactionIndex+1:]...)

		return true
	}

	return false
}

func (c *Comp) recordRemoteGPMAccess(
	now sim.VTimeInSec,
	trans transaction,
	rsp mem.AccessRsp,
) {
	if !memtrace.L2SourceStatsEnabled() && !memtrace.MemoryPathTraceEnabled() {
		return
	}

	var bytes uint64
	op := "unknown"
	switch rsp := rsp.(type) {
	case *mem.DataReadyRsp:
		bytes = uint64(len(rsp.Data))
		op = "read"
	case *mem.WriteDoneRsp:
		if req, ok := trans.fromInside.(mem.AccessReq); ok {
			bytes = req.GetByteSize()
		}
		op = "write"
	default:
		return
	}

	providerName := ""
	if trans.toOutside != nil && trans.toOutside.Meta().Dst != nil {
		providerName = trans.toOutside.Meta().Dst.Name()
	}

	memtrace.RecordRemoteGPMAccess(
		c.Name(),
		providerName,
		bytes,
		now-trans.fromInside.Meta().SendTime,
		now,
		op,
	)
	memtrace.RecordMemoryPathRemoteGPM(
		rdmaAccessReqInfo(trans.fromInside),
		c.Name(),
		providerName,
		bytes,
		now-trans.fromInside.Meta().SendTime,
		now,
		op,
	)
}

func rdmaAccessReqInfo(req sim.Msg) interface{} {
	switch req := req.(type) {
	case *mem.ReadReq:
		return req.Info
	case *mem.WriteReq:
		return req.Info
	case *BitmapReadReq:
		return req.Info
	default:
		return nil
	}
}

func remoteObservationOperation(req mem.AccessReq) string {
	switch req.(type) {
	case *mem.ReadReq:
		return "read"
	case *mem.WriteReq:
		return "write"
	default:
		return "unknown"
	}
}

func (c *Comp) findTransactionByRspToID(
	rspTo string,
	transactions []transaction,
) int {
	for i, trans := range transactions {
		if trans.toOutside != nil && trans.toOutside.Meta().ID == rspTo {
			return i
		}

		if trans.toInside != nil && trans.toInside.Meta().ID == rspTo {
			return i
		}
	}

	log.Panicf("transaction %s not found", rspTo)
	return 0
}

func (c *Comp) cloneReq(origin mem.AccessReq) mem.AccessReq {
	switch origin := origin.(type) {
	case *mem.ReadReq:
		read := mem.ReadReqBuilder{}.
			WithSendTime(origin.SendTime).
			WithSrc(origin.Src).
			WithDst(origin.Dst).
			WithAddress(origin.Address).
			WithByteSize(origin.AccessByteSize).
			WithPID(origin.PID).
			WithInfo(origin.Info).
			Build()
		read.CanWaitForCoalesce = origin.CanWaitForCoalesce
		read.LookupOnly = origin.LookupOnly
		return read
	case *mem.WriteReq:
		write := mem.WriteReqBuilder{}.
			WithSendTime(origin.SendTime).
			WithSrc(origin.Src).
			WithDst(origin.Dst).
			WithAddress(origin.Address).
			WithData(origin.Data).
			WithDirtyMask(origin.DirtyMask).
			WithPID(origin.PID).
			WithInfo(origin.Info).
			Build()
		write.CanWaitForCoalesce = origin.CanWaitForCoalesce
		return write
	default:
		log.Panicf("cannot clone request of type %s",
			reflect.TypeOf(origin))
	}
	return nil
}

func (c *Comp) markReqAsRemote(req mem.AccessReq, dst sim.Port) {
	if !memtrace.L2SourceStatsEnabled() && !memtrace.MemoryPathTraceEnabled() {
		return
	}

	providerName := ""
	if dst != nil {
		providerName = dst.Name()
	}

	switch req := req.(type) {
	case *mem.ReadReq:
		req.Info = memtrace.WithL2RemoteInfo(req.Info, c.Name(), providerName)
	case *mem.WriteReq:
		req.Info = memtrace.WithL2RemoteInfo(req.Info, c.Name(), providerName)
	}
}

func (c *Comp) cloneRsp(origin mem.AccessRsp, rspTo string) mem.AccessRsp {
	switch origin := origin.(type) {
	case *mem.DataReadyRsp:
		rsp := mem.DataReadyRspBuilder{}.
			WithSendTime(origin.SendTime).
			WithSrc(origin.Src).
			WithDst(origin.Dst).
			WithRspTo(rspTo).
			WithData(origin.Data).
			Build()
		return rsp
	case *mem.WriteDoneRsp:
		rsp := mem.WriteDoneRspBuilder{}.
			WithSendTime(origin.SendTime).
			WithSrc(origin.Src).
			WithDst(origin.Dst).
			WithRspTo(rspTo).
			Build()
		return rsp
	default:
		log.Panicf("cannot clone request of type %s",
			reflect.TypeOf(origin))
	}
	return nil
}

// SetFreq sets freq
func (c *Comp) SetFreq(freq sim.Freq) {
	c.TickingComponent.Freq = freq
}

func (c *Comp) traceInsideOutStart(req mem.AccessReq, cloned mem.AccessReq) {
	if len(c.Hooks()) == 0 {
		return
	}

	tracing.StartTaskWithSpecificLocation(
		tracing.MsgIDAtReceiver(req, c),
		req.Meta().ID+"_req_out",
		c,
		"req_in",
		reflect.TypeOf(req).String(),
		c.Name()+".InsideOut",
		req,
	)

	tracing.StartTaskWithSpecificLocation(
		cloned.Meta().ID+"_req_out",
		tracing.MsgIDAtReceiver(req, c),
		c,
		"req_out",
		reflect.TypeOf(req).String(),
		c.Name()+".InsideOut",
		cloned,
	)
}

func (c *Comp) traceOutsideInStart(req mem.AccessReq, cloned mem.AccessReq) {
	if len(c.Hooks()) == 0 {
		return
	}

	tracing.StartTaskWithSpecificLocation(
		tracing.MsgIDAtReceiver(req, c),
		req.Meta().ID+"_req_out",
		c,
		"req_in",
		reflect.TypeOf(req).String(),
		c.Name()+".OutsideIn",
		req,
	)

	tracing.StartTaskWithSpecificLocation(
		cloned.Meta().ID+"_req_out",
		tracing.MsgIDAtReceiver(req, c),
		c,
		"req_out",
		reflect.TypeOf(req).String(),
		c.Name()+".OutsideIn",
		cloned,
	)
}

func (c *Comp) traceInsideOutEnd(trans transaction) {
	if len(c.Hooks()) == 0 {
		return
	}

	tracing.TraceReqFinalize(trans.toOutside, c)
	tracing.TraceReqComplete(trans.fromInside, c)
}

func (c *Comp) traceOutsideInEnd(trans transaction) {
	tracing.TraceReqFinalize(trans.toInside, c)
	tracing.TraceReqComplete(trans.fromOutside, c)
}
