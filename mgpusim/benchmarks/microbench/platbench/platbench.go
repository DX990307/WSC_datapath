// Package platbench implements the T14 wafer-platform microbenchmarks. One
// benchmark runs one measurement item (Test); every measured point prints one
// CSV row prefixed with "PLATCSV," (header row "PLATCSV,header,...").
//
// Placement uses the probe package's SPMD skeleton: data lives on the GPM it
// is allocated for, and each kernel runs only on the GPM it is launched to.
// Times are simulated time measured around launch and drain; at 1 GHz one
// cycle is one nanosecond.
package platbench

import (
	// embed hsaco files
	_ "embed"
	"fmt"
	"math/rand"
	"sort"
	"strings"

	"github.com/sarchlab/mgpusim/v3/benchmarks/microbench/probe"
	"github.com/sarchlab/mgpusim/v3/driver"
	"github.com/sarchlab/mgpusim/v3/insts"
	"github.com/sarchlab/mgpusim/v3/kernels"
)

//go:embed kernels.hsaco
var hsacoBytes []byte

const (
	lineBytes = 64
	pageBytes = 4096
	wgSize    = 256
	// Work-groups per CU for bandwidth kernels (32 wavefronts per CU).
	wgsPerCU = 8
)

// ChaseArgs are the arguments of the chase kernel (no hidden arguments).
type ChaseArgs struct {
	Next  driver.Ptr
	Out   driver.Ptr
	Start uint32
	Steps uint32
}

// BwArgs are the arguments of the bw kernel (no hidden arguments).
type BwArgs struct {
	A, B, Sink driver.Ptr
	N4         uint32
	Iters      uint32
	Mode       uint32
	ActiveWGs  uint32
}

// GatherArgs are the arguments of the gather kernel (no hidden arguments).
type GatherArgs struct {
	Table, Srcs, Sink driver.Ptr
	NSrc              uint32
	Chunk4            uint32
	Off4              uint32
	Iters             uint32
}

// EmptyArgs are the arguments of the empty kernel.
type EmptyArgs struct {
	Sink                driver.Ptr
	HiddenGlobalOffsetX int64
	HiddenGlobalOffsetY int64
	HiddenGlobalOffsetZ int64
}

// Benchmark runs one T14 measurement item.
type Benchmark struct {
	driver  *driver.Driver
	context *driver.Context
	gpus    []int // the runner's GPU list (the unified GPU), for Test=kernel

	chaseCo, bwCo, gatherCo, emptyCo *insts.HsaCo

	Test       string   // latency, bandwidth, hopmatrix, remotebw, incast, patterns, kernel
	Config     string   // configuration name, copied into every row
	CUsPerGPM  int      // 32 (hpca) or 38 (mi300x-eighth)
	Src        int      // driver ID that runs the kernel
	Dsts       []int    // driver IDs that hold the data
	Footprints []uint64 // bytes
	Groups     []string // latency: A (page-local) and/or B (cross-page)
	Steps      int      // latency: measured chase steps
	WarmLimit  uint64   // latency: warm up footprints up to this size
	CUs        []int    // bandwidth: active CU counts
	Modes      []string // bandwidth: read, write, copy
	WGs        []int    // remotebw: work-group counts
	Ks         []int    // incast: number of readers
	Patterns   []string // patterns: alltoall, neighbor, transpose, ring, snake
	ChunkBytes uint64   // patterns: bytes read from each source
	Sources    []int    // hopmatrix: source driver IDs (0 = all GPMs)
	Seed       int64
}

// NewBenchmark returns a platform benchmark.
func NewBenchmark(d *driver.Driver) *Benchmark {
	b := &Benchmark{driver: d}
	b.context = d.Init()
	b.chaseCo = kernels.LoadProgramFromMemory(hsacoBytes, "chase")
	b.bwCo = kernels.LoadProgramFromMemory(hsacoBytes, "bw")
	b.gatherCo = kernels.LoadProgramFromMemory(hsacoBytes, "gather")
	b.emptyCo = kernels.LoadProgramFromMemory(hsacoBytes, "empty")
	b.Src = 1
	b.Steps = 20000
	b.WarmLimit = 8 << 20
	b.CUsPerGPM = 32
	b.ChunkBytes = 256 << 10
	b.Seed = 1
	return b
}

