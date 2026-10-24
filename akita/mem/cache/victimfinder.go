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
// re-fetch cost: Cost(tag) x Weight[RRPV], where Cost is the latency of
// fetching the line again (local DRAM, or the remote home over the mesh) and
// Weight is the re-use likelihood of a 2-bit SRRIP state (0 = re-referenced
// soon). A hit sets RRPV to 0; an insertion sets InsertRRPV and, when no
// other valid block of the set is at RRPV 3, ages the others by one.
// Invalid ways are used first; locked blocks and blocks being read are never
// chosen. Ties go to the least recently used block.
type CostVictimFinder struct {
	Cost       func(tag uint64) float64
	Weight     [4]float64
	InsertRRPV uint8
}

// NewCostVictimFinder returns a finder with weights 1, 1/2, 1/4, 1/8.
func NewCostVictimFinder(cost func(tag uint64) float64) *CostVictimFinder {
	return &CostVictimFinder{
		Cost:       cost,
		Weight:     [4]float64{1, 0.5, 0.25, 0.125},
		InsertRRPV: 2,
	}
}

// Score is the expected re-fetch cost of evicting b.
func (f *CostVictimFinder) Score(b *Block) float64 {
	return f.Cost(b.Tag) * f.Weight[b.RRPV&3]
}

// ScoreOf is the score a line with tag would have at RRPV r.
func (f *CostVictimFinder) ScoreOf(tag uint64, r uint8) float64 {
	return f.Cost(tag) * f.Weight[r&3]
}

// FindVictim returns the block to replace.
func (f *CostVictimFinder) FindVictim(set *Set) *Block {
	if b := f.FindVictimWhere(set, nil); b != nil {
		return b
	}
	return set.LRUQueue[0]
}

// FindVictimWhere is FindVictim among the blocks for which ok holds (nil =
// all); it returns nil when there is none.
func (f *CostVictimFinder) FindVictimWhere(set *Set, ok func(*Block) bool) *Block {
	for _, b := range set.Blocks {
		b.RRFresh = false
	}
	for _, b := range set.LRUQueue {
		if !b.IsValid && !b.IsLocked && b.ReadCount == 0 && (ok == nil || ok(b)) {
			b.RRFresh = true
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
		best.RRFresh = true
	}
	return best
}

// OnVisit updates the RRPV state.
func (f *CostVictimFinder) OnVisit(set *Set, b *Block) {
	if !b.RRFresh {
		b.RRPV = 0
		return
	}
	b.RRFresh = false
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
