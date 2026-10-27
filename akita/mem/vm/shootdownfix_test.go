package vm

import "testing"

func TestShootdownFixFill(t *testing.T) {
	var s ShootdownFix
	old := Page{PID: 1, VAddr: 0x1000, PAddr: 0xa000, Valid: true}
	if got := s.FixFill(old); got != old {
		t.Fatal("off: fill must be unchanged")
	}
	fresh := Page{PID: 1, VAddr: 0x1000, PAddr: 0xb000, Valid: true}
	s.Enable(func(pid PID, vAddr uint64) (Page, bool) { return fresh, true })
	if got := s.FixFill(old); got != old {
		t.Fatal("unmarked fill must be unchanged")
	}
	s.Mark(1, 0x1000)
	if got := s.FixFill(old); got != fresh {
		t.Fatal("marked fill must take the page table entry")
	}
	if got := s.FixFill(old); got != old {
		t.Fatal("the mark is consumed by one fill")
	}
}
