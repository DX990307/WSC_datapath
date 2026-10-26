package pagemigration

import (
	"container/list"
	"fmt"
	"os"
	"sort"

	"github.com/sarchlab/akita/v3/mem/mem"
	"github.com/sarchlab/akita/v3/mem/vm"
	"github.com/sarchlab/akita/v3/mem/vm/addresstranslator"
	"github.com/sarchlab/akita/v3/sim"
)

// PageInvalidator is a TLB that can drop the entry of one page.
type PageInvalidator interface {
	InvalidatePage(pid vm.PID, vAddr uint64) bool
}

// Waker is a component that can be asked to tick again.
type Waker = addresstranslator.Waker

// GPM is what the manager needs to know about one GPM.
type GPM struct {
	DeviceID uint64
	MemBase  uint64 // first physical address of the GPM's 8-GB range
	// Tile is the GPM's mesh tile (x + 7*y); used by -pm-target median/class.
	Tile int
	// TLBs are every TLB of the GPM that can hold a translation (L1V, L1S,
	// L1I, L2TLB); the shootdown drops the migrating pages from them.
	TLBs   []PageInvalidator
	Engine *CopyEngine
	// HoldsLine (optional, used with Config.Check) reports whether any L2
	// slice of the GPM holds the line at a physical address or has an MSHR
	// for it (local lines and M3 remote replicas alike).
	HoldsLine func(pAddr uint64) bool
	// InstallRemote (P21 cmig) installs a clean copy of a remote line in
	// the GPM's L2 like an admitted M3 fill; false = dropped.
	InstallRemote func(now sim.VTimeInSec, pid vm.PID, pAddr uint64, data []byte) bool
	// R4: PullInstall installs a pulled line (read opened at reqTime) and
	// marks it as pulled; PullUse reports pulled lines installed and used
	// by the GPM's L2; VictimScore the mean expected re-fetch cost (ns) of
	// the lines remote fills displaced.
	PullInstall func(now sim.VTimeInSec, pid vm.PID, pAddr uint64, data []byte,
		reqTime sim.VTimeInSec) bool
	PullUse     func() (installed, used uint64)
	VictimScore func() float64
}

// phase is where a unit is in its migration (DESIGN.md 2.3):
// idle -> queued -> service -> shootdown -> drain -> copy -> (remap) -> idle.
type phase int

const (
	phaseIdle      phase = iota // not migrating; may be triggered if eligible
	phaseQueued                 // in the destination GPM's software queue
	phaseService                // in a batch being serviced (SoftwareLatency)
	phaseShootdown              // TLBs invalidated; all accesses held
	phaseDrain                  // waiting for in-flight accesses to the old frames
	phaseCopy                   // copy engines moving the pages
	// phasePreCopy (P21 copy-first): the copy runs while the old frame stays
	// mapped and accessed; writes mark their 1 KB blocks dirty. When the copy
	// ends the unit goes through shootdown and drain, recopies the dirty
	// blocks (phaseCopy) and is remapped.
	phasePreCopy
)

// unitKey identifies a migration unit: the aligned 2^Log2Unit-byte block of
// virtual addresses of one process.
type unitKey struct {
	pid vm.PID
	idx uint64
}

// unit is the migration and counting granule (a 4-KB page, or a group of
// 4-KB pages for a 2-MB unit) and its migration state.
type unit struct {
	key   unitKey
	phase phase
	dst   uint64

	// faulters are the GPMs whose accesses wait during the queued and
	// service phases (on-fault policy).
	faulters []uint64

	migrations int
	// P19 Q6 page-class filter (Config.AllowPages), decided at the unit's
	// first counted remote access.
	allowChecked, allowed bool
	everMigrated          bool
	lastEnd               sim.VTimeInSec
	owners                uint64 // bit mask of GPMs the unit has lived on (pages' owners)

	counts []uint32 // access counter per device ID

	a *adaptState // P21 tracking entry (nil unless adaptive placement)

	touched bool // P21 first-touch migration: the first vector access was seen
	// accesses held by the running migration (count, sum of hold times):
	// the measured waiting it caused is heldN x end - heldT
	heldN int
	heldT float64

	pages         []vm.Page
	newPAddrs     []uint64
	pendingChunks int
	readyAt       sim.VTimeInSec
	triggerTime   sim.VTimeInSec
	blockStart    sim.VTimeInSec

	waiters   []Waker
	waiterSet map[Waker]bool
}

func (u *unit) blocks(dev uint64) bool {
	switch u.phase {
	case phaseShootdown, phaseDrain, phaseCopy:
		return true
	case phaseQueued, phaseService:
		for _, f := range u.faulters {
			if f == dev {
				return true
			}
		}
	}
	return false
}

