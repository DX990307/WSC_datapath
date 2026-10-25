package writeback

import (
	"math/rand"
	"sort"

	cachepkg "github.com/sarchlab/akita/v3/mem/cache"
	"github.com/sarchlab/akita/v3/mem/vm"
	"github.com/sarchlab/akita/v3/sim"
)

// ISCA'27 comparison designs (isca27/results_index/compare/README.md):
// published remote-data caches with hardware coherence, mapped to the wafer.
//
// HMG (Ren et al., HPCA 2020): every GPM's L2 caches remote data; each GPM
// has one set-associative coherence directory (entries of GroupLines lines,
// sharer bit vector, VI, no transient states). The wafer has no GPU level,
// so the mesh is cut into rectangular regions (the "GPUs"); within its region
// a requester sends an L2 miss to the region home of the address (hash of
// the line group over the region's GPMs), which caches the line and forwards
// its own misses to the system home (the GPM whose DRAM holds the address).
// Requesters in the system home's region go to the system home directly.
// The system home tracks GPMs of its own region and region homes of other
// regions; a region home tracks the GPMs of its region. A write at the home
// invalidates all sharers (no acknowledgments, non-multi-copy-atomic);
// invalidations reaching a region home are forwarded to its sharers.
// Directory entry eviction invalidates the entry's sharers.
//
// CARVE (Young et al., MICRO 2018), CARVE-HWC: every GPM's L2 caches remote
// data and a Remote Data Cache (RDC) in local DRAM (direct-mapped, 128-B
// lines, tags in ECC: an RDC probe is one DRAM access) holds remote lines
// below the L2. Coherence is GPU-VI: a write to a line the In-Memory Sharing
// Tracker (2 bits per 128-B line at the home: uncached / private /
// read-shared / read-write-shared) marks as shared broadcasts an
// invalidation to every other GPM (L2 and RDC). A local write to a
// read-write-shared line returns it to private with probability
// PrivateProb, after the broadcast.
//
// Both keep replica lines across kernel boundaries (hardware coherence; no
// A1 boundary rule for them). As a safety net that hardware gets from
// "release waits for invalidations in flight", a kept line is dropped at a
// boundary when its group was written after the time its data was read at
// the home (its age).

// CmpMode selects the design.
type CmpMode int

// Modes.
const (
	CmpNone CmpMode = iota
	CmpHMG
	CmpCARVE
)

// IMST states (CARVE).
const (
	imstUncached uint8 = iota
	imstPrivate
	imstReadShared
	imstRWShared
)

// CmpConfig configures the comparison design.
type CmpConfig struct {
	Mode          CmpMode
	Log2Group     uint // directory entry / IMST / invalidation granularity
	Log2OwnerSize uint // home GPM = addr >> Log2OwnerSize
	// HMG
	DirEntries int // per GPM
	DirWays    int
	RegionOf   map[int]int   // GPM -> region
	Regions    map[int][]int // region -> GPMs (sorted)
	// CARVE
	GPMs        []int  // all GPMs (broadcast targets)
	RDCSets     uint64 // per GPM
	PrivateProb float64
	Seed        int64
}

// CmpStats counts the work of the comparison design.
type CmpStats struct {
	InvalMsgs, InvalForwarded, InvalApplied, LinesInvalidated  uint64
	DirAllocs, DirEvictions, DirEvictInvals, DirPeak           uint64
	WriteNotes, WritesWithSharers                              uint64
	Broadcasts, BroadcastMsgs                                  uint64
	IMSTToRS, IMSTToRWS, IMSTToPrivate                         uint64
	RDCInserts, RDCReplacements, RDCInvalidated, RDCStaleDrops uint64
	BoundaryKept, BoundaryDropped                              uint64
}

type cmpDirEntry struct {
	group   uint64
	sharers holderSet
	lru     uint64
}

type cmpDir struct {
	sets  map[uint64][]*cmpDirEntry
	index map[uint64]*cmpDirEntry
	n     int
}

