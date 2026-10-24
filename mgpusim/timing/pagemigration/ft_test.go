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