func (u *unit) addWaiter(w Waker) {
	if u.waiterSet == nil {
		u.waiterSet = make(map[Waker]bool)
	}
	if u.waiterSet[w] {
		return
	}
	u.waiterSet[w] = true
	u.waiters = append(u.waiters, w)
}

// Frame states, for the invariant checks.
const (
	frameLive    uint8 = iota // mapped, accesses allowed
	frameFrozen               // being copied away: no access may be issued
	frameRetired              // migrated away: no access may ever reach it
)

// ppnInfo is what the manager knows about one physical page (frame). It is
// created by the first access issued to the frame.
type ppnInfo struct {
	pid      vm.PID
	vAddr    uint64 // page-aligned virtual address mapped to the frame
	inflight int32  // accesses issued by address translators, not answered
	devMask  uint64 // GPMs that accessed it (their TLBs may hold it)
	state    uint8
	// lastIdle (P21 copy-first): last time inflight dropped to zero.
	lastIdle sim.VTimeInSec
	// issued accesses and the first issue time (P21 r3: Little's law
	// residence time = inflight / issue rate)
	issued     uint64
	firstIssue sim.VTimeInSec
}

// Violation is the panic value of a broken migration invariant.
type Violation string

func (m *Manager) violation(format string, args ...interface{}) {
	panic(Violation(fmt.Sprintf("page migration invariant: "+format, args...)))
}

// handler is the per-GPM software service (UVM bottom half): it takes up to
// BatchSize queued units at a time and services them for SoftwareLatency.
type handler struct {
	queue     []*unit
	service   []*unit
	busy      bool
	busyUntil sim.VTimeInSec
}

// Stats are the migration statistics.
type Stats struct {
	Faults                uint64 // on-fault triggers
	FirstTouches          uint64 // P21 first-touch migration: units first touched
	CounterTriggers       uint64 // access-counter triggers
	ClassFilteredAccesses uint64 // P19 Q6: remote accesses to pages outside AllowPages
	// P20 -pm-target median/class: decisions when a counter fired.
	TargetDominant, TargetMedian, TargetBroadcast             uint64
	DeclinedSamePlace, DeclinedSmallSaving, DeclinedBroadcast uint64
	LocalCounted, ReplicaUncounted                            uint64
	Batches                                                   uint64
	Migrations                                                uint64 // units migrated
	NoopMigrations                                            uint64 // units with nothing to move when serviced
	PagesMigrated                                             uint64
	BytesMigrated                                             uint64
	PingPong                                                  uint64 // migrations to a GPM the unit lived on before
	Pinned                                                    uint64 // units that reached MaxMigrations
	ShootdownEvents                                           uint64
	TLBEntriesDropped                                         uint64
	RemoteAccesses                                            uint64 // accesses that left an L1 for remote memory
	RemoteReads                                               uint64
	MigrationTimeSum                                          float64 // trigger to remap, seconds
	BlockedTimeSum                                            float64 // shootdown start to remap, seconds
	DrainWaitSum                                              float64
	CopyTimeSum                                               float64
	RemoteReadsByOwner                                        []uint64 // by device ID
	CheckedFrames                                             uint64   // frames verified uncached (Config.Check)
}

// Manager decides and orchestrates migrations. It is a ticking component only
// to advance time-driven steps (software service, shootdown latency, drain
// polling); everything else happens in direct calls from the hooks and the
// copy engines, which is safe because migration requires the serial engine.
type Manager struct {
	*sim.TickingComponent

	cfg          Config
	pageTable    vm.PageTable
	log2PageSize uint64
	unitLog2     uint64

	gpms      []*GPM // by device ID (index 0 unused)
	iommuTLBs []PageInvalidator
	nextFrame []uint64

	units    map[unitKey]*unit
	ppns     map[uint64]*ppnInfo
	handlers []*handler
	active   []*unit

	drainStart map[*unit]sim.VTimeInSec

	stallNext    sim.VTimeInSec // Config.StallWatchNS
	stallReports int
	copyStart    map[*unit]sim.VTimeInSec

	// recent remote reads served per device (median/class tie-break),
	// halved every LoadWindowNS; nil with the argmax target.
	load  []float64
	loadT float64

	// P21 adaptive placement (adapt.go)
	blockHome    map[uint64]uint64 // 1 KB block -> serving device
	buffers      map[bufKey]*buffer
	tileDev      map[int]uint64
	kernel       int
	lastBoundary sim.VTimeInSec
	replicated   map[*unit]bool
	holds        []*unit
	toRetire     []uint64
	replBytes    []uint64  // replica bytes per device (P21)
	drainEMA     []float64 // recent drain time (ns) of migrations per home (P21)
	bhCount      []int     // run-time block-home entries per frame owner (P21)
	trackLRU     []*list.List
	deferred     []deferredAct
	drvEnd       []sim.VTimeInSec // per GPM driver queue (P21 BatchSW)
	drvN         []int
	lookupPass   map[Waker]sim.VTimeInSec
	cmigHit      []uint64 // r3 cmig: fetches by a GPM of its cmig pages served by the pulled lines
	cmigMiss     []uint64 // ... and those that still went to the home
	replOrder    []*unit
	AStats       AdaptStats
	L2Res        L2ResStats // ISCA27 R4
	meanHops     float64
	near         *nearestState // R4 direction 3 statistics
	// Storage is the global storage (only for the oracle replica list).
	Storage *mem.Storage

	Stats Stats
}

