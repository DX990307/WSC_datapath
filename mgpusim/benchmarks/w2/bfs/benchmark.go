// Package bfs is the W2 port of gpu_benchmarks tier2/rodinia_bfs: Rodinia's
// level-synchronous BFS (one thread per vertex, an expand and an update
// kernel per level) on a synthetic R-MAT / Kronecker graph (rmatgraph;
// upstream uses a uniform random directed graph, 20 edges per node). The
// atomicExch stores are plain stores (all writers store the same value);
// per-level buffers are fresh and the level count comes from the host
// reference (see kernels.cl). Registered as -benchmark=bfs.
package bfs

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

// ExpandArgs are the arguments of bfs_expand.
type ExpandArgs struct {
	RowPtr, ColIdx, Frontier, Visited, Next driver.Ptr
	NumNodes, Padding                       int32
	HiddenGlobalOffsetX                     int64
	HiddenGlobalOffsetY                     int64
	HiddenGlobalOffsetZ                     int64
}

// UpdateArgs are the arguments of bfs_update.
type UpdateArgs struct {
	Next, Visited, VisitedOut, FrontierOut, Cost driver.Ptr
	NewCost, NumNodes                            int32
	HiddenGlobalOffsetX                          int64
	HiddenGlobalOffsetY                          int64
	HiddenGlobalOffsetZ                          int64
}

// Params are the size knobs.
type Params struct {
	Scale, EdgeFactor int
	Seed              int64
}

// DefaultParams is a small smoke size.
func DefaultParams() Params { return Params{Scale: 12, EdgeFactor: 16, Seed: 1} }

// Benchmark is the BFS benchmark.
type Benchmark struct {
	driver         *driver.Driver
	context        *driver.Context
	gpus           []int
	expand, update *insts.HsaCo
	Params         Params

	g      *rmatgraph.CSR
	source int
	ref    []int32
	dCost  driver.Ptr
}

// NewBenchmark returns the benchmark with the default size.
func NewBenchmark(d *driver.Driver) *Benchmark {
	b := &Benchmark{driver: d, Params: DefaultParams()}
	b.context = d.Init()
	b.expand = kernels.LoadProgramFromMemory(hsacoBytes, "bfs_expand")
	b.update = kernels.LoadProgramFromMemory(hsacoBytes, "bfs_update")
	if b.expand == nil || b.update == nil {
		log.Panic("bfs: failed to load kernels")
	}
	return b
}

// SelectGPU selects the GPU.
func (b *Benchmark) SelectGPU(gpus []int) { b.gpus = gpus }

// SetUnifiedMemory is not supported.
func (b *Benchmark) SetUnifiedMemory() { panic("bfs does not support unified memory") }

// Reference is rodinia_bfs's bfs_reference: hop distances from source (-1
// unreached) and the number of levels upstream's loop runs (max distance
// + 1, the last level discovering nothing).
func Reference(g *rmatgraph.CSR, source int) (dist []int32, levels int) {
	dist = make([]int32, g.N)
	for i := range dist {
		dist[i] = -1
	}
	dist[source] = 0
	queue := []int32{int32(source)}
	for head := 0; head < len(queue); head++ {
		u := queue[head]
		levels = int(dist[u]) + 1
		for e := g.RowPtr[u]; e < g.RowPtr[u+1]; e++ {
			if v := g.Col[e]; dist[v] == -1 {
				dist[v] = dist[u] + 1
				queue = append(queue, v)
			}
		}
	}
	return dist, levels
}

func (b *Benchmark) upload(data []int32, name string) driver.Ptr {
	n := len(data)
	p := b.driver.AllocateMemory(b.context, uint64(4*n))
	b.driver.RegisterMemoryObject(b.context, p, uint64(4*n), name)
	b.driver.MemCopyH2D(b.context, p, data)
	return p
}

func (b *Benchmark) alloc(n int, name string) driver.Ptr {
	p := b.driver.AllocateMemory(b.context, uint64(4*n))
	b.driver.RegisterMemoryObject(b.context, p, uint64(4*n), name)
	return p
}

