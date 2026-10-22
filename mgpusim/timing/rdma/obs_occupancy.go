package rdma

import (
	"math"
	"sort"
	"strings"

	"github.com/sarchlab/akita/v3/mem/mem"
	memtrace "github.com/sarchlab/akita/v3/mem/trace"
	"github.com/sarchlab/akita/v3/sim"
)

// ISCA'27 P13 statistics: RRT / owner occupancy (A2) and the passive hooks
// of the latency, aggregation and reuse statistics (memtrace.Obs*). Nothing
// here changes the engine's state or timing; with the statistics off every
// hook is a nil or bool check.

// ObsOccupancy is the sampled occupancy of one RDMA engine.
type ObsOccupancy struct {
	Samples  uint64
	SampleNS float64
	Limit    int // requester in-flight limit (RRT entries or T18 window W)

	MeanUsed, MeanWaiting, MeanOwner float64
	MaxUsed, MaxWaiting, MaxOwner    int
	FracFull                         float64 // used >= Limit
	FracUsedGE32                     float64

	Dests []ObsDestOccupancy // destinations with a nonzero sample
}

// ObsDestOccupancy is the sampled in-flight count to one destination.
type ObsDestOccupancy struct {
	Dst       string // destination RDMA port name
	Mean      float64
	Max       int
	FracGE32  float64
	SumSample float64
}

type obsDestOcc struct {
	sum  float64
	max  int
	ge32 uint64
}

type obsOccState struct {
	sampleNS float64
	next     float64

	samples          uint64
	sumUsed, sumWait float64
	sumOwner         float64
	maxUsed, maxWait int
	maxOwner         int
	full, usedGE32   uint64
	limit            int
	dest             map[sim.Port]*obsDestOcc
	scratch          map[sim.Port]int
}

// EnableObsOccupancy samples this engine's occupancy every sampleNS.
func (c *Comp) EnableObsOccupancy(sampleNS float64) {
	if sampleNS <= 0 {
		sampleNS = 1000
	}
	c.obsOcc = &obsOccState{
		sampleNS: sampleNS,
		next:     sampleNS,
		dest:     map[sim.Port]*obsDestOcc{},
		scratch:  map[sim.Port]int{},
	}
}

// obsOccTick records the state for every sample boundary up to now. The
// state at the start of a tick is the state left by the previous tick, which
// held unchanged since then (the engine only changes state in Tick).
func (c *Comp) obsOccTick(now sim.VTimeInSec) {
	o := c.obsOcc
	t := float64(now) * 1e9
	if t < o.next {
		return
	}
	k := math.Floor((t-o.next)/o.sampleNS) + 1
	c.obsOccRecord(k)
	o.next += k * o.sampleNS
}

// ObsOccupancyFinish records the remaining samples up to endNS.
func (c *Comp) ObsOccupancyFinish(endNS float64) {
	o := c.obsOcc
	if o == nil || endNS < o.next {
		return
	}
	k := math.Floor((endNS-o.next)/o.sampleNS) + 1
	c.obsOccRecord(k)
	o.next += k * o.sampleNS
}

func (c *Comp) obsOccRecord(k float64) {
	o := c.obsOcc
	used, limit := c.obsUsed()
	wait := c.obsWaiting()
	owner := c.ownerOutstandingCount()
	o.limit = limit
	n := uint64(k)
	o.samples += n
	o.sumUsed += k * float64(used)
	o.sumWait += k * float64(wait)
	o.sumOwner += k * float64(owner)
	if used > o.maxUsed {
		o.maxUsed = used
	}
	if wait > o.maxWait {
		o.maxWait = wait
	}
	if owner > o.maxOwner {
		o.maxOwner = owner
	}
	if limit > 0 && used >= limit {
		o.full += n
	}
	if used >= 32 {
		o.usedGE32 += n
	}
	for p := range o.scratch {
		delete(o.scratch, p)
	}
	c.obsDestCounts(o.scratch)
	for p, v := range o.scratch {
		if v <= 0 {
			continue
		}
		d := o.dest[p]
		if d == nil {
			d = &obsDestOcc{}
			o.dest[p] = d
		}
		d.sum += k * float64(v)
		if v > d.max {
			d.max = v
		}
		if v >= 32 {
			d.ge32 += n
		}
	}
}

// obsUsed returns the requester in-flight count and its limit.
func (c *Comp) obsUsed() (used, limit int) {
	if s := c.t18; s != nil && (s.reqActive || s.compose) && s.credits != nil {
		return s.credits.total, s.credits.window
	}
	return c.requesterOutstandingCount(), c.maxOutstanding
}

