package rdma

import (
	"testing"

	"github.com/sarchlab/akita/v3/sim"
)

func m1wTestTopo() (*M1WTopology, map[[2]int]int) {
	var coords [][2]int
	idx := map[[2]int]int{}
	for y := 0; y < 7; y++ {
		for x := 0; x < 7; x++ {
			if x == 3 && y == 3 {
				continue
			}
			idx[[2]int{x, y}] = len(coords)
			coords = append(coords, [2]int{x, y})
		}
	}
	return NewM1WTopology(7, 7, coords, make([]sim.Port, len(coords))), idx
}

func m1wLine(line uint64, need float64) []M1WLine {
	return []M1WLine{{PID: 1, Line: line, NeedNS: need, Demand: true}}
}

func TestM1WPartitionHubs(t *testing.T) {
	topo, idx := m1wTestTopo()
	for _, part := range []int{2, 3} {
		regionOf, hubOf := m1wPartition(topo, part)
		for g := range topo.Coords {
			h := hubOf[regionOf[g]]
			if regionOf[h] != regionOf[g] {
				t.Fatalf("q%d: hub %d of GPM %d is outside its region", part, h, g)
			}
		}
	}
	regionOf, hubOf := m1wPartition(topo, 2)
	// (1,1): the 4 x 4 corner region lacks the center tile (3,3).
	if got := hubOf[regionOf[idx[[2]int{0, 0}]]]; got != idx[[2]int{1, 1}] {
		t.Fatalf("q2 hub of (0,0) = %v, want (1,1)", topo.Coords[got])
	}
	regionOf, hubOf = m1wPartition(topo, 3)
	if got := hubOf[regionOf[idx[[2]int{0, 0}]]]; got != idx[[2]int{1, 1}] &&
		got != idx[[2]int{0, 1}] && got != idx[[2]int{1, 0}] {
		t.Fatalf("q3 hub of (0,0) = %v", topo.Coords[got])
	}
}

func TestM1WRegionCombining(t *testing.T) {
	topo, idx := m1wTestTopo()
	defer DisableM1WBound()
	b := EnableM1WBound(topo, DefaultM1WBoundConfig())
	a, c, d := idx[[2]int{0, 0}], idx[[2]int{1, 0}], idx[[2]int{0, 1}]
	o := idx[[2]int{6, 6}]
	b.Send("a", a, o, m1wLine(0x1000, 0), 0, 0)
	b.Send("c", c, o, m1wLine(0x1000, 50), 50, 100) // A in flight: covered
	b.OwnerArrive("a", 400)
	b.Complete("a", 1000)
	b.Complete("c", 2000)
	b.Send("d", d, o, m1wLine(0x1000, 1500), 1500, 1500) // after A's response
	b.Complete("d", 2500)
	in := b.Scheme("q2_d2_ret0")
	if in.CoveredSend != 1 || in.CoveredInflight != 1 || in.FarReads != 3 {
		t.Fatalf("ret0: %+v", *in)
	}
	ret := b.Scheme("q2_d2_ret1000")
	if ret.CoveredSend != 2 {
		t.Fatalf("ret1000: covered %d, want 2", ret.CoveredSend)
	}
	if in.FullTxns != 1 || in.SlotSavedNS <= 0 {
		t.Fatalf("slot saving not credited: %+v", *in)
	}
	if b.OracleSend[0] != 1 { // c is 1 hop from a
		t.Fatalf("oracle r1 = %d, want 1", b.OracleSend[0])
	}
	if b.Occ.OwnerSeen[12] != 1 || b.Occ.ReqLegNS[12] != 400 {
		t.Fatalf("occupancy: %+v", b.Occ)
	}
}

func TestM1WBoundaryAndWriteKill(t *testing.T) {
	topo, idx := m1wTestTopo()
	defer DisableM1WBound()
	b := EnableM1WBound(topo, DefaultM1WBoundConfig())
	a, c := idx[[2]int{0, 0}], idx[[2]int{1, 0}]
	o := idx[[2]int{6, 6}]
	b.Send("a", a, o, m1wLine(0x1000, 0), 0, 0)
	b.KernelBoundary(50)
	b.Send("c", c, o, m1wLine(0x1000, 60), 60, 60)
	b.Send("a2", a, o, m1wLine(0x2000, 70), 70, 70)
	b.Write(1, 0x2000)
	b.Send("c2", c, o, m1wLine(0x2000, 80), 80, 80)
	for _, n := range b.SchemeNames() {
		if s := b.Scheme(n); s.CoveredSend != 0 {
			t.Fatalf("%s covered %d after boundary / write", n, s.CoveredSend)
		}
	}
}

func TestM1WPathAndTurn(t *testing.T) {
	topo, idx := m1wTestTopo()
	defer DisableM1WBound()
	b := EnableM1WBound(topo, DefaultM1WBoundConfig())
	o := idx[[2]int{6, 6}]
	far, near := idx[[2]int{0, 0}], idx[[2]int{0, 2}] // same column
	b.Send("f", far, o, m1wLine(0x40, 0), 0, 0)
	b.Send("n", near, o, m1wLine(0x40, 10), 10, 10)
	p := b.Scheme("path")
	if p.CoveredSend != 1 || p.HubHopsCovered != 0 {
		t.Fatalf("path: %+v", *p)
	}
	turn := b.Scheme("turn_ret0") // both turn at (0,6)
	if turn.CoveredSend != 1 || turn.DetourHops != 0 {
		t.Fatalf("turn: %+v", *turn)
	}
}

func TestM1WHold(t *testing.T) {
	topo, idx := m1wTestTopo()
	defer DisableM1WBound()
	b := EnableM1WBound(topo, DefaultM1WBoundConfig())
	r, o := idx[[2]int{0, 0}], idx[[2]int{6, 6}]
	b.Send("1", r, o, m1wLine(0x1000, 0), 0, 0)
	b.Send("2", r, o, m1wLine(0x1040, 200), 200, 200)
	b.Complete("1", 1000)
	b.Complete("2", 1300)
	for _, h := range b.holds {
		want := uint64(1)
		if h.name == "fixed100" {
			want = 0
		}
		if h.stats.Absorbed != want {
			t.Fatalf("%s absorbed %d, want %d", h.name, h.stats.Absorbed, want)
		}
		if want == 1 && h.stats.SlotSavedNS != 1100 {
			t.Fatalf("%s slot saved %g", h.name, h.stats.SlotSavedNS)
		}
	}
}
