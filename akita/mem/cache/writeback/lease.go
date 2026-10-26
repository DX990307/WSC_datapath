package writeback

import (
	"container/list"
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
	el          *list.Element
}

// LeaseTable keeps the per-page re-use counts of one GPM's remote fetches.
type LeaseTable struct {
	cfg   LeaseTableConfig
	lru   *list.List
	m     map[uint64]*leaseEntry
	stats LeaseTableStats
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
	} else {
		e.reus++
	}
	if e.first+e.reus >= t.cfg.MaxCount {
		e.first /= 2
		e.reus /= 2
	}
}

// NoteFetch records a network fetch of line; refetch = the requester had
// fetched the line before (its seen filter hit).
func (t *LeaseTable) NoteFetch(line uint64, refetch bool) {
	if refetch {
		t.stats.Refetches++
	} else {
		t.stats.Fetches++
	}
	t.bump(t.entry(line, true), !refetch)
}

// NoteHit records an L2 hit on a leased (remote) line.
func (t *LeaseTable) NoteHit(line uint64) {
	t.stats.Hits++
	if e := t.entry(line, false); e != nil {
		t.bump(e, false)
	}
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
	return (e.reus + 2*prior) / (e.reus + e.first + 2)
}

// LeaseStats counts the lease decisions of one L2 slice.
type LeaseStats struct {
	BlockedFills   uint64 // fills of pages with a pending migration (value 0)
	BlockedDrops   uint64 // ... dropped (no invalid way)
	ValueSum       float64
	ValueN         uint64
	DropsByValue   uint64
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
	p := c.costFinder.Reuse(1, ins)
	if c.lease != nil {
		p = c.lease.Reuse(line, p)
	}
	v := c.costFinder.Cost(line) * p
	c.leaseStats.ValueSum += v
	c.leaseStats.ValueN++
	return v
}
