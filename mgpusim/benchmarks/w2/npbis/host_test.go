package npbis

import "testing"

// Published partial-verification ranks of class S (upstream is.c): the
// host mirror must reproduce them, which checks randlc and create_seq.
func TestClassSPublishedRanks(t *testing.T) {
	p := Params{N: 1 << 16, Iterations: 1}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	mk := p.MaxKey()
	keys := CreateSeq(p.N, mk)
	testIndex := []int{48427, 17148, 23627, 62548, 4431}
	testRank := []int{0, 18, 346, 64917, 65463}
	for it := 1; it <= 3; it++ {
		Modify(keys, it, mk)
		r := RefRank(keys, mk)
		for i, idx := range testIndex {
			k := keys[idx]
			want := testRank[i] - it
			if i <= 2 {
				want = testRank[i] + it
			}
			if got := int(r[k-1]); got != want {
				t.Fatalf("iteration %d test key %d: rank %d, want %d", it, i, got, want)
			}
		}
	}
}

// TestBucketedRankModel runs the kernels' bucketed algorithm on the host
// (tile counts, column scan, bucket scan, scatter, per-bucket ranks) and
// compares with RefRank.
func TestBucketedRankModel(t *testing.T) {
	p := Params{N: 1 << 14, Iterations: 1}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	mk := p.MaxKey()
	nb, logw := p.Buckets()
	keys := CreateSeq(p.N, mk)
	Modify(keys, 1, mk)
	nt := p.N / Tile
	c := make([]int, nt*nb)
	r := make([]int, p.N)
	for i, k := range keys {
		b := int(k) >> logw
		r[i] = c[(i/Tile)*nb+b]
		c[(i/Tile)*nb+b]++
	}
	off := make([]int, nt*nb)
	tot := make([]int, nb)
	for b := 0; b < nb; b++ {
		for tl := 0; tl < nt; tl++ {
			off[tl*nb+b] = tot[b]
			tot[b] += c[tl*nb+b]
		}
	}
	start := make([]int, nb+1)
	for b := 0; b < nb; b++ {
		start[b+1] = start[b] + tot[b]
	}
	kb2 := make([]int32, p.N)
	seen := make([]bool, p.N)
	for i, k := range keys {
		b := int(k) >> logw
		pos := start[b] + off[(i/Tile)*nb+b] + r[i]
		if seen[pos] {
			t.Fatalf("position %d written twice", pos)
		}
		seen[pos] = true
		kb2[pos] = k
	}
	w := 1 << logw
	ref := RefRank(keys, mk)
	for b := 0; b < nb; b++ {
		h := make([]int, w)
		for i := start[b]; i < start[b+1]; i++ {
			h[int(kb2[i])-b*w]++
		}
		run := start[b]
		for j := 0; j < w; j++ {
			run += h[j]
			if int32(run) != ref[b*w+j] {
				t.Fatalf("key_buff1[%d] = %d, want %d", b*w+j, run, ref[b*w+j])
			}
		}
	}
}