// SelectGPU records the runner's GPU list (used only by Test=kernel).
func (b *Benchmark) SelectGPU(gpus []int) { b.gpus = gpus }

// SetUnifiedMemory is not supported.
func (b *Benchmark) SetUnifiedMemory() { panic("platbench does not support unified memory") }

// Verify is not used (timing only).
func (b *Benchmark) Verify() {}

// Run runs the selected measurement item.
func (b *Benchmark) Run() {
	printHeader()
	switch b.Test {
	case "latency":
		b.runLatency()
	case "bandwidth":
		b.runBandwidth()
	case "hopmatrix":
		b.runHopMatrix()
	case "remotebw":
		b.runRemoteBW()
	case "incast":
		b.runIncast()
	case "patterns":
		b.runPatterns()
	case "kernel":
		b.runKernel()
	default:
		panic(fmt.Sprintf("unknown -plat-test %q", b.Test))
	}
}

// ---------------------------------------------------------------- output

var columns = []string{
	"config", "test", "group", "mode", "src_driver", "src_tile", "dst_driver",
	"dst_tile", "hops", "footprint_bytes", "threads", "wgs", "active_cus",
	"steps", "iters", "k", "pattern", "bytes_moved", "cycles", "latency_ns",
	"bandwidth_B_per_cycle",
}

type row map[string]interface{}

func printHeader() {
	fmt.Printf("PLATCSV,header,%s\n", strings.Join(columns, ","))
}

func (b *Benchmark) emit(r row) {
	r["config"] = b.Config
	r["test"] = b.Test
	vals := make([]string, len(columns))
	for i, c := range columns {
		if v, ok := r[c]; ok {
			switch x := v.(type) {
			case float64:
				vals[i] = fmt.Sprintf("%.6g", x)
			default:
				vals[i] = fmt.Sprint(x)
			}
		}
	}
	fmt.Printf("PLATCSV,row,%s\n", strings.Join(vals, ","))
}

// TileOf returns the 7x7 mesh tile index of a driver GPU ID (tile 24, the
// center, holds the driver/IOMMU).
func TileOf(gpu int) int {
	if gpu <= 24 {
		return gpu - 1
	}
	return gpu
}

// Hops returns the Manhattan distance between two GPMs on the 7x7 mesh.
func Hops(a, b int) int {
	ta, tb := TileOf(a), TileOf(b)
	dx, dy := ta%7-tb%7, ta/7-tb/7
	if dx < 0 {
		dx = -dx
	}
	if dy < 0 {
		dy = -dy
	}
	return dx + dy
}

func (b *Benchmark) pair(r row, src, dst int) row {
	r["src_driver"], r["src_tile"] = src, TileOf(src)
	r["dst_driver"], r["dst_tile"] = dst, TileOf(dst)
	r["hops"] = Hops(src, dst)
	return r
}

func elapsed(s *probe.SPMD, f func()) float64 {
	start := s.Now()
	f()
	return (s.Now() - start) * 1e9 // ns = cycles at 1 GHz
}

// ---------------------------------------------------------------- 1. latency

