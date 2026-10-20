package ivfflat

import (
	"fmt"
	"math"
	"math/rand"
	"runtime"
	"sort"
	"sync"
)

// Dim is the vector dimension (fixed by the kernel).
const Dim = 128

// Chunk is the most positions one ivf_scan work-group scans.
const Chunk = 256

// MaxCand is the most candidates ivf_merge holds per query (LDS).
const MaxCand = 1024

// Params is the input size.
type Params struct {
	N      int // database vectors
	NList  int // inverted lists (coarse centroids)
	NProbe int // lists probed per query
	Q      int // queries
	K      int // results per query
	Seed   int64
}

// Validate checks the parameters.
func (p Params) Validate() error {
	if p.N <= 0 || p.NList <= 0 || p.NProbe <= 0 || p.Q <= 0 || p.K <= 0 {
		return fmt.Errorf("ivfflat: sizes must be positive: %+v", p)
	}
	if p.NList > p.N || p.NProbe > p.NList {
		return fmt.Errorf("ivfflat: need nprobe <= nlist <= n: %+v", p)
	}
	if p.K > Chunk {
		return fmt.Errorf("ivfflat: k must be <= %d", Chunk)
	}
	return nil
}

// Index is the generated database, coarse quantizer and the work list.
type Index struct {
	Params
	Base      []float32 // [N][D] in list order
	IDs       []int32   // original id of each position
	ListStart []int32   // [NList+1]
	Queries   []float32 // [Q][D]
	Probes    []int32   // [Q][NProbe]
	Items     []int32   // [n][3] query, position, count
	QItems    []int32   // [Q+1]
}

// NumItems is the number of ivf_scan work-groups.
func (x *Index) NumItems() int { return len(x.Items) / 3 }

func gauss(rng *rand.Rand) float32 { return float32(rng.NormFloat64()) }

// Build generates a clustered synthetic database (NList Gaussian blobs,
// SIFT-like scale), picks NList random database vectors as centroids
// (faiss random init, no k-means iterations), assigns every vector to its
// nearest centroid, probes the NProbe nearest lists of every query
// (queries = database vectors + noise) and splits probed lists into
// work items of <= Chunk positions. Everything on the host.
func Build(p Params) (*Index, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	rng := rand.New(rand.NewSource(p.Seed))
	centers := make([]float32, p.NList*Dim)
	for i := range centers {
		centers[i] = rng.Float32() * 100
	}
	raw := make([]float32, p.N*Dim)
	for i := 0; i < p.N; i++ {
		c := rng.Intn(p.NList)
		for d := 0; d < Dim; d++ {
			raw[i*Dim+d] = centers[c*Dim+d] + 12*gauss(rng)
		}
	}
	cent := make([]float32, p.NList*Dim)
	for l, i := range rng.Perm(p.N)[:p.NList] {
		copy(cent[l*Dim:(l+1)*Dim], raw[i*Dim:(i+1)*Dim])
	}
	assign := make([]int32, p.N)
	parallel(p.N, func(i int) {
		assign[i] = int32(nearest(raw[i*Dim:(i+1)*Dim], cent, 1)[0])
	})
	x := &Index{Params: p}
	x.ListStart = make([]int32, p.NList+1)
	for _, a := range assign {
		x.ListStart[a+1]++
	}
	for l := 0; l < p.NList; l++ {
		x.ListStart[l+1] += x.ListStart[l]
	}
	fill := append([]int32(nil), x.ListStart[:p.NList]...)
	x.IDs = make([]int32, p.N)
	x.Base = make([]float32, p.N*Dim)
	for i, a := range assign { // ids ascending within a list
		pos := fill[a]
		fill[a]++
		x.IDs[pos] = int32(i)
		copy(x.Base[int(pos)*Dim:int(pos+1)*Dim], raw[i*Dim:(i+1)*Dim])
	}
	x.Queries = make([]float32, p.Q*Dim)
	for q := 0; q < p.Q; q++ {
		i := rng.Intn(p.N)
		for d := 0; d < Dim; d++ {
			x.Queries[q*Dim+d] = raw[i*Dim+d] + 6*gauss(rng)
		}
	}
	x.Probes = make([]int32, p.Q*p.NProbe)
	parallel(p.Q, func(q int) {
		for j, l := range nearest(x.Queries[q*Dim:(q+1)*Dim], cent, p.NProbe) {
			x.Probes[q*p.NProbe+j] = int32(l)
		}
	})
	x.QItems = make([]int32, p.Q+1)
	for q := 0; q < p.Q; q++ {
		for _, l := range x.Probes[q*p.NProbe : (q+1)*p.NProbe] {
			for s := x.ListStart[l]; s < x.ListStart[l+1]; s += Chunk {
				n := x.ListStart[l+1] - s
				if n > Chunk {
					n = Chunk
				}
				x.Items = append(x.Items, int32(q), s, n)
			}
		}
		x.QItems[q+1] = int32(x.NumItems())
		if c := int(x.QItems[q+1]-x.QItems[q]) * p.K; c > MaxCand {
			return nil, fmt.Errorf("ivfflat: query %d has %d candidates > %d (lower k or nprobe, raise nlist)",
				q, c, MaxCand)
		}
	}
	return x, nil
}

