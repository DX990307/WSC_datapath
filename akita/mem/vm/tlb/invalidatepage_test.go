package tlb

import (
	"testing"

	"github.com/sarchlab/akita/v3/mem/vm"
	"github.com/sarchlab/akita/v3/sim"
)

func TestInvalidatePageDropsOnlyThatPage(t *testing.T) {
	engine := sim.NewSerialEngine()
	tlb := MakeBuilder().WithEngine(engine).WithNumSets(1).WithNumWays(4).
		Build("TLB")
	set := tlb.Sets[0]
	for i, va := range []uint64{0x1000, 0x2000} {
		set.Update(i, vm.Page{PID: 1, VAddr: va, PAddr: va * 2, Valid: true})
	}

	if !tlb.InvalidatePage(1, 0x1000) {
		t.Fatal("valid entry not dropped")
	}
	if tlb.InvalidatePage(1, 0x1000) || tlb.InvalidatePage(1, 0x3000) {
		t.Fatal("dropped an entry that is not there")
	}
	if _, _, found := set.Lookup(1, 0x1000); found {
		t.Fatal("invalidated page still found")
	}
	if _, p, found := set.Lookup(1, 0x2000); !found || !p.Valid {
		t.Fatal("other page lost")
	}
	// Refill of the dropped page in another way is not shadowed when the old
	// way is reused later.
	set.Update(2, vm.Page{PID: 1, VAddr: 0x1000, PAddr: 0x9000, Valid: true})
	set.Update(0, vm.Page{PID: 1, VAddr: 0x4000, Valid: true})
	if _, p, found := set.Lookup(1, 0x1000); !found || p.PAddr != 0x9000 {
		t.Fatal("refilled entry shadowed by the invalidated way")
	}
}