// NewManager creates a manager. gpms is indexed by device ID (entry 0, the
// CPU, is nil).
func NewManager(
	name string,
	engine sim.Engine,
	cfg Config,
	pageTable vm.PageTable,
	log2PageSize uint64,
	gpms []*GPM,
	iommuTLBs []PageInvalidator,
) *Manager {
	if err := cfg.Validate(log2PageSize); err != nil {
		panic(err)
	}
	m := &Manager{
		cfg:          cfg,
		pageTable:    pageTable,
		log2PageSize: log2PageSize,
		unitLog2:     cfg.Log2Unit,
		gpms:         gpms,
		iommuTLBs:    iommuTLBs,
		units:        make(map[unitKey]*unit),
		ppns:         make(map[uint64]*ppnInfo),
		drainStart:   make(map[*unit]sim.VTimeInSec),
		copyStart:    make(map[*unit]sim.VTimeInSec),
	}
	m.TickingComponent = sim.NewTickingComponent(name, engine, 1*sim.GHz, m)
	m.nextFrame = make([]uint64, len(gpms))
	m.handlers = make([]*handler, len(gpms))
	for i, g := range gpms {
		m.handlers[i] = &handler{}
		if g == nil {
			continue
		}
		m.nextFrame[i] = g.MemBase + cfg.FrameRegionOffset
		if g.Engine != nil {
			g.Engine.manager = m
		}
	}
	m.Stats.RemoteReadsByOwner = make([]uint64, len(gpms))
	if cfg.Target != TargetArgmax {
		m.load = make([]float64, len(gpms))
	}
	m.replicated = make(map[*unit]bool)
	m.initAdapt()
	return m
}

// Config returns the configuration.
func (m *Manager) Config() Config { return m.cfg }

func ns(n uint64) sim.VTimeInSec { return sim.VTimeInSec(float64(n) * 1e-9) }

func (m *Manager) unitOf(pid vm.PID, vAddr uint64) unitKey {
	return unitKey{pid: pid, idx: vAddr >> m.unitLog2}
}

func (m *Manager) getUnit(k unitKey) *unit {
	u := m.units[k]
	if u == nil {
		u = &unit{key: k}
		if m.cfg.Policy == PolicyAccessCounter {
			u.counts = make([]uint32, len(m.gpms))
		}
		m.units[k] = u
	}
	return u
}

func (m *Manager) eligible(u *unit, now sim.VTimeInSec) bool {
	if !m.cfg.Migrates() || u.phase != phaseIdle {
		return false
	}
	if m.cfg.MaxMigrations > 0 && u.migrations >= m.cfg.MaxMigrations {
		return false
	}
	if u.everMigrated && now-u.lastEnd < ns(m.cfg.CooldownNS) {
		return false
	}
	return true
}

func (m *Manager) ownerOf(pAddr uint64) uint64 {
	return pAddr >> 33 // each GPM maps 8 GB; device i starts at i*8 GB
}

// ATHook returns the hook for one address translator of GPM dev. Only the
// vector-memory translators raise faults (canFault).
func (m *Manager) ATHook(dev uint64, canFault bool) addresstranslator.MigrationHook {
	return &atHook{m: m, dev: dev, canFault: canFault}
}

type atHook struct {
	m         *Manager
	dev       uint64
	canFault  bool
	lastWrite bool
}

// ResolveRW (addresstranslator.MigrationHookRW) is Resolve plus the access
// type, for the P21 replicas and first-touch placement.
func (h *atHook) ResolveRW(
	now sim.VTimeInSec,
	at Waker,
	pid vm.PID,
	vAddr uint64,
	isWrite bool,
	page vm.Page,
) (vm.Page, bool) {
	h.lastWrite = isWrite
	m := h.m
	page, ok := h.Resolve(now, at, pid, vAddr, page)
	if !ok || (!m.cfg.Adapt.Enabled && !m.cfg.Adapt.FirstTouch &&
		m.cfg.Adapt.ReplicaList == nil) {
		return page, ok
	}
	return m.adaptResolve(now, at, h.dev, m.units[m.unitOf(pid, vAddr)],
		h.canFault, isWrite, page)
}

