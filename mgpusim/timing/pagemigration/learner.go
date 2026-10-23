package pagemigration

// Per-buffer rule learner (P21 predict). An observation is (page index i in
// the buffer, target device index t in 0..47, owner device index o). Four
// rules are fitted on the last learnHist observations:
//
//	const  t = target of the nearest observed index (adjacent pages same)
//	owner  t = (o + delta) mod 48
//	step   t = (c + s*i) mod 48, integer s
//	lin    t = (c + floor(i*sigma + phi)) mod 48, real sigma (a linear split
//	       with a non-integer number of pages per device that wraps around
//	       the 48 devices: grid-stride kernels over a contiguously placed
//	       buffer)
//
// Each rule predicts every new observation before it is added; a hit extends
// the rule's streak, a miss resets it and refits. The buffer trusts the first
// rule (owner, const, step, lin) with a streak of at least m; a miss of the
// trusted rule withdraws the trust until a rule reaches m hits again.
// (Same algorithm as results_index/adapt/scripts/offline_pattern.py, online
// variant with fallback.)

import (
	"math"
	"sort"
)

const (
	learnHist  = 32
	learnLimit = 512 // observations without trust after which a buffer is left alone
	numDev     = 48
)

const (
	ruleOwner = iota
	ruleConst
	ruleStep
	ruleLin
	numRules
)

type obs struct{ i, t, o int }

type linParams struct {
	sigma, phi float64
	c          int
}

type learner struct {
	m      int
	hist   []obs
	streak [numRules]int
	valid  [numRules]bool
	delta  int
	stepS  int
	stepC  int
	lin    linParams
	conf   int // trusted rule, -1 none
	n      int
}

func newLearner(m int) *learner {
	return &learner{m: m, conf: -1}
}

func mod48(x int) int {
	x %= numDev
	if x < 0 {
		x += numDev
	}
	return x
}

// predict returns the rule's target for index i (owner o), or -1.
func (l *learner) predictRule(r, i, o int) int {
	if !l.valid[r] {
		return -1
	}
	switch r {
	case ruleOwner:
		return mod48(o + l.delta)
	case ruleConst:
		best, bd := -1, math.MaxInt32
		for _, h := range l.hist {
			d := h.i - i
			if d < 0 {
				d = -d
			}
			if d < bd {
				best, bd = h.t, d
			}
		}
		return best
	case ruleStep:
		return mod48(l.stepC + l.stepS*i)
	case ruleLin:
		return mod48(l.lin.c + int(math.Floor(float64(i)*l.lin.sigma+l.lin.phi+1e-9)))
	}
	return -1
}

// predict uses the trusted rule.
func (l *learner) predict(i, o int) int {
	if l.conf < 0 {
		return -1
	}
	return l.predictRule(l.conf, i, o)
}

// add records an observation; it returns whether the trusted rule (or, with
// none trusted, any rule) predicted it.
func (l *learner) add(i, t, o int) bool {
	if l.conf < 0 && l.n >= learnLimit {
		return false
	}
	l.n++
	hitTrusted := l.conf >= 0 && l.predictRule(l.conf, i, o) == t
	anyHit := false
	pred := [numRules]int{}
	for r := 0; r < numRules; r++ {
		pred[r] = l.predictRule(r, i, o)
	}
	l.hist = append(l.hist, obs{i, t, o})
	if len(l.hist) > learnHist {
		l.hist = l.hist[1:]
	}
	for r := 0; r < numRules; r++ {
		if pred[r] == t {
			l.streak[r]++
			anyHit = true
			if r == ruleConst {
				l.valid[r] = true
			}
		} else {
			l.streak[r] = 0
			l.fit(r)
		}
	}
	if l.conf >= 0 && !hitTrusted {
		l.conf = -1
	}
	if l.conf < 0 {
		for _, r := range []int{ruleOwner, ruleConst, ruleStep, ruleLin} {
			if l.streak[r] >= l.m {
				l.conf = r
				break
			}
		}
	}
	if l.conf >= 0 {
		return hitTrusted || anyHit
	}
	return anyHit
}

func (l *learner) fit(r int) {
	h := l.hist
	switch r {
	case ruleConst:
		l.valid[r] = len(h) > 0
	case ruleOwner:
		var cnt [numDev]int
		for _, x := range h {
			cnt[mod48(x.t-x.o)]++
		}
		l.delta = argmax48(cnt[:])
		l.valid[r] = len(h) > 0
	case ruleStep:
		if len(h) < 2 {
			l.valid[r] = false
			return
		}
		bestH := -1
		for s := 0; s < numDev; s++ {
			var cnt [numDev]int
			for _, x := range h {
				cnt[mod48(x.t-s*x.i)]++
			}
			c := argmax48(cnt[:])
			if cnt[c] > bestH {
				bestH, l.stepS, l.stepC = cnt[c], s, c
			}
		}
		l.valid[r] = true
	case ruleLin:
		p, ok := fitLin(h)
		l.lin, l.valid[r] = p, ok
	}
}

func argmax48(c []int) int {
	b := 0
	for i := range c {
		if c[i] > c[b] {
			b = i
		}
	}
	return b
}

// fitLin: candidate slopes from observation pairs 1-3 apart in index order
// (device difference wrapped to (-24, 24], +-48 alternatives for adjacent
// pairs more than one index apart), each also snapped to n = p/q pages per
// device (q <= 4); phase in 16 steps; offset by the most common residue.
func fitLin(h []obs) (linParams, bool) {
	if len(h) < 2 {
		return linParams{}, false
	}
	s := append([]obs(nil), h...)
	sort.SliceStable(s, func(a, b int) bool { return s[a].i < s[b].i })
	cands := map[float64]bool{}
	add := func(sg float64) {
		cands[math.Round(sg*1e9)/1e9] = true
		if math.Abs(sg) > 1e-9 {
			n := 1 / sg
			for q := 1.0; q <= 4; q++ {
				r := math.Round(n*q) / q
				if r != 0 {
					cands[math.Round(1e9/r)/1e9] = true
				}
			}
		}
	}
	for gap := 1; gap <= 3; gap++ {
		for k := 0; k+gap < len(s); k++ {
			di := s[k+gap].i - s[k].i
			if di <= 0 {
				continue
			}
			dd := mod48(s[k+gap].t - s[k].t)
			if dd > 24 {
				dd -= 48
			}
			add(float64(dd) / float64(di))
			if gap == 1 && di > 1 {
				add(float64(dd-48) / float64(di))
				add(float64(dd+48) / float64(di))
			}
		}
	}
	if len(cands) == 0 {
		return linParams{}, false
	}
	keys := make([]float64, 0, len(cands))
	for k := range cands {
		keys = append(keys, k)
	}
	sort.Float64s(keys)
	best, bestH := linParams{}, -1
	for _, sg := range keys {
		for ph := 0; ph < 16; ph++ {
			phi := float64(ph) / 16
			var cnt [numDev]int
			for _, x := range s {
				cnt[mod48(x.t-int(math.Floor(float64(x.i)*sg+phi+1e-9)))]++
			}
			c := argmax48(cnt[:])
			if cnt[c] > bestH {
				bestH = cnt[c]
				best = linParams{sigma: sg, phi: phi, c: c}
			}
		}
	}
	return best, true
}
