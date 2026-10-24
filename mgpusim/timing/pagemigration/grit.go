package pagemigration

// GRIT baseline (Wang, Li, Jaleel, Yang, Tang, HPCA 2024), AdaptConfig.Rule
// "grit", built from this simulator's actions with the same real costs as
// R3 (driver software latency per batch, TLB shootdown, drain, copies over
// the mesh). Each page has a scheme (the PTE's bits 9-10 in GRIT):
//
//	on-touch (default): every vector access by a GPM that is not the page's
//	        home faults and migrates the page there (the access waits, as
//	        in m3p_ftm, but the page is never pinned: ping-pong);
//	counter: remote accesses are counted per GPM (4 KB pages here, GRIT
//	        counts 64 KB groups); Threshold (256) migrates to that GPM;
//	duplication: a read by a GPM without a local copy faults and gets a
//	        local copy (driver batch, then the copy); a write is a
//	        protection fault that invalidates every copy (shootdown).
//
// Faults (on-touch migrations, duplication read and protection faults) are
// counted per page; at GritFaults (4) the scheme is re-selected from the
// page's read/write bit during the current scheme: written -> counter,
// read-only -> duplication. Neighboring-aware prediction: the aligned group
// of 8 pages takes the new scheme when more than half of it already has it,
// and the aligned 64 pages when more than half of them have it (GRIT keeps
// group bits in the PTE; adopting pages switch on their next fault, no
// migration). Copies stay valid across kernel boundaries until a write
// (GRIT invalidates them only on writes; host writes, which GRIT's driver
// sees, collapse them at the boundary).

import (
	"github.com/sarchlab/akita/v3/mem/vm"
	"github.com/sarchlab/akita/v3/sim"
)

const (
	gritOnTouch = iota
	gritCounter
	gritDup
)

// GritFaults is GRIT's default fault threshold.
const GritFaults = 4

func (c *AdaptConfig) grit() bool { return c.Rule == "grit" }

// gritDriverAt is when a fault of GPM dev decided now is serviced: the end
// of the driver batch it joins (per-GPM queue, BatchSize per batch,
// SoftwareLatencyNS each).
func (m *Manager) gritDriverAt(now sim.VTimeInSec, dev uint64) sim.VTimeInSec {
	if m.drvEnd == nil {
		m.drvEnd = make([]sim.VTimeInSec, len(m.gpms))
		m.drvN = make([]int, len(m.gpms))
	}
	if m.drvN[dev] > 0 && m.drvN[dev] < m.cfg.BatchSize && now <= m.drvEnd[dev] {
		m.drvN[dev]++
		return m.drvEnd[dev]
	}
	start := now
	if m.drvEnd[dev] > start {
		start = m.drvEnd[dev]
	}
	m.drvEnd[dev] = start + ns(m.cfg.SoftwareLatencyNS)
	m.drvN[dev] = 1
	m.AStats.DrvBatches++
	return m.drvEnd[dev]
}

// gritResolve applies the page's scheme to an access; false = it waits.
func (m *Manager) gritResolve(now sim.VTimeInSec, at Waker, dev uint64, u *unit,
	vector, isWrite bool, page vm.Page) (vm.Page, bool) {
	if u == nil {
		u = m.getUnit(m.unitOf(page.PID, page.VAddr))
	}
	a := m.adaptUnit(u)
	if a.gWait&(1<<dev) != 0 || now < a.holdUntil {
		u.addWaiter(at)
		return page, false
	}
	if isWrite {
		a.gWritten = true
		a.gEpoch++
	}
	home := m.homeOf(page.PAddr)
	switch a.gScheme {
	case gritOnTouch:
		if !vector || home == dev || u.phase != phaseIdle || !m.eligible(u, now) {
			return page, true
		}
		a.gFaults++
		m.AStats.GritFaults++
		if m.gritCheck(now, u, a) {
			return m.gritResolve(now, at, dev, u, vector, isWrite, page)
		}
		m.AStats.GritTouchMoves++
		a.act = actMove
		a.blockDst = [blocksPerPage]uint64{dev, dev, dev, dev}
		m.Stats.Faults++
		u.faulters = append(u.faulters[:0], dev)
		m.trigger(now, u, dev)
		u.addWaiter(at)
		return page, false
	case gritDup:
		if isWrite {
			if len(a.replicas) == 0 {
				return page, true
			}
			// protection fault: every copy is invalidated
			a.gFaults++
			m.AStats.GritFaults++
			m.collapse(now, u, true)
			m.AStats.ReplicaWrite++
			m.gritCheck(now, u, a)
			u.addWaiter(at)
			return page, false
		}
		if !vector || home == dev {
			return page, true
		}
		for _, r := range a.replicas {
			if r.dev == dev {
				page.PAddr = r.frame
				page.DeviceID = dev
				m.AStats.ReplicaReads++
				return page, true
			}
		}
		if u.phase != phaseIdle {
			return page, true
		}
		a.gFaults++
		m.AStats.GritFaults++
		if m.gritCheck(now, u, a) {
			return m.gritResolve(now, at, dev, u, vector, isWrite, page)
		}
		if !m.gritAddReplica(now, u, a, page, home, dev) {
			return page, true // no room for a copy: read the master
		}
		u.addWaiter(at)
		return page, false
	}
	return page, true // counter: counted in gritCount
}

