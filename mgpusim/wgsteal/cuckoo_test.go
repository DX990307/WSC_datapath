package wgsteal

import (
	"math/rand"
	"testing"
)

func TestCuckooInsertLookupDelete(t *testing.T) {
	f := NewCuckooFilter(8, 4, 12, 0) // 1024 entries
	n := 900                          // ~88% load
	for i := 0; i < n; i++ {
		k := uint64(i)*7919 + 3
		if !f.Insert(HashPage(k), k) {
			t.Fatalf("insert %d failed", i)
		}
	}
	if f.InsertFails != 0 {
		t.Fatalf("%d inserts lost an item at 88%% load", f.InsertFails)
	}
	if f.Count() != n {
		t.Fatalf("count %d, want %d", f.Count(), n)
	}
	for i := 0; i < n; i++ {
		k := uint64(i)*7919 + 3
		hit, exact := f.LookupExact(HashPage(k), k)
		if !hit || !exact {
			t.Fatalf("item %d not found (no false negatives allowed)", i)
		}
	}
	// Delete every other item; the rest must still be found.
	for i := 0; i < n; i += 2 {
		k := uint64(i)*7919 + 3
		if !f.Delete(HashPage(k), k) {
			t.Fatalf("delete %d failed", i)
		}
	}
	if f.Count() != n/2 {
		t.Fatalf("count after delete %d, want %d", f.Count(), n/2)
	}
	for i := 1; i < n; i += 2 {
		k := uint64(i)*7919 + 3
		if _, exact := f.LookupExact(HashPage(k), k); !exact {
			t.Fatalf("item %d lost after deleting others", i)
		}
	}
	gone := 0
	for i := 0; i < n; i += 2 {
		k := uint64(i)*7919 + 3
		if _, exact := f.LookupExact(HashPage(k), k); !exact {
			gone++
		}
	}
	if gone != (n+1)/2 {
		t.Fatalf("%d of %d deleted items still exactly present", (n+1)/2-gone, (n+1)/2)
	}
}

func TestCuckooFalsePositiveRate(t *testing.T) {
	const fpBits = 12
	f := NewCuckooFilter(10, 4, fpBits, 0) // 4096 entries
	n := 3600                              // ~88% load
	for i := 0; i < n; i++ {
		k := uint64(i)
		f.Insert(HashPage(k), k)
	}
	fp := 0
	trials := 200000
	for i := 0; i < trials; i++ {
		k := uint64(1<<40) + uint64(i)
		if f.Lookup(HashPage(k)) {
			fp++
		}
	}
	rate := float64(fp) / float64(trials)
	// Upper bound for a filter with b slots per bucket and f-bit
	// fingerprints: 2b / 2^f (Fan et al.); 8 / 4096 = 0.195%.
	bound := 2.0 * 4 / float64(int(1)<<fpBits)
	if rate > bound {
		t.Fatalf("false-positive rate %.4f%% above bound %.4f%%", rate*100, bound*100)
	}
	t.Logf("false-positive rate %.4f%% (bound %.4f%%) at load %.2f",
		rate*100, bound*100, float64(n)/float64(f.Capacity()))
}

func TestCuckooOverflowKeepsCount(t *testing.T) {
	f := NewCuckooFilter(2, 2, 8, 0) // 8 entries
	for i := 0; i < 40; i++ {
		f.Insert(HashPage(uint64(i)), uint64(i))
	}
	if f.Count() > f.Capacity() {
		t.Fatalf("count %d above capacity %d", f.Count(), f.Capacity())
	}
	if f.InsertFails == 0 {
		t.Fatalf("expected failed inserts in an overfull filter")
	}
}

func TestCrossFilterProbe(t *testing.T) {
	rpf := NewRecentPages(9, 4, 12, 1024)
	grp := NewCuckooFilter(6, 4, 12, 9)
	rng := rand.New(rand.NewSource(1))
	// The group touches 200 pages; the GPM recently touched 100 of them
	// plus 500 others.
	pages := make([]uint64, 200)
	for i := range pages {
		pages[i] = rng.Uint64() >> 20
		grp.Insert(HashPage(pages[i]), pages[i])
	}
	for i := 0; i < 100; i++ {
		rpf.Touch(pages[i])
	}
	for i := 0; i < 500; i++ {
		rpf.Touch(rng.Uint64() >> 20)
	}
	items, hits, exact := grp.ProbeInto(rpf.F)
	if items != grp.Count() {
		t.Fatalf("probed %d items, filter holds %d", items, grp.Count())
	}
	if exact != 100 {
		t.Fatalf("exact overlap %d, want 100", exact)
	}
	if hits < exact || hits > exact+5 {
		t.Fatalf("probe hits %d far from exact %d", hits, exact)
	}
}

func TestRecentPagesAging(t *testing.T) {
	r := NewRecentPages(6, 4, 12, 128)
	for i := 0; i < 1000; i++ {
		r.Touch(uint64(i))
	}
	if r.Size() != 128 || r.F.Count() > 128 {
		t.Fatalf("window %d, filter count %d, want 128", r.Size(), r.F.Count())
	}
	// The most recent 128 pages are present, older ones mostly gone.
	for i := 1000 - 128; i < 1000; i++ {
		if _, exact := r.F.LookupExact(HashPage(uint64(i)), uint64(i)); !exact {
			t.Fatalf("recent page %d missing", i)
		}
	}
	old := 0
	for i := 0; i < 500; i++ {
		if r.F.Lookup(HashPage(uint64(i))) {
			old++
		}
	}
	if old > 10 {
		t.Fatalf("%d of 500 aged-out pages still answer present", old)
	}
}
