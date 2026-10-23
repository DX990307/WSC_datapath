package pagemigration

import (
	"bytes"
	"testing"

	"github.com/sarchlab/akita/v3/mem/vm"
	"github.com/sarchlab/akita/v3/sim"
)

func adaptConfig(actions ...string) Config {
	c := testConfig(PolicyAccessCounter)
	c.Adapt = DefaultAdaptConfig()
	c.Adapt.Enabled = true
	c.Adapt.Move, c.Adapt.Block, c.Adapt.Spread, c.Adapt.Replicate, c.Adapt.Predict =
		false, false, false, false, false
	for _, a := range actions {
		switch a {
		case "move":
			c.Adapt.Move = true
		case "block":
			c.Adapt.Block = true
		case "replicate":
			c.Adapt.Replicate = true
		case "predict":
			c.Adapt.Predict = true
		}
	}
	return c
}

// adaptRig: two GPMs on tiles 0 and 1 (one hop apart).
func adaptRig(t *testing.T, cfg Config) *testRig {
	r := newRig(t, cfg)
	r.m.gpms[1].Tile, r.m.gpms[2].Tile = 0, 1
	r.m.initAdapt()
	return r
}

func TestLearnerLinearSplitWithWrap(t *testing.T) {
	// KM-like: 1.5 pages per device, wrapping around the 48 devices.
	target := func(i int) int { return (i * 2 / 3) % 48 }
	l := newLearner(2)
	order := []int{5, 6, 7, 9, 10, 40, 41, 100, 101, 102, 103, 104, 105, 106}
	for _, i := range order {
		l.add(i, target(i), 0)
	}
	if l.conf < 0 {
		t.Fatal("no rule trusted")
	}
	for i := 0; i < 400; i++ {
		if got := l.predict(i, 0); got != target(i) {
			t.Fatalf("rule %d predicts %d for page %d, want %d", l.conf, got, i, target(i))
		}
	}
	// A miss withdraws the trust.
	l.add(500, (target(500)+7)%48, 0)
	if l.conf >= 0 {
		t.Fatal("trust kept after a miss")
	}
}

func TestLearnerOwnerOffset(t *testing.T) {
	l := newLearner(2)
	for i := 0; i < 4; i++ {
		l.add(i*100, (i*5+1)%48, i*5%48) // target = owner + 1
	}
	if l.conf != ruleOwner || l.predict(7, 20) != 21 {
		t.Fatalf("owner rule: conf %d predict %d", l.conf, l.predict(7, 20))
	}
}

func TestAdaptBlockMoveSplitsPage(t *testing.T) {
	cfg := adaptConfig("move", "block")
	cfg.Adapt.FixedNS = 0
	cfg.Adapt.Alpha = 1
	r := adaptRig(t, cfg)
	src := 1*8*gb + 0x3000
	r.addPage(0x20000, src, 1, 3)
	h2 := r.m.ATHook(2, true)
	h1 := r.m.ATHook(1, true)
	r.at(0, func(now sim.VTimeInSec) {
		h2.Issued(now, 1, 0x20000, src)
		h2.Completed(now, src)
		h1.Issued(now, 1, 0x20800, src+0x800)
		h1.Completed(now, src+0x800)
		// blocks 0 and 1: GPM 2 only; blocks 2 and 3: the owner (local).
		for k := 0; k < 48; k++ {
			for b := uint64(0); b < 2; b++ {
				remoteRead(r.m, 2, now, src+b*1024+uint64(k%16)*64)
			}
		}
		for k := 0; k < 8; k++ {
			for b := uint64(2); b < 4; b++ {
				r.m.LocalAccess(1, src+b*1024+uint64(k)*64)
			}
		}
		for k := 0; k < 8; k++ { // trigger evaluations
			remoteRead(r.m, 2, now, src+uint64(k)*64)
		}
	})
	if err := r.engine.Run(); err != nil {
		t.Fatal(err)
	}
	p := page(t, r, 0x20000)
	if p.PAddr == src {
		t.Fatalf("page not migrated: %+v %+v", r.m.Stats, r.m.AStats)
	}
	if r.m.AStats.Decided[actBlock][1] != 1 {
		t.Fatalf("expected one block decision: %+v", r.m.AStats.Decided)
	}
	for b := uint64(0); b < 4; b++ {
		want := uint64(2)
		if b >= 2 {
			want = 1
		}
		if got := r.m.homeOf(p.PAddr + b*1024); got != want {
			t.Fatalf("block %d home %d, want %d", b, got, want)
		}
	}
	got, _ := r.storage.Read(p.PAddr, 4096)
	want, _ := r.storage.Read(src, 4096)
	if !bytes.Equal(got, want) {
		t.Fatal("data not copied")
	}
}