type rdcLine struct {
	tag  uint64 // group address
	pid  vm.PID
	data []byte
	age  sim.VTimeInSec
}

// CmpCoherence is the platform-wide state of the comparison design (one
// simulated object; each GPM's directory / RDC has its own capacity).
type CmpCoherence struct {
	cfg       CmpConfig
	members   map[int][]*Cache
	dirs      map[int]*cmpDir
	lastWrite map[uint64]sim.VTimeInSec
	imst      map[uint64]uint8
	rdc       map[int]map[uint64]*rdcLine
	boundary  map[int]sim.VTimeInSec
	clock     uint64
	rng       *rand.Rand
	// Send carries an invalidation of group from GPM from to GPM to over the
	// network; OnInval runs at the destination when it arrives.
	Send func(now sim.VTimeInSec, from, to int, group uint64)
	// Age passes the age of the data in a region home's response to the
	// requester (a field of the response message in hardware).
	Age   map[string]sim.VTimeInSec
	stats CmpStats
}

// NewCmpCoherence creates the comparison design state.
func NewCmpCoherence(cfg CmpConfig) *CmpCoherence {
	if cfg.Log2OwnerSize == 0 {
		cfg.Log2OwnerSize = 33
	}
	if cfg.DirWays <= 0 {
		cfg.DirWays = 8
	}
	return &CmpCoherence{
		cfg:       cfg,
		members:   make(map[int][]*Cache),
		dirs:      make(map[int]*cmpDir),
		lastWrite: make(map[uint64]sim.VTimeInSec),
		imst:      make(map[uint64]uint8),
		rdc:       make(map[int]map[uint64]*rdcLine),
		boundary:  make(map[int]sim.VTimeInSec),
		rng:       rand.New(rand.NewSource(cfg.Seed + 1)),
		Age:       make(map[string]sim.VTimeInSec),
	}
}

// Config returns the configuration.
func (t *CmpCoherence) Config() CmpConfig { return t.cfg }

// Stats returns the counters.
func (t *CmpCoherence) Stats() CmpStats { return t.stats }

// Mode returns the design.
func (t *CmpCoherence) Mode() CmpMode { return t.cfg.Mode }

// Group returns the group address (directory / IMST granularity) of addr.
func (t *CmpCoherence) Group(addr uint64) uint64 {
	return addr >> t.cfg.Log2Group << t.cfg.Log2Group
}

func (t *CmpCoherence) home(addr uint64) int { return int(addr >> t.cfg.Log2OwnerSize) }

// RegionHome returns the HMG region home of addr for requester GPM g.
func (t *CmpCoherence) RegionHome(g int, addr uint64) int {
	m := t.cfg.Regions[t.cfg.RegionOf[g]]
	x := addr >> t.cfg.Log2Group
	x ^= x >> 17
	x *= 0xed5ad4bb
	x ^= x >> 11
	return m[x%uint64(len(m))]
}

// NextHop returns where GPM g sends an L2 miss to addr under HMG: the region
// home, or the system home when it is in g's region or g is the region home.
func (t *CmpCoherence) NextHop(g int, addr uint64) int {
	o := t.home(addr)
	if t.cfg.RegionOf[g] == t.cfg.RegionOf[o] {
		return o
	}
	r := t.RegionHome(g, addr)
	if r == g {
		return o
	}
	return r
}

// SetCmp registers an L2 slice of GPM id. rdmaToL2 is the GPM's RDMA port
// that issues writes of remote requesters at this home.
func (c *Cache) SetCmp(t *CmpCoherence, id int, rdmaToL2 sim.Port, fillLRU bool) {
	c.cmp = t
	c.cmpID = id
	c.cmpRDMAPort = rdmaToL2
	c.cmpFillLRU = fillLRU
	if c.remotePageIndex == nil {
		c.remotePageIndex = make(map[uint64]map[*cachepkg.Block]struct{})
	}
	t.members[id] = append(t.members[id], c)
}

