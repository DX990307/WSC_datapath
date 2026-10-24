package cache

// A VictimFinder decides with block should be evicted
type VictimFinder interface {
	FindVictim(set *Set) *Block
}

// LRUVictimFinder evicts the least recently used block to evict
type LRUVictimFinder struct {
}

// NewLRUVictimFinder returns a newly constructed lru evictor
func NewLRUVictimFinder() *LRUVictimFinder {
	e := new(LRUVictimFinder)
	return e
}

// FindVictim returns the least recently used block in a set
func (e *LRUVictimFinder) FindVictim(set *Set) *Block {
	// First try evicting an empty block
	for _, block := range set.LRUQueue {
		if !block.IsValid && !block.IsLocked {
			return block
		}
	}

	for _, block := range set.LRUQueue {
		if !block.IsLocked {
			return block
		}
	}

	return set.LRUQueue[0]
}

// VisitObserver is told about every Directory.Visit (hit or fill).
type VisitObserver interface {
	OnVisit(set *Set, block *Block)
}

// CostVictimFinder (ISCA27 R4) evicts the block with the smallest expected
// re-fetch cost: Cost(tag) x P(re-use | class, RRPV), where Cost is the
// latency of fetching the line again (local DRAM, or the remote home over
// the mesh), the class is local or remote (Class(tag)), and the re-use
// probability of a 2-bit SRRIP state is measured online per class: the
// share of blocks in that state whose next event was a hit rather than an
// eviction (Laplace prior, counters halved every DecayEvents events). A hit
// sets RRPV to 0; an insertion sets InsertRRPV and, when no other valid
// block of the set is at RRPV 3, ages the others by one. Invalid ways are
// used first; locked blocks and blocks being read are never chosen. Ties go
// to the least recently used block.
type CostVictimFinder struct {
	Cost        func(tag uint64) float64
	Class       func(tag uint64) int // 0 = local, 1 = remote
	InsertRRPV  uint8
	DecayEvents float64
	// Fixed: use the fixed weights below instead of the measured
	// probabilities (ablation).
	Fixed  bool
	Weight [4]float64

	hit, evict [2][4]float64
	events     float64
}

// NewCostVictimFinder returns a finder with measured re-use probabilities.
func NewCostVictimFinder(cost func(tag uint64) float64, class func(tag uint64) int) *CostVictimFinder {
	return &CostVictimFinder{
		Cost:        cost,
		Class:       class,
		Weight:      [4]float64{1, 0.5, 0.25, 0.125},
		InsertRRPV:  2,
		DecayEvents: 4096,
	}
}

func (f *CostVictimFinder) class(tag uint64) int {
	if f.Class == nil {
		return 0
	}
	return f.Class(tag) & 1
}

// Reuse is the re-use probability of a line of class c in state r.
func (f *CostVictimFinder) Reuse(c int, r uint8) float64 {
	if f.Fixed {
		return f.Weight[r&3]
	}
	h, e := f.hit[c][r&3], f.evict[c][r&3]
	return (h + 1) / (h + e + 2)
}

// Score is the expected re-fetch cost of evicting b.
func (f *CostVictimFinder) Score(b *Block) float64 {
	return f.Cost(b.Tag) * f.Reuse(f.class(b.Tag), b.RRPV)
}

// ScoreOf is the score a line with tag would have at RRPV r.
func (f *CostVictimFinder) ScoreOf(tag uint64, r uint8) float64 {
	return f.Cost(tag) * f.Reuse(f.class(tag), r)
}

func (f *CostVictimFinder) count(ev *[2][4]float64, c int, r uint8) {
	ev[c][r&3]++
	f.events++
	if f.DecayEvents > 0 && f.events >= f.DecayEvents {
		f.events = 0
		for i := range f.hit {
			for j := range f.hit[i] {
				f.hit[i][j] /= 2
				f.evict[i][j] /= 2
			}
		}
	}
}

// FindVictim returns the block to replace.
func (f *CostVictimFinder) FindVictim(set *Set) *Block {
	if b := f.FindVictimWhere(set, nil); b != nil {
		return b
	}
	return set.LRUQueue[0]
}

func (f *CostVictimFinder) mark(b *Block) {
	b.RRFresh = true
	b.RREvict = b.IsValid
	if b.IsValid {
		b.RREvC, b.RREvR = uint8(f.class(b.Tag)), b.RRPV&3
	}
}

// FindVictimWhere is FindVictim among the blocks for which ok holds (nil =
// all); it returns nil when there is none.
func (f *CostVictimFinder) FindVictimWhere(set *Set, ok func(*Block) bool) *Block {
	for _, b := range set.Blocks {
		b.RRFresh = false
	}
	for _, b := range set.LRUQueue {
		if !b.IsValid && !b.IsLocked && b.ReadCount == 0 && (ok == nil || ok(b)) {
			f.mark(b)
			return b
		}
	}
	var best *Block
	bs := 0.0
	for _, b := range set.LRUQueue {
		if b.IsLocked || b.ReadCount > 0 || (ok != nil && !ok(b)) {
			continue
		}
		if s := f.Score(b); best == nil || s < bs {
			best, bs = b, s
		}
	}
	if best != nil {
		f.mark(best)
	}
	return best
}

// OnVisit updates the RRPV state and the re-use statistics.
func (f *CostVictimFinder) OnVisit(set *Set, b *Block) {
	if !b.RRFresh {
		f.count(&f.hit, f.class(b.Tag), b.RRPV)
		b.RRPV = 0
		return
	}
	b.RRFresh = false
	if b.RREvict {
		b.RREvict = false
		f.count(&f.evict, int(b.RREvC), b.RREvR)
	}
	b.RRPV = f.InsertRRPV
	for _, o := range set.Blocks {
		if o != b && o.IsValid && o.RRPV >= 3 {
			return
		}
	}
	for _, o := range set.Blocks {
		if o != b && o.RRPV < 3 {
			o.RRPV++
		}
	}
}
