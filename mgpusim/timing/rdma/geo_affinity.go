package rdma

import (
	"sort"

	"github.com/sarchlab/akita/v3/mem/mem"
	"github.com/sarchlab/akita/v3/mem/vm"
	"github.com/sarchlab/akita/v3/sim"
)

// ISCA'27 affinity-push (-geo-push-mode affinity): the owner pushes a read
// response to the requester's learned co-readers instead of its mesh
// neighbors.
//
//   - Owner sharing table: every slot also remembers the line's last reader
//     and the last reader before it. When the owner answers requester R it
//     piggybacks, per line, the most recent reader other than R ("hint", one
//     6-bit GPM id per line).
//   - Requester: a 48-entry table of 4-bit saturating counters. A hint P
//     increments counter P. Every DecayHints hints (default 512) all
//     counters are halved, so the table follows phase changes. The top K
//     GPMs with a count >= MinCount (default 2) are the partners.
//   - Every remote read request carries the requester's partner list (K x
//     6 bits in the request header); the owner keeps the latest list per
//     requester (48 x K x 6 bits).
//   - The owner multicasts each read response to R plus R's partners
//     (owner, R and the other requesters excluded) along the mesh tree; the
//     partners handle the push exactly like geo-push neighbors. With the
//     shared filter only lines seen read by >= 2 GPMs are pushed.
//
// The hint and the partner list are header bits; the model does not add
// bytes for them (6-12 bits fit in the existing headers' unused fields).
//
// In the simulator the header bits travel in two package-level side tables
// keyed by the message ID (written by the sender, consumed by the
// receiver), because the request and response message types are shared
// with the baseline.

const affCounterMax = 15

var (
	affReqHdr  = map[string][]int{}
	affRspHint = map[string]map[uint64]int{}
)

// affTagRequest attaches the requester's partner list to a request it sends.
func (c *Comp) affTagRequest(msgID string) {
	g := c.geo
	if g == nil || g.affCnt == nil || len(g.affTop) == 0 {
		return
	}
	affReqHdr[msgID] = append([]int(nil), g.affTop...)
	g.stats.AffHdrSent++
}

// affOwnerReadHeader stores the partner list carried by a request.
func (c *Comp) affOwnerReadHeader(msg sim.Msg) {
	hdr, ok := affReqHdr[msg.Meta().ID]
	if !ok {
		return
	}
	delete(affReqHdr, msg.Meta().ID)
	if src := c.geoTopo.Index(msg.Meta().Src); src >= 0 {
		c.geo.partnersOf[src] = hdr
	}
}

// affSendHints piggybacks, per line, the previous reader on the response
// msg to the requesters reqs.
func (c *Comp) affSendHints(msg sim.Msg, reqs []sim.Port, pid vm.PID,
	lines []geoPushLine) {
	if len(reqs) != 1 {
		return // multicast responses (owner merge) carry no hint
	}
	r := c.geoTopo.Index(reqs[0])
	if r < 0 {
		return
	}
	var hints map[uint64]int
	for _, l := range lines {
		p := c.geo.share.hint(geoLineKey{uint32(pid), l.addr}, r)
		if p < 0 || p == r {
			continue
		}
		if hints == nil {
			hints = make(map[uint64]int, len(lines))
		}
		hints[l.addr] = p
		c.geo.stats.AffHintsSent++
	}
	if hints != nil {
		affRspHint[msg.Meta().ID] = hints
	}
}

// affTargets returns the union of the requesters' partners, owner and
// requesters excluded.
func (c *Comp) affTargets(reqIdx []int) []int {
	g := c.geo
	seen := map[int]bool{c.geoIdx: true}
	for _, r := range reqIdx {
		seen[r] = true
	}
	var out []int
	for _, r := range reqIdx {
		for _, p := range g.partnersOf[r] {
			if seen[p] {
				continue
			}
			seen[p] = true
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		g.stats.AffNoPartnerRsps++
		return nil
	}
	for _, p := range out {
		h := t18MaxHops - 1
		for _, r := range reqIdx {
			if x := c.geoTopo.Hops(r, p); x < h {
				h = x
			}
		}
		g.stats.AffPartnerHops[h]++
	}
	return out
}

// affLearn consumes the hints of a response this requester received.
func (c *Comp) affLearn(rsp sim.Msg) {
	g := c.geo
	if g == nil || g.affCnt == nil {
		return
	}
	hints, ok := affRspHint[rsp.Meta().ID]
	if !ok {
		return
	}
	delete(affRspHint, rsp.Meta().ID)
	// Deterministic order (map iteration is random).
	addrs := make([]uint64, 0, len(hints))
	for a := range hints {
		addrs = append(addrs, a)
	}
	sort.Slice(addrs, func(i, j int) bool { return addrs[i] < addrs[j] })
	for _, a := range addrs {
		p := hints[a]
		if p < 0 || p >= len(g.affCnt) || p == c.geoIdx {
			continue
		}
		if g.affCnt[p] < affCounterMax {
			g.affCnt[p]++
		}
		g.stats.AffHintsLearned++
		g.affHints++
		if g.affHints >= g.cfg.DecayHints {
			g.affHints = 0
			g.stats.AffDecays++
			for i := range g.affCnt {
				g.affCnt[i] /= 2
			}
		}
	}
	c.affRecomputeTop()
}

func (c *Comp) affRecomputeTop() {
	g := c.geo
	g.affTop = g.affTop[:0]
	for k := 0; k < g.cfg.AffinityK; k++ {
		best := -1
		for i, v := range g.affCnt {
			if int(v) < g.cfg.MinCount || i == c.geoIdx {
				continue
			}
			taken := false
			for _, t := range g.affTop {
				if t == i {
					taken = true
				}
			}
			if taken {
				continue
			}
			if best < 0 || v > g.affCnt[best] {
				best = i
			}
		}
		if best < 0 {
			break
		}
		g.affTop = append(g.affTop, best)
	}
}

// affLearnAccess is the hook for responses delivered through the baseline
// requester path.
func (c *Comp) affLearnAccess(rsp mem.AccessRsp) { c.affLearn(rsp) }
