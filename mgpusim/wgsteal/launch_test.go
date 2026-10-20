package wgsteal

import (
	"math/rand"
	"testing"

	"github.com/sarchlab/akita/v3/sim"
	"github.com/sarchlab/mgpusim/v3/kernels"
)

func meshGPMs() []GPMInfo {
	var g []GPMInfo
	for y := 0; y < 7; y++ {
		for x := 0; x < 7; x++ {
			if x == 3 && y == 3 {
				continue
			}
			g = append(g, GPMInfo{X: x, Y: y, NumCU: 28})
		}
	}
	return g
}

func TestCutTail(t *testing.T) {
	q := []WGRange{{0, 5}, {10, 12}, {20, 24}}
	// queue order: 0..4, 10, 11, 20..23 (11 WGs)
	keep, taken := cutTail(q, 0, 3)
	if len(taken) != 1 || taken[0] != (WGRange{21, 24}) {
		t.Fatalf("tail chunk %v", taken)
	}
	if n := count(keep); n != 8 {
		t.Fatalf("kept %d", n)
	}
	keep, taken = cutTail(q, 3, 4) // skips 21..23, takes 10, 11, 20 and 4
	want := []WGRange{{4, 5}, {10, 12}, {20, 21}}
	if len(taken) != 3 || taken[0] != want[0] || taken[1] != want[1] || taken[2] != want[2] {
		t.Fatalf("middle chunk %v", taken)
	}
	if len(keep) != 2 || keep[0] != (WGRange{0, 4}) || keep[1] != (WGRange{21, 24}) {
		t.Fatalf("keep %v", keep)
	}
}

func count(q []WGRange) int {
	n := 0
	for _, r := range q {
		n += r.Hi - r.Lo
	}
	return n
}

// TestStealBookkeeping drives random launches through the Launch API the
// way the dispatchers and CPs do (pop, plan, issue, serve, receive, done),
// with messages delivered after random delays, and checks that every WG
// is dispatched exactly once and that every GPM can finish.
func TestStealBookkeeping(t *testing.T) {
	for _, filter := range []bool{false, true} {
		var ok, nack, stolen, far, byFilter uint64
		for seed := int64(1); seed <= 20; seed++ {
			tot := runRandomLaunch(t, seed, filter)
			ok += tot.StealsOK
			nack += tot.StealsNack
			stolen += tot.WGsStolen
			far += tot.FarSteals
			byFilter += tot.FilterDecided
		}
		t.Logf("filter=%v: steals ok %d nack %d, WGs stolen %d, far %d, filter-decided %d",
			filter, ok, nack, stolen, far, byFilter)
		if ok == 0 || stolen == 0 {
			t.Fatalf("no steals happened")
		}
		if filter && byFilter == 0 {
			t.Fatalf("filter never decided a steal")
		}
	}
}

type pendingMsg struct {
	at       float64
	serve    bool // false: reply to the thief
	thief    int
	victim   int
	plan     Plan
	issue    float64
	serveAt  float64
	ranges   []WGRange
	remainAt int
}

func runRandomLaunch(t *testing.T, seed int64, filter bool) Totals {
	rng := rand.New(rand.NewSource(seed))
	cfg := DefaultConfig()
	cfg.ChunkWGs = 1 + rng.Intn(8)
	cfg.RadiusHops = rng.Intn(3)
	cfg.Fallback = rng.Intn(2) == 0
	cfg.LowWatermark = rng.Intn(3)
	cfg.Filter = filter
	cfg.CandidatesPerVictim = 1 + rng.Intn(3)
	if rng.Intn(2) == 0 {
		cfg.InitialSplit = "balanced"
	}
	c, err := NewCoordinator(cfg, meshGPMs())
	if err != nil {
		t.Fatal(err)
	}
	n := len(c.gpms)
	members := make([]int, n)
	numCUs := make([]int, n)
	for i := range members {
		members[i] = i
		numCUs[i] = 28
	}
	numWG := 1 + rng.Intn(3000)
	dist := Split(cfg.InitialSplit, numWG, numCUs)
	l := c.NewLaunch("k", members, dist, nil, 0)

	now := 0.0
	inflight := make([][]*kernels.WorkGroup, n)
	var msgs []pendingMsg
	finished := make([]bool, n)
	noVictimTill := make([]float64, n)
	left := n
	for step := 0; left > 0; step++ {
		if step > 5_000_000 {
			t.Fatalf("seed %d: no progress", seed)
		}
		now += 1 + float64(rng.Intn(20))
		ts := sim.VTimeInSec(now * 1e-9)
		// deliver due messages
		kept := msgs[:0]
		var due []pendingMsg
		for _, m := range msgs {
			if m.at <= now {
				due = append(due, m)
			} else {
				kept = append(kept, m)
			}
		}
		msgs = kept
		for _, m := range due {
			if m.serve {
				ranges, rem := l.Serve(m.victim, m.thief, m.plan.Cand, ts)
				m.serve = false
				m.ranges, m.remainAt, m.serveAt = ranges, rem, now
				m.at = now + float64(10+rng.Intn(300))
				msgs = append(msgs, m)
			} else {
				l.Receive(m.thief, m.victim, m.ranges, m.remainAt,
					sim.VTimeInSec(m.issue*1e-9), sim.VTimeInSec(m.serveAt*1e-9), ts, m.plan)
			}
		}
		g := rng.Intn(n)
		if finished[g] {
			continue
		}
		// complete a random in-flight WG
		if len(inflight[g]) > 0 && rng.Intn(3) == 0 {
			i := rng.Intn(len(inflight[g]))
			c.WGDone(l, inflight[g][i])
			inflight[g] = append(inflight[g][:i], inflight[g][i+1:]...)
		}
		// dispatch up to a few WGs if "CUs" have room
		for k := 0; k < 4 && len(inflight[g]) < 40; k++ {
			flat, ok := l.Pop(g, ts)
			if !ok {
				break
			}
			wg := &kernels.WorkGroup{}
			c.RegisterWG(wg, l, flat)
			if filter {
				c.OnRead(g, wg, uint64(flat/4)<<12)
			}
			inflight[g] = append(inflight[g], wg)
		}
		// steal
		noVictim := false
		if l.WantsToSteal(g) {
			if now >= noVictimTill[g] {
				p, ok := l.PlanSteal(g, ts)
				if ok {
					l.IssueSteal(g, p)
					msgs = append(msgs, pendingMsg{
						at: now + float64(10+rng.Intn(300)), serve: true,
						thief: g, victim: p.Victim, plan: p, issue: now,
					})
				} else {
					noVictimTill[g] = now + cfg.AdvPeriodNS
					noVictim = true
				}
			} else {
				noVictim = true
			}
		}
		if noVictim && len(inflight[g]) == 0 && !l.Pending(g) && l.Remaining(g) == 0 {
			l.Done(g, ts)
			finished[g] = true
			left--
		}
	}
	if !l.Finalized() {
		t.Fatalf("seed %d: launch not finalized", seed)
	}
	tot := c.Totals()
	if tot.WGsCompleted != uint64(numWG) {
		t.Fatalf("seed %d: %d WGs completed of %d", seed, tot.WGsCompleted, numWG)
	}
	exec := 0
	for _, r := range c.Rows() {
		exec += r.Executed
	}
	if exec != numWG {
		t.Fatalf("seed %d: rows executed %d of %d", seed, exec, numWG)
	}
	return tot
}

