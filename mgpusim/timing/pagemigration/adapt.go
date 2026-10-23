package pagemigration

// ISCA27 P21: one adaptive placement decision for every 4 KB page.
//
// Every page that a vector address translator touches gets one tracking
// entry (adaptState): per-1 KB-block access counts by GPM (the access
// counter, remote and the owner's own L1V misses), the set of GPMs that read
// it for the first time and when (the translation-side signal: a GPM's first
// read of a page needs a page walk), and whether it was written in the
// current kernel. One benefit/cost model then picks, for the page, one action
// out of an action set:
//
//	stay       nothing
//	move       the whole page to its dominant reader
//	block      each 1 KB block to its own dominant reader (others stay)
//	spread     the page's four 1 KB blocks to four tiles of the ring around the
//	           centre ("ring 12"; hot data concentrated on one owner) or to
//	           hashed GPMs (hot data already spread over many owners)
//	replicate  four read-only copies (tiles 8 / 12 / 36 / 40); each reader
//	           reads the nearest copy; any write or kernel boundary collapses
//	           them
//	predict    (a source of targets, not a different motion) the target of a
//	           page that nobody touched yet comes from a per-buffer rule
//	           learned from earlier decisions; such a page only pays the copy
//	           (no TLB shootdown, no held accesses, no software latency)
//
// Latency model (ns per access): local 0, remote Base + Hop * hops + Q(home)
// where Q is a load-proportional queueing term. benefit = expected accesses
// x latency saved; cost = Fixed (software + shootdown + holding, once per
// migrated page) + copied lines x (Base + Hop * hops) x CopyWeight. An action
// is taken when benefit > Alpha * cost; the best (benefit - Alpha * cost)
// wins. Expected accesses: for move / block the accesses already counted
// (ski rental: a page that had n accesses is expected to have about n more);
// for spread / replicate (decided at the first-read signal, when few
// accesses have been counted) 64 lines x number of readers (with M3 each
// reader fetches each line about once).
//
// The action switches only remove actions from the set (ablation). With
// CostModel off the old fixed counter threshold triggers move / block (to the
// dominant reader) and the hot actions are taken without the cost test.
//
// Mechanics. A move / block / spread of a touched page uses the normal
// migration (software batch, shootdown, drain, copy, remap) into a fresh
// frame; the four 1 KB blocks of the fresh frame can have different homes
// (blockHome table, consulted by the L1 finders, the RDMA address table and
// the owner side; see akkalat runner placement.go), so no cache ever holds a
// line of a frame whose home changes. A page nobody touched yet (no address
// translator ever issued an access to its frame, so no cache holds it) is
// re-homed in place: its blocks are copied by the copy engines (charging the
// traffic) and the blockHome entries are installed when the copy ends, if the
// page is still untouched (otherwise the pre-placement is dropped).
// Replicas are fresh frames on the replica GPMs; reads of a vector address
// translator are redirected to the nearest copy at Resolve; a write holds
// for the shootdown latency and collapses the replicas (later accesses use
// the master frame, the replica frames are retired once their in-flight
// accesses drained); every kernel boundary collapses all replicas (host
// copies between kernels write the master frame).

import (
	"fmt"
	"io"
	"math"
	"math/bits"
	"sort"

	"github.com/sarchlab/akita/v3/mem/vm"
	"github.com/sarchlab/akita/v3/sim"
)

const (
	log2Block     = 10
	blocksPerPage = 4
	blockLines    = 16
	pageLines     = 64
)

// RingTiles are the 12 GPM tiles at most two hops from the centre tile 24.
var RingTiles = []int{10, 16, 17, 18, 22, 23, 25, 26, 30, 31, 32, 38}

// AdaptConfig holds the P21 parameters (all off by default).
type AdaptConfig struct {
	Enabled bool
	// action set
	Move, Block, Spread, Replicate, Predict bool
	// CostModel false: fixed threshold triggers move / block, hot actions
	// are taken without the cost test, pre-placement without the test.
	CostModel bool
	// FirstTouch (baseline, needs no migration): the first vector access
	// to a page nobody touched yet makes the accessing GPM its home.
	FirstTouch bool

	Alpha, BaseNS, HopNS, QueueNS, FixedNS, CopyWeight float64
	EvalEvery                                          uint32
	MinBlockSamples                                    uint32

	HotK        int
	HotWindowNS uint64
	ConcShare   float64 // buffer: most common device share >= this -> concentrated
	SpreadOther string  // hot, not concentrated: "stay" or "hash"

	PropSmallPages int     // hot buffers up to this size propagate to all pages
	PropGate       float64 // larger buffers: hot / touched pages >= this

	PredictM      int // consecutive hits to trust a rule
	PredictWindow int // pages pre-placed around each confirmed observation

	ReplicaTiles []int
	// HotFutureReaders: readers a hot page is expected to have (spread /
	// replicate benefit); ReplicaBudget: replica bytes per GPM (0 = no
	// limit; the oldest replicated page holding a copy there is dropped);
	// BatchAmort: a migration triggered together with a predicted batch pays
	// FixedNS / BatchAmort.
	HotFutureReaders int
	ReplicaBudget    uint64
	BatchAmort       float64
	// PredictTouched: confident buffer rules also trigger (batched)
	// migrations of touched neighbours, not only pre-place untouched ones.
	PredictTouched bool
	// CopyFirst: a touched page is copied while its old frame stays mapped;
	// only the switch (shootdown, drain, recopy of blocks written meanwhile)
	// holds accesses.
	CopyFirst bool
	// EarlyObserve: a page whose first EvalEvery counted accesses have a
	// dominant GPM is an observation for its buffer's rule (not only pages
	// that are moved).
	EarlyObserve bool
	// HoldPrePlace: an access to a page whose pre-placement copy is running
	// waits for it (its blocks go to the front of the copy queue) instead of
	// cancelling the pre-placement.
	HoldPrePlace bool
	// ReplicateAny: hot read-only pages of buffers spread over many owners
	// may be replicated too (not only concentrated buffers).
	ReplicateAny bool
	// CountHot: a page whose counted accesses have no dominant GPM and come
	// from at least HotK GPMs is hot (written shared pages, which the
	// first-read signal does not see).
	CountHot bool

	// ReplicaList (oracle region replication): original physical page ->
	// replica devices. A listed page gets zero-cost read-only copies (fresh
	// frames filled functionally from the master, as if the host copy had
	// written several copies) on its first access in a kernel; vector reads
	// go to the nearest copy; a write collapses them (no new copies in that
	// kernel); every kernel boundary collapses them.
	ReplicaList map[uint64][]uint64
}

