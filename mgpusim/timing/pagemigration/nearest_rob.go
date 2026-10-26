package pagemigration

// isca27/nearest.
//
// (1) R3Served: the regret counts the distance of the GPM that actually
// answered a read (a nearest holder found by the RDMA lookup), not the
// home's. Accesses are counted at issue at RTT(g, home); when the answer
// comes over a different path the difference RTT(g, home) - cost(path)
// (cost = R3BaseNS x legs / 2 + R3HopNS x hops / 2, the zero-load model the
// rule uses) is taken back from the accumulator and recorded per reader and
// per block, and every regret (move, block, replicate, cmig) is reduced by
// what the readers already save that way.
//
// (2) Rob: rule-based whole-page replicas (rent-or-buy per reader and page)
// instead of R3's quadrant replicate action. Each counted remote access of
// GPM g to a read-only page adds RTT(g, nearest copy) (the copy the access
// actually went to) to the page's accumulator for g; when it reaches the
// cost f_g of opening a copy at g, the copy is made (driver batch, then a
// page copy from the home). f_g is the R3 replicate cost for one site:
// SW (amortised over the driver batch with BatchSW unless RobFullSW) +
// shootdown + copy(home, g) + P(write) x RTT(home, g) + the lines the
// readers that the copy becomes nearest for keep in their L2s (re-fetched).
// Write gate: no copy when the page's Laplace P(write) over the kernels it
// lived in is above 1/2, or it was written in this kernel. A page needs two
// readers (one reader is a move, left to R3). Copies are dropped by a write
// (as R3 replicas) and, with RobKeep, survive kernel boundaries while they
// still hold the master's bytes (GPM writes collapse them at once; only
// host writes can change the master behind them). The accumulator of g
// restarts when g gets its copy, and all restart when the copies collapse.

import (
	"github.com/sarchlab/akita/v3/mem/vm"
	"github.com/sarchlab/akita/v3/sim"
)

// NearestServed (R3Served) is called when a remote read of line pAddr by
// GPM dev was answered over legs messages and hops hops (one way, summed)
// instead of the direct home round trip.
func (m *Manager) NearestServed(dev, pAddr uint64, legs, hops int) {
	c := &m.cfg.Adapt
	if !c.r3() || !c.R3Served {
		return
	}
	info := m.ppns[pAddr>>m.log2PageSize]
	if info == nil {
		return
	}
	u := m.units[m.unitOf(info.pid, info.vAddr)]
	if u == nil || u.a == nil || u.phase != phaseIdle {
		return
	}
	a := u.a
	cost := c.R3BaseNS*float64(legs)/2 + c.R3HopNS*float64(hops)/2
	d := m.r3RTT(dev, m.homeOf(pAddr)) - cost
	m.AStats.ServedAdj++
	m.AStats.ServedSaveNS += d
	if len(a.replicas) == 0 {
		a.acc -= d
		if a.acc < 0 {
			a.acc = 0
		}
	}
	if a.nearSave == nil {
		a.nearSave = make([]float64, len(m.gpms))
	}
	if int(dev) < len(a.nearSave) {
		a.nearSave[dev] += d
	}
	a.nearSaveB[(pAddr>>log2Block)&3] += d
	if a.rob != nil && int(dev) < len(a.rob) {
		a.rob[dev] -= d
		if a.rob[dev] < 0 {
			a.rob[dev] = 0
		}
	}
}

// served is what reader g already saves by nearest-holder reads (R3Served).
func (a *adaptState) served(g int) float64 {
	if a.nearSave == nil || g >= len(a.nearSave) {
		return 0
	}
	return a.nearSave[g]
}

func (a *adaptState) resetServed() {
	a.nearSave = nil
	a.nearSaveB = [blocksPerPage]float64{}
}

