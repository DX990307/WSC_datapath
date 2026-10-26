package writeback

import (
	cachepkg "github.com/sarchlab/akita/v3/mem/cache"
	"github.com/sarchlab/akita/v3/sim"
)

// ISCA27 statistics only (-l2-cls-stats): why remote lines cross the
// network, and how full the L2 is. Nothing here changes cache state.
//
// Every remote read line that reaches its owner (one network fetch by GPM g
// of line L, any data path) is classified by what happened to (g, L) before:
//
//	cold        g never fetched L
//	capacity    g's L2 held L (installed as a remote line) and replaced it
//	coherence   g's L2 dropped L at a write epoch (kernel-boundary or
//	            holder invalidation, a write to the line) or g's own write
//	            made L uncacheable
//	notadmitted g fetched L before and its L2 never installed it since
//	            (no remote-line cache: every re-read lands here)
//	resident    g's L2 held L at the time (filter / PID miss; rare)
//
// Lines are physical: after a migration, reads of the new frame are cold.

// ReadClassStats counts network fetches by class.
type ReadClassStats struct {
	Cold, Capacity, Coherence, NotAdmitted, Resident uint64
	Unknown                                          uint64 // requester port not registered
}

const (
	rcNone uint8 = iota
	rcFetched
	rcInstalled
	rcEvicted
	rcInvalid
)

// ReadClassifier keeps the per-(GPM, line) history.
type ReadClassifier struct {
	gpm   map[int]map[uint64]*[64]uint8
	ports map[sim.Port]int
	Stats ReadClassStats
}

// NewReadClassifier creates a classifier.
func NewReadClassifier() *ReadClassifier {
	return &ReadClassifier{gpm: map[int]map[uint64]*[64]uint8{}, ports: map[sim.Port]int{}}
}

// RegisterPort maps a requester RDMA port to its GPM.
func (r *ReadClassifier) RegisterPort(p sim.Port, g int) { r.ports[p] = g }

func (r *ReadClassifier) slot(g int, line uint64, alloc bool) *uint8 {
	m := r.gpm[g]
	if m == nil {
		if !alloc {
			return nil
		}
		m = map[uint64]*[64]uint8{}
		r.gpm[g] = m
	}
	p := line >> 12
	e := m[p]
	if e == nil {
		if !alloc {
			return nil
		}
		e = new([64]uint8)
		m[p] = e
	}
	return &e[(line>>6)&63]
}

// OwnerRead classifies the network fetch of the lines [addr, addr+size)
// requested through port src.
func (r *ReadClassifier) OwnerRead(src sim.Port, addr, size uint64) {
	g, ok := r.ports[src]
	if !ok {
		r.Stats.Unknown++
		return
	}
	if size == 0 {
		size = 1
	}
	for l := addr &^ 63; l < addr+size; l += 64 {
		s := r.slot(g, l, true)
		switch *s {
		case rcNone:
			r.Stats.Cold++
		case rcFetched:
			r.Stats.NotAdmitted++
		case rcInstalled:
			r.Stats.Resident++
		case rcEvicted:
			r.Stats.Capacity++
		case rcInvalid:
			r.Stats.Coherence++
		}
		if *s != rcInstalled {
			*s = rcFetched
		}
	}
}

// Installed: g's L2 installed remote line.
func (r *ReadClassifier) Installed(g int, line uint64) { *r.slot(g, line, true) = rcInstalled }

// Gone: g's L2 dropped remote line (coherence = a write epoch, else replaced).
func (r *ReadClassifier) Gone(g int, line uint64, coherence bool) {
	if s := r.slot(g, line, false); s != nil && *s == rcInstalled {
		if coherence {
			*s = rcInvalid
		} else {
			*s = rcEvicted
		}
	}
}

// Uncacheable: g's own write made line uncacheable for g.
func (r *ReadClassifier) Uncacheable(g int, line uint64) {
	if s := r.slot(g, line, false); s != nil && *s != rcNone {
		*s = rcInvalid
	}
}

// SetReadClassifier attaches the classifier to the slice of GPM g and turns
// on the occupancy statistic.
func (c *Cache) SetReadClassifier(r *ReadClassifier, g int) {
	c.rc = r
	c.rcGPM = g
	c.occOn = true
}

func (c *Cache) rcGone(block *cachepkg.Block, coherence bool) {
	if c.rc != nil && block != nil {
		c.rc.Gone(c.rcGPM, block.Tag, coherence || c.rcCoherence)
	}
}

// Occupancy returns the valid, valid remote, and total blocks of the slice.
func (c *Cache) Occupancy() (valid, remote, total int) {
	d, ok := c.directory.(*cachepkg.DirectoryImpl)
	if !ok {
		return 0, 0, 0
	}
	for _, set := range d.GetSets() {
		for _, b := range set.Blocks {
			total++
			if b.IsValid {
				valid++
				if _, r := c.remoteReplicaBlocks[b]; r {
					remote++
				}
			}
		}
	}
	return
}

// occSample integrates the slice's occupancy over time (piecewise constant
// between samples at least 1 us apart, taken when the slice ticks).
func (c *Cache) occSample(now sim.VTimeInSec, force bool) {
	if !c.occOn {
		return
	}
	dt := float64(now - c.occLast)
	if !force && dt < 1e-6 {
		return
	}
	c.occSumV += float64(c.occV) * dt
	c.occSumR += float64(c.occR) * dt
	c.occSumT += float64(c.occT) * dt
	c.occLast = now
	c.occV, c.occR, c.occT = c.Occupancy()
}

// OccupancyTimeAvg closes the integral at end and returns the time-weighted
// sums of valid, remote, and total blocks (divide valid / total).
func (c *Cache) OccupancyTimeAvg(end sim.VTimeInSec) (v, r, t float64) {
	c.occSample(end, true)
	return c.occSumV, c.occSumR, c.occSumT
}