func (h *atHook) Resolve(
	now sim.VTimeInSec,
	at Waker,
	pid vm.PID,
	vAddr uint64,
	page vm.Page,
) (vm.Page, bool) {
	m := h.m
	k := m.unitOf(pid, vAddr)
	u := m.units[k]
	if u != nil {
		if u.blocks(h.dev) {
			u.addWaiter(at)
			u.heldN++
			u.heldT += float64(now)
			return page, false
		}
		if u.everMigrated {
			// The TLB copy may predate the remap; the page table is the
			// authority.
			if p, ok := m.pageTable.Find(pid, vAddr); ok {
				page = p
			}
		}
	}
	if h.canFault && m.cfg.Policy == PolicyOnFault && m.cfg.FirstTouchMigrate {
		// P21 real-cost first touch: only the first vector access to the
		// unit decides; a page already at its first toucher stays.
		if u == nil {
			u = m.getUnit(k)
		}
		if !u.touched {
			u.touched = true
			m.Stats.FirstTouches++
			if page.DeviceID != h.dev && m.eligible(u, now) {
				m.Stats.Faults++
				u.faulters = append(u.faulters[:0], h.dev)
				m.trigger(now, u, h.dev)
				u.addWaiter(at)
				return page, false
			}
		}
		return page, true
	}
	if h.canFault && m.cfg.Policy == PolicyOnFault && page.DeviceID != h.dev {
		if u == nil {
			u = m.getUnit(k)
		}
		if m.eligible(u, now) {
			m.Stats.Faults++
			u.faulters = append(u.faulters[:0], h.dev)
			m.trigger(now, u, h.dev)
			u.addWaiter(at)
			return page, false
		}
	}
	return page, true
}

func (h *atHook) Issued(now sim.VTimeInSec, pid vm.PID, vAddr, pAddr uint64) {
	m := h.m
	ppn := pAddr >> m.log2PageSize
	info := m.ppns[ppn]
	newFrame := info == nil
	if info == nil {
		info = &ppnInfo{pid: pid, vAddr: vAddr >> m.log2PageSize << m.log2PageSize}
		m.ppns[ppn] = info
	}
	if info.state != frameLive {
		m.violation("GPM %d issued an access to frame %#x (va %#x) in state %d",
			h.dev, pAddr, vAddr, info.state)
	}
	info.inflight++
	if info.issued == 0 {
		info.firstIssue = now
	}
	info.issued++
	info.devMask |= 1 << h.dev
	if m.cfg.Adapt.Enabled {
		m.adaptIssued(now, h.dev, pid, vAddr, h.lastWrite, h.canFault, newFrame)
	}
}

func (h *atHook) Completed(now sim.VTimeInSec, pAddr uint64) {
	m := h.m
	info := m.ppns[pAddr>>m.log2PageSize]
	if info == nil || info.inflight <= 0 {
		panic(fmt.Sprintf("page migration: completion without issue at %#x", pAddr))
	}
	info.inflight--
	if info.inflight == 0 {
		info.lastIdle = now
	}
}

// RDMAHook returns the function the RDMA engine of GPM dev calls for every
// access that leaves the L1 for remote memory.
func (m *Manager) RDMAHook(dev uint64) func(now sim.VTimeInSec, req mem.AccessReq) {
	return func(now sim.VTimeInSec, req mem.AccessReq) {
		m.remoteAccess(now, dev, req)
	}
}

func (m *Manager) remoteAccess(now sim.VTimeInSec, dev uint64, req mem.AccessReq) {
	pAddr := req.GetAddress()
	m.Stats.RemoteAccesses++
	if _, ok := req.(*mem.ReadReq); ok {
		m.Stats.RemoteReads++
		owner := m.homeOf(pAddr)
		if owner < uint64(len(m.Stats.RemoteReadsByOwner)) {
			m.Stats.RemoteReadsByOwner[owner]++
		}
		m.noteServed(float64(now), owner)
	}
	info := m.ppns[pAddr>>m.log2PageSize]
	if info == nil {
		return // not translated by an address translator (never expected)
	}
	if info.state == frameRetired {
		m.violation("GPM %d sent a remote access to retired frame %#x", dev, pAddr)
	}
	if m.cfg.Policy != PolicyAccessCounter {
		return
	}
	u := m.getUnit(m.unitOf(info.pid, info.vAddr))
	if m.cfg.Adapt.Enabled {
		if u.phase == phaseIdle {
			m.adaptCount(now, u, dev, pAddr, true)
		}
		return
	}
	if m.cfg.AllowPages != nil {
		if !u.allowChecked {
			u.allowChecked = true
			u.allowed = m.cfg.AllowPages[pAddr>>12]
		}
		if !u.allowed {
			m.Stats.ClassFilteredAccesses++
			return
		}
	}
	if u.phase != phaseIdle {
		return
	}
	u.counts[dev]++
	if u.counts[dev] < m.cfg.Threshold || !m.eligible(u, now) {
		return
	}
	if m.cfg.Target != TargetArgmax {
		m.counterFiredTarget(now, u, pAddr)
		return
	}
	best := dev
	for d, c := range u.counts {
		if c > u.counts[best] {
			best = uint64(d)
		}
	}
	for i := range u.counts {
		u.counts[i] = 0
	}
	m.Stats.CounterTriggers++
	u.faulters = u.faulters[:0]
	m.trigger(now, u, best)
}

