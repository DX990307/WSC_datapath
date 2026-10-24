package writeback

import (
	"testing"

	cachepkg "github.com/sarchlab/akita/v3/mem/cache"

	"github.com/sarchlab/akita/v3/mem/mem"
	"github.com/sarchlab/akita/v3/mem/vm"
	"github.com/sarchlab/akita/v3/sim"
)

func l2resTestCache() *Cache {
	return MakeBuilder().
		WithEngine(sim.NewSerialEngine()).
		WithByteSize(8 * 64).
		WithWayAssociativity(2).
		WithResidentFilter(true).
		WithRemoteReplicaFilter(true).
		Build("L2")
}

func l2resFill(addr uint64, req sim.VTimeInSec) *mem.RemoteDataFill {
	f := boundaryTestFill(addr, req+1)
	f.ReqTime = req
	return f
}

// setLocal makes way w of set 0 a valid clean local line.
func setLocal(c *Cache, w int, tag uint64) {
	b := c.directory.GetSets()[0].Blocks[w]
	b.IsValid, b.Tag, b.PID = true, tag, vm.PID(4)
	c.directory.Visit(b)
}

func TestCostReplacementRemoteFillEvictsLocal(t *testing.T) {
	remote := uint64(1)<<33 + 0x200 // set 0 of 4
	// M3 (LRU + protection): a full set of local lines drops the fill.
	c := l2resTestCache()
	setLocal(c, 0, 0x0)
	setLocal(c, 1, 0x100)
	if c.installRemoteDataFill(l2resFill(remote, 1)) {
		t.Fatal("M3 protection: remote fill evicted a local line")
	}
	// R4: the remote line is worth more than an aged local line.
	c = l2resTestCache()
	c.EnableCostReplacement(func(a uint64) float64 {
		if a>>33 == 0 {
			return 1
		}
		return 10
	}, 0, false, nil, true)
	setLocal(c, 0, 0x0)
	setLocal(c, 1, 0x100)
	if !c.installRemoteDataFill(l2resFill(remote, 1)) {
		t.Fatal("cost replacement dropped a valuable remote fill")
	}
	if s := c.GetL2ResStats(); s.FillEvictLocal != 1 {
		t.Fatalf("stats %+v", s)
	}
	b := c.directory.Lookup(vm.PID(4), remote)
	if b == nil || b.RRPV != 2 {
		t.Fatalf("installed block %+v", b)
	}
	// A demand fill now evicts the remaining local line (score 1 x w), not
	// the remote one (10 x w).
	v := c.directory.FindVictim(0x300)
	if v == nil || v.Tag>>33 != 0 {
		t.Fatalf("victim %+v", v)
	}
	// A first-touch fill (RRPV 3) of a cheap line is not admitted over a
	// recently hit expensive one.
	c2 := l2resTestCache()
	c2.EnableCostReplacement(func(a uint64) float64 {
		if a>>33 == 1 {
			return 10
		}
		return 1
	}, 0, false, nil, true)
	setLocal(c2, 0, uint64(1)<<33)
	setLocal(c2, 1, uint64(1)<<33+0x100)
	c2.directory.Visit(c2.directory.GetSets()[0].Blocks[0]) // hits: RRPV 0
	c2.directory.Visit(c2.directory.GetSets()[0].Blocks[1])
	f := l2resFill(uint64(2)<<33, 1)
	f.RequireInvalidVictim = true
	c2.EnableCostReplacement(func(a uint64) float64 {
		if a>>33 == 1 {
			return 10
		}
		return 1
	}, 0, false, nil, true)
	if c2.installRemoteDataFill(f) {
		t.Fatal("cheap first-touch fill replaced an expensive hot line")
	}
	if c2.GetL2ResStats().CostAdmitDrops != 1 {
		t.Fatal("admission drop not counted")
	}
}

