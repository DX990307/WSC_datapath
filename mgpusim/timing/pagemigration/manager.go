package pagemigration

import (
	"fmt"
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
	MemBase  uint64
	TLBs     []PageInvalidator
	Engine   *CopyEngine
}

type phase int

const (
	phaseIdle phase = iota
	phaseQueued
	phaseService
	phaseShootdown
	phaseDrain
	phaseCopy
)

type unitKey struct {
	pid vm.PID
	idx uint64
}

type unit struct {
	key   unitKey
	phase phase
	dst   uint64

	// faulters are the GPMs whose accesses wait during the queued and
	// service phases (on-fault policy).
	faulters []uint64

	migrations   int
	everMigrated bool
	lastEnd      sim.VTimeInSec
	owners       uint64 // bit mask of GPMs the unit has lived on (pages' owners)

	counts []uint32 // access counter per device ID

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

// ppnInfo is what the manager knows about one physical page.
type ppnInfo struct {
	pid      vm.PID
	vAddr    uint64 // page-aligned
	inflight int32
	devMask  uint64 // GPMs that accessed it (their TLBs may hold it)
}

type handler struct {
	queue     []*unit
	service   []*unit
	busy      bool
	busyUntil sim.VTimeInSec
}

// Stats are the migration statistics.
type Stats struct {
	Faults             uint64 // on-fault triggers
	CounterTriggers    uint64 // access-counter triggers
	Batches            uint64
	Migrations         uint64 // units migrated
	NoopMigrations     uint64 // units with nothing to move when serviced
	PagesMigrated      uint64
	BytesMigrated      uint64
	PingPong           uint64 // migrations to a GPM the unit lived on before
	Pinned             uint64 // units that reached MaxMigrations
	ShootdownEvents    uint64
	TLBEntriesDropped  uint64
	RemoteAccesses     uint64 // accesses that left an L1 for remote memory
	RemoteReads        uint64
	MigrationTimeSum   float64 // trigger to remap, seconds
	BlockedTimeSum     float64 // shootdown start to remap, seconds
	DrainWaitSum       float64
	CopyTimeSum        float64
	RemoteReadsByOwner []uint64 // by device ID
}

// Manager decides and orchestrates migrations.
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
	copyStart  map[*unit]sim.VTimeInSec

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
	m        *Manager
	dev      uint64
	canFault bool
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
	if info == nil {
		info = &ppnInfo{pid: pid, vAddr: vAddr >> m.log2PageSize << m.log2PageSize}
		m.ppns[ppn] = info
	}
	info.inflight++
	info.devMask |= 1 << h.dev
}

func (h *atHook) Completed(now sim.VTimeInSec, pAddr uint64) {
	m := h.m
	info := m.ppns[pAddr>>m.log2PageSize]
	if info == nil || info.inflight <= 0 {
		panic(fmt.Sprintf("page migration: completion without issue at %#x", pAddr))
	}
	info.inflight--
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
		owner := m.ownerOf(pAddr)
		if owner < uint64(len(m.Stats.RemoteReadsByOwner)) {
			m.Stats.RemoteReadsByOwner[owner]++
		}
	}
	if m.cfg.Policy != PolicyAccessCounter {
		return
	}
	info := m.ppns[pAddr>>m.log2PageSize]
	if info == nil {
		return // not translated by an address translator (never expected)
	}
	u := m.getUnit(m.unitOf(info.pid, info.vAddr))
	if u.phase != phaseIdle {
		return
	}
	u.counts[dev]++
	if u.counts[dev] < m.cfg.Threshold || !m.eligible(u, now) {
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

func (m *Manager) trigger(now sim.VTimeInSec, u *unit, dst uint64) {
	u.dst = dst
	u.phase = phaseQueued
	u.triggerTime = now
	h := m.handlers[dst]
	h.queue = append(h.queue, u)
	m.TickLater(now)
}

// Tick advances the software handlers and the migrations in flight.
func (m *Manager) Tick(now sim.VTimeInSec) bool {
	progress := false
	for _, h := range m.handlers {
		if h.busy && now >= h.busyUntil {
			h.busy = false
			for _, u := range h.service {
				m.startShootdown(now, u)
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
					m.startShootdown(now, u)
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
			delete(m.drainStart, u)
			m.startCopy(now, u)
		}
		if u.phase != phaseIdle {
			stillActive = append(stillActive, u)
		}
	}
	m.active = stillActive
	if len(m.active) > 0 {
		progress = true
	}
	return progress
}

func (m *Manager) startShootdown(now sim.VTimeInSec, u *unit) {
	pageSize := uint64(1) << m.log2PageSize
	start := u.key.idx << m.unitLog2
	end := start + (uint64(1) << m.unitLog2)
	u.pages = u.pages[:0]
	for va := start; va < end; va += pageSize {
		p, ok := m.pageTable.Find(u.key.pid, va)
		if !ok || p.VAddr != va || p.DeviceID == u.dst {
			continue
		}
		u.pages = append(u.pages, p)
	}
	if len(u.pages) == 0 {
		m.Stats.NoopMigrations++
		u.phase = phaseIdle
		u.faulters = u.faulters[:0]
		m.wake(now, u)
		return
	}

	u.phase = phaseShootdown
	u.blockStart = now
	u.readyAt = now + ns(m.cfg.ShootdownLatencyNS)
	m.Stats.ShootdownEvents++
	for _, p := range u.pages {
		m.invalidate(p)
	}
	m.active = append(m.active, u)
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
	return f
}

func (m *Manager) startCopy(now sim.VTimeInSec, u *unit) {
	u.phase = phaseCopy
	m.copyStart[u] = now
	pageSize := uint64(1) << m.log2PageSize
	dstEngine := m.gpms[u.dst].Engine
	u.newPAddrs = u.newPAddrs[:0]
	u.pendingChunks = 0
	for _, p := range u.pages {
		frame := m.allocFrame(u.dst)
		u.newPAddrs = append(u.newPAddrs, frame)
		src := m.gpms[p.DeviceID].Engine
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
	u.pendingChunks--
	if u.pendingChunks > 0 {
		return
	}
	m.Stats.CopyTimeSum += float64(now - m.copyStart[u])
	delete(m.copyStart, u)
	m.remap(now, u)
}

func (m *Manager) remap(now sim.VTimeInSec, u *unit) {
	pageSize := uint64(1) << m.log2PageSize
	var prevOwners uint64
	for i, p := range u.pages {
		prevOwners |= 1 << p.DeviceID
		p.PAddr = u.newPAddrs[i]
		p.DeviceID = u.dst
		m.pageTable.Update(p)
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
	if len(m.active) > 0 {
		return true
	}
	for _, h := range m.handlers {
		if h.busy || len(h.queue) > 0 {
			return true
		}
	}
	return false
}
