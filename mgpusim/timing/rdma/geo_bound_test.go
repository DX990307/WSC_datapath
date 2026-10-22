package rdma

import (
	"testing"

	"github.com/sarchlab/akita/v3/sim"
)

type geoTestPort struct {
	sim.Port
	name string
}

func (p *geoTestPort) Name() string { return p.name }

func geoTestTopology() *GeoTopology {
	var coords [][2]int
	var ports []sim.Port
	for y := 0; y < 7; y++ {
		for x := 0; x < 7; x++ {
			if x == 3 && y == 3 {
				continue
			}
			coords = append(coords, [2]int{x, y})
			ports = append(ports, &geoTestPort{name: "p"})
		}
	}
	return NewGeoTopology(7, 7, coords, ports)
}

// gpmAt returns the GPM index of tile (x, y).
func gpmAt(t *GeoTopology, x, y int) int {
	for i, c := range t.Coords {
		if c == [2]int{x, y} {
			return i
		}
	}
	return -1
}

func TestGeoTopologyNeighborsAndTree(t *testing.T) {
	topo := geoTestTopology()
	if topo.NumGPMs() != 48 {
		t.Fatalf("GPMs = %d", topo.NumGPMs())
	}
	corner := gpmAt(topo, 0, 0)
	if n := len(topo.Neighbors(corner, 1)); n != 2 {
		t.Fatalf("corner 1-hop neighbors = %d, want 2", n)
	}
	if n := len(topo.Neighbors(corner, 2)); n != 5 {
		t.Fatalf("corner 2-hop neighbors = %d, want 5", n)
	}
	// Tile (3,2) is next to the center (not a GPM): 3 one-hop neighbors.
	if n := len(topo.Neighbors(gpmAt(topo, 3, 2), 1)); n != 3 {
		t.Fatalf("(3,2) 1-hop neighbors = %d, want 3", n)
	}
	// Owner (0,0), requester (0,6): Y-then-X route goes down column 0.
	// Radius-1 targets: (1,6) and (0,5); (0,5) is on the route already,
	// (1,6) adds one link -> 1 extra link.
	o, r := corner, gpmAt(topo, 0, 6)
	if got := topo.ExtraPushLinks(o, r, 1); got != 1 {
		t.Fatalf("extra links = %d, want 1", got)
	}
	targets := topo.PushTargets(o, []int{r}, 1)
	if len(targets) != 2 {
		t.Fatalf("targets = %v", targets)
	}
	// The owner itself is never a target.
	for _, x := range topo.PushTargets(gpmAt(topo, 0, 1), []int{corner}, 1) {
		if x == gpmAt(topo, 0, 1) {
			t.Fatal("owner is a push target")
		}
	}
}

func TestGeoBoundCreditsNeighborsOnce(t *testing.T) {
	topo := geoTestTopology()
	g := EnableGeoBound(topo)
	defer DisableGeoBound()
	o := gpmAt(topo, 0, 0)
	r := gpmAt(topo, 5, 5)
	n1 := gpmAt(topo, 5, 6) // 1 hop from r
	n2 := gpmAt(topo, 6, 6) // 2 hops from r
	far := gpmAt(topo, 1, 1)

	g.Deliver(r, o, 1, 0x1000, 900, 1000)
	g.Deliver(n1, o, 1, 0x1000, 950, 1100)  // later delivery of the same line
	g.NetRead(n1, o, 1, 0x1000, 1050, 1060) // before its own delivery: credited to r's
	g.NetRead(n2, o, 1, 0x1000, 1500, 1500) // 2 hops from r, 1 from n1
	g.NetRead(far, o, 1, 0x1000, 1500, 1500)
	g.NetRead(n1, o, 1, 0x1000, 5000, 5000) // 1 us window missed, 10 us hit
	g.NetRead(r, o, 1, 0x2000, 1200, 1200)  // other line
	s := g.Finish()
	if s.Deliveries != 2 || s.NetReads != 5 {
		t.Fatalf("deliveries %d net reads %d", s.Deliveries, s.NetReads)
	}
	// radius 1, 1 us: n1 @1050 (from r), n2 @1500 (from n1's delivery).
	if s.Covered[1][0] != 2 {
		t.Fatalf("r1 1us covered = %d, want 2", s.Covered[1][0])
	}
	// radius 1, 10 us: plus n1 @5000 (earliest qualifying = r's delivery).
	if s.Covered[1][1] != 3 {
		t.Fatalf("r1 10us covered = %d, want 3", s.Covered[1][1])
	}
	// n1 credited twice to r's delivery in 10 us but counted once as a copy.
	if s.UsedCopies[1][1] != 2 {
		t.Fatalf("r1 10us used copies = %d, want 2", s.UsedCopies[1][1])
	}
	// radius 2, 1 us: n2 is within 2 hops of r -> credited to r's delivery.
	if s.Covered[2][0] != 2 {
		t.Fatalf("r2 1us covered = %d, want 2", s.Covered[2][0])
	}
	want := float64(topo.Hops(n1, o)+topo.Hops(n2, o)) * geoBoundBytesPerHop
	if s.SavedBytesHop[1][0] != want {
		t.Fatalf("saved bytes hop = %v, want %v", s.SavedBytesHop[1][0], want)
	}
}

