package rdma

import (
	"github.com/sarchlab/akita/v3/mem/mem"
	"github.com/sarchlab/akita/v3/sim"
)

// isca27/nearest: realistic "read from the nearest holder" (the timing
// oracle of nearest.go is the bound). Two lookups, both built on a holder
// directory kept by each home RDMA engine for its own lines:
//
//   - Home directory. A GPM whose L2 installs a remote (M3) replica line
//     sends an install notice (one small message) to the line's home; with
//     EvictNotice it also sends one when the line is replaced. The home keeps
//     a holder bitmap per line and a per-page count of lines held per GPM.
//     A notice for a fill whose read was opened before the last write of the
//     line or before the kernel boundary is ignored (the fill carries the
//     version it read). A write performed at the home (any writer, driver
//     writes included) clears the line's holders and marks the page written
//     for the rest of the kernel. Everything is cleared at kernel boundaries
//     (the M3 write epochs), like the oracle's holder sets.
//
//   - A1 requester hint (Mode NearHint). The home's data response carries
//     (free, a few bits) the holder of that page nearest to the reader by the
//     expected cost hop*d(R,G) + p*(base + hop*d(G,H)), p = Laplace share of
//     the page's held lines G lacks; nothing when no holder is closer than
//     the home or the page was written in this kernel. The reader keeps the
//     hint per page in a small table (HintEntries, FIFO) and sends a later
//     read of the page to the hinted holder iff
//     hop*d(R,G) + p_R*(base + hop*d(G,H)) < hop*d(R,H), p_R = the reader's
//     Laplace estimate of its hint misses. The holder reads its L2 (lookup
//     only); a miss forwards the read to the home and relays the data, and
//     the reader drops the hint. The home remembers which readers it hinted
//     for a page; the first write to the page in a kernel sends each of them
//     a hint invalidation. A GPM that writes a page drops its own hint and
//     ignores hints for that page until the boundary (it reads its own
//     writes from the home).
//
//   - A2 home forward (Mode NearFwd; NearHint|NearFwd combines both: reads
//     with a usable hint go to the holder, the others to the home, which may
//     delegate them). The home delegates a read of a line
//     that has holders (and was not written in this kernel) to the holder G
//     minimising d(H,G)+d(G,R) when that path is at most FwdSlackHops longer
//     than d(H,R): reader -> home -> holder -> reader. A holder miss
//     forwards to the home (which then serves it) and relays.
//
// Every message crosses the real mesh with its real size and queueing.

// Nearest modes.
const (
	NearHint = 1
	NearFwd  = 2
)

// NearestRealConfig configures the realistic nearest-holder lookup.
type NearestRealConfig struct {
	Mode         int
	HintEntries  int  // per-GPM hint table entries (<= 0: unlimited)
	EvictNotice  bool // holders notify the home when they replace a line
	FwdSlackHops int
	BaseNS       float64 // R3 constants: RTT = BaseNS + HopNS * hops
	HopNS        float64
	NoticeBytes  int
	PageBytes    uint64
}

// NearestRealStats are the platform-wide counters.
type NearestRealStats struct {
	EligibleReads   uint64 // remote-data-path single-line reads seen
	HintsGiven      uint64 // home responses that carried a hint
	HintsNone       uint64 // ... that carried "no holder"
	HintUses        uint64 // reads sent to a hinted holder
	HintSkips       uint64 // a hint existed but failed the cost test
	HintAbsent      uint64 // no hint for the page
	HolderHits      uint64
	HolderMisses    uint64
	Delegated       uint64 // A2 reads forwarded by the home
	InstallNotices  uint64
	EvictNotices    uint64
	StaleNotices    uint64
	HintInvalMsgs   uint64
	HintTableEvicts uint64
	SelfWriteDrops  uint64
	HopsHome        float64 // one-way hops reader->home of the eligible reads
	PathHops        float64 // total hops of the paths they took (all legs)
	HomeHops2       float64 // 2 x HopsHome (the direct path)
	Kernels         uint64
	ServedByEngine  []uint64
	HomeByEngine    []uint64
}

type nrBits [2]uint64

func (s *nrBits) set(i int)     { s[i>>6] |= 1 << uint(i&63) }
func (s *nrBits) clear(i int)   { s[i>>6] &^= 1 << uint(i&63) }
func (s nrBits) has(i int) bool { return s[i>>6]&(1<<uint(i&63)) != 0 }
func (s nrBits) empty() bool    { return s[0] == 0 && s[1] == 0 }
func (s nrBits) each(f func(int)) {
	for w := 0; w < 2; w++ {
		for x := s[w]; x != 0; x &= x - 1 {
			f(w*64 + trailingZeros64(x))
		}
	}
}

