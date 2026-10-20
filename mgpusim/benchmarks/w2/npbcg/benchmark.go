// Package npbcg is the W2 port of gpu_benchmarks tier2/npb_cg (NAS CG
// inverse-power method, GMAP/NPB-GPU CUDA/CG) in fp32. The NAS matrix
// (makea) is built on the host; every CG step runs on the GPU, including
// the dot-product reductions (cg_dot partials + cg_reduce) and alpha/beta,
// so there is no host round trip between kernels. Default: one outer
// iteration (26 SpMVs) instead of NAS's 15/75. Registered as
// -benchmark=npbcg.
package npbcg

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

const (
	vecWG    = 64  // cg_init/dot/reduce/axpy/direction/normalize
	spmvWG   = 256 // 4 rows per WG
	spmvRows = 4
	zetaTol  = 1e-3 // fp32 vs fp64 relative zeta error
	vecTol   = 1e-2 // fp32 vs fp64 max |x| error relative to max |x|
)

type Hidden struct {
	HiddenGlobalOffsetX int64
	HiddenGlobalOffsetY int64
	HiddenGlobalOffsetZ int64
}

// InitArgs are the arguments of cg_init.
type InitArgs struct {
	X, Z, P, R driver.Ptr
	N, Pad     int32
	Hidden
}

// SpmvArgs are the arguments of cg_spmv.
type SpmvArgs struct {
	Offsets, Columns, Values, X, Y driver.Ptr
	N, Pad                         int32
	Hidden
}

// DotArgs are the arguments of cg_dot.
type DotArgs struct {
	A, B, Partial driver.Ptr
	N, Difference int32
	Hidden
}

// ReduceArgs are the arguments of cg_reduce.
type ReduceArgs struct {
	Partial, Out driver.Ptr
	NParts, Pad  int32
	Hidden
}

// AxpyArgs are the arguments of cg_axpy.
type AxpyArgs struct {
	Rho, PQ, P, Q, Z, R, ZOut, ROut driver.Ptr
	N, Pad                          int32
	Hidden
}

// DirectionArgs are the arguments of cg_direction.
type DirectionArgs struct {
	RhoNew, RhoOld, R, P, POut driver.Ptr
	N, Pad                     int32
	Hidden
}

// NormalizeArgs are the arguments of cg_normalize.
type NormalizeArgs struct {
	ZZ, Z, XOut driver.Ptr
	N, Pad      int32
	Hidden
}

// Params are the size knobs.
type Params struct {
	Class        string
	Outer, Inner int
}

// DefaultParams is class S, one outer iteration of 25 inner iterations.
func DefaultParams() Params { return Params{Class: "S", Outer: 1, Inner: 25} }

// Benchmark is the CG benchmark.
type Benchmark struct {
	driver  *driver.Driver
	context *driver.Context
	gpus    []int
	k       map[string]*insts.HsaCo
	Params  Params

	class   Class
	a       *Matrix
	q       *driver.CommandQueue
	dX      driver.Ptr
	dXZ     []driver.Ptr
	launchN map[string]int
	wgN     map[string]int
	allocs  int
}

// NewBenchmark returns the benchmark with the default size.
func NewBenchmark(d *driver.Driver) *Benchmark {
	b := &Benchmark{driver: d, Params: DefaultParams(), k: map[string]*insts.HsaCo{}}
	b.context = d.Init()
	for _, name := range []string{"cg_init", "cg_spmv", "cg_dot", "cg_reduce", "cg_axpy",
		"cg_direction", "cg_normalize"} {
		b.k[name] = kernels.LoadProgramFromMemory(hsacoBytes, name)
		if b.k[name] == nil {
			log.Panicf("npbcg: failed to load %s", name)
		}
	}
	return b
}

// SelectGPU selects the GPU.
func (b *Benchmark) SelectGPU(gpus []int) { b.gpus = gpus }

// SetUnifiedMemory is not supported.
func (b *Benchmark) SetUnifiedMemory() { panic("npbcg does not support unified memory") }

func (b *Benchmark) alloc(bytes int, name string) driver.Ptr {
	p := b.driver.AllocateMemory(b.context, uint64(bytes))
	b.driver.RegisterMemoryObject(b.context, p, uint64(bytes), name)
	b.allocs++
	return p
}

func (b *Benchmark) vec(name string) driver.Ptr { return b.alloc(4*b.class.N, name) }

func (b *Benchmark) launch(name string, wgs, wgSize int, args interface{}) {
	b.driver.EnqueueLaunchKernel(b.q, b.k[name], [3]uint32{uint32(wgs * wgSize), 1, 1},
		[3]uint16{uint16(wgSize), 1, 1}, args)
	b.launchN[name]++
	b.wgN[name] += wgs
}

