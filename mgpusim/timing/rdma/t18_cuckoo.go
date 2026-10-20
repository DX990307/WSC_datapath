package rdma

// T18 (ISCA'27 new m2): bounded cuckoo hash table used by both ends of the
// two-sided merge. The requester uses it as its in-flight (transaction) table,
// the owner as its merge table.
//
// Geometry: Buckets x Ways slots. A key may live in one of two buckets,
// h1(key) and h2(key). Each slot holds a short fingerprint (the tag that the
// hardware compares in parallel across the 2 x Ways candidate slots) and the
// entry, which stores the full key. A lookup only reports a hit when the full
// key matches: a fingerprint match on a different key is counted as a
// fingerprint false positive and never merges two different lines.
//
// Several slots may hold the same key (e.g. a closed entry whose request is
// still in flight and a newer open entry); callers choose which one they need
// through the match predicate. Inserts use bounded cuckoo displacement; a
// failed insert restores every moved entry and reports the table as full.

import "fmt"

// t18Key identifies one 64-B line of one address space.
type t18Key struct {
	pid  uint64
	line uint64
}

type t18Slot[V any] struct {
	valid bool
	fp    uint32
	key   t18Key
	val   *V
}

// t18CuckooStats counts table activity; callers add them to the T18 stats.
type t18CuckooStats struct {
	Lookups             uint64
	FingerprintMatches  uint64
	FingerprintFalsePos uint64
	Inserts             uint64
	InsertFailures      uint64
	Kicks               uint64
	Deletes             uint64
	Peak                uint64
}

type t18CuckooTable[V any] struct {
	buckets  int
	ways     int
	fpBits   uint
	maxKicks int
	slots    []t18Slot[V]
	count    int
	rng      uint64
	where    map[*V]int // simulator bookkeeping: slot index of each entry
	stats    t18CuckooStats
}

func newT18CuckooTable[V any](
	buckets, ways int,
	fpBits uint,
	maxKicks int,
) *t18CuckooTable[V] {
	if buckets < 1 || ways < 1 {
		panic(fmt.Sprintf("t18 cuckoo table needs >=1 bucket and way, got %d x %d",
			buckets, ways))
	}
	if fpBits < 1 || fpBits > 32 {
		panic(fmt.Sprintf("t18 cuckoo fingerprint bits must be in [1,32], got %d",
			fpBits))
	}
	if maxKicks < 0 {
		maxKicks = 0
	}
	return &t18CuckooTable[V]{
		buckets:  buckets,
		ways:     ways,
		fpBits:   fpBits,
		maxKicks: maxKicks,
		slots:    make([]t18Slot[V], buckets*ways),
		rng:      0x9e3779b97f4a7c15,
		where:    make(map[*V]int),
	}
}

func t18Mix(x uint64) uint64 {
	// splitmix64 finalizer
	x += 0x9e3779b97f4a7c15
	x = (x ^ (x >> 30)) * 0xbf58476d1ce4e5b9
	x = (x ^ (x >> 27)) * 0x94d049bb133111eb
	return x ^ (x >> 31)
}

func (k t18Key) hash() uint64 {
	return t18Mix(k.line ^ t18Mix(k.pid+0x51ed27))
}

func (t *t18CuckooTable[V]) fingerprint(k t18Key) uint32 {
	h := t18Mix(k.hash() ^ 0x2545f4914f6cdd1d)
	mask := uint64(1)<<t.fpBits - 1
	return uint32(h & mask)
}

func (t *t18CuckooTable[V]) bucketPair(k t18Key) (int, int) {
	h := k.hash()
	b1 := int(h % uint64(t.buckets))
	b2 := int((h >> 32) % uint64(t.buckets))
	if b2 == b1 && t.buckets > 1 {
		b2 = (b1 + 1) % t.buckets
	}
	return b1, b2
}

func (t *t18CuckooTable[V]) altBucket(k t18Key, current int) int {
	b1, b2 := t.bucketPair(k)
	if current == b1 {
		return b2
	}
	return b1
}

// Capacity returns the number of slots.
func (t *t18CuckooTable[V]) Capacity() int { return len(t.slots) }

// Len returns the number of valid slots.
func (t *t18CuckooTable[V]) Len() int { return t.count }

// Stats returns a copy of the activity counters.
func (t *t18CuckooTable[V]) Stats() t18CuckooStats { return t.stats }

