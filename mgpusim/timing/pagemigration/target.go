package pagemigration

// Migration target selection (ISCA27 P20, -pm-target). The access counter
// of a unit is its reader distribution; argmax (the default) migrates to the
// GPM with the largest count, as before. median and class choose the target
// from the whole distribution on the 2D mesh (Manhattan distance):
//
//   median: the GPM tile that minimises sum_d count[d] * hops(d, tile) (the
//           weighted Manhattan median; x and y separable). The centre tile
//           is not a GPM, so it is never chosen; among equally good tiles the
//           one whose owner served the fewest remote reads recently wins.
//   class:  one GPM holds >= DomShare of the count -> that GPM;
//           more than MedianMaxReaders readers ("broadcast") -> BroadcastK
//           tiles closest to the median, page by page in turn (0 = do not
//           migrate); otherwise -> median.
//
// median and class do not migrate when the target is the current owner or
// the weighted hops would drop by less than MinSaving (fraction).

// Target is the target-selection rule.
type Target int

// Targets.
const (
	TargetArgmax Target = iota
	TargetMedian
	TargetClass
)

// ParseTarget converts a flag value.
func ParseTarget(s string) (Target, bool) {
	switch s {
	case "", "argmax":
		return TargetArgmax, true
	case "median":
		return TargetMedian, true
	case "class":
		return TargetClass, true
	}
	return TargetArgmax, false
}

func (t Target) String() string {
	switch t {
	case TargetMedian:
		return "median"
	case TargetClass:
		return "class"
	}
	return "argmax"
}

// meshWidth is the mesh width in tiles (tile = x + meshWidth * y).
var meshWidth = 7

func tileHops(a, b int) int {
	dx := a%meshWidth - b%meshWidth
	dy := a/meshWidth - b/meshWidth
	if dx < 0 {
		dx = -dx
	}
	if dy < 0 {
		dy = -dy
	}
	return dx + dy
}

// noteServed records a remote read served by owner (for the load tie-break).
func (m *Manager) noteServed(now float64, owner uint64) {
	if m.load == nil {
		return
	}
	m.decayLoad(now)
	if owner < uint64(len(m.load)) {
		m.load[owner]++
	}
}

func (m *Manager) decayLoad(now float64) {
	w := float64(m.cfg.LoadWindowNS) * 1e-9
	if w <= 0 {
		return
	}
	for now-m.loadT >= w {
		for i := range m.load {
			m.load[i] /= 2
		}
		m.loadT += w
	}
}

// candidate is a GPM tile with its weighted hop cost.
type candidate struct {
	dev  uint64
	cost uint64
	load float64
}

// rankTargets returns every GPM ordered by weighted hops, then load, then
// device ID.
func (m *Manager) rankTargets(counts []uint32) []candidate {
	out := make([]candidate, 0, len(m.gpms))
	for d, g := range m.gpms {
		if g == nil {
			continue
		}
		var cost uint64
		for r, c := range counts {
			if c == 0 || m.gpms[r] == nil {
				continue
			}
			cost += uint64(c) * uint64(tileHops(m.gpms[r].Tile, g.Tile))
		}
		var l float64
		if m.load != nil {
			l = m.load[d]
		}
		out = append(out, candidate{dev: uint64(d), cost: cost, load: l})
	}
	// insertion sort: 48 entries
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && less(out[j], out[j-1]); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

func less(a, b candidate) bool {
	if a.cost != b.cost {
		return a.cost < b.cost
	}
	if a.load != b.load {
		return a.load < b.load
	}
	return a.dev < b.dev
}

// TargetDecision is the outcome of chooseTarget (also used by the tests).
type TargetDecision int

// Decisions.
const (
	DecideMigrate TargetDecision = iota
	DecideSamePlace
	DecideSmallSaving
	DecideBroadcastStay
)

// chooseTarget picks the destination of a unit whose counter fired. owner is
// the unit's current GPM and page the unit's index (for the k-way spread).
func (m *Manager) chooseTarget(counts []uint32, owner uint64, page uint64,
) (uint64, TargetDecision, string) {
	var total uint64
	best := 0
	for d, c := range counts {
		total += uint64(c)
		if c > counts[best] {
			best = d
		}
	}
	if total == 0 {
		return owner, DecideSamePlace, "none"
	}
	ranked := m.rankTargets(counts)
	dst := ranked[0].dev
	kind := "median"
	if m.cfg.Target == TargetClass {
		readers := 0
		for _, c := range counts {
			if c > 0 && float64(c) >= m.cfg.ReaderNoise*float64(total) {
				readers++
			}
		}
		switch {
		case float64(counts[best]) >= m.cfg.DomShare*float64(total):
			dst, kind = uint64(best), "dominant"
		case readers > m.cfg.MedianMaxReaders:
			if m.cfg.BroadcastK <= 0 {
				return owner, DecideBroadcastStay, "broadcast"
			}
			k := m.cfg.BroadcastK
			if k > len(ranked) {
				k = len(ranked)
			}
			dst, kind = ranked[int(page%uint64(k))].dev, "broadcast"
		}
	}
	if dst == owner {
		return owner, DecideSamePlace, kind
	}
	var costOwner, costDst uint64
	for _, c := range ranked {
		if c.dev == owner {
			costOwner = c.cost
		}
		if c.dev == dst {
			costDst = c.cost
		}
	}
	if costOwner == 0 || float64(costOwner-min64(costOwner, costDst)) <
		m.cfg.MinSaving*float64(costOwner) {
		return owner, DecideSmallSaving, kind
	}
	return dst, DecideMigrate, kind
}

func min64(a, b uint64) uint64 {
	if a < b {
		return a
	}
	return b
}

// LocalAccess (CountLocal) counts an L1V miss of GPM dev sent to its own L2
// slices: the owner's own use of the unit, which argmax ignores. It never
// triggers a migration by itself.
func (m *Manager) LocalAccess(dev uint64, pAddr uint64) {
	if m.cfg.Policy != PolicyAccessCounter || !m.cfg.CountLocal {
		return
	}
	info := m.ppns[pAddr>>m.log2PageSize]
	if info == nil || info.state != frameLive {
		return
	}
	if m.cfg.AllowPages != nil {
		return
	}
	u := m.getUnit(m.unitOf(info.pid, info.vAddr))
	if u.phase != phaseIdle {
		return
	}
	m.Stats.LocalCounted++
	if m.cfg.Adapt.Enabled {
		m.adaptCount(0, u, dev, pAddr, false)
		return
	}
	u.counts[dev]++
}

// ReplicaHit (CountNetwork) takes back the count of a remote access that was
// served by a retained (M3) replica in the requester's own L2, so the counter
// sees only accesses that went to the network.
func (m *Manager) ReplicaHit(dev uint64, pAddr uint64) {
	if m.cfg.Policy != PolicyAccessCounter || !m.cfg.CountNetwork {
		return
	}
	info := m.ppns[pAddr>>m.log2PageSize]
	if info == nil {
		return
	}
	u := m.units[m.unitOf(info.pid, info.vAddr)]
	if u != nil && u.a != nil && m.cfg.Adapt.r3() {
		u.a.m3Hits++ // reuse of lines the readers keep (r3 re-fetch cost)
		if u.a.cmigHolders&(1<<dev) != 0 {
			m.AStats.CmigHolderHits++
		}
	}
	if u == nil || u.phase != phaseIdle || u.counts[dev] == 0 {
		return
	}
	if u.a != nil {
		m.adaptUncount(u, dev, pAddr)
		m.Stats.ReplicaUncounted++
		return
	}
	u.counts[dev]--
	m.Stats.ReplicaUncounted++
}