func (b *Benchmark) dot(x, y driver.Ptr, diff bool, name string) driver.Ptr {
	n := b.class.N
	parts := (n + vecWG - 1) / vecWG
	partial := b.alloc(4*parts, name+"_partial")
	out := b.alloc(4, name)
	d := int32(0)
	if diff {
		d = 1
	}
	b.launch("cg_dot", parts, vecWG, &DotArgs{A: x, B: y, Partial: partial, N: int32(n), Difference: d})
	b.launch("cg_reduce", 1, vecWG, &ReduceArgs{Partial: partial, Out: out, NParts: int32(parts)})
	return out
}

// Run builds the matrix and launches the whole CG sequence.
func (b *Benchmark) Run() {
	b.driver.SelectGPU(b.context, b.gpus[0])
	b.driver.SetMagicCopyCoherent(true)
	c, ok := Classes[b.Params.Class]
	if !ok {
		log.Panicf("npbcg: unknown class %q (S/W/A/B/C)", b.Params.Class)
	}
	b.class = c
	b.a = MakeMatrix(c)
	b.launchN, b.wgN = map[string]int{}, map[string]int{}
	n := c.N
	vals := make([]float32, len(b.a.Values))
	for i, v := range b.a.Values {
		vals[i] = float32(v)
	}
	dOff := b.alloc(4*(n+1), "cg_offsets")
	b.driver.MemCopyH2D(b.context, dOff, b.a.Offsets)
	dCol := b.alloc(4*len(b.a.Columns), "cg_columns")
	b.driver.MemCopyH2D(b.context, dCol, b.a.Columns)
	dVal := b.alloc(4*len(vals), "cg_values")
	b.driver.MemCopyH2D(b.context, dVal, vals)
	ones := make([]float32, n)
	for i := range ones {
		ones[i] = 1
	}
	x := b.vec("cg_x_o0")
	b.driver.MemCopyH2D(b.context, x, ones)

	vw := (n + vecWG - 1) / vecWG
	sw := (n + spmvRows - 1) / spmvRows
	b.q = b.driver.CreateCommandQueue(b.context)
	for o := 0; o < b.Params.Outer; o++ {
		tag := func(s string, k int) string { return fmt.Sprintf("cg_%s_o%d_k%d", s, o, k) }
		z, p, r := b.vec(tag("z", 0)), b.vec(tag("p", 0)), b.vec(tag("r", 0))
		b.launch("cg_init", vw, vecWG, &InitArgs{X: x, Z: z, P: p, R: r, N: int32(n)})
		rho := b.dot(r, r, false, tag("rho", 0))
		for k := 0; k < b.Params.Inner; k++ {
			q := b.vec(tag("q", k))
			b.launch("cg_spmv", sw, spmvWG, &SpmvArgs{Offsets: dOff, Columns: dCol, Values: dVal,
				X: p, Y: q, N: int32(n)})
			pq := b.dot(p, q, false, tag("pq", k))
			z2, r2 := b.vec(tag("z", k+1)), b.vec(tag("r", k+1))
			b.launch("cg_axpy", vw, vecWG, &AxpyArgs{Rho: rho, PQ: pq, P: p, Q: q, Z: z, R: r,
				ZOut: z2, ROut: r2, N: int32(n)})
			rho2 := b.dot(r2, r2, false, tag("rho", k+1))
			p2 := b.vec(tag("p", k+1))
			b.launch("cg_direction", vw, vecWG, &DirectionArgs{RhoNew: rho2, RhoOld: rho, R: r2,
				P: p, POut: p2, N: int32(n)})
			z, r, p, rho = z2, r2, p2, rho2
		}
		ax := b.vec(fmt.Sprintf("cg_az_o%d", o))
		b.launch("cg_spmv", sw, spmvWG, &SpmvArgs{Offsets: dOff, Columns: dCol, Values: dVal,
			X: z, Y: ax, N: int32(n)})
		b.dot(x, ax, true, fmt.Sprintf("cg_resid_o%d", o))
		xz := b.dot(x, z, false, fmt.Sprintf("cg_xz_o%d", o))
		zz := b.dot(z, z, false, fmt.Sprintf("cg_zz_o%d", o))
		x2 := b.vec(fmt.Sprintf("cg_x_o%d", o+1))
		b.launch("cg_normalize", vw, vecWG, &NormalizeArgs{ZZ: zz, Z: z, XOut: x2, N: int32(n)})
		b.dXZ = append(b.dXZ, xz)
		x = x2
	}
	b.dX = x
	total, kernels := 0, 0
	var per string
	for _, name := range []string{"cg_init", "cg_spmv", "cg_dot", "cg_reduce", "cg_axpy",
		"cg_direction", "cg_normalize"} {
		total += b.wgN[name]
		kernels += b.launchN[name]
		per += fmt.Sprintf("%s:%dx%d;", name, b.launchN[name], b.wgN[name]/max(1, b.launchN[name]))
	}
	fmt.Printf("NPBCG,config,class=%s,n=%d,nnz=%d,outer=%d,inner=%d,kernels=%d,total_wgs=%d,"+
		"allocations=%d,matrix_mb=%.1f,spmv_traffic_mb=%.1f\n",
		c.Name, n, len(b.a.Values), b.Params.Outer, b.Params.Inner, kernels, total, b.allocs,
		float64(8*len(b.a.Values)+4*(n+1))/1e6,
		float64(b.launchN["cg_spmv"])*float64(12*len(b.a.Values))/1e6)
	fmt.Printf("NPBCG,kernel_launches_x_wgs,%s\n", per)
	b.driver.DrainCommandQueue(b.q)
}

