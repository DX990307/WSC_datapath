package mmuTLB

import (
	"fmt"
	"log"
	"reflect"

	"github.com/sarchlab/akita/v3/mem/mem"
	"github.com/sarchlab/akita/v3/mem/vm"
	"github.com/sarchlab/akita/v3/mem/vm/mmuTLB/internal"
	"github.com/sarchlab/akita/v3/sim"
	"github.com/sarchlab/akita/v3/tracing"
)

var log2PageSize uint64 = 12

// A TLB is a cache that maintains some page information.
type TLB struct {
	*sim.TickingComponent

	topPort     sim.Port
	bottomPort  sim.Port
	controlPort sim.Port

	LowModule sim.Port

	numSets        int
	numWays        int
	pageSize       uint64
	numReqPerCycle int
	TopPortBuffer  []sim.Msg

	Sets []internal.Set

	mshr                mshr
	respondingMSHREntry []*mshrEntry
	gmmuCacheTable      *mem.MultiPageFinder

	isPrediction bool
	log2PageSize uint64

	InnerLoop  map[uint64]uint64
	MiddleLoop map[uint64]uint64
	isPaused   bool
	pageTable  vm.PageTable

	reqBuffer           []*vm.TranslationReq
	incomingReqCount    int
	downstreamReqCount  int
	lookupLatencyCycles int
	lookupReadyTimes    map[string]sim.VTimeInSec

	// translationRequests     map[uint64]map[vm.PID]*vm.TranslationReq
}

// Reset sets all the entries int he TLB to be invalid
func (tlb *TLB) reset() {
	tlb.Sets = make([]internal.Set, tlb.numSets)
	for i := 0; i < tlb.numSets; i++ {
		set := internal.NewSet(tlb.numWays)
		tlb.Sets[i] = set
	}

	clear(tlb.lookupReadyTimes)
}

// Tick defines how TLB update states at each cycle
func (tlb *TLB) Tick(now sim.VTimeInSec) bool {
	madeProgress := false

	if !tlb.isPaused {
		for i := 0; i < tlb.numReqPerCycle; i++ {
			madeProgress = tlb.respondMSHREntry(now) || madeProgress
		}

		for i := 0; i < tlb.numReqPerCycle; i++ {
			madeProgress = tlb.pushReqBuffer(now) || madeProgress
		}

		for i := 0; i < tlb.numReqPerCycle; i++ {
			madeProgress = tlb.lookup(now) || madeProgress
		}

		for i := 0; i < tlb.numReqPerCycle; i++ {
			madeProgress = tlb.parseBottom(now) || madeProgress
		}
	}

	return madeProgress
}
func (tlb *TLB) pushReqBuffer(now sim.VTimeInSec) bool {
	msg := tlb.topPort.Peek()
	if msg == nil {
		return false
	}

	req := msg.(*vm.TranslationReq)

	tlb.reqBuffer = append(tlb.reqBuffer, req)
	tlb.setLookupReadyTime(now, req)
	tlb.incomingReqCount++
	tlb.topPort.Retrieve(now)

	tracing.TraceReqReceive(req, tlb)
	tracing.AddTaskStep(tracing.MsgIDAtReceiver(req, tlb), tlb, "buffered")
	return true
}

func (tlb *TLB) respondMSHREntry(now sim.VTimeInSec) bool {
	if len(tlb.respondingMSHREntry) == 0 {
		return false
	}

	mshrEntry := tlb.respondingMSHREntry[0]
	req := mshrEntry.Requests[0]
	page := mshrEntry.page

	if req.Src == nil {
		panic(fmt.Sprintf(
			"mmutlb responding request has nil Src pid=%d vAddr=%#x device=%d task=%s",
			req.PID, req.VAddr, req.DeviceID, req.TaskID,
		))
	}

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

	tracing.TraceReqComplete(req, tlb)
	return true
}

func (tlb *TLB) lookup(now sim.VTimeInSec) bool {
	// msg := tlb.topPort.Peek()
	if len(tlb.reqBuffer) == 0 {
		return false
	}

	req := tlb.reqBuffer[0]
	// tlb.reqBuffer = tlb.reqBuffer[1:]

	if req == nil {
		return false
	}

	if !tlb.isLookupReady(now, req) {
		return false
	}

	if tlb.handleTranslationHits(now, req) {
		return true
	}

	mshrEntry := tlb.mshr.GetEntry(req.PID, req.VAddr)
	if mshrEntry != nil {
		return tlb.processTLBMSHRHit(now, mshrEntry, req)
	}

	return tlb.handleTranslationMiss(now, req)
}

