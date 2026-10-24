package pagemigration

import (
	"testing"

	"github.com/sarchlab/akita/v3/sim"
)

type nopWaker struct{}

func (nopWaker) TickLater(sim.VTimeInSec) {}

// predict: with no evidence the first touch rents; after the buffer's
// checks show that first touchers are the best place, it buys; "always"
// buys at once. Unmoved pages are checked too (unbiased evidence).
func TestFTPredictLearns(t *testing.T) {
	m, pt := r3Wafer(t)
	m.cfg.Adapt.FT = "predict"
	pg := func(i uint64) (uint64, *unit) {
		va := 0x100000 + i*4096
		return va, m.getUnit(m.unitOf(1, va))
	}
	va, u := pg(0)
	p, _ := (*pt).Find(1, va)
	if _, ok := m.adaptResolve(0, nopWaker{}, 30, u, true, false, p); !ok || m.AStats.FTRent != 1 {
		t.Fatalf("first touch without evidence should rent: %+v", m.AStats)
	}
	// the toucher 30 is the only reader of page 0 (remote, at GPM 1)
	for k := 0; k < 50 && !u.a.ftTested; k++ {
		m.adaptCount(sim.VTimeInSec(float64(k)*1e-9), u, 30, 8*gb+uint64(k%64)*64, true)
	}
	if m.AStats.FTTests != 1 || m.AStats.FTHits != 1 {
		t.Fatalf("checks %d hits %d, want 1 / 1", m.AStats.FTTests, m.AStats.FTHits)
	}
	u.a.buf.ftTests[1] += 4
	u.a.buf.ftHits[1] += 4
	va, u = pg(1)
	p, _ = (*pt).Find(1, va)
	if _, ok := m.adaptResolve(0, nopWaker{}, 30, u, true, false, p); ok || m.AStats.FTMoves != 1 || u.dst != 30 {
		t.Fatalf("first touch with evidence should buy: %+v", m.AStats)
	}
	m.cfg.Adapt.FT = "always"
	va, u = pg(2)
	p, _ = (*pt).Find(1, va)
	if _, ok := m.adaptResolve(0, nopWaker{}, 1, u, true, false, p); !ok {
		t.Fatal("first touch by the home must not migrate")
	}
	va, u = pg(3)
	p, _ = (*pt).Find(1, va)
	if _, ok := m.adaptResolve(0, nopWaker{}, 12, u, true, false, p); ok || m.AStats.FTMoves != 2 {
		t.Fatalf("always: first touch should migrate: %+v", m.AStats)
	}
}

// GRIT: a read-only page touched by several GPMs ping-pongs under on-touch
// until the fourth fault, then switches to duplication; a reader then
// waits for its own copy.
func TestGritOnTouchToDup(t *testing.T) {
	m, pt := r3Wafer(t)
	m.cfg.Adapt.Rule = "grit"
	m.cfg.Adapt.Replicate = false
	u := m.getUnit(m.unitOf(1, 0x100000))
	for i, dev := range []uint64{10, 20, 30} {
		p, _ := (*pt).Find(1, 0x100000)
		if _, ok := m.adaptResolve(0, nopWaker{}, dev, u, true, false, p); ok {
			t.Fatalf("touch %d by %d should fault", i, dev)
		}
		// pretend the migration finished
		p.DeviceID = dev
		p.PAddr = dev*8*gb + 0x5000
		(*pt).Update(p)
		u.phase = phaseIdle
	}
	p, _ := (*pt).Find(1, 0x100000)
	if _, ok := m.adaptResolve(0, nopWaker{}, 40, u, true, false, p); ok {
		t.Fatal("fourth touch should fault (duplication)")
	}
	if u.a.gScheme != gritDup || m.AStats.GritToDup != 1 || m.AStats.GritDupFaults != 1 {
		t.Fatalf("scheme %d stats %+v", u.a.gScheme, m.AStats)
	}
}
