package wgsteal

import (
	"fmt"
	"math"
	"sort"

	"github.com/sarchlab/akita/v3/sim"
)

// WGRange is a half-open range of flattened WG IDs.
type WGRange struct{ Lo, Hi int }

type logEnt struct {
	t float64 // ns
	v int32
}

type knownEnt struct {
	valid bool
	t     float64
	v     int
}

type slotStats struct {
	executed, stolenIn, stolenOut int
	stealReqs, stealsOK, nack     int
	nearOK, farOK                 int
	latSum, latMax                float64
	hopsSum                       int
	firstPop, finish              float64
	popped                        bool
}

// Launch is the steal state of one unified kernel launch. Slots are the
// members of the unified device in order; GPM indices are global.
type Launch struct {
	c       *Coordinator
	ID      int
	Name    string
	numWG   int
	members []int
	slotOf  []int // GPM index -> slot, -1 if not a member
	static  []int // per-slot boundaries
	groupFn GroupFunc
	startNS float64

	queues  [][]WGRange
	remain  []int
	logs    [][]logEnt
	pending []bool
	known   [][]knownEnt

	dispatched  []uint64
	nDispatched int
	nCompleted  int
	done        []bool
	nDone       int
	finalized   bool

	spanLo, spanHi []int // executed flat range per slot (-1 = none)
	lastGroup      []int

	nearCount, farCount []int
	advEpochNear        []int64
	advEpochFar         []int64

	touched []*groupFP
	st      []slotStats
}

func newLaunch(c *Coordinator, id int, name string, members []int, dist []int,
	groupFn GroupFunc, startNS float64) *Launch {
	n := len(members)
	if len(dist) != n+1 {
		panic("wgsteal: launch boundaries do not match the members")
	}
	l := &Launch{
		c: c, ID: id, Name: name, numWG: dist[n], members: members,
		static: dist, groupFn: groupFn, startNS: startNS,
		slotOf:       make([]int, len(c.gpms)),
		queues:       make([][]WGRange, n),
		remain:       make([]int, n),
		logs:         make([][]logEnt, n),
		pending:      make([]bool, n),
		known:        make([][]knownEnt, n),
		dispatched:   make([]uint64, (dist[n]+63)/64),
		done:         make([]bool, n),
		spanLo:       make([]int, n),
		spanHi:       make([]int, n),
		lastGroup:    make([]int, n),
		nearCount:    make([]int, n),
		farCount:     make([]int, n),
		advEpochNear: make([]int64, n),
		advEpochFar:  make([]int64, n),
		st:           make([]slotStats, n),
	}
	for i := range l.slotOf {
		l.slotOf[i] = -1
	}
	for s, g := range members {
		l.slotOf[g] = s
		if dist[s+1] > dist[s] {
			l.queues[s] = []WGRange{{dist[s], dist[s+1]}}
		}
		l.remain[s] = dist[s+1] - dist[s]
		l.known[s] = make([]knownEnt, n)
		l.spanLo[s], l.spanHi[s] = -1, -1
		l.lastGroup[s] = math.MinInt32
		l.advEpochNear[s], l.advEpochFar[s] = -1, -1
	}
	for s, g := range members {
		for _, h := range members {
			if h == g {
				continue
			}
			if c.hops[g][h] <= c.cfg.RadiusHops {
				l.nearCount[s]++
			} else {
				l.farCount[s]++
			}
		}
	}
	return l
}

// NumWG returns the number of WGs of the kernel.
func (l *Launch) NumWG() int { return l.numWG }

func (l *Launch) slot(gpm int) int {
	s := l.slotOf[gpm]
	if s < 0 {
		panic(fmt.Sprintf("wgsteal: GPM %d is not a member of launch %d", gpm, l.ID))
	}
	return s
}

func (l *Launch) staticSlotOf(flat int) int {
	// first boundary > flat
	s := sort.SearchInts(l.static, flat+1) - 1
	if s < 0 {
		s = 0
	}
	for s+1 < len(l.static) && l.static[s+1] <= flat {
		s++
	}
	return s
}

func (l *Launch) groupOf(flat int) int {
	if l.groupFn != nil {
		return l.groupFn(flat)
	}
	return flat / l.c.cfg.GroupWGs
}

func nsOf(t sim.VTimeInSec) float64 { return float64(t) * 1e9 }

