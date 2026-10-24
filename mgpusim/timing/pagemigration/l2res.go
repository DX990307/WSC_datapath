package pagemigration

import (
	"math/bits"

	"github.com/sarchlab/akita/v3/mem/vm"
	"github.com/sarchlab/akita/v3/sim"
)

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
	HolderMsgs                                  uint64
	Pulls, PullLines, PullInstalled, PullAborts uint64
	SlotReplacements                            uint64
	Prefetches, PrefetchSkips                   uint64
}

// ---------------------------------------------------------------------------
// R4 step d: footprint pulls into a reader's L2

// l2rSlot is one reader's line footprint of a tracked page: the lines it
// fetched (counted remote accesses, M3 hits included) in kernel kern and in
// the kernel before.
type l2rSlot struct {
	dev       uint64
	cur, prev uint64
	kern      int
	tried     int // kernel of the last pull attempt (-1 = none)
	n         uint32
}

type l2rPage struct {
	slots []l2rSlot
}

type l2rOpt struct {
	g      uint64
	lines  uint64
	regret float64
	cost   float64
}

// l2rNote records a counted remote access of dev to pAddr.
func (m *Manager) l2rNote(a *adaptState, dev, pAddr uint64) {
	k := m.cfg.Adapt.L2PullSlots
	if k <= 0 {
		k = 4
	}
	if a.l2r == nil {
		a.l2r = &l2rPage{}
	}
	p := a.l2r
	line := uint64(1) << ((pAddr >> 6) & 63)
	for i := range p.slots {
		s := &p.slots[i]
		if s.dev != dev {
			continue
		}
		if s.kern != m.kernel {
			if s.kern == m.kernel-1 {
				s.prev = s.cur
			} else {
				s.prev = 0
			}
			s.cur, s.kern = 0, m.kernel
		}
		s.cur |= line
		s.n++
		return
	}
	ns := l2rSlot{dev: dev, cur: line, kern: m.kernel, tried: -1, n: 1}
	if len(p.slots) < k {
		p.slots = append(p.slots, ns)
		return
	}
	// replace the slot with the fewest accesses
	j := 0
	for i := range p.slots {
		if p.slots[i].n < p.slots[j].n {
			j = i
		}
	}
	p.slots[j] = ns
	m.L2Res.SlotReplacements++
}

// l2rOption is the best pull for the page: for reader g, the rent paid so
// far (its counted network fetches x RTT x the measured probability that a
// pulled line is used) against the copy of the footprint lines it does not
// hold plus the expected re-fetch cost of the lines they displace.
func (m *Manager) l2rOption(a *adaptState, tot []float64, home, base uint64) (l2rOpt, bool) {
	var best l2rOpt
	found := false
	for i := range a.l2r.slots {
		s := &a.l2r.slots[i]
		g := s.dev
		if g == home || s.tried == m.kernel || int(g) >= len(tot) || tot[g] <= 0 {
			continue
		}
		gp := m.gpms[g]
		if gp == nil || gp.PullInstall == nil {
			continue
		}
		fp := s.prev
		if s.kern == m.kernel {
			fp |= s.cur
		} else if s.kern == m.kernel-1 {
			fp = s.cur
		} else {
			fp = 0
		}
		var lines uint64
		for x := fp; x != 0; x &= x - 1 {
			b := uint64(bits.TrailingZeros64(x))
			if !gp.HoldsLine(base + b*64) {
				lines |= 1 << b
			}
		}
		n := bits.OnesCount64(lines)
		if n == 0 {
			continue
		}
		inst, used := gp.PullUse()
		ph := float64(used+1) / float64(inst+2)
		r := tot[g] * m.r3RTT(g, home) * ph
		c := m.r3Copy(home, g, float64(n*64)) + float64(n)*gp.VictimScore()
		if !found || r-c > best.regret-best.cost {
			best = l2rOpt{g: g, lines: lines, regret: r, cost: c}
			found = true
		}
	}
	return best, found
}

// l2rDecide starts the pull after the hardware decision delay.
func (m *Manager) l2rDecide(now sim.VTimeInSec, a *adaptState, p vm.Page, home uint64, o l2rOpt) {
	for i := range a.l2r.slots {
		if a.l2r.slots[i].dev == o.g {
			a.l2r.slots[i].tried = m.kernel
		}
	}
	if o.regret > 0 && a.buf != nil {
		a.buf.l2rRent += o.regret
		a.buf.l2rPulls++
	}
	d := m.cfg.Adapt.L2DecideNS
	if d == 0 {
		m.l2rStartPull(now, a, p, home, o)
		return
	}
	m.later(now+ns(d), func(t sim.VTimeInSec) { m.l2rStartPull(t, a, p, home, o) })
}

