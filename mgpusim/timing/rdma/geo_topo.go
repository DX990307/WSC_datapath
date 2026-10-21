package rdma

import (
	"sort"

	"github.com/sarchlab/akita/v3/sim"
)

// ISCA'27 geo-push ("geographic broadcast response") topology helpers.
//
// The mesh is W x H tiles; every tile except the center one holds a GPM whose
// RDMA engine is reachable through its ToOutside port. Routes are
// dimension-ordered, Y first, then X (akita mesh routing table), so a
// multicast tree is the union of the Y-then-X unicast routes from the source.
// GPM indices follow the platform's creation order (row by row, skipping the
// center tile), the same order as the T18 topology.

// GeoTopology is shared by every RDMA engine of the platform.
type GeoTopology struct {
	Width, Height int
	Coords        [][2]int   // GPM index -> (x, y)
	Ports         []sim.Port // GPM index -> RDMA ToOutside port
	index         map[sim.Port]int
	nbrs          [3][][]int // radius 1, 2 -> GPM -> neighbors (excl. itself)
	treeCache     map[geoTreeKey]int
}

type geoTreeKey struct {
	owner, req, radius int
}

// NewGeoTopology builds the topology; coords and ports are indexed by GPM.
func NewGeoTopology(width, height int, coords [][2]int, ports []sim.Port) *GeoTopology {
	if len(coords) != len(ports) {
		panic("geo topology: coordinates and ports differ in length")
	}
	t := &GeoTopology{
		Width:     width,
		Height:    height,
		Coords:    append([][2]int(nil), coords...),
		Ports:     append([]sim.Port(nil), ports...),
		index:     make(map[sim.Port]int, len(ports)),
		treeCache: make(map[geoTreeKey]int),
	}
	for i, p := range ports {
		t.index[p] = i
	}
	for r := 1; r <= 2; r++ {
		t.nbrs[r] = make([][]int, len(coords))
		for i := range coords {
			var list []int
			for j := range coords {
				if j == i {
					continue
				}
				if h := t18Hops(coords[i], coords[j]); h <= r {
					list = append(list, j)
				}
			}
			sort.Slice(list, func(a, b int) bool {
				ha := t18Hops(coords[i], coords[list[a]])
				hb := t18Hops(coords[i], coords[list[b]])
				if ha != hb {
					return ha < hb
				}
				return list[a] < list[b]
			})
			t.nbrs[r][i] = list
		}
	}
	return t
}

// Index returns the GPM index of an RDMA ToOutside port, or -1.
func (t *GeoTopology) Index(p sim.Port) int {
	if t == nil || p == nil {
		return -1
	}
	if i, ok := t.index[p]; ok {
		return i
	}
	return -1
}

// NumGPMs returns the number of GPMs.
func (t *GeoTopology) NumGPMs() int { return len(t.Coords) }

// Hops returns the Manhattan distance between two GPMs.
func (t *GeoTopology) Hops(a, b int) int {
	return t18Hops(t.Coords[a], t.Coords[b])
}

// Neighbors returns the GPMs within radius (1 or 2) hops of g, g excluded,
// nearest first.
func (t *GeoTopology) Neighbors(g, radius int) []int {
	if radius <= 0 {
		return nil
	}
	if radius > 2 {
		radius = 2
	}
	return t.nbrs[radius][g]
}

// PushTargets returns the neighbors of the requesters reqs within radius,
// excluding the owner and the requesters themselves, without duplicates.
func (t *GeoTopology) PushTargets(owner int, reqs []int, radius int) []int {
	if radius <= 0 {
		return nil
	}
	var out []int
	seen := make(map[int]bool, 16)
	seen[owner] = true
	for _, r := range reqs {
		seen[r] = true
	}
	for _, r := range reqs {
		for _, n := range t.Neighbors(r, radius) {
			if seen[n] {
				continue
			}
			seen[n] = true
			out = append(out, n)
		}
	}
	return out
}

func (t *GeoTopology) tile(c [2]int) int { return c[0] + c[1]*t.Width }

// routeLinks appends the directed links (from tile, to tile) of the
// Y-then-X route a -> b.
func (t *GeoTopology) routeLinks(a, b [2]int, out map[[2]int]bool) int {
	n := 0
	x, y := a[0], a[1]
	for y != b[1] {
		ny := y + 1
		if b[1] < y {
			ny = y - 1
		}
		out[[2]int{t.tile([2]int{x, y}), t.tile([2]int{x, ny})}] = true
		y = ny
		n++
	}
	for x != b[0] {
		nx := x + 1
		if b[0] < x {
			nx = x - 1
		}
		out[[2]int{t.tile([2]int{x, y}), t.tile([2]int{nx, y})}] = true
		x = nx
		n++
	}
	return n
}

// TreeLinks returns the number of directed mesh links of the multicast tree
// from owner to all dsts (union of the Y-then-X routes).
func (t *GeoTopology) TreeLinks(owner int, dsts []int) int {
	links := make(map[[2]int]bool, 32)
	for _, d := range dsts {
		t.routeLinks(t.Coords[owner], t.Coords[d], links)
	}
	return len(links)
}

// ExtraPushLinks returns the links the push to R's neighbors (radius) adds to
// the owner -> R route: |tree(owner, {R} + targets)| - hops(owner, R).
func (t *GeoTopology) ExtraPushLinks(owner, req, radius int) int {
	key := geoTreeKey{owner, req, radius}
	if v, ok := t.treeCache[key]; ok {
		return v
	}
	targets := t.PushTargets(owner, []int{req}, radius)
	v := 0
	if len(targets) > 0 {
		v = t.TreeLinks(owner, append([]int{req}, targets...)) - t.Hops(owner, req)
	}
	t.treeCache[key] = v
	return v
}