// record logs a change of a slot's queued-WG count and counts the
// advertisements the change triggers (one per advertisement period).
func (l *Launch) record(s int, now float64) {
	cfg := &l.c.cfg
	lg := l.logs[s]
	lg = append(lg, logEnt{now, int32(l.remain[s])})
	if len(lg) > 4096 {
		horizon := now - (cfg.AdvFixedNS + 20*cfg.AdvHopNS + 2*cfg.FarAdvPeriodNS + 2000)
		cut := sort.Search(len(lg), func(i int) bool { return lg[i].t >= horizon })
		if cut > 1 {
			lg = append(lg[:0:0], lg[cut-1:]...)
		}
	}
	l.logs[s] = lg

	e := int64(math.Floor(now / cfg.AdvPeriodNS))
	if e != l.advEpochNear[s] {
		l.advEpochNear[s] = e
		l.c.totals.AdvNearMsgs += uint64(l.nearCount[s])
	}
	if cfg.Fallback {
		e = int64(math.Floor(now / cfg.FarAdvPeriodNS))
		if e != l.advEpochFar[s] {
			l.advEpochFar[s] = e
			l.c.totals.AdvFarMsgs += uint64(l.farCount[s])
		}
	}
}

func (l *Launch) valueAt(s int, t float64) int {
	lg := l.logs[s]
	i := sort.Search(len(lg), func(i int) bool { return lg[i].t > t })
	if i == 0 {
		return l.static[s+1] - l.static[s]
	}
	return int(lg[i-1].v)
}

// view is thief slot s's belief of victim slot v's queued-WG count.
func (l *Launch) view(s, v int, now float64) int {
	cfg := &l.c.cfg
	h := l.c.hops[l.members[s]][l.members[v]]
	p := cfg.FarAdvPeriodNS
	if h <= cfg.RadiusHops {
		p = cfg.AdvPeriodNS
	}
	lat := cfg.AdvFixedNS + float64(h)*cfg.AdvHopNS
	ta := math.Floor((now-lat)/p) * p
	val := l.static[v+1] - l.static[v]
	if ta >= l.startNS {
		val = l.valueAt(v, ta)
	}
	if k := l.known[s][v]; k.valid && k.t > ta {
		val = k.v
	}
	return val
}

// Remaining returns the number of WGs queued at a GPM.
func (l *Launch) Remaining(gpm int) int {
	l.c.mu.Lock()
	defer l.c.mu.Unlock()
	return l.remain[l.slot(gpm)]
}

// Pending reports whether a GPM waits for a steal reply.
func (l *Launch) Pending(gpm int) bool {
	l.c.mu.Lock()
	defer l.c.mu.Unlock()
	return l.pending[l.slot(gpm)]
}

// WantsToSteal reports whether a GPM should look for a victim.
func (l *Launch) WantsToSteal(gpm int) bool {
	l.c.mu.Lock()
	defer l.c.mu.Unlock()
	s := l.slot(gpm)
	return !l.pending[s] && l.remain[s] <= l.c.cfg.LowWatermark
}

// Pop takes the next WG from the head of a GPM's queue.
func (l *Launch) Pop(gpm int, now sim.VTimeInSec) (int, bool) {
	l.c.mu.Lock()
	defer l.c.mu.Unlock()
	s := l.slot(gpm)
	q := l.queues[s]
	if len(q) == 0 {
		return 0, false
	}
	flat := q[0].Lo
	q[0].Lo++
	if q[0].Lo == q[0].Hi {
		q = q[1:]
	}
	l.queues[s] = q
	l.remain[s]--
	if l.dispatched[flat/64]&(1<<(flat%64)) != 0 {
		panic(fmt.Sprintf("wgsteal: WG %d of launch %d dispatched twice", flat, l.ID))
	}
	l.dispatched[flat/64] |= 1 << (flat % 64)
	l.nDispatched++
	t := nsOf(now)
	st := &l.st[s]
	st.executed++
	if !st.popped {
		st.popped = true
		st.firstPop = t
	}
	if l.spanLo[s] < 0 || flat < l.spanLo[s] {
		l.spanLo[s] = flat
	}
	if flat+1 > l.spanHi[s] {
		l.spanHi[s] = flat + 1
	}
	l.lastGroup[s] = l.groupOf(flat)
	l.record(s, t)
	return flat, true
}

