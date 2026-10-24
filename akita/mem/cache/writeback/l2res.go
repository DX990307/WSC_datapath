package writeback

import (
	"container/list"
	"sort"

	cachepkg "github.com/sarchlab/akita/v3/mem/cache"
	"github.com/sarchlab/akita/v3/sim"
)

// ISCA27 R4: L2 residency of remote data instead of page migration.
//
// (1) Cost-aware replacement: the victim is the block with the smallest
// expected re-fetch cost (cachepkg.CostVictimFinder: re-fetch latency of the
// line from its home x re-use weight of a 2-bit SRRIP state). Remote clean
// fills (M3) lose their hard protection rules (they never evicted a valid
// local line, and a first-touch fill used only an invalid way): a fill is
// installed when the best victim's score is below the score the new line
// would have (first touch at RRPV 3, other fills at the insertion RRPV).
//
// (2) Holder table (HolderTable): each owner GPM keeps, for up to
// CapPerOwner of its pages, the set of GPMs whose L2 holds lines of the page
// (registered when a remote line is installed). A write performed at the
// home (any writer, including the owner's own CUs, the host and the DMA)
// sends an invalidation message to every holder of the page, whose L2 drops
// the page's lines on arrival. A line registered in the table ("tracked")
// survives kernel boundaries; untracked lines (the owner's table had no
// entry, or the entry was replaced) keep the A1 rule (invalidated at the
// boundary when the page was written since the previous boundary).
// A tracked line whose data could predate a write (the write happened after
// the requester opened the read) is dropped at the boundary as well: in
// hardware the requester marks fills that are in flight when an
// invalidation for their page arrives as untracked, and the boundary waits
// for the invalidations in flight.

// L2ResStats counts the R4 work of one L2 slice.
type L2ResStats struct {
	CostAdmitDrops                                  uint64 // remote fills not installed: victim worth more
	FillEvictLocal                                  uint64 // remote fills that evicted a valid local line
	FillEvictRemote                                 uint64 // ... a valid remote line
	FillIntoInvalid                                 uint64
	TrackedInstalls                                 uint64 // remote lines registered in the holder table
	UntrackedInstalls                               uint64
	PageInvals                                      uint64 // invalidation messages applied at this slice
	LinesInvalByWrites                              uint64 // lines dropped by those messages
	Downgrades                                      uint64 // lines made untracked by holder-table replacement
	BoundaryTrackedKept, BoundaryTrackedDropped     uint64
	BoundaryUntrackedKept, BoundaryUntrackedDropped uint64
}

// GetL2ResStats returns the R4 counters.
func (c *Cache) GetL2ResStats() L2ResStats { return c.l2resStats }

// EnableCostReplacement switches the slice to cost-aware replacement; cost
// gives the re-fetch latency (ns) of the line at a physical address.
func (c *Cache) EnableCostReplacement(cost func(addr uint64) float64) {
	d, ok := c.directory.(*cachepkg.DirectoryImpl)
	if !ok {
		panic("cost replacement needs the default directory")
	}
	c.costFinder = cachepkg.NewCostVictimFinder(cost)
	d.SetVictimFinder(c.costFinder)
}

// CostFinder returns the cost-aware victim finder (nil = LRU).
func (c *Cache) CostFinder() *cachepkg.CostVictimFinder { return c.costFinder }

// costRemoteVictim picks the victim of a remote fill under cost-aware
// replacement: never dirty, locked or in-use blocks; nil = drop the fill.
func (c *Cache) costRemoteVictim(line uint64, insertRRPV uint8) *cachepkg.Block {
	d := c.directory.(*cachepkg.DirectoryImpl)
	set := d.SetOf(line)
	v := c.costFinder.FindVictimWhere(set, func(b *cachepkg.Block) bool {
		return !(b.IsValid && b.IsDirty)
	})
	if v == nil {
		return nil
	}
	if v.IsValid && c.costFinder.Score(v) >= c.costFinder.ScoreOf(line, insertRRPV) {
		v.RRFresh = false
		c.l2resStats.CostAdmitDrops++
		return nil
	}
	return v
}

// SetHolderTable registers the slice as part of holder id (its GPM).
func (c *Cache) SetHolderTable(t *HolderTable, id int) {
	c.holders = t
	c.holderID = id
	c.remotePageIndex = make(map[uint64]map[*cachepkg.Block]struct{})
	t.addMember(id, c)
}