// DefaultAdaptConfig returns the defaults (the global parameters chosen
// offline, results_index/adapt/offline.md).
func DefaultAdaptConfig() AdaptConfig {
	return AdaptConfig{
		Move: true, Block: true, Spread: true, Predict: true,
		CostModel:        true,
		Alpha:            2,
		BaseNS:           198,
		HopNS:            72,
		QueueNS:          0,
		FixedNS:          2000,
		CopyWeight:       1,
		EvalEvery:        8,
		MinBlockSamples:  4,
		HotK:             4,
		HotWindowNS:      2000,
		ConcShare:        0.3,
		SpreadOther:      "stay",
		PropSmallPages:   256,
		PropGate:         0.5,
		PredictM:         2,
		PredictWindow:    512,
		ReplicaTiles:     []int{8, 12, 36, 40},
		HotFutureReaders: 48,
		ReplicaBudget:    64 << 20,
		BatchAmort:       16,
		PredictTouched:   false,
		CopyFirst:        true,
		EarlyObserve:     true,
		HoldPrePlace:     true,
		ReplicateAny:     true,
		CountHot:         true,
	}
}

// Action kinds.
const (
	actStay = iota
	actMove
	actBlock
	actSpread
	actReplicate
	numActs
)

// ActionNames name the actions in the statistics.
var ActionNames = [numActs]string{"stay", "move", "block", "spread", "replicate"}

// reader-distribution buckets of a decision (for the "distribution ->
// action" table): readers 1, 2-3, 4-15, >= 16.
const numBuckets = 4

func readerBucket(n int) int {
	switch {
	case n <= 1:
		return 0
	case n <= 3:
		return 1
	case n <= 15:
		return 2
	}
	return 3
}

// AdaptStats are the P21 statistics.
type AdaptStats struct {
	Evaluations  uint64
	Decided      [numActs][numBuckets]uint64 // pages, by action and reader bucket
	BlocksMoved  [numActs]uint64             // 1 KB blocks whose home changed
	HotDetected  uint64
	HotBuffers   uint64
	Propagated   uint64 // pages the hot buffer propagation acted on
	PrePlaced    uint64 // untouched pages re-homed in place (all reasons)
	PreBlocks    uint64
	PreRaced     uint64 // pre-placements dropped (page touched during the copy)
	PreByPredict uint64
	PreByHot     uint64
	PredictObs   uint64
	PredictHits  uint64
	PredictConf  uint64 // times a buffer became confident
	PredictMiss  uint64 // misses while confident (fallback)
	PreFallback  uint64 // pre-placed pages later moved by the counter
	ReplicaOn    uint64
	ReplicaAbort uint64
	ReplicaWrite uint64 // collapses by a write
	ReplicaKern  uint64 // collapses at a kernel boundary
	ReplicaReads uint64 // reads redirected to a replica
	FirstTouch   uint64 // pages homed by first touch
	CopyBytes    uint64 // bytes copied by adaptive actions
	ReplCopy     uint64 // of which for replicas
	ReplInvals   uint64 // invalidation messages sent to replica holders
	ReplEvict    uint64 // replicated pages dropped for the capacity budget
	ReplPeak     uint64 // largest replica bytes on one GPM
	BatchTrig    uint64 // touched pages migrated together with a predicted batch
	Kernels      uint64
	// copy-first
	PreCopies    uint64 // migrations copied before the switch
	Recopies     uint64 // of which recopied dirty blocks at the switch
	RecopyBlocks uint64
	// pre-placement
	PreHeld      uint64 // accesses held on a running pre-placement
	PreHeldPages uint64
	PreCostSkip  uint64 // untouched predicted pages skipped by the cost test
	PreTouched   uint64 // predicted pages skipped because already touched
	PreHomeOK    uint64 // predicted pages already at the predicted home
	EarlyObs     uint64 // observations from the early signal
	CountHotN    uint64 // pages found hot by the counters
}

type adaptState struct {
	buf *buffer
	idx int // page index in its buffer

	bc [blocksPerPage][]uint32
	n  uint32

	readMask uint64
	readT    []float64

	hot, hotHandled bool
	fixed           bool
	preplacing      bool
	preplaced       bool
	writeKernel     int
	writeSeq        uint64

	replicas  []replica
	replHome  uint64
	holdUntil sim.VTimeInSec
	orig      uint64

	act      int
	blockDst [blocksPerPage]uint64
	bucket   int

	// copy-first: the migration copied before the switch; blocks written
	// since (or possibly in flight at) the copy start; last write issue
	// time per block.
	copied   bool
	dirty    [blocksPerPage]bool
	lastW    [blocksPerPage]sim.VTimeInSec
	observed bool // fed to the buffer's learner
	preCopy  bool // pre-placement copy running
	preHeld  bool
	origPA   uint64 // physical address of the frame at tracking start
}

type replica struct {
	dev   uint64
	frame uint64
}

type bufKey struct {
	pid   vm.PID
	block uint64
}

type buffer struct {
	pid        vm.PID
	start      uint64
	pages      int
	conc       bool
	touched    int
	hot        int
	propagated bool
	learn      *learner
	sumReads   float64
	nReads     int
	accesses   float64 // counted accesses to the buffer's pages
}

func (a *AdaptConfig) on() bool { return a.Enabled }

// homeOf is the GPM serving a physical address.
func (m *Manager) homeOf(pAddr uint64) uint64 {
	if m.blockHome != nil {
		if d, ok := m.blockHome[pAddr>>log2Block]; ok {
			return d
		}
	}
	return pAddr >> 33
}

// BlockHomeTile is the lookup the runner's placement wrappers use: the tile
// of the GPM that serves a 1 KB block re-homed at run time.
func (m *Manager) BlockHomeTile(blk uint64) (int, bool) {
	d, ok := m.blockHome[blk]
	if !ok {
		return 0, false
	}
	return m.gpms[d].Tile, true
}

// UsesBlockHomes reports whether the run can re-home 1 KB blocks.
func (m *Manager) UsesBlockHomes() bool { return m.blockHome != nil }

func (m *Manager) setBlockHome(blk, dev uint64) {
	if dev == blk<<log2Block>>33 {
		delete(m.blockHome, blk)
		return
	}
	m.blockHome[blk] = dev
}

func (m *Manager) initAdapt() {
	a := &m.cfg.Adapt
	if !a.Enabled && !a.FirstTouch {
		return
	}
	m.blockHome = make(map[uint64]uint64)
	if !a.Enabled {
		return
	}
	m.buffers = make(map[bufKey]*buffer)
	m.tileDev = make(map[int]uint64)
	m.replBytes = make([]uint64, len(m.gpms))
	for d, g := range m.gpms {
		if g != nil {
			m.tileDev[g.Tile] = uint64(d)
		}
	}
	if m.load == nil {
		m.load = make([]float64, len(m.gpms))
	}
	var need []int
	if a.Spread {
		need = append(need, RingTiles...)
	}
	if a.Replicate {
		need = append(need, a.ReplicaTiles...)
	}
	for _, t := range need {
		if _, ok := m.tileDev[t]; !ok {
			panic("adaptive placement: tile is not a GPM")
		}
	}
}

func (m *Manager) hops(a, b uint64) int { return tileHops(m.gpms[a].Tile, m.gpms[b].Tile) }