func (tlb *TLB) handleTranslationHits(now sim.VTimeInSec, req *vm.TranslationReq) bool {
	setID := tlb.vAddrToSetID(req.VAddr)
	set := tlb.Sets[setID]
	wayID, page, found := set.Lookup(req.PID, req.VAddr)
	if !found || !page.Valid {
		return false
	}

	tlb.visit(setID, wayID)
	if !tlb.sendRspToTop(now, req, page) {
		return false
	}

	tlb.reqBuffer = tlb.reqBuffer[1:]

	return true
}

func (tlb *TLB) handleTranslationMiss(
	now sim.VTimeInSec,
	req *vm.TranslationReq,
) bool {
	if tlb.mshr.IsFull() {
		return false
	}

	if tlb.mshr.IsEntryFull(req.PID, req.VAddr) {
		return false
	}

	fetched := tlb.fetchBottom(now, req)
	if fetched {
		// tlb.topPort.Retrieve(now)
		tlb.reqBuffer = tlb.reqBuffer[1:]

		tracing.TraceReqReceive(req, tlb)
		tracing.AddTaskStep(tracing.MsgIDAtReceiver(req, tlb), tlb, "miss")
		return true
	}

	return false
}

func (tlb *TLB) vAddrToSetID(vAddr uint64) (setID int) {
	return int(vAddr / tlb.pageSize % uint64(tlb.numSets))
}

func (tlb *TLB) sendRspToTop(
	now sim.VTimeInSec,
	req *vm.TranslationReq,
	page vm.Page,
) bool {
	if req.Src == nil {
		panic(fmt.Sprintf(
			"mmutlb hit response has nil Src pid=%d vAddr=%#x device=%d task=%s",
			req.PID, req.VAddr, req.DeviceID, req.TaskID,
		))
	}

	rsp := vm.TranslationRspBuilder{}.
		WithSendTime(now).
		WithSrc(tlb.topPort).
		WithDst(req.Src).
		WithRspTo(req.ID).
		WithPage(page).
		WithOriginPort(req.OriginPort).
		Build()

	err := tlb.topPort.Send(rsp)
	if err != nil {
		// fmt.Printf("Failed to send response to top for page %d, error: %s\n", page.VAddr, err)
		return false
	}

	return true
}

func (tlb *TLB) processTLBMSHRHit(
	now sim.VTimeInSec,
	mshrEntry *mshrEntry,
	req *vm.TranslationReq,
) bool {
	if tlb.mshr.IsEntryFull(req.PID, req.VAddr) {
		return false
	}

	mshrEntry.Requests = append(mshrEntry.Requests, req)
	tlb.reqBuffer = tlb.reqBuffer[1:]

	tracing.TraceReqReceive(req, tlb)
	tracing.AddTaskStep(tracing.MsgIDAtReceiver(req, tlb), tlb, "mshr-hit")

	return true
}

func (tlb *TLB) fetchBottom(now sim.VTimeInSec, req *vm.TranslationReq) bool {
	reqToBottom, ok := tlb.issueBottomReq(now, req)
	if !ok {
		return false
	}

	mshrEntry := tlb.mshr.Add(req.PID, req.VAddr)
	mshrEntry.Requests = append(mshrEntry.Requests, req)
	mshrEntry.reqToBottom = reqToBottom

	return true
}

func (tlb *TLB) parseBottom(now sim.VTimeInSec) bool {
	item := tlb.bottomPort.Peek()
	if item == nil {
		return false
	}

	switch rsp := item.(type) {
	case *vm.TranslationRsp:
		if tlb.handleRsp(now, rsp) {
			tlb.bottomPort.Retrieve(now)
			return true
		}
		return false
	default:
		log.Panicf("cannot process request %s", reflect.TypeOf(item))
	}

	return false
}