// counterFiredTarget is the -pm-target median/class version of the counter
// trigger: choose the destination from the reader distribution, or decline.
func (m *Manager) counterFiredTarget(now sim.VTimeInSec, u *unit, pAddr uint64) {
	dst, dec, kind := m.chooseTarget(u.counts, m.ownerOf(pAddr), u.key.idx)
	for i := range u.counts {
		u.counts[i] = 0
	}
	m.Stats.CounterTriggers++
	switch dec {
	case DecideSamePlace:
		m.Stats.DeclinedSamePlace++
		return
	case DecideSmallSaving:
		m.Stats.DeclinedSmallSaving++
		return
	case DecideBroadcastStay:
		m.Stats.DeclinedBroadcast++
		return
	}
	switch kind {
	case "dominant":
		m.Stats.TargetDominant++
	case "broadcast":
		m.Stats.TargetBroadcast++
	default:
		m.Stats.TargetMedian++
	}
	u.faulters = u.faulters[:0]
	m.trigger(now, u, dst)
}

func (m *Manager) trigger(now sim.VTimeInSec, u *unit, dst uint64) {
	u.dst = dst
	u.phase = phaseQueued
	u.triggerTime = now
	h := m.handlers[dst]
	h.queue = append(h.queue, u)
	m.TickLater(now)
}

// Tick advances the software handlers (start a batch, finish a batch) and the
// migrations in flight (shootdown latency elapsed -> drain; drained -> copy).
// The copy finishes in chunkDone, called by the destination copy engine.
func (m *Manager) Tick(now sim.VTimeInSec) bool {
	progress := false
	for _, h := range m.handlers {
		if h.busy && now >= h.busyUntil {
			h.busy = false
			for _, u := range h.service {
				m.startAfterService(now, u)
			}
			h.service = h.service[:0]
			progress = true
		}
		if !h.busy && len(h.queue) > 0 {
			n := len(h.queue)
			if n > m.cfg.BatchSize {
				n = m.cfg.BatchSize
			}
			batch := append([]*unit(nil), h.queue[:n]...)
			h.queue = h.queue[n:]
			m.Stats.Batches++
			for _, u := range batch {
				u.phase = phaseService
			}
			if m.cfg.SoftwareLatencyNS == 0 {
				for _, u := range batch {
					m.startAfterService(now, u)
				}
			} else {
				h.service = batch
				h.busy = true
				h.busyUntil = now + ns(m.cfg.SoftwareLatencyNS)
			}
			progress = true
		}
		if h.busy {
			progress = true // waiting for the service time
		}
	}

	if (m.cfg.Adapt.Enabled || m.cfg.Adapt.ReplicaList != nil) && m.adaptTick(now) {
		progress = true
	}

	stillActive := m.active[:0]
	for _, u := range m.active {
		switch u.phase {
		case phaseShootdown:
			if now >= u.readyAt {
				u.phase = phaseDrain
				m.drainStart[u] = now
			}
		}
		if u.phase == phaseDrain && m.drained(u) {
			m.Stats.DrainWaitSum += float64(now - m.drainStart[u])
			if u.a != nil && len(u.pages) > 0 {
				m.noteDrain(m.homeOf(u.pages[0].PAddr), float64(now-m.drainStart[u])*1e9)
			}
			delete(m.drainStart, u)
			if u.a != nil && u.a.copied {
				m.adaptSwitch(now, u)
			} else {
				m.startCopy(now, u)
			}
		}
		if u.phase != phaseIdle {
			stillActive = append(stillActive, u)
		}
	}
	m.active = stillActive
	if len(m.active) > 0 {
		progress = true
	}
	if m.cfg.StallWatchNS > 0 {
		m.stallWatch(now)
	}
	return progress
}

func nbuf(p sim.Port) int {
	if l, ok := p.(*sim.LimitNumMsgPort); ok {
		return l.NumBuffered()
	}
	return -1
}