func (c *Cache) remoteIndexLog2() uint {
	if c.cmp != nil {
		return c.cmp.cfg.Log2Group
	}
	return c.holders.cfg.Log2PageSize
}

// ReplicaAge returns the age of a replica line held by this slice.
func (c *Cache) ReplicaAge(pid vm.PID, line uint64) (sim.VTimeInSec, bool) {
	b := c.directory.Lookup(pid, line)
	if b == nil {
		return 0, false
	}
	r := c.remoteReplicaBlocks[b]
	if r == nil {
		return 0, false
	}
	return r.reqTime, true
}

// cmpNoteWriteTime records the time a home write changes data (every call
// of noteHomeWrite: acceptance and completion).
func (c *Cache) cmpNoteWriteTime(now sim.VTimeInSec, addr, size uint64) {
	if c.cmp != nil {
		c.cmp.noteWriteTime(now, addr, size)
	}
}

// cmpNoteWriteAccepted runs the coherence action of a write accepted at
// this home slice (once per write).
func (c *Cache) cmpNoteWriteAccepted(now sim.VTimeInSec, addr, size uint64, src sim.Port) {
	if c.cmp == nil {
		return
	}
	c.cmp.noteWrite(now, addr, size, src != nil && src == c.cmpRDMAPort)
}

// dropCmpGroup drops this slice's replica lines of group.
func (c *Cache) dropCmpGroup(group uint64) uint64 {
	n := uint64(0)
	for _, block := range c.pageBlocks(group >> c.cmp.cfg.Log2Group) {
		if block.IsLocked {
			continue
		}
		c.obsL2InvalidateBlock(block)
		c.untrackRemoteReplica(block)
		c.untrackResidentBlock(block)
		block.IsValid = false
		block.IsDirty = false
		n++
	}
	return n
}

func (t *CmpCoherence) noteWriteTime(now sim.VTimeInSec, addr, size uint64) {
	if size == 0 {
		size = 1
	}
	for g := t.Group(addr); g < addr+size; g += 1 << t.cfg.Log2Group {
		t.lastWrite[g] = now
	}
}

// NoteWrite records a write the driver performs at a home (host copy,
// page release): a local write.
func (t *CmpCoherence) NoteWrite(now sim.VTimeInSec, addr, size uint64) {
	t.noteWriteTime(now, addr, size)
	t.noteWrite(now, addr, size, false)
}

func (t *CmpCoherence) noteWrite(now sim.VTimeInSec, addr, size uint64, remote bool) {
	if size == 0 {
		size = 1
	}
	t.stats.WriteNotes++
	for g := t.Group(addr); g < addr+size; g += 1 << t.cfg.Log2Group {
		o := t.home(g)
		switch t.cfg.Mode {
		case CmpHMG:
			d := t.dirs[o]
			if d == nil {
				continue
			}
			e := d.index[g]
			if e == nil {
				continue
			}
			t.stats.WritesWithSharers++
			t.invalSharers(now, o, e)
			d.remove(e)
		case CmpCARVE:
			st := t.imst[g]
			switch st {
			case imstUncached, imstPrivate:
				if remote {
					t.imst[g] = imstRWShared
					t.stats.IMSTToRWS++
				} else if st == imstUncached {
					t.imst[g] = imstPrivate
				}
			case imstReadShared:
				t.broadcast(now, o, g)
				t.imst[g] = imstRWShared
				t.stats.IMSTToRWS++
			case imstRWShared:
				t.broadcast(now, o, g)
				if !remote && t.rng.Float64() < t.cfg.PrivateProb {
					t.imst[g] = imstPrivate
					t.stats.IMSTToPrivate++
				}
			}
		}
	}
}

// OwnerRemoteRead is the CARVE IMST transition of a remote read arriving
// at the home.
func (t *CmpCoherence) OwnerRemoteRead(addr uint64) {
	if t.cfg.Mode != CmpCARVE {
		return
	}
	g := t.Group(addr)
	if st := t.imst[g]; st == imstUncached || st == imstPrivate {
		t.imst[g] = imstReadShared
		t.stats.IMSTToRS++
	}
}

