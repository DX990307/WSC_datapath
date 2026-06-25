package l2tlb

import (
	"log"
	"reflect"

	"github.com/sarchlab/akita/v3/mem/mem"
	memtrace "github.com/sarchlab/akita/v3/mem/trace"
	"github.com/sarchlab/akita/v3/mem/vm"
	"github.com/sarchlab/akita/v3/mem/vm/l2tlb/internal"
	"github.com/sarchlab/akita/v3/sim"
	"github.com/sarchlab/akita/v3/tracing"
)

type TimeConsumption struct {
	StartTime sim.VTimeInSec
	EndTime   sim.VTimeInSec
}

// A TLB is a cache that maintains some page information.
type L2TLB struct {
	*sim.TickingComponent

	topPort     sim.Port
	bottomPort  sim.Port
	OutsidePort sim.Port
	controlPort sim.Port
	IOMMUPort   sim.Port

	LowModule sim.Port

	numSets        int
	numWays        int
	pageSize       uint64
	numReqPerCycle int
	TopPortBuffer  []sim.Msg

	Sets []internal.Set

	mshr                mshr
	respondingMSHREntry []*mshrEntry
	log2Pagesize        uint64
	vpnMSHRBaseline     bool
	downstreamReqCount  int
	localReqCount       int
	iommuReqCount       int

	isPaused   bool
	DeviceID   uint64
	pageTable  vm.PageTable
	PageFinder mem.PageFinder

	TimeConsumption map[uint64]TimeConsumption
}

// Reset sets all the entries int he TLB to be invalid
func (tlb *L2TLB) reset() {
	tlb.Sets = make([]internal.Set, tlb.numSets)
	for i := 0; i < tlb.numSets; i++ {
		set := internal.NewSet(tlb.numWays)
		tlb.Sets[i] = set
	}
}

// Tick defines how TLB update states at each cycle
func (tlb *L2TLB) Tick(now sim.VTimeInSec) bool {
	madeProgress := false

	madeProgress = tlb.performCtrlReq(now) || madeProgress

	if !tlb.isPaused {
		for i := 0; i < tlb.numReqPerCycle; i++ {
			madeProgress = tlb.parseBottom(now) || madeProgress
		}
		for i := 0; i < tlb.numReqPerCycle; i++ {
			madeProgress = tlb.respondMSHREntry(now) || madeProgress
		}
		for i := 0; i < tlb.numReqPerCycle; i++ {
			madeProgress = tlb.lookupFromTopPort(now) || madeProgress
			madeProgress = tlb.lookupFromOutsidePort(now) || madeProgress
		}
	}

	return madeProgress
}

func (tlb *L2TLB) respondMSHREntry(now sim.VTimeInSec) bool {
	if len(tlb.respondingMSHREntry) == 0 {
		return false
	}

	mshrEntry := tlb.respondingMSHREntry[0]
	req := mshrEntry.Requests[0]

	page := mshrEntry.page
	rspToTop := vm.TranslationRspBuilder{}.
		WithSendTime(now).
		WithSrc(tlb.topPort).
		WithDst(req.Src).
		WithRspTo(req.ID).
		WithPage(page).
		WithTaskID(req.TaskID).
		WithOriginPort(req.OriginPort).
		Build()

	err := tlb.topPort.Send(rspToTop)
	if err != nil {
		return false
	}

	mshrEntry.Requests = mshrEntry.Requests[1:]
	if len(mshrEntry.Requests) == 0 {
		tlb.respondingMSHREntry = tlb.respondingMSHREntry[1:]
	}

	memtrace.RecordMemoryPathTLBComplete(
		tlb.Name(),
		req.TaskID,
		req.ID,
		req.SendTime,
		now,
	)
	tracing.TraceReqComplete(req, tlb)
	return true
}

func (tlb *L2TLB) lookupFromTopPort(now sim.VTimeInSec) bool {
	msg := tlb.topPort.Peek()
	if msg == nil {
		return false
	}

	req := msg.(*vm.TranslationReq)

	return tlb.processTranslation(now, req)
}