// Run generates the graph and launches every level.
func (b *Benchmark) Run() {
	b.driver.SelectGPU(b.context, b.gpus[0])
	b.driver.SetMagicCopyCoherent(true)
	g, err := rmatgraph.Generate(b.Params.Scale, b.Params.EdgeFactor, b.Params.Seed)
	if err != nil {
		log.Panic(err)
	}
	b.g = g
	b.source = g.PickSource(b.Params.Seed)
	var levels int
	b.ref, levels = Reference(g, b.source)
	n := g.N
	reached := 0
	for _, d := range b.ref {
		if d >= 0 {
			reached++
		}
	}

	dRow := b.upload(g.RowPtr, "bfs_row_ptr")
	dCol := b.upload(g.Col, "bfs_col_idx")
	cost := make([]int32, n)
	for i := range cost {
		cost[i] = -1
	}
	cost[b.source] = 0
	b.dCost = b.upload(cost, "bfs_cost")
	init := make([]int32, n)
	init[b.source] = 1
	frontier := b.upload(init, "bfs_frontier_l0")
	visited := b.upload(init, "bfs_visited_l0")
	zeros := make([]int32, n)
	wgs := (n + wgSize - 1) / wgSize
	q := b.driver.CreateCommandQueue(b.context)
	for l := 0; l < levels; l++ {
		next := b.upload(zeros, fmt.Sprintf("bfs_next_l%d", l))
		vOut := b.alloc(n, fmt.Sprintf("bfs_visited_l%d", l+1))
		fOut := b.alloc(n, fmt.Sprintf("bfs_frontier_l%d", l+1))
		ea := ExpandArgs{RowPtr: dRow, ColIdx: dCol, Frontier: frontier, Visited: visited,
			Next: next, NumNodes: int32(n)}
		b.driver.EnqueueLaunchKernel(q, b.expand, [3]uint32{uint32(wgs * wgSize), 1, 1},
			[3]uint16{wgSize, 1, 1}, &ea)
		ua := UpdateArgs{Next: next, Visited: visited, VisitedOut: vOut, FrontierOut: fOut,
			Cost: b.dCost, NewCost: int32(l + 1), NumNodes: int32(n)}
		b.driver.EnqueueLaunchKernel(q, b.update, [3]uint32{uint32(wgs * wgSize), 1, 1},
			[3]uint16{wgSize, 1, 1}, &ua)
		frontier, visited = fOut, vOut
	}
	fmt.Printf("BFS,config,scale=%d,edge_factor=%d,seed=%d,vertices=%d,csr_entries=%d,"+
		"max_degree=%d,source=%d,reached=%d,levels=%d,kernels=%d,wgs_per_kernel=%d,total_wgs=%d,"+
		"graph_mb=%.1f,level_buffers_mb=%.1f\n",
		b.Params.Scale, b.Params.EdgeFactor, b.Params.Seed, n, g.NumEntries(), g.MaxDegree(),
		b.source, reached, levels, 2*levels, wgs, 2*levels*wgs,
		float64(4*(n+1+g.NumEntries()+n))/1e6, float64(12*n*levels)/1e6)
	b.driver.DrainCommandQueue(q)
}

// Verify compares every distance with the CPU reference.
func (b *Benchmark) Verify() {
	got := make([]int32, b.g.N)
	b.driver.MemCopyD2H(b.context, got, b.dCost)
	bad := 0
	for i := range got {
		if got[i] != b.ref[i] {
			if bad < 5 {
				log.Printf("bfs: cost[%d] got %d want %d", i, got[i], b.ref[i])
			}
			bad++
		}
	}
	fmt.Printf("BFS,verify,vertices=%d,mismatches=%d\n", len(got), bad)
	if bad > 0 {
		log.Fatalf("bfs: %d mismatches", bad)
	}
	log.Printf("Passed!")
}