// buildRing returns next[] (element indices) for a random pointer ring with
// one node per cache line. Group A visits all lines of a page before moving to
// the next page; group B changes page on every step.
func buildRing(footprint uint64, group string, rng *rand.Rand) ([]uint32, uint32) {
	elemsPerLine := uint32(lineBytes / 4)
	linesPerPage := pageBytes / lineBytes
	pages := int(footprint / pageBytes)
	lines := int(footprint / lineBytes)
	if pages == 0 {
		pages = 1
		linesPerPage = lines
	}

	pageOrder := rng.Perm(pages)
	order := make([]int, 0, lines)
	if group == "B" && pages > 1 {
		for _, slot := range rng.Perm(linesPerPage) {
			for _, p := range pageOrder {
				order = append(order, p*linesPerPage+slot)
			}
		}
	} else {
		for _, p := range pageOrder {
			for _, slot := range rng.Perm(linesPerPage) {
				order = append(order, p*linesPerPage+slot)
			}
		}
	}

	next := make([]uint32, footprint/4)
	for i, n := range order {
		succ := order[(i+1)%len(order)]
		next[uint32(n)*elemsPerLine] = uint32(succ) * elemsPerLine
	}
	return next, uint32(order[0]) * elemsPerLine
}

// chaseLatency measures the average latency of one chase step (ns) with a
// warm-up pass for footprints up to WarmLimit. It subtracts a zero-step
// launch so that kernel launch overhead cancels.
func (b *Benchmark) chaseLatency(s *probe.SPMD, rank int, next driver.Ptr, out driver.Ptr,
	start uint32, nodes int, warm bool, steps int) float64 {
	launch := func(n int) float64 {
		return elapsed(s, func() {
			s.RunPhase(b.chaseCo, []probe.Launch{{
				Rank: rank, Grid: [3]uint32{1, 1, 1}, WG: [3]uint16{1, 1, 1},
				Args: &ChaseArgs{Next: next, Out: out, Start: start, Steps: uint32(n)},
			}})
		})
	}
	if warm {
		launch(nodes)
	}
	t0 := launch(0)
	t := launch(steps)
	return (t - t0) / float64(steps)
}

func (b *Benchmark) runLatency() {
	rng := rand.New(rand.NewSource(b.Seed))
	s := probe.NewSPMD(b.driver, b.context, []int{b.Src})
	out := s.AllocFor(0, b.Src, 64)
	for _, dst := range b.Dsts {
		for _, fp := range b.Footprints {
			for _, g := range b.Groups {
				if g == "B" && fp <= pageBytes {
					continue // a single page cannot be crossed
				}
				next, start := buildRing(fp, g, rng)
				buf := s.AllocFor(0, dst, fp)
				b.driver.MemCopyH2D(s.Ctxs[0], buf, next)
				warm := fp <= b.WarmLimit
				lat := b.chaseLatency(s, 0, buf, out, start, int(fp/lineBytes), warm, b.Steps)
				b.emit(b.pair(row{
					"group": g, "footprint_bytes": fp, "threads": 1, "wgs": 1,
					"steps": b.Steps, "latency_ns": lat, "cycles": lat * float64(b.Steps),
					"mode": map[bool]string{true: "warm", false: "cold"}[warm],
				}, b.Src, dst))
				if err := b.driver.FreeMemory(s.Ctxs[0], buf); err != nil {
					panic(err)
				}
			}
		}
	}
}

// ---------------------------------------------------------------- 2. bandwidth

func modeID(m string) uint32 {
	switch m {
	case "read":
		return 0
	case "write":
		return 1
	case "copy":
		return 2
	}
	panic("unknown mode " + m)
}

// bwLaunch runs the bw kernel on rank's GPM with activeCUs CUs doing work.
func (b *Benchmark) bwLaunch(s *probe.SPMD, rank int, a, bb, sink driver.Ptr,
	bytes uint64, iters uint32, mode uint32, activeCUs int) float64 {
	nwg := b.CUsPerGPM * wgsPerCU
	return elapsed(s, func() {
		s.RunPhase(b.bwCo, []probe.Launch{{
			Rank: rank, Grid: [3]uint32{uint32(nwg * wgSize), 1, 1}, WG: [3]uint16{wgSize, 1, 1},
			Args: &BwArgs{A: a, B: bb, Sink: sink, N4: uint32(bytes / 16), Iters: iters,
				Mode: mode, ActiveWGs: uint32(activeCUs * wgsPerCU)},
		}})
	})
}

