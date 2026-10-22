package runner

import (
	"testing"

	"github.com/sarchlab/akita/v3/mem/mem"
	"github.com/sarchlab/akita/v3/sim"
)

func TestPlacementFinders(t *testing.T) {
	const gb = uint64(1) << 30
	mk := func(name string) sim.Port { return sim.NewLimitNumMsgPort(nil, 1, name) }
	// GPM at tile 3 owns [8 GB, 16 GB); GPM at tile 5 owns [16 GB, 24 GB).
	l2a, l2b := mk("GPU[3].L2[0]"), mk("GPU[3].L2[1]")
	rdma3 := mk("GPU[3].RDMA.ToL1")
	def := mem.NewInterleavedLowModuleFinder(4096)
	def.LowModules = []sim.Port{l2a, l2b}
	def.UseAddressSpaceLimitation = true
	def.LowAddress = 8 * gb
	def.HighAddress = 16 * gb
	def.ModuleForOtherAddresses = rdma3
	local := *def
	local.UseAddressSpaceLimitation = false
	local.ModuleForOtherAddresses = nil

	own3 := 8*gb + 0x10000  // owned by tile 3
	own5 := 16*gb + 0x20000 // owned by tile 5
	tbl := &placeTable{blocks: map[uint64]int{
		own3 >> 10:          5, // tile 3's block moved to tile 5
		own5 >> 10:          3, // tile 5's block moved to tile 3
		(own5 + 4096) >> 10: 5, // listed at its own owner
	}}
	l1 := &placeL1Finder{t: tbl, self: 3, local: &local, rdma: rdma3, def: def}
	if got := l1.Find(own3); got != rdma3 {
		t.Fatalf("moved-away block: got %v, want RDMA", got.Name())
	}
	if got := l1.Find(own3 + 1024); got == rdma3 {
		t.Fatalf("unlisted local block must stay local")
	}
	if got := l1.Find(own5); got == rdma3 {
		t.Fatalf("block placed here must go to a local L2")
	}
	if got := l1.Find(own5 + 1024); got != rdma3 {
		t.Fatalf("unlisted remote block must go to the RDMA")
	}

	rdma5 := mk("GPU[5].RDMA.ToOutside")
	rdma3o := mk("GPU[3].RDMA.ToOutside")
	shared := mem.NewBankedLowModuleFinder(8 * gb)
	shared.LowModules = []sim.Port{nil, rdma3o, rdma5}
	at := &placeAddrTable{t: tbl, ports: map[int]sim.Port{3: rdma3o, 5: rdma5},
		def: shared}
	if at.Find(own3) != rdma5 || at.Find(own5) != rdma3o ||
		at.Find(own3+1024) != rdma3o || at.Find(own5+1024) != rdma5 {
		t.Fatalf("address table routes wrongly")
	}

	of := &placeOwnerFinder{t: tbl, self: 3, local: &local, def: def}
	if p := of.Find(own5); p != l2a && p != l2b {
		t.Fatalf("owner side: block placed here must be served locally")
	}
	if of.Find(own3+1024) == rdma3 {
		t.Fatalf("owner side: unlisted own block must be served locally")
	}
	if tbl.misrouted != 0 {
		t.Fatalf("unexpected misroute")
	}
	of.Find(own3)
	if tbl.misrouted != 1 {
		t.Fatalf("misroute not counted")
	}

	rnd := &placeTable{seed: 7, tiles: []int{3, 5}}
	seen := map[int]bool{}
	for a := uint64(0); a < 64; a++ {
		tile, ok := rnd.lookup(8*gb + a<<10)
		if !ok {
			t.Fatalf("random mode must place every block")
		}
		seen[tile] = true
	}
	if !seen[3] || !seen[5] {
		t.Fatalf("random mode uses only %v", seen)
	}
}