// gritCount is a counted remote access under the counter scheme.
func (m *Manager) gritCount(now sim.VTimeInSec, u *unit, a *adaptState, dev uint64, remote bool) {
	if !remote || a.gScheme != gritCounter || u.phase != phaseIdle || !m.eligible(u, now) ||
		u.counts[dev] < m.cfg.Threshold {
		return
	}
	for i := range u.counts {
		u.counts[i] = 0
	}
	m.AStats.GritCounterMoves++
	a.act = actMove
	a.blockDst = [blocksPerPage]uint64{dev, dev, dev, dev}
	m.Stats.CounterTriggers++
	u.faulters = u.faulters[:0]
	m.trigger(now, u, dev)
}

// gritCheck re-selects the scheme once the page reached the fault
// threshold; it reports whether the scheme changed.
func (m *Manager) gritCheck(now sim.VTimeInSec, u *unit, a *adaptState) bool {
	if a.gFaults < GritFaults {
		return false
	}
	nw := gritDup
	if a.gWritten {
		nw = gritCounter
	}
	a.gFaults = 0
	a.gWritten = false
	if nw == a.gScheme {
		return false
	}
	m.gritSet(now, u, a, nw)
	if nw == gritCounter {
		m.AStats.GritToCounter++
	} else {
		m.AStats.GritToDup++
	}
	m.gritNeighbors(now, u, a, nw)
	return true
}

// gritSet changes the scheme of a page (leaving duplication drops the
// copies).
func (m *Manager) gritSet(now sim.VTimeInSec, u *unit, a *adaptState, s int) {
	if a.gScheme == gritDup && s != gritDup && len(a.replicas) > 0 {
		m.collapse(now, u, true)
	}
	a.gScheme = s
	for i := range u.counts {
		u.counts[i] = 0
	}
}

// gritNeighbors is GRIT's neighboring-aware prediction over the aligned
// groups of 8 and 64 pages around u.
func (m *Manager) gritNeighbors(now sim.VTimeInSec, u *unit, a *adaptState, s int) {
	for _, g := range []uint64{8, 64} {
		base := u.key.idx &^ (g - 1)
		var us []*unit
		same := 0
		for i := base; i < base+g; i++ {
			if _, ok := m.pageTable.Find(u.key.pid, i<<m.unitLog2); !ok {
				continue
			}
			v := m.getUnit(unitKey{u.key.pid, i})
			us = append(us, v)
			if m.adaptUnit(v).gScheme == s {
				same++
			}
		}
		if 2*same <= len(us) {
			return
		}
		for _, v := range us {
			if va := v.a; va.gScheme != s {
				va.gScheme = s // takes effect at its next fault
				va.gFaults = 0
				va.gWritten = false
				m.AStats.GritAdopted++
			}
		}
	}
}

// gritAddReplica gives GPM dev a local copy of the page after the driver
// services the fault; the faulting GPM waits. A write in the meantime
// cancels it.
func (m *Manager) gritAddReplica(now sim.VTimeInSec, u *unit, a *adaptState, p vm.Page,
	home, dev uint64) bool {
	ps := uint64(1) << m.log2PageSize
	if b := m.cfg.Adapt.ReplicaBudget; b > 0 {
		for m.replBytes[dev]+ps > b && m.evictOldestOn(now, dev) {
		}
		if m.replBytes[dev]+ps > b {
			return false
		}
	}
	m.replBytes[dev] += ps
	if m.replBytes[dev] > m.AStats.ReplPeak {
		m.AStats.ReplPeak = m.replBytes[dev]
	}
	a.gWait |= 1 << dev
	epoch := a.gEpoch
	src := p.PAddr
	m.AStats.GritDupFaults++
	m.later(m.gritDriverAt(now, dev), func(now sim.VTimeInSec) {
		f := m.allocFrame(dev)
		m.gpms[dev].Engine.enqueue(now, &chunk{
			src:      m.gpms[home].Engine,
			srcPAddr: src,
			dstPAddr: f,
			size:     ps,
			done: func(now sim.VTimeInSec) {
				a.gWait &^= 1 << dev
				m.wake(now, u)
				if a.gEpoch != epoch || a.gScheme != gritDup || u.phase != phaseIdle ||
					m.homeOf(src) != home {
					m.retireFrame(f)
					m.replBytes[dev] -= ps
					m.AStats.ReplicaAbort++
					return
				}
				if len(a.replicas) == 0 {
					a.replHome = home
					m.replicated[u] = true
					m.replOrder = append(m.replOrder, u)
					m.AStats.ReplicaOn++
				}
				a.replicas = append(a.replicas, replica{dev: dev, frame: f})
			},
		})
		m.AStats.CopyBytes += ps
		m.AStats.ReplCopy += ps
	})
	return true
}

// gritReplicasCurrent reports whether every copy still holds the master's
// bytes (only host writes can change the master without collapsing them).
func (m *Manager) gritReplicasCurrent(u *unit) bool {
	if m.Storage == nil {
		return false
	}
	p, ok := m.pageTable.Find(u.key.pid, u.key.idx<<m.unitLog2)
	if !ok {
		return false
	}
	ps := uint64(1) << m.log2PageSize
	master, err := m.Storage.Read(p.PAddr, ps)
	if err != nil {
		return false
	}
	for _, r := range u.a.replicas {
		d, err := m.Storage.Read(r.frame, ps)
		if err != nil || string(d) != string(master) {
			return false
		}
	}
	return true
}