// robCount is the Rob part of a counted remote access of dev to pAddr.
func (m *Manager) robCount(now sim.VTimeInSec, u *unit, a *adaptState, dev, pAddr uint64) {
	c := &m.cfg.Adapt
	if u.phase != phaseIdle || a.preplacing || a.writeKernel == m.kernel {
		return
	}
	if a.rob == nil {
		a.rob = make([]float64, len(m.gpms))
		a.robPend = make([]bool, len(m.gpms))
	}
	if int(dev) >= len(a.rob) {
		return
	}
	a.rob[dev] += m.r3RTT(dev, m.homeOf(pAddr))
	if a.robPend[dev] {
		return
	}
	for _, r := range a.replicas {
		if r.dev == dev {
			return
		}
	}
	// cheap pre-check: the accumulator must at least pay the copy
	sw := float64(m.cfg.SoftwareLatencyNS)
	if c.BatchSW && !c.RobFullSW && m.cfg.BatchSize > 1 {
		sw /= float64(m.cfg.BatchSize)
	}
	fixed := sw + float64(m.cfg.ShootdownLatencyNS)
	if a.rob[dev] < fixed {
		return
	}
	p, ok := m.pageTable.Find(u.key.pid, u.key.idx<<m.unitLog2)
	if !ok {
		return
	}
	home, uniform := m.pageHomes(p)
	if !uniform || home[0] == dev {
		return
	}
	pw := float64(a.kernWritten+1) / float64(a.kernSeen+2)
	if pw > 0.5 {
		m.AStats.RobGated++
		return
	}
	readers := 0
	fetches := 0.0
	for _, x := range u.counts {
		if x > 0 {
			readers++
			fetches += float64(x)
		}
	}
	if readers < 2 {
		return
	}
	ps := float64(uint64(1) << m.log2PageSize)
	f := fixed + m.r3Copy(home[0], dev, ps) + pw*m.r3RTT(home[0], dev)
	reuse := 0.0
	if fetches+float64(a.m3Hits) > 0 {
		reuse = float64(a.m3Hits) / (fetches + float64(a.m3Hits))
	}
	for g, x := range u.counts {
		if x == 0 || uint64(g) == dev {
			continue
		}
		near := m.r3RTT(uint64(g), home[0])
		for _, r := range a.replicas {
			if t := m.r3RTT(uint64(g), r.dev); t < near {
				near = t
			}
		}
		if t := m.r3RTT(uint64(g), dev); t < near {
			f += minF(float64(x), pageLines) * reuse * t
		}
	}
	if a.rob[dev] < f {
		return
	}
	m.robOpen(now, u, a, p, home[0], dev)
}

func minF(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}

// robOpen makes a copy of the page at GPM g.
func (m *Manager) robOpen(now sim.VTimeInSec, u *unit, a *adaptState, p vm.Page, home, g uint64) {
	ps := uint64(1) << m.log2PageSize
	if b := m.cfg.Adapt.ReplicaBudget; b > 0 {
		for m.replBytes[g]+ps > b && m.evictOldestOn(now, g) {
		}
		if m.replBytes[g]+ps > b {
			m.AStats.RobBudget++
			return
		}
	}
	m.replBytes[g] += ps
	if m.replBytes[g] > m.AStats.ReplPeak {
		m.AStats.ReplPeak = m.replBytes[g]
	}
	a.robPend[g] = true
	m.AStats.RobDecided++
	m.AStats.Decided[actReplicate][readerBucket(r3Readers(a))]++
	seq, kern, src := a.writeSeq, m.kernel, p.PAddr
	m.deferAct(now, g, func(now sim.VTimeInSec) {
		if a.writeSeq != seq || m.kernel != kern || u.phase != phaseIdle ||
			m.homeOf(src) != home {
			a.robPend[g] = false
			m.replBytes[g] -= ps
			m.AStats.ReplicaAbort++
			return
		}
		f := m.allocFrame(g)
		m.AStats.CopyBytes += ps
		m.AStats.ReplCopy += ps
		m.gpms[g].Engine.enqueue(now, &chunk{
			src:      m.gpms[home].Engine,
			srcPAddr: src,
			dstPAddr: f,
			size:     ps,
			done: func(now sim.VTimeInSec) {
				a.robPend[g] = false
				if a.writeSeq != seq || m.kernel != kern || u.phase != phaseIdle ||
					m.homeOf(src) != home {
					m.retireFrame(f)
					m.replBytes[g] -= ps
					m.AStats.ReplicaAbort++
					return
				}
				if len(a.replicas) == 0 {
					a.replHome = home
					a.fixed = true
					m.replicated[u] = true
					m.replOrder = append(m.replOrder, u)
					m.AStats.ReplicaOn++
				}
				a.replicas = append(a.replicas, replica{dev: g, frame: f})
				a.rob[g] = 0
				m.AStats.RobCopies++
			},
		})
	})
}