type nrPage struct {
	cnt     []uint8 // lines of the page held per GPM
	lines   int     // lines of the page with at least one holder
	hinted  nrBits  // readers that got a hint for the page this kernel
	written bool
}

type nrHints struct {
	m     map[uint64]int
	order []uint64
}

// nrTask is a read a holder serves for a reader.
type nrTask struct {
	replyTo sim.Port
	rspTo   string
}

// nrSent is the path record of one reader read (statistics / R5 hook).
type nrSent struct {
	r, home, via int
	line         uint64
	delegated    bool
	missed       bool
}

// NearestReal is shared by every RDMA engine of the platform.
type NearestReal struct {
	Cfg NearestRealConfig
	// Served (optional, R5): a read of line by engine e was answered over
	// legs messages and hops hops instead of the direct home round trip.
	Served func(e int, line uint64, legs, hops int)

	engines []*Comp
	index   map[*Comp]int
	byPort  map[sim.Port]int

	lines    map[uint64]nrBits
	lastW    map[uint64]sim.VTimeInSec
	pages    map[uint64]*nrPage
	boundary sim.VTimeInSec
	kernels  int

	hints []nrHints
	selfW []map[uint64]bool
	pUse  []float64
	pMiss []float64

	tasks      map[string]*nrTask
	sent       map[string]*nrSent
	hintFor    map[string]int // read ID -> hinted holder + 1 (0: none)
	noDelegate map[string]bool

	Stats NearestRealStats
}

// NearNotice is a small control message of the holder directory.
type NearNotice struct {
	sim.MsgMeta
	Kind    int // nrInstall, nrEvict, nrHintInval
	Addr    uint64
	ReqTime sim.VTimeInSec
}

// Meta returns the message metadata.
func (m *NearNotice) Meta() *sim.MsgMeta { return &m.MsgMeta }

const (
	nrInstall = iota
	nrEvict
	nrHintInval
)

// NewNearestReal creates the lookup over the platform's RDMA engines (in
// GPM order) and attaches it to them.
func NewNearestReal(engines []*Comp, cfg NearestRealConfig) *NearestReal {
	if cfg.NoticeBytes <= 0 {
		cfg.NoticeBytes = 16
	}
	if cfg.PageBytes == 0 {
		cfg.PageBytes = 4096
	}
	n := &NearestReal{
		Cfg:        cfg,
		engines:    engines,
		index:      make(map[*Comp]int, len(engines)),
		byPort:     make(map[sim.Port]int, len(engines)),
		lines:      make(map[uint64]nrBits),
		lastW:      make(map[uint64]sim.VTimeInSec),
		pages:      make(map[uint64]*nrPage),
		hints:      make([]nrHints, len(engines)),
		selfW:      make([]map[uint64]bool, len(engines)),
		pUse:       make([]float64, len(engines)),
		pMiss:      make([]float64, len(engines)),
		tasks:      make(map[string]*nrTask),
		sent:       make(map[string]*nrSent),
		hintFor:    make(map[string]int),
		noDelegate: make(map[string]bool),
	}
	if len(engines) > 128 {
		panic("nearest: more than 128 GPMs")
	}
	n.Stats.ServedByEngine = make([]uint64, len(engines))
	n.Stats.HomeByEngine = make([]uint64, len(engines))
	for i, e := range engines {
		n.index[e] = i
		n.byPort[e.ToOutside] = i
		n.hints[i].m = make(map[uint64]int)
		n.selfW[i] = make(map[uint64]bool)
		e.nearReal = n
	}
	return n
}

// NearestRealOf returns the lookup attached to this engine (nil when off).
func (c *Comp) NearestRealOf() *NearestReal { return c.nearReal }

func (n *NearestReal) hops(a, b int) int {
	return t18Hops(n.engines[a].t18Topo.self, n.engines[b].t18Topo.self)
}

func (n *NearestReal) page(line uint64) uint64 { return line &^ (n.Cfg.PageBytes - 1) }

