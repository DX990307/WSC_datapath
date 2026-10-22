package rdma

import (
	"fmt"
	"math/bits"
	"sort"

	"github.com/sarchlab/akita/v3/sim"
)

// ISCA'27 wafer-related M1 upper bounds (-report-m1w-bound). Statistics
// only: nothing here changes timing.
//
// The tracker sees every requester network transaction (a single-line read,
// a bitmap batch, or a legacy baseline read): its requester GPM r, owner o,
// the lines it carries (demand or prefetch, with the time each demand line
// was first needed), when the requester created it (batch creation or first
// arrival), when it was put on the wire, when it reached the owner, and when
// it completed (the requester's window slot is released). From that it
// computes three families of bounds:
//
//  (a) Regional combining. A far read (hops(r, o) > d, owner outside r's
//      region) would first go to its region's hub GPM H; H keeps an
//      in-flight entry until the owner's response comes back to it, and
//      optionally retains the line for ret ns afterwards (kernel-scoped).
//      A later far read of the same line from the same region finds the
//      entry and is served by H (covered): it saves the long-haul request,
//      the owner read, and the H <-> o part of the bytes x hops. Uncovered
//      far reads pay the detour hops(r, H) + hops(H, o) - hops(r, o).
//      Variants: region partition (q2 = 2 x 2, q3 = 3 x 3), d, ret; plus
//      "turn" (H = the Y-X route's turn tile (x_r, y_o); zero detour when
//      responses retrace the request path) and "path" (idealized in-network
//      combining at the first switch where r's request path meets the path
//      of another in-flight request of the same line, responses retracing
//      the request path). Oracles: another GPM within 1 / 2 / any hops has
//      the line in flight when it is needed.
//  (b) Distance-aware batching. A batch to a far owner is held T ns after
//      creation before it may leave; later transactions of the same
//      (requester, owner, page) created before the held batch leaves join
//      it (up to 8 lines). Counts transactions saved, lines per packet, and
//      the latency added to batches that left before T.
//  (c) RTT-weighted window occupancy. Slot time (send -> completion) and its
//      request leg (send -> owner arrival) per hop count; the window gain of
//      releasing the slot at owner arrival (owner-side credit) or after an
//      owner ack (2 x request leg).
//
// Kernel boundaries are global (a kernel starts on all GPMs once the previous
// one has finished), so a hub entry or an in-flight holder created before the
// latest boundary never covers a read. A remote write kills the line's
// entries.

const (
	m1wBytesPerHop  = 112.0 // 1 request flit + 6 response flits, 16 B flits
	m1wHubProcNS    = 10.0  // relay RDMA pipeline (10 cycles at 1 GHz)
	m1wHopNS        = 35.7  // one-way per hop (half the T14 round trip)
	m1wRTTPerHopNS  = 71.4  // T14 round trip per hop
	m1wSweepEveryNS = 50000.0
	m1wMaxHop       = 13
	m1wMaxBatch     = 8
)

// M1WBoundConfig selects the bound variants.
type M1WBoundConfig struct {
	Ds         []int     // far threshold (hops > d) for region schemes
	RetainNS   []float64 // hub retention after the response (0 = in flight only)
	HoldFixed  []float64 // (b) fixed hold T (ns), all owners
	HoldAlphas []float64 // (b) T = alpha x RTT(h) for h > HoldMinHops
	HoldMin    int       // (b) owners at most this many hops are never held
}

// DefaultM1WBoundConfig is the configuration of the report flag.
func DefaultM1WBoundConfig() M1WBoundConfig {
	return M1WBoundConfig{
		Ds:         []int{2, 3, 4},
		RetainNS:   []float64{0, 1000, 10000},
		HoldFixed:  []float64{100, 300, 1000},
		HoldAlphas: []float64{0.25, 0.5, 1.0},
		HoldMin:    2,
	}
}

// M1WTopology is the 7 x 7 mesh with GPM indices in creation order (row by
// row, skipping the center tile).
type M1WTopology struct {
	Width, Height int
	Coords        [][2]int
	Ports         []sim.Port
	HubPorts      []sim.Port // m1w hub ports (nil when hubs are off)
	index         map[sim.Port]int
	tileGPM       []int // tile -> GPM index or -1
}

// NewM1WTopology builds the topology; coords and ports are indexed by GPM.
func NewM1WTopology(width, height int, coords [][2]int, ports []sim.Port) *M1WTopology {
	if len(coords) != len(ports) {
		panic("m1w topology: coordinates and ports differ in length")
	}
	t := &M1WTopology{
		Width: width, Height: height,
		Coords:  append([][2]int(nil), coords...),
		Ports:   append([]sim.Port(nil), ports...),
		index:   make(map[sim.Port]int, len(ports)),
		tileGPM: make([]int, width*height),
	}
	for i := range t.tileGPM {
		t.tileGPM[i] = -1
	}
	for i, p := range ports {
		t.index[p] = i
		t.tileGPM[coords[i][0]+coords[i][1]*width] = i
	}
	return t
}

