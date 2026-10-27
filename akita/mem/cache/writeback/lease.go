package writeback

import (
	"container/list"

	cachepkg "github.com/sarchlab/akita/v3/mem/cache"
)

// ISCA27 lease: whether a remote line is cached in the requester's L2
// ("lease") is decided by the same value as the replacement of R4 (cost-aware
// replacement, l2res.go): value = E[re-use] x re-fetch cost. The re-fetch
// cost is the R3 round-trip model (remote) or the measured local miss
// latency (local). What the lease adds:
//
//   - E[re-use] of an incoming remote line is measured per page (LeaseTable,
//     one per GPM, shared by its L2 slices): among the network fetches and
//     L2 replica hits of the page's lines by this GPM, the share that were
//     re-uses (a replica hit, or a re-fetch of a line the requester had
//     fetched before = a re-use the L2 did not keep). Pages not in the table
//     and the prior of new entries use the slice's measured class rate
//     (remote lines at the insertion state, as R4 b).
//   - A remote fill with no invalid way is installed iff its value exceeds
//     the lowest-value clean victim of the set (R4 b's rule with this
//     E[re-use]); local lines are protected only by their value.
//   - Buy interplay: a line of a page with a migration pending or in flight
//     (it will change address, so the cached copy dies) has value 0: it may
//     use an invalid way but never evicts anything (LeaseBlocked).

// LeaseTableConfig configures a per-GPM lease table.
type LeaseTableConfig struct {
	Log2PageSize uint // 12 = 4 KiB
	Entries      int  // pages tracked per GPM (LRU); <= 0 = unlimited
	// MaxCount: counters of an entry are halved when their sum reaches it.
	MaxCount float64
	// LineBits: a re-fetch is detected by a per-entry bit per 64-B line
	// (else by the requester's seen filter).
	LineBits bool
	// Episodes: E[re-use] = share of fetches (episodes) whose line was
	// accessed again (an L2 hit or a re-fetch) before the GPM's next fetch
	// of it; one count per episode, the same quantity as the victims'
	// P(next event is a hit). Otherwise every hit counts.
	Episodes bool
	// PriorN: weight of the prior in pseudo-events (0 = 2, Laplace).
	PriorN float64
}

// LeaseTableStats counts the table's work.
type LeaseTableStats struct {
	Fetches, Refetches, Hits uint64
	Allocs, Replacements     uint64
	Lookups, LookupMisses    uint64
}

type leaseEntry struct {
	page        uint64
	first, reus float64
	fetched     uint64 // lines of the page fetched before (64 x 64 B in 4 KiB)
	open        uint64 // Episodes: lines whose last fetch has not been re-used yet
	el          *list.Element
}

// LeaseTable keeps the per-page re-use counts of one GPM's remote fetches.
type LeaseTable struct {
	cfg LeaseTableConfig
	// all pages (Symmetric): episodes and re-used episodes, decayed
	allFirst, allReus float64
	lru               *list.List
	m                 map[uint64]*leaseEntry
	stats             LeaseTableStats
}

// NewLeaseTable creates a lease table.
func NewLeaseTable(cfg LeaseTableConfig) *LeaseTable {
	if cfg.Log2PageSize == 0 {
		cfg.Log2PageSize = 12
	}
	if cfg.MaxCount <= 0 {
		cfg.MaxCount = 256
	}
	return &LeaseTable{cfg: cfg, lru: list.New(), m: make(map[uint64]*leaseEntry)}
}

// Stats returns the counters.
func (t *LeaseTable) Stats() LeaseTableStats { return t.stats }

func (t *LeaseTable) entry(line uint64, alloc bool) *leaseEntry {
	p := line >> t.cfg.Log2PageSize
	e := t.m[p]
	if e != nil {
		t.lru.MoveToFront(e.el)
		return e
	}
	if !alloc {
		return nil
	}
	if t.cfg.Entries > 0 && len(t.m) >= t.cfg.Entries {
		old := t.lru.Back().Value.(*leaseEntry)
		t.lru.Remove(old.el)
		delete(t.m, old.page)
		t.stats.Replacements++
	}
	e = &leaseEntry{page: p}
	e.el = t.lru.PushFront(e)
	t.m[p] = e
	t.stats.Allocs++
	return e
}

func (t *LeaseTable) bump(e *leaseEntry, first bool) {
	if first {
		e.first++
		t.allFirst++
	} else {
		e.reus++
		t.allReus++
	}
	if t.allFirst+t.allReus >= 65536 {
		t.allFirst /= 2
		t.allReus /= 2
	}
	if e.first+e.reus >= t.cfg.MaxCount {
		e.first /= 2
		e.reus /= 2
	}
}

// NoteFetch records a network fetch of line. With LineBits the entry's
// bit vector tells whether the GPM fetched the line before (a re-fetch is a
// re-use the L2 did not keep, across kernels too); otherwise refetch (the
// requester's seen filter hit, cleared at kernel boundaries) does.
func (t *LeaseTable) NoteFetch(line uint64, refetch bool) {
	e := t.entry(line, true)
	if t.cfg.Episodes {
		bit := uint64(1) << ((line >> 6) & 63)
		if e.fetched&bit != 0 {
			t.stats.Refetches++
		} else {
			t.stats.Fetches++
		}
		if e.open&bit != 0 { // the previous episode ends with this re-use
			e.reus++
			t.allReus++
		}
		e.fetched |= bit
		e.open |= bit
		t.bump(e, true) // a new episode
		return
	}
	if t.cfg.LineBits {
		bit := uint64(1) << ((line >> 6) & 63)
		refetch = e.fetched&bit != 0
		e.fetched |= bit
	}
	if refetch {
		t.stats.Refetches++
	} else {
		t.stats.Fetches++
	}
	t.bump(e, !refetch)
}