// obsWaiting returns the remote requests waiting for an in-flight slot:
// requests in the L1 input buffer, T18 entries waiting for a credit, and M2
// lines not yet sent.
func (c *Comp) obsWaiting() int {
	w := 0
	if p, ok := c.ToL1.(interface{ NumBuffered() int }); ok {
		w += p.NumBuffered()
	}
	if s := c.t18; s != nil && s.reqActive {
		w += s.queued
	}
	if c.remoteConfig.Enabled {
		w += len(c.remotePendingBatch)
		for _, b := range c.remoteBatches {
			if b != nil {
				w += b.lineCount()
			}
		}
	}
	return w
}

func (c *Comp) obsDestCounts(out map[sim.Port]int) {
	if s := c.t18; s != nil && (s.reqActive || s.compose) &&
		s.credits != nil && s.topo != nil {
		for d := range s.credits.inflight {
			if v := s.credits.Inflight(d); v > 0 {
				out[s.topo.ports[d]] += v
			}
		}
		return
	}
	for _, t := range c.transactionsFromInside {
		if t.toOutside != nil && t.toOutside.Meta().Dst != nil {
			out[t.toOutside.Meta().Dst]++
		}
	}
	for _, e := range c.remoteSingleInflight {
		if e != nil && e.owner != nil {
			out[e.owner]++
		}
	}
	for _, b := range c.remoteBitmapInflight {
		if b != nil && b.dst != nil {
			out[b.dst]++
		}
	}
}

// ObsOccupancyResult returns the sampled occupancy (nil when off).
func (c *Comp) ObsOccupancyResult() *ObsOccupancy {
	o := c.obsOcc
	if o == nil {
		return nil
	}
	r := &ObsOccupancy{Samples: o.samples, SampleNS: o.sampleNS,
		Limit: o.limit, MaxUsed: o.maxUsed, MaxWaiting: o.maxWait,
		MaxOwner: o.maxOwner}
	if o.samples > 0 {
		n := float64(o.samples)
		r.MeanUsed = o.sumUsed / n
		r.MeanWaiting = o.sumWait / n
		r.MeanOwner = o.sumOwner / n
		r.FracFull = float64(o.full) / n
		r.FracUsedGE32 = float64(o.usedGE32) / n
	}
	for p, d := range o.dest {
		dd := ObsDestOccupancy{Dst: p.Name(), Max: d.max, SumSample: d.sum}
		if o.samples > 0 {
			dd.Mean = d.sum / float64(o.samples)
			dd.FracGE32 = float64(d.ge32) / float64(o.samples)
		}
		r.Dests = append(r.Dests, dd)
	}
	sort.Slice(r.Dests, func(i, j int) bool { return r.Dests[i].Dst < r.Dests[j].Dst })
	return r
}

// ---------------------------------------------------------------------------
// latency / aggregation / reuse hooks

func obsIsRead(req mem.AccessReq) bool {
	_, ok := req.(*mem.ReadReq)
	return ok
}

func obsPortName(p sim.Port) string {
	if p == nil {
		return ""
	}
	return p.Name()
}

// obsAdmit: t0 of a request from the L1V.
func (c *Comp) obsAdmit(req mem.AccessReq, dst sim.Port) {
	if !memtrace.ObsEnabled() {
		return
	}
	memtrace.ObsAdmit(c.Name(), obsPortName(dst), req.Meta().ID,
		req.GetAddress(), req.GetByteSize(), uint64(req.GetPID()),
		obsIsRead(req), req.Meta().RecvTime)
}

// obsNetSend: t1 of one line of a read packet.
func (c *Comp) obsNetSend(dst sim.Port, netID string, addr uint64,
	lines int, now sim.VTimeInSec) {
	if !memtrace.ObsEnabled() {
		return
	}
	memtrace.ObsNetSend(c.Name(), obsPortName(dst), netID, addr, lines, now)
}

// obsRspTo returns the RspTo of a response message.
func obsRspTo(msg sim.Msg) string {
	if r, ok := msg.(mem.AccessRsp); ok {
		return r.GetRspTo()
	}
	return ""
}

// pgclsAdmit records a remote access from an L1 for the page classification
// statistics (-report-pgcls). Read-only.
func (c *Comp) pgclsAdmit(req mem.AccessReq, dst sim.Port) {
	src := "v"
	if s := req.Meta().Src; s != nil {
		n := s.Name()
		switch {
		case strings.Contains(n, "L1VCache"):
			src = "v"
		case strings.Contains(n, "L1S"):
			src = "s"
		default:
			src = "i"
		}
	}
	bytes := req.GetByteSize()
	if w, ok := req.(*mem.WriteReq); ok {
		bytes = uint64(len(w.Data))
	}
	memtrace.PgclsAccess(c.Name(), obsPortName(dst), req.GetAddress(), bytes,
		obsIsRead(req), src, req.Meta().RecvTime)
}