func (tlb *L2TLB) lookupFromOutsidePort(now sim.VTimeInSec) bool {
	msg := tlb.OutsidePort.Peek()
	if msg == nil {
		return false
	}

	switch msg := msg.(type) {
	case *vm.TranslationRsp:
		return tlb.processRsp(now, msg, false)
	default:
		panic("unexpected message type")
	}
}

func (tlb *L2TLB) handleTranslationHit(
	now sim.VTimeInSec,
	req *vm.TranslationReq,
	setID, wayID int,
	page vm.Page,
) bool {
	ok := tlb.sendRspToTop(now, req, page)
	if !ok {
		return false
	}
	tlb.topPort.Retrieve(now)

	tlb.visit(setID, wayID)

	tracing.TraceReqReceive(req, tlb)
	tracing.AddTaskStep(tracing.MsgIDAtReceiver(req, tlb), tlb, "hit")
	memtrace.RecordMemoryPathTLBResult(
		tlb.Name(),
		req.TaskID,
		req.ID,
		"hit",
		req.SendTime,
		now,
	)
	memtrace.RecordMemoryPathTLBComplete(
		tlb.Name(),
		req.TaskID,
		req.ID,
		req.SendTime,
		now,
	)
	tracing.TraceReqComplete(req, tlb)
	tracing.StartTask(req.TaskID,
		tracing.MsgIDAtReceiver(req, tlb),
		tlb, "EvictTest", "*vm.TranslationReq", req)

	return true
}

func (tlb *L2TLB) handleTranslationMiss(
	now sim.VTimeInSec,
	mshrReq *vm.TranslationReq,
) bool {
	if tlb.mshr.IsFull() {
		return false
	}

	fetched := tlb.fetchBottom(now, mshrReq)
	if fetched {
		tracing.TraceReqReceive(mshrReq, tlb)
		tracing.AddTaskStep(tracing.MsgIDAtReceiver(mshrReq, tlb), tlb, "miss")
		memtrace.RecordMemoryPathTLBResult(
			tlb.Name(),
			mshrReq.TaskID,
			mshrReq.ID,
			"miss",
			mshrReq.SendTime,
			now,
		)
		tracing.StartTask(mshrReq.TaskID,
			tracing.MsgIDAtReceiver(mshrReq, tlb),
			tlb, "EvictTest", "*vm.TranslationReq", mshrReq)
		return true
	}

	return false
}

func (tlb *L2TLB) vAddrToSetID(vAddr uint64) (setID int) {
	return int(vAddr / tlb.pageSize % uint64(tlb.numSets))
}

func (tlb *L2TLB) sendRspToTop(
	now sim.VTimeInSec,
	req *vm.TranslationReq,
	page vm.Page,
) bool {
	rsp := vm.TranslationRspBuilder{}.
		WithSendTime(now).
		WithSrc(tlb.topPort).
		WithDst(req.Src).
		WithRspTo(req.ID).
		WithPage(page).
		WithTaskID(req.TaskID).
		WithOriginPort(req.OriginPort).
		Build()

	err := tlb.topPort.Send(rsp)
	return err == nil
}

func (tlb *L2TLB) processTLBMSHRHit(
	now sim.VTimeInSec,
	mshrEntry *mshrEntry,
	mshrReq *vm.TranslationReq,
) bool {
	if tlb.mshr.IsEntryFull(mshrReq.PID, mshrReq.VAddr) {
		return false
	}

	mshrEntry.Requests = append(mshrEntry.Requests, mshrReq)

	tlb.topPort.Retrieve(now)

	tracing.TraceReqReceive(mshrReq, tlb)
	tracing.AddTaskStep(tracing.MsgIDAtReceiver(mshrReq, tlb), tlb, "mshr-hit")
	memtrace.RecordMemoryPathTLBResult(
		tlb.Name(),
		mshrReq.TaskID,
		mshrReq.ID,
		"mshr-hit",
		mshrReq.SendTime,
		now,
	)
	tracing.StartTask(mshrReq.TaskID,
		tracing.MsgIDAtReceiver(mshrReq, tlb),
		tlb, "EvictTest", "*vm.TranslationReq", mshrReq)

	return true
}

