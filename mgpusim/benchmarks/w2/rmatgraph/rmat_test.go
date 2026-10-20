package rmatgraph

import "testing"

func TestGenerate(t *testing.T) {
	g, err := Generate(8, 8, 3)
	if err != nil {
		t.Fatal(err)
	}
	if int(g.RowPtr[g.N]) != len(g.Col) || len(g.Col) == 0 {
		t.Fatalf("bad row ptr")
	}
	adj := map[[2]int32]bool{}
	for u := 0; u < g.N; u++ {
		for e := g.RowPtr[u]; e < g.RowPtr[u+1]; e++ {
			v := g.Col[e]
			if int(v) == u || v < 0 || int(v) >= g.N {
				t.Fatalf("bad neighbour %d of %d", v, u)
			}
			if e > g.RowPtr[u] && g.Col[e-1] >= v {
				t.Fatalf("row %d not sorted/unique", u)
			}
			adj[[2]int32{int32(u), v}] = true
		}
	}
	for k := range adj {
		if !adj[[2]int32{k[1], k[0]}] {
			t.Fatalf("edge %v not symmetric", k)
		}
	}
	g.AddWeights(100, 3)
	for u := 0; u < g.N; u++ {
		for e := g.RowPtr[u]; e < g.RowPtr[u+1]; e++ {
			v := g.Col[e]
			if g.W[e] < 1 || g.W[e] > 100 {
				t.Fatalf("weight %d", g.W[e])
			}
			// find reverse
			for f := g.RowPtr[v]; f < g.RowPtr[v+1]; f++ {
				if int(g.Col[f]) == u && g.W[f] != g.W[e] {
					t.Fatalf("asymmetric weight")
				}
			}
		}
	}
	h, _ := Generate(8, 8, 3)
	for i := range g.Col {
		if g.Col[i] != h.Col[i] {
			t.Fatal("not deterministic")
		}
	}
	if s := g.PickSource(1); g.RowPtr[s+1] == g.RowPtr[s] {
		t.Fatal("isolated source")
	}
}