func (m *Manager) queue(h uint64) float64 {
	q := m.cfg.Adapt.QueueNS
	if q == 0 {
		return 0
	}
	var sum float64
	n := 0
	for d, g := range m.gpms {
		if g != nil {
			sum += m.load[d]
			n++
		}
	}
	if sum == 0 {
		return 0
	}
	r := m.load[h] / (sum / float64(n))
	if r > 8 {
		r = 8
	}
	return q * r
}

// lat is the modeled latency of one access of GPM g to data homed at h.
func (m *Manager) lat(g, h uint64) float64 {
	if g == h {
		return 0
	}
	a := &m.cfg.Adapt
	return a.BaseNS + a.HopNS*float64(m.hops(g, h)) + m.queue(h)
}

// copyCost is the cost of copying `lines` lines from src to dst.
func (m *Manager) copyCost(src, dst uint64, lines int) float64 {
	if src == dst {
		return 0
	}
	a := &m.cfg.Adapt
	return float64(lines) * (a.BaseNS + a.HopNS*float64(m.hops(src, dst))) * a.CopyWeight
}

// adaptUnit returns the tracking entry of a page, creating it.
func (m *Manager) adaptUnit(u *unit) *adaptState {
	if u.a != nil {
		return u.a
	}
	a := &adaptState{writeKernel: -1}
	va := u.key.idx << m.unitLog2
	if p, ok := m.pageTable.Find(u.key.pid, va); ok {
		a.origPA = p.PAddr
	}
	if m.buffers != nil {
		if b, idx := m.bufferOf(u.key.pid, va); b != nil {
			a.buf, a.idx = b, idx
		}
	}
	u.a = a
	if m.cfg.Adapt.Block {
		for b := range a.bc {
			a.bc[b] = make([]uint32, len(m.gpms))
		}
	}
	return a
}

// bufferOf finds the driver allocation (PageBlock) holding va.
func (m *Manager) bufferOf(pid vm.PID, va uint64) (*buffer, int) {
	p, ok := m.pageTable.Find(pid, va)
	if !ok {
		return nil, 0
	}
	k := bufKey{pid, p.PageBlock}
	if b := m.buffers[k]; b != nil {
		return b, int((va - b.start) >> m.log2PageSize)
	}
	ps := uint64(1) << m.log2PageSize
	start := va
	for start >= 2*ps {
		q, ok := m.pageTable.Find(pid, start-ps)
		if !ok || q.PageBlock != p.PageBlock {
			break
		}
		start -= ps
	}
	devs := map[uint64]int{}
	n := 0
	for v := start; ; v += ps {
		q, ok := m.pageTable.Find(pid, v)
		if !ok || q.PageBlock != p.PageBlock {
			break
		}
		devs[q.DeviceID]++
		n++
	}
	best := 0
	for _, c := range devs {
		if c > best {
			best = c
		}
	}
	b := &buffer{pid: pid, start: start, pages: n,
		conc: n > 0 && float64(best) >= m.cfg.Adapt.ConcShare*float64(n)}
	b.learn = newLearner(m.cfg.Adapt.PredictM)
	m.buffers[k] = b
	return b, int((va - start) >> m.log2PageSize)
}

// ---------------------------------------------------------------------------
// counting and signals

// adaptCount is called for a counted access (remote, or the owner's own L1V
// miss with CountLocal) of GPM dev to pAddr of unit u.
func (m *Manager) adaptCount(now sim.VTimeInSec, u *unit, dev, pAddr uint64, remote bool) {
	a := m.adaptUnit(u)
	if a.fixed || len(a.replicas) > 0 {
		return
	}
	u.counts[dev]++
	if a.bc[0] != nil {
		a.bc[(pAddr>>log2Block)&3][dev]++
	}
	a.n++
	if a.buf != nil {
		a.buf.accesses++
	}
	if !remote {
		return
	}
	c := &m.cfg.Adapt
	if c.CostModel {
		if a.n%c.EvalEvery == 0 {
			m.adaptEvaluate(now, u)
		}
		return
	}
	if u.counts[dev] >= m.cfg.Threshold {
		m.adaptEvaluate(now, u)
	}
}

func (m *Manager) adaptUncount(u *unit, dev, pAddr uint64) {
	a := u.a
	if a == nil || u.counts[dev] == 0 {
		return
	}
	u.counts[dev]--
	if a.bc[0] != nil {
		if c := &a.bc[(pAddr>>log2Block)&3][dev]; *c > 0 {
			*c--
		}
	}
	if a.n > 0 {
		a.n--
	}
}

// adaptIssued is called for every access an address translator issued.
func (m *Manager) adaptIssued(now sim.VTimeInSec, dev uint64, pid vm.PID,
	vAddr uint64, isWrite, vector, newFrame bool) {
	u := m.getUnit(m.unitOf(pid, vAddr))
	a := m.adaptUnit(u)
	if newFrame && a.buf != nil && !a.preplaced {
		a.buf.touched++
	}
	if isWrite {
		a.writeSeq++
		a.writeKernel = m.kernel
		b := (vAddr >> log2Block) & (blocksPerPage - 1)
		a.lastW[b] = now
		if a.copied && u.phase == phasePreCopy {
			a.dirty[b] = true
		}
		return
	}
	if !vector || a.readMask&(1<<dev) != 0 {
		return
	}
	a.readMask |= 1 << dev
	t := float64(now)
	a.readT = append(a.readT, t)
	if a.hot {
		return
	}
	w := float64(m.cfg.Adapt.HotWindowNS) * 1e-9
	n := 0
	for _, x := range a.readT {
		if t-x <= w {
			n++
		}
	}
	if n < m.cfg.Adapt.HotK {
		return
	}
	a.hot = true
	m.AStats.HotDetected++
	if a.buf != nil {
		a.buf.hot++
	}
	m.adaptEvaluate(now, u)
}

// ---------------------------------------------------------------------------
// the decision

type choice struct {
	act    int
	score  float64
	dst    [blocksPerPage]uint64
	reps   []uint64
	moved  int
	benefi float64
}

func (m *Manager) pageHomes(p vm.Page) (h [blocksPerPage]uint64, uniform bool) {
	uniform = true
	for b := 0; b < blocksPerPage; b++ {
		h[b] = m.homeOf(p.PAddr + uint64(b)<<log2Block)
		if h[b] != h[0] {
			uniform = false
		}
	}
	return
}

func dominant(c []uint32) (uint64, uint32, uint32) {
	var best uint64
	var tot uint32
	for d, x := range c {
		tot += x
		if x > c[best] {
			best = uint64(d)
		}
	}
	return best, c[best], tot
}