func parallel(n int, f func(i int)) {
	w := runtime.NumCPU()
	if w > 16 {
		w = 16
	}
	var wg sync.WaitGroup
	for t := 0; t < w; t++ {
		wg.Add(1)
		go func(t int) {
			defer wg.Done()
			for i := t; i < n; i += w {
				f(i)
			}
		}(t)
	}
	wg.Wait()
}

// nearest returns the indices of the m centroids closest to v.
func nearest(v, cent []float32, m int) []int {
	n := len(cent) / Dim
	d := make([]float64, n)
	idx := make([]int, n)
	for c := 0; c < n; c++ {
		var s float64
		for k := 0; k < Dim; k++ {
			t := float64(v[k] - cent[c*Dim+k])
			s += t * t
		}
		d[c], idx[c] = s, c
	}
	sort.Slice(idx, func(a, b int) bool {
		if d[idx[a]] != d[idx[b]] {
			return d[idx[a]] < d[idx[b]]
		}
		return idx[a] < idx[b]
	})
	return idx[:m]
}

// Dist is the kernel's fp32 squared distance (8-dim slices summed in order,
// then the 16 slices in order).
func Dist(x, q []float32) float32 {
	var sum float32
	for s := 0; s < 16; s++ {
		var acc float32
		for k := s * 8; k < s*8+8; k++ {
			t := x[k] - q[k]
			acc += t * t
		}
		sum += acc
	}
	return sum
}

// Result is a query's top-k.
type Result struct {
	Dist []float32
	ID   []int32
}

// Reference scans the probed lists of every query on the CPU.
func (x *Index) Reference() []Result {
	out := make([]Result, x.Q)
	parallel(x.Q, func(q int) {
		type c struct {
			d  float32
			id int32
		}
		var cs []c
		qv := x.Queries[q*Dim : (q+1)*Dim]
		for _, l := range x.Probes[q*x.NProbe : (q+1)*x.NProbe] {
			for pos := x.ListStart[l]; pos < x.ListStart[l+1]; pos++ {
				cs = append(cs, c{Dist(x.Base[int(pos)*Dim:int(pos+1)*Dim], qv), x.IDs[pos]})
			}
		}
		sort.Slice(cs, func(a, b int) bool {
			if cs[a].d != cs[b].d {
				return cs[a].d < cs[b].d
			}
			return cs[a].id < cs[b].id
		})
		r := Result{Dist: make([]float32, x.K), ID: make([]int32, x.K)}
		for i := 0; i < x.K; i++ {
			if i < len(cs) {
				r.Dist[i], r.ID[i] = cs[i].d, cs[i].id
			} else {
				r.Dist[i], r.ID[i] = float32(math.Inf(1)), -1
			}
		}
		out[q] = r
	})
	return out
}

// ScannedVectors is the number of database vectors the scan reads.
func (x *Index) ScannedVectors() int {
	n := 0
	for i := 0; i < x.NumItems(); i++ {
		n += int(x.Items[3*i+2])
	}
	return n
}
