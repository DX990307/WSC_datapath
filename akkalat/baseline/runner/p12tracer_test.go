package runner

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sarchlab/mgpusim/v3/kernels"
	"github.com/sarchlab/mgpusim/v3/protocol"
)

func TestWGFootprintAgg(t *testing.T) {
	var buf bytes.Buffer
	a := newWGFootprintAgg(&buf, 3)
	pkt := &kernels.HsaKernelDispatchPacket{
		GridSizeX: 256, GridSizeY: 1, GridSizeZ: 1,
		WorkgroupSizeX: 64, WorkgroupSizeY: 1, WorkgroupSizeZ: 1,
	}
	a.launch("cmd1", "k", pkt, true, 1)
	pkt2 := *pkt
	a.launch("cmd1", "k", &pkt2, true, 1) // same kernel, other GPM's packet
	wg := &kernels.WorkGroup{Packet: &pkt2, IDX: 2}
	req := &protocol.MapWGReq{WorkGroup: wg}
	req.ID = "m1"
	a.wgStart(req, 5, 3, 10)
	for _, addr := range []uint64{0x5000, 0x5040, 0x3000, 0x9000, 0xa000, 0x3010} {
		a.read("m1", addr, 20)
	}
	a.wgEnd("m1", 30, true)
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("lines %q", lines)
	}
	// pages 3, 5, 9 kept (cap 3); 0xa is new -> overflow.
	want := "0,2,5,3,10.000,30.000,20.000,20.000,6,3,1,1,3;2;4"
	if lines[1] != want {
		t.Fatalf("got %s want %s", lines[1], want)
	}
	if len(a.kernels) != 1 {
		t.Fatalf("kernels %d", len(a.kernels))
	}
}

func testGPMs() *p12GPMs {
	// tiles 0,1,2 on row 0 and 7 (0,1), 8 (1,1).
	return newP12GPMs([]string{"GPU[0]", "GPU[1]", "GPU[2]", "GPU[7]", "GPU[8]"})
}

func TestM1InflightDup(t *testing.T) {
	a := newM1Agg(testGPMs(), 5, []float64{1000})
	addr := uint64(3*gpmMemBytes + 0x40) // owner index 2
	a.remoteIssue("a", 0, addr, 1)
	a.remoteIssue("b", 0, addr+4, 2) // same line, same GPM
	a.remoteIssue("c", 3, addr, 3)   // same line, other GPM (1 hop from 0)
	a.remoteDone("a")
	a.remoteDone("b")
	a.remoteIssue("d", 3, addr, 4) // GPM 3 still outstanding: same only
	a.remoteDone("c")
	a.remoteDone("d")
	a.remoteIssue("e", 1, addr, 5) // nothing outstanding
	if a.dupSame != 2 || a.dupSameOnly != 2 || a.dupCross != 1 || a.dupBoth != 0 {
		t.Fatalf("dup same %d only %d cross %d both %d", a.dupSame, a.dupSameOnly,
			a.dupCross, a.dupBoth)
	}
	if a.crossMinHops[1] != 1 {
		t.Fatalf("min hops %v", a.crossMinHops)
	}
	a.ownerIssue("x", 0, addr)
	a.ownerIssue("y", 0, addr)
	a.ownerIssue("z", 1, addr)
	a.ownerDone("x")
	a.ownerDone("y")
	a.ownerDone("z")
	a.ownerIssue("w", 1, addr)
	if a.ownDup != 2 || a.ownDupOther != 1 || a.ownDupSame != 1 {
		t.Fatalf("owner dup %d other %d same %d", a.ownDup, a.ownDupOther, a.ownDupSame)
	}
}

func TestM1Windows(t *testing.T) {
	a := newM1Agg(testGPMs(), 5, []float64{1000})
	addr := uint64(1*gpmMemBytes + 0x80) // owner index 0 (tile 0)
	a.remoteIssue("a", 2, addr, 100)  // tile 2 (2,0)
	a.remoteIssue("b", 4, addr, 200)  // tile 8 (1,1)
	a.remoteIssue("c", 4, addr, 300)  // again
	a.remoteIssue("d", 1, addr, 1500) // next window, alone
	dir := t.TempDir()
	if err := a.write(filepath.Join(dir, "m")); err != nil {
		t.Fatal(err)
	}
	w := a.windows[0]
	if w.windows != 2 {
		t.Fatalf("windows %d", w.windows)
	}
	s2 := w.byK[2]
	// owner (0,0): hops to tile 2 = 2, to tile 8 = 2; pair hops (2,0)-(1,1) = 2.
	// YX tree: to (2,0): right, right; to (1,1): down then right -> 4 links.
	if s2 == nil || s2.lines != 1 || s2.reqs != 3 || s2.sumOwnerHops != 4 ||
		s2.pairs != 1 || s2.sumPairHops != 2 || s2.sumTreeLinks != 4 || s2.sumMaxHops != 2 {
		t.Fatalf("k=2 %+v", s2)
	}
	if s1 := w.byK[1]; s1 == nil || s1.lines != 1 || s1.reqs != 1 {
		t.Fatalf("k=1 %+v", s1)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "m_windows.csv"))
	if !strings.Contains(string(b), "1000,2,2,1,3,4,2,1,2,2,4") {
		t.Fatalf("windows csv %s", b)
	}
}
