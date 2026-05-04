package cp

import (
	"github.com/sarchlab/akita/v3/mem/mem"
	"github.com/sarchlab/akita/v3/mem/vm"
	"github.com/sarchlab/akita/v3/sim"
)

type CP struct {
	*sim.TickingComponent

	Engine         sim.Engine
	Freq           sim.Freq
	NumReqPerCycle int

	ToTLB     sim.Port
	ToNetwork sim.Port
	pageTable vm.PageTable

	pageFinder mem.PageFinder
}

func (cp *CP) Tick(now sim.VTimeInSec) bool {
	madeProgress := false
	madeProgress = cp.comparer(now)
	return madeProgress
}

func (cp *CP) comparer(now sim.VTimeInSec) bool {
	madeProgress := false

	msg := cp.ToTLB.Peek()

	if msg == nil {
		return false
	}

	switch req := msg.(type) {
	case *vm.TranslationReq:
		madeProgress = cp.handleTranslationReq(req, now)
	default:
		panic("Unknown message type")
	}
	return madeProgress
}

func (cp *CP) handleTranslationReq(
	req *vm.TranslationReq, now sim.VTimeInSec) bool {

	madeProgress := false

	page, found := cp.pageTable.Find(req.PID, req.VAddr)
	if !found {
		panic("Page not found")
	}

	dstPort := cp.pageFinder.Find(page.DeviceID)

	newReq := vm.TranslationReqBuilder{}.
		WithSendTime(now).
		WithDeviceID(req.DeviceID).
		WithPID(req.PID).
		WithVAddr(req.VAddr).
		WithOriginPort(req.OriginPort).
		WithSrc(cp.ToNetwork).
		WithDst(dstPort).
		WithTaskID(req.TaskID).
		Build()

	err := dstPort.Send(newReq)

	if err != nil {
		return madeProgress
	}

	madeProgress = true
	cp.ToTLB.Retrieve(now)
	return madeProgress
}