func TestHolderTableInvalidatesAndRetains(t *testing.T) {
	c := l2resTestCache()
	ht := NewHolderTable(HolderTableConfig{})
	c.SetHolderTable(ht, 2)
	c.SetHomeWriteObserver(ht)
	page := uint64(1)<<33 + 0x3000
	if !c.installRemoteDataFill(l2resFill(page, 1)) {
		t.Fatal("setup")
	}
	if !ht.owners[1].m[page>>12].holders.has(2) {
		t.Fatal("holder not registered")
	}
	// a write at the home (another line of the page) drops the line
	ht.NoteWrite(3, page+0x40, 64)
	if c.directory.Lookup(vm.PID(4), page) != nil {
		t.Fatal("holder line survived a write")
	}
	if ht.Stats().InvalMsgs != 1 || c.GetL2ResStats().LinesInvalByWrites != 1 {
		t.Fatalf("stats %+v %+v", ht.Stats(), c.GetL2ResStats())
	}
	// re-read after the write (fresh, tracked) and a stale fill whose read
	// was opened before the write
	if !c.installRemoteDataFill(l2resFill(page, 5)) ||
		!c.installRemoteDataFill(l2resFill(page+0x80, 2)) {
		t.Fatal("refill")
	}
	written := func(uint64) bool { return true } // A1: page written
	inv, ret := c.InvalidateWrittenRemoteReplicas(10, written)
	if inv != 1 || ret != 1 || c.directory.Lookup(vm.PID(4), page) == nil {
		t.Fatalf("boundary: inv %d ret %d", inv, ret)
	}
	s := c.GetL2ResStats()
	if s.BoundaryTrackedKept != 1 || s.BoundaryTrackedDropped != 1 {
		t.Fatalf("stats %+v", s)
	}
}

func TestHolderTableReplacementDowngrades(t *testing.T) {
	c := l2resTestCache()
	ht := NewHolderTable(HolderTableConfig{CapPerOwner: 1})
	c.SetHolderTable(ht, 2)
	a := uint64(1)<<33 + 0x1000
	b := uint64(1)<<33 + 0x2040
	if !c.installRemoteDataFill(l2resFill(a, 1)) || !c.installRemoteDataFill(l2resFill(b, 1)) {
		t.Fatal("setup")
	}
	if ht.Stats().Replacements != 1 || ht.Stats().DowngradeMsgs != 1 ||
		c.GetL2ResStats().Downgrades != 1 {
		t.Fatalf("stats %+v %+v", ht.Stats(), c.GetL2ResStats())
	}
	// A1 says page a was written: the untracked line goes, tracked b stays
	inv, ret := c.InvalidateWrittenRemoteReplicas(10, func(l uint64) bool { return l>>12 == a>>12 })
	if inv != 1 || ret != 1 || c.directory.Lookup(vm.PID(4), a) != nil {
		t.Fatalf("boundary: inv %d ret %d", inv, ret)
	}
}

func TestHolderTableMarkOnWrite(t *testing.T) {
	c := l2resTestCache()
	ht := NewHolderTable(HolderTableConfig{MarkOnWrite: true})
	c.SetHolderTable(ht, 2)
	page := uint64(1)<<33 + 0x3000
	if !c.installRemoteDataFill(l2resFill(page, 1)) {
		t.Fatal("setup")
	}
	ht.NoteWrite(3, page+0x40, 64)
	if c.directory.Lookup(vm.PID(4), page) == nil {
		t.Fatal("marked line dropped mid-kernel")
	}
	if !c.installRemoteDataFill(l2resFill(page+0x80, 5)) {
		t.Fatal("refill")
	}
	// boundary: the marked line follows A1 (written: dropped), the line
	// read after the write stays
	inv, ret := c.InvalidateWrittenRemoteReplicas(10, func(uint64) bool { return true })
	if inv != 1 || ret != 1 || c.directory.Lookup(vm.PID(4), page+0x80) == nil {
		t.Fatalf("boundary: inv %d ret %d", inv, ret)
	}
}

func TestCostVictimFinderMeasuredReuse(t *testing.T) {
	c := l2resTestCache()
	c.EnableCostReplacement(func(a uint64) float64 {
		if a>>33 == 0 {
			return 1
		}
		return 3
	}, 0, false, func(a uint64) int { return int(a >> 33 & 1) }, false)
	f := c.CostFinder()
	// local lines: hits in state 0 are frequent; remote lines in state 2
	// are evicted without hits
	for i := 0; i < 50; i++ {
		f.OnVisit(&c.directory.GetSets()[0], &cachepkgBlockLocal)
	}
	if r := f.Reuse(0, 0); r < 0.9 {
		t.Fatalf("local state-0 re-use %f", r)
	}
	if r := f.Reuse(1, 2); r != 0.5 {
		t.Fatalf("prior %f", r)
	}
}

var cachepkgBlockLocal = cachepkg.Block{IsValid: true}