func TestPrePlaceDroppedWhenTouched(t *testing.T) {
	cfg := adaptConfig("move")
	r := adaptRig(t, cfg)
	src := 1*8*gb + 0x5000
	r.addPage(0x30000, src, 1, 1)
	r.addPage(0x31000, src+0x1000, 1, 2)
	h2 := r.m.ATHook(2, true)
	d := [blocksPerPage]uint64{2, 2, 2, 2}
	var ok1, ok2 bool
	r.at(0, func(now sim.VTimeInSec) {
		ok1 = r.m.prePlace(now, r.m.getUnit(r.m.unitOf(1, 0x30000)), d, actMove, true)
		ok2 = r.m.prePlace(now, r.m.getUnit(r.m.unitOf(1, 0x31000)), d, actMove, true)
		// the second page is touched while it is copied
		h2.Issued(now, 1, 0x31000, src+0x1000)
	})
	if err := r.engine.Run(); err != nil {
		t.Fatal(err)
	}
	if !ok1 || !ok2 {
		t.Fatal("pre-placement not started")
	}
	if r.m.homeOf(src) != 2 || r.m.homeOf(src+0x1000) != 1 {
		t.Fatalf("homes %d %d", r.m.homeOf(src), r.m.homeOf(src+0x1000))
	}
	if r.m.AStats.PrePlaced != 1 || r.m.AStats.PreRaced != 1 {
		t.Fatalf("stats %+v", r.m.AStats)
	}
	if p := page(t, r, 0x30000); p.PAddr != src {
		t.Fatal("pre-placement must not remap the page")
	}
}

func TestReplicaRedirectAndCollapse(t *testing.T) {
	cfg := adaptConfig()
	cfg.Adapt.ReplicaTiles = []int{1}
	cfg.Adapt.FixedNS = 0
	cfg.Adapt.Alpha = 0.001
	cfg.Adapt.HotK = 2
	r := adaptRig(t, cfg)
	r.m.cfg.Adapt.Replicate = true
	r.m.initAdapt()
	src := 1*8*gb + 0x7000
	r.addPage(0x40000, src, 1, 9)
	u := r.m.getUnit(r.m.unitOf(1, 0x40000))
	a := r.m.adaptUnit(u)
	a.buf = &buffer{conc: true, pages: 1, learn: newLearner(2)}
	h1 := r.m.ATHook(1, true).(*atHook)
	h2 := r.m.ATHook(2, true).(*atHook)
	w := &fakeWaker{}
	r.at(0, func(now sim.VTimeInSec) {
		h1.ResolveRW(now, w, 1, 0x40000, false, page(t, r, 0x40000))
		h1.Issued(now, 1, 0x40000, src)
		h1.Completed(now, src)
		h2.ResolveRW(now, w, 1, 0x40040, false, page(t, r, 0x40000))
		h2.Issued(now, 1, 0x40040, src+0x40)
		h2.Completed(now, src+0x40)
	})
	if err := r.engine.Run(); err != nil {
		t.Fatal(err)
	}
	if r.m.AStats.ReplicaOn != 1 || len(a.replicas) != 1 {
		t.Fatalf("no replica: %+v", r.m.AStats)
	}
	rf := a.replicas[0].frame
	got, _ := r.storage.Read(rf, 4096)
	want, _ := r.storage.Read(src, 4096)
	if !bytes.Equal(got, want) {
		t.Fatal("replica data")
	}
	now := sim.VTimeInSec(1e-3)
	p, ok := h2.ResolveRW(now, w, 1, 0x40080, false, page(t, r, 0x40000))
	if !ok || p.PAddr != rf || p.DeviceID != 2 {
		t.Fatalf("read not redirected: %+v", p)
	}
	p, ok = h1.ResolveRW(now, w, 1, 0x40080, false, page(t, r, 0x40000))
	if !ok || p.PAddr != src {
		t.Fatal("the home reads its own copy")
	}
	// a write collapses the replicas and waits for the shootdown
	if _, ok := h1.ResolveRW(now, w, 1, 0x40000, true, page(t, r, 0x40000)); ok {
		t.Fatal("write not held")
	}
	if p, ok := h2.ResolveRW(now, w, 1, 0x40000, false, page(t, r, 0x40000)); ok ||
		p.PAddr != src {
		t.Fatal("reads during the collapse must wait")
	}
	later := now + 1e-6
	if p, ok := h2.ResolveRW(later, w, 1, 0x40000, false, page(t, r, 0x40000)); !ok ||
		p.PAddr != src {
		t.Fatalf("after the collapse reads go to the master: %+v", p)
	}
	r.m.adaptTick(later)
	if r.m.ppns[rf>>12] == nil || r.m.ppns[rf>>12].state != frameRetired {
		t.Fatal("replica frame not retired")
	}
}

func TestFirstTouchHomesPage(t *testing.T) {
	cfg := testConfig(PolicyObserve)
	cfg.Adapt.FirstTouch = true
	r := adaptRig(t, cfg)
	src := 1*8*gb + 0x9000
	r.addPage(0x50000, src, 1, 1)
	h2 := r.m.ATHook(2, true).(*atHook)
	h1 := r.m.ATHook(1, true).(*atHook)
	w := &fakeWaker{}
	h2.ResolveRW(0, w, 1, 0x50000, false, page(t, r, 0x50000))
	h2.Issued(0, 1, 0x50000, src)
	h1.ResolveRW(0, w, 1, 0x50000, false, page(t, r, 0x50000))
	if r.m.homeOf(src+3*1024) != 2 || r.m.AStats.FirstTouch != 1 {
		t.Fatal("first touch")
	}
	_ = vm.Page{}
}