func maxRelErr(got []float32, want []float64) float64 {
	m, e := 0.0, 0.0
	for i := range want {
		m = math.Max(m, math.Abs(want[i]))
		e = math.Max(e, math.Abs(float64(got[i])-want[i]))
	}
	return e / m
}

// hostFP32 runs the GPU's sequence in float32 on the host (dot products
// as per-64 partials summed in order; the tree order inside a partial
// differs slightly). Returns the final x and the last zeta.
func hostFP32(a *Matrix, c Class, outer, inner, part int) ([]float32, float64) {
	n := c.N
	vals := make([]float32, len(a.Values))
	for i, v := range a.Values {
		vals[i] = float32(v)
	}
	spmv := func(x, y []float32) {
		for row := 0; row < n; row++ {
			var s float32
			for k := a.Offsets[row]; k < a.Offsets[row+1]; k++ {
				s += vals[k] * x[a.Columns[k]]
			}
			y[row] = s
		}
	}
	dot := func(x, y []float32) float32 {
		var t float32
		for s := 0; s < n; s += part {
			var p float32
			for i := s; i < s+part && i < n; i++ {
				p += x[i] * y[i]
			}
			t += p
		}
		return t
	}
	x := make([]float32, n)
	for i := range x {
		x[i] = 1
	}
	z, p, q, r := make([]float32, n), make([]float32, n), make([]float32, n), make([]float32, n)
	zeta := 0.0
	for o := 0; o < outer; o++ {
		for i := range z {
			z[i] = 0
		}
		copy(r, x)
		copy(p, x)
		rho := dot(r, r)
		for k := 0; k < inner; k++ {
			spmv(p, q)
			alpha := rho / dot(p, q)
			for i := 0; i < n; i++ {
				z[i] += alpha * p[i]
				r[i] -= alpha * q[i]
			}
			rho2 := dot(r, r)
			beta := rho2 / rho
			for i := 0; i < n; i++ {
				p[i] = r[i] + beta*p[i]
			}
			rho = rho2
		}
		zeta = c.Shift + 1.0/float64(dot(x, z))
		inv := float32(1 / math.Sqrt(float64(dot(z, z))))
		for i := 0; i < n; i++ {
			x[i] = inv * z[i]
		}
	}
	return x, zeta
}

// Verify compares the final x and every outer zeta with the fp64
// reference (loose fp32 tolerances).
func (b *Benchmark) Verify() {
	c := b.class
	ref := Reference(b.a, c, b.Params.Outer, b.Params.Inner)
	x := make([]float32, c.N)
	b.driver.MemCopyD2H(b.context, x, b.dX)
	bad := 0
	for o, p := range b.dXZ {
		xz := make([]float32, 1)
		b.driver.MemCopyD2H(b.context, xz, p)
		zeta := c.Shift + 1/float64(xz[0])
		e := math.Abs(zeta-ref.Zeta[o]) / math.Abs(ref.Zeta[o])
		fmt.Printf("NPBCG,zeta,outer=%d,gpu=%.9g,ref=%.12g,rel_err=%.3g\n", o, zeta, ref.Zeta[o], e)
		if !(e <= zetaTol) {
			bad++
		}
	}
	e := maxRelErr(x, ref.X)
	fmt.Printf("NPBCG,verify,n=%d,x_max_rel_err=%.3g,zeta_mismatches=%d\n", c.N, e, bad)
	if bad > 0 || !(e <= vecTol) {
		log.Fatalf("npbcg: verification failed")
	}
	log.Printf("Passed!")
}