// KernelBoundary is called at every GPM's kernel launch; launches less than
// 1 us apart count as one boundary.
func (n *NearestReal) KernelBoundary(now sim.VTimeInSec) {
	if n.kernels > 0 && float64(now-n.boundary) < 1e-6 {
		n.boundary = now
		return
	}
	n.kernels++
	n.Stats.Kernels++
	n.boundary = now
	n.lines = make(map[uint64]nrBits)
	n.lastW = make(map[uint64]sim.VTimeInSec)
	n.pages = make(map[uint64]*nrPage)
	for i := range n.hints {
		n.hints[i] = nrHints{m: make(map[uint64]int)}
		n.selfW[i] = make(map[uint64]bool)
	}
}

func (n *NearestReal) dropHolder(line uint64, g int) {
	h, ok := n.lines[line]
	if !ok || !h.has(g) {
		return
	}
	h.clear(g)
	pg := n.pages[n.page(line)]
	if pg != nil && pg.cnt[g] > 0 {
		pg.cnt[g]--
	}
	if h.empty() {
		delete(n.lines, line)
		if pg != nil && pg.lines > 0 {
			pg.lines--
		}
		return
	}
	n.lines[line] = h
}

// NoteWrite is a write performed at home engine hi (-1: a driver write,
// between kernels: hinted readers drop the hint at once) of
// [addr, addr+size).
func (n *NearestReal) NoteWrite(now sim.VTimeInSec, hi int, addr, size uint64) {
	if size == 0 {
		size = 1
	}
	for a := addr &^ (remoteLineBytes - 1); a < addr+size; a += remoteLineBytes {
		if h, ok := n.lines[a]; ok {
			h.each(func(g int) { n.dropHolder(a, g) })
		}
		n.lastW[a] = now
		p := n.page(a)
		pg := n.pages[p]
		if pg == nil {
			pg = &nrPage{cnt: make([]uint8, len(n.engines))}
			n.pages[p] = pg
		}
		if pg.written {
			continue
		}
		pg.written = true
		// the home tells the readers it hinted to forget the hint
		pg.hinted.each(func(r int) {
			if hi >= 0 && r != hi {
				n.engines[hi].nrQueue(now, n.engines[r].ToOutside, nrHintInval, p, 0)
				n.Stats.HintInvalMsgs++
			} else {
				n.hints[r].drop(p)
			}
		})
		pg.hinted = nrBits{}
	}
}

func (h *nrHints) drop(page uint64) { delete(h.m, page) }

func (n *NearestReal) setHint(r int, page uint64, g int) {
	h := &n.hints[r]
	if _, ok := h.m[page]; !ok {
		if c := n.Cfg.HintEntries; c > 0 {
			for len(h.m) >= c && len(h.order) > 0 {
				old := h.order[0]
				h.order = h.order[1:]
				if _, ok := h.m[old]; ok {
					delete(h.m, old)
					n.Stats.HintTableEvicts++
				}
			}
		}
		h.order = append(h.order, page)
		if len(h.order) > 4*len(h.m)+64 {
			keep := h.order[:0]
			for _, p := range h.order {
				if _, ok := h.m[p]; ok {
					keep = append(keep, p)
				}
			}
			h.order = keep
		}
	}
	h.m[page] = g
}

// pmiss is reader r's Laplace estimate of a hint miss.
func (n *NearestReal) pmiss(r int) float64 {
	return (n.pMiss[r] + 1) / (n.pUse[r] + 2)
}

func (n *NearestReal) noteHintResult(r int, miss bool) {
	if n.pUse[r] >= 256 {
		n.pUse[r] /= 2
		n.pMiss[r] /= 2
	}
	n.pUse[r]++
	if miss {
		n.pMiss[r]++
	}
}

// route (requester, A1) picks where a single-line read of line goes.
func (n *NearestReal) route(c *Comp, line uint64, home sim.Port) (sim.Port, bool) {
	if n.Cfg.Mode&NearHint == 0 {
		return nil, false
	}
	ri, ok := n.index[c]
	hi, ok2 := n.byPort[home]
	if !ok || !ok2 || c.t18Topo == nil {
		return nil, false
	}
	p := n.page(line)
	if n.selfW[ri][p] {
		return nil, false
	}
	g, ok := n.hints[ri].m[p]
	if !ok {
		n.Stats.HintAbsent++
		return nil, false
	}
	if g == ri || g == hi {
		n.Stats.HintSkips++
		return nil, false
	}
	dRG, dRH, dGH := float64(n.hops(ri, g)), float64(n.hops(ri, hi)), float64(n.hops(g, hi))
	b, h := n.Cfg.BaseNS, n.Cfg.HopNS
	pm := n.pmiss(ri)
	if h*dRG+pm*(b+h*dGH) >= h*dRH {
		n.Stats.HintSkips++
		return nil, false
	}
	n.Stats.HintUses++
	return n.engines[g].ToOutside, true
}

