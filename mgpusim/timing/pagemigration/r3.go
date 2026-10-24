package pagemigration

// ISCA27 P21 round 3: one break-even (ski-rental) rule for every action,
// with every constant taken from the platform (AdaptConfig.Rule == "r3").
//
// Latency model (zero load, measured on the 7x7 mesh at 32-cycle switches):
// a round trip from GPM g to data homed at h costs
//
//	RTT(g, h) = 0                                 g == h
//	          = R3BaseNS + R3HopNS * hops(g, h)   otherwise
//
// with R3HopNS = 2 * (switch latency + 4.1 ns) (both directions through
// each hop) and R3BaseNS = 65.7 ns (requester and owner RDMA pipelines);
// on a flat network hops(g, h) is the average hop count for every pair.
//
// Accumulated regret. Every counted access of GPM g to block b of a page
// adds to the per-block per-GPM counters (local L1V misses included). For a
// candidate home c the regret is
//
//	R(c) = sum_b sum_g count[b][g] * (RTT(g, home_b) - RTT(g, c)),
//
// the extra latency the accesses paid because the data was not at c. An
// action is taken as soon as its regret reaches its one-time cost (the
// classic break-even rule of rent-or-buy, which is 2-competitive against an
// offline placement that knows the future accesses: before buying, the
// online rule paid at most the buy price in rent, then the buy price). The
// accumulator restarts after an action, so moving back pays the cost again
// (no migration limit, no cooldown).
//
// Actions and their one-time cost (ns):
//
//	move c      page to c:          SW + shootdown + hold + copy(home_b -> c)
//	block       each 1 KB block to its own best c_b: same, copying the moved blocks
//	replicate   read-only page, copies at one reader-weighted site per mesh
//	            quadrant: SW + shootdown (driver work and TLB update to make
//	            the copies visible) + copy(home -> s) per site + P(write) *
//	            invalidation messages; regret = sum_g count_g * (RTT(g, home)
//	            - RTT(g, nearest copy))
//
// Every action that changes the address readers use also loses the lines
// they keep in their L2s (M3): sum_g min(lines, fetches_g) x reuse x RTT to
// the new location, reuse = M3 hits / (hits + fetches) of the page.
// SW is -pm-sw-latency-ns, shootdown -pm-shootdown-ns, hold the waiting of
// the accesses arriving during the switch (rate x hold^2 / 2, hold =
// shootdown + recent drain at the home), copy(a, b, bytes) = RTT(a, b) +
// bytes x hops / link bandwidth. P(write) is the Laplace estimate over the
// kernels the page lived in. The best action is the one with the largest
// regret - cost among those with regret >= cost; candidates for c are the
// GPMs that accessed the page and the reader-weighted median tile.
// Evaluations happen when the accumulated remote latency (an upper bound of
// every regret) doubles, starting at SW + shootdown.
//
// Pre-placement: the buffer's rule (learner.go) predicts the home of a page
// nobody touched; the page is pre-placed iff p E - (1 - p) E > copy (a
// right placement saves about E, a wrong one costs about as much), where
// E is the mean regret the buffer's migrated pages had accumulated when
// they were moved and p = (hits + 1) / (tests + 2) over the out-of-sample
// tests of the rule at the page's distance class (log2 of the distance to
// the nearest observation): the trusted rule's moving predictions of new
// observations and the checks of pre-placed pages (made when their accesses
// would have cost one evaluation at the original home, local or remote).
// A rule is trusted only as far from its observations as it was validated. Observations for the rule: a page's best candidate at its
// first evaluation.

import (
	"math"
	"math/bits"
	"sort"

	"github.com/sarchlab/akita/v3/mem/vm"
	"github.com/sarchlab/akita/v3/sim"
)

func (c *AdaptConfig) r3() bool { return c.Rule == "r3" }

// r3Hops is the hop count between two GPMs (the average on a flat network).
func (m *Manager) r3Hops(g, h uint64) float64 {
	if g == h {
		return 0
	}
	if f := m.cfg.Adapt.FlatHops; f > 0 {
		return f
	}
	return float64(m.hops(g, h))
}

// r3RTT is the zero-load round trip of GPM g to data homed at h.
func (m *Manager) r3RTT(g, h uint64) float64 {
	if g == h {
		return 0
	}
	c := &m.cfg.Adapt
	return c.R3BaseNS + c.R3HopNS*m.r3Hops(g, h)
}

