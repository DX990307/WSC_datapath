// Package rgat implements the T17 W-C workload: R-GAT style sampled
// mini-batch inference (MLPerf Inference RGAT) on IGBH.
//
// Host side: the heterogeneous graph (IGBH from -rgat-data, or a synthetic
// graph with the IGBH schema), neighbour sampling like DGL's
// MultiLayerNeighborSampler (default 512 paper seeds, fanouts (10, 5), per
// relation, without replacement), the per-block index arrays and the
// weights. GPU side, per layer and per relation (kernels.cl): rgat_proj
// gathers the sampled source rows and projects them, rgat_agg computes the
// attention, the neighbour softmax and the weighted sum for each
// destination, adding into the destination type's output rows (relations
// summed, ReLU after the hidden layer). No atomics, pull only. Single
// attention head, no bias (simplifications of MLPerf's 4-head R-GAT).
//
// Raw features live in one buffer per node type (the whole table is
// allocated, as on a real GPU; only the rows the sample touches are
// copied). Buffers use the default allocator.
package rgat

import (
	// embed hsaco files
	_ "embed"
	"fmt"
	"log"
	"math"
	"math/rand"
	"sort"
	"strings"

	"github.com/sarchlab/mgpusim/v3/driver"
	"github.com/sarchlab/mgpusim/v3/insts"
	"github.com/sarchlab/mgpusim/v3/kernels"
)

//go:embed kernels.hsaco
var hsacoBytes []byte

// Kernel tile constants (kernels.cl).
const (
	MaxDeg = 32
	TileM  = 32
	TileN  = 64
	TileK  = 32
	wgSize = 256
)

// ProjArgs are the arguments of rgat_proj.
type ProjArgs struct {
	X, SrcIDs, W, H           driver.Ptr
	NRows, FIn, FOut, NTilesN int32
	HiddenGlobalOffsetX       int64
	HiddenGlobalOffsetY       int64
	HiddenGlobalOffsetZ       int64
}

// AggArgs are the arguments of rgat_agg.
type AggArgs struct {
	HSrc, XDst, DstIDs, RowPtr, ColIdx, AttnL, VR, Out driver.Ptr
	FIn, FH, UseAcc, Act                               int32
	HiddenGlobalOffsetX                                int64
	HiddenGlobalOffsetY                                int64
	HiddenGlobalOffsetZ                                int64
}

// Params are the size knobs.
type Params struct {
	DataDir     string // IGBH directory; empty = synthetic graph
	SynthPapers int    // synthetic graph size
	SynthDim    int    // synthetic feature dimension
	Seeds       int
	Fanouts     []int // input layer first
	Hidden      int
	Seed        int64
}

// DefaultParams returns a small synthetic default.
func DefaultParams() Params {
	return Params{SynthPapers: 2000, SynthDim: 128, Seeds: 32, Fanouts: []int{10, 5},
		Hidden: 64, Seed: 1}
}

// Benchmark is the R-GAT benchmark.
type Benchmark struct {
	driver  *driver.Driver
	context *driver.Context
	gpus    []int

	proj, agg *insts.HsaCo
	Params    Params

	graph  *Graph
	sample *Sample
	model  *Model
	outs   []map[string]driver.Ptr // per layer, per type
	launch []launchInfo
}

type launchInfo struct {
	name string
	wgs  int
}

// NewBenchmark returns the benchmark with the small default.
func NewBenchmark(d *driver.Driver) *Benchmark {
	b := &Benchmark{driver: d, Params: DefaultParams()}
	b.context = d.Init()
	b.proj = kernels.LoadProgramFromMemory(hsacoBytes, "rgat_proj")
	b.agg = kernels.LoadProgramFromMemory(hsacoBytes, "rgat_agg")
	if b.proj == nil || b.agg == nil {
		log.Panic("rgat: failed to load kernels")
	}
	return b
}

// SelectGPU selects the GPU.
func (b *Benchmark) SelectGPU(gpus []int) { b.gpus = gpus }

// SetUnifiedMemory is not supported.
func (b *Benchmark) SetUnifiedMemory() { panic("rgat does not support unified memory") }