// noteSent (requester) records a read sent to dst for line homed at home.
func (n *NearestReal) noteSent(c *Comp, id string, line uint64, home, dst sim.Port) {
	ri := n.index[c]
	hi := n.byPort[home]
	s := &nrSent{r: ri, home: hi, via: -1, line: line}
	if dst != home {
		s.via = n.byPort[dst]
		n.tasks[id] = &nrTask{replyTo: c.ToOutside, rspTo: id}
	}
	n.sent[id] = s
	n.Stats.EligibleReads++
	n.Stats.HomeByEngine[hi]++
	d := n.hops(ri, hi)
	n.Stats.HopsHome += float64(d)
	n.Stats.HomeHops2 += float64(2 * d)
}

// noteRsp (requester) closes the record of read id when its data arrives.
func (n *NearestReal) noteRsp(c *Comp, id string) {
	s := n.sent[id]
	if s == nil {
		return
	}
	delete(n.sent, id)
	ri := s.r
	if g, ok := n.hintFor[id]; ok {
		delete(n.hintFor, id)
		p := n.page(s.line)
		if g > 0 && !n.selfW[ri][p] {
			n.setHint(ri, p, g-1)
		} else if g == 0 {
			n.hints[ri].drop(p)
		}
	}
	legs, hops := 2, 2*n.hops(ri, s.home)
	served := s.home
	switch {
	case s.via >= 0 && !s.delegated:
		g := s.via
		n.noteHintResult(ri, s.missed)
		if s.missed {
			legs, hops = 4, 2*n.hops(ri, g)+2*n.hops(g, s.home)
			n.hints[ri].drop(n.page(s.line))
		} else {
			legs, hops = 2, 2*n.hops(ri, g)
			served = g
		}
	case s.delegated:
		g := s.via
		if s.missed {
			legs, hops = 5, n.hops(ri, s.home)+3*n.hops(s.home, g)+n.hops(g, ri)
		} else {
			legs, hops = 3, n.hops(ri, s.home)+n.hops(s.home, g)+n.hops(g, ri)
			served = g
		}
	}
	n.Stats.ServedByEngine[served]++
	n.Stats.PathHops += float64(hops)
	if n.Served != nil && (legs != 2 || hops != 2*n.hops(ri, s.home)) {
		n.Served(ri, s.line, legs, hops)
	}
}

// selfWrite (requester): this GPM writes a remote line.
func (n *NearestReal) selfWrite(c *Comp, addr uint64) {
	ri, ok := n.index[c]
	if !ok {
		return
	}
	p := n.page(addr)
	if _, had := n.hints[ri].m[p]; had {
		n.Stats.SelfWriteDrops++
	}
	n.hints[ri].drop(p)
	n.selfW[ri][p] = true
}

// homeRead (home) sees an eligible single-line read of line from reader r.
// A1: the hint for the response. A2: the holder to delegate to (or -1).
func (n *NearestReal) homeRead(c *Comp, id string, line uint64, src sim.Port) int {
	hi := n.index[c]
	ri, ok := n.byPort[src]
	if !ok {
		return -1
	}
	_, hinted := n.hintFor[id]
	if n.Cfg.Mode&NearHint != 0 && !hinted {
		p := n.page(line)
		pg := n.pages[p]
		best := -1
		if pg != nil && !pg.written && pg.lines > 0 {
			b, h := n.Cfg.BaseNS, n.Cfg.HopNS
			dRH := float64(n.hops(ri, hi))
			bc := h * dRH
			for g, k := range pg.cnt {
				if k == 0 || g == ri || g == hi {
					continue
				}
				pm := float64(pg.lines-int(k)+1) / float64(pg.lines+2)
				if x := h*float64(n.hops(ri, g)) + pm*(b+h*float64(n.hops(g, hi))); x < bc {
					best, bc = g, x
				}
			}
		}
		if best >= 0 {
			n.hintFor[id] = best + 1
			pg.hinted.set(ri)
			n.Stats.HintsGiven++
		} else {
			n.hintFor[id] = 0
			n.Stats.HintsNone++
		}
	}
	if n.Cfg.Mode&NearFwd != 0 {
		if n.noDelegate[id] {
			return -1
		}
		h, ok := n.lines[line]
		if !ok {
			return -1
		}
		dRH := n.hops(ri, hi)
		best, bd := -1, dRH+n.Cfg.FwdSlackHops+1
		h.each(func(g int) {
			if g == ri || g == hi {
				return
			}
			if d := n.hops(hi, g) + n.hops(g, ri); d < bd {
				best, bd = g, d
			}
		})
		return best
	}
	return -1
}

