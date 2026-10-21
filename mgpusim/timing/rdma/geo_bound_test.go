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
