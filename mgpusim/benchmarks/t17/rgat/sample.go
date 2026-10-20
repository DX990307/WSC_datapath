package rgat

import (
	"fmt"
	"math"
	"math/rand"
	"sort"
)

// Block is the sampled bipartite graph of one relation in one layer.
type Block struct {
	Layer            int
	Rel              int
	RelName          string
	SrcType, DstType string
	// DstNodes are the layer's destination nodes of DstType (global ids);
	// the block's destination i is DstNodes[i].
	DstNodes []int32
	// SrcNodes are the unique sampled sources (global ids, sorted).
	SrcNodes []int32
	// RowPtr/ColIdx: sampled neighbours of destination i, as indices into
	// SrcNodes.
	RowPtr []int32
	ColIdx []int32
	// Row indices into the layer's input tables (raw features by global id
	// for layer 0, the previous layer's output rows otherwise).
	SrcRows []int32
	DstRows []int32
	// Accumulate into the output rows (not the first relation of DstType),
	// and apply ReLU (last relation of DstType in a hidden layer).
	UseAcc, Act bool
}

// Sample is a sampled mini-batch: Dst[l][t] lists the destination nodes of
// type t of layer l (layer 0 reads the raw features, the last layer
// produces the seeds' outputs), Blocks[l] the blocks of layer l.
type Sample struct {
	Seeds   []int32
	Fanouts []int // per layer, input layer first (DGL order)
	Dst     []map[string][]int32
	Blocks  [][]Block
}

// SampleBlocks samples like DGL's MultiLayerNeighborSampler on a
// heterograph: from the last layer to the first, every destination node
// samples up to fanout in-neighbours per relation, without replacement; the
// destination nodes of layer l-1 are those of layer l followed by the newly
// sampled sources (sorted), per type.
func SampleBlocks(g *Graph, seedType string, seeds []int32, fanouts []int, rng *rand.Rand) *Sample {
	L := len(fanouts)
	s := &Sample{Seeds: seeds, Fanouts: fanouts, Dst: make([]map[string][]int32, L),
		Blocks: make([][]Block, L)}
	s.Dst[L-1] = map[string][]int32{seedType: append([]int32(nil), seeds...)}
	for l := L - 1; l >= 0; l-- {
		newSrc := map[string]map[int32]bool{}
		for ri, r := range g.Rels {
			dsts := s.Dst[l][r.Dst]
			if len(dsts) == 0 {
				continue
			}
			adj := g.in[ri]
			b := Block{Layer: l, Rel: ri, RelName: r.Name, SrcType: r.Src, DstType: r.Dst,
				DstNodes: dsts, RowPtr: make([]int32, len(dsts)+1)}
			var nbrs [][]int32
			uniq := map[int32]bool{}
			for _, d := range dsts {
				cand := adj.src[adj.ptr[d]:adj.ptr[d+1]]
				pick := cand
				if len(cand) > fanouts[l] {
					idx := rng.Perm(len(cand))[:fanouts[l]]
					sort.Ints(idx)
					pick = make([]int32, fanouts[l])
					for k, i := range idx {
						pick[k] = cand[i]
					}
				}
				nbrs = append(nbrs, pick)
				for _, v := range pick {
					uniq[v] = true
				}
			}
			if len(uniq) == 0 {
				continue
			}
			for v := range uniq {
				b.SrcNodes = append(b.SrcNodes, v)
			}
			sort.Slice(b.SrcNodes, func(i, j int) bool { return b.SrcNodes[i] < b.SrcNodes[j] })
			pos := make(map[int32]int32, len(b.SrcNodes))
			for i, v := range b.SrcNodes {
				pos[v] = int32(i)
			}
			for i, p := range nbrs {
				for _, v := range p {
					b.ColIdx = append(b.ColIdx, pos[v])
				}
				b.RowPtr[i+1] = int32(len(b.ColIdx))
			}
			if newSrc[r.Src] == nil {
				newSrc[r.Src] = map[int32]bool{}
			}
			for _, v := range b.SrcNodes {
				newSrc[r.Src][v] = true
			}
			s.Blocks[l] = append(s.Blocks[l], b)
		}
		if l > 0 {
			prev := map[string][]int32{}
			for t, dsts := range s.Dst[l] {
				prev[t] = append([]int32(nil), dsts...)
			}
			for t, set := range newSrc {
				in := map[int32]bool{}
				for _, v := range prev[t] {
					in[v] = true
				}
				var add []int32
				for v := range set {
					if !in[v] {
						add = append(add, v)
					}
				}
				sort.Slice(add, func(i, j int) bool { return add[i] < add[j] })
				prev[t] = append(prev[t], add...)
			}
			s.Dst[l-1] = prev
		}
	}
	// input rows, accumulation and activation flags
	for l := 0; l < L; l++ {
		pos := map[string]map[int32]int32{}
		if l > 0 {
			for t, nodes := range s.Dst[l-1] {
				pos[t] = map[int32]int32{}
				for i, v := range nodes {
					pos[t][v] = int32(i)
				}
			}
		}
		row := func(t string, v int32) int32 {
			if l == 0 {
				return v
			}
			p, ok := pos[t][v]
			if !ok {
				panic(fmt.Sprintf("rgat: node %s/%d missing from layer %d", t, v, l-1))
			}
			return p
		}
		seen := map[string]int{}
		last := map[string]int{}
		for i, b := range s.Blocks[l] {
			last[b.DstType] = i
		}
		for i := range s.Blocks[l] {
			b := &s.Blocks[l][i]
			b.UseAcc = seen[b.DstType] > 0
			seen[b.DstType]++
			b.Act = l < L-1 && last[b.DstType] == i
			b.SrcRows = make([]int32, len(b.SrcNodes))
			for k, v := range b.SrcNodes {
				b.SrcRows[k] = row(b.SrcType, v)
			}
			b.DstRows = make([]int32, len(b.DstNodes))
			for k, v := range b.DstNodes {
				b.DstRows[k] = row(b.DstType, v)
			}
		}
	}
	return s
}

