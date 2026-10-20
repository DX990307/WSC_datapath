// Package sssp is the W2 port of gpu_benchmarks tier2/lonestar_sssp:
// topology-driven synchronous Bellman-Ford, rewritten from push
// (atomicMin) to pull (every vertex reads its neighbours' distances,
// sssp_pull in kernels.cl), with the same round-by-round distances. Input:
// a synthetic undirected R-MAT / Kronecker graph (rmatgraph; upstream uses
// a random directed graph) with integer weights 1..100 as upstream. One
// fresh dist buffer per round; the host reference gives the round count.
// Registered as -benchmark=sssp.
package sssp

import (
	// embed hsaco files
	_ "embed"
	"fmt"
	"log"
	"math"

	"github.com/sarchlab/mgpusim/v3/benchmarks/w2/rmatgraph"
	"github.com/sarchlab/mgpusim/v3/driver"
	"github.com/sarchlab/mgpusim/v3/insts"
	"github.com/sarchlab/mgpusim/v3/kernels"
)

//go:embed kernels.hsaco
var hsacoBytes []byte

const (
	wgSize = 256
	// VertsPerWG is the vertices per WG (a wave per vertex).
	VertsPerWG = 4
	// Inf is the unreached distance (INT_MAX, as upstream).
	Inf = math.MaxInt32
	// MaxWeight is upstream's weight range 1..100.
	MaxWeight = 100
)

// Args are the arguments of sssp_pull.
type Args struct {
	RowPtr, ColIdx, Weights, DistIn, DistOut driver.Ptr
	NumNodes, Padding                        int32
	HiddenGlobalOffsetX                      int64
	HiddenGlobalOffsetY                      int64
	HiddenGlobalOffsetZ                      int64
}

// Params are the size knobs. MaxRounds > 0 caps the rounds launched (the
// result is then not final; Verify checks the capped round's distances).
type Params struct {
	Scale, EdgeFactor int
	Seed              int64
	MaxRounds         int
}

// DefaultParams is a small smoke size.
func DefaultParams() Params { return Params{Scale: 12, EdgeFactor: 16, Seed: 1} }

// Benchmark is the SSSP benchmark.
type Benchmark struct {
	driver  *driver.Driver
	context *driver.Context
	gpus    []int
	co      *insts.HsaCo
	Params  Params

	g      *rmatgraph.CSR
	source int
	ref    []int32
	dLast  driver.Ptr
}

// NewBenchmark returns the benchmark with the default size.
func NewBenchmark(d *driver.Driver) *Benchmark {
	b := &Benchmark{driver: d, Params: DefaultParams()}
	b.context = d.Init()
	b.co = kernels.LoadProgramFromMemory(hsacoBytes, "sssp_pull")
	if b.co == nil {
		log.Panic("sssp: failed to load kernel")
	}
	return b
}

// SelectGPU selects the GPU.
func (b *Benchmark) SelectGPU(gpus []int) { b.gpus = gpus }

// SetUnifiedMemory is not supported.
func (b *Benchmark) SetUnifiedMemory() { panic("sssp does not support unified memory") }

// Rounds runs synchronous Bellman-Ford (lonestar_sssp's bellman_ford_cpu)
// for at most maxRounds rounds (0 = until no change) and returns the
// distances and the rounds upstream's loop runs, including the final round
// that changes nothing.
func Rounds(g *rmatgraph.CSR, source, maxRounds int) (dist []int32, rounds int) {
	dist = make([]int32, g.N)
	for i := range dist {
		dist[i] = Inf
	}
	dist[source] = 0
	prev := make([]int32, g.N)
	for rounds < g.N && (maxRounds <= 0 || rounds < maxRounds) {
		rounds++
		copy(prev, dist)
		changed := false
		for u := 0; u < g.N; u++ {
			if prev[u] == Inf {
				continue
			}
			for e := g.RowPtr[u]; e < g.RowPtr[u+1]; e++ {
				v := g.Col[e]
				if c := prev[u] + g.W[e]; c < dist[v] {
					dist[v] = c
					changed = true
				}
			}
		}
		if !changed {
			break
		}
	}
	return dist, rounds
}