func (tlb *L2TLB) fetchBottom(now sim.VTimeInSec, mshrReq *vm.TranslationReq) bool {
	reqToBottom, ok := tlb.sendDownstream(now, mshrReq)
	if !ok {
		return false
	}

	mshrEntry := tlb.mshr.Add(mshrReq.PID, mshrReq.VAddr)
	mshrEntry.Requests = append(mshrEntry.Requests, mshrReq)
	mshrEntry.reqToBottom = reqToBottom

	tlb.topPort.Retrieve(now)

	tracing.TraceReqInitiate(reqToBottom, tlb,
		tracing.MsgIDAtReceiver(mshrReq, tlb))

	return true
}

func (tlb *L2TLB) parseBottom(now sim.VTimeInSec) bool {
	if len(tlb.respondingMSHREntry) != 0 {
		return false
	}

	item := tlb.bottomPort.Peek()
	if item == nil {
		return false
	}

	switch item := item.(type) {
	case *vm.TranslationRsp:
		return tlb.processRsp(now, item, true)
	default:
		panic("unexpected message type")
	}
}

func (tlb *L2TLB) performCtrlReq(now sim.VTimeInSec) bool {
	item := tlb.controlPort.Peek()
	if item == nil {
		return false
	}

	item = tlb.controlPort.Retrieve(now)

	switch req := item.(type) {
	case *FlushReq:
		return tlb.handleTLBFlush(now, req)
	case *RestartReq:
		return tlb.handleTLBRestart(now, req)
	default:
		log.Panicf("cannot process request %s", reflect.TypeOf(req))
	}

	return true
}

func (tlb *L2TLB) visit(setID, wayID int) {
	set := tlb.Sets[setID]
	set.Visit(wayID)
}

func (tlb *L2TLB) handleTLBFlush(now sim.VTimeInSec, req *FlushReq) bool {
	rsp := FlushRspBuilder{}.
		WithSrc(tlb.controlPort).
		WithDst(req.Src).
		WithSendTime(now).
		Build()

	err := tlb.controlPort.Send(rsp)
	if err != nil {
		return false
	}

	for _, vAddr := range req.VAddr {
		setID := tlb.vAddrToSetID(vAddr)
		set := tlb.Sets[setID]
		wayID, page, found := set.Lookup(req.PID, vAddr)
		if !found {
			continue
		}

		page.Valid = false
		set.Update(wayID, page)
	}

	tlb.mshr.Reset()
	tlb.isPaused = true
	return true
}

func (tlb *L2TLB) handleTLBRestart(now sim.VTimeInSec, req *RestartReq) bool {
	rsp := RestartRspBuilder{}.
		WithSendTime(now).
		WithSrc(tlb.controlPort).
		WithDst(req.Src).
		Build()

	err := tlb.controlPort.Send(rsp)
	if err != nil {
		return false
	}

	tlb.isPaused = false

	for tlb.topPort.Retrieve(now) != nil {
		tlb.topPort.Retrieve(now)
	}

	for tlb.bottomPort.Retrieve(now) != nil {
		tlb.bottomPort.Retrieve(now)
	}

	return true
}

func (tlb *L2TLB) processTranslation(now sim.VTimeInSec, req *vm.TranslationReq) bool {
	setID := tlb.vAddrToSetID(req.VAddr)
	set := tlb.Sets[setID]
	wayID, page, found := set.Lookup(req.PID, req.VAddr)
	if found && page.Valid {
		return tlb.handleTranslationHit(now, req, setID, wayID, page)
	}

	mshrEntry := tlb.mshr.GetEntry(req.PID, req.VAddr)

	if mshrEntry != nil {
		return tlb.processTLBMSHRHit(now, mshrEntry, req)
	}

	return tlb.handleTranslationMiss(now, req)
}