// stallWatch (Config.StallWatchNS, diagnostic) prints the units that have
// been migrating for longer than StallWatchNS, with the in-flight accesses of
// their frames.
func (m *Manager) stallWatch(now sim.VTimeInSec) {
	if now < m.stallNext {
		return
	}
	m.stallNext = now + ns(m.cfg.StallWatchNS)
	var stuck []*unit
	for _, u := range m.active {
		if now-u.triggerTime > ns(m.cfg.StallWatchNS) {
			stuck = append(stuck, u)
		}
	}
	queued := 0
	for _, h := range m.handlers {
		queued += len(h.queue) + len(h.service)
		for _, u := range append(append([]*unit(nil), h.queue...), h.service...) {
			if now-u.triggerTime > ns(m.cfg.StallWatchNS) {
				stuck = append(stuck, u)
			}
		}
	}
	if len(stuck) == 0 {
		return
	}
	m.stallReports++
	if m.stallReports > 3 && m.cfg.StallReports == 0 {
		return // keep running quietly (inspect it through the monitor)
	}
	fmt.Fprintf(os.Stderr, "PMSTALL report=%d now=%.9f active=%d queued_or_service=%d stuck=%d\n",
		m.stallReports, float64(now), len(m.active), queued, len(stuck))
	for i, u := range stuck {
		if i >= 40 {
			break
		}
		fmt.Fprintf(os.Stderr, "PMSTALL unit pid=%d idx=%#x phase=%d dst=%d trigger=%.9f block=%.9f faulters=%v waiters=%d migrations=%d pages=%d pending=%d\n",
			u.key.pid, u.key.idx, u.phase, u.dst, float64(u.triggerTime), float64(u.blockStart),
			u.faulters, len(u.waiters), u.migrations, len(u.pages), u.pendingChunks)
		for _, p := range u.pages {
			if info := m.ppns[p.PAddr>>m.log2PageSize]; info != nil {
				fmt.Fprintf(os.Stderr, "PMSTALL   page va=%#x pa=%#x dev=%d inflight=%d issued=%d devMask=%#x state=%d lastIdle=%.9f\n",
					p.VAddr, p.PAddr, p.DeviceID, info.inflight, info.issued, info.devMask, info.state, float64(info.lastIdle))
			} else {
				fmt.Fprintf(os.Stderr, "PMSTALL   page va=%#x pa=%#x dev=%d (no frame info)\n", p.VAddr, p.PAddr, p.DeviceID)
			}
		}
	}
	for d, g := range m.gpms {
		if g == nil || g.Engine == nil {
			continue
		}
		e := g.Engine
		if !e.Busy() {
			continue
		}
		fmt.Fprintf(os.Stderr, "PMSTALL engine gpm=%d pending=%d urgent=%d pulling=%d inWindow=%d writes=%d writeChunks=%d reads=%d readOwners=%d l2Inflight=%d dramInflight=%d rsps=%d invals=%d remoteBuf=%d l2Buf=%d\n",
			d, len(e.pending), len(e.urgentQ), len(e.pulling), e.inWindow, len(e.writes), len(e.writeChunks),
			len(e.reads), len(e.readOwners), e.l2Inflight, e.dramInflight, len(e.rsps), len(e.invals),
			nbuf(e.remotePort), nbuf(e.l2Port))
	}
	if m.cfg.StallReports > 0 && m.stallReports >= m.cfg.StallReports {
		panic(fmt.Sprintf("page migration: %d units stuck for more than %d ns (-pm-stall-watch-ns)",
			len(stuck), m.cfg.StallWatchNS))
	}
}

// startAfterService starts the shootdown of a serviced unit, or (P21
// copy-first) its copy.
func (m *Manager) startAfterService(now sim.VTimeInSec, u *unit) {
	if u.a != nil && u.a.act != actStay && m.cfg.Adapt.CopyFirst {
		m.adaptPreCopy(now, u)
		return
	}
	m.startShootdown(now, u)
}

// collectPages fills u.pages with the pages of the unit that must move; it
// returns false (and ends the migration as a no-op) when there are none.
func (m *Manager) collectPages(now sim.VTimeInSec, u *unit) bool {
	pageSize := uint64(1) << m.log2PageSize
	start := u.key.idx << m.unitLog2
	end := start + (uint64(1) << m.unitLog2)
	u.pages = u.pages[:0]
	for va := start; va < end; va += pageSize {
		p, ok := m.pageTable.Find(u.key.pid, va)
		if !ok || p.VAddr != va {
			continue
		}
		if u.a != nil && u.a.act != actStay {
			if !m.adaptNeedsMove(u, p) {
				continue
			}
		} else if p.DeviceID == u.dst {
			continue
		}
		u.pages = append(u.pages, p)
	}
	if len(u.pages) == 0 {
		m.Stats.NoopMigrations++
		if u.a != nil {
			u.a.act = actStay
		}
		u.phase = phaseIdle
		u.faulters = u.faulters[:0]
		m.wake(now, u)
		return false
	}
	return true
}

func (m *Manager) startShootdown(now sim.VTimeInSec, u *unit) {
	if !m.collectPages(now, u) {
		return
	}
	m.beginShootdown(now, u)
	m.active = append(m.active, u)
}

