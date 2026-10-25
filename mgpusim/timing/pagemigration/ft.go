package pagemigration

// ISCA27 P21 round 5: first touch inside the break-even rule
// (AdaptConfig.FT, r3 only).
//
// "always" (FT+R3): the first vector access to a page migrates it to the
// accessing GPM through the fault path (the access waits; software latency,
// shootdown and the 4 KB copy are paid, exactly like the m3p_ftm baseline);
// the page is not pinned and R3 keeps replicating, moving and pre-placing.
//
// "predict" (learning-augmented rent-or-buy): the first touch buys at once
// only when the buffer's evidence says that the first toucher g is the
// page's best place often enough; otherwise the page rents (R3 break-even).
// With the page at its original home o, the FT cost C = SW + shootdown +
// copy(o, g, page) (nothing is cached or in flight yet, so no hold), and
//
//	E   = the regret R3 pays before it moves a page of this buffer (the
//	      measured mean at the moves; before any move, its lower bound C:
//	      break-even moves only once the regret reached the move cost),
//	p   = (hits + 1) / (tests + 2): measured share of first-touched pages
//	      whose best place was g, by evidence class (below),
//	f_o = (harm + 1) / (wrong + 2): measured share of the misses whose best
//	      place was the original home o (FT moved away a page that renting
//	      never had to move),
//
// a right buy saves the rent and the later move (E + C) for C; a harmful
// wrong buy loses C plus the move back (E + C); a harmless wrong buy (best
// place neither g nor o) loses only C, the later move happens either way.
// Buy at first touch iff
//
//	(E + C) (p - (1 - p) f_o) > C.
//
// Bounds (per page, against the offline optimum OPT and R3's break-even,
// which is 2-competitive). Right prediction: FT pays C, the offline action;
// renting would have paid up to E + C. Wrong prediction: FT pays C on top of
// R3's break-even from g, i.e. at most C + 2 OPT; when the best place was o
// (OPT may be 0) the loss is C + E + C. The test buys only with positive
// expected value under the measured p and f_o, and with Laplace estimates a
// buffer whose first touchers are not its readers stops buying after O(1)
// checked misses, so the extra cost over R3 is bounded by a constant number
// of wrong buys per buffer and evidence class.
//
// Evidence (unbiased: moved and unmoved pages alike): a first-touched page
// whose first toucher is not its home is checked once, when its counted
// accesses would have cost one evaluation (SW + shootdown) at the original
// home o, or at its first R3 evaluation if earlier; hit iff the regret-best
// place over its counts is g. Classes: the buffer's trusted rule (learner.go)
// predicts g for the page (class 0) or not (class 1).

import (
	"github.com/sarchlab/akita/v3/mem/vm"
	"github.com/sarchlab/akita/v3/sim"
)

const numFTClass = 2

func (c *AdaptConfig) ft() bool { return c.FT != "" }

// ftFirst handles the first vector access to unit u by GPM dev; false =
// the access waits for the first-touch migration.
func (m *Manager) ftFirst(now sim.VTimeInSec, at Waker, dev uint64, u *unit,
	page vm.Page) (vm.Page, bool) {
	u.touched = true
	a := m.adaptUnit(u)
	if a.preplaced || a.preplacing || a.fixed || len(a.replicas) > 0 ||
		u.phase != phaseIdle || !m.eligible(u, now) {
		return page, true
	}
	p, ok := m.pageTable.Find(u.key.pid, u.key.idx<<m.unitLog2)
	if !ok {
		return page, true
	}
	home, uniform := m.pageHomes(p)
	if !uniform || home[0] == dev || m.gpms[dev] == nil {
		return page, true
	}
	o := home[0]
	cost := m.r3Fixed() + m.r3Copy(o, dev, float64(uint64(1)<<m.log2PageSize))
	a.ftG, a.ftOrig = dev+1, o
	// the buy also stalls the accesses that arrive during it: the
	// buffer's measured waiting per FT migration (none measured yet: 0)
	stall := 0.0
	if b := a.buf; b != nil && b.ftHeldN > 0 {
		stall = b.ftHeld / float64(b.ftHeldN)
	}
	a.ftCost = cost + stall
	if b := a.buf; b != nil {
		if l := b.learn; l.conf >= 0 && l.predict(a.idx, int(o)-1) == int(dev)-1 {
			a.ftClass = 0
		} else {
			a.ftClass = 1
		}
	}
	m.AStats.FTFirst++
	if m.cfg.Adapt.FT == "predict" && !m.ftWorth(a, cost, a.ftCost) {
		m.AStats.FTRent++
		return page, true
	}
	m.AStats.FTMoves++
	a.ftMoved = true
	a.act = actMove
	a.blockDst = [blocksPerPage]uint64{dev, dev, dev, dev}
	if m.cfg.Adapt.FT == "move" {
		// buy at once with R3's own move: the access goes on to the
		// home (rent) while the driver works; only the switch holds
		a.ftPending = true
		u.faulters = u.faulters[:0]
		m.Stats.CounterTriggers++
		m.adaptTrigger(now, u, dev)
		return page, true
	}
	m.Stats.Faults++
	u.faulters = append(u.faulters[:0], dev)
	m.trigger(now, u, dev)
	u.addWaiter(at)
	u.heldN++
	u.heldT += float64(now)
	a.ftPending = true
	return page, false
}