// cutTail removes the n WGs that come right before the last `skip` WGs of
// the queue (queue order) and returns the new queue and the removed WGs.
func cutTail(q []WGRange, skip, n int) (keep, taken []WGRange) {
	var keepRev, takenRev []WGRange
	pos := 0
	for i := len(q) - 1; i >= 0; i-- {
		r := q[i]
		size := r.Hi - r.Lo
		a, b := pos, pos+size
		if a < skip {
			a = skip
		}
		if b > skip+n {
			b = skip + n
		}
		if a >= b {
			keepRev = append(keepRev, r)
		} else {
			tlo, thi := r.Hi-(b-pos), r.Hi-(a-pos)
			if thi < r.Hi {
				keepRev = append(keepRev, WGRange{thi, r.Hi})
			}
			takenRev = append(takenRev, WGRange{tlo, thi})
			if r.Lo < tlo {
				keepRev = append(keepRev, WGRange{r.Lo, tlo})
			}
		}
		pos += size
	}
	for i := len(keepRev) - 1; i >= 0; i-- {
		keep = append(keep, keepRev[i])
	}
	for i := len(takenRev) - 1; i >= 0; i-- {
		taken = append(taken, takenRev[i])
	}
	return keep, taken
}

// chunkEnds returns the first and last WG of candidate chunk c of slot v
// (by the current queue), or ok = false if the queue is too short.
func (l *Launch) chunkEnds(v, c int) (first, last int, ok bool) {
	k := l.c.cfg.ChunkWGs
	if l.remain[v] < (c+1)*k {
		return 0, 0, false
	}
	_, taken := cutTail(l.queues[v], c*k, k)
	if len(taken) == 0 {
		return 0, 0, false
	}
	return taken[0].Lo, taken[len(taken)-1].Hi - 1, true
}

// Plan is a thief's choice.
type Plan struct {
	Victim       int // GPM index
	Cand         int
	Hops         int
	Near         bool
	HasScore     bool
	Score        float64 // filter overlap (fraction of the group footprint)
	Exact        float64 // true overlap (simulator-only)
	Reason       string  // "distance", "filter", "affinity"
	ProbeEntries int
}

type cand struct {
	v, c, hops, rem int
	near            bool
	hasScore        bool
	score, exact    float64
	sameGroup       bool
	idxDist         int
}

// PlanSteal picks a victim for GPM gpm. ok is false when no GPM looks
// eligible.
func (l *Launch) PlanSteal(gpm int, nowT sim.VTimeInSec) (Plan, bool) {
	l.c.mu.Lock()
	defer l.c.mu.Unlock()
	cfg := &l.c.cfg
	s := l.slot(gpm)
	now := nsOf(nowT)
	var near, far []cand
	for v := range l.members {
		if v == s || l.done[v] {
			continue
		}
		h := l.c.hops[gpm][l.members[v]]
		isNear := h <= cfg.RadiusHops
		if !isNear && !cfg.Fallback {
			continue
		}
		rem := l.view(s, v, now)
		if rem < cfg.MinVictimWGs {
			continue
		}
		x := cand{v: v, hops: h, rem: rem, near: isNear}
		if isNear {
			near = append(near, x)
		} else {
			far = append(far, x)
		}
	}
	if len(near) == 0 && len(far) == 0 {
		return Plan{}, false
	}

	probe := 0
	var best cand
	reason := "distance"
	if len(near) > 0 && cfg.Filter {
		var all []cand
		for _, x := range near {
			for c := 0; c < cfg.CandidatesPerVictim; c++ {
				if c > 0 && x.rem < (c+2)*cfg.ChunkWGs {
					break
				}
				y := x
				y.c = c
				l.scoreCand(s, &y, &probe)
				all = append(all, y)
			}
		}
		best, reason = l.pickFiltered(all)
	} else {
		set := near
		if len(set) == 0 {
			set = far
		}
		sort.SliceStable(set, func(i, j int) bool { return lessDistance(set[i], set[j]) })
		best = set[0]
	}
	return Plan{
		Victim: l.members[best.v], Cand: best.c, Hops: best.hops, Near: best.near,
		HasScore: best.hasScore, Score: best.score, Exact: best.exact,
		Reason: reason, ProbeEntries: probe,
	}, true
}

