package writeback

import "testing"

func TestLeaseTableReuse(t *testing.T) {
	tb := NewLeaseTable(LeaseTableConfig{Entries: 2})
	if got := tb.Reuse(0x1000, 0.3); got != 0.3 {
		t.Fatalf("untracked page: prior expected, got %v", got)
	}
	// page 1: 4 first fetches, never re-used -> below the prior
	for i := 0; i < 4; i++ {
		tb.NoteFetch(0x1000+uint64(i)*64, false)
	}
	if got := tb.Reuse(0x1000, 0.3); got >= 0.3 {
		t.Fatalf("streaming page should fall below the prior, got %v", got)
	}
	// page 2: 1 fetch, 6 hits, 1 re-fetch -> above the prior
	tb.NoteFetch(0x2000, false)
	for i := 0; i < 6; i++ {
		tb.NoteHit(0x2000)
	}
	tb.NoteFetch(0x2040, true)
	if got := tb.Reuse(0x2000, 0.3); got <= 0.7 {
		t.Fatalf("re-used page should be high, got %v", got)
	}
	// hits on untracked pages do not allocate; a third page evicts the LRU (page 1)
	tb.NoteHit(0x9000)
	tb.NoteFetch(0x3000, false)
	if tb.Reuse(0x1000, 0.5) != 0.5 || tb.Stats().Replacements != 1 {
		t.Fatalf("LRU replacement expected")
	}
	// line bits: the second fetch of a line is a re-fetch
	tb3 := NewLeaseTable(LeaseTableConfig{LineBits: true})
	tb3.NoteFetch(0x7000, false)
	tb3.NoteFetch(0x7000, false)
	tb3.NoteFetch(0x7040, true)
	if st := tb3.Stats(); st.Fetches != 2 || st.Refetches != 1 {
		t.Fatalf("line bits: %+v", st)
	}
	// halving keeps the ratio
	tb2 := NewLeaseTable(LeaseTableConfig{MaxCount: 8})
	for i := 0; i < 100; i++ {
		tb2.NoteFetch(0x5000, i%2 == 1)
	}
	if got := tb2.Reuse(0x5000, 0.5); got < 0.4 || got > 0.6 {
		t.Fatalf("decayed ratio, got %v", got)
	}
}

func TestLeaseTableEpisodes(t *testing.T) {
	tb := NewLeaseTable(LeaseTableConfig{Episodes: true})
	// page A: each line fetched once, hit 10 times -> every episode re-used once
	for i := uint64(0); i < 8; i++ {
		tb.NoteFetch(0x1000+i*64, false)
		for k := 0; k < 10; k++ {
			tb.NoteHit(0x1000 + i*64)
		}
	}
	if got := tb.Reuse(0x1000, 0.5); got < 0.85 || got > 1.0 {
		t.Fatalf("re-used page %v", got)
	}
	// page B: lines fetched once, never again
	for i := uint64(0); i < 8; i++ {
		tb.NoteFetch(0x2000+i*64, false)
	}
	if got := tb.Reuse(0x2000, 0.5); got > 0.15 {
		t.Fatalf("streaming page %v", got)
	}
	// page C: fetched twice (not kept): first episode re-used, second open
	tb.NoteFetch(0x3000, false)
	tb.NoteFetch(0x3000, false)
	if got := tb.Reuse(0x3000, 0.5); got < 0.4 || got > 0.6 {
		t.Fatalf("re-fetched line %v", got)
	}
}