// beginShootdown invalidates the TLB entries of u.pages and holds accesses.
func (m *Manager) beginShootdown(now sim.VTimeInSec, u *unit) {
	u.phase = phaseShootdown
	u.blockStart = now
	u.readyAt = now + ns(m.cfg.ShootdownLatencyNS)
	m.Stats.ShootdownEvents++
	for _, p := range u.pages {
		m.invalidate(p)
	}
}

// invalidate drops the TLB entries of page p in every GPM that accessed it
// and in the IOMMU TLB.
func (m *Manager) invalidate(p vm.Page) {
	var mask uint64
	if info := m.ppns[p.PAddr>>m.log2PageSize]; info != nil {
		mask = info.devMask
	}
	for dev, g := range m.gpms {
		if g == nil || mask&(1<<uint(dev)) == 0 {
			continue
		}
		for _, t := range g.TLBs {
			if t.InvalidatePage(p.PID, p.VAddr) {
				m.Stats.TLBEntriesDropped++
			}
		}
	}
	for _, t := range m.iommuTLBs {
		if t.InvalidatePage(p.PID, p.VAddr) {
			m.Stats.TLBEntriesDropped++
		}
	}
}

func (m *Manager) drained(u *unit) bool {
	for _, p := range u.pages {
		if info := m.ppns[p.PAddr>>m.log2PageSize]; info != nil && info.inflight > 0 {
			return false
		}
	}
	return true
}

func (m *Manager) allocFrame(dev uint64) uint64 {
	g := m.gpms[dev]
	pageSize := uint64(1) << m.log2PageSize
	f := m.nextFrame[dev]
	if f+pageSize > g.MemBase+m.cfg.FrameRegionOffset+m.cfg.FrameRegionSize {
		panic(fmt.Sprintf("page migration: GPM %d ran out of migration frames", dev))
	}
	m.nextFrame[dev] = f + pageSize
	// A new frame has never been accessed, so no L1, L2 or M3 replica can
	// hold a line of it; the copy may write it in DRAM directly.
	if m.ppns[f>>m.log2PageSize] != nil {
		m.violation("new frame %#x was accessed before", f)
	}
	if m.cfg.Check {
		m.checkNoCachedLines(f, "allocation")
	}
	return f
}

// checkNoCachedLines (Config.Check) verifies that no L2 slice of any GPM
// holds or is fetching a line of the frame.
func (m *Manager) checkNoCachedLines(frame uint64, when string) {
	pageSize := uint64(1) << m.log2PageSize
	for a := frame; a < frame+pageSize; a += m.cfg.LineBytes {
		for _, g := range m.gpms {
			if g != nil && g.HoldsLine != nil && g.HoldsLine(a) {
				m.violation("GPM %d caches line %#x of new frame %#x at %s",
					g.DeviceID, a, frame, when)
			}
		}
	}
	m.Stats.CheckedFrames++
}

// frameInfo returns the record of the frame mapped by p, creating it for a
// frame that no access has touched yet.
func (m *Manager) frameInfo(p vm.Page) *ppnInfo {
	ppn := p.PAddr >> m.log2PageSize
	info := m.ppns[ppn]
	if info == nil {
		info = &ppnInfo{pid: p.PID, vAddr: p.VAddr}
		m.ppns[ppn] = info
	}
	return info
}

func (m *Manager) startCopy(now sim.VTimeInSec, u *unit) {
	u.phase = phaseCopy
	m.copyStart[u] = now
	pageSize := uint64(1) << m.log2PageSize
	dstEngine := m.gpms[u.dst].Engine
	u.newPAddrs = u.newPAddrs[:0]
	u.pendingChunks = 0
	if u.a != nil && u.a.act != actStay {
		m.adaptStartCopy(now, u, true)
		return
	}
	for _, p := range u.pages {
		m.frameInfo(p).state = frameFrozen
		frame := m.allocFrame(u.dst)
		u.newPAddrs = append(u.newPAddrs, frame)
		src := m.gpms[p.DeviceID].Engine
		if m.cfg.TracePages[p.PAddr] {
			fmt.Fprintf(os.Stderr, "PMTRACE %.9f manager pa=%#x startCopy unit=%#x dst=%d src=%d\n", float64(now), p.PAddr, u.key.idx, u.dst, p.DeviceID)
		}
		for off := uint64(0); off < pageSize; off += m.cfg.ChunkBytes {
			dstEngine.enqueue(now, &chunk{
				u:        u,
				src:      src,
				srcPAddr: p.PAddr + off,
				dstPAddr: frame + off,
				size:     m.cfg.ChunkBytes,
			})
			u.pendingChunks++
		}
	}
}