func TestGeoBoundBoundaryAndWrite(t *testing.T) {
	topo := geoTestTopology()
	g := EnableGeoBound(topo)
	defer DisableGeoBound()
	o := gpmAt(topo, 0, 0)
	r := gpmAt(topo, 5, 5)
	n := gpmAt(topo, 5, 6)
	g.Deliver(r, o, 1, 0x40, 0, 1000)
	g.KernelBoundary(n, 1200)
	g.NetRead(n, o, 1, 0x40, 1300, 1300)
	g.Deliver(r, o, 1, 0x80, 0, 1000)
	g.Write(1, 0x80)
	g.NetRead(n, o, 1, 0x80, 1300, 1300)
	// Read before the delivery arrived but after the owner sent: late.
	g.Deliver(r, o, 1, 0xc0, 2000, 2400)
	g.NetRead(n, o, 1, 0xc0, 2100, 2500)
	s := g.Finish()
	if s.Covered[1][1] != 0 {
		t.Fatalf("covered = %d, want 0", s.Covered[1][1])
	}
	if s.BoundaryRejects == 0 || s.WriteKills != 1 {
		t.Fatalf("boundary rejects %d write kills %d", s.BoundaryRejects, s.WriteKills)
	}
	if s.Late[1][0] != 1 {
		t.Fatalf("late = %d, want 1", s.Late[1][0])
	}
	if s.ZeroUsePushes[1][0] != 3 {
		t.Fatalf("zero-use pushes = %d, want 3", s.ZeroUsePushes[1][0])
	}
}

func TestGeoMulticastSplit(t *testing.T) {
	src := &geoTestPort{name: "owner"}
	r1 := &geoTestPort{name: "r1"}
	r2 := &geoTestPort{name: "r2"}
	n1 := &geoTestPort{name: "n1"}
	n2 := &geoTestPort{name: "n2"}
	inner := &T18MulticastRsp{
		MsgMeta: sim.MsgMeta{ID: "mc", Src: src, Dst: r1, TrafficBytes: 74},
		Data:    make([]byte, 64),
		Dsts:    []sim.Port{r1, r2},
		RspTos:  []string{"a", "b"},
	}
	w := &GeoMulticast{
		MsgMeta:   sim.MsgMeta{ID: "w", Src: src, Dst: r1, TrafficBytes: 74},
		Inner:     inner,
		InnerDsts: []sim.Port{r1, r2},
		PushDsts:  []sim.Port{n1, n2},
		Push:      &GeoPushMsg{PID: 1, Owner: src, Lines: []geoPushLine{{addr: 0x40}}},
	}
	if got := len(w.MulticastDsts()); got != 4 {
		t.Fatalf("dsts = %d", got)
	}
	// Both requesters only: the inner multicast itself.
	if m := w.SplitForDsts([]sim.Port{r1, r2}); m != sim.Msg(inner) {
		t.Fatalf("inner branch = %T", m)
	}
	// One requester: the ordinary unicast leaf.
	if m, ok := w.SplitForDsts([]sim.Port{r2}).(interface{ GetRspTo() string }); !ok || m.GetRspTo() != "b" {
		t.Fatalf("r2 leaf wrong")
	}
	// One neighbor: a push leaf addressed to it.
	leaf, ok := w.SplitForDsts([]sim.Port{n2}).(*GeoPushMsg)
	if !ok || leaf.Dst != n2 || len(leaf.Lines) != 1 || leaf.ID == w.ID {
		t.Fatalf("push leaf wrong: %#v", leaf)
	}
	// Mixed branch: a smaller GeoMulticast.
	mixed, ok := w.SplitForDsts([]sim.Port{r1, n1, n2}).(*GeoMulticast)
	if !ok || len(mixed.InnerDsts) != 1 || len(mixed.PushDsts) != 2 ||
		mixed.Dst != r1 || mixed.ID == w.ID {
		t.Fatalf("mixed branch wrong: %#v", mixed)
	}
	pushOnly, ok := w.SplitForDsts([]sim.Port{n1, n2}).(*GeoMulticast)
	if !ok || pushOnly.Inner != nil || pushOnly.Dst != n1 {
		t.Fatalf("push-only branch wrong")
	}
}

func TestGeoShareTable(t *testing.T) {
	tab := newGeoShareTable(8) // 2 sets x 4 ways
	k := geoLineKey{1, 0x1000}
	tab.record(k, 3)
	tab.record(k, 3)
	if tab.shared(k) {
		t.Fatal("one reader is not shared")
	}
	tab.record(k, 4)
	if !tab.shared(k) {
		t.Fatal("two readers are shared")
	}
	// Fill the table with other lines: k is eventually evicted (LRU).
	for i := uint64(1); i <= 64; i++ {
		tab.record(geoLineKey{1, 0x1000 + i*64}, 5)
	}
	if tab.shared(k) {
		t.Fatal("evicted line still shared")
	}
}

func TestCoReadPairs(t *testing.T) {
	topo := geoTestTopology()
	c := EnableCoRead(topo)
	defer DisableCoRead()
	c.NetRead(1, 1, 0x40, 100, 100)
	c.NetRead(2, 1, 0x40, 600, 600)   // within 1 us of GPM 1
	c.NetRead(3, 1, 0x40, 5000, 5000) // within 10 us of both
	c.NetRead(2, 1, 0x40, 700, 700)   // GPM 2 again: pairs with 1 again
	c.KernelBoundary(4, 6000)
	c.NetRead(4, 1, 0x40, 6500, 6500) // after its boundary: no pairs
	c.Write(1, 0x80)
	if c.Pairs[0][1][2] != 2 || c.Pairs[0][2][1] != 2 {
		t.Fatalf("1us pairs (1,2) = %d", c.Pairs[0][1][2])
	}
	if c.Pairs[0][3][1] != 0 || c.Pairs[1][3][1] != 1 || c.Pairs[1][3][2] != 2 {
		t.Fatalf("10us pairs wrong: %d %d", c.Pairs[1][3][1], c.Pairs[1][3][2])
	}
	if c.Pairs[1][4][1] != 0 || c.Reads[4] != 1 {
		t.Fatal("boundary not respected")
	}
}