// l2rStartPull pulls the chosen lines from the home into g's L2 (copy
// engine; one attempt per reader, page and kernel).
func (m *Manager) l2rStartPull(now sim.VTimeInSec, a *adaptState, p vm.Page, home uint64, o l2rOpt) {
	g := o.g
	ps := uint64(1) << m.log2PageSize
	seq, kern := a.writeSeq, m.kernel
	n := bits.OnesCount64(o.lines)
	m.L2Res.Pulls++
	m.L2Res.PullLines += uint64(n)
	m.AStats.CopyBytes += uint64(n * 64)
	m.gpms[g].Engine.enqueue(now, &chunk{
		src:      m.gpms[home].Engine,
		srcPAddr: p.PAddr,
		dstPAddr: p.PAddr,
		size:     ps,
		mask:     o.lines,
		install: func(t sim.VTimeInSec, data []byte) {
			if a.writeSeq != seq || m.kernel != kern {
				m.L2Res.PullAborts++
				return
			}
			for x := o.lines; x != 0; x &= x - 1 {
				off := uint64(bits.TrailingZeros64(x)) * 64
				if m.gpms[g].PullInstall(t, p.PID, p.PAddr+off,
					append([]byte(nil), data[off:off+64]...), now) {
					m.L2Res.PullInstalled++
				}
			}
		},
	})
}

// ---------------------------------------------------------------------------
// R4 step f: rule-predicted pulls

// l2rFootprint is g's footprint of the page (this kernel and the previous).
func (m *Manager) l2rFootprint(a *adaptState, g uint64) uint64 {
	if a.l2r == nil {
		return 0
	}
	for i := range a.l2r.slots {
		s := &a.l2r.slots[i]
		if s.dev != g {
			continue
		}
		switch {
		case s.kern == m.kernel:
			return s.cur | s.prev
		case s.kern == m.kernel-1:
			return s.cur
		}
	}
	return 0
}

func (m *Manager) l2rSlot(a *adaptState, g uint64) *l2rSlot {
	if a.l2r == nil {
		return nil
	}
	for i := range a.l2r.slots {
		if a.l2r.slots[i].dev == g {
			return &a.l2r.slots[i]
		}
	}
	return nil
}

// l2rPrefetch pulls the expected footprint of page idx of buffer b into the
// L2 of its predicted reader d, unless d already fetched it in this kernel.
// Test: p E ph > copy + displacement, p = the rule's out-of-sample hit rate
// (Laplace), E = mean rent of the buffer's decided pulls, ph = measured use
// of pulled lines at d.
func (m *Manager) l2rPrefetch(now sim.VTimeInSec, b *buffer, idx int, v *unit,
	a *adaptState, p vm.Page, home, d uint64) {
	if d == home || b.l2rPulls == 0 || v.phase != phaseIdle {
		return
	}
	if a.writeKernel == m.kernel {
		return
	}
	if s := m.l2rSlot(a, d); s != nil && (s.tried == m.kernel ||
		(s.kern == m.kernel && s.cur != 0)) {
		return
	}
	gp := m.gpms[d]
	if gp == nil || gp.PullInstall == nil {
		return
	}
	fp := m.l2rFootprint(a, d)
	if fp == 0 {
		fp = b.l2rFP
	}
	var lines uint64
	for x := fp; x != 0; x &= x - 1 {
		bit := uint64(bits.TrailingZeros64(x))
		if !gp.HoldsLine(p.PAddr + bit*64) {
			lines |= 1 << bit
		}
	}
	n := bits.OnesCount64(lines)
	if n == 0 {
		return
	}
	tests, hits := 0, 0
	for i := range b.evTests {
		tests += b.evTests[i]
		hits += b.evHits[i]
	}
	pr := float64(hits+1) / float64(tests+2)
	inst, used := gp.PullUse()
	ph := float64(used+1) / float64(inst+2)
	e := b.l2rRent / float64(b.l2rPulls)
	c := m.r3Copy(home, d, float64(n*64)) + float64(n)*gp.VictimScore()
	if pr*e*ph <= c {
		m.L2Res.PrefetchSkips++
		return
	}
	if a.l2r == nil {
		a.l2r = &l2rPage{}
	}
	if s := m.l2rSlot(a, d); s == nil {
		k := m.cfg.Adapt.L2PullSlots
		if k <= 0 {
			k = 4
		}
		if len(a.l2r.slots) < k {
			a.l2r.slots = append(a.l2r.slots, l2rSlot{dev: d, kern: m.kernel, tried: -1})
		}
	}
	m.L2Res.Prefetches++
	m.l2rDecide(now, a, p, home, l2rOpt{g: d, lines: lines})
}