// adaptEvaluate runs the benefit/cost decision for one page.
func (m *Manager) adaptEvaluate(now sim.VTimeInSec, u *unit) {
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
	home, uniform := m.pageHomes(p)
	best := choice{act: actStay}
	consider := func(ch choice, benefit, cost float64) {
		ch.score = benefit - c.Alpha*cost
		if !c.CostModel {
			ch.score = 1 + float64(ch.act) // fixed preference: later actions win
		} else if benefit <= c.Alpha*cost {
			return
		}
		if best.act == actStay || ch.score > best.score {
			best = ch
		}
	}
	dom, dc, tot := dominant(u.counts)
	readers := 0
	for _, x := range u.counts {
		if x > 0 {
			readers++
		}
	}
	if r := bits.OnesCount64(a.readMask); r > readers {
		readers = r
	}
	hasDom := tot > 0 && float64(dc) >= m.cfg.DomShare*float64(tot)
	if c.CountHot && !a.hot && !hasDom && tot >= c.EvalEvery {
		n := 0
		for _, x := range u.counts {
			if x > 0 {
				n++
			}
		}
		if n >= c.HotK {
			a.hot = true
			m.AStats.CountHotN++
			m.AStats.HotDetected++
			if a.buf != nil {
				a.buf.hot++
			}
		}
	}
	// early observation: only pages that look private so far (a broadcast
	// page's first accesses are dominated by whoever came first)
	if c.Predict && c.EarlyObserve && a.buf != nil && !a.observed && hasDom &&
		tot >= c.EvalEvery && uniform && !a.hot && bits.OnesCount64(a.readMask) <= 2 {
		a.observed = true
		m.AStats.EarlyObs++
		m.observe(now, a.buf, a.idx, dom, home[0], false)
	}
	// move: the whole page to the dominant reader
	if c.Move && hasDom {
		ch := choice{act: actMove}
		var benefit, cost float64
		for b := 0; b < blocksPerPage; b++ {
			ch.dst[b] = dom
			cnt := u.counts
			if a.bc[0] != nil {
				cnt = a.bc[b]
			}
			for g, x := range cnt {
				if x == 0 || m.gpms[g] == nil {
					continue
				}
				w := float64(x)
				if a.bc[0] == nil {
					w /= blocksPerPage
				}
				benefit += w * (m.lat(uint64(g), home[b]) - m.lat(uint64(g), dom))
			}
			if home[b] != dom {
				cost += m.copyCost(home[b], dom, blockLines)
				ch.moved++
			}
		}
		if ch.moved > 0 {
			consider(ch, benefit, cost+c.FixedNS)
		}
	}
	// block: each 1 KB block to its own dominant reader
	if c.Block && a.bc[0] != nil {
		ch := choice{act: actBlock}
		var benefit, cost float64
		distinct := map[uint64]bool{}
		for b := 0; b < blocksPerPage; b++ {
			ch.dst[b] = home[b]
			d, x, t := dominant(a.bc[b])
			if t >= c.MinBlockSamples && float64(x) >= m.cfg.DomShare*float64(t) {
				ch.dst[b] = d
			}
			distinct[ch.dst[b]] = true
			for g, n := range a.bc[b] {
				if n == 0 || m.gpms[g] == nil {
					continue
				}
				benefit += float64(n) * (m.lat(uint64(g), home[b]) - m.lat(uint64(g), ch.dst[b]))
			}
			if ch.dst[b] != home[b] {
				cost += m.copyCost(home[b], ch.dst[b], blockLines)
				ch.moved++
			}
		}
		if ch.moved > 0 && len(distinct) > 1 {
			consider(ch, benefit, cost+c.FixedNS)
		}
	}
	// hot actions, decided at the first-read signal
	if a.hot && !a.hotHandled {
		shares := m.readerShares(u, a)
		// a hot page is expected to be read by HotFutureReaders GPMs, each
		// fetching each line about once (M3 keeps it afterwards)
		fr := readers
		if c.HotFutureReaders > fr {
			fr = c.HotFutureReaders
		}
		future := float64(pageLines * fr)
		if (c.Spread) && uniform {
			if dst, ok := m.spreadTargets(a, p); ok {
				ch := choice{act: actSpread, dst: dst}
				var benefit, cost float64
				for g, s := range shares {
					if s == 0 {
						continue
					}
					var after float64
					for b := 0; b < blocksPerPage; b++ {
						after += m.lat(uint64(g), dst[b]) / blocksPerPage
					}
					benefit += future * s * (m.lat(uint64(g), home[0]) - after)
				}
				for b := 0; b < blocksPerPage; b++ {
					if dst[b] != home[b] {
						cost += m.copyCost(home[b], dst[b], blockLines)
						ch.moved++
					}
				}
				if ch.moved > 0 {
					consider(ch, benefit, cost+c.FixedNS)
				}
			}
		}
		if c.Replicate && uniform && a.buf != nil && (a.buf.conc || c.ReplicateAny) &&
			a.writeKernel != m.kernel {
			ch := choice{act: actReplicate}
			var cost float64
			for _, t := range c.ReplicaTiles {
				d := m.tileDev[t]
				if d != home[0] {
					ch.reps = append(ch.reps, d)
					cost += m.copyCost(home[0], d, pageLines)
					// one invalidation message per copy when it is dropped
					cost += c.BaseNS + c.HopNS*float64(m.hops(home[0], d))
				}
			}
			var benefit float64
			for g, s := range shares {
				if s == 0 {
					continue
				}
				near := m.lat(uint64(g), home[0])
				for _, r := range ch.reps {
					near = math.Min(near, m.lat(uint64(g), r))
				}
				benefit += future * s * (m.lat(uint64(g), home[0]) - near)
			}
			if len(ch.reps) > 0 {
				consider(ch, benefit, cost+c.FixedNS)
			}
		}
	}
	if best.act == actStay {
		return
	}
	if best.act == actSpread || best.act == actReplicate {
		a.hotHandled = true
	}
	a.bucket = readerBucket(readers)
	m.adaptExecute(now, u, a, p, home, best)
	if a.hot && (best.act == actSpread || best.act == actReplicate) {
		m.propagateHot(now, u, a, best)
	}
}

// readerShares are the reader weights of a hot page: the counts if there
// are enough, else the GPMs that read it so far, equally.
func (m *Manager) readerShares(u *unit, a *adaptState) []float64 {
	s := make([]float64, len(m.gpms))
	var tot float64
	for _, x := range u.counts {
		tot += float64(x)
	}
	if tot >= 16 {
		for g, x := range u.counts {
			s[g] = float64(x) / tot
		}
		return s
	}
	n := 0
	for _, g := range m.gpms {
		if g != nil {
			n++
		}
	}
	for d, g := range m.gpms {
		if g != nil {
			s[d] = 1 / float64(n)
		}
	}
	return s
}

// spreadTargets: concentrated buffer -> ring 12 (page i, block b ->
// RingTiles[(4i+b) mod 12]); otherwise hash or nothing (SpreadOther).
func (m *Manager) spreadTargets(a *adaptState, p vm.Page) ([blocksPerPage]uint64, bool) {
	var dst [blocksPerPage]uint64
	conc := a.buf != nil && a.buf.conc
	switch {
	case conc:
		for b := 0; b < blocksPerPage; b++ {
			dst[b] = m.tileDev[RingTiles[(blocksPerPage*a.idx+b)%len(RingTiles)]]
		}
	case m.cfg.Adapt.SpreadOther == "hash":
		n := uint64(0)
		for _, g := range m.gpms {
			if g != nil {
				n++
			}
		}
		for b := 0; b < blocksPerPage; b++ {
			blk := (p.VAddr >> log2Block) + uint64(b)
			k := mix64(blk) % n
			for d, g := range m.gpms {
				if g == nil {
					continue
				}
				if k == 0 {
					dst[b] = uint64(d)
					break
				}
				k--
			}
		}
	default:
		return dst, false
	}
	return dst, true
}

