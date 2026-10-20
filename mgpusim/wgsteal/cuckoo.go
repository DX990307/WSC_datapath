package wgsteal

// Cuckoo filters for T19 (filter-guided WG stealing).
//
// All filters hash a key (a 4 KB virtual page number) with the same 64-bit
// hash H. A filter with 2^b buckets of `slots` entries stores, per item, an
// f-bit fingerprint (bits 32.. of H, never 0) in bucket i1 = H mod 2^b or in
// its alternate bucket i2 = i1 XOR altMask(fp) (partial-key cuckoo hashing,
// Fan et al., CoNEXT'14), so an entry can move between its two buckets
// knowing only its fingerprint, and can be deleted.
//
// Cross-filter probing. A small per-group footprint filter (2^g buckets) is
// compared against a GPM's larger recent-page filter (2^r buckets, r >= g)
// without the keys: each small-filter entry also stores the r-g hash bits
// above its bucket index ("extra") and whether it sits in its primary
// bucket. From (bucket, fp, primary, extra) the probe rebuilds the item's
// primary bucket in the large filter and looks the fingerprint up there and
// in its alternate. Both filters must use the same fingerprint width.
//
// Each entry also keeps the key it was inserted for. The key is simulator
// bookkeeping only (hardware does not store it); it lets the statistics
// separate true hits from fingerprint false positives.

const (
	defaultMaxKicks = 500
)

// HashPage is the hash shared by every T19 filter.
func HashPage(page uint64) uint64 {
	return splitmix64(page ^ 0x5851f42d4c957f2d)
}

func splitmix64(x uint64) uint64 {
	x += 0x9e3779b97f4a7c15
	x = (x ^ (x >> 30)) * 0xbf58476d1ce4e5b9
	x = (x ^ (x >> 27)) * 0x94d049bb133111eb
	return x ^ (x >> 31)
}

type cfEntry struct {
	used    bool
	primary bool
	fp      uint16
	extra   uint16
	key     uint64 // simulator-only shadow
}

// CuckooFilter is a partial-key cuckoo filter with deletion.
type CuckooFilter struct {
	log2Buckets uint
	slots       int
	fpBits      uint
	refLog2     uint // log2 bucket count of the probed (reference) filter
	extraBits   uint
	entries     []cfEntry
	count       int
	maxKicks    int
	rng         uint64

	// InsertFails counts inserts that ended after maxKicks relocations:
	// the new item is stored but the last relocated (older) item is lost.
	InsertFails uint64
}

// NewCuckooFilter creates a filter with 2^log2Buckets buckets of `slots`
// entries and fpBits-bit fingerprints. refLog2 (>= log2Buckets, or 0 for
// none) is the bucket count of the filter this one will be probed against.
func NewCuckooFilter(log2Buckets uint, slots int, fpBits uint, refLog2 uint) *CuckooFilter {
	if log2Buckets < 1 || slots < 1 || fpBits < 2 || fpBits > 16 {
		panic("wgsteal: bad cuckoo filter geometry")
	}
	f := &CuckooFilter{
		log2Buckets: log2Buckets,
		slots:       slots,
		fpBits:      fpBits,
		refLog2:     refLog2,
		entries:     make([]cfEntry, (1<<log2Buckets)*slots),
		maxKicks:    defaultMaxKicks,
		rng:         0x2545f4914f6cdd1d ^ uint64(log2Buckets)<<8 ^ uint64(fpBits),
	}
	if refLog2 > log2Buckets {
		f.extraBits = refLog2 - log2Buckets
	}
	return f
}

// Capacity is the number of entries (buckets x slots).
func (f *CuckooFilter) Capacity() int { return len(f.entries) }

// Count is the number of stored items.
func (f *CuckooFilter) Count() int { return f.count }

// EntryBits is the storage per entry in hardware: fingerprint, plus the
// extra hash bits and the primary flag of a probe-able filter.
func (f *CuckooFilter) EntryBits() int {
	b := int(f.fpBits)
	if f.refLog2 > 0 {
		b += int(f.extraBits) + 1
	}
	return b
}