// r3Copy is the cost of copying bytes from a to b.
func (m *Manager) r3Copy(a, b uint64, bytes float64) float64 {
	if a == b {
		return 0
	}
	h := m.r3Hops(a, b)
	if h < 1 {
		h = 1
	}
	return m.r3RTT(a, b) + bytes*h/m.cfg.Adapt.R3LinkBytesPerNS
}

// r3Fixed is the fixed part of one migration (software and shootdown).
func (m *Manager) r3Fixed() float64 {
	return float64(m.cfg.SoftwareLatencyNS + m.cfg.ShootdownLatencyNS)
}

// r3Kernel updates the per-page kernel write history.
func (m *Manager) r3Kernel(a *adaptState) {
	if a.lastKern == m.kernel {
		return
	}
	if a.lastKern >= 0 {
		a.kernSeen++
		if a.writeKernel == a.lastKern {
			a.kernWritten++
		}
	}
	a.lastKern = m.kernel
}

// r3Count is the R3 part of a counted access (after the counters).
func (m *Manager) r3Count(now sim.VTimeInSec, u *unit, a *adaptState, dev, pAddr uint64, remote bool) {
	m.r3Kernel(a)
	if a.preplaced && !a.preCheck && a.buf != nil {
		// check a pre-placed page when its accesses would have cost one
		// evaluation's worth at the original home, whether the prediction
		// was right (accesses local) or wrong (remote): unbiased evidence
		a.accOrig += m.r3RTT(dev, m.ownerOf(a.origPA))
		if a.accOrig >= m.r3Fixed() {
			m.r3Check(u, a)
		}
	}
	m.ftAccess(u, a, dev)
	if !remote {
		return
	}
	if a.cmigHolders&(1<<dev) != 0 && a.cmigKernel == m.kernel && !a.cmigBusy &&
		m.cmigMiss != nil {
		m.cmigMiss[dev]++ // a fetch of a cmig page the pulled lines did not serve
	}
	a.acc += m.r3RTT(dev, m.homeOf(pAddr))
	if a.nextEval == 0 {
		a.nextEval = m.r3Fixed()
	}
	if a.acc >= a.nextEval {
		m.r3Evaluate(now, u)
	}
}

// r3Uncount takes back an access served by an M3 replica.
func (m *Manager) r3Uncount(a *adaptState, dev, pAddr uint64) {
	a.acc -= m.r3RTT(dev, m.homeOf(pAddr))
	if a.acc < 0 {
		a.acc = 0
	}
}

// r3Weights returns the per-block per-GPM counts.
func (m *Manager) r3Weights(u *unit, a *adaptState) [blocksPerPage][]float64 {
	var w [blocksPerPage][]float64
	for b := 0; b < blocksPerPage; b++ {
		w[b] = make([]float64, len(m.gpms))
		for g := range m.gpms {
			if a.bc[0] != nil {
				w[b][g] = float64(a.bc[b][g])
			} else {
				w[b][g] = float64(u.counts[g]) / blocksPerPage
			}
		}
	}
	return w
}

// r3Median is the GPM nearest to the reader-weighted median tile of the
// readers in quadrant q (q < 0: all readers).
func (m *Manager) r3Median(tot []float64, q int) (uint64, bool) {
	type pt struct {
		v int
		w float64
	}
	var xs, ys []pt
	sum := 0.0
	for g, w := range tot {
		if w <= 0 || m.gpms[g] == nil {
			continue
		}
		t := m.gpms[g].Tile
		if q >= 0 && m.quadrant(t) != q {
			continue
		}
		xs = append(xs, pt{t % meshWidth, w})
		ys = append(ys, pt{t / meshWidth, w})
		sum += w
	}
	if sum == 0 {
		return 0, false
	}
	med := func(p []pt) int {
		sort.Slice(p, func(i, j int) bool { return p[i].v < p[j].v })
		acc := 0.0
		for _, x := range p {
			acc += x.w
			if acc >= sum/2 {
				return x.v
			}
		}
		return p[len(p)-1].v
	}
	t := med(xs) + meshWidth*med(ys)
	if d, ok := m.tileDev[t]; ok {
		return d, true
	}
	best, bh := uint64(0), math.MaxInt32
	for d, g := range m.gpms {
		if g == nil || (q >= 0 && m.quadrant(g.Tile) != q) {
			continue
		}
		if h := tileHops(g.Tile, t); h < bh {
			best, bh = uint64(d), h
		}
	}
	return best, bh < math.MaxInt32
}

