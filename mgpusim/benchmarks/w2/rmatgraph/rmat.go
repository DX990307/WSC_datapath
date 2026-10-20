// Package rmatgraph generates the synthetic graphs of the W2 graph
// workloads (graphtc, bfs, sssp): a Kronecker / R-MAT graph with the
// Graph500 / GAP "kron" parameters (A=0.57, B=0.19, C=0.19), 2^scale
// vertices and edgeFactor*2^scale generated edges, made undirected, with
// self loops and duplicate edges removed and every adjacency list sorted.
// Vertex ids are randomly permuted (as Graph500 and GAP do), so the
// numbering carries no locality. Deterministic for a given seed.
package rmatgraph

import (
	"fmt"
	"math/rand"
	"sort"
)

// Graph500 / GAP kron initiator probabilities.
const (
	A = 0.57
	B = 0.19
	C = 0.19
)

// CSR is an undirected graph in compressed sparse row form: the neighbours
// of v are Col[RowPtr[v]:RowPtr[v+1]], sorted ascending, no duplicates, no
// self loops. W (when generated) holds a weight per directed entry; the two
// entries of an undirected edge carry the same weight.
type CSR struct {
	N      int
	RowPtr []int32
	Col    []int32
	W      []int32
}

// NumEntries is the number of directed CSR entries (2x undirected edges).
func (g *CSR) NumEntries() int { return len(g.Col) }

// MaxDegree returns the largest vertex degree.
func (g *CSR) MaxDegree() int {
	m := 0
	for v := 0; v < g.N; v++ {
		if d := int(g.RowPtr[v+1] - g.RowPtr[v]); d > m {
			m = d
		}
	}
	return m
}

// Generate returns the R-MAT graph with 2^scale vertices and
// edgeFactor*2^scale generated (undirected, before de-duplication) edges.
func Generate(scale, edgeFactor int, seed int64) (*CSR, error) {
	if scale < 1 || scale > 26 {
		return nil, fmt.Errorf("rmatgraph: scale %d outside 1..26", scale)
	}
	if edgeFactor < 1 {
		return nil, fmt.Errorf("rmatgraph: edge factor %d < 1", edgeFactor)
	}
	n := 1 << scale
	m := edgeFactor * n
	if 2*int64(m) >= 1<<31 {
		return nil, fmt.Errorf("rmatgraph: %d edges exceed the 32-bit index range", m)
	}
	rng := rand.New(rand.NewSource(seed))
	perm := rng.Perm(n)
	keys := make([]uint64, 0, 2*m)
	for e := 0; e < m; e++ {
		u, v := 0, 0
		for bit := 0; bit < scale; bit++ {
			r := rng.Float64()
			switch {
			case r < A:
			case r < A+B:
				v |= 1 << bit
			case r < A+B+C:
				u |= 1 << bit
			default:
				u |= 1 << bit
				v |= 1 << bit
			}
		}
		pu, pv := perm[u], perm[v]
		if pu == pv {
			continue
		}
		keys = append(keys, uint64(pu)<<32|uint64(pv), uint64(pv)<<32|uint64(pu))
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	g := &CSR{N: n, RowPtr: make([]int32, n+1)}
	g.Col = make([]int32, 0, len(keys))
	var last uint64 = ^uint64(0)
	for _, k := range keys {
		if k == last {
			continue
		}
		last = k
		g.RowPtr[int(k>>32)+1]++
		g.Col = append(g.Col, int32(uint32(k)))
	}
	for v := 0; v < n; v++ {
		g.RowPtr[v+1] += g.RowPtr[v]
	}
	return g, nil
}

// AddWeights gives every undirected edge a weight in [1, maxW] (a hash of
// the unordered pair and the seed, so both directions agree), like
// lonestar_sssp's uniform 1..100 weights.
func (g *CSR) AddWeights(maxW int, seed int64) {
	g.W = make([]int32, len(g.Col))
	for u := 0; u < g.N; u++ {
		for e := g.RowPtr[u]; e < g.RowPtr[u+1]; e++ {
			v := int(g.Col[e])
			a, b := u, v
			if a > b {
				a, b = b, a
			}
			h := mix(uint64(a)<<32 | uint64(b) ^ uint64(seed)*0x9e3779b97f4a7c15)
			g.W[e] = int32(1 + h%uint64(maxW))
		}
	}
}

func mix(k uint64) uint64 {
	k = (k ^ (k >> 30)) * 0xbf58476d1ce4e5b9
	k = (k ^ (k >> 27)) * 0x94d049bb133111eb
	return k ^ (k >> 31)
}

// PickSource returns a deterministic non-isolated vertex (GAP picks a random
// vertex with non-zero degree as the traversal source).
func (g *CSR) PickSource(seed int64) int {
	rng := rand.New(rand.NewSource(seed ^ 0x5eed))
	for i := 0; i < 1000*g.N; i++ {
		v := rng.Intn(g.N)
		if g.RowPtr[v+1] > g.RowPtr[v] {
			return v
		}
	}
	return 0
}