// Prepare loads the graph, samples and builds the model (host only).
func Prepare(pr Params) (*Graph, *Sample, *Model, error) {
	for _, f := range pr.Fanouts {
		if f < 1 || f > MaxDeg {
			return nil, nil, nil, fmt.Errorf("rgat: fanout %d outside 1..%d", f, MaxDeg)
		}
	}
	if pr.Hidden <= 0 || pr.Hidden%TileN != 0 {
		return nil, nil, nil, fmt.Errorf("rgat: hidden %d must be a multiple of %d", pr.Hidden, TileN)
	}
	var g *Graph
	var err error
	if pr.DataDir != "" {
		g, err = LoadIGBH(pr.DataDir)
		if err != nil {
			return nil, nil, nil, err
		}
	} else {
		g = SyntheticIGBH(pr.SynthPapers, pr.SynthDim, pr.Seed)
	}
	if g.FeatDim%TileK != 0 {
		return nil, nil, nil, fmt.Errorf("rgat: feature dim %d not a multiple of %d", g.FeatDim, TileK)
	}
	for _, t := range g.Types {
		if uint64(g.NumNodes[t])*uint64(g.FeatDim) >= 1<<32 {
			return nil, nil, nil, fmt.Errorf("rgat: %s table exceeds 2^32 elements", t)
		}
	}
	rng := rand.New(rand.NewSource(pr.Seed))
	if pr.Seeds > g.NumNodes["paper"] {
		return nil, nil, nil, fmt.Errorf("rgat: %d seeds > %d papers", pr.Seeds, g.NumNodes["paper"])
	}
	seeds := make([]int32, pr.Seeds)
	for i, v := range rng.Perm(g.NumNodes["paper"])[:pr.Seeds] {
		seeds[i] = int32(v)
	}
	s := SampleBlocks(g, "paper", seeds, pr.Fanouts, rng)
	m := NewModel(s, g.FeatDim, pr.Hidden, pr.Seed)
	if err := s.Check(g, m); err != nil {
		return nil, nil, nil, err
	}
	return g, s, m, nil
}

// Check verifies every index the kernels dereference.
func (s *Sample) Check(g *Graph, m *Model) error {
	for l, blocks := range s.Blocks {
		for _, b := range blocks {
			rows := func(t string) int {
				if l == 0 {
					return g.NumNodes[t]
				}
				return len(s.Dst[l-1][t])
			}
			for _, r := range b.SrcRows {
				if r < 0 || int(r) >= rows(b.SrcType) {
					return fmt.Errorf("layer %d %s: src row %d out of range", l, b.RelName, r)
				}
			}
			for _, r := range b.DstRows {
				if r < 0 || int(r) >= rows(b.DstType) {
					return fmt.Errorf("layer %d %s: dst row %d out of range", l, b.RelName, r)
				}
			}
			if len(b.RowPtr) != len(b.DstNodes)+1 || int(b.RowPtr[len(b.DstNodes)]) != len(b.ColIdx) {
				return fmt.Errorf("layer %d %s: bad row pointer", l, b.RelName)
			}
			for i := range b.DstNodes {
				if d := b.RowPtr[i+1] - b.RowPtr[i]; d < 0 || int(d) > MaxDeg || int(d) > s.Fanouts[l] {
					return fmt.Errorf("layer %d %s: degree %d", l, b.RelName, d)
				}
			}
			for _, c := range b.ColIdx {
				if c < 0 || int(c) >= len(b.SrcNodes) {
					return fmt.Errorf("layer %d %s: neighbour %d out of range", l, b.RelName, c)
				}
			}
		}
	}
	return nil
}

func (b *Benchmark) alloc(bytes int, name string) driver.Ptr {
	if bytes == 0 {
		bytes = 4
	}
	p := b.driver.AllocateMemory(b.context, uint64(bytes))
	b.driver.RegisterMemoryObject(b.context, p, uint64(bytes), name)
	return p
}

func (b *Benchmark) upload(name string, data interface{}, n int) driver.Ptr {
	p := b.alloc(4*n, name)
	if n > 0 {
		b.driver.MemCopyH2D(b.context, p, data)
	}
	return p
}

