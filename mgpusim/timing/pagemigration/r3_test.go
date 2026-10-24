package pagemigration

import (
	"fmt"
	"testing"

	"github.com/sarchlab/akita/v3/mem/vm"
	"github.com/sarchlab/akita/v3/sim"
)

func r3Wafer(t *testing.T) (*Manager, *vm.PageTable) {
	cfg := DefaultConfig()
	cfg.Policy = PolicyAccessCounter
	cfg.MaxMigrations, cfg.CooldownNS = 0, 0
	cfg.CountLocal = true
	cfg.Adapt = DefaultAdaptConfig()
	cfg.Adapt.Enabled = true
	cfg.Adapt.Rule = "r3"
	cfg.Adapt.Spread = false
	cfg.Adapt.Replicate = true
	pt := vm.NewPageTable(12)
	gpms := make([]*GPM, 49)
	eng := sim.NewSerialEngine()
	d := uint64(1)
	for tile := 0; tile < 49; tile++ {
		if tile == 24 {
			continue
		}
		gpms[d] = &GPM{DeviceID: d, MemBase: d * 8 * gb, Tile: tile,
			Engine: NewCopyEngine(fmt.Sprintf("E%d", d), eng, cfg)}
		d++
	}
	m := NewManager("PM", eng, cfg, pt, 12, gpms, nil)
	for i := uint64(0); i < 4; i++ {
		pt.Insert(vm.Page{PID: 1, VAddr: 0x100000 + i*4096, PAddr: 8*gb + i*4096,
			PageSize: 4096, Valid: true, DeviceID: 1, PageBlock: 7})
	}
	return m, &pt
}

// A read-only page at a corner GPM read by every GPM must be replicated
// after a few dozen accesses (the break-even point), not thousands.
func TestR3ReplicatesBroadcastPageEarly(t *testing.T) {
	m, _ := r3Wafer(t)
	u := m.getUnit(m.unitOf(1, 0x100000))
	n := 0
	for k := 0; k < 5000 && m.AStats.Decided[actReplicate][0]+m.AStats.Decided[actReplicate][1]+
		m.AStats.Decided[actReplicate][2]+m.AStats.Decided[actReplicate][3] == 0; k++ {
		dev := uint64(2 + k%47)
		now := sim.VTimeInSec(float64(k) * 1e-9)
		m.adaptCount(now, u, dev, 8*gb+uint64(k%64)*64, true)
		n++
	}
	t.Logf("replicated after %d accesses; evaluations %d", n, m.AStats.Evaluations)
	if n > 200 {
		t.Fatalf("broadcast page replicated only after %d accesses", n)
	}
}

// A private page read only by one remote GPM must be moved there.
func TestR3MovesPrivatePage(t *testing.T) {
	m, _ := r3Wafer(t)
	u := m.getUnit(m.unitOf(1, 0x101000))
	for k := 0; k < 2000 && u.phase == phaseIdle; k++ {
		m.adaptCount(sim.VTimeInSec(float64(k)*1e-9), u, 30, 8*gb+4096+uint64(k%64)*64, true)
	}
	if u.phase == phaseIdle || u.dst != 30 {
		t.Fatalf("private page not moved to its reader: phase %d dst %d, %+v", u.phase, u.dst, m.AStats.Decided)
	}
}

// A pre-placed page that its reader accesses locally (the prediction was
// right) is checked too, and counts as a hit; a wrong one counts as a miss.
func TestR3PrePlaceCheckIsUnbiased(t *testing.T) {
	m, _ := r3Wafer(t)
	right := m.getUnit(m.unitOf(1, 0x102000))
	wrong := m.getUnit(m.unitOf(1, 0x103000))
	for _, u := range []*unit{right, wrong} {
		a := m.adaptUnit(u)
		a.preplaced = true
		if a.buf == nil {
			t.Fatal("page without buffer")
		}
	}
	// the right page was pre-placed (re-homed in place) at its reader 30
	for b := uint64(0); b < blocksPerPage; b++ {
		m.setBlockHome((8*gb+2*4096)>>log2Block+b, 30)
	}
	for k := 0; k < 200; k++ {
		now := sim.VTimeInSec(float64(k) * 1e-9)
		m.adaptCount(now, right, 30, 8*gb+2*4096+uint64(k%64)*64, false) // reader local
		m.adaptCount(now, wrong, 40, 8*gb+3*4096+uint64(k%64)*64, true)  // far reader, remote
	}
	b := right.a.buf
	if m.AStats.PreChecks != 2 || m.AStats.PreCheckHits != 1 || b.preChecks != 2 || b.preHits != 1 {
		t.Fatalf("checks %d hits %d (buffer %d / %d), want 2 / 1",
			m.AStats.PreChecks, m.AStats.PreCheckHits, b.preChecks, b.preHits)
	}
}
