package rdma

import (
	"fmt"
	"sort"
)

// T18 requester window. The requester may have at most W network
// transactions in flight (W = -t18-window, default -rdma-max-outstanding).
//
// Without distance credits the W slots form one shared pool (the baseline
// RDMA table semantics with a different size).
//
// With distance credits, destination d owns credit_d slots, proportional to
// its modeled round-trip time RTT_d = 84 + 71.4 ns x hops_d (T14), so that by
// Little's law every destination can sustain the same bandwidth with its own
// credits: credit_d = W x RTT_d / sum_e RTT_e, rounded by largest remainder so
// the credits add up to W exactly. Idle credits may be borrowed: a destination
// beyond its own credits may still send while the pool is not full, but only
// if the free slots exceed the unused own credits of the other destinations
// that currently have requests waiting. Borrowed slots are returned when the
// borrower's response arrives (no preemption).

const (
	t18RTTBaseNS   = 84.0
	t18RTTPerHopNS = 71.4
)

func t18RTTNS(hops int) float64 {
	return t18RTTBaseNS + t18RTTPerHopNS*float64(hops)
}

type t18CreditAllocator struct {
	window      int
	distCredits bool
	credits     []int // per destination index
	hops        []int
	inflight    []int
	total       int
}

// newT18CreditAllocator builds the allocator for destinations with the given
// hop counts. A negative hop count marks a non-destination (the requester
// itself); it gets no credit.
func newT18CreditAllocator(
	window int,
	distCredits bool,
	hops []int,
) *t18CreditAllocator {
	if window < 1 {
		panic(fmt.Sprintf("t18 window must be positive, got %d", window))
	}
	a := &t18CreditAllocator{
		window:      window,
		distCredits: distCredits,
		credits:     make([]int, len(hops)),
		hops:        append([]int(nil), hops...),
		inflight:    make([]int, len(hops)),
	}
	if distCredits {
		a.credits = t18RTTProportionalCredits(window, hops)
	}
	return a
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
	if !a.distCredits {
		return true, false
	}
	if a.inflight[d] < a.credits[d] {
		return true, false
	}
	free := a.window - a.total
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

// Acquire records a sent transaction.
func (a *t18CreditAllocator) Acquire(d int) {
	a.inflight[d]++
	a.total++
	if a.total > a.window {
		panic("t18 window exceeded")
	}
}

// Release records a completed transaction.
func (a *t18CreditAllocator) Release(d int) {
	if a.inflight[d] <= 0 || a.total <= 0 {
		panic("t18 window released below zero")
	}
	a.inflight[d]--
	a.total--
}

// Credits returns the own credits of each destination (nil-safe copy).
func (a *t18CreditAllocator) Credits() []int {
	return append([]int(nil), a.credits...)
}