// runBandwidth measures local read/write/copy bandwidth versus active CUs. A
// footprint at or above 16 MB reads HBM once (no warm-up); a smaller one
// (L2) is warmed up and then read Iters times.
func (b *Benchmark) runBandwidth() {
	s := probe.NewSPMD(b.driver, b.context, []int{b.Src})
	sink := s.AllocFor(0, b.Src, 64)
	for _, fp := range b.Footprints {
		hbm := fp >= 16<<20
		iters := uint32(1)
		if !hbm {
			iters = 4
		}
		a := s.AllocFor(0, b.Src, fp)
		bb := s.AllocFor(0, b.Src, fp)
		for _, m := range b.Modes {
			for _, k := range b.CUs {
				if k > b.CUsPerGPM {
					continue
				}
				// For copy, each side holds half of the footprint.
				span := fp
				if m == "copy" {
					span = fp / 2
				}
				if !hbm {
					b.bwLaunch(s, 0, a, bb, sink, span, 1, modeID(m), b.CUsPerGPM)
				}
				t := b.bwLaunch(s, 0, a, bb, sink, span, iters, modeID(m), k)
				moved := span * uint64(iters)
				if m == "copy" {
					moved *= 2
				}
				b.emit(b.pair(row{
					"group": map[bool]string{true: "HBM", false: "L2"}[hbm], "mode": m,
					"footprint_bytes": fp, "threads": k * wgsPerCU * wgSize,
					"wgs": k * wgsPerCU, "active_cus": k, "iters": iters,
					"bytes_moved": moved, "cycles": t,
					"bandwidth_B_per_cycle": float64(moved) / t,
				}, b.Src, b.Src))
			}
		}
		for _, p := range []driver.Ptr{a, bb} {
			if err := b.driver.FreeMemory(s.Ctxs[0], p); err != nil {
				panic(err)
			}
		}
	}
}

// ---------------------------------------------------------------- 3. hop matrix

// runHopMatrix measures remote-L2-hit latency for (source, owner) pairs: the
// owner first reads its 1 MB buffer locally (filling its L2), then the source
// chases through it with one thread.
func (b *Benchmark) runHopMatrix() {
	n := b.driver.GetNumGPUs()
	all := make([]int, n)
	for i := range all {
		all[i] = i + 1
	}
	sources := b.Sources
	if len(sources) == 1 && sources[0] == 0 {
		sources = all
	}
	owners := b.Dsts
	if len(owners) == 1 && owners[0] == 0 {
		owners = all
	}
	s := probe.NewSPMD(b.driver, b.context, all)
	rng := rand.New(rand.NewSource(b.Seed))
	fp := b.Footprints[0]
	sinks := s.AllocShards(64)
	for _, dst := range owners {
		next, start := buildRing(fp, "A", rng)
		buf := s.AllocFor(dst-1, dst, fp)
		b.driver.MemCopyH2D(s.Ctxs[dst-1], buf, next)
		for _, src := range sources {
			if src == dst {
				continue
			}
			b.bwLaunch(s, dst-1, buf, buf, sinks[dst-1], fp, 1, 0, b.CUsPerGPM) // warm owner L2
			lat := b.chaseLatency(s, src-1, buf, sinks[src-1], start, 0, false, b.Steps)
			b.emit(b.pair(row{
				"group": "remoteL2", "footprint_bytes": fp, "threads": 1, "wgs": 1,
				"steps": b.Steps, "latency_ns": lat, "cycles": lat * float64(b.Steps),
			}, src, dst))
		}
		if err := b.driver.FreeMemory(s.Ctxs[dst-1], buf); err != nil {
			panic(err)
		}
	}
}

// ---------------------------------------------------------------- 4. remote BW

