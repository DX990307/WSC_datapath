// Package rgat holds the T17 W-C step-0 minimal kernels (R-GAT style
// sampled aggregation, MLPerf Inference RGAT) and a tiny harness that runs
// them on a random sampled subgraph: rgat_proj projects the source features
// (h = x W), rgat_agg gathers each target's sampled neighbours, scores them
// (dot product with the attention vectors + LeakyReLU), applies the
// neighbour softmax and writes the weighted sum, once per relation, each
// relation adding to the previous relation's output (pull, no atomics).
//
// This is only the instruction check of T17 step 0: no real dataset, no
// multi-layer sampling. The workload itself is not started.
package rgat

import (
	// embed hsaco files
	_ "embed"
	"fmt"
	"log"
	"math"
	"math/rand"

	"github.com/sarchlab/mgpusim/v3/driver"
	"github.com/sarchlab/mgpusim/v3/insts"
	"github.com/sarchlab/mgpusim/v3/kernels"
)

//go:embed kernels.hsaco
var hsacoBytes []byte

// MaxDeg is the largest neighbour count rgat_agg handles (MAXDEG).
const MaxDeg = 32

// ProjArgs are the arguments of rgat_proj.
type ProjArgs struct {
	X, W, H             driver.Ptr
	FIn, FOut           int32
	HiddenGlobalOffsetX int64
	HiddenGlobalOffsetY int64
	HiddenGlobalOffsetZ int64
}

// AggArgs are the arguments of rgat_agg.
type AggArgs struct {
	HSrc, HDst, RowPtr, ColIdx, AttnL, AttnR driver.Ptr
	KernargGap, GapPadding                   int32 // unused, see kernels.cl
	AccIn, Out                               driver.Ptr
	F, UseAcc                                int32
	HiddenGlobalOffsetX                      int64
	HiddenGlobalOffsetY                      int64
	HiddenGlobalOffsetZ                      int64
}

// Benchmark is the minimal R-GAT aggregation harness.
type Benchmark struct {
	driver  *driver.Driver
	context *driver.Context
	gpus    []int

	proj, agg *insts.HsaCo

	NumSrc, NumDst, Fanout, F, Relations int
	Seed                                 int64

	x, w, al, ar []float32 // x [NumSrc][F], w [Rel][F][F], a [Rel][F]
	rowPtr       [][]int32 // per relation
	colIdx       [][]int32
	dOut         []driver.Ptr
}