func lessDistance(a, b cand) bool {
	if a.hops != b.hops {
		return a.hops < b.hops
	}
	if a.rem != b.rem {
		return a.rem > b.rem
	}
	if a.v != b.v {
		return a.v < b.v
	}
	return a.c < b.c
}

func (l *Launch) scoreCand(s int, x *cand, probe *int) {
	first, last, ok := l.chunkEnds(x.v, x.c)
	if !ok {
		x.idxDist = math.MaxInt32
		return
	}
	g1, g2 := l.groupOf(first), l.groupOf(last)
	x.sameGroup = g1 == l.lastGroup[s] || g2 == l.lastGroup[s]
	x.idxDist = math.MaxInt32
	if l.spanLo[s] >= 0 {
		d := 0
		if last < l.spanLo[s] {
			d = l.spanLo[s] - last
		} else if first >= l.spanHi[s] {
			d = first - l.spanHi[s] + 1
		}
		x.idxDist = d
	}
	rpf := l.c.rpf[l.members[s]]
	groups := []int{g1}
	if g2 != g1 {
		groups = append(groups, g2)
	}
	n := 0
	for _, g := range groups {
		fp := l.c.groupFootprint(l, g, first, false)
		if fp == nil || fp.hist.Count() == 0 {
			continue
		}
		items, hits, exact := fp.hist.ProbeInto(rpf.F)
		*probe += items
		x.score += float64(hits) / float64(items)
		x.exact += float64(exact) / float64(items)
		n++
	}
	if n > 0 {
		x.hasScore = true
		x.score /= float64(n)
		x.exact /= float64(n)
	}
}

// pickFiltered ranks near candidates: best overlap score (ties within
// ScoreEps), then same group as the thief's last WG, then WG-index
// distance to the thief's executed range, then hops, remaining, index.
func (l *Launch) pickFiltered(all []cand) (cand, string) {
	eps := l.c.cfg.ScoreEps
	bestScore := -1.0
	for _, x := range all {
		if x.hasScore && x.score > bestScore {
			bestScore = x.score
		}
	}
	reason := "affinity"
	pool := all
	if bestScore > 0 {
		reason = "filter"
		pool = nil
		for _, x := range all {
			if x.hasScore && x.score >= bestScore-eps {
				pool = append(pool, x)
			}
		}
	}
	sort.SliceStable(pool, func(i, j int) bool {
		a, b := pool[i], pool[j]
		if a.sameGroup != b.sameGroup {
			return a.sameGroup
		}
		if a.idxDist != b.idxDist {
			return a.idxDist < b.idxDist
		}
		return lessDistance(a, b)
	})
	if reason == "affinity" && !pool[0].sameGroup && pool[0].idxDist == math.MaxInt32 {
		reason = "distance"
	}
	return pool[0], reason
}

// IssueSteal marks a GPM as waiting for a reply and counts the request.
func (l *Launch) IssueSteal(gpm int, p Plan) {
	l.c.mu.Lock()
	defer l.c.mu.Unlock()
	s := l.slot(gpm)
	if l.pending[s] {
		panic("wgsteal: second steal request in flight")
	}
	l.pending[s] = true
	l.st[s].stealReqs++
	l.c.totals.StealReqs++
	l.c.totals.ProbeEntries += uint64(p.ProbeEntries)
}

// Serve cuts a chunk for a thief at the victim's CP. It returns the WGs
// (empty = NACK) and the victim's queued count after the cut.
func (l *Launch) Serve(victim, thief, candIdx int, nowT sim.VTimeInSec) ([]WGRange, int) {
	l.c.mu.Lock()
	defer l.c.mu.Unlock()
	cfg := &l.c.cfg
	v := l.slot(victim)
	r := l.remain[v]
	if r < cfg.MinVictimWGs {
		return nil, r
	}
	k := cfg.ChunkWGs
	if r < (candIdx+2)*k {
		candIdx = 0
	}
	keep, taken := cutTail(l.queues[v], candIdx*k, k)
	n := 0
	for _, x := range taken {
		n += x.Hi - x.Lo
	}
	l.queues[v] = keep
	l.remain[v] -= n
	l.st[v].stolenOut += n
	l.record(v, nsOf(nowT))
	return taken, l.remain[v]
}