// Lookup returns the first entry with exactly this key that satisfies match
// (nil match accepts any). Fingerprint matches on other keys are counted.
func (t *t18CuckooTable[V]) Lookup(k t18Key, match func(*V) bool) *V {
	t.stats.Lookups++
	fp := t.fingerprint(k)
	b1, b2 := t.bucketPair(k)
	var found *V
	for _, b := range [2]int{b1, b2} {
		for w := 0; w < t.ways; w++ {
			s := &t.slots[b*t.ways+w]
			if !s.valid || s.fp != fp {
				continue
			}
			t.stats.FingerprintMatches++
			if s.key != k {
				t.stats.FingerprintFalsePos++
				continue
			}
			if found == nil && (match == nil || match(s.val)) {
				found = s.val
			}
		}
		if b2 == b1 {
			break
		}
	}
	return found
}

// Insert stores v under k. It returns false, leaving the table unchanged,
// when no slot can be found within maxKicks displacements.
func (t *t18CuckooTable[V]) Insert(k t18Key, v *V) bool {
	if v == nil {
		panic("t18 cuckoo: nil entry")
	}
	if _, dup := t.where[v]; dup {
		panic("t18 cuckoo: entry inserted twice")
	}
	t.stats.Inserts++
	fp := t.fingerprint(k)
	b1, b2 := t.bucketPair(k)
	for _, b := range [2]int{b1, b2} {
		if idx := t.freeWay(b); idx >= 0 {
			t.place(idx, k, fp, v)
			return true
		}
	}

	// Bounded random-walk displacement with an undo log.
	type move struct{ from, to int }
	var moves []move
	cur := t18Slot[V]{valid: true, fp: fp, key: k, val: v}
	bucket := b1
	if t.nextRand()&1 == 1 {
		bucket = b2
	}
	for kick := 0; kick < t.maxKicks; kick++ {
		victimIdx := bucket*t.ways + int(t.nextRand()%uint64(t.ways))
		victim := t.slots[victimIdx]
		t.slots[victimIdx] = cur
		t.where[cur.val] = victimIdx
		moves = append(moves, move{from: -1, to: victimIdx})
		t.stats.Kicks++
		cur = victim
		bucket = t.altBucket(cur.key, bucket)
		if idx := t.freeWay(bucket); idx >= 0 {
			t.slots[idx] = cur
			t.where[cur.val] = idx
			t.count++
			t.notePeak()
			return true
		}
	}
	// Undo: walk back, restoring each displaced entry to the slot it was
	// evicted from; the new entry ends up out of the table.
	for i := len(moves) - 1; i >= 0; i-- {
		idx := moves[i].to
		displacedBy := t.slots[idx]
		t.slots[idx] = cur
		t.where[cur.val] = idx
		cur = displacedBy
	}
	if cur.val != v {
		panic("t18 cuckoo: undo did not recover the new entry")
	}
	delete(t.where, v)
	t.stats.InsertFailures++
	return false
}

// Delete removes the given entry (by identity).
func (t *t18CuckooTable[V]) Delete(v *V) {
	idx, ok := t.where[v]
	if !ok {
		panic("t18 cuckoo: deleting an entry that is not in the table")
	}
	t.slots[idx] = t18Slot[V]{}
	delete(t.where, v)
	t.count--
	t.stats.Deletes++
}

// Contains reports whether the entry is still stored (by identity).
func (t *t18CuckooTable[V]) Contains(v *V) bool {
	_, ok := t.where[v]
	return ok
}

func (t *t18CuckooTable[V]) freeWay(b int) int {
	for w := 0; w < t.ways; w++ {
		if !t.slots[b*t.ways+w].valid {
			return b*t.ways + w
		}
	}
	return -1
}

func (t *t18CuckooTable[V]) place(idx int, k t18Key, fp uint32, v *V) {
	t.slots[idx] = t18Slot[V]{valid: true, fp: fp, key: k, val: v}
	t.where[v] = idx
	t.count++
	t.notePeak()
}

func (t *t18CuckooTable[V]) notePeak() {
	if uint64(t.count) > t.stats.Peak {
		t.stats.Peak = uint64(t.count)
	}
}

func (t *t18CuckooTable[V]) nextRand() uint64 {
	// xorshift64: deterministic across runs.
	x := t.rng
	x ^= x << 13
	x ^= x >> 7
	x ^= x << 17
	t.rng = x
	return x
}
