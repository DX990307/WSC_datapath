package rdma

import (
	"fmt"
	"sort"
)

// T18 requester window. The requester may have at most W network
// transactions in flight (W = -t18-window, default -rdma-max-outstanding).
//
// Credit policies (-t18-credit-policy; ISCA'27 M1 "allocation"):
//
// shared: the W slots form one shared pool (the baseline RDMA table
// semantics with a different size).
//
// rtt (= the old -t18-dist-credits=true): destination d owns credit_d slots,
// proportional to its modeled round-trip time RTT_d = 84 + 71.4 ns x hops_d
// (T14), so that by Little's law every destination can sustain the same
// bandwidth with its own credits: credit_d = W x RTT_d / sum_e RTT_e, rounded
// by largest remainder so the credits add up to W exactly. Idle credits may
// be borrowed: a destination beyond its own credits may still send while the
// pool is not full, but only if the free slots exceed the unused own credits
// of the other destinations that currently have requests waiting. Borrowed
// slots are returned when the borrower's response arrives (no preemption).
//
// near: R = -t18-near-reserve slots (the near zone) are set aside for
// destinations at most -t18-near-hops hops away; a farther destination can
// never use them. The other W - R slots (the general zone) are split over
// all destinations exactly as in rtt (RTT-proportional own credits summing
// to W - R, borrowing, no preemption), with every rtt quantity counted in the
// general zone only. A near destination sends in the near zone while it has
// a free slot and otherwise falls back to the rtt rule in the general zone.
// A transaction returns the slot of the zone it was sent in. With R = 0 the
// allocator is exactly rtt.

// T18CreditPolicy selects how the W requester slots are shared.
type T18CreditPolicy int

// Credit policies.
const (
	T18CreditShared T18CreditPolicy = iota
	T18CreditRTT
	T18CreditNear
)

func (p T18CreditPolicy) String() string {
	switch p {
	case T18CreditShared:
		return "shared"
	case T18CreditRTT:
		return "rtt"
	case T18CreditNear:
		return "near"
	}
	return fmt.Sprintf("T18CreditPolicy(%d)", int(p))
}

// ParseT18CreditPolicy parses shared | rtt | near.
func ParseT18CreditPolicy(s string) (T18CreditPolicy, error) {
	switch s {
	case "shared":
		return T18CreditShared, nil
	case "rtt":
		return T18CreditRTT, nil
	case "near":
		return T18CreditNear, nil
	}
	return 0, fmt.Errorf("credit policy must be shared, rtt or near, got %q", s)
}

const (
	t18RTTBaseNS   = 84.0
	t18RTTPerHopNS = 71.4
)

func t18RTTNS(hops int) float64 {
	return t18RTTBaseNS + t18RTTPerHopNS*float64(hops)
}

type t18CreditAllocator struct {
	window      int
	distCredits bool  // rtt or near: own credits + borrowing in the general zone
	credits     []int // general-zone own credits per destination index
	hops        []int
	inflight    []int // general-zone in-flight per destination index
	total       int   // in flight in both zones

	// General zone: W - R slots (all W slots unless the policy is near).
	genWindow int
	genTotal  int

	// Near zone (policy near): R slots for destinations within nearHops.
	policy      T18CreditPolicy
	nearReserve int
	nearHops    int
	near        []bool // destination may use the near zone
	nearIn      []int  // near-zone in-flight per destination index
	nearTotal   int
}

// newT18CreditAllocator builds the allocator for destinations with the given
// hop counts. A negative hop count marks a non-destination (the requester
// itself); it gets no credit.
func newT18CreditAllocator(
	window int,
	distCredits bool,
	hops []int,
) *t18CreditAllocator {
	policy := T18CreditShared
	if distCredits {
		policy = T18CreditRTT
	}
	return newT18CreditAllocatorPolicy(window, policy, 0, 0, hops)
}