// noteHeld records the measured waiting of the accesses a migration held
// (ns): FT migrations per buffer (the stall part of the first-touch cost),
// R3 moves in total (against the modeled hold).
func (m *Manager) noteHeld(u *unit, end float64) {
	a := u.a
	w := (float64(u.heldN)*end - u.heldT) * 1e9
	if w < 0 {
		w = 0
	}
	if a.ftPending {
		a.ftPending = false
		m.AStats.FTHeldNS += w
		if b := a.buf; b != nil {
			b.ftHeld += w
			b.ftHeldN++
		}
		return
	}
	m.AStats.MoveHeldNS += w
	if b := a.buf; b != nil && a.wasMove {
		b.mvHeld += w
		b.mvHeldN++
	}
	a.wasMove = false
}

// ftWorth is the expected-value test of a first-touch buy: cost is the
// migration (SW + shootdown + copy), buy the cost plus the measured stall.
func (m *Manager) ftWorth(a *adaptState, cost, buy float64) bool {
	b := a.buf
	if b == nil {
		return false
	}
	k := a.ftClass
	p := float64(b.ftHits[k]+1) / float64(b.ftTests[k]+2)
	wrong := 0
	for i := range b.ftTests {
		wrong += b.ftTests[i] - b.ftHits[i]
	}
	fo := float64(b.ftHarm+1) / float64(wrong+2)
	e := cost
	if b.nMig > 0 {
		e = b.sumRegret / float64(b.nMig)
	}
	return (e+cost)*(p-(1-p)*fo) > buy
}

// ftAccess feeds a counted access of GPM dev to the first-touch check.
func (m *Manager) ftAccess(now sim.VTimeInSec, u *unit, a *adaptState, dev uint64) {
	if a.ftG == 0 || a.ftTested || a.buf == nil {
		return
	}
	// checked once the accesses cost as much as the buy, counted at both
	// the original home and the first toucher (moved or not: unbiased)
	a.ftAcc += m.r3RTT(dev, a.ftOrig) + m.r3RTT(dev, a.ftG-1)
	if a.ftAcc >= a.ftCost {
		m.ftTest(now, u, a)
	}
}

// ftTest records whether the first toucher is the page's best place.
func (m *Manager) ftTest(now sim.VTimeInSec, u *unit, a *adaptState) {
	if a.ftG == 0 || a.ftTested || a.buf == nil {
		return
	}
	p, ok := m.pageTable.Find(u.key.pid, u.key.idx<<m.unitLog2)
	if !ok {
		return
	}
	a.ftTested = true
	b := a.buf
	b.ftTests[a.ftClass]++
	m.AStats.FTTests++
	home, _ := m.pageHomes(p)
	best := m.r3Best(u, a, home)
	if now == 0 && m.TickingComponent != nil {
		now = m.Engine.CurrentTime() // a local access (LocalAccess has no time)
	}
	if m.cfg.Adapt.Predict && !a.observed {
		// the check is an observation for the buffer's rule too (FT'd
		// pages are rarely evaluated)
		a.observed = true
		m.observe(now, b, a.idx, best, a.ftOrig, false)
	}
	switch {
	case best == a.ftG-1:
		b.ftHits[a.ftClass]++
		m.AStats.FTHits++
		a.ftHitRec = true
	case best == a.ftOrig:
		b.ftHarm++
		m.AStats.FTHarm++
	}
}