func (t *CmpCoherence) broadcast(now sim.VTimeInSec, o int, g uint64) {
	t.stats.Broadcasts++
	for _, d := range t.cfg.GPMs {
		if d == o {
			continue
		}
		t.stats.BroadcastMsgs++
		t.send(now, o, d, g)
	}
}

func (t *CmpCoherence) send(now sim.VTimeInSec, from, to int, g uint64) {
	t.stats.InvalMsgs++
	if t.Send == nil {
		t.OnInval(now, to, g)
		return
	}
	t.Send(now, from, to, g)
}

func (t *CmpCoherence) invalSharers(now sim.VTimeInSec, node int, e *cmpDirEntry) {
	e.sharers.each(func(s int) { t.send(now, node, s, e.group) })
	e.sharers = holderSet{}
}

// OnInval applies an invalidation of group arriving at GPM node: its L2
// replica lines (and RDC line) of the group are dropped; under HMG a region
// home forwards it to the sharers its directory records.
func (t *CmpCoherence) OnInval(now sim.VTimeInSec, node int, g uint64) {
	t.stats.InvalApplied++
	for _, c := range t.members[node] {
		t.stats.LinesInvalidated += c.dropCmpGroup(g)
	}
	switch t.cfg.Mode {
	case CmpHMG:
		if d := t.dirs[node]; d != nil {
			if e := d.index[g]; e != nil {
				e.sharers.each(func(s int) {
					t.stats.InvalForwarded++
					t.send(now, node, s, g)
				})
				d.remove(e)
			}
		}
	case CmpCARVE:
		if r := t.rdc[node]; r != nil {
			set := t.rdcSet(g)
			if l := r[set]; l != nil && l.tag == g {
				delete(r, set)
				t.stats.RDCInvalidated++
			}
		}
	}
}

// register records that GPM h installed a replica of line; under HMG it is
// added to its directory (region home or system home). Always tracked.
func (t *CmpCoherence) register(now sim.VTimeInSec, h int, line uint64) bool {
	if t.cfg.Mode != CmpHMG {
		return true
	}
	g := t.Group(line)
	o := t.home(line)
	r := t.NextHop(h, line)
	if r == o {
		t.dirAdd(now, o, g, h)
		return true
	}
	t.dirAdd(now, r, g, h)
	t.dirAdd(now, o, g, r)
	return true
}

func (t *CmpCoherence) dirAdd(now sim.VTimeInSec, node int, g uint64, s int) {
	d := t.dirs[node]
	if d == nil {
		d = &cmpDir{sets: make(map[uint64][]*cmpDirEntry), index: make(map[uint64]*cmpDirEntry)}
		t.dirs[node] = d
	}
	t.clock++
	if e := d.index[g]; e != nil {
		e.sharers.add(s)
		e.lru = t.clock
		return
	}
	nsets := uint64(t.cfg.DirEntries / t.cfg.DirWays)
	if nsets == 0 {
		nsets = 1
	}
	si := (g >> t.cfg.Log2Group) % nsets
	set := d.sets[si]
	if len(set) >= t.cfg.DirWays {
		v := 0
		for i, e := range set {
			if e.lru < set[v].lru {
				v = i
			}
		}
		old := set[v]
		t.stats.DirEvictions++
		old.sharers.each(func(x int) {
			t.stats.DirEvictInvals++
			t.send(now, node, x, old.group)
		})
		d.remove(old)
		set = d.sets[si]
	}
	e := &cmpDirEntry{group: g, lru: t.clock}
	e.sharers.add(s)
	d.sets[si] = append(set, e)
	d.index[g] = e
	d.n++
	t.stats.DirAllocs++
	if uint64(d.n) > t.stats.DirPeak {
		t.stats.DirPeak = uint64(d.n)
	}
}

