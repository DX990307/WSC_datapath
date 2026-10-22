package addresstranslator

import (
	"testing"

	"github.com/sarchlab/akita/v3/mem/idealmemcontroller"
	"github.com/sarchlab/akita/v3/mem/mem"
	"github.com/sarchlab/akita/v3/mem/vm"
	"github.com/sarchlab/akita/v3/sim"
)

// stubTranslator answers every translation with the page in its table.
type stubTranslator struct {
	*sim.TickingComponent
	top   sim.Port
	pages map[uint64]vm.Page
}

func (s *stubTranslator) Tick(now sim.VTimeInSec) bool {
	msg := s.top.Peek()
	if msg == nil {
		return false
	}
	req := msg.(*vm.TranslationReq)
	rsp := vm.TranslationRspBuilder{}.WithSendTime(now).WithSrc(s.top).
		WithDst(req.Src).WithRspTo(req.ID).WithPage(s.pages[req.VAddr]).Build()
	if s.top.Send(rsp) != nil {
		return false
	}
	s.top.Retrieve(now)
	return true
}

// sink collects responses for the requester.
type sink struct {
	*sim.TickingComponent
	port sim.Port
	rsps []sim.Msg
}

func (s *sink) Tick(now sim.VTimeInSec) bool {
	msg := s.port.Retrieve(now)
	if msg == nil {
		return false
	}
	s.rsps = append(s.rsps, msg)
	return true
}

type testHook struct {
	blocked   bool
	remap     *vm.Page
	resolves  int
	issued    []uint64
	completed []uint64
}

func (h *testHook) Resolve(now sim.VTimeInSec, at Waker, pid vm.PID,
	vAddr uint64, page vm.Page) (vm.Page, bool) {
	h.resolves++
	if h.blocked {
		return page, false
	}
	if h.remap != nil {
		return *h.remap, true
	}
	return page, true
}

func (h *testHook) Issued(now sim.VTimeInSec, pid vm.PID, vAddr, pAddr uint64) {
	h.issued = append(h.issued, pAddr)
}

func (h *testHook) Completed(now sim.VTimeInSec, pAddr uint64) {
	h.completed = append(h.completed, pAddr)
}

type event struct {
	t sim.VTimeInSec
	f func(sim.VTimeInSec)
}

func (e *event) Time() sim.VTimeInSec      { return e.t }
func (e *event) Handler() sim.Handler      { return e }
func (e *event) IsSecondary() bool         { return false }
func (e *event) Handle(ev sim.Event) error { e.f(ev.Time()); return nil }

func TestMigrationHookHoldsAndRemaps(t *testing.T) {
	engine := sim.NewSerialEngine()
	storage := mem.NewStorage(1 << 20)
	if err := storage.Write(0x2040, []byte{7, 7, 7, 7}); err != nil {
		t.Fatal(err)
	}
	memCtrl := idealmemcontroller.MakeBuilder().WithEngine(engine).
		WithStorage(storage).WithLatency(10).Build("Mem")

	tr := &stubTranslator{pages: map[uint64]vm.Page{
		0x10000: {PID: 1, VAddr: 0x10000, PAddr: 0x1000, PageSize: 4096, Valid: true},
	}}
	tr.TickingComponent = sim.NewTickingComponent("TLB", engine, 1*sim.GHz, tr)
	tr.top = sim.NewLimitNumMsgPort(tr, 4, "TLB.Top")

	cu := &sink{}
	cu.TickingComponent = sim.NewTickingComponent("CU", engine, 1*sim.GHz, cu)
	cu.port = sim.NewLimitNumMsgPort(cu, 4, "CU.Port")

	at := MakeBuilder().WithEngine(engine).WithFreq(1 * sim.GHz).
		WithTranslationProvider(tr.top).
		WithLowModuleFinder(&mem.SingleLowModuleFinder{
			LowModule: memCtrl.GetPortByName("Top")}).
		Build("AT")
	hook := &testHook{blocked: true}
	at.SetMigrationHook(hook)

	conn := sim.NewDirectConnection("Conn", engine, 1*sim.GHz)
	conn.PlugIn(cu.port, 4)
	conn.PlugIn(at.GetPortByName("Top"), 4)
	conn.PlugIn(at.GetPortByName("Bottom"), 4)
	conn.PlugIn(at.GetPortByName("Translation"), 4)
	conn.PlugIn(tr.top, 4)
	conn.PlugIn(memCtrl.GetPortByName("Top"), 4)

	req := mem.ReadReqBuilder{}.WithSrc(cu.port).WithDst(at.GetPortByName("Top")).
		WithAddress(0x10040).WithByteSize(4).WithPID(1).Build()
	engine.Schedule(&event{t: 0, f: func(now sim.VTimeInSec) {
		req.SendTime = now
		if cu.port.Send(req) != nil {
			t.Error("send failed")
		}
	}})
	if err := engine.Run(); err != nil {
		t.Fatal(err)
	}
	if len(cu.rsps) != 0 || len(hook.issued) != 0 || at.NumHeld() != 1 ||
		at.HeldAccesses != 1 {
		t.Fatalf("access not held: rsps=%d issued=%v held=%d",
			len(cu.rsps), hook.issued, at.NumHeld())
	}

	// The page moved to 0x2000 while the access was held; wake the
	// translator as the migration manager does.
	hook.blocked = false
	hook.remap = &vm.Page{PID: 1, VAddr: 0x10000, PAddr: 0x2000, PageSize: 4096,
		Valid: true}
	engine.Schedule(&event{t: 1e-6, f: func(now sim.VTimeInSec) { at.TickLater(now) }})
	if err := engine.Run(); err != nil {
		t.Fatal(err)
	}
	if len(cu.rsps) != 1 || at.NumHeld() != 0 {
		t.Fatalf("held access not released: rsps=%d held=%d", len(cu.rsps), at.NumHeld())
	}
	data := cu.rsps[0].(*mem.DataReadyRsp).Data
	if data[0] != 7 {
		t.Fatalf("access did not use the remapped frame: %v", data)
	}
	if len(hook.issued) != 1 || hook.issued[0] != 0x2040 ||
		len(hook.completed) != 1 || hook.completed[0] != 0x2040 {
		t.Fatalf("issue/complete accounting: %v %v", hook.issued, hook.completed)
	}
}
