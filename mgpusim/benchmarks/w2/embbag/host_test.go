package embbag

import "testing"

func TestGenerate(t *testing.T) {
	p := Params{Tables: 3, Rows: 1000, Batch: 50, Pooling: 20, Alpha: 1.05, Seed: 2}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	a, b := Generate(p), Generate(p)
	count := map[int32]int{}
	for i, v := range a.Idx {
		if v != b.Idx[i] {
			t.Fatal("not deterministic")
		}
		if v < 0 || int(v) >= p.Rows {
			t.Fatalf("index %d out of range", v)
		}
		if (i/p.Pooling)%p.Tables == 0 {
			count[v]++
		}
	}
	// power law: the hottest row of table 0 takes a large share
	max := 0
	for _, c := range count {
		if c > max {
			max = c
		}
	}
	if total := p.Batch * p.Pooling; max*20 < total {
		t.Fatalf("hottest row %d of %d lookups: not skewed", max, total)
	}
	ref := a.Reference()
	// bag 4 = sample 1, table 1
	var want float32
	for k := 0; k < p.Pooling; k++ {
		want += a.Emb[(1*p.Rows+int(a.Idx[4*p.Pooling+k]))*Dim+7]
	}
	if ref[4*Dim+7] != want {
		t.Fatalf("reference %g want %g", ref[4*Dim+7], want)
	}
	if (Params{Tables: 1, Rows: 1, Batch: 1, Pooling: 1, Alpha: 1}).Validate() == nil {
		t.Fatal("alpha 1 accepted")
	}
}