func mix64(x uint64) uint64 {
	x += 0x9e3779b97f4a7c15
	x = (x ^ (x >> 30)) * 0xbf58476d1ce4e5b9
	x = (x ^ (x >> 27)) * 0x94d049bb133111eb
	return x ^ (x >> 31)
}

// ---------------------------------------------------------------------------
// execution

func (m *Manager) adaptExecute(now sim.VTimeInSec, u *unit, a *adaptState,
	p vm.Page, home [blocksPerPage]uint64, ch choice) {
	m.AStats.Decided[ch.act][a.bucket]++
	switch ch.act {
	case actReplicate:
		m.startReplicate(now, u, a, p, home[0], ch.reps)
		return
	case actSpread:
		a.fixed = true
	}
	if a.preplaced && (ch.act == actMove || ch.act == actBlock) {
		m.AStats.PreFallback++
	}
	// learn: a page-level dominant target is an observation of its buffer
	if m.cfg.Adapt.Predict && a.buf != nil && (ch.act == actMove || ch.act == actBlock) {
		dom, dc, tot := dominant(u.counts)
		if tot > 0 && float64(dc) >= m.cfg.DomShare*float64(tot) {
			a.buf.sumReads += float64(tot)
			a.buf.nReads++
			if !a.observed {
				a.observed = true
				m.observe(now, a.buf, a.idx, dom, home[0], ch.act == actMove)
			}
		}
	}
	a.act = ch.act
	a.blockDst = ch.dst
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
	m.Stats.CounterTriggers++
	u.faulters = u.faulters[:0]
	m.trigger(now, u, majority(ch.dst))
}

func majority(d [blocksPerPage]uint64) uint64 {
	best, bn := d[0], 0
	for i := 0; i < blocksPerPage; i++ {
		n := 0
		for j := 0; j < blocksPerPage; j++ {
			if d[j] == d[i] {
				n++
			}
		}
		if n > bn {
			best, bn = d[i], n
		}
	}
	return best
}

// adaptNeedsMove reports whether a triggered adaptive unit still has a
// block to move (startShootdown).
func (m *Manager) adaptNeedsMove(u *unit, p vm.Page) bool {
	home, _ := m.pageHomes(p)
	for b := 0; b < blocksPerPage; b++ {
		if home[b] != u.a.blockDst[b] {
			return true
		}
	}
	return false
}

// adaptStartCopy copies each 1 KB block from its home to its new home, into
// a fresh frame allocated on the majority destination.
func (m *Manager) adaptStartCopy(now sim.VTimeInSec, u *unit, freeze bool) {
	a := u.a
	for _, p := range u.pages {
		if freeze {
			m.frameInfo(p).state = frameFrozen
		}
		frame := m.allocFrame(u.dst)
		u.newPAddrs = append(u.newPAddrs, frame)
		home, _ := m.pageHomes(p)
		for b := 0; b < blocksPerPage; b++ {
			off := uint64(b) << log2Block
			dst := a.blockDst[b]
			m.gpms[dst].Engine.enqueue(now, &chunk{
				u:        u,
				src:      m.gpms[home[b]].Engine,
				srcPAddr: p.PAddr + off,
				dstPAddr: frame + off,
				size:     1 << log2Block,
			})
			u.pendingChunks++
			if home[b] != dst {
				m.AStats.BlocksMoved[a.act]++
				m.AStats.CopyBytes += 1 << log2Block
			}
		}
	}
}

// adaptRemapFrame installs the block homes of a fresh frame.
func (m *Manager) adaptRemapFrame(u *unit, frame uint64) {
	for b := 0; b < blocksPerPage; b++ {
		m.setBlockHome(frame>>log2Block+uint64(b), u.a.blockDst[b])
	}
}

// prePlace re-homes the blocks of a page nobody touched yet: copy, then
// install the block homes if the page is still untouched.
func (m *Manager) prePlace(now sim.VTimeInSec, u *unit, dst [blocksPerPage]uint64,
	act int, byPredict bool) bool {
	a := m.adaptUnit(u)
	if a.preplacing || a.fixed || len(a.replicas) > 0 || u.phase != phaseIdle {
		return false
	}
	p, ok := m.pageTable.Find(u.key.pid, u.key.idx<<m.unitLog2)
	if !ok || m.ppns[p.PAddr>>m.log2PageSize] != nil {
		return false
	}
	home, _ := m.pageHomes(p)
	pending := 0
	for b := 0; b < blocksPerPage; b++ {
		if home[b] != dst[b] {
			pending++
		}
	}
	if pending == 0 {
		return false
	}
	a.preplacing = true
	a.preCopy = true
	ppn := p.PAddr >> m.log2PageSize
	done := func(now sim.VTimeInSec) {
		pending--
		if pending > 0 {
			return
		}
		a.preplacing = false
		a.preCopy = false
		m.wake(now, u)
		if m.ppns[ppn] != nil {
			m.AStats.PreRaced++
			return
		}
		for b := 0; b < blocksPerPage; b++ {
			if home[b] != dst[b] {
				m.setBlockHome(p.PAddr>>log2Block+uint64(b), dst[b])
				m.AStats.PreBlocks++
				m.AStats.BlocksMoved[act]++
			}
		}
		m.AStats.PrePlaced++
		if byPredict {
			m.AStats.PreByPredict++
		} else {
			m.AStats.PreByHot++
		}
		a.preplaced = true
		if act == actSpread {
			a.fixed = true
		}
	}
	for b := 0; b < blocksPerPage; b++ {
		if home[b] == dst[b] {
			continue
		}
		off := uint64(b) << log2Block
		m.gpms[dst[b]].Engine.enqueue(now, &chunk{
			src:      m.gpms[home[b]].Engine,
			srcPAddr: p.PAddr + off,
			dstPAddr: p.PAddr + off,
			size:     1 << log2Block,
			done:     done,
			pre:      a,
		})
		m.AStats.CopyBytes += 1 << log2Block
	}
	return true
}