func (f *CuckooFilter) mask() uint64 { return (1 << f.log2Buckets) - 1 }

func (f *CuckooFilter) fingerprint(h uint64) uint16 {
	fp := uint16((h >> 32) & ((1 << f.fpBits) - 1))
	if fp == 0 {
		fp = 1
	}
	return fp
}

func altMask(fp uint16, mask uint64) uint64 {
	return (splitmix64(uint64(fp)*0xff51afd7ed558ccd) | 1) & mask
}

func (f *CuckooFilter) primaryIndex(h uint64) uint64 { return h & f.mask() }

func (f *CuckooFilter) extraOf(h uint64) uint16 {
	if f.extraBits == 0 {
		return 0
	}
	return uint16((h >> f.log2Buckets) & ((1 << f.extraBits) - 1))
}

func (f *CuckooFilter) nextRand() uint64 {
	f.rng ^= f.rng << 13
	f.rng ^= f.rng >> 7
	f.rng ^= f.rng << 17
	return f.rng
}

func (f *CuckooFilter) bucket(i uint64) []cfEntry {
	s := int(i) * f.slots
	return f.entries[s : s+f.slots]
}

func (f *CuckooFilter) placeIn(i uint64, e cfEntry) bool {
	b := f.bucket(i)
	for j := range b {
		if !b[j].used {
			b[j] = e
			return true
		}
	}
	return false
}

// Insert adds an item (h = HashPage(key)). It returns false if the filter
// is full and the item could not be stored.
func (f *CuckooFilter) Insert(h, key uint64) bool {
	fp := f.fingerprint(h)
	i1 := f.primaryIndex(h)
	i2 := i1 ^ altMask(fp, f.mask())
	e := cfEntry{used: true, primary: true, fp: fp, extra: f.extraOf(h), key: key}
	if f.placeIn(i1, e) {
		f.count++
		return true
	}
	e.primary = false
	if f.placeIn(i2, e) {
		f.count++
		return true
	}

	i := i1
	e.primary = true
	if f.nextRand()&1 == 1 {
		i = i2
		e.primary = false
	}
	for n := 0; n < f.maxKicks; n++ {
		b := f.bucket(i)
		j := int(f.nextRand() % uint64(f.slots))
		e, b[j] = b[j], e
		i ^= altMask(e.fp, f.mask())
		e.primary = !e.primary
		if f.placeIn(i, e) {
			f.count++
			return true
		}
	}
	// The last relocated entry is lost; the new item is stored, so the
	// number of items does not change.
	f.InsertFails++
	return true
}

func (f *CuckooFilter) matchIn(i uint64, fp uint16, key uint64) (hit, exact bool) {
	for _, e := range f.bucket(i) {
		if e.used && e.fp == fp {
			hit = true
			if e.key == key {
				return true, true
			}
		}
	}
	return hit, false
}

// LookupExact reports whether the filter answers "present" for the item
// (hit) and whether that answer is true (exact, simulator-only).
func (f *CuckooFilter) LookupExact(h, key uint64) (hit, exact bool) {
	fp := f.fingerprint(h)
	i1 := f.primaryIndex(h)
	return f.lookupAt(i1, fp, key)
}

func (f *CuckooFilter) lookupAt(i1 uint64, fp uint16, key uint64) (hit, exact bool) {
	h1, e1 := f.matchIn(i1, fp, key)
	if e1 {
		return true, true
	}
	h2, e2 := f.matchIn(i1^altMask(fp, f.mask()), fp, key)
	if e2 {
		return true, true
	}
	return h1 || h2, false
}

// Lookup reports whether the filter answers "present".
func (f *CuckooFilter) Lookup(h uint64) bool {
	hit, _ := f.LookupExact(h, ^uint64(0))
	return hit
}

