// Package graphtc is the W2 port of gpu_benchmarks tier2/graph_tc:
// triangle counting by sorted-adjacency intersection, one thread per
// undirected edge (u < v). The input is a synthetic R-MAT / Kronecker graph
// (rmatgraph; upstream uses a uniform random graph) so adj(v) reads are
// skewed and data dependent. The host sums the per-edge counts and divides
// by 3 (upstream does the same). Registered as -benchmark=graphtc.
package graphtc

import (
	// embed hsaco files
	_ "embed"
	"fmt"
	"log"

	"github.com/sarchlab/mgpusim/v3/benchmarks/w2/rmatgraph"
	"github.com/sarchlab/mgpusim/v3/driver"
	"github.com/sarchlab/mgpusim/v3/insts"
	"github.com/sarchlab/mgpusim/v3/kernels"
)

//go:embed kernels.hsaco
var hsacoBytes []byte

const wgSize = 256

// Args are the arguments of tc_count.
type Args struct {
	RowPtr, ColIdx, EdgeSrc, EdgeDst, Counts driver.Ptr
	NumEdges, Padding                        int32
	HiddenGlobalOffsetX                      int64
	HiddenGlobalOffsetY                      int64
	HiddenGlobalOffsetZ                      int64
}

// Params are the size knobs.
type Params struct {
	Scale, EdgeFactor int
	Seed              int64
}

// DefaultParams is a small smoke size.
func DefaultParams() Params { return Params{Scale: 12, EdgeFactor: 16, Seed: 1} }

// Benchmark is the triangle-counting benchmark.
type Benchmark struct {
	driver  *driver.Driver
	context *driver.Context
	gpus    []int
	co      *insts.HsaCo
	Params  Params

	g          *rmatgraph.CSR
	src, dst   []int32
	dCounts    driver.Ptr
	gotRaw     uint64
	numEdgesUV int
}

// NewBenchmark returns the benchmark with the default size.
func NewBenchmark(d *driver.Driver) *Benchmark {
	b := &Benchmark{driver: d, Params: DefaultParams()}
	b.context = d.Init()
	b.co = kernels.LoadProgramFromMemory(hsacoBytes, "tc_count")
	if b.co == nil {
		log.Panic("graphtc: failed to load kernel")
	}
	return b
}

// SelectGPU selects the GPU.
func (b *Benchmark) SelectGPU(gpus []int) { b.gpus = gpus }

// SetUnifiedMemory is not supported.
func (b *Benchmark) SetUnifiedMemory() { panic("graphtc does not support unified memory") }

// EdgeList returns the u < v edges of g in CSR order (upstream's edge list).
func EdgeList(g *rmatgraph.CSR) (src, dst []int32) {
	for u := 0; u < g.N; u++ {
		for e := g.RowPtr[u]; e < g.RowPtr[u+1]; e++ {
			if v := g.Col[e]; int(v) > u {
				src = append(src, int32(u))
				dst = append(dst, v)
			}
		}
	}
	return src, dst
}

// ReferenceRaw is graph_tc_host::triangle_count_cpu: 3 x the triangles.
func ReferenceRaw(g *rmatgraph.CSR) uint64 {
	var t uint64
	for u := 0; u < g.N; u++ {
		for at := g.RowPtr[u]; at < g.RowPtr[u+1]; at++ {
			v := g.Col[at]
			if int(v) <= u {
				continue
			}
			ui, ue := at+1, g.RowPtr[u+1]
			vi, ve := g.RowPtr[v], g.RowPtr[v+1]
			for vi < ve && int(g.Col[vi]) <= int(v) {
				vi++
			}
			for ui < ue && vi < ve {
				switch {
				case g.Col[ui] == g.Col[vi]:
					t++
					ui++
					vi++
				case g.Col[ui] < g.Col[vi]:
					ui++
				default:
					vi++
				}
			}
		}
	}
	return 3 * t
}

func (b *Benchmark) upload(data []int32, name string) driver.Ptr {
	n := len(data)
	if n == 0 {
		n = 1
	}
	p := b.driver.AllocateMemory(b.context, uint64(4*n))
	b.driver.RegisterMemoryObject(b.context, p, uint64(4*n), name)
	if len(data) > 0 {
		b.driver.MemCopyH2D(b.context, p, data)
	}
	return p
}

// Run generates the graph and launches the kernel.
func (b *Benchmark) Run() {
	b.driver.SelectGPU(b.context, b.gpus[0])
	b.driver.SetMagicCopyCoherent(true)
	g, err := rmatgraph.Generate(b.Params.Scale, b.Params.EdgeFactor, b.Params.Seed)
	if err != nil {
		log.Panic(err)
	}
	b.g = g
	b.src, b.dst = EdgeList(g)
	ne := len(b.src)
	b.numEdgesUV = ne
	dRow := b.upload(g.RowPtr, "tc_row_ptr")
	dCol := b.upload(g.Col, "tc_col_idx")
	dSrc := b.upload(b.src, "tc_edge_src")
	dDst := b.upload(b.dst, "tc_edge_dst")
	b.dCounts = b.upload(make([]int32, ne), "tc_edge_counts")
	wgs := (ne + wgSize - 1) / wgSize
	fmt.Printf("GRAPHTC,config,scale=%d,edge_factor=%d,seed=%d,vertices=%d,csr_entries=%d,"+
		"edges_uv=%d,max_degree=%d,wgs=%d,footprint_mb=%.1f\n",
		b.Params.Scale, b.Params.EdgeFactor, b.Params.Seed, g.N, g.NumEntries(), ne,
		g.MaxDegree(), wgs, float64(4*(g.N+1+g.NumEntries()+3*ne))/1e6)
	q := b.driver.CreateCommandQueue(b.context)
	args := Args{RowPtr: dRow, ColIdx: dCol, EdgeSrc: dSrc, EdgeDst: dDst,
		Counts: b.dCounts, NumEdges: int32(ne)}
	b.driver.EnqueueLaunchKernel(q, b.co, [3]uint32{uint32(wgs * wgSize), 1, 1},
		[3]uint16{wgSize, 1, 1}, &args)
	b.driver.DrainCommandQueue(q)
}

// Verify compares the raw count with the CPU reference.
func (b *Benchmark) Verify() {
	got := make([]uint32, b.numEdgesUV)
	if len(got) > 0 {
		b.driver.MemCopyD2H(b.context, got, b.dCounts)
	}
	var raw uint64
	for _, c := range got {
		raw += uint64(c)
	}
	want := ReferenceRaw(b.g)
	fmt.Printf("GRAPHTC,verify,triangles=%d,raw_gpu=%d,raw_cpu=%d\n", raw/3, raw, want)
	if raw != want {
		log.Fatalf("graphtc: raw count %d, want %d", raw, want)
	}
	log.Printf("Passed!")
}