// newT18CreditAllocatorPolicy builds the allocator for a credit policy.
// nearReserve and nearHops are used only by T18CreditNear.
func newT18CreditAllocatorPolicy(
	window int,
	policy T18CreditPolicy,
	nearReserve, nearHops int,
	hops []int,
) *t18CreditAllocator {
	if window < 1 {
		panic(fmt.Sprintf("t18 window must be positive, got %d", window))
	}
	if policy != T18CreditNear {
		nearReserve, nearHops = 0, 0
	}
	if nearReserve < 0 || nearReserve >= window {
		panic(fmt.Sprintf("t18 near reserve must be in [0, W-1] = [0, %d], got %d",
			window-1, nearReserve))
	}
	if nearHops < 0 {
		panic(fmt.Sprintf("t18 near hops must be >= 0, got %d", nearHops))
	}
	a := &t18CreditAllocator{
		window:      window,
		distCredits: policy != T18CreditShared,
		credits:     make([]int, len(hops)),
		hops:        append([]int(nil), hops...),
		inflight:    make([]int, len(hops)),
		genWindow:   window - nearReserve,
		policy:      policy,
		nearReserve: nearReserve,
		nearHops:    nearHops,
		near:        make([]bool, len(hops)),
		nearIn:      make([]int, len(hops)),
	}
	if a.distCredits {
		a.credits = t18RTTProportionalCredits(a.genWindow, hops)
	}
	if nearReserve > 0 {
		for i, h := range hops {
			a.near[i] = h >= 0 && h <= nearHops
		}
	}
	return a
}

// nearZoneFree reports whether destination d would send in the near zone.
func (a *t18CreditAllocator) nearZoneFree(d int) bool {
	return a.near[d] && a.nearTotal < a.nearReserve
}

// t18RTTProportionalCredits splits window over the destinations (hops >= 0)
// proportionally to their RTT, by largest remainder (ties: lower index).
func t18RTTProportionalCredits(window int, hops []int) []int {
	credits := make([]int, len(hops))
	sum := 0.0
	for _, h := range hops {
		if h >= 0 {
			sum += t18RTTNS(h)
		}
	}
	if sum == 0 {
		return credits
	}
	type rem struct {
		idx  int
		frac float64
	}
	var rems []rem
	assigned := 0
	for i, h := range hops {
		if h < 0 {
			continue
		}
		exact := float64(window) * t18RTTNS(h) / sum
		credits[i] = int(exact)
		assigned += credits[i]
		rems = append(rems, rem{idx: i, frac: exact - float64(credits[i])})
	}
	sort.SliceStable(rems, func(i, j int) bool {
		return rems[i].frac > rems[j].frac
	})
	for i := 0; assigned < window && len(rems) > 0; i = (i + 1) % len(rems) {
		credits[rems[i].idx]++
		assigned++
	}
	return credits
}

// CanSend reports whether a transaction to destination d may be sent now,
// and whether it would use a borrowed credit. waiting(e) reports whether
// destination e has queued requests.
func (a *t18CreditAllocator) CanSend(
	d int,
	waiting func(e int) bool,
) (ok, borrow bool) {
	if a.total >= a.window {
		return false, false
	}
	if a.nearZoneFree(d) {
		return true, false
	}
	if a.genTotal >= a.genWindow {
		return false, false
	}
	if !a.distCredits {
		return true, false
	}
	if a.inflight[d] < a.credits[d] {
		return true, false
	}
	free := a.genWindow - a.genTotal
	reserved := 0
	for e := range a.credits {
		if e == d || a.inflight[e] >= a.credits[e] || !waiting(e) {
			continue
		}
		reserved += a.credits[e] - a.inflight[e]
	}
	if free > reserved {
		return true, true
	}
	return false, false
}

// Acquire records a sent transaction (admitted by CanSend in the same
// state). nearZone reports the zone it took; pass it back to Release.
func (a *t18CreditAllocator) Acquire(d int) (nearZone bool) {
	if a.nearZoneFree(d) {
		a.nearIn[d]++
		a.nearTotal++
		nearZone = true
	} else {
		a.inflight[d]++
		a.genTotal++
		if a.genTotal > a.genWindow {
			panic("t18 general zone exceeded")
		}
	}
	a.total++
	if a.total > a.window {
		panic("t18 window exceeded")
	}
	return nearZone
}

// Release records a completed transaction sent in the given zone.
func (a *t18CreditAllocator) Release(d int, nearZone bool) {
	if nearZone {
		if a.nearIn[d] <= 0 || a.nearTotal <= 0 {
			panic("t18 near zone released below zero")
		}
		a.nearIn[d]--
		a.nearTotal--
	} else {
		if a.inflight[d] <= 0 || a.genTotal <= 0 {
			panic("t18 window released below zero")
		}
		a.inflight[d]--
		a.genTotal--
	}
	if a.total <= 0 {
		panic("t18 window released below zero")
	}
	a.total--
}

// Inflight returns destination d's transactions in flight in both zones.
func (a *t18CreditAllocator) Inflight(d int) int {
	return a.inflight[d] + a.nearIn[d]
}

// Credits returns the own credits of each destination (nil-safe copy).
func (a *t18CreditAllocator) Credits() []int {
	return append([]int(nil), a.credits...)
}
