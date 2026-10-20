package sssp

import (
	"container/heap"
	"testing"

	"github.com/sarchlab/mgpusim/v3/benchmarks/w2/rmatgraph"
)

type item struct{ v, d int32 }
type pq []item

func (p pq) Len() int            { return len(p) }
func (p pq) Less(i, j int) bool  { return p[i].d < p[j].d }
func (p pq) Swap(i, j int)       { p[i], p[j] = p[j], p[i] }
func (p *pq) Push(x interface{}) { *p = append(*p, x.(item)) }
func (p *pq) Pop() interface{} {
	o := *p
	x := o[len(o)-1]
	*p = o[:len(o)-1]
	return x
}

// The pull rounds (what sssp_pull computes) reach the same fixed point in
// the same number of rounds as the push reference, and that is Dijkstra.
func TestPullMatchesPushAndDijkstra(t *testing.T) {
	g, err := rmatgraph.Generate(10, 8, 5)
	if err != nil {
		t.Fatal(err)
	}
	g.AddWeights(MaxWeight, 5)
	src := g.PickSource(5)
	ref, rounds := Rounds(g, src, 0)

	dist := make([]int32, g.N)
	for i := range dist {
		dist[i] = Inf
	}
	dist[src] = 0
	for r := 0; r < rounds; r++ {
		out := make([]int32, g.N)
		for v := 0; v < g.N; v++ {
			d := dist[v]
			for e := g.RowPtr[v]; e < g.RowPtr[v+1]; e++ {
				if du := dist[g.Col[e]]; du != Inf && du+g.W[e] < d {
					d = du + g.W[e]
				}
			}
			out[v] = d
		}
		// pull round r equals push round r
		pr, _ := Rounds(g, src, r+1)
		for v := range out {
			if out[v] != pr[v] {
				t.Fatalf("round %d vertex %d: pull %d push %d", r+1, v, out[v], pr[v])
			}
		}
		dist = out
	}
	// Dijkstra
	dj := make([]int32, g.N)
	for i := range dj {
		dj[i] = Inf
	}
	dj[src] = 0
	h := &pq{{int32(src), 0}}
	for h.Len() > 0 {
		it := heap.Pop(h).(item)
		if it.d > dj[it.v] {
			continue
		}
		for e := g.RowPtr[it.v]; e < g.RowPtr[it.v+1]; e++ {
			if c := it.d + g.W[e]; c < dj[g.Col[e]] {
				dj[g.Col[e]] = c
				heap.Push(h, item{g.Col[e], c})
			}
		}
	}
	for v := range dj {
		if dj[v] != ref[v] || dist[v] != ref[v] {
			t.Fatalf("vertex %d: dijkstra %d reference %d pull %d", v, dj[v], ref[v], dist[v])
		}
	}
	t.Logf("rounds %d", rounds)
}
