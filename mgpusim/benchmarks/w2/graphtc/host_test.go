package graphtc

import (
	"testing"

	"github.com/sarchlab/mgpusim/v3/benchmarks/w2/rmatgraph"
)

// The per-edge merge the kernel does, summed, equals the reference.
func TestReferenceMatchesPerEdgeMerge(t *testing.T) {
	g, err := rmatgraph.Generate(9, 8, 2)
	if err != nil {
		t.Fatal(err)
	}
	src, dst := EdgeList(g)
	var raw uint64
	for e := range src {
		u, v := src[e], dst[e]
		i, ie := g.RowPtr[u], g.RowPtr[u+1]
		j, je := g.RowPtr[v], g.RowPtr[v+1]
		for i < ie && j < je {
			switch {
			case g.Col[i] == g.Col[j]:
				raw++
				i++
				j++
			case g.Col[i] < g.Col[j]:
				i++
			default:
				j++
			}
		}
	}
	// brute force triangles
	adj := map[[2]int32]bool{}
	for u := 0; u < g.N; u++ {
		for e := g.RowPtr[u]; e < g.RowPtr[u+1]; e++ {
			adj[[2]int32{int32(u), g.Col[e]}] = true
		}
	}
	var tri uint64
	for e := range src {
		u, v := src[e], dst[e]
		for k := g.RowPtr[v]; k < g.RowPtr[v+1]; k++ {
			w := g.Col[k]
			if w > v && adj[[2]int32{u, w}] {
				tri++
			}
		}
	}
	if ref := ReferenceRaw(g); raw != ref || ref != 3*tri {
		t.Fatalf("merge %d reference %d brute %d", raw, ref, 3*tri)
	}
	if tri == 0 {
		t.Fatal("no triangles; test graph too sparse")
	}
}
