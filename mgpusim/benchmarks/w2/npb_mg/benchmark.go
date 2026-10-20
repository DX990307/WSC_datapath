// Package npbmg is the W2 port of gpu_benchmarks tier2/npb_mg (NAS MG from
// NPB-GPU HIP/MG): periodic 3-D multigrid V-cycles, all levels of u and r in
// one buffer each, kernels per upstream stage order (resid, psinv, rprj3,
// interp, the three periodic ghost exchanges, zero, norm).
//
// Deviations: fp32 instead of fp64; the V-cycle count is a flag (default 1
// instead of the class's 4/20); the class is a flag (S, W, A, B, C); norm
// partials are finished on the host after the run (upstream copies them
// back right away, outside the timed region). Coarse levels have few WGs
// (inherent to multigrid).
package npbmg

import (
	// embed hsaco files
	_ "embed"
	"fmt"
	"log"
	"math"

	"github.com/sarchlab/mgpusim/v3/driver"
	"github.com/sarchlab/mgpusim/v3/insts"
	"github.com/sarchlab/mgpusim/v3/kernels"
)

//go:embed kernels.hsaco
var hsacoBytes []byte

// Hidden is the hidden kernel argument tail.
type Hidden struct {
	HiddenGlobalOffsetX int64
	HiddenGlobalOffsetY int64
	HiddenGlobalOffsetZ int64
}

// ResidArgs are the arguments of mg_resid.
type ResidArgs struct {
	U, V, R    driver.Ptr
	N, BS      int32
	A0, A2, A3 float32
	Pad        int32
	Hidden
}

// PsinvArgs are the arguments of mg_psinv.
type PsinvArgs struct {
	R, U       driver.Ptr
	N, BS      int32
	C0, C1, C2 float32
	Pad        int32
	Hidden
}

// RprjArgs are the arguments of mg_rprj3.
type RprjArgs struct {
	R, S       driver.Ptr
	M, MJ, One int32
	Pad        int32
	Hidden
}

// InterpArgs are the arguments of mg_interp.
type InterpArgs struct {
	Z, U       driver.Ptr
	MM, N, One int32
	Pad        int32
	Hidden
}

// CommArgs are the arguments of mg_comm3_*.
type CommArgs struct {
	U   driver.Ptr
	N   int32
	Pad int32
	Hidden
}

// ZeroArgs are the arguments of mg_zero.
type ZeroArgs struct {
	U     driver.Ptr
	Count int32
	Pad   int32
	Hidden
}

// NormArgs are the arguments of mg_norm.
type NormArgs struct {
	R, Partial driver.Ptr
	N          int32
	Pad        int32
	Hidden
}

// Params are the size knobs.
type Params struct {
	Class  byte
	Cycles int
	Block  int // max WG size of resid/psinv (upstream 256)
}

// DefaultParams is a small smoke size.
func DefaultParams() Params { return Params{Class: 'S', Cycles: 1, Block: 256} }

// Benchmark is the MG benchmark.
type Benchmark struct {
	driver  *driver.Driver
	context *driver.Context
	gpus    []int

	k      map[string]*insts.HsaCo
	Params Params

	layout             Layout
	dU, dR, dV, dNorm0 driver.Ptr
	dNorm1             driver.Ptr
	launches, wgs      map[string]int
	order              []string
}

var kernelNames = []string{"mg_resid", "mg_psinv", "mg_rprj3", "mg_interp", "mg_comm3_1",
	"mg_comm3_2", "mg_comm3_3", "mg_zero", "mg_norm"}

// NewBenchmark returns the benchmark with the default size.
func NewBenchmark(d *driver.Driver) *Benchmark {
	b := &Benchmark{driver: d, Params: DefaultParams(), k: map[string]*insts.HsaCo{}}
	b.context = d.Init()
	for _, n := range kernelNames {
		b.k[n] = kernels.LoadProgramFromMemory(hsacoBytes, n)
		if b.k[n] == nil {
			log.Panicf("npb_mg: failed to load %s", n)
		}
	}
	return b
}

// SelectGPU selects the GPU.
func (b *Benchmark) SelectGPU(gpus []int) { b.gpus = gpus }

// SetUnifiedMemory is not supported.
func (b *Benchmark) SetUnifiedMemory() { panic("npb_mg does not support unified memory") }

func (b *Benchmark) alloc(bytes int, name string) driver.Ptr {
	p := b.driver.AllocateMemory(b.context, uint64(bytes))
	b.driver.RegisterMemoryObject(b.context, p, uint64(bytes), name)
	return p
}

