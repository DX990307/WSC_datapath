// Package embbag is the W2 DLRM embedding-bag workload (inference, sum
// pooling), after gpu_benchmarks tier5/dlrm_inference (torch EmbeddingBag,
// mode='sum') and the T17 W-D spec: Tables tables of Rows x 128 fp32 rows,
// Batch samples, Pooling lookups per (sample, table), row popularity Zipf
// (Alpha) with hot rows scattered by a per-table permutation.
//
// Only the embedding stage is simulated (the MLPs and the interaction are
// local GEMMs). Deviations from upstream: power-law instead of uniform
// indices, fixed pooling (no offsets array), pooling 20 instead of 10.
//
// Kernel (kernels.cl, pull, no atomics): embbag_sum, one work-group per two
// bags, Batch*Tables/2 work-groups.
package embbag

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

// HiddenArgs are the hidden kernel arguments.
type HiddenArgs struct {
	HiddenGlobalOffsetX int64
	HiddenGlobalOffsetY int64
	HiddenGlobalOffsetZ int64
}

// KernelArgs are the arguments of embbag_sum.
type KernelArgs struct {
	Emb, Idx, Out                     driver.Ptr
	NumTables, Rows, Pooling, NumBags int32
	HiddenArgs
}

// DefaultParams is a small smoke size (1664 work-groups).
func DefaultParams() Params {
	return Params{Tables: 26, Rows: 4096, Batch: 128, Pooling: 20, Alpha: 1.05, Seed: 1}
}

// Benchmark is the embedding-bag benchmark.
type Benchmark struct {
	driver  *driver.Driver
	context *driver.Context
	gpus    []int
	kernel  *insts.HsaCo

	Params Params
	prob   *Problem

	dEmb, dIdx, dOut driver.Ptr
}

// NewBenchmark returns an embedding-bag benchmark.
func NewBenchmark(d *driver.Driver) *Benchmark {
	b := &Benchmark{driver: d, Params: DefaultParams()}
	b.context = d.Init()
	b.kernel = kernels.LoadProgramFromMemory(hsacoBytes, "embbag_sum")
	if b.kernel == nil {
		log.Panic("embbag: failed to load kernel")
	}
	return b
}

// SelectGPU selects the GPU.
func (b *Benchmark) SelectGPU(gpus []int) { b.gpus = gpus }

// SetUnifiedMemory is not supported.
func (b *Benchmark) SetUnifiedMemory() { panic("embbag does not support unified memory") }

// EnableVerification is a no-op (Verify recomputes the reference).
func (b *Benchmark) EnableVerification() {}

func (b *Benchmark) alloc(bytes uint64, name string) driver.Ptr {
	p := b.driver.AllocateMemory(b.context, bytes)
	b.driver.RegisterMemoryObject(b.context, p, bytes, name)
	return p
}

// Run generates the input, uploads it and launches the kernel.
func (b *Benchmark) Run() {
	if err := b.Params.Validate(); err != nil {
		log.Panic(err)
	}
	b.driver.SelectGPU(b.context, b.gpus[0])
	b.driver.SetMagicCopyCoherent(true)
	p := Generate(b.Params)
	b.prob = p
	nb := p.NumBags()
	b.dEmb = b.alloc(uint64(len(p.Emb))*4, "embbag_tables")
	b.dIdx = b.alloc(uint64(len(p.Idx))*4, "embbag_idx")
	b.dOut = b.alloc(uint64(nb*Dim)*4, "embbag_out")
	b.driver.MemCopyH2D(b.context, b.dEmb, p.Emb)
	b.driver.MemCopyH2D(b.context, b.dIdx, p.Idx)

	wgs := (nb + 1) / 2
	distinct := p.DistinctRows()
	fmt.Printf("EMBBAG,config,tables=%d,rows=%d,dim=%d,batch=%d,pooling=%d,alpha=%g,seed=%d\n",
		p.Tables, p.Rows, Dim, p.Batch, p.Pooling, p.Alpha, p.Seed)
	fmt.Printf("EMBBAG,kernels,count=1,wgs=%d\n", wgs)
	fmt.Printf("EMBBAG,footprint,table_mb=%.1f,lookups=%d,distinct_rows=%d,distinct_row_mb=%.1f,"+
		"gather_mb=%.1f\n", float64(len(p.Emb))*4/1e6, len(p.Idx), distinct,
		float64(distinct)*Dim*4/1e6, float64(len(p.Idx))*Dim*4/1e6)

	q := b.driver.CreateCommandQueue(b.context)
	args := KernelArgs{Emb: b.dEmb, Idx: b.dIdx, Out: b.dOut, NumTables: int32(p.Tables),
		Rows: int32(p.Rows), Pooling: int32(p.Pooling), NumBags: int32(nb)}
	b.driver.EnqueueLaunchKernel(q, b.kernel, [3]uint32{uint32(wgs * 256), 1, 1},
		[3]uint16{256, 1, 1}, &args)
	b.driver.DrainCommandQueue(q)
}

// Verify compares with the CPU reference (1e-5 abs + 1e-4 rel).
func (b *Benchmark) Verify() {
	p := b.prob
	ref := p.Reference()
	got := make([]float32, len(ref))
	b.driver.MemCopyD2H(b.context, got, b.dOut)
	bad, maxErr := 0, 0.0
	for i := range ref {
		e := math.Abs(float64(got[i]) - float64(ref[i]))
		maxErr = math.Max(maxErr, e)
		if !(e <= 1e-5+1e-4*math.Abs(float64(ref[i]))) {
			if bad < 5 {
				log.Printf("embbag: mismatch at %d: got %g want %g", i, got[i], ref[i])
			}
			bad++
		}
	}
	fmt.Printf("EMBBAG,verify,elements=%d,max_abs_err=%.3g,mismatches=%d\n", len(ref), maxErr, bad)
	if bad > 0 {
		log.Fatalf("embbag: verification failed")
	}
	log.Printf("Passed!")
}