// Model holds the per-layer, per-relation weights. Hidden is the projected
// width with the heads concatenated (GATConv concat=True): head hd owns the
// columns [hd*d, (hd+1)*d), d = Hidden/Heads, and its attention vectors are
// those columns of AL and AR.
type Model struct {
	Hidden int
	Heads  int
	FIn    []int                // per layer
	W      map[[2]int][]float32 // (layer, relation) -> [FIn][Hidden]
	AL, AR map[[2]int][]float32 // attention vectors [Hidden]
	V      map[[2]int][]float32 // per head W[:,head] . AR[head], [Heads][FIn]
}

// NewModel draws uniform weights with variance 1/fan_in for every block.
func NewModel(s *Sample, featDim, hidden, heads int, seed int64) *Model {
	m := &Model{Hidden: hidden, Heads: heads, W: map[[2]int][]float32{}, AL: map[[2]int][]float32{},
		AR: map[[2]int][]float32{}, V: map[[2]int][]float32{}}
	for l := range s.Blocks {
		fin := featDim
		if l > 0 {
			fin = hidden
		}
		m.FIn = append(m.FIn, fin)
		for _, b := range s.Blocks[l] {
			k := [2]int{l, b.Rel}
			rng := rand.New(rand.NewSource(seed*104729 + int64(l)*131 + int64(b.Rel)))
			w := make([]float32, fin*hidden)
			a := math.Sqrt(3 / float64(fin))
			for i := range w {
				w[i] = float32((rng.Float64()*2 - 1) * a)
			}
			al := make([]float32, hidden)
			ar := make([]float32, hidden)
			aa := math.Sqrt(3 / float64(hidden))
			for i := range al {
				al[i] = float32((rng.Float64()*2 - 1) * aa)
				ar[i] = float32((rng.Float64()*2 - 1) * aa)
			}
			d := hidden / heads
			v := make([]float32, heads*fin)
			for hd := 0; hd < heads; hd++ {
				for i := 0; i < fin; i++ {
					s := 0.0
					for c := hd * d; c < (hd+1)*d; c++ {
						s += float64(w[i*hidden+c]) * float64(ar[c])
					}
					v[hd*fin+i] = float32(s)
				}
			}
			m.W[k], m.AL[k], m.AR[k], m.V[k] = w, al, ar, v
		}
	}
	return m
}

// Reference computes every layer's outputs in float64: out[l][t] is
// [len(Dst[l][t])][Hidden]. feat(t, id) returns a raw feature row.
func Reference(s *Sample, m *Model, feat func(t string, id int32) []float32) []map[string][]float64 {
	L := len(s.Blocks)
	H := m.Hidden
	outs := make([]map[string][]float64, L)
	for l := 0; l < L; l++ {
		fin := m.FIn[l]
		input := func(t string, rowIdx int32, id int32) []float64 {
			x := make([]float64, fin)
			if l == 0 {
				for i, v := range feat(t, id) {
					x[i] = float64(v)
				}
				return x
			}
			copy(x, outs[l-1][t][int(rowIdx)*H:(int(rowIdx)+1)*H])
			return x
		}
		outs[l] = map[string][]float64{}
		for t, nodes := range s.Dst[l] {
			outs[l][t] = make([]float64, len(nodes)*H)
		}
		for _, b := range s.Blocks[l] {
			k := [2]int{l, b.Rel}
			w, al, ar := m.W[k], m.AL[k], m.AR[k]
			h := make([][]float64, len(b.SrcNodes))
			for j := range b.SrcNodes {
				x := input(b.SrcType, b.SrcRows[j], b.SrcNodes[j])
				h[j] = make([]float64, H)
				for c := 0; c < H; c++ {
					s := 0.0
					for i := 0; i < fin; i++ {
						s += x[i] * float64(w[i*H+c])
					}
					h[j][c] = s
				}
			}
			out := outs[l][b.DstType]
			d := H / m.Heads
			for i := range b.DstNodes {
				xd := input(b.DstType, b.DstRows[i], b.DstNodes[i])
				beg, end := b.RowPtr[i], b.RowPtr[i+1]
				row := out[i*H : (i+1)*H]
				if !b.UseAcc {
					for c := range row {
						row[c] = 0
					}
				}
				for hd := 0; hd < m.Heads; hd++ {
					c0, c1 := hd*d, (hd+1)*d
					er := 0.0
					for c := c0; c < c1; c++ {
						p := 0.0
						for q := 0; q < fin; q++ {
							p += xd[q] * float64(w[q*H+c])
						}
						er += p * float64(ar[c])
					}
					sc := make([]float64, end-beg)
					mx := math.Inf(-1)
					for e := beg; e < end; e++ {
						el := 0.0
						for c := c0; c < c1; c++ {
							el += h[b.ColIdx[e]][c] * float64(al[c])
						}
						v := el + er
						if v < 0 {
							v *= 0.2
						}
						sc[e-beg] = v
						mx = math.Max(mx, v)
					}
					sum := 0.0
					for q := range sc {
						sc[q] = math.Exp(sc[q] - mx)
						sum += sc[q]
					}
					for e := beg; e < end; e++ {
						for c := c0; c < c1; c++ {
							row[c] += sc[e-beg] / sum * h[b.ColIdx[e]][c]
						}
					}
				}
				if b.Act {
					for c := range row {
						row[c] = math.Max(row[c], 0)
					}
				}
			}
		}
	}
	return outs
}