// Receive gives a reply to the thief: the WGs join its queue.
func (l *Launch) Receive(thief, victim int, ranges []WGRange, victimRemain int,
	issue, serve, nowT sim.VTimeInSec, p Plan) {
	l.c.mu.Lock()
	defer l.c.mu.Unlock()
	s := l.slot(thief)
	v := l.slot(victim)
	if !l.pending[s] {
		panic("wgsteal: steal reply without a request")
	}
	l.pending[s] = false
	l.known[s][v] = knownEnt{valid: true, t: nsOf(serve), v: victimRemain}
	st := &l.st[s]
	t := &l.c.totals
	if len(ranges) == 0 {
		st.nack++
		t.StealsNack++
		return
	}
	n := 0
	for _, r := range ranges {
		n += r.Hi - r.Lo
	}
	l.queues[s] = append(l.queues[s], ranges...)
	l.remain[s] += n
	l.record(s, nsOf(nowT))
	lat := nsOf(nowT) - nsOf(issue)
	st.stolenIn += n
	st.stealsOK++
	st.latSum += lat
	st.latMax = math.Max(st.latMax, lat)
	st.hopsSum += p.Hops
	t.StealsOK++
	t.WGsStolen += uint64(n)
	t.LatSumNS += lat
	t.LatMaxNS = math.Max(t.LatMaxNS, lat)
	t.HopsSum += uint64(p.Hops)
	if p.Near {
		st.nearOK++
		t.NearSteals++
	} else {
		st.farOK++
		t.FarSteals++
	}
	switch p.Reason {
	case "filter":
		t.FilterDecided++
	case "affinity":
		t.AffinityDecided++
	}
	if p.HasScore {
		t.ScoreSum += p.Score
		t.ExactSum += p.Exact
		t.ScoreCount++
	}
}

// Done records that a GPM's dispatcher finished the kernel. When every
// member is done, the launch checks that every WG ran exactly once and
// updates the group histories.
func (l *Launch) Done(gpm int, nowT sim.VTimeInSec) {
	l.c.mu.Lock()
	defer l.c.mu.Unlock()
	s := l.slot(gpm)
	if l.done[s] {
		panic("wgsteal: GPM finished a launch twice")
	}
	if l.pending[s] || l.remain[s] != 0 {
		panic("wgsteal: GPM finished with queued or requested WGs")
	}
	l.done[s] = true
	l.st[s].finish = nsOf(nowT)
	l.nDone++
	if l.nDone == len(l.members) {
		l.finalize()
	}
}

func (l *Launch) finalize() {
	if l.finalized {
		return
	}
	l.finalized = true
	if l.nDispatched != l.numWG || l.nCompleted != l.numWG {
		panic(fmt.Sprintf("wgsteal: launch %d (%s): %d WGs, %d dispatched, %d completed",
			l.ID, l.Name, l.numWG, l.nDispatched, l.nCompleted))
	}
	for i, w := range l.dispatched {
		want := ^uint64(0)
		if (i+1)*64 > l.numWG {
			want = (1 << uint(l.numWG-i*64)) - 1
		}
		if w != want {
			panic(fmt.Sprintf("wgsteal: launch %d: WG bitmap word %d is %x", l.ID, i, w))
		}
	}
	if l.c.cfg.Filter {
		l.c.finishHistory(l)
	}
	for s, g := range l.members {
		st := l.st[s]
		l.c.rows = append(l.c.rows, LaunchRow{
			Launch: l.ID, GPM: g, Name: l.Name,
			StaticWGs: l.static[s+1] - l.static[s], Executed: st.executed,
			StolenIn: st.stolenIn, StolenOut: st.stolenOut,
			StealReqs: st.stealReqs, StealsOK: st.stealsOK, StealsNack: st.nack,
			NearOK: st.nearOK, FarOK: st.farOK,
			LatSumNS: st.latSum, LatMaxNS: st.latMax, HopsSum: st.hopsSum,
			StartNS: l.startNS, FirstPopNS: st.firstPop, FinishNS: st.finish,
		})
	}
	l.logs = nil
	l.known = nil
}

// Finalized reports whether every member finished and the checks passed.
func (l *Launch) Finalized() bool {
	l.c.mu.Lock()
	defer l.c.mu.Unlock()
	return l.finalized
}