// notice (home) applies a holder-directory message.
func (n *NearestReal) notice(c *Comp, m *NearNotice) {
	switch m.Kind {
	case nrInstall:
		g, ok := n.byPort[m.Src]
		if !ok {
			return
		}
		line := m.Addr
		if m.ReqTime < n.boundary || m.ReqTime < n.lastW[line] {
			n.Stats.StaleNotices++
			return
		}
		h := n.lines[line]
		if h.has(g) {
			return
		}
		p := n.page(line)
		pg := n.pages[p]
		if pg == nil {
			pg = &nrPage{cnt: make([]uint8, len(n.engines))}
			n.pages[p] = pg
		}
		if pg.written {
			return
		}
		if h.empty() {
			pg.lines++
		}
		h.set(g)
		n.lines[line] = h
		pg.cnt[g]++
	case nrEvict:
		g, ok := n.byPort[m.Src]
		if ok {
			n.dropHolder(m.Addr, g)
		}
	case nrHintInval:
		n.hints[n.index[c]].drop(m.Addr)
	}
}

// ---------------------------------------------------------------------------
// engine side

// nrQueue queues a notice from this engine to dst.
func (c *Comp) nrQueue(now sim.VTimeInSec, dst sim.Port, kind int, addr uint64, reqTime sim.VTimeInSec) {
	m := &NearNotice{
		MsgMeta: sim.MsgMeta{
			ID:           sim.GetIDGenerator().Generate(),
			Src:          c.ToOutside,
			Dst:          dst,
			TrafficBytes: c.nearReal.Cfg.NoticeBytes,
		},
		Kind:    kind,
		Addr:    addr,
		ReqTime: reqTime,
	}
	c.nrOut = append(c.nrOut, m)
	c.TickLater(now)
}

// nrSendNotices sends queued notices.
func (c *Comp) nrSendNotices(now sim.VTimeInSec) bool {
	progress := false
	for i := 0; i < c.effectivePipelineWidth() && len(c.nrOut) > 0; i++ {
		m := c.nrOut[0]
		m.SendTime = now
		if err := c.ToOutside.Send(m); err != nil {
			return progress
		}
		c.nrOut = c.nrOut[1:]
		progress = true
	}
	return progress
}

// RemoteInstalled (writeback.ReplicaObserver) is called by this GPM's L2
// when it installs a remote replica line: an install notice to the home.
func (c *Comp) RemoteInstalled(reqTime sim.VTimeInSec, line uint64) {
	n := c.nearReal
	home := c.RemoteRDMAAddressTable.Find(line)
	if _, ok := n.byPort[home]; !ok || home == c.ToOutside {
		return
	}
	n.Stats.InstallNotices++
	c.nrQueue(c.Engine.CurrentTime(), home, nrInstall, line, reqTime)
}

// RemoteGone (writeback.ReplicaObserver): with EvictNotice a replaced line
// is reported to its home (lines dropped by a write epoch are not: the home
// clears them itself).
func (c *Comp) RemoteGone(line uint64, coherence bool) {
	n := c.nearReal
	if coherence || !n.Cfg.EvictNotice {
		return
	}
	home := c.RemoteRDMAAddressTable.Find(line)
	if _, ok := n.byPort[home]; !ok || home == c.ToOutside {
		return
	}
	n.Stats.EvictNotices++
	c.nrQueue(c.Engine.CurrentTime(), home, nrEvict, line, 0)
}

func (c *Comp) nrLive() int { return len(c.nrProbes) + len(c.nrFwd) }

func (c *Comp) nrPending() bool { return c.nrLive() > 0 || len(c.nrOut) > 0 }

