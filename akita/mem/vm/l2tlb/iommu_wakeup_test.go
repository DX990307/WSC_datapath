package l2tlb

import (
	"testing"

	"github.com/sarchlab/akita/v3/mem/vm"
	"github.com/sarchlab/akita/v3/sim"
)

// sharedEndpoint mimics the network endpoint that carries the IOMMU's Top
// port in the timing platform: a small source buffer shared by every plugged
// port, Send fails when it is full, and once it drains it notifies every
// plugged port (switching.EndPoint does the same in sendFlitOut).
type sharedEndpoint struct {
	sim.HookableBase

	engine   sim.Engine
	capacity int
	buf      []sim.Msg
	ports    []sim.Port
	iommu    *fakeIOMMU
	pending  bool
}

type drainEvent struct {
	*sim.EventBase
}

func (ep *sharedEndpoint) CanSend(_ sim.Port) bool { return len(ep.buf) < ep.capacity }

func (ep *sharedEndpoint) Send(msg sim.Msg) *sim.SendError {
	if len(ep.buf) >= ep.capacity {
		return sim.NewSendError()
	}

	ep.buf = append(ep.buf, msg)
	ep.scheduleDrain(msg.Meta().SendTime + 5e-9)

	return nil
}

func (ep *sharedEndpoint) scheduleDrain(t sim.VTimeInSec) {
	if ep.pending {
		return
	}

	ep.pending = true
	ep.engine.Schedule(drainEvent{sim.NewEventBase(t, ep)})
}

func (ep *sharedEndpoint) PlugIn(port sim.Port, _ int) {
	port.SetConnection(ep)
	ep.ports = append(ep.ports, port)
}

func (ep *sharedEndpoint) Unplug(_ sim.Port)                            { panic("not implemented") }
func (ep *sharedEndpoint) NotifyAvailable(_ sim.VTimeInSec, _ sim.Port) {}

// Handle drains the buffer: translation requests go to the fake IOMMU, other
// messages (traffic of other devices) just leave.
func (ep *sharedEndpoint) Handle(e sim.Event) error {
	now := e.Time()
	ep.pending = false

	for _, msg := range ep.buf {
		if req, ok := msg.(*vm.TranslationReq); ok {
			ep.iommu.serve(now, req)
		}
	}

	ep.buf = nil

	for _, p := range ep.ports {
		p.NotifyAvailable(now)
	}

	return nil
}

// fakeIOMMU answers each translation request immediately.
type fakeIOMMU struct {
	*sim.TickingComponent
	top       sim.Port
	pageTable vm.PageTable
}

func (f *fakeIOMMU) Tick(_ sim.VTimeInSec) bool { return false }

func (f *fakeIOMMU) serve(now sim.VTimeInSec, req *vm.TranslationReq) {
	page, _ := f.pageTable.Find(req.PID, req.VAddr)
	rsp := vm.TranslationRspBuilder{}.
		WithSendTime(now).
		WithSrc(f.top).
		WithDst(req.Src).
		WithRspTo(req.ID).
		WithPage(page).
		Build()
	rsp.RecvTime = now

	if err := req.Src.Recv(rsp); err != nil {
		panic("outside port full")
	}
}

// agent plays the L1 TLBs: it counts the responses it gets.
type agent struct {
	*sim.TickingComponent
	port sim.Port
	rsps int
}

func (a *agent) Tick(now sim.VTimeInSec) bool {
	if a.port.Retrieve(now) != nil {
		a.rsps++
		return true
	}

	return false
}