// Index returns the GPM index of an RDMA ToOutside port, or -1.
func (t *M1WTopology) Index(p sim.Port) int {
	if t == nil || p == nil {
		return -1
	}
	if i, ok := t.index[p]; ok {
		return i
	}
	return -1
}

// NumGPMs returns the number of GPMs.
func (t *M1WTopology) NumGPMs() int { return len(t.Coords) }

// Hops returns the Manhattan distance between two GPMs.
func (t *M1WTopology) Hops(a, b int) int { return t18Hops(t.Coords[a], t.Coords[b]) }

func (t *M1WTopology) tileOf(g int) int {
	return t.Coords[g][0] + t.Coords[g][1]*t.Width
}

func (t *M1WTopology) tileHops(a, b int) int {
	ax, ay := a%t.Width, a/t.Width
	bx, by := b%t.Width, b/t.Width
	return absInt(ax-bx) + absInt(ay-by)
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// pathTiles returns the tiles of the Y-then-X route from tile a to tile b,
// a and b included, in order.
func (t *M1WTopology) pathTiles(a, b int, out []int) []int {
	x, y := a%t.Width, a/t.Width
	bx, by := b%t.Width, b/t.Width
	out = append(out[:0], a)
	for y != by {
		if by > y {
			y++
		} else {
			y--
		}
		out = append(out, x+y*t.Width)
	}
	for x != bx {
		if bx > x {
			x++
		} else {
			x--
		}
		out = append(out, x+y*t.Width)
	}
	return out
}

// Directed links: tile*4 + dir (0 +x, 1 -x, 2 +y, 3 -y).
func (t *M1WTopology) addRoute(load []uint64, a, b int, yFirst bool, n uint64) {
	x, y := a%t.Width, a/t.Width
	bx, by := b%t.Width, b/t.Width
	stepY := func() {
		for y != by {
			tile := x + y*t.Width
			if by > y {
				load[tile*4+2] += n
				y++
			} else {
				load[tile*4+3] += n
				y--
			}
		}
	}
	stepX := func() {
		for x != bx {
			tile := x + y*t.Width
			if bx > x {
				load[tile*4] += n
				x++
			} else {
				load[tile*4+1] += n
				x--
			}
		}
	}
	if yFirst {
		stepY()
		stepX()
	} else {
		stepX()
		stepY()
	}
}

// ---------------------------------------------------------------- records

type m1wLineKey struct {
	pid  uint32
	line uint64
}

// M1WLine is one line of a transaction.
type M1WLine struct {
	PID    uint32
	Line   uint64
	NeedNS float64
	Demand bool
}

// m1wPending is a cover that is checked when the covering entry's data
// passes the merge point: the later read must reach the merge point (hub or
// switch) before the earlier read's response passes it on its way back.
type m1wPending struct {
	scheme   int
	arriveNS float64 // later read reaches the merge point
	dOut     int     // merge point -> earlier requester hops
}

type m1wHubEntry struct {
	pend    []m1wPending
	startNS float64
	endNS   float64 // 0 while in flight
	creator int
	gen     uint32
}

type m1wHolder struct {
	pend    []m1wPending
	key     m1wLineKey
	r       int
	startNS float64
	endNS   float64 // 0 while in flight
	tiles   []int   // request path tiles r -> o
	gen     uint32
	txn     *m1wTxn
}

type m1wCover struct {
	scheme int
	hubHop int          // hops(r, H) (or r -> combining switch)
	entry  *m1wHubEntry // region / turn schemes
	holder *m1wHolder   // path scheme
}

type m1wTxn struct {
	r, o      int
	hops      int
	createdNS float64
	sendNS    float64
	ownerNS   float64
	lines     []M1WLine
	demand    int
	page      uint64
	pid       uint32
	bitmap    uint64
	hubRefs   []*m1wHubEntry
	holders   []*m1wHolder
	covers    []m1wCover
	detour    []int  // per scheme: max detour hops over the txn's lines
	far       []bool // per scheme: txn has a far demand line
	held      []bool // (b) per hold policy: absorbed into an earlier batch
}

// --------------------------------------------------------------- schemes

const (
	m1wKindRegion = iota
	m1wKindTurn
	m1wKindPath
)

type m1wHubKey struct {
	pid    uint32
	line   uint64
	region int32
}

// M1WSchemeStats are the counters of one combining scheme.
type M1WSchemeStats struct {
	FarReads         uint64
	CoveredSend      uint64 // covered at the time the read left the requester
	CoveredNeed      uint64 // covered at the time the read was first needed
	CoveredInflight  uint64 // covered by an in-flight entry (not retention)
	CoveredVerified  uint64 // covers whose read reached the merge point in time
	DirectBytesHop   float64
	SchemeBytesHop   float64
	DetourHops       float64 // sum over uncovered far demand lines
	HubHopsCovered   float64 // sum hops(r, H) of covered lines
	FullTxns         uint64  // transactions whose demand lines are all covered
	SlotSavedNS      float64 // estimated slot time saved by fully covered txns
	SlotDetourCostNS float64 // slot time added by detours of uncovered far txns
	PeakEntries      uint64
	Load             []uint64 // response lines per directed link
}

type m1wScheme struct {
	name     string
	kind     int
	d        int
	retNS    float64
	regionOf []int // GPM -> region
	hubOf    []int // region -> hub GPM
	entries  map[m1wHubKey]*m1wHubEntry
	stats    M1WSchemeStats
}

type m1wHold struct {
	name  string
	fixed float64
	alpha float64
	open  map[m1wHoldKey]*m1wVBatch
	stats M1WHoldBoundStats
}

type m1wHoldKey struct {
	r, o int
	pid  uint32
	page uint64
}

type m1wVBatch struct {
	vsendNS float64
	bitmap  uint64
}

// M1WHoldBoundStats are the counters of one (b) hold policy.
type M1WHoldBoundStats struct {
	Txns          uint64
	Lines         uint64
	Absorbed      uint64
	AbsorbedLines uint64
	SlotSavedNS   float64
	AddedDelayNS  float64 // sum over demand lines of max(0, created+T - send)
	HeldTxns      uint64
}

// M1WOccStats are the (c) counters per hop count.
type M1WOccStats struct {
	Txns      [m1wMaxHop]uint64
	Lines     [m1wMaxHop]uint64
	SlotNS    [m1wMaxHop]float64
	ReqLegNS  [m1wMaxHop]float64
	OwnerSeen [m1wMaxHop]uint64
}

// M1WBound is the platform-wide tracker (the simulation is single-threaded).
type M1WBound struct {
	topo       *M1WTopology
	cfg        M1WBoundConfig
	txns       map[string]*m1wTxn
	schemes    []*m1wScheme
	holds      []*m1wHold
	holders    map[m1wLineKey][]*m1wHolder
	gens       map[m1wLineKey]uint32
	boundary   float64
	lastSweep  float64
	pathScheme int

	// oracle: another GPM within radius has the line in flight
	OracleReads   uint64
	OracleSend    [3]uint64 // radius 1, 2, any
	OracleNeed    [3]uint64
	DemandLines   uint64
	PrefetchLines uint64
	Txns          uint64
	Completed     uint64
	Writes        uint64
	DirectLoad    []uint64 // response lines per link, Y-X routes
	ReverseLoad   []uint64 // response lines per link, X-Y (reverse) routes
	Occ           M1WOccStats

	scratch []int
}

var m1wBound *M1WBound

// EnableM1WBound turns the bound on for the whole platform.
func EnableM1WBound(topo *M1WTopology, cfg M1WBoundConfig) *M1WBound {
	b := &M1WBound{
		topo:     topo,
		cfg:      cfg,
		txns:     make(map[string]*m1wTxn),
		holders:  make(map[m1wLineKey][]*m1wHolder),
		gens:     make(map[m1wLineKey]uint32),
		boundary: -1,
	}
	nLinks := topo.Width * topo.Height * 4
	b.DirectLoad = make([]uint64, nLinks)
	b.ReverseLoad = make([]uint64, nLinks)
	for _, part := range []int{2, 3} {
		regionOf, hubOf := m1wPartition(topo, part)
		for _, d := range cfg.Ds {
			for _, ret := range cfg.RetainNS {
				b.schemes = append(b.schemes, &m1wScheme{
					name:     fmt.Sprintf("q%d_d%d_ret%g", part, d, ret),
					kind:     m1wKindRegion,
					d:        d,
					retNS:    ret,
					regionOf: regionOf,
					hubOf:    hubOf,
				})
			}
		}
	}
	for _, ret := range []float64{0, 1000} {
		b.schemes = append(b.schemes, &m1wScheme{
			name: fmt.Sprintf("turn_ret%g", ret), kind: m1wKindTurn, retNS: ret,
		})
	}
	b.pathScheme = len(b.schemes)
	b.schemes = append(b.schemes, &m1wScheme{name: "path", kind: m1wKindPath})
	for _, s := range b.schemes {
		s.entries = make(map[m1wHubKey]*m1wHubEntry)
		s.stats.Load = make([]uint64, nLinks)
	}
	for _, t := range cfg.HoldFixed {
		b.holds = append(b.holds, &m1wHold{name: fmt.Sprintf("fixed%g", t), fixed: t})
	}
	for _, a := range cfg.HoldAlphas {
		b.holds = append(b.holds, &m1wHold{
			name: fmt.Sprintf("rtt%g_min%d", a, cfg.HoldMin), alpha: a})
	}
	for _, h := range b.holds {
		h.open = make(map[m1wHoldKey]*m1wVBatch)
	}
	m1wBound = b
	return b
}

// DisableM1WBound turns the bound off (tests).
func DisableM1WBound() { m1wBound = nil }

// CurrentM1WBound returns the platform's tracker, or nil when off.
func CurrentM1WBound() *M1WBound { return m1wBound }

// m1wPartition splits the mesh into part x part regions and picks each
// region's hub: the GPM minimizing the summed hops to the region's GPMs
// (ties: closer to the mesh center, then lower index).
func m1wPartition(t *M1WTopology, part int) (regionOf, hubOf []int) {
	bounds := func(n int) []int {
		// cut points: part ranges as equal as possible, larger in the middle
		switch {
		case part == 2:
			return []int{0, (n + 1) / 2, n}
		case part == 3 && n == 7:
			return []int{0, 2, 5, 7}
		}
		cuts := []int{0}
		for i := 1; i < part; i++ {
			cuts = append(cuts, i*n/part)
		}
		return append(cuts, n)
	}
	bx, by := bounds(t.Width), bounds(t.Height)
	rangeOf := func(cuts []int, v int) int {
		for i := 0; i+1 < len(cuts); i++ {
			if v >= cuts[i] && v < cuts[i+1] {
				return i
			}
		}
		return len(cuts) - 2
	}
	regionOf = make([]int, t.NumGPMs())
	members := make([][]int, part*part)
	for g, c := range t.Coords {
		r := rangeOf(by, c[1])*part + rangeOf(bx, c[0])
		regionOf[g] = r
		members[r] = append(members[r], g)
	}
	cx, cy := t.Width/2, t.Height/2
	hubOf = make([]int, part*part)
	for r, ms := range members {
		best, bestCost, bestC := -1, 1<<30, 1<<30
		for _, h := range ms {
			cost := 0
			for _, m := range ms {
				cost += t.Hops(h, m)
			}
			cc := absInt(t.Coords[h][0]-cx) + absInt(t.Coords[h][1]-cy)
			if cost < bestCost || (cost == bestCost && cc < bestC) {
				best, bestCost, bestC = h, cost, cc
			}
		}
		hubOf[r] = best
	}
	return regionOf, hubOf
}

func m1wNS(t sim.VTimeInSec) float64 { return float64(t) * 1e9 }

func (b *M1WBound) valid(startNS float64, gen uint32, key m1wLineKey) bool {
	return startNS > b.boundary && gen == b.gens[key]
}

// hubFor returns the hub GPM of read r -> o under scheme s, or -1 when the
// read goes direct; region is the entry key's region.
func (b *M1WBound) hubFor(s *m1wScheme, r, o int) (hub, region int) {
	switch s.kind {
	case m1wKindRegion:
		reg := s.regionOf[r]
		if b.topo.Hops(r, o) <= s.d || s.regionOf[o] == reg {
			return -1, -1
		}
		return s.hubOf[reg], reg
	case m1wKindTurn:
		tile := b.topo.Coords[r][0] + b.topo.Coords[o][1]*b.topo.Width
		h := b.topo.tileGPM[tile]
		if h < 0 || h == r || h == o {
			return -1, -1
		}
		return h, h
	}
	return -1, -1
}

// Send records a network transaction put on the wire by requester r.
func (b *M1WBound) Send(id string, r, o int, lines []M1WLine,
	createdNS, nowNS float64) {
	if r < 0 || o < 0 || r == o || len(lines) == 0 {
		return
	}
	b.maybeSweep(nowNS)
	t := &m1wTxn{r: r, o: o, hops: b.topo.Hops(r, o), createdNS: createdNS,
		sendNS: nowNS, lines: lines, pid: lines[0].PID,
		page: lines[0].Line &^ (remotePageBytes - 1)}
	t.detour = make([]int, len(b.schemes))
	t.far = make([]bool, len(b.schemes))
	t.held = make([]bool, len(b.holds))
	b.txns[id] = t
	b.Txns++
	for _, l := range lines {
		t.bitmap |= uint64(1) << ((l.Line & (remotePageBytes - 1)) / remoteLineBytes)
		if l.Demand {
			t.demand++
			b.DemandLines++
		} else {
			b.PrefetchLines++
		}
	}
	for _, l := range lines {
		b.sendLine(t, l, nowNS)
	}
	b.holdSend(t, nowNS)
}

func (b *M1WBound) sendLine(t *m1wTxn, l M1WLine, nowNS float64) {
	key := m1wLineKey{l.PID, l.Line}
	gen := b.gens[key]
	topo := b.topo
	r, o := t.r, t.o
	hops := t.hops
	if l.Demand {
		topo.addRoute(b.DirectLoad, topo.tileOf(o), topo.tileOf(r), true, 1)
		topo.addRoute(b.ReverseLoad, topo.tileOf(o), topo.tileOf(r), false, 1)
		// oracles over the in-flight holders of the line
		b.OracleReads++
		var sendHit, needHit [3]bool
		for _, h := range b.holders[key] {
			if h.r == r || !b.valid(h.startNS, h.gen, key) {
				continue
			}
			d := topo.Hops(h.r, r)
			for k, rad := range [3]int{1, 2, 1 << 20} {
				if d > rad {
					continue
				}
				sendHit[k] = true
				if h.startNS <= l.NeedNS {
					needHit[k] = true
				}
			}
		}
		for k := 0; k < 3; k++ {
			if sendHit[k] {
				b.OracleSend[k]++
			}
			if needHit[k] {
				b.OracleNeed[k]++
			}
		}
	}
	for si, s := range b.schemes {
		if s.kind == m1wKindPath {
			if l.Demand {
				b.pathLine(si, s, t, l, key, nowNS)
			}
			continue
		}
		hub, region := b.hubFor(s, r, o)
		if l.Demand {
			s.stats.DirectBytesHop += float64(hops) * m1wBytesPerHop
		}
		if hub < 0 {
			if l.Demand {
				s.stats.SchemeBytesHop += float64(hops) * m1wBytesPerHop
				topo.addRoute(s.stats.Load, topo.tileOf(o), topo.tileOf(r), true, 1)
			}
			continue
		}
		hk := m1wHubKey{l.PID, l.Line, int32(region)}
		e := s.entries[hk]
		live := e != nil && b.valid(e.startNS, e.gen, key) &&
			(e.endNS == 0 || nowNS <= e.endNS+s.retNS)
		hr := topo.Hops(r, hub)
		if l.Demand {
			s.stats.FarReads++
			t.far[si] = true
		}
		if live && e.creator != r {
			if l.Demand {
				s.stats.CoveredSend++
				if e.startNS <= l.NeedNS {
					s.stats.CoveredNeed++
				}
				if e.endNS == 0 {
					s.stats.CoveredInflight++
				}
				dOut := topo.Hops(hub, e.creator)
				arrive := nowNS + m1wHopNS*float64(hr)
				if e.endNS == 0 {
					e.pend = append(e.pend, m1wPending{si, arrive, dOut})
				} else if arrive <= e.endNS-m1wHopNS*float64(dOut)+s.retNS {
					s.stats.CoveredVerified++
				}
				s.stats.HubHopsCovered += float64(hr)
				s.stats.SchemeBytesHop += float64(hr) * m1wBytesPerHop
				topo.addRoute(s.stats.Load, topo.tileOf(hub), topo.tileOf(r), true, 1)
				t.covers = append(t.covers, m1wCover{scheme: si, hubHop: hr, entry: e})
			}
			continue
		}
		if live {
			// The requester's own earlier entry (no combining benefit).
			if l.Demand {
				b.chargeFar(s, t, si, r, o, hub, hops)
			}
			continue
		}
		ne := &m1wHubEntry{startNS: nowNS, creator: r, gen: gen}
		s.entries[hk] = ne
		t.hubRefs = append(t.hubRefs, ne)
		if uint64(len(s.entries)) > s.stats.PeakEntries {
			s.stats.PeakEntries = uint64(len(s.entries))
		}
		if l.Demand {
			b.chargeFar(s, t, si, r, o, hub, hops)
		}
	}
	// the transaction becomes an in-flight holder of the line
	h := &m1wHolder{key: key, r: r, startNS: nowNS, gen: gen, txn: t,
		tiles: topo.pathTiles(topo.tileOf(r), topo.tileOf(o), nil)}
	b.holders[key] = append(b.holders[key], h)
	t.holders = append(t.holders, h)
}

func (b *M1WBound) chargeFar(s *m1wScheme, t *m1wTxn, si, r, o, hub, hops int) {
	topo := b.topo
	via := topo.Hops(r, hub) + topo.Hops(hub, o)
	s.stats.SchemeBytesHop += float64(via) * m1wBytesPerHop
	det := via - hops
	s.stats.DetourHops += float64(det)
	if det > t.detour[si] {
		t.detour[si] = det
	}
	// turn hub: o -> turn tile is X only, turn tile -> r is Y only, so the
	// two Y-X legs retrace the request path.
	topo.addRoute(s.stats.Load, topo.tileOf(o), topo.tileOf(hub), true, 1)
	topo.addRoute(s.stats.Load, topo.tileOf(hub), topo.tileOf(r), true, 1)
}

// pathLine: idealized in-network combining. The read is covered at the first
// tile of its request path (from r toward o, o excluded) that lies on the
// path of another valid in-flight request of the same line; the response
// retraces request paths.
func (b *M1WBound) pathLine(si int, s *m1wScheme, t *m1wTxn, l M1WLine,
	key m1wLineKey, nowNS float64) {
	topo := b.topo
	s.stats.FarReads++
	s.stats.DirectBytesHop += float64(t.hops) * m1wBytesPerHop
	t.far[si] = true
	b.scratch = topo.pathTiles(topo.tileOf(t.r), topo.tileOf(t.o), b.scratch)
	path := b.scratch
	bestPos := -1
	var best *m1wHolder
	for _, h := range b.holders[key] {
		if h.r == t.r || h.endNS != 0 || !b.valid(h.startNS, h.gen, key) {
			continue
		}
		for pos := 0; pos < len(path)-1; pos++ {
			if bestPos >= 0 && pos >= bestPos {
				break
			}
			if m1wContains(h.tiles, path[pos]) {
				bestPos, best = pos, h
				break
			}
		}
	}
	if best == nil {
		s.stats.SchemeBytesHop += float64(t.hops) * m1wBytesPerHop
		topo.addRoute(s.stats.Load, topo.tileOf(t.o), topo.tileOf(t.r), false, 1)
		return
	}
	s.stats.CoveredSend++
	s.stats.CoveredInflight++
	if best.startNS <= l.NeedNS {
		s.stats.CoveredNeed++
	}
	best.pend = append(best.pend, m1wPending{si,
		nowNS + m1wHopNS*float64(bestPos), topo.tileHops(path[bestPos], topo.tileOf(best.r))})
	s.stats.HubHopsCovered += float64(bestPos)
	s.stats.SchemeBytesHop += float64(bestPos) * m1wBytesPerHop
	topo.addRoute(s.stats.Load, path[bestPos], topo.tileOf(t.r), false, 1)
	t.covers = append(t.covers, m1wCover{scheme: si, hubHop: bestPos, holder: best})
}

func m1wContains(list []int, v int) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func (b *M1WBound) holdT(h *m1wHold, hops int) float64 {
	if h.alpha > 0 {
		if hops <= b.cfg.HoldMin {
			return 0
		}
		return h.alpha * t18RTTNS(hops)
	}
	return h.fixed
}

func (b *M1WBound) holdSend(t *m1wTxn, nowNS float64) {
	n := bits.OnesCount64(t.bitmap)
	for hi, h := range b.holds {
		key := m1wHoldKey{t.r, t.o, t.pid, t.page}
		h.stats.Txns++
		h.stats.Lines += uint64(n)
		vb := h.open[key]
		if vb != nil && t.createdNS <= vb.vsendNS &&
			bits.OnesCount64(vb.bitmap|t.bitmap) <= m1wMaxBatch {
			vb.bitmap |= t.bitmap
			h.stats.Absorbed++
			h.stats.AbsorbedLines += uint64(n)
			t.held[hi] = true
			continue
		}
		T := b.holdT(h, t.hops)
		vsend := nowNS
		if t.createdNS+T > vsend {
			h.stats.AddedDelayNS += (t.createdNS + T - nowNS) * float64(t.demand)
			h.stats.HeldTxns++
			vsend = t.createdNS + T
		}
		if T > 0 {
			h.open[key] = &m1wVBatch{vsendNS: vsend, bitmap: t.bitmap}
		} else {
			delete(h.open, key)
		}
	}
}

// OwnerArrive records the arrival of a transaction at its owner.
func (b *M1WBound) OwnerArrive(id string, nowNS float64) {
	if t := b.txns[id]; t != nil && t.ownerNS == 0 {
		t.ownerNS = nowNS
	}
}

// Complete records that the requester's window slot of id was released.
func (b *M1WBound) Complete(id string, nowNS float64) {
	t := b.txns[id]
	if t == nil {
		return
	}
	delete(b.txns, id)
	b.Completed++
	for _, e := range t.hubRefs {
		e.endNS = nowNS
		b.verify(e.pend, nowNS)
		e.pend = nil
	}
	for _, h := range t.holders {
		h.endNS = nowNS
		b.verify(h.pend, nowNS)
		h.pend = nil
	}
	b.dropHolders(t)
	slot := nowNS - t.sendNS
	hb := t.hops
	if hb >= m1wMaxHop {
		hb = m1wMaxHop - 1
	}
	b.Occ.Txns[hb]++
	b.Occ.Lines[hb] += uint64(len(t.lines))
	b.Occ.SlotNS[hb] += slot
	if t.ownerNS > 0 {
		b.Occ.ReqLegNS[hb] += t.ownerNS - t.sendNS
		b.Occ.OwnerSeen[hb]++
	} else {
		b.Occ.ReqLegNS[hb] += slot / 2
	}
	for hi, h := range b.holds {
		if t.held[hi] {
			h.stats.SlotSavedNS += slot
		}
	}
	if t.demand == 0 {
		return
	}
	covered := make(map[int]int, 4)
	maxEst := make(map[int]float64, 4)
	for _, c := range t.covers {
		covered[c.scheme]++
		est := t18RTTNS(c.hubHop) + m1wHubProcNS
		if c.entry != nil {
			switch {
			case c.entry.endNS == 0:
				est = slot // still in flight: no saving
			case c.entry.endNS > t.sendNS:
				est += c.entry.endNS - t.sendNS
			}
		} else if c.holder != nil {
			if c.holder.endNS == 0 {
				est = slot
			} else if c.holder.endNS > t.sendNS {
				est = c.holder.endNS - t.sendNS + m1wRTTPerHopNS*float64(c.hubHop)
			}
		}
		if est > maxEst[c.scheme] {
			maxEst[c.scheme] = est
		}
	}
	for si, s := range b.schemes {
		if covered[si] == t.demand {
			s.stats.FullTxns++
			if maxEst[si] < slot {
				s.stats.SlotSavedNS += slot - maxEst[si]
			}
		} else if t.far[si] && t.detour[si] > 0 {
			s.stats.SlotDetourCostNS += m1wRTTPerHopNS*float64(t.detour[si]) + m1wHubProcNS
		}
	}
}

func (b *M1WBound) verify(pend []m1wPending, endNS float64) {
	for _, p := range pend {
		if p.arriveNS <= endNS-m1wHopNS*float64(p.dOut) {
			b.schemes[p.scheme].stats.CoveredVerified++
		}
	}
}

func (b *M1WBound) dropHolders(t *m1wTxn) {
	for _, h := range t.holders {
		list := b.holders[h.key]
		for i, x := range list {
			if x == h {
				list = append(list[:i:i], list[i+1:]...)
				break
			}
		}
		if len(list) == 0 {
			delete(b.holders, h.key)
		} else {
			b.holders[h.key] = list
		}
	}
	t.holders = nil
}

// Write kills every entry of a line written remotely.
func (b *M1WBound) Write(pid uint32, line uint64) {
	b.Writes++
	b.gens[m1wLineKey{pid, line}]++
}

// KernelBoundary records a kernel launch (global).
func (b *M1WBound) KernelBoundary(nowNS float64) {
	if nowNS > b.boundary {
		b.boundary = nowNS
	}
}

func (b *M1WBound) maybeSweep(nowNS float64) {
	if nowNS-b.lastSweep < m1wSweepEveryNS {
		return
	}
	b.lastSweep = nowNS
	for _, s := range b.schemes {
		for k, e := range s.entries {
			if e.startNS <= b.boundary ||
				(e.endNS != 0 && e.endNS+s.retNS < nowNS) {
				delete(s.entries, k)
			}
		}
	}
	for _, h := range b.holds {
		for k, vb := range h.open {
			if vb.vsendNS < nowNS {
				delete(h.open, k)
			}
		}
	}
	if len(b.gens) > 1<<20 {
		// generations only matter for live entries; entries created after a
		// reset carry generation 0 again, so a reset is safe at a boundary
		// with no live entries only. Keep it bounded conservatively.
		live := false
		for _, s := range b.schemes {
			if len(s.entries) > 0 {
				live = true
				break
			}
		}
		if !live && len(b.holders) == 0 {
			b.gens = make(map[m1wLineKey]uint32)
		}
	}
}

// ---------------------------------------------------------------- report

func m1wMax(load []uint64) uint64 {
	var m uint64
	for _, v := range load {
		if v > m {
			m = v
		}
	}
	return m
}

func m1wSum(load []uint64) uint64 {
	var s uint64
	for _, v := range load {
		s += v
	}
	return s
}

// Rows returns (name, value) rows of the bound.
func (b *M1WBound) Rows() [][2]string {
	div := func(a, c float64) float64 {
		if c == 0 {
			return 0
		}
		return a / c
	}
	var rows [][2]string
	put := func(name string, v float64) {
		rows = append(rows, [2]string{name, fmt.Sprintf("%.6g", v)})
	}
	put("txns", float64(b.Txns))
	put("completed", float64(b.Completed))
	put("demand_lines", float64(b.DemandLines))
	put("prefetch_lines", float64(b.PrefetchLines))
	put("lines_per_txn", div(float64(b.DemandLines+b.PrefetchLines), float64(b.Txns)))
	put("remote_writes", float64(b.Writes))
	names := [3]string{"r1", "r2", "any"}
	for k := 0; k < 3; k++ {
		put("oracle_"+names[k]+"_inflight_send_frac",
			div(float64(b.OracleSend[k]), float64(b.OracleReads)))
		put("oracle_"+names[k]+"_inflight_need_frac",
			div(float64(b.OracleNeed[k]), float64(b.OracleReads)))
	}
	dmax := float64(m1wMax(b.DirectLoad))
	put("direct_max_link_lines", dmax)
	put("reverse_max_link_lines", float64(m1wMax(b.ReverseLoad)))
	put("direct_total_link_lines", float64(m1wSum(b.DirectLoad)))
	for _, s := range b.schemes {
		p := "a_" + s.name + "_"
		st := s.stats
		put(p+"far_frac", div(float64(st.FarReads), float64(b.DemandLines)))
		put(p+"covered_send_frac", div(float64(st.CoveredSend), float64(b.DemandLines)))
		put(p+"covered_need_frac", div(float64(st.CoveredNeed), float64(b.DemandLines)))
		put(p+"covered_inflight_frac", div(float64(st.CoveredInflight), float64(b.DemandLines)))
		put(p+"covered_verified_frac", div(float64(st.CoveredVerified), float64(b.DemandLines)))
		put(p+"covered_of_far", div(float64(st.CoveredSend), float64(st.FarReads)))
		put(p+"bytes_hop_ratio", div(st.SchemeBytesHop, st.DirectBytesHop))
		put(p+"detour_hops_per_far", div(st.DetourHops,
			float64(st.FarReads-st.CoveredSend)))
		put(p+"hub_hops_per_covered", div(st.HubHopsCovered, float64(st.CoveredSend)))
		put(p+"max_link_ratio", div(float64(m1wMax(st.Load)), dmax))
		put(p+"total_link_ratio", div(float64(m1wSum(st.Load)),
			float64(m1wSum(b.DirectLoad))))
		put(p+"full_txn_frac", div(float64(st.FullTxns), float64(b.Completed)))
		put(p+"peak_entries", float64(st.PeakEntries))
		var slot float64
		for _, v := range b.Occ.SlotNS {
			slot += v
		}
		put(p+"slot_saved_frac", div(st.SlotSavedNS, slot))
		put(p+"slot_detour_cost_frac", div(st.SlotDetourCostNS, slot))
		put(p+"slot_net_frac", div(st.SlotSavedNS-st.SlotDetourCostNS, slot))
	}
	for _, h := range b.holds {
		p := "b_" + h.name + "_"
		st := h.stats
		var slot float64
		for _, v := range b.Occ.SlotNS {
			slot += v
		}
		put(p+"txn_saved_frac", div(float64(st.Absorbed), float64(st.Txns)))
		put(p+"lines_per_txn", div(float64(st.Lines), float64(st.Txns-st.Absorbed)))
		put(p+"slot_saved_frac", div(st.SlotSavedNS, slot))
		put(p+"held_txn_frac", div(float64(st.HeldTxns), float64(st.Txns)))
		put(p+"added_delay_ns_per_demand_line", div(st.AddedDelayNS, float64(b.DemandLines)))
	}
	var slot, leg, txns float64
	for h := 0; h < m1wMaxHop; h++ {
		slot += b.Occ.SlotNS[h]
		leg += b.Occ.ReqLegNS[h]
		txns += float64(b.Occ.Txns[h])
	}
	put("c_slot_ns_per_txn", div(slot, txns))
	put("c_req_leg_ns_per_txn", div(leg, txns))
	put("c_gain_release_at_owner", div(slot, leg))
	put("c_gain_release_after_ack", div(slot, 2*leg))
	var farSlot, farTx float64
	for h := 0; h < m1wMaxHop; h++ {
		if h >= 6 {
			farSlot += b.Occ.SlotNS[h]
			farTx += float64(b.Occ.Txns[h])
		}
		put(fmt.Sprintf("c_h%d_txns", h), float64(b.Occ.Txns[h]))
		put(fmt.Sprintf("c_h%d_slot_ns_mean", h), div(b.Occ.SlotNS[h], float64(b.Occ.Txns[h])))
		put(fmt.Sprintf("c_h%d_req_leg_ns_mean", h), div(b.Occ.ReqLegNS[h], float64(b.Occ.Txns[h])))
		put(fmt.Sprintf("c_h%d_slot_share", h), div(b.Occ.SlotNS[h], slot))
	}
	put("c_far6_txn_share", div(farTx, txns))
	put("c_far6_slot_share", div(farSlot, slot))
	return rows
}

// SchemeNames lists the (a) schemes (tests).
func (b *M1WBound) SchemeNames() []string {
	var out []string
	for _, s := range b.schemes {
		out = append(out, s.name)
	}
	sort.Strings(out)
	return out
}

// Scheme returns the counters of a named (a) scheme, or nil.
func (b *M1WBound) Scheme(name string) *M1WSchemeStats {
	for _, s := range b.schemes {
		if s.name == name {
			return &s.stats
		}
	}
	return nil
}

// HubOf returns the hub of GPM g in a q<part> partition (tests).
func (b *M1WBound) HubOf(part, g int) int {
	regionOf, hubOf := m1wPartition(b.topo, part)
	return hubOf[regionOf[g]]
}