func (m *Manager) chunkDone(now sim.VTimeInSec, c *chunk) {
	u := c.u
	if m.cfg.TracePages[c.srcPAddr>>12<<12] {
		fmt.Fprintf(os.Stderr, "PMTRACE %.9f manager pa=%#x chunkDone unit=%#x pending=%d phase=%d\n", float64(now), c.srcPAddr, u.key.idx, u.pendingChunks, u.phase)
	}
	u.pendingChunks--
	if u.pendingChunks > 0 {
		return
	}
	if u.phase == phasePreCopy {
		m.adaptPreCopyDone(now, u)
		return
	}
	if t, ok := m.copyStart[u]; ok {
		m.Stats.CopyTimeSum += float64(now - t)
		delete(m.copyStart, u)
	}
	m.remap(now, u)
}

func (m *Manager) remap(now sim.VTimeInSec, u *unit) {
	pageSize := uint64(1) << m.log2PageSize
	var prevOwners uint64
	for i, p := range u.pages {
		if m.cfg.Check {
			// Nothing may have cached the new frame while it was filled.
			m.checkNoCachedLines(u.newPAddrs[i], "remap")
		}
		m.frameInfo(p).state = frameRetired
		if u.a != nil && u.a.act != actStay {
			m.adaptRetireFrameHomes(p.PAddr)
		}
		prevOwners |= 1 << p.DeviceID
		p.PAddr = u.newPAddrs[i]
		p.DeviceID = u.dst
		m.pageTable.Update(p)
		if u.a != nil && u.a.act != actStay {
			m.adaptRemapFrame(u, p.PAddr)
		}
		m.Stats.PagesMigrated++
		m.Stats.BytesMigrated += pageSize
	}
	if u.owners&(1<<u.dst) != 0 {
		m.Stats.PingPong++
	}
	u.owners |= prevOwners
	u.migrations++
	if m.cfg.MaxMigrations > 0 && u.migrations == m.cfg.MaxMigrations {
		m.Stats.Pinned++
	}
	u.everMigrated = true
	u.lastEnd = now
	if u.a != nil {
		u.a.act = actStay
		u.a.copied = false
	}
	if u.a != nil {
		m.noteHeld(u, float64(now))
	}
	u.heldN, u.heldT = 0, 0
	m.Stats.Migrations++
	m.Stats.MigrationTimeSum += float64(now - u.triggerTime)
	m.Stats.BlockedTimeSum += float64(now - u.blockStart)
	u.phase = phaseIdle
	u.faulters = u.faulters[:0]
	u.pages = u.pages[:0]
	m.wake(now, u)
	m.TickLater(now)
}

func (m *Manager) wake(now sim.VTimeInSec, u *unit) {
	for _, w := range u.waiters {
		w.TickLater(now)
	}
	u.waiters = u.waiters[:0]
	u.waiterSet = nil
}

// UnitsTouched returns the number of units the manager tracked.
func (m *Manager) UnitsTouched() int { return len(m.units) }

// HotOwnerShare returns the largest share of remote reads served by one
// owner GPM and that owner.
func (m *Manager) HotOwnerShare() (float64, uint64) {
	var total, best uint64
	var owner uint64
	for d, n := range m.Stats.RemoteReadsByOwner {
		total += n
		if n > best {
			best, owner = n, uint64(d)
		}
	}
	if total == 0 {
		return 0, 0
	}
	return float64(best) / float64(total), owner
}

// MigrationsPerUnitHistogram returns how many units migrated k times.
func (m *Manager) MigrationsPerUnitHistogram() map[int]int {
	h := make(map[int]int)
	for _, u := range m.units {
		if u.migrations > 0 {
			h[u.migrations]++
		}
	}
	return h
}

// SortedKeys returns the keys of a histogram in order.
func SortedKeys(h map[int]int) []int {
	keys := make([]int, 0, len(h))
	for k := range h {
		keys = append(keys, k)
	}
	sort.Ints(keys)
	return keys
}

// Busy reports whether migrations are in progress.
func (m *Manager) Busy() bool {
	if len(m.active) > 0 || len(m.holds) > 0 {
		return true
	}
	for _, h := range m.handlers {
		if h.busy || len(h.queue) > 0 {
			return true
		}
	}
	return false
}

// FrameUsage reports the migration frame region use: the most bytes taken
// on one GPM (frames are never reused), the total, and the bytes of frames
// retired by the end (what frame recycling could give back).
func (m *Manager) FrameUsage() (maxPerGPM, total, retired uint64) {
	ps := uint64(1) << m.log2PageSize
	for d, g := range m.gpms {
		if g == nil {
			continue
		}
		used := m.nextFrame[d] - (g.MemBase + m.cfg.FrameRegionOffset)
		total += used
		if used > maxPerGPM {
			maxPerGPM = used
		}
	}
	for _, info := range m.ppns {
		if info.state == frameRetired {
			retired += ps
		}
	}
	return
}