func (b *Benchmark) launch(q *driver.CommandQueue, name string, grid [3]uint32, wg [3]uint16, args interface{}) {
	b.driver.EnqueueLaunchKernel(q, b.k[name], grid, wg, args)
	if b.launches[name] == 0 {
		b.order = append(b.order, name)
	}
	b.launches[name]++
	b.wgs[name] += int(grid[0]/uint32(wg[0])) * int(grid[1]/uint32(wg[1]))
}

func f32s(v []float64) []float32 {
	o := make([]float32, len(v))
	for i, x := range v {
		o[i] = float32(x)
	}
	return o
}

// Run uploads the input and launches the V-cycles.
func (b *Benchmark) Run() {
	pr := b.Params
	c, ok := Classes[pr.Class]
	if !ok || pr.Cycles < 1 || pr.Block < 64 || pr.Block > 1024 {
		log.Panicf("npb_mg: bad params %+v", pr)
	}
	b.driver.SelectGPU(b.context, b.gpus[0])
	b.driver.SetMagicCopyCoherent(true)
	b.launches, b.wgs = map[string]int{}, map[string]int{}
	l := NewLayout(c.N)
	b.layout = l
	top := l.Levels() - 1
	nt := l.Width[top]
	b.dU = b.alloc(4*l.Cells, "mg_u")
	b.dR = b.alloc(4*l.Cells, "mg_r")
	b.dV = b.alloc(4*nt*nt*nt, "mg_v")
	lines := c.N * c.N
	b.dNorm0 = b.alloc(8*lines, "mg_norm_initial")
	b.dNorm1 = b.alloc(8*lines, "mg_norm_final")
	zeros := make([]float32, l.Cells)
	b.driver.MemCopyH2D(b.context, b.dU, zeros)
	b.driver.MemCopyH2D(b.context, b.dR, zeros)
	b.driver.MemCopyH2D(b.context, b.dV, f32s(Input(c.N)))
	q := b.driver.CreateCommandQueue(b.context)

	a, sm := OperatorA, Smoother(c)
	up := func(k int) driver.Ptr { return b.dU + driver.Ptr(4*l.Offset[k]) }
	rp := func(k int) driver.Ptr { return b.dR + driver.Ptr(4*l.Offset[k]) }
	bsOf := func(p int) int { return min(p, pr.Block) }
	comm := func(u driver.Ptr, p int) {
		gx := func(w int) uint32 { return uint32((w + 63) / 64 * 64) }
		b.launch(q, "mg_comm3_1", [3]uint32{gx(p - 2), uint32(p - 2), 1}, [3]uint16{64, 1, 1}, &CommArgs{U: u, N: int32(p)})
		b.launch(q, "mg_comm3_2", [3]uint32{gx(p), uint32(p - 2), 1}, [3]uint16{64, 1, 1}, &CommArgs{U: u, N: int32(p)})
		b.launch(q, "mg_comm3_3", [3]uint32{gx(p), uint32(p), 1}, [3]uint16{64, 1, 1}, &CommArgs{U: u, N: int32(p)})
	}
	resid := func(k int, rhs driver.Ptr) {
		p := l.Width[k]
		bs := bsOf(p)
		b.launch(q, "mg_resid", [3]uint32{uint32((p - 2) * bs), uint32(p - 2), 1}, [3]uint16{uint16(bs), 1, 1},
			&ResidArgs{U: up(k), V: rhs, R: rp(k), N: int32(p), BS: int32(bs),
				A0: float32(a[0]), A2: float32(a[2]), A3: float32(a[3])})
		comm(rp(k), p)
	}
	smooth := func(k int) {
		p := l.Width[k]
		bs := bsOf(p)
		b.launch(q, "mg_psinv", [3]uint32{uint32((p - 2) * bs), uint32(p - 2), 1}, [3]uint16{uint16(bs), 1, 1},
			&PsinvArgs{R: rp(k), U: up(k), N: int32(p), BS: int32(bs),
				C0: float32(sm[0]), C1: float32(sm[1]), C2: float32(sm[2])})
		comm(up(k), p)
	}
	restrict := func(k int) {
		f, p := l.Width[k], l.Width[k-1]
		b.launch(q, "mg_rprj3", [3]uint32{uint32((p - 2) * (p - 1)), uint32(p - 2), 1}, [3]uint16{uint16(p - 1), 1, 1},
			&RprjArgs{R: rp(k), S: rp(k - 1), M: int32(f), MJ: int32(p), One: 1})
		comm(rp(k-1), p)
	}
	interpolate := func(k int) {
		p, qq := l.Width[k], l.Width[k-1]
		b.launch(q, "mg_interp", [3]uint32{uint32((qq - 1) * qq), uint32(qq - 1), 1}, [3]uint16{uint16(qq), 1, 1},
			&InterpArgs{Z: up(k - 1), U: up(k), MM: int32(qq), N: int32(p), One: 1})
	}
	zero := func(k int) {
		cnt := l.Width[k] * l.Width[k] * l.Width[k]
		b.launch(q, "mg_zero", [3]uint32{uint32((cnt + 255) / 256 * 256), 1, 1}, [3]uint16{256, 1, 1},
			&ZeroArgs{U: up(k), Count: int32(cnt)})
	}
	norm := func(dst driver.Ptr) {
		b.launch(q, "mg_norm", [3]uint32{uint32(c.N * 64), uint32(c.N), 1}, [3]uint16{64, 1, 1},
			&NormArgs{R: rp(top), Partial: dst, N: int32(nt)})
	}

	resid(top, b.dV)
	norm(b.dNorm0)
	for it := 0; it < pr.Cycles; it++ {
		for k := top; k > 0; k-- {
			restrict(k)
		}
		zero(0)
		smooth(0)
		for k := 1; k < top; k++ {
			zero(k)
			interpolate(k)
			resid(k, rp(k))
			smooth(k)
		}
		interpolate(top)
		resid(top, b.dV)
		smooth(top)
		resid(top, b.dV)
	}
	norm(b.dNorm1)
	total, totalWG := 0, 0
	var per string
	for _, n := range b.order {
		total += b.launches[n]
		totalWG += b.wgs[n]
		per += fmt.Sprintf("%s:%d/%d;", n, b.launches[n], b.wgs[n])
	}
	fmt.Printf("MG,config,class=%c,n=%d,levels=%d,cycles=%d,kernels=%d,total_wgs=%d,hierarchy_mb=%.1f\n",
		c.Name, c.N, l.Levels(), pr.Cycles, total, totalWG, float64(8*l.Cells+4*nt*nt*nt)/1e6)
	fmt.Printf("MG,kernel_launches/wgs,%s\n", per)
	b.driver.DrainCommandQueue(q)
}

