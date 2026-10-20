package kernels

import "testing"

// BuildWG (T19) must produce the same WGs as NextWG walking the grid.
func TestBuildWGMatchesNextWG(t *testing.T) {
	pkts := []*HsaKernelDispatchPacket{
		{GridSizeX: 1000, GridSizeY: 1, GridSizeZ: 1, WorkgroupSizeX: 256, WorkgroupSizeY: 1, WorkgroupSizeZ: 1},
		{GridSizeX: 70, GridSizeY: 33, GridSizeZ: 3, WorkgroupSizeX: 16, WorkgroupSizeY: 8, WorkgroupSizeZ: 2},
	}
	for _, pkt := range pkts {
		info := KernelLaunchInfo{Packet: pkt, PacketAddr: 0x1000}
		b := NewGridBuilder()
		b.SetKernel(info)
		for flat := 0; ; flat++ {
			want := b.NextWG()
			if want == nil {
				if flat != b.NumWG() {
					t.Fatalf("walked %d WGs, NumWG %d", flat, b.NumWG())
				}
				break
			}
			got := BuildWG(info, flat)
			if got.IDX != want.IDX || got.IDY != want.IDY || got.IDZ != want.IDZ ||
				got.CurrSizeX != want.CurrSizeX || got.CurrSizeY != want.CurrSizeY ||
				got.CurrSizeZ != want.CurrSizeZ || got.SizeX != want.SizeX ||
				len(got.WorkItems) != len(want.WorkItems) ||
				len(got.Wavefronts) != len(want.Wavefronts) {
				t.Fatalf("WG %d differs: got %+v want %+v", flat, got, want)
			}
			for i, wf := range got.Wavefronts {
				w := want.Wavefronts[i]
				if wf.FirstWiFlatID != w.FirstWiFlatID || wf.InitExecMask != w.InitExecMask ||
					len(wf.WorkItems) != len(w.WorkItems) || wf.PacketAddress != w.PacketAddress {
					t.Fatalf("WG %d wavefront %d differs", flat, i)
				}
			}
		}
	}
}
