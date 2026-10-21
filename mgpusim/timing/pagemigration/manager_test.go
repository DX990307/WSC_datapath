package pagemigration

import (
	"bytes"
	"testing"

	"github.com/sarchlab/akita/v3/mem/idealmemcontroller"
	"github.com/sarchlab/akita/v3/mem/mem"
	"github.com/sarchlab/akita/v3/mem/vm"
	"github.com/sarchlab/akita/v3/sim"
)

const gb = uint64(1) << 30

type fakeTLB struct {
	dropped []uint64
}

func (t *fakeTLB) InvalidatePage(pid vm.PID, vAddr uint64) bool {
	t.dropped = append(t.dropped, vAddr)
	return true
}

type fakeWaker struct{ woken []sim.VTimeInSec }

func (w *fakeWaker) TickLater(now sim.VTimeInSec) { w.woken = append(w.woken, now) }

// callback is a one-shot event that runs f.
type callback struct {
	t sim.VTimeInSec
	f func(now sim.VTimeInSec)
}

func (c *callback) Time() sim.VTimeInSec     { return c.t }
func (c *callback) Handler() sim.Handler     { return c }
func (c *callback) IsSecondary() bool        { return false }
func (c *callback) Handle(e sim.Event) error { c.f(e.Time()); return nil }

type testRig struct {
	engine  sim.Engine
	storage *mem.Storage
	pt      vm.PageTable
	tlbs    []*fakeTLB
	m       *Manager
}

// newRig builds two GPMs (device IDs 1 and 2) whose "L2" is an ideal memory
// controller on a shared storage, connected by one network connection.
func newRig(t *testing.T, cfg Config) *testRig {
	r := &testRig{
		engine:  sim.NewSerialEngine(),
		storage: mem.NewStorage(3 * 8 * gb),
		pt:      vm.NewPageTable(12),
	}
	net := sim.NewDirectConnection("Net", r.engine, 1*sim.GHz)
	gpms := make([]*GPM, 3)
	for dev := uint64(1); dev <= 2; dev++ {
		ctrl := idealmemcontroller.MakeBuilder().
			WithEngine(r.engine).
			WithStorage(r.storage).
			WithLatency(20).
			Build("L2" + string(rune('0'+dev)))
		e := NewCopyEngine("DMA"+string(rune('0'+dev)), r.engine, cfg)
		e.L2Finder = &mem.SingleLowModuleFinder{LowModule: ctrl.GetPortByName("Top")}
		e.DRAMFinder = e.L2Finder
		conn := sim.NewDirectConnection("L1L2"+string(rune('0'+dev)), r.engine, 1*sim.GHz)
		conn.PlugIn(ctrl.GetPortByName("Top"), 64)
		conn.PlugIn(e.L2Port(), 64)
		conn.PlugIn(e.DRAMPort(), 64)
		net.PlugIn(e.RemotePort(), 64)
		tlb := &fakeTLB{}
		r.tlbs = append(r.tlbs, tlb)
		gpms[dev] = &GPM{DeviceID: dev, MemBase: dev * 8 * gb,
			TLBs: []PageInvalidator{tlb}, Engine: e}
	}
	r.m = NewManager("PM", r.engine, cfg, r.pt, 12, gpms, nil)
	return r
}

func (r *testRig) addPage(vAddr, pAddr, dev uint64, fill byte) {
	r.pt.Insert(vm.Page{PID: 1, VAddr: vAddr, PAddr: pAddr, PageSize: 4096,
		Valid: true, DeviceID: dev})
	data := bytes.Repeat([]byte{fill}, 4096)
	for i := range data {
		data[i] += byte(i)
	}
	if err := r.storage.Write(pAddr, data); err != nil {
		panic(err)
	}
}

func (r *testRig) at(t sim.VTimeInSec, f func(now sim.VTimeInSec)) {
	r.engine.Schedule(&callback{t: t, f: f})
}

func testConfig(p Policy) Config {
	c := DefaultConfig()
	c.Policy = p
	c.SoftwareLatencyNS = 100
	c.ShootdownLatencyNS = 50
	c.CooldownNS = 0
	c.MaxMigrations = 0
	return c
}

func page(t *testing.T, r *testRig, vAddr uint64) vm.Page {
	p, ok := r.pt.Find(1, vAddr)
	if !ok {
		t.Fatalf("page %#x missing", vAddr)
	}
	return p
}