func (b *Benchmark) upload(data []int32, name string) driver.Ptr {
	p := b.alloc(len(data), name)
	b.driver.MemCopyH2D(b.context, p, data)
	return p
}

func (b *Benchmark) alloc(n int, name string) driver.Ptr {
	p := b.driver.AllocateMemory(b.context, uint64(4*n))
	b.driver.RegisterMemoryObject(b.context, p, uint64(4*n), name)
	return p
}

// Run generates the graph and launches every round.
func (b *Benchmark) Run() {
	b.driver.SelectGPU(b.context, b.gpus[0])
	b.driver.SetMagicCopyCoherent(true)
	pr := b.Params
	g, err := rmatgraph.Generate(pr.Scale, pr.EdgeFactor, pr.Seed)
	if err != nil {
		log.Panic(err)
	}
	g.AddWeights(MaxWeight, pr.Seed)
	b.g = g
	b.source = g.PickSource(pr.Seed)
	var rounds int
	b.ref, rounds = Rounds(g, b.source, pr.MaxRounds)
	n := g.N
	if int64(n)*MaxWeight >= Inf {
		log.Panic("sssp: distances could overflow int32")
	}
	dRow := b.upload(g.RowPtr, "sssp_row_ptr")
	dCol := b.upload(g.Col, "sssp_col_idx")
	dW := b.upload(g.W, "sssp_weights")
	d0 := make([]int32, n)
	for i := range d0 {
		d0[i] = Inf
	}
	d0[b.source] = 0
	dist := b.upload(d0, "sssp_dist_r0")
	wgs := (n + VertsPerWG - 1) / VertsPerWG
	q := b.driver.CreateCommandQueue(b.context)
	for r := 0; r < rounds; r++ {
		out := b.alloc(n, fmt.Sprintf("sssp_dist_r%d", r+1))
		a := Args{RowPtr: dRow, ColIdx: dCol, Weights: dW, DistIn: dist, DistOut: out,
			NumNodes: int32(n)}
		b.driver.EnqueueLaunchKernel(q, b.co, [3]uint32{uint32(wgs * wgSize), 1, 1},
			[3]uint16{wgSize, 1, 1}, &a)
		dist = out
	}
	b.dLast = dist
	reached := 0
	for _, d := range b.ref {
		if d != Inf {
			reached++
		}
	}
	fmt.Printf("SSSP,config,scale=%d,edge_factor=%d,seed=%d,vertices=%d,csr_entries=%d,"+
		"max_degree=%d,source=%d,reached=%d,rounds=%d,wgs_per_kernel=%d,total_wgs=%d,"+
		"graph_mb=%.1f,dist_buffers_mb=%.1f\n",
		pr.Scale, pr.EdgeFactor, pr.Seed, n, g.NumEntries(), g.MaxDegree(), b.source, reached,
		rounds, wgs, rounds*wgs, float64(4*(n+1+2*g.NumEntries()))/1e6,
		float64(4*n*(rounds+1))/1e6)
	b.driver.DrainCommandQueue(q)
}

// Verify compares the final distances with the CPU reference.
func (b *Benchmark) Verify() {
	got := make([]int32, b.g.N)
	b.driver.MemCopyD2H(b.context, got, b.dLast)
	bad := 0
	for i := range got {
		if got[i] != b.ref[i] {
			if bad < 5 {
				log.Printf("sssp: dist[%d] got %d want %d", i, got[i], b.ref[i])
			}
			bad++
		}
	}
	fmt.Printf("SSSP,verify,vertices=%d,mismatches=%d\n", len(got), bad)
	if bad > 0 {
		log.Fatalf("sssp: %d mismatches", bad)
	}
	log.Printf("Passed!")
}