// readers builds an SPMD over the given GPMs plus the owner, with a pointer
// table so that kernels can address the owner's buffer.
func (b *Benchmark) gatherLaunch(s *probe.SPMD, rank int, table, srcs, sink driver.Ptr,
	nsrc int, chunk, off uint64, iters uint32, wgs int) probe.Launch {
	return probe.Launch{
		Rank: rank, Grid: [3]uint32{uint32(wgs * wgSize), 1, 1}, WG: [3]uint16{wgSize, 1, 1},
		Args: &GatherArgs{Table: table, Srcs: srcs, Sink: sink, NSrc: uint32(nsrc),
			Chunk4: uint32(chunk / 16), Off4: uint32(off / 16), Iters: iters},
	}
}

// remoteRead allocates a buffer on owner and returns a pointer table that
// holds its address, on the reader's GPM.
func (b *Benchmark) ownerTable(s *probe.SPMD, rank int, owner driver.Ptr, nslots int) (driver.Ptr, driver.Ptr) {
	addrs := make([]uint64, nslots)
	for i := range addrs {
		addrs[i] = uint64(owner)
	}
	table := s.AllocFor(rank, s.GPUs[rank], uint64(8*nslots))
	b.driver.MemCopyH2D(s.Ctxs[rank], table, addrs)
	srcs := s.AllocFor(rank, s.GPUs[rank], 4)
	b.driver.MemCopyH2D(s.Ctxs[rank], srcs, []uint32{0})
	return table, srcs
}

// runRemoteBW measures one reader's remote read bandwidth versus its number
// of work-groups, for each owner and footprint (below L2: warmed in the
// owner's L2; at or above 16 MB: remote HBM).
func (b *Benchmark) runRemoteBW() {
	for _, dst := range b.Dsts {
		s := probe.NewSPMD(b.driver, b.context, []int{b.Src, dst})
		sinks := s.AllocShards(64)
		for _, fp := range b.Footprints {
			hbm := fp >= 16<<20
			buf := s.AllocFor(1, dst, fp)
			table, srcs := b.ownerTable(s, 0, buf, 1)
			for _, w := range b.WGs {
				if !hbm {
					b.bwLaunch(s, 1, buf, buf, sinks[1], fp, 1, 0, b.CUsPerGPM)
				}
				t := elapsed(s, func() {
					s.RunPhase(b.gatherCo, []probe.Launch{
						b.gatherLaunch(s, 0, table, srcs, sinks[0], 1, fp, 0, 1, w)})
				})
				b.emit(b.pair(row{
					"group": map[bool]string{true: "remoteHBM", false: "remoteL2"}[hbm],
					"mode":  "read", "footprint_bytes": fp, "threads": w * wgSize, "wgs": w,
					"iters": 1, "bytes_moved": fp, "cycles": t,
					"bandwidth_B_per_cycle": float64(fp) / t,
				}, b.Src, dst))
			}
		}
	}
}

// ---------------------------------------------------------------- 5. incast

// runIncast: k readers read the same owner's buffer (below L2, warmed)
// concurrently, either all the same lines (shared) or disjoint slices.
func (b *Benchmark) runIncast() {
	owner := b.Dsts[0]
	var others []int
	for g := 1; g <= b.driver.GetNumGPUs(); g++ {
		if g != owner {
			others = append(others, g)
		}
	}
	// Readers are taken nearest-first so that small k is not dominated by distance.
	sort.SliceStable(others, func(i, j int) bool { return Hops(others[i], owner) < Hops(others[j], owner) })

	fp := b.Footprints[0]
	for _, k := range b.Ks {
		if k > len(others) {
			continue
		}
		gpus := append([]int{owner}, others[:k]...)
		s := probe.NewSPMD(b.driver, b.context, gpus)
		sinks := s.AllocShards(64)
		buf := s.AllocFor(0, owner, fp)
		tables := make([]driver.Ptr, len(gpus))
		srcs := make([]driver.Ptr, len(gpus))
		for r := 1; r < len(gpus); r++ {
			tables[r], srcs[r] = b.ownerTable(s, r, buf, 1)
		}
		for _, mode := range b.Modes { // shared, disjoint
			b.bwLaunch(s, 0, buf, buf, sinks[0], fp, 1, 0, b.CUsPerGPM)
			var ls []probe.Launch
			var moved uint64
			for r := 1; r < len(gpus); r++ {
				chunk, off := fp, uint64(0)
				if mode == "disjoint" {
					chunk = fp / uint64(k) / lineBytes * lineBytes
					off = uint64(r-1) * chunk
				}
				moved += chunk
				ls = append(ls, b.gatherLaunch(s, r, tables[r], srcs[r], sinks[r], 1,
					chunk, off, 1, b.CUsPerGPM*wgsPerCU))
			}
			t := elapsed(s, func() { s.RunPhase(b.gatherCo, ls) })
			b.emit(b.pair(row{
				"group": "remoteL2", "mode": mode, "footprint_bytes": fp, "k": k,
				"wgs": k * b.CUsPerGPM * wgsPerCU, "bytes_moved": moved, "cycles": t,
				"bandwidth_B_per_cycle": float64(moved) / t,
			}, others[0], owner))
		}
	}
}