// A send to the IOMMU fails while the shared endpoint is full of other
// devices' traffic and the L2TLB has nothing else in flight (empty MSHR). It
// must be woken up when the endpoint drains and finish every request. Before
// the fix the L2TLB sent through the IOMMU's own port, the endpoint notified
// the IOMMU, and the L2TLB never ticked again (the requests stayed in its
// TopPort).
func TestL2TLBWakesUpAfterIOMMUEndpointDrains(t *testing.T) {
	engine := sim.NewSerialEngine()
	pageTable := vm.NewPageTable(12)

	const numReq = 6
	for i := 0; i < numReq; i++ {
		pageTable.Insert(vm.Page{
			PID:      1,
			VAddr:    uint64(i) << 12,
			PAddr:    0x100000 + uint64(i)<<12,
			PageSize: 4096,
			Valid:    true,
			DeviceID: 2, // owned by another device -> goes to the IOMMU
		})
	}

	iommu := &fakeIOMMU{pageTable: pageTable}
	iommu.TickingComponent = sim.NewTickingComponent("IOMMU", engine, 1*sim.GHz, iommu)
	iommu.top = sim.NewLimitNumMsgPort(iommu, 4, "IOMMU.Top")

	tlb := MakeBuilder().
		WithEngine(engine).
		WithFreq(1 * sim.GHz).
		WithNumSets(1).
		WithNumWays(16).
		WithNumMSHREntry(8).
		WithNumReqPerCycle(4).
		WithPageSize(4096).
		WithLog2PageSize(12).
		WithDeviceID(1).
		WithPageTable(pageTable).
		WithIOMMUPort(iommu.top).
		Build("L2TLB")

	ep := &sharedEndpoint{engine: engine, capacity: 1, iommu: iommu}
	ep.PlugIn(iommu.top, 1)
	ep.PlugIn(tlb.GetPortByName("ToIOMMU"), 1)

	// The endpoint is full of another device's message until 50 ns.
	foreign := vm.TranslationRspBuilder{}.WithSrc(iommu.top).Build()
	ep.buf = append(ep.buf, foreign)
	ep.scheduleDrain(50e-9)

	l1 := &agent{}
	l1.TickingComponent = sim.NewTickingComponent("L1TLB", engine, 1*sim.GHz, l1)
	l1.port = sim.NewLimitNumMsgPort(l1, 64, "L1TLB.Bottom")

	topConn := sim.NewDirectConnection("TopConn", engine, 1*sim.GHz)
	topConn.PlugIn(tlb.GetPortByName("Top"), 64)
	topConn.PlugIn(l1.port, 64)

	for i := 0; i < numReq; i++ {
		req := vm.TranslationReqBuilder{}.
			WithSendTime(0).
			WithSrc(l1.port).
			WithDst(tlb.GetPortByName("Top")).
			WithPID(1).
			WithVAddr(uint64(i) << 12).
			WithDeviceID(1).
			Build()
		if err := l1.port.Send(req); err != nil {
			t.Fatalf("cannot send request %d", i)
		}
	}

	if err := engine.Run(); err != nil {
		t.Fatal(err)
	}

	failed, wakeups := tlb.IOMMUSendStats()

	if l1.rsps != numReq {
		t.Fatalf("L2TLB stopped: %d of %d translations answered, %d left in TopPort "+
			"(failed sends %d, wake-ups %d)",
			l1.rsps, numReq, numBuffered(tlb.GetPortByName("Top")), failed, wakeups)
	}

	if failed == 0 || wakeups == 0 {
		t.Fatalf("the test did not exercise a failed IOMMU send: failed=%d wakeups=%d",
			failed, wakeups)
	}

	_, rttN, _, reqN := tlb.MissLatencyStats()
	if rttN[1] != numReq || reqN[1] != numReq || rttN[0] != 0 {
		t.Fatalf("miss statistics: rtt counts %v, request counts %v", rttN, reqN)
	}

	if tlb.toIOMMUPort.Component() != tlb {
		t.Fatal("the ToIOMMU port must belong to the L2TLB")
	}
}

func numBuffered(p sim.Port) int {
	n := 0
	if lp, ok := p.(*sim.LimitNumMsgPort); ok {
		n = lp.NumBuffered()
	}

	return n
}

// Notifications from the shared endpoint must not wake an L2TLB that has no
// failed IOMMU send, so that runs that never hit a full endpoint keep exactly
// the same event schedule as before the fix.
func TestL2TLBIgnoresEndpointNotificationWhenNotBlocked(t *testing.T) {
	engine := sim.NewSerialEngine()
	tlb := MakeBuilder().WithEngine(engine).Build("L2TLB")

	tlb.NotifyPortFree(10e-9, tlb.GetPortByName("ToIOMMU"))
	if err := engine.Run(); err != nil {
		t.Fatal(err)
	}
	if now := engine.CurrentTime(); now != 0 {
		t.Fatalf("an unblocked L2TLB was woken up (time advanced to %v)", now)
	}

	tlb.iommuSendBlocked = true
	tlb.NotifyPortFree(10e-9, tlb.GetPortByName("ToIOMMU"))
	if err := engine.Run(); err != nil {
		t.Fatal(err)
	}
	if now := engine.CurrentTime(); now == 0 {
		t.Fatal("a blocked L2TLB was not woken up")
	}
	if tlb.iommuSendBlocked {
		t.Fatal("the wake-up should clear the blocked flag")
	}
}
