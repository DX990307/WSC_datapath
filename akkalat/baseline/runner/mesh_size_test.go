package runner

import (
	"fmt"
	"testing"

	"github.com/sarchlab/akita/v3/mem/mem"
)

// Building the timing platform on an N x N mesh gives N*N-1 GPMs on distinct
// non-center tiles in row-major order (GPU[x+y*N], driver IDs 1..N*N-1), the
// IOMMU on the center tile (checkIOMMUTopology panics otherwise), and one
// RDMA / PMC address-table entry and one L2TLB-table entry per GPM. No
// simulation is run.
func TestTimingPlatformMeshSizes(t *testing.T) {
	for _, n := range []int{7, 9} {
		p := MakeR9NanoBuilder().WithMeshSize(n).WithBandwidth(24).Build(4)
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
