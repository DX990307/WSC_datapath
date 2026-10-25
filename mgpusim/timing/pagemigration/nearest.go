package pagemigration

import (
	"math/bits"
	"sort"

	"github.com/sarchlab/akita/v3/sim"
)

// R4 direction 3, offline bound (read-only statistics): for every remote
// line fetched over the network, how far is the nearest GPM that could have
// supplied it instead of the home?
//   - ideal: a GPM that fetched the same line earlier in the same kernel
//     with no write to the line since (no L2 capacity limit);
//   - real: a GPM whose L2 holds an M3 replica of the line when the fetch
//     arrives (real capacity, replacement and invalidation).
// Nothing here changes timing.

type nearLine struct {
	holders [2]uint64
}

// NearestStats are the nearest-copy statistics.
type NearestStats struct {
	Fetches                          uint64
	HopsHome, HopsIdeal, HopsReal    float64
	RTTHome, RTTIdeal, RTTReal       float64
	CloserIdeal, CloserReal          uint64
	Cat                              [4]uint64 // by earlier fetchers in the kernel: 0, 1, 2-7, 8+
	CatHome, CatIdeal, CatReal       [4]float64
	OwnerHome, OwnerIdeal, OwnerReal []uint64 // requests served by each GPM
}

type nearestState struct {
	kern  int
	lines map[uint64]*nearLine
	s     NearestStats
}

// EnableNearestStats switches the statistics on.
func (m *Manager) EnableNearestStats() {
	n := len(m.gpms)
	m.near = &nearestState{kern: -1, lines: make(map[uint64]*nearLine)}
	m.near.s.OwnerHome = make([]uint64, n)
	m.near.s.OwnerIdeal = make([]uint64, n)
	m.near.s.OwnerReal = make([]uint64, n)
}

// NearestStats returns the statistics (nil when off).
func (m *Manager) NearestStats() *NearestStats {
	if m.near == nil {
		return nil
	}
	return &m.near.s
}

func nearCat(k int) int {
	switch {
	case k == 0:
		return 0
	case k == 1:
		return 1
	case k < 8:
		return 2
	}
	return 3
}

// NearestNote records a network fetch of line by GPM dev. holds(g) tells
// whether GPM g's L2 holds an M3 replica of the line.
func (m *Manager) NearestNote(dev, line uint64, holds func(g uint64) bool) {
	st := m.near
	if st == nil {
		return
	}
	if st.kern != m.kernel {
		st.kern = m.kernel
		st.lines = make(map[uint64]*nearLine)
	}
	owner := line >> 33
	if int(owner) >= len(m.gpms) || int(dev) >= len(m.gpms) || owner == dev {
		return
	}
	s := &st.s
	s.Fetches++
	hHome := m.r3Hops(dev, owner)
	l := st.lines[line]
	if l == nil {
		l = &nearLine{}
		st.lines[line] = l
	}
	prior := bits.OnesCount64(l.holders[0]) + bits.OnesCount64(l.holders[1])
	bestI, atI := hHome, owner
	for w := 0; w < 2; w++ {
		for x := l.holders[w]; x != 0; x &= x - 1 {
			g := uint64(w*64 + bits.TrailingZeros64(x))
			if g == dev || g == owner {
				continue
			}
			if h := m.r3Hops(dev, g); h < bestI {
				bestI, atI = h, g
			}
		}
	}
	bestR, atR := hHome, owner
	for g := range m.gpms {
		gg := uint64(g)
		if m.gpms[g] == nil || gg == dev || gg == owner {
			continue
		}
		h := m.r3Hops(dev, gg)
		if h < bestR && holds(gg) {
			bestR, atR = h, gg
		}
	}
	l.holders[dev>>6] |= 1 << (dev & 63)
	rtt := func(h float64) float64 {
		c := &m.cfg.Adapt
		return c.R3BaseNS + c.R3HopNS*h
	}
	s.HopsHome += hHome
	s.HopsIdeal += bestI
	s.HopsReal += bestR
	s.RTTHome += rtt(hHome)
	s.RTTIdeal += rtt(bestI)
	s.RTTReal += rtt(bestR)
	if atI != owner {
		s.CloserIdeal++
	}
	if atR != owner {
		s.CloserReal++
	}
	k := nearCat(prior)
	s.Cat[k]++
	s.CatHome[k] += hHome
	s.CatIdeal[k] += bestI
	s.CatReal[k] += bestR
	s.OwnerHome[owner]++
	s.OwnerIdeal[atI]++
	s.OwnerReal[atR]++
}

// NearestWrite drops the ideal holders of written lines.
func (m *Manager) NearestWrite(now sim.VTimeInSec, addr, size uint64) {
	st := m.near
	if st == nil {
		return
	}
	if size == 0 {
		size = 1
	}
	for a := addr &^ 63; a < addr+size; a += 64 {
		delete(st.lines, a)
	}
}

// NearestCorners returns the four GPMs with the largest mean distance to
// the others (the corner owners).
func (m *Manager) NearestCorners() []uint64 {
	type gd struct {
		g uint64
		d float64
	}
	var all []gd
	for i, gi := range m.gpms {
		if gi == nil {
			continue
		}
		sum, n := 0.0, 0
		for j, gj := range m.gpms {
			if gj != nil && i != j {
				sum += float64(m.hops(uint64(i), uint64(j)))
				n++
			}
		}
		all = append(all, gd{uint64(i), sum / float64(n)})
	}
	sort.Slice(all, func(a, b int) bool {
		if all[a].d != all[b].d {
			return all[a].d > all[b].d
		}
		return all[a].g < all[b].g
	})
	var out []uint64
	for i := 0; i < 4 && i < len(all); i++ {
		out = append(out, all[i].g)
	}
	return out
}