func (f *CuckooFilter) deleteIn(i uint64, fp uint16, key uint64, needKey bool) bool {
	b := f.bucket(i)
	for j := range b {
		if b[j].used && b[j].fp == fp && (!needKey || b[j].key == key) {
			b[j] = cfEntry{}
			f.count--
			return true
		}
	}
	return false
}

// Delete removes one entry with the item's fingerprint from its two
// buckets. Hardware cannot tell equal fingerprints apart; the simulator
// prefers the entry inserted for this key so that its shadow keys stay
// right. Returns false if no entry matched.
func (f *CuckooFilter) Delete(h, key uint64) bool {
	fp := f.fingerprint(h)
	i1 := f.primaryIndex(h)
	i2 := i1 ^ altMask(fp, f.mask())
	if f.deleteIn(i1, fp, key, true) || f.deleteIn(i2, fp, key, true) {
		return true
	}
	return f.deleteIn(i1, fp, key, false) || f.deleteIn(i2, fp, key, false)
}

// Clear empties the filter.
func (f *CuckooFilter) Clear() {
	for i := range f.entries {
		f.entries[i] = cfEntry{}
	}
	f.count = 0
}

// ProbeInto looks every entry of f up in ref (a filter with
// 2^f.refLog2 buckets and the same fingerprint width) without the keys.
// It returns the number of f's items, how many ref answers "present" for,
// and how many of those are true (simulator-only).
func (f *CuckooFilter) ProbeInto(ref *CuckooFilter) (items, hits, exact int) {
	if ref.log2Buckets != f.refLog2 || ref.fpBits != f.fpBits {
		panic("wgsteal: probing filters with different geometry")
	}
	probe := func(bucket uint64, e cfEntry) {
		i1 := bucket
		if !e.primary {
			i1 = bucket ^ altMask(e.fp, f.mask())
		}
		r1 := uint64(e.extra)<<f.log2Buckets | i1
		h, x := ref.lookupAt(r1, e.fp, e.key)
		items++
		if h {
			hits++
		}
		if x {
			exact++
		}
	}
	for idx, e := range f.entries {
		if e.used {
			probe(uint64(idx/f.slots), e)
		}
	}
	return items, hits, exact
}

// RecentPages is a GPM's filter of recently touched pages: a cuckoo filter
// plus a FIFO of the inserted items. When more than `window` distinct
// pages are held, the oldest is deleted (aging by deletion). In hardware
// the FIFO holds (primary bucket, fingerprint) pairs, enough to delete.
type RecentPages struct {
	F      *CuckooFilter
	window int
	fifoH  []uint64
	fifoK  []uint64
	head   int
	size   int

	Lookups   uint64
	Hits      uint64
	FalsePos  uint64
	Inserts   uint64
	Evictions uint64
}

// NewRecentPages creates the filter.
func NewRecentPages(log2Buckets uint, slots int, fpBits uint, window int) *RecentPages {
	f := NewCuckooFilter(log2Buckets, slots, fpBits, 0)
	if window <= 0 || window > f.Capacity() {
		panic("wgsteal: recent-page window must be in (0, capacity]")
	}
	return &RecentPages{
		F:      f,
		window: window,
		fifoH:  make([]uint64, window),
		fifoK:  make([]uint64, window),
	}
}

// Touch records an access to a page.
func (r *RecentPages) Touch(page uint64) {
	h := HashPage(page)
	r.Lookups++
	hit, exact := r.F.LookupExact(h, page)
	if hit {
		r.Hits++
		if !exact {
			r.FalsePos++
		}
		return
	}
	if r.size == r.window {
		r.F.Delete(r.fifoH[r.head], r.fifoK[r.head])
		r.head = (r.head + 1) % r.window
		r.size--
		r.Evictions++
	}
	r.F.Insert(h, page)
	tail := (r.head + r.size) % r.window
	r.fifoH[tail] = h
	r.fifoK[tail] = page
	r.size++
	r.Inserts++
}

// Size is the number of pages held.
func (r *RecentPages) Size() int { return r.size }
