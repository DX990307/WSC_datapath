package rdma

import (
	"testing"
)

type t18TestVal struct {
	id   int
	open bool
}

func TestT18CuckooInsertLookupDelete(t *testing.T) {
	tab := newT18CuckooTable[t18TestVal](16, 4, 12, 32)
	vals := make([]*t18TestVal, 40)
	for i := range vals {
		vals[i] = &t18TestVal{id: i}
		if !tab.Insert(t18Key{pid: 1, line: uint64(i) * 64}, vals[i]) {
			t.Fatalf("insert %d failed at load %d/%d", i, tab.Len(), tab.Capacity())
		}
	}
	for i, v := range vals {
		got := tab.Lookup(t18Key{pid: 1, line: uint64(i) * 64}, nil)
		if got != v {
			t.Fatalf("lookup %d: got %v want %v", i, got, v)
		}
	}
	if tab.Lookup(t18Key{pid: 2, line: 0}, nil) != nil {
		t.Fatal("different PID must miss")
	}
	if tab.Lookup(t18Key{pid: 1, line: 9999 * 64}, nil) != nil {
		t.Fatal("absent line must miss")
	}
	for i := 0; i < 40; i += 2 {
		tab.Delete(vals[i])
	}
	for i, v := range vals {
		got := tab.Lookup(t18Key{pid: 1, line: uint64(i) * 64}, nil)
		if i%2 == 0 && got != nil {
			t.Fatalf("deleted %d still found", i)
		}
		if i%2 == 1 && got != v {
			t.Fatalf("kept %d lost after deletes", i)
		}
	}
	if tab.Len() != 20 {
		t.Fatalf("len %d, want 20", tab.Len())
	}
}

func TestT18CuckooFullAndUndo(t *testing.T) {
	tab := newT18CuckooTable[t18TestVal](4, 2, 12, 16)
	inserted := map[uint64]*t18TestVal{}
	failures := 0
	for i := 0; i < 64; i++ {
		v := &t18TestVal{id: i}
		k := t18Key{pid: 7, line: uint64(i) * 64}
		if tab.Insert(k, v) {
			inserted[k.line] = v
		} else {
			failures++
			// A failed insert must leave the table unchanged.
			if tab.Contains(v) {
				t.Fatal("failed insert left the entry in the table")
			}
		}
		for line, want := range inserted {
			if got := tab.Lookup(t18Key{pid: 7, line: line}, nil); got != want {
				t.Fatalf("after insert %d: line %d lost (got %v)", i, line, got)
			}
		}
	}
	if tab.Len() > tab.Capacity() || tab.Len() != len(inserted) {
		t.Fatalf("len %d inserted %d cap %d", tab.Len(), len(inserted), tab.Capacity())
	}
	if failures == 0 {
		t.Fatal("an 8-slot table must reject some of 64 inserts")
	}
	if tab.Stats().InsertFailures != uint64(failures) {
		t.Fatalf("insert failures stat %d, want %d",
			tab.Stats().InsertFailures, failures)
	}
}

// With a 1-bit fingerprint almost every lookup sees fingerprint matches on
// other lines; the full-address check must keep them from merging.
func TestT18CuckooFingerprintCollisionNeverMatches(t *testing.T) {
	tab := newT18CuckooTable[t18TestVal](2, 8, 1, 8)
	present := map[uint64]*t18TestVal{}
	for i := 0; i < 16; i++ {
		v := &t18TestVal{id: i}
		k := t18Key{pid: 3, line: uint64(i) * 4096}
		if tab.Insert(k, v) {
			present[k.line] = v
		}
	}
	for i := 0; i < 1000; i++ {
		line := uint64(i) * 64
		got := tab.Lookup(t18Key{pid: 3, line: line}, nil)
		want := present[line]
		if got != want {
			t.Fatalf("line %#x: got %v, want %v", line, got, want)
		}
		if got != nil && got.id != int(line/4096) {
			t.Fatalf("line %#x returned entry of another line", line)
		}
	}
	st := tab.Stats()
	if st.FingerprintFalsePos == 0 {
		t.Fatal("expected fingerprint false positives with 1-bit tags")
	}
	if st.FingerprintMatches <= st.FingerprintFalsePos {
		t.Fatal("true matches must also be counted as fingerprint matches")
	}
}

func TestT18CuckooDuplicateKeysWithPredicate(t *testing.T) {
	tab := newT18CuckooTable[t18TestVal](8, 4, 12, 16)
	k := t18Key{pid: 1, line: 0x1000}
	closed := &t18TestVal{id: 1, open: false}
	open := &t18TestVal{id: 2, open: true}
	if !tab.Insert(k, closed) || !tab.Insert(k, open) {
		t.Fatal("inserts failed")
	}
	isOpen := func(v *t18TestVal) bool { return v.open }
	if got := tab.Lookup(k, isOpen); got != open {
		t.Fatalf("predicate lookup got %v", got)
	}
	tab.Delete(open)
	if got := tab.Lookup(k, isOpen); got != nil {
		t.Fatal("no open entry should remain")
	}
	if got := tab.Lookup(k, nil); got != closed {
		t.Fatal("closed entry should remain")
	}
}

func TestT18CuckooHighLoad(t *testing.T) {
	// 2 x W slots, 4 ways: inserting W keys must always succeed.
	for _, w := range []int{64, 256, 1024, 4096} {
		tab := newT18CuckooTable[t18TestVal](2*w/4, 4, 12, 32)
		for i := 0; i < w; i++ {
			k := t18Key{pid: 1, line: uint64(i*7919) * 64}
			if !tab.Insert(k, &t18TestVal{id: i}) {
				t.Fatalf("W=%d: insert %d failed", w, i)
			}
		}
	}
}