// ---------------------------------------------------------------- 6. patterns

func meshXY(gpu int) (int, int) { t := TileOf(gpu); return t % 7, t / 7 }

func gpuAt(x, y int) int {
	t := y*7 + x
	if t == 24 {
		return 0
	}
	if t < 24 {
		return t + 1
	}
	return t
}

// sourcesFor returns the GPMs that gpu reads from under a pattern.
func sourcesFor(pattern string, gpu, n int, snake []int) []int {
	x, y := meshXY(gpu)
	switch pattern {
	case "alltoall":
		var s []int
		for g := 1; g <= n; g++ {
			if g != gpu {
				s = append(s, g)
			}
		}
		return s
	case "neighbor":
		var s []int
		for _, d := range [][2]int{{1, 0}, {-1, 0}, {0, 1}, {0, -1}} {
			nx, ny := x+d[0], y+d[1]
			if nx >= 0 && nx < 7 && ny >= 0 && ny < 7 && gpuAt(nx, ny) != 0 {
				s = append(s, gpuAt(nx, ny))
			}
		}
		return s
	case "transpose":
		if t := gpuAt(y, x); t != gpu && t != 0 {
			return []int{t}
		}
		return nil
	case "ring":
		return []int{gpu%n + 1}
	case "snake":
		for i, g := range snake {
			if g == gpu {
				return []int{snake[(i+1)%len(snake)]}
			}
		}
	}
	panic("unknown pattern " + pattern)
}

// snakeOrder visits the mesh row by row, alternating direction, skipping the
// center tile.
func snakeOrder() []int {
	var o []int
	for y := 0; y < 7; y++ {
		for i := 0; i < 7; i++ {
			x := i
			if y%2 == 1 {
				x = 6 - i
			}
			if g := gpuAt(x, y); g != 0 {
				o = append(o, g)
			}
		}
	}
	return o
}

// runPatterns: every GPM holds a shard and reads ChunkBytes from each of its
// pattern sources at the same time.
func (b *Benchmark) runPatterns() {
	n := b.driver.GetNumGPUs()
	all := make([]int, n)
	for i := range all {
		all[i] = i + 1
	}
	s := probe.NewSPMD(b.driver, b.context, all)
	sinks := s.AllocShards(64)
	shards := s.AllocShards(b.ChunkBytes)
	tables := s.UploadPtrTable(shards)
	snake := snakeOrder()
	for _, p := range b.Patterns {
		var ls []probe.Launch
		var moved uint64
		var pairs, hopSum int
		for r, g := range all {
			src := sourcesFor(p, g, n, snake)
			if len(src) == 0 {
				continue
			}
			ranks := make([]uint32, len(src))
			for i, x := range src {
				ranks[i] = uint32(x - 1)
				hopSum += Hops(g, x)
			}
			pairs += len(src)
			list := s.AllocFor(r, g, uint64(4*len(ranks)))
			b.driver.MemCopyH2D(s.Ctxs[r], list, ranks)
			ls = append(ls, b.gatherLaunch(s, r, tables[r], list, sinks[r], len(src),
				b.ChunkBytes, 0, 1, b.CUsPerGPM*wgsPerCU))
			moved += b.ChunkBytes * uint64(len(src))
		}
		t := elapsed(s, func() { s.RunPhase(b.gatherCo, ls) })
		avgHops := 0.0
		if pairs > 0 {
			avgHops = float64(hopSum) / float64(pairs)
		}
		b.emit(row{
			"pattern": p, "mode": "read", "footprint_bytes": b.ChunkBytes, "k": pairs,
			"hops": fmt.Sprintf("%.3f", avgHops), "wgs": len(ls) * b.CUsPerGPM * wgsPerCU,
			"bytes_moved": moved, "cycles": t, "bandwidth_B_per_cycle": float64(moved) / t,
		})
	}
}

