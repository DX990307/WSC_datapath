package pagemigration

import "testing"

func TestL2rFootprintSlots(t *testing.T) {
	cfg := adaptConfig()
	cfg.Adapt.L2Pull = true
	cfg.Adapt.L2PullSlots = 2
	r := adaptRig(t, cfg)
	m := r.m
	a := &adaptState{}
	m.kernel = 3
	m.l2rNote(a, 1, 0x1000+5*64)
	m.l2rNote(a, 1, 0x1000+7*64)
	m.l2rNote(a, 2, 0x1000)
	if fp := m.l2rFootprint(a, 1); fp != 1<<5|1<<7 {
		t.Fatalf("footprint %x", fp)
	}
	// next kernel: the previous footprint is kept, the current restarts
	m.kernel = 4
	m.l2rNote(a, 1, 0x1000+9*64)
	if s := m.l2rSlot(a, 1); s.prev != 1<<5|1<<7 || s.cur != 1<<9 {
		t.Fatalf("slot %+v", s)
	}
	if fp := m.l2rFootprint(a, 2); fp != 1 {
		t.Fatalf("previous-kernel footprint %x", fp)
	}
	// a third reader replaces the slot with the fewest accesses (GPM 2)
	m.l2rNote(a, 2+1, 0x1000) // device 3 is not in the rig; only the slot matters
	if m.l2rSlot(a, 2) != nil || m.l2rSlot(a, 3) == nil || m.L2Res.SlotReplacements != 1 {
		t.Fatalf("slots %+v", a.l2r.slots)
	}
}