// NewBenchmark returns the harness with a tiny default size.
func NewBenchmark(d *driver.Driver) *Benchmark {
	b := &Benchmark{driver: d, NumSrc: 256, NumDst: 64, Fanout: 10, F: 128, Relations: 2, Seed: 1}
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

func (b *Benchmark) alloc(n int, name string) driver.Ptr {
	p := b.driver.AllocateMemory(b.context, uint64(n*4))
	b.driver.RegisterMemoryObject(b.context, p, uint64(n*4), name)
	return p
}

func (b *Benchmark) generate() {
	if b.Fanout > MaxDeg {
		panic("rgat: fanout above MAXDEG")
	}
	rng := rand.New(rand.NewSource(b.Seed))
	F := b.F
	b.x = make([]float32, b.NumSrc*F)
	for i := range b.x {
		b.x[i] = rng.Float32()*2 - 1
	}
	b.w = make([]float32, b.Relations*F*F)
	for i := range b.w {
		b.w[i] = (rng.Float32()*2 - 1) / float32(math.Sqrt(float64(F)))
	}
	b.al = make([]float32, b.Relations*F)
	b.ar = make([]float32, b.Relations*F)
	for i := range b.al {
		b.al[i] = (rng.Float32()*2 - 1) / float32(math.Sqrt(float64(F)))
		b.ar[i] = (rng.Float32()*2 - 1) / float32(math.Sqrt(float64(F)))
	}
	// host-side sampling: each target gets up to Fanout distinct sources
	// (targets are the first NumDst source nodes, as in a mini-batch)
	for r := 0; r < b.Relations; r++ {
		rp := []int32{0}
		var ci []int32
		for i := 0; i < b.NumDst; i++ {
			deg := 1 + rng.Intn(b.Fanout)
			for _, j := range rng.Perm(b.NumSrc)[:deg] {
				ci = append(ci, int32(j))
			}
			rp = append(rp, int32(len(ci)))
		}
		b.rowPtr = append(b.rowPtr, rp)
		b.colIdx = append(b.colIdx, ci)
	}
}

// Run projects the features per relation and aggregates relation by
// relation.
func (b *Benchmark) Run() {
	b.driver.SelectGPU(b.context, b.gpus[0])
	// Verify copies results back; make the magic D2H copy read through the
	// write-back L2 (no simulated time; does not affect the kernels).
	b.driver.SetMagicCopyCoherent(true)
	b.generate()
	F := b.F
	q := b.driver.CreateCommandQueue(b.context)
	dX := b.alloc(len(b.x), "rgat_x")
	b.driver.MemCopyH2D(b.context, dX, b.x)
	var prev driver.Ptr
	wgs := 0
	for r := 0; r < b.Relations; r++ {
		dW := b.alloc(F*F, "rgat_w")
		dAL := b.alloc(F, "rgat_attn_l")
		dAR := b.alloc(F, "rgat_attn_r")
		dRP := b.alloc(len(b.rowPtr[r]), "rgat_row_ptr")
		dCI := b.alloc(len(b.colIdx[r]), "rgat_col_idx")
		dH := b.alloc(b.NumSrc*F, "rgat_h")
		dOut := b.alloc(b.NumDst*F, "rgat_out")
		b.driver.MemCopyH2D(b.context, dW, b.w[r*F*F:(r+1)*F*F])
		b.driver.MemCopyH2D(b.context, dAL, b.al[r*F:(r+1)*F])
		b.driver.MemCopyH2D(b.context, dAR, b.ar[r*F:(r+1)*F])
		b.driver.MemCopyH2D(b.context, dRP, b.rowPtr[r])
		b.driver.MemCopyH2D(b.context, dCI, b.colIdx[r])

		pa := ProjArgs{X: dX, W: dW, H: dH, FIn: int32(F), FOut: int32(F)}
		b.driver.EnqueueLaunchKernel(q, b.proj, [3]uint32{uint32(b.NumSrc * 256), 1, 1},
			[3]uint16{256, 1, 1}, &pa)
		aa := AggArgs{HSrc: dH, HDst: dH, RowPtr: dRP, ColIdx: dCI, AttnL: dAL, AttnR: dAR,
			AccIn: prev, Out: dOut, F: int32(F)}
		if r > 0 {
			aa.UseAcc = 1
		} else {
			aa.AccIn = dOut
		}
		b.driver.EnqueueLaunchKernel(q, b.agg, [3]uint32{uint32(b.NumDst * 256), 1, 1},
			[3]uint16{256, 1, 1}, &aa)
		wgs += b.NumSrc + b.NumDst
		prev = dOut
		b.dOut = append(b.dOut, dOut)
	}
	b.driver.DrainCommandQueue(q)
	fmt.Printf("RGAT,mini,src=%d,dst=%d,fanout=%d,f=%d,relations=%d,kernels=%d,wgs=%d\n",
		b.NumSrc, b.NumDst, b.Fanout, F, b.Relations, 2*b.Relations, wgs)
}

// Verify compares the last relation's output with a float64 reference.
func (b *Benchmark) Verify() {
	F := b.F
	acc := make([]float64, b.NumDst*F)
	for r := 0; r < b.Relations; r++ {
		h := make([]float64, b.NumSrc*F)
		for n := 0; n < b.NumSrc; n++ {
			for c := 0; c < F; c++ {
				s := 0.0
				for k := 0; k < F; k++ {
					s += float64(b.x[n*F+k]) * float64(b.w[r*F*F+k*F+c])
				}
				h[n*F+c] = s
			}
		}
		for i := 0; i < b.NumDst; i++ {
			er := 0.0
			for c := 0; c < F; c++ {
				er += h[i*F+c] * float64(b.ar[r*F+c])
			}
			beg, end := b.rowPtr[r][i], b.rowPtr[r][i+1]
			sc := make([]float64, end-beg)
			m := math.Inf(-1)
			for j := beg; j < end; j++ {
				el := 0.0
				src := int(b.colIdx[r][j])
				for c := 0; c < F; c++ {
					el += h[src*F+c] * float64(b.al[r*F+c])
				}
				e := el + er
				if e < 0 {
					e *= 0.2
				}
				sc[j-beg] = e
				m = math.Max(m, e)
			}
			sum := 0.0
			for k := range sc {
				sc[k] = math.Exp(sc[k] - m)
				sum += sc[k]
			}
			for j := beg; j < end; j++ {
				src := int(b.colIdx[r][j])
				for c := 0; c < F; c++ {
					acc[i*F+c] += sc[j-beg] / sum * h[src*F+c]
				}
			}
		}
	}
	got := make([]float32, b.NumDst*F)
	b.driver.MemCopyD2H(b.context, got, b.dOut[len(b.dOut)-1])
	maxErr, bad := 0.0, 0
	for i := range acc {
		e := math.Abs(float64(got[i]) - acc[i])
		maxErr = math.Max(maxErr, e)
		if !(e <= 1e-4+1e-3*math.Abs(acc[i])) {
			bad++
		}
	}
	fmt.Printf("RGAT,verify,elements=%d,max_abs_err=%.3g,mismatches=%d\n", len(acc), maxErr, bad)
	if bad > 0 {
		log.Fatalf("rgat: %d mismatches", bad)
	}
	log.Printf("Passed!")
}