func (c *Cache) indexRemote(block *cachepkg.Block, line uint64) {
	if c.remotePageIndex == nil {
		return
	}
	p := line >> c.holders.cfg.Log2PageSize
	m := c.remotePageIndex[p]
	if m == nil {
		m = make(map[*cachepkg.Block]struct{})
		c.remotePageIndex[p] = m
	}
	m[block] = struct{}{}
}

func (c *Cache) unindexRemote(block *cachepkg.Block, line uint64) {
	if c.remotePageIndex == nil {
		return
	}
	p := line >> c.holders.cfg.Log2PageSize
	if m := c.remotePageIndex[p]; m != nil {
		delete(m, block)
		if len(m) == 0 {
			delete(c.remotePageIndex, p)
		}
	}
}

func (c *Cache) pageBlocks(page uint64) []*cachepkg.Block {
	m := c.remotePageIndex[page]
	if len(m) == 0 {
		return nil
	}
	bs := make([]*cachepkg.Block, 0, len(m))
	for b := range m {
		bs = append(bs, b)
	}
	sort.Slice(bs, func(i, j int) bool { return bs[i].CacheAddress < bs[j].CacheAddress })
	return bs
}

// invalidateRemotePage drops this slice's replica lines of page (a holder
// invalidation message arrived).
func (c *Cache) invalidateRemotePage(page uint64) {
	c.l2resStats.PageInvals++
	for _, block := range c.pageBlocks(page) {
		if block.IsLocked {
			continue
		}
		c.obsL2InvalidateBlock(block)
		c.untrackRemoteReplica(block)
		c.untrackResidentBlock(block)
		block.IsValid = false
		block.IsDirty = false
		c.l2resStats.LinesInvalByWrites++
	}
}

// downgradeRemotePage makes the page's lines untracked (the owner replaced
// the page's holder-table entry).
func (c *Cache) downgradeRemotePage(page uint64) {
	for _, block := range c.pageBlocks(page) {
		if r := c.remoteReplicaBlocks[block]; r != nil && r.tracked {
			r.tracked = false
			c.l2resStats.Downgrades++
		}
	}
}

func (c *Cache) replicaStale(record *remoteReplicaRecord, written func(uint64) bool) bool {
	if c.holders != nil && record.tracked {
		if c.holders.WrittenAfter(record.key.line, record.reqTime) {
			c.l2resStats.BoundaryTrackedDropped++
			return true
		}
		c.l2resStats.BoundaryTrackedKept++
		return false
	}
	w := written(record.key.line)
	if c.holders != nil {
		if w {
			c.l2resStats.BoundaryUntrackedDropped++
		} else {
			c.l2resStats.BoundaryUntrackedKept++
		}
	}
	return w
}

// HolderTableConfig configures the holder table.
type HolderTableConfig struct {
	Log2PageSize  uint // 12 = 4 KiB pages
	Log2OwnerSize uint // home GPM = addr >> Log2OwnerSize
	CapPerOwner   int  // entries per owner GPM; <= 0 = unlimited
}

// HolderTableStats counts the table's work.
type HolderTableStats struct {
	Registers, Allocs, Replacements uint64
	InvalMsgs, DowngradeMsgs        uint64
	WriteNotes, WritesWithHolders   uint64
	PeakEntries                     uint64
}

type holderSet [2]uint64

func (s *holderSet) add(i int)      { s[i>>6] |= 1 << uint(i&63) }
func (s *holderSet) has(i int) bool { return s[i>>6]&(1<<uint(i&63)) != 0 }
func (s holderSet) empty() bool     { return s[0] == 0 && s[1] == 0 }
func (s holderSet) each(f func(int)) {
	for w := 0; w < 2; w++ {
		for x := s[w]; x != 0; x &= x - 1 {
			b := 0
			for y := x & -x; y > 1; y >>= 1 {
				b++
			}
			f(w*64 + b)
		}
	}
}

type holderEntry struct {
	page    uint64
	holders holderSet
	el      *list.Element
}

type holderOwner struct {
	lru *list.List // front = most recent
	m   map[uint64]*holderEntry
}