// Run samples on the host, uploads and launches every kernel.
func (b *Benchmark) Run() {
	b.driver.SelectGPU(b.context, b.gpus[0])
	// Verify copies results back through the write-back L2 (no time).
	b.driver.SetMagicCopyCoherent(true)
	g, s, m, err := Prepare(b.Params)
	if err != nil {
		log.Panic(err)
	}
	b.graph, b.sample, b.model = g, s, m
	H := m.Hidden
	q := b.driver.CreateCommandQueue(b.context)

	// raw feature tables: allocate whole, copy the touched rows
	tables := map[string]driver.Ptr{}
	touched := b.touchedRows()
	for _, t := range g.Types {
		tables[t] = b.alloc(4*g.NumNodes[t]*g.FeatDim, "rgat_feat_"+t)
		row := make([]float32, g.FeatDim)
		for _, id := range touched[t] {
			g.Feats[t].Row(int(id), row)
			b.driver.MemCopyH2D(b.context,
				tables[t]+driver.Ptr(uint64(id)*uint64(g.FeatDim)*4), row)
		}
	}
	b.outs = make([]map[string]driver.Ptr, len(s.Blocks))
	for l := range s.Blocks {
		b.outs[l] = map[string]driver.Ptr{}
		for _, t := range sortedTypes(s.Dst[l]) {
			b.outs[l][t] = b.alloc(4*len(s.Dst[l][t])*H, fmt.Sprintf("rgat_out%d_%s", l, t))
		}
		input := func(t string) driver.Ptr {
			if l == 0 {
				return tables[t]
			}
			return b.outs[l-1][t]
		}
		fin := m.FIn[l]
		for _, blk := range s.Blocks[l] {
			k := [2]int{l, blk.Rel}
			nm := fmt.Sprintf("rgat_l%d_%s", l, blk.RelName)
			dSrc := b.upload(nm+"_src_rows", blk.SrcRows, len(blk.SrcRows))
			dDst := b.upload(nm+"_dst_rows", blk.DstRows, len(blk.DstRows))
			dPtr := b.upload(nm+"_row_ptr", blk.RowPtr, len(blk.RowPtr))
			dCol := b.upload(nm+"_col_idx", blk.ColIdx, len(blk.ColIdx))
			dW := b.upload(nm+"_w", m.W[k], len(m.W[k]))
			dAL := b.upload(nm+"_attn_l", m.AL[k], len(m.AL[k]))
			dV := b.upload(nm+"_v", m.V[k], len(m.V[k]))
			dH := b.alloc(4*len(blk.SrcNodes)*H, nm+"_h")

			ntn := H / TileN
			nProj := (len(blk.SrcNodes) + TileM - 1) / TileM * ntn
			pa := ProjArgs{X: input(blk.SrcType), SrcIDs: dSrc, W: dW, H: dH,
				NRows: int32(len(blk.SrcNodes)), FIn: int32(fin), FOut: int32(H), NTilesN: int32(ntn)}
			b.driver.EnqueueLaunchKernel(q, b.proj, [3]uint32{uint32(nProj * wgSize), 1, 1},
				[3]uint16{wgSize, 1, 1}, &pa)
			aa := AggArgs{HSrc: dH, XDst: input(blk.DstType), DstIDs: dDst, RowPtr: dPtr,
				ColIdx: dCol, AttnL: dAL, VR: dV, Out: b.outs[l][blk.DstType],
				FIn: int32(fin), FH: int32(H), UseAcc: b2i(blk.UseAcc), Act: b2i(blk.Act)}
			b.driver.EnqueueLaunchKernel(q, b.agg, [3]uint32{uint32(len(blk.DstNodes) * wgSize), 1, 1},
				[3]uint16{wgSize, 1, 1}, &aa)
			b.launch = append(b.launch, launchInfo{"proj_" + nm[5:], nProj},
				launchInfo{"agg_" + nm[5:], len(blk.DstNodes)})
		}
	}
	b.report(touched)
	b.driver.DrainCommandQueue(q)
}

func b2i(v bool) int32 {
	if v {
		return 1
	}
	return 0
}

func sortedTypes(m map[string][]int32) []string {
	var k []string
	for t := range m {
		k = append(k, t)
	}
	sort.Strings(k)
	return k
}

// touchedRows returns, per type, the raw-feature rows layer 0 reads.
func (b *Benchmark) touchedRows() map[string][]int32 {
	set := map[string]map[int32]bool{}
	add := func(t string, ids []int32) {
		if set[t] == nil {
			set[t] = map[int32]bool{}
		}
		for _, v := range ids {
			set[t][v] = true
		}
	}
	for _, blk := range b.sample.Blocks[0] {
		add(blk.SrcType, blk.SrcNodes)
		add(blk.DstType, blk.DstNodes)
	}
	out := map[string][]int32{}
	for t, s := range set {
		for v := range s {
			out[t] = append(out[t], v)
		}
		sort.Slice(out[t], func(i, j int) bool { return out[t][i] < out[t][j] })
	}
	return out
}