// ---------------------------------------------------------------- 7. kernel

// runKernel measures empty-kernel launch-to-completion time and the average
// interval of 10 back-to-back kernels: on one GPM, on all GPMs with one queue
// each, and on the runner's unified GPU.
func (b *Benchmark) runKernel() {
	n := b.driver.GetNumGPUs()
	all := make([]int, n)
	for i := range all {
		all[i] = i + 1
	}
	measure := func(name string, s *probe.SPMD, ranks []int, wgs int) {
		sinks := s.AllocShards(64)
		launches := func() []probe.Launch {
			ls := make([]probe.Launch, len(ranks))
			for i, r := range ranks {
				ls[i] = probe.Launch{Rank: r, Grid: [3]uint32{uint32(wgs * 64), 1, 1},
					WG: [3]uint16{64, 1, 1}, Args: &EmptyArgs{Sink: sinks[r]}}
			}
			return ls
		}
		one := elapsed(s, func() { s.RunPhase(b.emptyCo, launches()) })
		ten := elapsed(s, func() {
			for i := 0; i < 10; i++ {
				s.RunPhase(b.emptyCo, launches())
			}
		})
		b.emit(row{"pattern": name, "k": len(ranks), "wgs": wgs, "cycles": one,
			"latency_ns": ten / 10, "iters": 10})
	}

	single := probe.NewSPMD(b.driver, b.context, []int{b.Src})
	measure("one-gpm", single, []int{0}, 1)
	every := probe.NewSPMD(b.driver, b.context, all)
	ranks := make([]int, n)
	for i := range ranks {
		ranks[i] = i
	}
	measure("all-gpms-per-gpm-queues", every, ranks, 1)

	// Unified GPUs (how the existing benchmarks run): over this one GPM, and
	// the runner's unified GPU over all GPMs. One work-group per CU.
	ctx := b.driver.InitWithExistingPID(b.context)
	one := b.driver.CreateUnifiedGPU(ctx, []int{b.Src})
	b.measureUnified(ctx, "unified-1-gpm", one, 1)
	if len(b.gpus) == 1 {
		b.measureUnified(ctx, "unified-all-gpms", b.gpus[0], n)
	}
}

func (b *Benchmark) measureUnified(ctx *driver.Context, name string, dev, ngpm int) {
	b.driver.SelectGPU(ctx, dev)
	q := b.driver.CreateCommandQueue(ctx)
	sink := b.driver.AllocateMemory(ctx, 64)
	wgs := ngpm * b.CUsPerGPM
	now := func() float64 { return float64(b.driver.Engine.CurrentTime()) * 1e9 }
	launch := func() {
		b.driver.EnqueueLaunchKernel(q, b.emptyCo, [3]uint32{uint32(wgs * 64), 1, 1},
			[3]uint16{64, 1, 1}, &EmptyArgs{Sink: sink})
		b.driver.DrainCommandQueue(q)
	}
	start := now()
	launch()
	oneKernel := now() - start
	start = now()
	for i := 0; i < 10; i++ {
		launch()
	}
	b.emit(row{"pattern": name, "k": ngpm, "wgs": wgs, "cycles": oneKernel,
		"latency_ns": (now() - start) / 10, "iters": 10})
}