// propagateHot applies a hot decision to the rest of the page's buffer:
// untouched pages are re-homed in place (spread) or replicated; touched
// pages are marked hot and decide on their next evaluation.
func (m *Manager) propagateHot(now sim.VTimeInSec, u *unit, a *adaptState, ch choice) {
	b := a.buf
	c := &m.cfg.Adapt
	if b == nil || b.propagated {
		return
	}
	if b.pages > c.PropSmallPages &&
		(b.touched < 8 || float64(b.hot) < c.PropGate*float64(b.touched)) {
		return
	}
	b.propagated = true
	m.AStats.HotBuffers++
	ps := uint64(1) << m.log2PageSize
	for i := 0; i < b.pages; i++ {
		va := b.start + uint64(i)*ps
		k := m.unitOf(b.pid, va)
		if k == u.key {
			continue
		}
		v := m.getUnit(k)
		va2 := m.adaptUnit(v)
		if va2.hot || va2.fixed {
			continue
		}
		p, ok := m.pageTable.Find(b.pid, va)
		if !ok {
			continue
		}
		va2.hot = true
		if m.ppns[p.PAddr>>m.log2PageSize] == nil && ch.act == actSpread {
			dst, ok := m.spreadTargets(va2, p)
			if ok && m.prePlace(now, v, dst, actSpread, false) {
				m.AStats.Propagated++
				m.AStats.Decided[actSpread][a.bucket]++
			}
			continue
		}
		m.AStats.Propagated++
		va2.readMask |= a.readMask
		m.adaptEvaluate(now, v)
	}
}

// ---------------------------------------------------------------------------
// replicas

func (m *Manager) startReplicate(now sim.VTimeInSec, u *unit, a *adaptState,
	p vm.Page, home uint64, reps []uint64) {
	a.fixed = true
	a.preplacing = true
	seq, kern := a.writeSeq, m.kernel
	var frames []replica
	pending := 0
	ps := uint64(1) << m.log2PageSize
	if b := m.cfg.Adapt.ReplicaBudget; b > 0 {
		keep := reps[:0:0]
		for _, r := range reps {
			for m.replBytes[r]+ps > b && m.evictOldestOn(now, r) {
			}
			if m.replBytes[r]+ps <= b {
				keep = append(keep, r)
			}
		}
		reps = keep
	}
	if len(reps) == 0 {
		a.fixed, a.preplacing = false, false
		return
	}
	a.replHome = home
	done := func(now sim.VTimeInSec) {
		pending--
		if pending > 0 {
			return
		}
		a.preplacing = false
		if a.writeSeq != seq || m.kernel != kern || u.phase != phaseIdle {
			m.AStats.ReplicaAbort++
			for _, r := range frames {
				m.retireFrame(r.frame)
				m.replBytes[r.dev] -= ps
			}
			a.fixed = false
			return
		}
		a.replicas = frames
		m.AStats.ReplicaOn++
		m.replicated[u] = true
		m.replOrder = append(m.replOrder, u)
	}
	for _, r := range reps {
		f := m.allocFrame(r)
		frames = append(frames, replica{dev: r, frame: f})
		m.gpms[r].Engine.enqueue(now, &chunk{
			src:      m.gpms[home].Engine,
			srcPAddr: p.PAddr,
			dstPAddr: f,
			size:     uint64(1) << m.log2PageSize,
			done:     done,
		})
		pending++
		m.AStats.CopyBytes += ps
		m.AStats.ReplCopy += ps
		m.replBytes[r] += ps
		if m.replBytes[r] > m.AStats.ReplPeak {
			m.AStats.ReplPeak = m.replBytes[r]
		}
	}
}

// evictOldestOn drops the oldest replicated page that has a copy on dev.
func (m *Manager) evictOldestOn(now sim.VTimeInSec, dev uint64) bool {
	for i, u := range m.replOrder {
		if !m.replicated[u] {
			continue
		}
		for _, r := range u.a.replicas {
			if r.dev == dev {
				m.replOrder = append(m.replOrder[:i:i], m.replOrder[i+1:]...)
				m.collapse(now, u, false)
				m.AStats.ReplEvict++
				return true
			}
		}
	}
	return false
}

// retireFrame marks a frame retired once nothing is in flight to it.
func (m *Manager) retireFrame(f uint64) {
	m.toRetire = append(m.toRetire, f>>m.log2PageSize)
}

func (m *Manager) collapse(now sim.VTimeInSec, u *unit, hold bool) {
	a := u.a
	ps := uint64(1) << m.log2PageSize
	for _, r := range a.replicas {
		m.retireFrame(r.frame)
		if m.replBytes != nil {
			m.replBytes[r.dev] -= ps
		}
		if src := m.gpms[a.replHome]; src != nil && src.Engine != nil &&
			m.gpms[r.dev].Engine != nil {
			src.Engine.sendInval(now, m.gpms[r.dev].Engine)
			m.AStats.ReplInvals++
		}
	}
	a.replicas = nil
	a.fixed = false
	a.hotHandled = false
	delete(m.replicated, u)
	if hold && m.cfg.ShootdownLatencyNS > 0 {
		a.holdUntil = now + ns(m.cfg.ShootdownLatencyNS)
		m.holds = append(m.holds, u)
		m.TickLater(now)
	}
}

// KernelBoundary is called by every GPM's command processor when it accepts
// a kernel launch; launches less than 1 us apart count as one boundary.
func (m *Manager) KernelBoundary(now sim.VTimeInSec) {
	if !m.cfg.Adapt.Enabled && m.cfg.Adapt.ReplicaList == nil {
		return
	}
	if m.AStats.Kernels > 0 && float64(now-m.lastBoundary) < 1e-6 {
		m.lastBoundary = now
		return
	}
	m.lastBoundary = now
	m.kernel++
	m.AStats.Kernels++
	for _, u := range m.active {
		if u.a != nil && u.a.copied {
			for b := range u.a.dirty {
				u.a.dirty[b] = true
			}
		}
	}
	us := make([]*unit, 0, len(m.replicated))
	for u := range m.replicated {
		us = append(us, u)
	}
	sort.Slice(us, func(i, j int) bool { return us[i].key.idx < us[j].key.idx })
	for _, u := range us {
		if m.cfg.Adapt.ReplicaList != nil && m.replicasCurrent(u) {
			continue // master unchanged (no host copy): the copies stay valid
		}
		m.collapse(now, u, false)
		u.a.hotHandled = false
		m.AStats.ReplicaKern++
	}
	if len(m.toRetire) > 0 {
		m.TickLater(now)
	}
}

// adaptResolve redirects reads of replicated pages and holds accesses while
// a collapse is in progress. It also implements the first-touch baseline.
func (m *Manager) adaptResolve(now sim.VTimeInSec, at Waker, dev uint64,
	u *unit, vector, isWrite bool, page vm.Page) (vm.Page, bool) {
	if m.cfg.Adapt.ReplicaList != nil {
		if u == nil {
			u = m.getUnit(m.unitOf(page.PID, page.VAddr))
		}
		a := m.adaptUnit(u)
		if isWrite {
			a.writeKernel = m.kernel
		} else if len(a.replicas) == 0 && a.writeKernel < 0 && now >= a.holdUntil {
			if devs, ok := m.cfg.Adapt.ReplicaList[a.origPPN(page)]; ok {
				m.oracleReplicate(u, a, page, devs)
			}
		}
		if len(a.replicas) > 0 || now < a.holdUntil {
			return m.replicaResolve(now, at, dev, u, vector, isWrite, page)
		}
	}
	if m.cfg.Adapt.FirstTouch {
		if vector && m.ppns[page.PAddr>>m.log2PageSize] == nil {
			if _, placed := m.blockHome[page.PAddr>>log2Block]; !placed &&
				page.DeviceID != dev {
				for b := uint64(0); b < blocksPerPage; b++ {
					m.blockHome[page.PAddr>>log2Block+b] = dev
				}
				m.AStats.FirstTouch++
			}
		}
		return page, true
	}
	if u == nil || u.a == nil {
		return page, true
	}
	if u.a.preCopy && m.cfg.Adapt.HoldPrePlace {
		m.holdPrePlace(now, at, u)
		return page, false
	}
	return m.replicaResolve(now, at, dev, u, vector, isWrite, page)
}