func TestOnFaultMigratesAfterDrainAndCopiesData(t *testing.T) {
	r := newRig(t, testConfig(PolicyOnFault))
	src := 1*8*gb + 0x1000
	r.addPage(0x10000, src, 1, 7)
	h1 := r.m.ATHook(1, true)
	h2 := r.m.ATHook(2, true)
	w := &fakeWaker{}

	// GPM 1 has an access to the page in flight; it completes at 1 us.
	h1.Issued(0, 1, 0x10040, src+0x40)
	r.at(1e-6, func(now sim.VTimeInSec) { h1.Completed(now, src+0x40) })

	var held bool
	r.at(0, func(now sim.VTimeInSec) {
		_, ok := h2.Resolve(now, w, 1, 0x10080, page(t, r, 0x10000))
		held = !ok
	})
	if err := r.engine.Run(); err != nil {
		t.Fatal(err)
	}
	if !held {
		t.Fatal("the faulting access was not held")
	}
	p := page(t, r, 0x10000)
	if p.DeviceID != 2 || p.PAddr != 2*8*gb+6*gb {
		t.Fatalf("page not remapped to GPM 2's frame region: %+v", p)
	}
	got, _ := r.storage.Read(p.PAddr, 4096)
	want, _ := r.storage.Read(src, 4096)
	if !bytes.Equal(got, want) {
		t.Fatal("data not copied")
	}
	s := r.m.Stats
	if s.Faults != 1 || s.Migrations != 1 || s.PagesMigrated != 1 ||
		s.BytesMigrated != 4096 || s.ShootdownEvents != 1 {
		t.Fatalf("stats %+v", s)
	}
	if s.DrainWaitSum <= 0 {
		t.Fatal("the migration did not wait for the in-flight access")
	}
	if len(r.tlbs[0].dropped) != 1 || len(r.tlbs[1].dropped) != 0 {
		t.Fatalf("shootdown must reach the GPMs that accessed the page: %v %v",
			r.tlbs[0].dropped, r.tlbs[1].dropped)
	}
	if len(w.woken) != 1 || w.woken[0] < 1e-6 {
		t.Fatalf("held translator woken %v", w.woken)
	}
	// The faulting GPM now resolves a stale TLB copy to the new frame.
	np, ok := h2.Resolve(2e-6, w, 1, 0x10080, vm.Page{PID: 1, VAddr: 0x10000,
		PAddr: src, DeviceID: 1})
	if !ok || np.PAddr != p.PAddr {
		t.Fatalf("resolve after remap: %+v %v", np, ok)
	}
}

func TestOnFaultCapAndCooldown(t *testing.T) {
	cfg := testConfig(PolicyOnFault)
	cfg.MaxMigrations = 1
	r := newRig(t, cfg)
	r.addPage(0x10000, 1*8*gb, 1, 1)
	h1 := r.m.ATHook(1, true)
	h2 := r.m.ATHook(2, true)
	w := &fakeWaker{}
	r.at(0, func(now sim.VTimeInSec) { h2.Resolve(now, w, 1, 0x10000, page(t, r, 0x10000)) })
	if err := r.engine.Run(); err != nil {
		t.Fatal(err)
	}
	// Pinned on GPM 2: GPM 1's access proceeds remotely without a fault.
	p, ok := h1.Resolve(1e-3, w, 1, 0x10000, page(t, r, 0x10000))
	if !ok || p.DeviceID != 2 || r.m.Stats.Faults != 1 || r.m.Stats.Pinned != 1 {
		t.Fatalf("pinned unit faulted again: %+v %v %+v", p, ok, r.m.Stats)
	}
	// Scalar / instruction translators never fault.
	r2 := newRig(t, testConfig(PolicyOnFault))
	r2.addPage(0x10000, 1*8*gb, 1, 1)
	if _, ok := r2.m.ATHook(2, false).Resolve(0, w, 1, 0x10000, page(t, r2, 0x10000)); !ok ||
		r2.m.Stats.Faults != 0 {
		t.Fatal("non-vector translator faulted")
	}
}

func remoteRead(m *Manager, dev uint64, now sim.VTimeInSec, pAddr uint64) {
	req := mem.ReadReqBuilder{}.WithAddress(pAddr).WithByteSize(64).Build()
	m.RDMAHook(dev)(now, req)
}