func (b *Benchmark) report(touched map[string][]int32) {
	g, s, m, pr := b.graph, b.sample, b.model, b.Params
	fan := make([]string, len(pr.Fanouts))
	for i, f := range pr.Fanouts {
		fan[i] = fmt.Sprint(f)
	}
	fmt.Printf("RGAT,config,graph=%s,types=%d,relations=%d,edges=%d,feat_dim=%d,seeds=%d,"+
		"fanouts=%s,hidden=%d,heads=1,seed=%d\n", g.Name, len(g.Types), len(g.Rels),
		g.NumEdges(), g.FeatDim, pr.Seeds, strings.Join(fan, ":"), m.Hidden, pr.Seed)
	for l := range s.Blocks {
		var parts []string
		edges, srcRows := 0, 0
		for _, t := range sortedTypes(s.Dst[l]) {
			parts = append(parts, fmt.Sprintf("%s:%d", t, len(s.Dst[l][t])))
		}
		for _, blk := range s.Blocks[l] {
			edges += len(blk.ColIdx)
			srcRows += len(blk.SrcNodes)
		}
		fmt.Printf("RGAT,layer%d,dst=%s,blocks=%d,edges=%d,projected_rows=%d\n",
			l, strings.Join(parts, ";"), len(s.Blocks[l]), edges, srcRows)
	}
	featRows := 0
	for _, ids := range touched {
		featRows += len(ids)
	}
	featBytes := uint64(featRows) * uint64(g.FeatDim) * 4
	var gather, hBytes, wBytes uint64
	totalWG, projWG, aggWG := 0, 0, 0
	for _, li := range b.launch {
		totalWG += li.wgs
		if strings.HasPrefix(li.name, "proj_") {
			projWG += li.wgs
		} else {
			aggWG += li.wgs
		}
	}
	for l, blocks := range s.Blocks {
		for _, blk := range blocks {
			gather += uint64(len(blk.SrcNodes)) * uint64(m.FIn[l]) * 4 * uint64(m.Hidden/TileN)
			hBytes += uint64(len(blk.SrcNodes)) * uint64(m.Hidden) * 4
			wBytes += uint64(m.FIn[l]*m.Hidden) * 4
		}
	}
	allocMB := 0.0
	for _, t := range g.Types {
		allocMB += float64(g.NumNodes[t]*g.FeatDim*4) / 1e6
	}
	fmt.Printf("RGAT,kernels,count=%d,proj_wgs=%d,agg_wgs=%d,total_wgs=%d\n",
		len(b.launch), projWG, aggWG, totalWG)
	var per []string
	for _, li := range b.launch {
		per = append(per, fmt.Sprintf("%s:%d", li.name, li.wgs))
	}
	fmt.Printf("RGAT,kernel_wgs,%s\n", strings.Join(per, ";"))
	fmt.Printf("RGAT,footprint,feature_rows=%d,feature_bytes=%d,projected_bytes=%d,weight_bytes=%d,"+
		"total_mb=%.1f,feature_gather_traffic_mb=%.1f,feature_tables_alloc_mb=%.1f\n",
		featRows, featBytes, hBytes, wBytes, float64(featBytes+hBytes+wBytes)/1e6,
		float64(gather)/1e6, allocMB)
}

// Verify compares the seeds' outputs with the float64 reference
// (tolerance 1e-4 absolute + 1e-3 relative).
func (b *Benchmark) Verify() {
	s, m, g := b.sample, b.model, b.graph
	feat := func(t string, id int32) []float32 {
		row := make([]float32, g.FeatDim)
		g.Feats[t].Row(int(id), row)
		return row
	}
	ref := Reference(s, m, feat)
	L := len(s.Blocks)
	bad, maxErr, n := 0, 0.0, 0
	for _, t := range sortedTypes(s.Dst[L-1]) {
		want := ref[L-1][t]
		got := make([]float32, len(want))
		b.driver.MemCopyD2H(b.context, got, b.outs[L-1][t])
		for i := range want {
			e := math.Abs(float64(got[i]) - want[i])
			maxErr = math.Max(maxErr, e)
			if !(e <= 1e-4+1e-3*math.Abs(want[i])) {
				if bad < 5 {
					log.Printf("rgat: %s[%d] got %g want %g", t, i, got[i], want[i])
				}
				bad++
			}
			n++
		}
	}
	fmt.Printf("RGAT,verify,elements=%d,max_abs_err=%.3g,mismatches=%d\n", n, maxErr, bad)
	if bad > 0 {
		log.Fatalf("rgat: %d mismatches", bad)
	}
	log.Printf("Passed!")
}