// holdPrePlace makes an access wait for the running pre-placement of its
// page; the page's blocks go to the front of their copy queues.
func (m *Manager) holdPrePlace(now sim.VTimeInSec, at Waker, u *unit) {
	a := u.a
	m.AStats.PreHeld++
	if !a.preHeld {
		a.preHeld = true
		m.AStats.PreHeldPages++
		for _, g := range m.gpms {
			if g != nil && g.Engine != nil {
				g.Engine.promote(now, func(c *chunk) bool { return c.pre == a })
			}
		}
	}
	u.addWaiter(at)
}

// ---------------------------------------------------------------------------
// copy-first migration

// adaptPreCopy copies a serviced unit into its fresh frame while the old
// frame stays mapped.
func (m *Manager) adaptPreCopy(now sim.VTimeInSec, u *unit) {
	if !m.collectPages(now, u) {
		return
	}
	a := u.a
	u.phase = phasePreCopy
	a.copied = true
	a.dirty = [blocksPerPage]bool{}
	for _, p := range u.pages {
		// writes issued before the copy start may still be in flight
		if info := m.ppns[p.PAddr>>m.log2PageSize]; info != nil && info.inflight > 0 {
			for b := 0; b < blocksPerPage; b++ {
				if a.lastW[b] >= info.lastIdle && a.lastW[b] > 0 {
					a.dirty[b] = true
				}
			}
		}
	}
	m.AStats.PreCopies++
	m.copyStart[u] = now
	u.newPAddrs = u.newPAddrs[:0]
	u.pendingChunks = 0
	m.adaptStartCopy(now, u, false)
	m.active = append(m.active, u)
}

// adaptPreCopyDone starts the switch: shootdown, then drain (Tick), then
// adaptSwitch.
func (m *Manager) adaptPreCopyDone(now sim.VTimeInSec, u *unit) {
	m.Stats.CopyTimeSum += float64(now - m.copyStart[u])
	delete(m.copyStart, u)
	m.beginShootdown(now, u)
	m.TickLater(now)
}

// adaptSwitch runs after the drain of a copy-first migration: recopy the
// blocks written since the copy started, then remap.
func (m *Manager) adaptSwitch(now sim.VTimeInSec, u *unit) {
	a := u.a
	u.phase = phaseCopy
	u.pendingChunks = 0
	for i, p := range u.pages {
		m.frameInfo(p).state = frameFrozen
		home, _ := m.pageHomes(p)
		for b := 0; b < blocksPerPage; b++ {
			if !a.dirty[b] {
				continue
			}
			off := uint64(b) << log2Block
			m.gpms[a.blockDst[b]].Engine.enqueue(now, &chunk{
				u:        u,
				src:      m.gpms[home[b]].Engine,
				srcPAddr: p.PAddr + off,
				dstPAddr: u.newPAddrs[i] + off,
				size:     1 << log2Block,
				urgent:   true,
			})
			u.pendingChunks++
			m.AStats.RecopyBlocks++
			m.AStats.CopyBytes += 1 << log2Block
		}
	}
	if u.pendingChunks == 0 {
		m.remap(now, u)
		return
	}
	m.AStats.Recopies++
}

func (m *Manager) replicaResolve(now sim.VTimeInSec, at Waker, dev uint64,
	u *unit, vector, isWrite bool, page vm.Page) (vm.Page, bool) {
	a := u.a
	if now < a.holdUntil {
		u.addWaiter(at)
		return page, false
	}
	if len(a.replicas) == 0 {
		return page, true
	}
	if isWrite {
		m.collapse(now, u, true)
		m.AStats.ReplicaWrite++
		u.addWaiter(at)
		return page, false
	}
	if !vector {
		return page, true
	}
	home := m.homeOf(page.PAddr)
	best, bh := uint64(0), m.hops(dev, home)
	for _, r := range a.replicas {
		if h := m.hops(dev, r.dev); h < bh {
			best, bh = r.frame, h
			page.DeviceID = r.dev
		}
	}
	if best != 0 {
		page.PAddr = best
		m.AStats.ReplicaReads++
	}
	return page, true
}

// adaptTick wakes held accesses and retires drained replica frames.
func (m *Manager) adaptTick(now sim.VTimeInSec) bool {
	progress := false
	if len(m.holds) > 0 {
		keep := m.holds[:0]
		for _, u := range m.holds {
			if now >= u.a.holdUntil {
				m.wake(now, u)
			} else {
				keep = append(keep, u)
			}
		}
		m.holds = keep
		progress = progress || len(m.holds) > 0
	}
	if len(m.toRetire) > 0 {
		keep := m.toRetire[:0]
		for _, ppn := range m.toRetire {
			info := m.ppns[ppn]
			if info == nil {
				info = &ppnInfo{}
				m.ppns[ppn] = info
			}
			if info.inflight > 0 {
				keep = append(keep, ppn)
				continue
			}
			info.state = frameRetired
		}
		m.toRetire = keep
		progress = progress || len(m.toRetire) > 0
	}
	return progress
}

// ---------------------------------------------------------------------------
// prediction