func (tlb *L2TLB) processRsp(now sim.VTimeInSec, rsp *vm.TranslationRsp, bottom bool) bool {
	page := rsp.Page

	// fmt.Printf("Received from %s VAddr %d\n", rsp.Src.Name(), page.VAddr)

	mshrEntryPresent := tlb.mshr.IsEntryPresent(rsp.Page.PID, rsp.Page.VAddr)

	if !mshrEntryPresent {
		setID := tlb.vAddrToSetID(page.VAddr)
		set := tlb.Sets[setID]
		wayID, ok, _ := tlb.Sets[setID].Evict()

		if !ok {
			panic("failed to evict")
		}

		set.Update(wayID, page)
		set.Visit(wayID)

		if bottom {
			tlb.bottomPort.Retrieve(now)
			// fmt.Printf("Bottom\n")
		} else {
			tlb.OutsidePort.Retrieve(now)
			// fmt.Printf("OutsidePort\n")
		}

		return true
	}

	setID := tlb.vAddrToSetID(page.VAddr)
	set := tlb.Sets[setID]
	wayID, ok, _ := tlb.Sets[setID].Evict()

	if !ok {
		panic("failed to evict")
	}

	set.Update(wayID, page)
	set.Visit(wayID)

	tlb.mshr.UpdatePage(rsp.Page.PID, rsp.Page.VAddr, page)

	mshrEntry := tlb.mshr.GetEntry(page.PID, page.VAddr)
	if mshrEntry == nil {
		if bottom {
			tlb.bottomPort.Retrieve(now)
		} else {
			tlb.OutsidePort.Retrieve(now)
		}
		return true
	}

	tlb.respondingMSHREntry = append(tlb.respondingMSHREntry, mshrEntry)
	tlb.mshr.Remove(page.PID, page.VAddr)
	if bottom {
		tlb.bottomPort.Retrieve(now)

	} else {
		tlb.OutsidePort.Retrieve(now)
	}
	return true
}

func (tlb *L2TLB) sendToIOMMU(
	req *vm.TranslationReq,
	now sim.VTimeInSec,
) (*vm.TranslationReq, bool) {
	if tlb.IOMMUPort == nil {
		log.Panicf("L2TLB %s does not have an IOMMU port", tlb.Name())
	}

	newReq := vm.TranslationReqBuilder{}.
		WithSendTime(now).
		WithSrc(tlb.OutsidePort).
		WithDst(tlb.IOMMUPort).
		WithPID(req.PID).
		WithVAddr(req.VAddr).
		WithDeviceID(tlb.DeviceID).
		WithTaskID(req.TaskID).
		WithOriginPort(req.OriginPort).
		Build()

	err := tlb.IOMMUPort.Send(newReq)
	if err != nil {
		return nil, false
	}

	return newReq, true
}

func (tlb *L2TLB) sendDownstream(
	now sim.VTimeInSec,
	req *vm.TranslationReq,
) (*vm.TranslationReq, bool) {
	page, found := tlb.pageTable.Find(req.PID, req.VAddr)
	if !found {
		panic("page not found")
	}

	newReq := vm.TranslationReqBuilder{}.
		WithSendTime(now).
		WithPID(req.PID).
		WithVAddr(req.VAddr).
		WithDeviceID(tlb.DeviceID).
		WithTaskID(req.TaskID).
		WithOriginPort(req.OriginPort)

	if page.DeviceID != tlb.DeviceID {
		if tlb.IOMMUPort == nil {
			log.Panicf("L2TLB %s does not have an IOMMU port", tlb.Name())
		}

		translatedReq := newReq.
			WithSrc(tlb.OutsidePort).
			WithDst(tlb.IOMMUPort).
			Build()

		err := tlb.IOMMUPort.Send(translatedReq)
		if err != nil {
			return nil, false
		}

		tlb.downstreamReqCount++
		tlb.iommuReqCount++

		return translatedReq, true
	}

	translatedReq := newReq.
		WithSrc(tlb.bottomPort).
		WithDst(tlb.LowModule).
		Build()

	err := tlb.bottomPort.Send(translatedReq)
	if err != nil {
		return nil, false
	}

	tlb.downstreamReqCount++
	tlb.localReqCount++

	return translatedReq, true
}