// nrFromOutside handles a request from outside that belongs to the lookup.
// handled false: the normal path processes it.
func (c *Comp) nrFromOutside(now sim.VTimeInSec, req mem.AccessReq) (handled, progress bool) {
	n := c.nearReal
	read, ok := req.(*mem.ReadReq)
	if !ok {
		return false, false
	}
	if t := n.tasks[read.ID]; t != nil {
		return true, c.nrServe(now, read, t)
	}
	s := n.sent[read.ID]
	if s == nil || s.via >= 0 {
		return false, false
	}
	g := n.homeRead(c, read.ID, read.Address, read.Src)
	if g < 0 {
		return false, false
	}
	// A2: delegate to holder g
	fwd := mem.ReadReqBuilder{}.
		WithSendTime(now).
		WithSrc(c.ToOutside).
		WithDst(n.engines[g].ToOutside).
		WithAddress(read.Address).
		WithByteSize(read.AccessByteSize).
		WithPID(read.PID).
		WithInfo(read.Info).
		Build()
	if err := c.ToOutside.Send(fwd); err != nil {
		return true, false
	}
	n.tasks[fwd.ID] = &nrTask{replyTo: read.Src, rspTo: read.ID}
	s.via, s.delegated = g, true
	n.Stats.Delegated++
	c.ToOutside.Retrieve(now)
	c.forgetSeen(c.firstSeenFromOutsideReq, read.ID)
	return true, true
}

// nrServe: this engine is the holder of a read (hint or delegation).
func (c *Comp) nrServe(now sim.VTimeInSec, req *mem.ReadReq, t *nrTask) bool {
	if !c.canAcceptOwnerOutstanding(1) {
		return false
	}
	probe := mem.ReadReqBuilder{}.
		WithSendTime(now).
		WithSrc(c.ToL2).
		WithDst(c.remoteCacheModules.Find(req.Address)).
		WithAddress(req.Address).
		WithByteSize(req.AccessByteSize).
		WithPID(req.PID).
		WithInfo(req.Info).
		WithLookupOnly().
		Build()
	if err := c.ToL2.Send(probe); err != nil {
		return false
	}
	delete(c.nearReal.tasks, req.ID)
	if c.nrProbes == nil {
		c.nrProbes = make(map[string]*nrProbe)
		c.nrFwd = make(map[string]*nrProbe)
	}
	c.nrProbes[probe.ID] = &nrProbe{req: req, t: t}
	c.ToOutside.Retrieve(now)
	c.forgetSeen(c.firstSeenFromOutsideReq, req.ID)
	return true
}

type nrProbe struct {
	req *mem.ReadReq
	t   *nrTask
}

// nrProbeRsp handles the holder's L2 lookup result.
func (c *Comp) nrProbeRsp(now sim.VTimeInSec, rsp *mem.CacheLookupRsp) bool {
	n := c.nearReal
	pr := c.nrProbes[rsp.RespondTo]
	if rsp.Hit {
		out := mem.DataReadyRspBuilder{}.
			WithSendTime(now).
			WithSrc(c.ToOutside).
			WithDst(pr.t.replyTo).
			WithRspTo(pr.t.rspTo).
			WithData(append([]byte(nil), rsp.Data...)).
			Build()
		if err := c.ToOutside.Send(out); err != nil {
			return false
		}
		n.Stats.HolderHits++
	} else {
		fwd := mem.ReadReqBuilder{}.
			WithSendTime(now).
			WithSrc(c.ToOutside).
			WithDst(c.RemoteRDMAAddressTable.Find(pr.req.Address)).
			WithAddress(pr.req.Address).
			WithByteSize(pr.req.AccessByteSize).
			WithPID(pr.req.PID).
			WithInfo(pr.req.Info).
			Build()
		if err := c.ToOutside.Send(fwd); err != nil {
			return false
		}
		n.Stats.HolderMisses++
		n.noDelegate[fwd.ID] = true
		if s := n.sent[pr.t.rspTo]; s != nil {
			s.missed = true
		}
		c.nrFwd[fwd.ID] = pr
	}
	delete(c.nrProbes, rsp.RespondTo)
	c.ToL2.Retrieve(now)
	return true
}

// nrFwdRsp relays the home's data of a forwarded (missed) read.
func (c *Comp) nrFwdRsp(now sim.VTimeInSec, rsp *mem.DataReadyRsp) bool {
	pr := c.nrFwd[rsp.RespondTo]
	out := mem.DataReadyRspBuilder{}.
		WithSendTime(now).
		WithSrc(c.ToOutside).
		WithDst(pr.t.replyTo).
		WithRspTo(pr.t.rspTo).
		WithData(append([]byte(nil), rsp.Data...)).
		Build()
	if err := c.ToOutside.Send(out); err != nil {
		return false
	}
	delete(c.nrFwd, rsp.RespondTo)
	delete(c.nearReal.noDelegate, rsp.RespondTo)
	c.ToOutside.Retrieve(now)
	return true
}