// HolderTable is the owner-side holder table of all GPMs (one simulated
// object; each owner's part has its own capacity). It also keeps the time
// of the last write of each page (the boundary check of tracked lines;
// untracked lines use the A1 tracker).
type HolderTable struct {
	cfg       HolderTableConfig
	owners    map[uint64]*holderOwner
	lastWrite map[uint64]sim.VTimeInSec
	members   map[int][]*Cache
	// Deliver sends an invalidation (or downgrade) message from the owner
	// GPM to a holder GPM; apply runs when it arrives. nil = at once.
	Deliver func(now sim.VTimeInSec, owner uint64, holder int, apply func())
	entries uint64
	stats   HolderTableStats
}

// NewHolderTable creates a holder table.
func NewHolderTable(cfg HolderTableConfig) *HolderTable {
	if cfg.Log2PageSize == 0 {
		cfg.Log2PageSize = 12
	}
	if cfg.Log2OwnerSize == 0 {
		cfg.Log2OwnerSize = 33
	}
	return &HolderTable{
		cfg:       cfg,
		owners:    make(map[uint64]*holderOwner),
		lastWrite: make(map[uint64]sim.VTimeInSec),
		members:   make(map[int][]*Cache),
	}
}

// Stats returns the counters.
func (t *HolderTable) Stats() HolderTableStats { return t.stats }

// Config returns the configuration.
func (t *HolderTable) Config() HolderTableConfig { return t.cfg }

func (t *HolderTable) addMember(id int, c *Cache) {
	t.members[id] = append(t.members[id], c)
}

func (t *HolderTable) owner(o uint64) *holderOwner {
	w := t.owners[o]
	if w == nil {
		w = &holderOwner{lru: list.New(), m: make(map[uint64]*holderEntry)}
		t.owners[o] = w
	}
	return w
}

func (t *HolderTable) deliver(now sim.VTimeInSec, owner uint64, holder int, apply func()) {
	if t.Deliver == nil {
		apply()
		return
	}
	t.Deliver(now, owner, holder, apply)
}

// register records that holder installed a line of the page of line; it
// returns whether the line is tracked.
func (t *HolderTable) register(now sim.VTimeInSec, holder int, line uint64) bool {
	t.stats.Registers++
	page := line >> t.cfg.Log2PageSize
	o := line >> t.cfg.Log2OwnerSize
	w := t.owner(o)
	e := w.m[page]
	if e == nil {
		if t.cfg.CapPerOwner > 0 && len(w.m) >= t.cfg.CapPerOwner {
			old := w.lru.Back().Value.(*holderEntry)
			w.lru.Remove(old.el)
			delete(w.m, old.page)
			t.entries--
			t.stats.Replacements++
			oldPage := old.page
			old.holders.each(func(h int) {
				t.stats.DowngradeMsgs++
				// applied at once: a late downgrade is covered by the
				// boundary check of tracked lines (WrittenAfter)
				for _, c := range t.members[h] {
					c.downgradeRemotePage(oldPage)
				}
			})
		}
		e = &holderEntry{page: page}
		e.el = w.lru.PushFront(e)
		w.m[page] = e
		t.entries++
		if t.entries > t.stats.PeakEntries {
			t.stats.PeakEntries = t.entries
		}
		t.stats.Allocs++
	} else {
		w.lru.MoveToFront(e.el)
	}
	e.holders.add(holder)
	return true
}

// NoteWrite records a write performed at the home (HomeWriteObserver).
func (t *HolderTable) NoteWrite(now sim.VTimeInSec, addr, size uint64) {
	if size == 0 {
		size = 1
	}
	t.stats.WriteNotes++
	first := addr >> t.cfg.Log2PageSize
	last := (addr + size - 1) >> t.cfg.Log2PageSize
	for page := first; page <= last; page++ {
		t.lastWrite[page] = now
		o := (page << t.cfg.Log2PageSize) >> t.cfg.Log2OwnerSize
		w := t.owners[o]
		if w == nil {
			continue
		}
		e := w.m[page]
		if e == nil || e.holders.empty() {
			continue
		}
		t.stats.WritesWithHolders++
		p := page
		e.holders.each(func(h int) {
			t.stats.InvalMsgs++
			t.deliver(now, o, h, func() {
				for _, c := range t.members[h] {
					c.invalidateRemotePage(p)
				}
			})
		})
		e.holders = holderSet{}
	}
}

// WrittenAfter reports whether the page of line was written after time ts.
func (t *HolderTable) WrittenAfter(line uint64, ts sim.VTimeInSec) bool {
	w, ok := t.lastWrite[line>>t.cfg.Log2PageSize]
	return ok && w >= ts
}