func TestAccessCounterThresholdArgmaxAndPingPong(t *testing.T) {
	cfg := testConfig(PolicyAccessCounter)
	cfg.Threshold = 4
	r := newRig(t, cfg)
	src := 1*8*gb + 0x3000
	r.addPage(0x20000, src, 1, 3)
	h2 := r.m.ATHook(2, true)
	h2.Issued(0, 1, 0x20000, src)
	h2.Completed(0, src)
	w := &fakeWaker{}
	r.at(0, func(now sim.VTimeInSec) {
		for i := 0; i < 3; i++ {
			remoteRead(r.m, 2, now, src)
		}
		if r.m.Stats.CounterTriggers != 0 {
			t.Error("triggered below the threshold")
		}
		// Asynchronous: accesses are not held before the shootdown.
		if _, ok := h2.Resolve(now, w, 1, 0x20000, page(t, r, 0x20000)); !ok {
			t.Error("access held before the shootdown")
		}
		remoteRead(r.m, 2, now, src)
	})
	if err := r.engine.Run(); err != nil {
		t.Fatal(err)
	}
	p := page(t, r, 0x20000)
	if p.DeviceID != 2 || r.m.Stats.CounterTriggers != 1 || r.m.Stats.Migrations != 1 {
		t.Fatalf("counter migration failed: %+v %+v", p, r.m.Stats)
	}
	if r.m.Stats.RemoteReadsByOwner[1] != 4 {
		t.Fatalf("owner histogram %v", r.m.Stats.RemoteReadsByOwner)
	}
	// GPM 1 now reads it remotely: it moves back, which is a ping-pong.
	h1 := r.m.ATHook(1, true)
	h1.Issued(1e-3, 1, 0x20000, p.PAddr)
	h1.Completed(1e-3, p.PAddr)
	r.at(1e-3, func(now sim.VTimeInSec) {
		for i := 0; i < 4; i++ {
			remoteRead(r.m, 1, now, p.PAddr)
		}
	})
	if err := r.engine.Run(); err != nil {
		t.Fatal(err)
	}
	if page(t, r, 0x20000).DeviceID != 1 || r.m.Stats.PingPong != 1 {
		t.Fatalf("ping-pong not counted: %+v", r.m.Stats)
	}
	got, _ := r.storage.Read(page(t, r, 0x20000).PAddr, 4096)
	want, _ := r.storage.Read(src, 4096)
	if !bytes.Equal(got, want) {
		t.Fatal("data changed over two migrations")
	}
}

func TestTwoMBUnitMovesTheWholeGroup(t *testing.T) {
	cfg := testConfig(PolicyOnFault)
	cfg.Log2Unit = 21
	r := newRig(t, cfg)
	r.addPage(0x200000, 1*8*gb, 1, 1)
	r.addPage(0x201000, 1*8*gb+0x1000, 1, 2)
	r.addPage(0x3ff000, 2*8*gb, 2, 3)        // already on GPM 2
	r.addPage(0x400000, 1*8*gb+0x2000, 1, 4) // next unit
	h2 := r.m.ATHook(2, true)
	w := &fakeWaker{}
	r.at(0, func(now sim.VTimeInSec) { h2.Resolve(now, w, 1, 0x201010, page(t, r, 0x201000)) })
	if err := r.engine.Run(); err != nil {
		t.Fatal(err)
	}
	for _, va := range []uint64{0x200000, 0x201000, 0x3ff000} {
		if page(t, r, va).DeviceID != 2 {
			t.Fatalf("page %#x not on GPM 2", va)
		}
	}
	if page(t, r, 0x3ff000).PAddr != 2*8*gb || page(t, r, 0x400000).DeviceID != 1 {
		t.Fatal("pages outside the group or already local were moved")
	}
	if r.m.Stats.PagesMigrated != 2 || r.m.Stats.Migrations != 1 {
		t.Fatalf("stats %+v", r.m.Stats)
	}
}

func TestObserveNeverMigrates(t *testing.T) {
	r := newRig(t, testConfig(PolicyObserve))
	r.addPage(0x10000, 1*8*gb, 1, 1)
	h2 := r.m.ATHook(2, true)
	w := &fakeWaker{}
	h2.Issued(0, 1, 0x10000, 1*8*gb)
	for i := 0; i < 1000; i++ {
		remoteRead(r.m, 2, 0, 1*8*gb)
	}
	if _, ok := h2.Resolve(0, w, 1, 0x10000, page(t, r, 0x10000)); !ok {
		t.Fatal("observe held an access")
	}
	if err := r.engine.Run(); err != nil {
		t.Fatal(err)
	}
	if r.m.Stats.Migrations != 0 || page(t, r, 0x10000).DeviceID != 1 {
		t.Fatal("observe migrated")
	}
}

func TestParsePolicyAndValidate(t *testing.T) {
	for in, want := range map[string]Policy{"none": PolicyNone, "fault": PolicyOnFault,
		"counter": PolicyAccessCounter, "observe": PolicyObserve} {
		if p, err := ParsePolicy(in); err != nil || p != want {
			t.Fatalf("%s: %v %v", in, p, err)
		}
	}
	if _, err := ParsePolicy("bogus"); err == nil {
		t.Fatal("bogus policy accepted")
	}
	c := testConfig(PolicyOnFault)
	c.Log2Unit = 11
	if c.Validate(12) == nil {
		t.Fatal("unit smaller than the page accepted")
	}
}