// quadrant of a tile around the mesh centre.
func (m *Manager) quadrant(t int) int {
	c := (meshWidth - 1) / 2
	q := 0
	if t%meshWidth > c {
		q |= 1
	}
	if t/meshWidth > c {
		q |= 2
	}
	return q
}

// r3Sites are the replica sites: one reader-weighted point per quadrant
// (Blind: four pseudo-random GPMs).
func (m *Manager) r3Sites(tot []float64, home uint64, seed uint64) []uint64 {
	var out []uint64
	seen := map[uint64]bool{home: true}
	if m.cfg.Adapt.R3Blind {
		var devs []uint64
		for d, g := range m.gpms {
			if g != nil {
				devs = append(devs, uint64(d))
			}
		}
		for i := uint64(0); len(out) < 4 && i < 64; i++ {
			d := devs[mix64(seed*131+i)%uint64(len(devs))]
			if !seen[d] {
				seen[d] = true
				out = append(out, d)
			}
		}
		return out
	}
	for q := 0; q < 4; q++ {
		if d, ok := m.r3Median(tot, q); ok && !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	return out
}

// r3Evaluate takes the break-even decision for one page.
func (m *Manager) r3Evaluate(now sim.VTimeInSec, u *unit) {
	a := u.a
	c := &m.cfg.Adapt
	if a == nil || a.fixed || a.preplacing || len(a.replicas) > 0 ||
		u.phase != phaseIdle || !m.eligible(u, now) {
		return
	}
	p, ok := m.pageTable.Find(u.key.pid, u.key.idx<<m.unitLog2)
	if !ok {
		return
	}
	m.AStats.Evaluations++
	m.AStats.DecisionMsgs++
	m.ftTest(u, a) // first-touch check at the first evaluation at the latest
	home, uniform := m.pageHomes(p)
	w := m.r3Weights(u, a)
	tot := make([]float64, len(m.gpms))
	readers := 0
	for g := range m.gpms {
		for b := 0; b < blocksPerPage; b++ {
			tot[g] += w[b][g]
		}
		if tot[g] > 0 {
			readers++
		}
	}
	// candidates: accessing GPMs, the weighted median, the current homes
	cands := map[uint64]bool{}
	for g, x := range tot {
		if x > 0 && m.gpms[g] != nil {
			cands[uint64(g)] = true
		}
	}
	if d, ok := m.r3Median(tot, -1); ok {
		cands[d] = true
	}
	for b := 0; b < blocksPerPage; b++ {
		cands[home[b]] = true
	}
	keys := make([]uint64, 0, len(cands))
	for d := range cands {
		keys = append(keys, d)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	regret := func(b int, cnd uint64) float64 {
		r := 0.0
		for g, x := range w[b] {
			if x > 0 {
				r += x * (m.r3RTT(uint64(g), home[b]) - m.r3RTT(uint64(g), cnd))
			}
		}
		return r
	}
	// best page-level candidate (Blind: the GPM with the most accesses)
	var best uint64
	bestR := math.Inf(-1)
	for _, cnd := range keys {
		r := 0.0
		for b := 0; b < blocksPerPage; b++ {
			r += regret(b, cnd)
		}
		if r > bestR {
			best, bestR = cnd, r
		}
	}
	if c.R3Blind {
		d, _, _ := dominantF(tot)
		best = d
		bestR = 0
		for b := 0; b < blocksPerPage; b++ {
			bestR += regret(b, best)
		}
	}
	if a.buf != nil && !a.r3Evaluated {
		a.r3Evaluated = true
		a.buf.r3Evaluated++
	}
	// observation and pre-placement check at the first evaluation
	if a.buf != nil && c.Predict && !a.observed {
		a.observed = true
		m.AStats.EarlyObs++
		m.observe(now, a.buf, a.idx, best, home[0], false)
	}
	hold := m.r3Hold(now, a, p.PAddr, home[0])
	fixed := m.r3Fixed() + hold
	// lines the readers keep in their L2s (M3) are lost when the data
	// changes address: each reader g holds about min(lines, fetches of g)
	// lines, reused with the observed probability hits / (hits + fetches),
	// and re-fetches them from the new location
	fetches := 0.0
	for _, x := range tot {
		fetches += x
	}
	reuse := 0.0
	if fetches+float64(a.m3Hits) > 0 {
		reuse = float64(a.m3Hits) / (fetches + float64(a.m3Hits))
	}
	refetch := func(b int, to uint64) float64 {
		r := 0.0
		for g, x := range w[b] {
			if x > 0 {
				r += math.Min(x, blockLines) * reuse * m.r3RTT(uint64(g), to)
			}
		}
		return r
	}
	type opt struct {
		act    int
		dst    [blocksPerPage]uint64
		reps   []uint64
		regret float64
		cost   float64
	}
	var chosen *opt
	pick := func(o opt) {
		if o.regret < o.cost {
			return
		}
		if chosen == nil || o.regret-o.cost > chosen.regret-chosen.cost {
			oo := o
			chosen = &oo
		}
	}
	if c.Move {
		o := opt{act: actMove, regret: bestR, cost: fixed}
		moved := 0
		for b := 0; b < blocksPerPage; b++ {
			o.dst[b] = best
			if home[b] != best {
				o.cost += m.r3Copy(home[b], best, 1<<log2Block) + refetch(b, best)
				moved++
			}
		}
		if moved > 0 {
			pick(o)
		}
	}
	if c.Block {
		o := opt{act: actBlock, cost: fixed}
		distinct := map[uint64]bool{}
		moved := 0
		for b := 0; b < blocksPerPage; b++ {
			bd, br := home[b], 0.0
			if c.R3Blind {
				d, _, _ := dominantF(w[b])
				if r := regret(b, d); r > 0 {
					bd, br = d, r
				}
			} else {
				for _, cnd := range keys {
					if r := regret(b, cnd); r > br {
						bd, br = cnd, r
					}
				}
			}
			o.dst[b] = bd
			o.regret += br
			distinct[bd] = true
			if bd != home[b] {
				o.cost += m.r3Copy(home[b], bd, 1<<log2Block) + refetch(b, bd)
				moved++
			}
		}
		if moved > 0 && len(distinct) > 1 && m.bhRoom(majority(o.dst), o.dst) {
			pick(o)
		}
	}
	if c.Replicate && uniform && a.writeKernel != m.kernel {
		sites := m.r3Sites(tot, home[0], p.PAddr>>m.log2PageSize)
		if len(sites) > 0 {
			o := opt{act: actReplicate}
			used := map[uint64]bool{}
			for g, x := range tot {
				if x <= 0 {
					continue
				}
				near, at := m.r3RTT(uint64(g), home[0]), home[0]
				for _, s := range sites {
					if r := m.r3RTT(uint64(g), s); r < near {
						near, at = r, s
					}
				}
				o.regret += x * (m.r3RTT(uint64(g), home[0]) - near)
				if at != home[0] {
					used[at] = true
				}
			}
			// copies serve readers in at least two places (the home or
			// sites); a single useful site is a move, which outlives the
			// kernel
			for _, s := range sites {
				if used[s] {
					o.reps = append(o.reps, s)
				}
			}
			homeUsed := false
			for g, x := range tot {
				if x > 0 && !used[uint64(g)] {
					near := m.r3RTT(uint64(g), home[0])
					for _, s := range o.reps {
						near = math.Min(near, m.r3RTT(uint64(g), s))
					}
					if near == m.r3RTT(uint64(g), home[0]) {
						homeUsed = true
					}
				}
			}
			if len(o.reps) < 2 && !(len(o.reps) == 1 && homeUsed) {
				o.reps = nil
			}
			// making copies visible needs the driver (frames, PTE replica
			// bits) and a TLB shootdown, like a migration
			o.cost = m.r3Fixed()
			pw := float64(a.kernWritten+1) / float64(a.kernSeen+2)
			for _, s := range o.reps {
				o.cost += m.r3Copy(home[0], s, float64(uint64(1)<<m.log2PageSize)) +
					pw*m.r3RTT(home[0], s)
			}
			// readers redirected to a copy re-fetch the lines they keep
			for g, x := range tot {
				if x <= 0 {
					continue
				}
				near, at := m.r3RTT(uint64(g), home[0]), home[0]
				for _, s := range o.reps {
					if r := m.r3RTT(uint64(g), s); r < near {
						near, at = r, s
					}
				}
				if at != home[0] {
					o.cost += math.Min(x, pageLines) * reuse * near
				}
			}
			if len(o.reps) > 0 {
				pick(o)
			}
		}
	}
	if a.cmigKernel != m.kernel {
		a.cmigHolders, a.cmigKernel = 0, m.kernel // attempts are per kernel
	}
	if c.Cmig && uniform && a.writeKernel != m.kernel && !a.cmigBusy {
		// L2-resident migration to the reader with the most regret: its
		// future fetches hit its own L2 with the measured probability that
		// a fetch of a cmig page there is served by the pulled lines
		// (they may be dropped, replaced or invalidated first); only the
		// copy is paid; one attempt per reader, page and kernel
		var t uint64
		var tr float64
		for g, x := range tot {
			if x <= 0 || uint64(g) == home[0] || a.cmigHolders&(1<<uint(g)) != 0 {
				continue
			}
			ph := float64(m.cmigHit[g]+1) / float64(m.cmigHit[g]+m.cmigMiss[g]+2)
			if r := x * m.r3RTT(uint64(g), home[0]) * ph; r > tr {
				t, tr = uint64(g), r
			}
		}
		if tr > 0 && m.gpms[t].InstallRemote != nil {
			pick(opt{act: actCmig, dst: [blocksPerPage]uint64{t, t, t, t}, regret: tr,
				cost: m.r3Copy(home[0], t, float64(uint64(1)<<m.log2PageSize))})
		}
	}
	if chosen == nil {
		a.nextEval = 2 * a.acc
		return
	}
	a.bucket = readerBucket(readers)
	m.AStats.Decided[chosen.act][a.bucket]++
	if chosen.act == actCmig {
		t := chosen.dst[0]
		u.counts[t] = 0
		if a.bc[0] != nil {
			for b := range a.bc {
				a.bc[b][t] = 0
			}
		}
		a.acc, a.nextEval = 0, 0
		m.startCmig(now, u, a, p, home[0], t)
		return
	}
	if a.cmigHolders != 0 && (chosen.act == actMove || chosen.act == actBlock) {
		m.AStats.CmigUpgrades++
	}
	if chosen.act == actReplicate {
		a.acc, a.nextEval = 0, 0
		if a.buf != nil {
			a.buf.repDecided++
			a.buf.sumRepRegret += chosen.regret
		}
		m.startReplicate(now, u, a, p, home[0], chosen.reps)
		return
	}
	if a.preplaced {
		m.AStats.PreFallback++
		if a.preWrong && a.buf != nil {
			a.buf.preWrongMoved++
			a.preWrong = false
		}
	}
	if a.buf != nil {
		a.buf.sumRegret += chosen.regret
		a.buf.nMig++
	}
	a.act = chosen.act
	a.blockDst = chosen.dst
	for i := range u.counts {
		u.counts[i] = 0
	}
	if a.bc[0] != nil {
		for b := range a.bc {
			for i := range a.bc[b] {
				a.bc[b][i] = 0
			}
		}
	}
	a.n = 0
	a.acc, a.nextEval = 0, 0
	m.Stats.CounterTriggers++
	u.faulters = u.faulters[:0]
	m.adaptTrigger(now, u, majority(chosen.dst))
}

func dominantF(c []float64) (uint64, float64, float64) {
	var best uint64
	var tot float64
	for d, x := range c {
		tot += x
		if x > c[best] {
			best = uint64(d)
		}
	}
	return best, c[best], tot
}

// r3PrePlaceOK is the pre-placement test of an untouched page of buffer b
// predicted at d (home h).
func (m *Manager) r3PrePlaceOK(b *buffer, k int, h, d uint64) bool {
	if b.nMig == 0 {
		return false
	}
	// evidence (Laplace prior): checks of pre-placed pages once there are
	// any (they test exactly the predictions acted on), before that the
	// trusted rule's moving predictions of new observations
	tests, hits := 0, 0
	for i := range b.evTests {
		tests += b.evTests[i]
		hits += b.evHits[i]
	}
	p := float64(hits+1) / float64(tests+2)
	if b.preChecks > 0 {
		p = float64(b.preHits+1) / float64(b.preChecks+2)
	}
	e := b.sumRegret / float64(b.nMig)
	// a right placement saves about E (the regret the page would pay before
	// being moved); a wrong one costs the share f of wrong placements that
	// had to be moved again (measured, Laplace prior) times a migration
	// (about E at break-even); a wrong one that need not move costs only
	// the copy
	f := float64(b.preWrongMoved+1) / float64(b.preChecks-b.preHits+2)
	return e*(p-(1-p)*f) > m.r3Copy(h, d, float64(uint64(1)<<m.log2PageSize))
}

// SetMeshWidth sets the mesh width used for tile coordinates (7 by default).
func SetMeshWidth(w int) { meshWidth = w }

// r3Readers is the number of distinct first readers (diagnostics).
func r3Readers(a *adaptState) int { return bits.OnesCount64(a.readMask) }

// r3Hold is the expected waiting of the page's counted accesses (the same
// accesses the regret counts) that arrive while the page is switched: rate
// x hold^2 / 2, hold = shootdown + drain (+ the copy without copy-first);
// the drain is the measured drain of recent migrations from the home (a
// Little's-law estimate from the frame's lifetime issue rate overestimated
// it for bursty pages and suppressed migrations, round 3 it5).
func (m *Manager) r3Hold(now sim.VTimeInSec, a *adaptState, pAddr, home uint64) float64 {
	if a.n == 0 {
		return 0
	}
	age := float64(now-a.t0) * 1e9
	if age < 1 {
		age = 1
	}
	rate := float64(a.n) / age
	drain := 0.0
	if m.drainEMA != nil {
		drain = m.drainEMA[home] // measured drains of recent migrations from home
	}
	h := float64(m.cfg.ShootdownLatencyNS) + drain
	if !m.cfg.Adapt.CopyFirst {
		// the copy happens during the hold: one page over (at least) one hop
		c := &m.cfg.Adapt
		h += c.R3BaseNS + c.R3HopNS + float64(uint64(1)<<m.log2PageSize)/c.R3LinkBytesPerNS
	}
	return rate * h * h / 2
}

// r3PropagateReplicate applies a replicate decision to the buffer's other
// read-only pages around idx (the action is predicted like a home): a page
// is replicated iff p x E >= its replication cost, p = (pages that decided
// replicate + 1) / (pages evaluated + 2), E = their mean regret at the
// decision.
func (m *Manager) r3PropagateReplicate(now sim.VTimeInSec, b *buffer, idx int, sites []uint64) {
	if !m.cfg.Adapt.Replicate || b.repDecided == 0 {
		return
	}
	p := float64(b.repDecided+1) / float64(b.r3Evaluated+2)
	e := b.sumRepRegret / float64(b.repDecided)
	ps := uint64(1) << m.log2PageSize
	lo, hi := idx-m.cfg.Adapt.PredictWindow, idx+m.cfg.Adapt.PredictWindow
	if lo < 0 {
		lo = 0
	}
	if hi >= b.pages {
		hi = b.pages - 1
	}
	for i := lo; i <= hi; i++ {
		if i == idx {
			continue
		}
		va := b.start + uint64(i)*ps
		pg, ok := m.pageTable.Find(b.pid, va)
		if !ok {
			continue
		}
		home, uniform := m.pageHomes(pg)
		if !uniform {
			continue
		}
		v := m.getUnit(m.unitOf(b.pid, va))
		a := m.adaptUnit(v)
		if a.fixed || a.preplacing || len(a.replicas) > 0 || v.phase != phaseIdle ||
			a.writeKernel == m.kernel {
			continue
		}
		var rs []uint64
		cost := m.r3Fixed()
		pw := float64(a.kernWritten+1) / float64(a.kernSeen+2)
		for _, s := range sites {
			if s == home[0] {
				continue
			}
			rs = append(rs, s)
			cost += m.r3Copy(home[0], s, float64(ps)) + pw*m.r3RTT(home[0], s)
		}
		if len(rs) == 0 || p*e < cost {
			continue
		}
		m.AStats.Decided[actReplicate][a.bucket]++
		m.AStats.Propagated++
		m.startReplicate(now, v, a, pg, home[0], rs)
	}
}

// r3Best is the candidate with the largest regret over the page's counts
// (the GPMs that accessed it, the weighted median, the current homes).
func (m *Manager) r3Best(u *unit, a *adaptState, home [blocksPerPage]uint64) uint64 {
	w := m.r3Weights(u, a)
	tot := make([]float64, len(m.gpms))
	for g := range m.gpms {
		for b := 0; b < blocksPerPage; b++ {
			tot[g] += w[b][g]
		}
	}
	cands := []uint64{home[0]}
	for g, x := range tot {
		if x > 0 && m.gpms[g] != nil {
			cands = append(cands, uint64(g))
		}
	}
	if d, ok := m.r3Median(tot, -1); ok {
		cands = append(cands, d)
	}
	best, bestR := home[0], 0.0
	for _, c := range cands {
		r := 0.0
		for b := 0; b < blocksPerPage; b++ {
			for g, x := range w[b] {
				if x > 0 {
					r += x * (m.r3RTT(uint64(g), home[b]) - m.r3RTT(uint64(g), c))
				}
			}
		}
		if r > bestR {
			best, bestR = c, r
		}
	}
	return best
}

// r3Check records whether a pre-placed page is at its best place.
func (m *Manager) r3Check(u *unit, a *adaptState) {
	p, ok := m.pageTable.Find(u.key.pid, u.key.idx<<m.unitLog2)
	if !ok {
		return
	}
	a.preCheck = true
	a.buf.preChecks++
	a.buf.evTests[a.preClass]++
	m.AStats.PreChecks++
	home, _ := m.pageHomes(p)
	if m.r3Best(u, a, home) == home[0] {
		a.buf.preHits++
		a.buf.evHits[a.preClass]++
		m.AStats.PreCheckHits++
	} else {
		a.preWrong = true
	}
}

// startCmig pulls the page from its home into GPM t's L2 (copy engine; the
// lines are installed like M3 fills, so the normal L2 replacement and the
// M3 kernel-boundary rules apply). A write to the page during the copy
// cancels the install.
func (m *Manager) startCmig(now sim.VTimeInSec, u *unit, a *adaptState, p vm.Page, home, t uint64) {
	ps := uint64(1) << m.log2PageSize
	seq, kern := a.writeSeq, m.kernel
	start := now // the data may be read at the home from here on
	a.cmigBusy = true
	a.cmigHolders |= 1 << t // attempted: no second pull by t this kernel
	m.AStats.CopyBytes += ps
	m.gpms[t].Engine.enqueue(now, &chunk{
		src:      m.gpms[home].Engine,
		srcPAddr: p.PAddr,
		dstPAddr: p.PAddr,
		size:     ps,
		install: func(now sim.VTimeInSec, data []byte) {
			a.cmigBusy = false
			if a.writeSeq != seq || m.kernel != kern {
				m.AStats.CmigAborts++
				return
			}
			line := m.cfg.LineBytes
			n := 0
			for off := uint64(0); off < ps; off += line {
				// sent at the pull's start: a pull opened before the
				// requester's kernel boundary is dropped like a stale fill
				if m.gpms[t].InstallRemote(start, p.PID, p.PAddr+off,
					append([]byte(nil), data[off:off+line]...)) {
					n++
				} else {
					m.AStats.CmigDropped++
				}
			}
			m.AStats.CmigLines += uint64(n)
			if n > 0 {
				m.AStats.CmigPages++
			}
		},
	})
}

// r3CmigWrite: a write to a page with cmig holders sends them an
// invalidation message each (the M3 kernel-boundary rules keep the data
// coherent; the messages are the traffic of the holder table).
func (m *Manager) r3CmigWrite(now sim.VTimeInSec, a *adaptState, home uint64) {
	for d, g := range m.gpms {
		if g == nil || a.cmigHolders&(1<<uint(d)) == 0 {
			continue
		}
		if src := m.gpms[home]; src != nil && src.Engine != nil && g.Engine != nil {
			src.Engine.sendInval(now, g.Engine)
			m.AStats.CmigInvals++
		}
	}
	a.cmigHolders = 0
}