func (tlb *TLB) handleRsp(now sim.VTimeInSec, rsp *vm.TranslationRsp) bool {
	page := rsp.Page

	// fmt.Printf("Received from %s VAddr %d\n", rsp.Src.Name(), page.VAddr)

	mshrEntryPresent := tlb.mshr.IsEntryPresent(rsp.Page.PID, rsp.Page.VAddr)
	if !mshrEntryPresent {
		setID := tlb.vAddrToSetID(page.VAddr)
		set := tlb.Sets[setID]
		wayID, ok := tlb.Sets[setID].Evict()
		if !ok {
			panic("failed to evict")
		}
		set.Update(wayID, page)
		set.Visit(wayID)
		return true
	}

	setID := tlb.vAddrToSetID(page.VAddr)
	set := tlb.Sets[setID]
	wayID, ok := tlb.Sets[setID].Evict()
	if !ok {
		panic("failed to evict")
	}
	set.Update(wayID, page)
	set.Visit(wayID)

	tlb.mshr.UpdatePage(rsp.Page.PID, rsp.Page.VAddr, page)

	mshrEntry := tlb.mshr.GetEntry(page.PID, page.VAddr)
	if mshrEntry == nil {
		return true
	}

	tlb.respondingMSHREntry = append(tlb.respondingMSHREntry, mshrEntry)
	tlb.mshr.Remove(page.PID, page.VAddr)

	return true
}

func (tlb *TLB) visit(setID, wayID int) {
	set := tlb.Sets[setID]
	set.Visit(wayID)
}

func (tlb *TLB) getMSHREntryVAddr(vAddr uint64) uint64 {
	return (vAddr >> tlb.log2PageSize) << tlb.log2PageSize
}

func (tlb *TLB) isInRespondingMSHREntry(pid vm.PID, vAddr uint64) bool {
	baseVAddr := tlb.getMSHREntryVAddr(vAddr)
	for _, e := range tlb.respondingMSHREntry {
		if e.pid == pid && e.vAddr == baseVAddr {
			return true
		}
	}
	return false
}

func (tlb *TLB) addToExistingRespondingMSHREntry(req *vm.TranslationReq) bool {
	baseVAddr := tlb.getMSHREntryVAddr(req.VAddr)
	for _, e := range tlb.respondingMSHREntry {
		if e.pid == req.PID && e.vAddr == baseVAddr {
			e.Requests = append(e.Requests, req)
			return true
		}
	}
	return false
}

func (tlb *TLB) issueBottomReq(
	now sim.VTimeInSec,
	req *vm.TranslationReq,
) (*vm.TranslationReq, bool) {
	reqToBottom := vm.TranslationReqBuilder{}.
		WithSendTime(now).
		WithSrc(tlb.bottomPort).
		WithDst(tlb.LowModule).
		WithPID(req.PID).
		WithVAddr(req.VAddr).
		WithDeviceID(req.DeviceID).
		WithTaskID(req.TaskID).
		WithOriginPort(req.OriginPort).
		Build()
	reqToBottom.StartGPUID = req.StartGPUID

	err := tlb.bottomPort.Send(reqToBottom)
	if err != nil {
		return nil, false
	}

	tlb.downstreamReqCount++

	return reqToBottom, true
}

func (tlb *TLB) setLookupReadyTime(
	now sim.VTimeInSec,
	req *vm.TranslationReq,
) {
	if req == nil {
		return
	}

	if tlb.lookupLatencyCycles <= 0 {
		delete(tlb.lookupReadyTimes, req.ID)
		return
	}

	tlb.lookupReadyTimes[req.ID] = tlb.Freq.NCyclesLater(
		tlb.lookupLatencyCycles,
		now,
	)
}

func (tlb *TLB) isLookupReady(
	now sim.VTimeInSec,
	req *vm.TranslationReq,
) bool {
	if req == nil || tlb.lookupLatencyCycles <= 0 {
		return true
	}

	readyTime, found := tlb.lookupReadyTimes[req.ID]
	if !found {
		return true
	}

	if readyTime > now {
		tlb.TickNow(readyTime)
		return false
	}

	delete(tlb.lookupReadyTimes, req.ID)
	return true
}

func (tlb *TLB) lookupRequestPage(req *vm.TranslationReq) (vm.Page, bool) {
	return tlb.pageTable.Find(req.PID, req.VAddr)
}
