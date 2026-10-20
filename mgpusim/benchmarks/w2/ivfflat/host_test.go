package ivfflat

import (
	"math"
	"testing"
)

func TestBuild(t *testing.T) {
	p := Params{N: 3000, NList: 16, NProbe: 3, Q: 20, K: 5, Seed: 4}
	x, err := Build(p)
	if err != nil {
		t.Fatal(err)
	}
	seen := make([]bool, p.N)
	for _, id := range x.IDs {
		if seen[id] {
			t.Fatal("duplicate id")
		}
		seen[id] = true
	}
	if int(x.ListStart[p.NList]) != p.N {
		t.Fatal("lists do not cover the database")
	}
	// items cover exactly the probed lists, in chunks <= Chunk
	for q := 0; q < p.Q; q++ {
		n := 0
		for i := x.QItems[q]; i < x.QItems[q+1]; i++ {
			if x.Items[3*i] != int32(q) || x.Items[3*i+2] > Chunk {
				t.Fatal("bad item")
			}
			n += int(x.Items[3*i+2])
		}
		want := 0
		for _, l := range x.Probes[q*p.NProbe : (q+1)*p.NProbe] {
			want += int(x.ListStart[l+1] - x.ListStart[l])
		}
		if n != want {
			t.Fatalf("query %d: items cover %d, lists %d", q, n, want)
		}
	}
	ref := x.Reference()
	for q, r := range ref {
		for i := 1; i < p.K; i++ {
			if r.Dist[i] < r.Dist[i-1] {
				t.Fatal("not sorted")
			}
		}
		// the best result is no worse than any scanned vector of its first list
		l := x.Probes[q*p.NProbe]
		for pos := x.ListStart[l]; pos < x.ListStart[l+1]; pos++ {
			d := Dist(x.Base[int(pos)*Dim:int(pos+1)*Dim], x.Queries[q*Dim:(q+1)*Dim])
			if d < r.Dist[0] && !math.IsInf(float64(r.Dist[0]), 1) {
				t.Fatal("reference missed a closer vector")
			}
		}
	}
}
