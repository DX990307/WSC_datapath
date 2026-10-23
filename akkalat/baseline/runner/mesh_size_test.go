package runner

import (
	"fmt"
	"testing"

	"github.com/sarchlab/akita/v3/mem/mem"
	"github.com/sarchlab/mgpusim/v3/timing/rdma"
)

// Building the timing platform on an N x N mesh gives N*N-1 GPMs on distinct
// non-center tiles in row-major order (GPU[x+y*N], driver IDs 1..N*N-1), the
// IOMMU on the center tile (checkIOMMUTopology panics otherwise), and one
// RDMA / PMC address-table entry and one L2TLB-table entry per GPM. No
// simulation is run.
func TestTimingPlatformMeshSizes(t *testing.T) {
	for _, n := range []int{7, 9} {
		// T18 requester side on (v4 Complete): the per-hop arrays must hold
		// the mesh diameter (2*(n-1) hops).
		p := MakeR9NanoBuilder().WithMeshSize(n).WithBandwidth(24).
			WithT18(rdma.T18Config{ReqMerge: true, Window: 256}).Build(4)
		want := n*n - 1
		if len(p.GPUs) != want {
			t.Fatalf("n=%d: %d GPUs, want %d", n, len(p.GPUs), want)
		}
		center := (n/2)*n + n/2
		i := 0
		for tile := 0; tile < n*n; tile++ {
			if tile == center {
				continue
			}
			g := p.GPUs[i]
			if name := fmt.Sprintf("GPU[%d]", tile); g.Domain.Name() != name {
				t.Fatalf("n=%d: GPU %d is %s, want %s", n, i, g.Domain.Name(), name)
			}
			if g.GPUID != uint64(i+1) {
				t.Fatalf("n=%d: %s has ID %d, want %d", n, g.Domain.Name(), g.GPUID, i+1)
			}
			i++
		}
		far := p.GPUs[0].RDMAEngine.GetT18Stats().HopDests
		if d := 2 * (n - 1); far[d] != 1 {
			t.Fatalf("n=%d: GPU[0] has %d destinations %d hops away, want 1", n, far[d], d)
		}
		// GPM k (driver ID k) owns physical addresses [k*8GB, (k+1)*8GB).
		rdmaTable := p.GPUs[0].RDMAEngine.RemoteRDMAAddressTable
		for k, g := range p.GPUs {
			addr := uint64(k+1)*8*mem.GB + 4096
			if rdmaTable.Find(addr) != g.RDMAEngine.ToOutside {
				t.Fatalf("n=%d: address %#x does not map to %s", n, addr, g.Domain.Name())
			}
		}
	}
}

// The scaled 9x9 GPM (mi200s): 80 GPMs of 16 CUs, 5 L2 slices and 5 memory
// controllers each; the L2 capacity comes from -l2-cache-size-kb.
func TestScaledMI200GPMOn9x9(t *testing.T) {
	p := lookupGPMPresetKB("mi200s", 5, 4, 1280)
	if p.NumCU() != 16 || p.NumMemoryBanks != 5 || p.L2CacheSizeKB != 1280 {
		t.Fatalf("mi200s: %d CUs, %d banks, %d KiB", p.NumCU(), p.NumMemoryBanks, p.L2CacheSizeKB)
	}
	b := MakeR9NanoBuilder().WithGPMPreset(p).WithMeshSize(9).WithBandwidth(14).
		WithL2CacheSizeKB(1280)
	if b.l2CacheSize != 1280*mem.KB {
		t.Fatalf("L2 capacity %d", b.l2CacheSize)
	}
	pl := b.Build(5)
	if len(pl.GPUs) != 80 {
		t.Fatalf("%d GPUs", len(pl.GPUs))
	}
	for _, g := range pl.GPUs {
		if len(g.CUs) != 16 || len(g.L2Caches) != 5 || len(g.MemControllers) != 5 {
			t.Fatalf("%s: %d CUs, %d L2 slices, %d memory controllers",
				g.Domain.Name(), len(g.CUs), len(g.L2Caches), len(g.MemControllers))
		}
	}
	for _, bad := range []func(){
		func() { lookupGPMPresetKB("mi200s", 8, 2, 1280) },
		func() { lookupGPMPresetKB("mi200s", 5, 2, 0) },
		func() { lookupGPMPresetKB("mi200", 8, 2, 1280) },
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Fatal("bad preset arguments did not panic")
				}
			}()
			bad()
		}()
	}
}