// NoteHit records an L2 hit on a leased (remote) line.
func (t *LeaseTable) NoteHit(line uint64) {
	t.stats.Hits++
	e := t.entry(line, false)
	if e == nil {
		return
	}
	if t.cfg.Episodes {
		bit := uint64(1) << ((line >> 6) & 63)
		if e.open&bit != 0 {
			e.open &^= bit
			e.reus++
			t.allReus++
			if e.first+e.reus >= t.cfg.MaxCount {
				e.first /= 2
				e.reus /= 2
			}
		}
		return
	}
	t.bump(e, false)
}

// Reuse is the re-use probability of a line of the page of line: the
// measured share of re-uses among the page's events, with the slice's class
// rate prior as two pseudo-events (Laplace with a measured prior).
func (t *LeaseTable) Reuse(line uint64, prior float64) float64 {
	t.stats.Lookups++
	e := t.m[line>>t.cfg.Log2PageSize]
	if e == nil {
		t.stats.LookupMisses++
		return prior
	}
	n := t.cfg.PriorN
	if n <= 0 {
		n = 2
	}
	if t.cfg.Episodes { // first = episodes, reus = re-used episodes
		return (e.reus + n*prior) / (e.first + n)
	}
	return (e.reus + n*prior) / (e.reus + e.first + n)
}

// Multiplier (Symmetric) is the re-use of the page of line relative to the
// average over all pages of the table (1 for pages not in the table).
func (t *LeaseTable) Multiplier(line uint64) float64 {
	avg := (t.allReus + 1) / (t.allFirst + 2)
	if !t.cfg.Episodes {
		avg = (t.allReus + 1) / (t.allReus + t.allFirst + 2)
	}
	e := t.m[line>>t.cfg.Log2PageSize]
	if e == nil {
		return 1
	}
	return t.Reuse(line, avg) / avg
}

// LeaseStats counts the lease decisions of one L2 slice.
type LeaseStats struct {
	BlockedFills   uint64 // fills of pages with a pending migration (value 0)
	BlockedDrops   uint64 // ... dropped (no invalid way)
	ValueSum       float64
	ValueN         uint64
	DropsByValue   uint64
	GhostHits      uint64 // local lines re-read after a remote fill evicted them
	InstallsByEvic uint64
}

// SetLease puts the slice under the lease rule: the per-GPM table t (nil =
// the class rate, ablation b) and blocked (nil = no buy interplay) telling
// whether the page of a line has a migration pending or in flight.
func (c *Cache) SetLease(t *LeaseTable, blocked func(line uint64) bool) {
	if c.costFinder == nil {
		panic("lease needs cost-aware replacement")
	}
	c.leaseOn = true
	c.lease = t
	c.leaseBlocked = blocked
}

// SetLeaseSymmetric makes the page re-use a multiplier of the class x state
// probability, applied to incoming and resident remote lines alike.
func (c *Cache) SetLeaseSymmetric() {
	if c.lease == nil {
		panic("symmetric lease needs the page table")
	}
	c.leaseSym = true
	c.costFinder.Scale = c.lease.Multiplier
}

// GetLeaseStats returns the lease counters of the slice.
func (c *Cache) GetLeaseStats() LeaseStats { return c.leaseStats }

// leaseNoteFetch records a network fetch at the slice's GPM table.
func (c *Cache) leaseNoteFetch(line uint64, refetch bool) {
	if c.lease != nil {
		c.lease.NoteFetch(line, refetch)
	}
}

func (c *Cache) leaseNoteHit(line uint64) {
	if c.lease != nil {
		c.lease.NoteHit(line)
	}
}

// leaseValue is the value of an incoming remote line inserted at RRPV ins.
func (c *Cache) leaseValue(line uint64, ins uint8) float64 {
	if c.leaseBlocked != nil && c.leaseBlocked(line) {
		c.leaseStats.BlockedFills++
		return 0
	}
	var v float64
	if c.leaseSym {
		v = c.costFinder.ScoreOf(line, ins)
	} else {
		p := c.costFinder.Reuse(1, ins)
		if c.lease != nil {
			p = c.lease.Reuse(line, p)
		}
		v = c.costFinder.Cost(line) * p
	}
	c.leaseStats.ValueSum += v
	c.leaseStats.ValueN++
	return v
}

// Ghosts (-lease-ghost): local lines evicted by remote fills, with the
// state they were evicted in; a later demand read miss of such a line turns
// that eviction into a hit in the local class statistics, so the value of
// local lines includes re-reads that only happen because they were evicted.
// FIFO of at most as many entries as the slice has blocks.
func (c *Cache) EnableLeaseGhost() {
	c.leaseGhost = make(map[uint64]uint8)
	n := 0
	for _, set := range c.directory.(*cachepkg.DirectoryImpl).GetSets() {
		n += len(set.Blocks)
	}
	c.leaseGhostCap = n
}

func (c *Cache) leaseGhostAdd(line uint64, rrpv uint8) {
	if c.leaseGhost == nil {
		return
	}
	if _, ok := c.leaseGhost[line]; !ok {
		c.leaseGhostQ = append(c.leaseGhostQ, line)
	}
	c.leaseGhost[line] = rrpv
	for len(c.leaseGhostQ) > c.leaseGhostCap {
		delete(c.leaseGhost, c.leaseGhostQ[0])
		c.leaseGhostQ = c.leaseGhostQ[1:]
	}
}

func (c *Cache) leaseGhostMiss(line uint64) {
	if r, ok := c.leaseGhost[line]; ok {
		delete(c.leaseGhost, line)
		c.costFinder.Regret(0, r)
		c.leaseStats.GhostHits++
	}
}
