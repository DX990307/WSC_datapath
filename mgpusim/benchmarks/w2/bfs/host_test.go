package bfs

import (
	"testing"

	"github.com/sarchlab/mgpusim/v3/benchmarks/w2/rmatgraph"
)

// Simulates the two kernels level by level on the host and checks the
// costs against the reference BFS and the level count.
func TestLevelKernelsMatchReference(t *testing.T) {
	g, err := rmatgraph.Generate(10, 8, 4)
	if err != nil {
		t.Fatal(err)
	}
	src := g.PickSource(4)
	ref, levels := Reference(g, src)
	n := g.N
	cost := make([]int32, n)
	for i := range cost {
		cost[i] = -1
	}
	cost[src] = 0
	frontier := make([]int32, n)
	visited := make([]int32, n)
	frontier[src], visited[src] = 1, 1
	for l := 0; l < levels; l++ {
		next := make([]int32, n)
		for u := 0; u < n; u++ {
			if frontier[u] == 0 {
				continue
			}
			for e := g.RowPtr[u]; e < g.RowPtr[u+1]; e++ {
				if visited[g.Col[e]] == 0 {
					next[g.Col[e]] = 1
				}
			}
		}
		fOut, vOut := make([]int32, n), make([]int32, n)
		any := false
		for v := 0; v < n; v++ {
			newly := visited[v] == 0 && next[v] != 0
			if newly {
				fOut[v] = 1
				cost[v] = int32(l + 1)
				any = true
			}
			vOut[v] = visited[v]
			if newly {
				vOut[v] = 1
			}
		}
		if l == levels-1 && any {
			t.Fatal("last level still discovers vertices")
		}
		frontier, visited = fOut, vOut
	}
	for i := range cost {
		if cost[i] != ref[i] {
			t.Fatalf("cost[%d] = %d, want %d", i, cost[i], ref[i])
		}
	}
	if levels < 3 {
		t.Fatalf("only %d levels", levels)
	}
}