func (d *cmpDir) remove(e *cmpDirEntry) {
	if d.index[e.group] != e {
		return
	}
	delete(d.index, e.group)
	for si, set := range d.sets {
		for i, x := range set {
			if x == e {
				d.sets[si] = append(set[:i:i], set[i+1:]...)
				if len(d.sets[si]) == 0 {
					delete(d.sets, si)
				}
				d.n--
				return
			}
		}
	}
}

// WrittenAfter reports whether the group of line was written at or after ts.
func (t *CmpCoherence) WrittenAfter(line uint64, ts sim.VTimeInSec) bool {
	w, ok := t.lastWrite[t.Group(line)]
	return ok && w >= ts
}

// NoteBoundary records a kernel boundary of GPM g (lazy RDC check).
func (t *CmpCoherence) NoteBoundary(g int, now sim.VTimeInSec) { t.boundary[g] = now }

func (t *CmpCoherence) rdcSet(g uint64) uint64 {
	x := g >> t.cfg.Log2Group
	x ^= x >> 21
	x *= 0x9e3779b97f4a7c15
	x ^= x >> 29
	return x % t.cfg.RDCSets
}

// RDCSetOf returns the RDC set of addr (its DRAM location).
func (t *CmpCoherence) RDCSetOf(addr uint64) uint64 { return t.rdcSet(t.Group(addr)) }

// RDCLookup looks up addr in GPM g's RDC; it returns the 64-B line of addr.
// A line kept across a kernel boundary whose group was written after its
// age is stale (the release would have waited for its invalidation): it is
// dropped and the lookup misses.
func (t *CmpCoherence) RDCLookup(g int, addr, lineBytes uint64) ([]byte, sim.VTimeInSec, bool) {
	r := t.rdc[g]
	grp := t.Group(addr)
	set := t.rdcSet(grp)
	l := r[set]
	if l == nil || l.tag != grp {
		return nil, 0, false
	}
	if l.age < t.boundary[g] && t.WrittenAfter(grp, l.age) {
		delete(r, set)
		t.stats.RDCStaleDrops++
		return nil, 0, false
	}
	off := addr - grp
	return append([]byte(nil), l.data[off:off+lineBytes]...), l.age, true
}

// RDCInsert installs a 128-B line (direct-mapped: replaces the set's line).
func (t *CmpCoherence) RDCInsert(g int, grp uint64, pid vm.PID, data []byte, age sim.VTimeInSec) {
	r := t.rdc[g]
	if r == nil {
		r = make(map[uint64]*rdcLine)
		t.rdc[g] = r
	}
	set := t.rdcSet(grp)
	if old := r[set]; old != nil && old.tag != grp {
		t.stats.RDCReplacements++
	}
	r[set] = &rdcLine{tag: grp, pid: pid, data: append([]byte(nil), data...), age: age}
	t.stats.RDCInserts++
}

// RDCLines returns the number of valid RDC lines of all GPMs.
func (t *CmpCoherence) RDCLines() uint64 {
	n := uint64(0)
	for _, r := range t.rdc {
		n += uint64(len(r))
	}
	return n
}

// DirEntries returns the directory entries in use, all GPMs.
func (t *CmpCoherence) DirEntries() uint64 {
	n := uint64(0)
	ids := make([]int, 0, len(t.dirs))
	for id := range t.dirs {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	for _, id := range ids {
		n += uint64(t.dirs[id].n)
	}
	return n
}

func (t *CmpCoherence) noteBoundaryCheck(kept bool) {
	if kept {
		t.stats.BoundaryKept++
	} else {
		t.stats.BoundaryDropped++
	}
}

// Home returns the home GPM of addr.
func (t *CmpCoherence) Home(addr uint64) int { return t.home(addr) }

// Boundary returns the time of GPM g's last kernel boundary.
func (t *CmpCoherence) Boundary(g int) sim.VTimeInSec { return t.boundary[g] }