func (b *Benchmark) finishNorm(p driver.Ptr, n int) (float64, float64) {
	lines := n * n
	part := make([]float32, 2*lines)
	b.driver.MemCopyD2H(b.context, part, p)
	s, mx := 0.0, 0.0
	for i := 0; i < lines; i++ {
		s += float64(part[i])
		mx = math.Max(mx, float64(part[lines+i]))
	}
	return math.Sqrt(s / float64(n*n*n)), mx
}

// Verify compares the finest u and r and the norms with the float64
// reference (fp32 tolerance).
func (b *Benchmark) Verify() {
	pr := b.Params
	c := Classes[pr.Class]
	ref := Reference(c, pr.Cycles)
	l := b.layout
	top := l.Levels() - 1
	w := l.Width[top]
	cnt := w * w * w
	got := make([]float32, l.Cells)
	bad := 0
	worst := 0.0
	for _, buf := range []struct {
		name string
		p    driver.Ptr
		want []float64
	}{{"u", b.dU, ref.U}, {"r", b.dR, ref.R}} {
		b.driver.MemCopyD2H(b.context, got, buf.p)
		scale := 0.0
		for _, v := range buf.want[l.Offset[top] : l.Offset[top]+cnt] {
			scale = math.Max(scale, math.Abs(v))
		}
		for i := l.Offset[top]; i < l.Offset[top]+cnt; i++ {
			e := math.Abs(float64(got[i])-buf.want[i]) / math.Max(scale, 1e-30)
			worst = math.Max(worst, e)
			if !(e <= 1e-3) {
				if bad < 5 {
					log.Printf("npb_mg: %s[%d] got %g want %g", buf.name, i, got[i], buf.want[i])
				}
				bad++
			}
		}
	}
	l20, _ := b.finishNorm(b.dNorm0, c.N)
	l21, linf := b.finishNorm(b.dNorm1, c.N)
	relL2 := math.Abs(l21-ref.Norms[2]) / ref.Norms[2]
	fmt.Printf("MG,verify,elements=%d,max_rel_err=%.3g,mismatches=%d,initial_l2=%.6e(ref %.6e),final_l2=%.6e(ref %.6e,rel %.2g),final_linf=%.6e\n",
		2*cnt, worst, bad, l20, ref.Norms[0], l21, ref.Norms[2], relL2, linf)
	if pr.Cycles == c.Iterations {
		fmt.Printf("MG,verify,published_l2=%.13e,rel=%.3g\n", c.L2, math.Abs(l21-c.L2)/c.L2)
	}
	if bad > 0 || !(relL2 < 1e-3) {
		log.Fatalf("npb_mg: %d mismatches, final L2 rel error %g", bad, relL2)
	}
	log.Printf("Passed!")
}
