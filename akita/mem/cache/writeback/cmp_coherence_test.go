package writeback

import (
	"testing"

	"github.com/sarchlab/akita/v3/sim"
)

func cmpTestHMG() *CmpCoherence {
	// GPMs 1..4: region 0 = {1, 2}, region 1 = {3, 4}
	return NewCmpCoherence(CmpConfig{
		Mode: CmpHMG, Log2Group: 8, Log2OwnerSize: 33,
		DirEntries: 2, DirWays: 2,
		RegionOf: map[int]int{1: 0, 2: 0, 3: 1, 4: 1},
		Regions:  map[int][]int{0: {1, 2}, 1: {3, 4}},
	})
}

func TestCmpHMGHierarchicalInval(t *testing.T) {
	c := cmpTestHMG()
	var sent [][3]uint64
	c.Send = func(now sim.VTimeInSec, from, to int, g uint64) {
		sent = append(sent, [3]uint64{uint64(from), uint64(to), g})
		c.OnInval(now, to, g)
	}
	line := uint64(1)<<33 | 0x1000 // home GPM 1
	// requesters 3 and 4 (other region) register through their region home
	r := c.RegionHome(3, line)
	if c.NextHop(3, line) != r && r != 3 {
		t.Fatalf("next hop")
	}
	c.register(0, 3, line)
	c.register(0, 4, line)
	c.register(0, 2, line) // same region as the home: direct
	c.noteWrite(0, line, 64, false)
	// home invalidates 2 and the region home; the region home forwards to
	// the other region GPMs that are not itself
	got := map[int]bool{}
	for _, s := range sent {
		got[int(s[1])] = true
	}
	if !got[2] || !got[3] || !got[4] {
		t.Fatalf("invalidations %v", sent)
	}
	if c.dirs[1].n != 0 {
		t.Fatalf("home entry not removed")
	}
}

func TestCmpHMGDirEviction(t *testing.T) {
	c := cmpTestHMG()
	n := 0
	c.Send = func(now sim.VTimeInSec, from, to int, g uint64) { n++ }
	for i := uint64(0); i < 3; i++ {
		c.register(0, 2, uint64(1)<<33|i<<8) // home 1, same set (1 set of 2 ways)
	}
	if c.stats.DirEvictions != 1 || n != 1 {
		t.Fatalf("evictions %d msgs %d", c.stats.DirEvictions, n)
	}
}

func TestCmpCARVEIMST(t *testing.T) {
	c := NewCmpCoherence(CmpConfig{Mode: CmpCARVE, Log2Group: 7, Log2OwnerSize: 33,
		GPMs: []int{1, 2, 3}, RDCSets: 16})
	n := 0
	c.Send = func(now sim.VTimeInSec, from, to int, g uint64) { n++ }
	line := uint64(1)<<33 | 0x80
	c.noteWrite(0, line, 64, false) // U -> P, no broadcast
	c.noteWrite(0, line, 64, true)  // remote write to P -> RWS, no broadcast
	if n != 0 || c.imst[line] != imstRWShared {
		t.Fatalf("n=%d st=%d", n, c.imst[line])
	}
	c.noteWrite(0, line, 64, false) // RWS: broadcast to 2 other GPMs
	if n != 2 {
		t.Fatalf("broadcast %d", n)
	}
	l2 := uint64(1)<<33 | 0x100
	c.OwnerRemoteRead(l2)
	if c.imst[l2] != imstReadShared {
		t.Fatalf("RS")
	}
	c.RDCInsert(2, l2, 0, make([]byte, 128), 5)
	if _, _, ok := c.RDCLookup(2, l2+64, 64); !ok {
		t.Fatalf("rdc hit")
	}
	c.noteWriteTime(6, l2, 64)
	c.NoteBoundary(2, 7)
	if _, _, ok := c.RDCLookup(2, l2, 64); ok {
		t.Fatalf("stale rdc line kept across boundary")
	}
}