func expectViolation(t *testing.T, what string, f func()) {
	t.Helper()
	defer func() {
		r := recover()
		if _, ok := r.(Violation); !ok {
			t.Fatalf("%s: expected an invariant violation, got %v", what, r)
		}
	}()
	f()
}

// A write already in flight to the old frame when the migration starts must
// land before the copy reads the page (drain), so the new frame has it.
func TestInFlightWriteLandsBeforeCopy(t *testing.T) {
	r := newRig(t, testConfig(PolicyOnFault))
	src := 1*8*gb + 0x5000
	r.addPage(0x50000, src, 1, 9)
	h1 := r.m.ATHook(1, true)
	h2 := r.m.ATHook(2, true)
	w := &fakeWaker{}
	h1.Issued(0, 1, 0x50100, src+0x100) // GPM 1's write, in flight
	r.at(0, func(now sim.VTimeInSec) { h2.Resolve(now, w, 1, 0x50000, page(t, r, 0x50000)) })
	r.at(3e-6, func(now sim.VTimeInSec) {
		// The write is performed at the owner, then acknowledged.
		if err := r.storage.Write(src+0x100, []byte{0xAB, 0xCD}); err != nil {
			t.Error(err)
		}
		h1.Completed(now, src+0x100)
	})
	if err := r.engine.Run(); err != nil {
		t.Fatal(err)
	}
	p := page(t, r, 0x50000)
	got, _ := r.storage.Read(p.PAddr+0x100, 2)
	if p.DeviceID != 2 || got[0] != 0xAB || got[1] != 0xCD {
		t.Fatalf("in-flight write lost: %+v %x", p, got)
	}
}

// After the remap no access may use the old frame (stale L1 / M3 copies of it
// are unreachable), and frames are never reused.
func TestRetiredFramesAndFreshFrames(t *testing.T) {
	cfg := testConfig(PolicyOnFault)
	r := newRig(t, cfg)
	src := 1*8*gb + 0x6000
	r.addPage(0x60000, src, 1, 1)
	h1 := r.m.ATHook(1, true)
	h2 := r.m.ATHook(2, true)
	w := &fakeWaker{}
	h1.Issued(0, 1, 0x60000, src)
	h1.Completed(0, src)
	r.at(0, func(now sim.VTimeInSec) { h2.Resolve(now, w, 1, 0x60000, page(t, r, 0x60000)) })
	if err := r.engine.Run(); err != nil {
		t.Fatal(err)
	}
	first := page(t, r, 0x60000).PAddr
	expectViolation(t, "access to retired frame", func() { h1.Issued(1e-3, 1, 0x60000, src) })
	expectViolation(t, "remote access to retired frame", func() { remoteRead(r.m, 2, 1e-3, src) })

	// Move it back: GPM 1 gets a fresh frame, not its old one.
	np := page(t, r, 0x60000)
	h2.Issued(2e-3, 1, 0x60000, np.PAddr)
	h2.Completed(2e-3, np.PAddr)
	r.at(2e-3, func(now sim.VTimeInSec) { h1.Resolve(now, w, 1, 0x60000, np) })
	if err := r.engine.Run(); err != nil {
		t.Fatal(err)
	}
	back := page(t, r, 0x60000)
	if back.DeviceID != 1 || back.PAddr == src || back.PAddr == first {
		t.Fatalf("frame reused: src %#x first %#x back %#x", src, first, back.PAddr)
	}
}

// With Check on, a line of a new frame cached anywhere is a violation.
func TestCheckFindsCachedLineOfNewFrame(t *testing.T) {
	cfg := testConfig(PolicyOnFault)
	cfg.Check = true
	r := newRig(t, cfg)
	r.addPage(0x70000, 1*8*gb+0x7000, 1, 1)
	newFrame := 2*8*gb + 6*gb
	r.m.gpms[1].HoldsLine = func(a uint64) bool { return a == newFrame+0x40 }
	h2 := r.m.ATHook(2, true)
	w := &fakeWaker{}
	r.at(0, func(now sim.VTimeInSec) { h2.Resolve(now, w, 1, 0x70000, page(t, r, 0x70000)) })
	expectViolation(t, "cached new frame", func() { _ = r.engine.Run() })

	r2 := newRig(t, cfg)
	r2.addPage(0x70000, 1*8*gb+0x7000, 1, 1)
	h := r2.m.ATHook(2, true)
	r2.at(0, func(now sim.VTimeInSec) { h.Resolve(now, w, 1, 0x70000, page(t, r2, 0x70000)) })
	if err := r2.engine.Run(); err != nil {
		t.Fatal(err)
	}
	if r2.m.Stats.CheckedFrames != 2 { // at allocation and at remap
		t.Fatalf("checked %d frames", r2.m.Stats.CheckedFrames)
	}
}
