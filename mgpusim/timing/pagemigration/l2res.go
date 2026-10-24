package pagemigration

import "github.com/sarchlab/akita/v3/sim"

// ISCA27 R4 (L2 residency instead of page migration): the manager supplies
// the latency model of the L2 cost-aware replacement and carries the holder
// table's invalidation messages over the copy engines' network ports.

// L2ResRTT is the zero-load round trip (ns) of GPM g to data homed at h
// (0 when local); blind = every remote home at the mesh-average distance.
func (m *Manager) L2ResRTT(g, h uint64, blind bool) float64 {
	if g == h {
		return 0
	}
	if blind {
		c := &m.cfg.Adapt
		return c.R3BaseNS + c.R3HopNS*m.avgHops()
	}
	return m.r3RTT(g, h)
}

func (m *Manager) avgHops() float64 {
	if m.meanHops > 0 {
		return m.meanHops
	}
	sum, n := 0, 0
	for i, gi := range m.gpms {
		for j, gj := range m.gpms {
			if gi != nil && gj != nil && i != j {
				sum += tileHops(gi.Tile, gj.Tile)
				n++
			}
		}
	}
	m.meanHops = float64(sum) / float64(n)
	return m.meanHops
}

// SendHolderMsg sends a holder-table message (invalidation) from the owner
// GPM's copy engine to the holder's; apply runs on arrival. Without copy
// engines it applies at once.
func (m *Manager) SendHolderMsg(now sim.VTimeInSec, owner uint64, holder int, apply func()) {
	if int(owner) >= len(m.gpms) || holder >= len(m.gpms) ||
		m.gpms[owner] == nil || m.gpms[holder] == nil ||
		m.gpms[owner].Engine == nil || m.gpms[holder].Engine == nil {
		apply()
		return
	}
	m.L2Res.HolderMsgs++
	m.gpms[owner].Engine.sendInvalThen(now, m.gpms[holder].Engine, apply)
}

// L2ResStats counts the manager-side R4 work.
type L2ResStats struct {
	HolderMsgs uint64
}