func TestSplitStaticMatchesDriver(t *testing.T) {
	numCUs := make([]int, 48)
	for i := range numCUs {
		numCUs[i] = 28
	}
	d := Split("static", 56, numCUs)
	if d[1] != 28 || d[2] != 56 || d[3] != 56 || d[48] != 56 {
		t.Fatalf("static split %v", d[:4])
	}
	d = Split("static", 1344*3+5, numCUs)
	if d[1] != 28*4 {
		t.Fatalf("static split per GPM %d", d[1])
	}
	d = Split("balanced", 100, numCUs)
	if d[1] != 3 || d[4] != 12 || d[5] != 14 || d[48] != 100 {
		t.Fatalf("balanced split %v", d[:6])
	}
}

// Footprints are kept only for the stealable tail of each static range,
// and a later launch can score a tail chunk against the thief's pages.
func TestGroupFootprintTailOnly(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Filter = true
	cfg.ChunkWGs = 4
	cfg.GroupTablePerGPM = 2 // tail region = 2 groups x 4 WGs = 8 WGs
	c, err := NewCoordinator(cfg, meshGPMs()[:2])
	if err != nil {
		t.Fatal(err)
	}
	members := []int{0, 1}
	dist := []int{0, 64, 128}
	for launch := 0; launch < 2; launch++ {
		l := c.NewLaunch("k", members, dist, nil, sim.VTimeInSec(launch))
		for g := 0; g < 2; g++ {
			for {
				flat, ok := l.Pop(g, sim.VTimeInSec(launch))
				if !ok {
					break
				}
				wg := &kernels.WorkGroup{}
				c.RegisterWG(wg, l, flat)
				c.OnRead(g, wg, uint64(1000+flat)<<12) // one page per WG
				c.WGDone(l, wg)
			}
			l.Done(g, sim.VTimeInSec(launch))
		}
	}
	kh := c.hist["k"]
	if len(kh.groups) != 4 {
		t.Fatalf("%d groups tracked, want 4 (2 tail groups per GPM)", len(kh.groups))
	}
	for id := range kh.groups {
		if id != 14 && id != 15 && id != 30 && id != 31 {
			t.Fatalf("group %d is not a tail group", id)
		}
	}
	tot := c.Totals()
	if tot.GroupsRecorded != 4 || tot.GroupsKept != 4 || tot.GroupsReplaced != 0 {
		t.Fatalf("recorded %d kept %d replaced %d", tot.GroupsRecorded, tot.GroupsKept, tot.GroupsReplaced)
	}
	// GPM 0 touched pages of WGs 120..127 (GPM 1's tail) -> its tail chunk
	// scores 1 for GPM 0.
	l := c.NewLaunch("k", members, dist, nil, 10)
	for i := 0; i < 64; i++ {
		l.Pop(0, 10) // empty GPM 0's own queue
	}
	for f := 120; f < 128; f++ {
		c.rpf[0].Touch(uint64(1000 + f))
	}
	x := cand{v: 1, c: 0}
	probe := 0
	c.mu.Lock()
	l.scoreCand(0, &x, &probe)
	c.mu.Unlock()
	if !x.hasScore || x.score != 1 || x.exact != 1 || probe == 0 {
		t.Fatalf("tail chunk score %+v probe %d", x, probe)
	}
}