// observe feeds one decided page target to its buffer's learner and, while a
// rule is trusted, pre-places the untouched pages around it.
func (m *Manager) observe(now sim.VTimeInSec, b *buffer, idx int, dst, owner uint64, _ bool) {
	l := b.learn
	m.AStats.PredictObs++
	wasConf := l.conf >= 0
	hit := l.add(idx, int(dst)-1, int(owner)-1)
	if hit {
		m.AStats.PredictHits++
	} else if wasConf {
		m.AStats.PredictMiss++
	}
	if l.conf < 0 {
		return
	}
	if !wasConf {
		m.AStats.PredictConf++
	}
	c := &m.cfg.Adapt
	ps := uint64(1) << m.log2PageSize
	lo, hi := idx-c.PredictWindow, idx+c.PredictWindow
	if lo < 0 {
		lo = 0
	}
	if hi >= b.pages {
		hi = b.pages - 1
	}
	// expected counted accesses of a page of this buffer: accesses so far
	// per touched page (a lower bound of the final count)
	mean := 0.0
	if b.touched > 0 {
		mean = b.accesses / float64(b.touched)
	}
	// pages of this buffer that were moved had this many counted accesses
	// at the decision (ski rental: as many again are expected)
	if b.nReads > 0 && b.sumReads/float64(b.nReads) > mean {
		mean = b.sumReads / float64(b.nReads)
	}
	for i := lo; i <= hi; i++ {
		if i == idx {
			continue
		}
		va := b.start + uint64(i)*ps
		p, ok := m.pageTable.Find(b.pid, va)
		if !ok {
			continue
		}
		t := l.predict(i, int(p.DeviceID)-1)
		if t < 0 {
			continue
		}
		d := uint64(t + 1)
		if m.gpms[d] == nil {
			continue
		}
		home, uniform := m.pageHomes(p)
		if !uniform || home[0] == d {
			if uniform {
				m.AStats.PreHomeOK++
			}
			continue
		}
		v := m.getUnit(m.unitOf(b.pid, va))
		va2 := m.adaptUnit(v)
		if va2.preplaced || va2.preplacing || va2.fixed || len(va2.replicas) > 0 {
			continue
		}
		dsts := [blocksPerPage]uint64{d, d, d, d}
		if m.ppns[p.PAddr>>m.log2PageSize] == nil {
			if c.CostModel && mean*m.lat(d, home[0]) <= c.Alpha*m.copyCost(home[0], d, pageLines) {
				m.AStats.PreCostSkip++
				continue
			}
			m.prePlace(now, v, dsts, actMove, true)
			continue
		}
		m.AStats.PreTouched++
		// touched neighbour: join the batch (one software service and one
		// shootdown round for the predicted pages), unless its own counter
		// already points elsewhere
		if !c.PredictTouched || v.phase != phaseIdle || !m.eligible(v, now) {
			continue
		}
		if dom, dc, tot := dominant(v.counts); tot >= c.EvalEvery &&
			float64(dc) >= m.cfg.DomShare*float64(tot) && dom != d {
			continue
		}
		if c.CostModel && mean*m.lat(d, home[0]) <=
			c.Alpha*(m.copyCost(home[0], d, pageLines)+c.FixedNS/c.BatchAmort) {
			continue
		}
		va2.act = actMove
		va2.blockDst = dsts
		for k := range v.counts {
			v.counts[k] = 0
		}
		va2.n = 0
		if va2.bc[0] != nil {
			for bb := range va2.bc {
				for k := range va2.bc[bb] {
					va2.bc[bb][k] = 0
				}
			}
		}
		v.faulters = v.faulters[:0]
		m.AStats.BatchTrig++
		m.Stats.CounterTriggers++
		m.trigger(now, v, d)
	}
}

// BlockHomes is the number of 1 KB blocks with a run-time home.
func (m *Manager) BlockHomes() int { return len(m.blockHome) }

// origPPN is the page's original physical page number (the frame it had
// before any replica; the oracle list is keyed by it).
func (a *adaptState) origPPN(p vm.Page) uint64 {
	if a.orig == 0 {
		a.orig = p.PAddr >> 12
	}
	return a.orig
}

// oracleReplicate creates zero-cost replicas of a listed page.
func (m *Manager) oracleReplicate(u *unit, a *adaptState, p vm.Page, devs []uint64) {
	if m.Storage == nil {
		panic("replica list needs the global storage")
	}
	ps := uint64(1) << m.log2PageSize
	data, err := m.Storage.Read(p.PAddr, ps)
	if err != nil {
		panic(err)
	}
	home := m.homeOf(p.PAddr)
	for _, d := range devs {
		if d == home || m.gpms[d] == nil {
			continue
		}
		f := m.allocFrame(d)
		if err := m.Storage.Write(f, data); err != nil {
			panic(err)
		}
		a.replicas = append(a.replicas, replica{dev: d, frame: f})
		m.AStats.CopyBytes += ps
	}
	if len(a.replicas) > 0 {
		m.AStats.ReplicaOn++
		m.replicated[u] = true
	}
}

// replicasCurrent reports whether every replica frame still holds the
// master's bytes (the oracle list mode keeps them across a kernel boundary
// then; GPM writes collapse replicas immediately and stop replication of the
// page, so the master's storage is current for listed pages).
func (m *Manager) replicasCurrent(u *unit) bool {
	p, ok := m.pageTable.Find(u.key.pid, u.key.idx<<m.unitLog2)
	if !ok || u.a.writeKernel >= 0 {
		return false
	}
	ps := uint64(1) << m.log2PageSize
	master, err := m.Storage.Read(p.PAddr, ps)
	if err != nil {
		return false
	}
	for _, r := range u.a.replicas {
		d, err := m.Storage.Read(r.frame, ps)
		if err != nil || string(d) != string(master) {
			return false
		}
	}
	return true
}

// DumpAdapt writes one line per tracked page (diagnostics): virtual page,
// buffer start and index, original and current physical page, block homes,
// flags, readers, counted accesses, migrations.
func (m *Manager) DumpAdapt(w io.Writer) {
	keys := make([]unitKey, 0, len(m.units))
	for k, u := range m.units {
		if u.a != nil {
			keys = append(keys, k)
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].pid != keys[j].pid {
			return keys[i].pid < keys[j].pid
		}
		return keys[i].idx < keys[j].idx
	})
	fmt.Fprintf(w, "vpn\tbuf_start_vpn\tbuf_pages\tidx\torig_ppn\tcur_ppn\thomes\t"+
		"touched\thot\thot_handled\tfixed\tpreplaced\treplicas\treaders\twritten\tn\t"+
		"migrations\tobserved\tpre_held\tbuf_conc\tbuf_hot\tbuf_touched\tbuf_propagated\tbuf_conf\n")
	for _, k := range keys {
		u := m.units[k]
		a := u.a
		va := k.idx << m.unitLog2
		p, ok := m.pageTable.Find(k.pid, va)
		if !ok {
			continue
		}
		h, _ := m.pageHomes(p)
		touched := 0
		if m.ppns[a.origPA>>m.log2PageSize] != nil {
			touched = 1
		}
		bs, bp, conc, bhot, btouch, bprop, bconf := uint64(0), 0, 0, 0, 0, 0, -1
		if b := a.buf; b != nil {
			bs, bp, bhot, btouch = b.start>>m.log2PageSize, b.pages, b.hot, b.touched
			if b.conc {
				conc = 1
			}
			if b.propagated {
				bprop = 1
			}
			bconf = b.learn.conf
		}
		b2i := func(x bool) int {
			if x {
				return 1
			}
			return 0
		}
		fmt.Fprintf(w, "%d\t%d\t%d\t%d\t%d\t%d\t%d,%d,%d,%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\n",
			va>>m.log2PageSize, bs, bp, a.idx, a.origPA>>m.log2PageSize,
			p.PAddr>>m.log2PageSize, h[0], h[1], h[2], h[3], touched,
			b2i(a.hot), b2i(a.hotHandled), b2i(a.fixed), b2i(a.preplaced),
			len(a.replicas), bits.OnesCount64(a.readMask), b2i(a.writeKernel >= 0),
			a.n, u.migrations, b2i(a.observed), b2i(a.preHeld), conc, bhot, btouch,
			bprop, bconf)
	}
}
