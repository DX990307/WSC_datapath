// Package atax is PolyBench ATAX (y = A^T (A x)), ported from
// gpu_benchmarks tier2/polybench_atax to OpenCL for the CuPath wafer (W2
// workload set). The row pass (A x) reads A row-wise (local); the column
// pass (A^T tmp) makes every GPM read every row of A (all-to-all), so the
// two kernels want opposite placements. Upstream's thread-per-row/column
// kernels are tiled to fill the wafer (see kernels.cl).
package atax

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

// K1Args are the arguments of atax_k1.
type K1Args struct {
	A, X, Tmp driver.Ptr
	NX, NY    int32
	HiddenArgs
}

// K2Args are the arguments of atax_k2.
type K2Args struct {
	A, Tmp, Part                 driver.Ptr
	NX, NY, RowsPerChunk, NChunk int32
	HiddenArgs
}

// K2RedArgs are the arguments of atax_k2red.
type K2RedArgs struct {
	Part, Y    driver.Ptr
	NY, NChunk int32
	HiddenArgs
}

// Benchmark is ATAX.
type Benchmark struct {
	driver  *driver.Driver
	context *driver.Context
	gpus    []int

	k1, k2, k2red *insts.HsaCo

	Params Params

	a, x                    []float32
	dA, dX, dTmp, dPart, dY driver.Ptr
}

// DefaultParams is a small smoke size (upstream default 4096^2; the
// candidate list suggests 8192^2).
func DefaultParams() Params { return Params{NX: 1024, NY: 1024, RowsPerChunk: 128, Seed: 42} }

// NewBenchmark returns the benchmark.
func NewBenchmark(d *driver.Driver) *Benchmark {
	b := &Benchmark{driver: d, Params: DefaultParams()}
	b.context = d.Init()
	b.k1 = kernels.LoadProgramFromMemory(hsacoBytes, "atax_k1")
	b.k2 = kernels.LoadProgramFromMemory(hsacoBytes, "atax_k2")
	b.k2red = kernels.LoadProgramFromMemory(hsacoBytes, "atax_k2red")
	for _, co := range []*insts.HsaCo{b.k1, b.k2, b.k2red} {
		if co == nil {
			log.Panic("atax: failed to load kernels")
		}
	}
	return b
}

// SelectGPU selects the GPU.
func (b *Benchmark) SelectGPU(gpus []int) { b.gpus = gpus }

// SetUnifiedMemory is not supported.
func (b *Benchmark) SetUnifiedMemory() { panic("atax does not support unified memory") }

func (b *Benchmark) alloc(bytes uint64, name string) driver.Ptr {
	p := b.driver.AllocateMemory(b.context, bytes)
	b.driver.RegisterMemoryObject(b.context, p, bytes, name)
	return p
}

// Run launches the three kernels.
func (b *Benchmark) Run() {
	p := b.Params
	if err := p.Validate(); err != nil {
		log.Panic(err)
	}
	b.driver.SelectGPU(b.context, b.gpus[0])
	b.a, b.x = Generate(p)
	nc, ncb := p.NumChunks(), p.NumColBlocks()
	b.dA = b.alloc(uint64(p.NX)*uint64(p.NY)*4, "atax_a")
	b.dX = b.alloc(uint64(p.NY)*4, "atax_x")
	b.dTmp = b.alloc(uint64(p.NX)*4, "atax_tmp")
	b.dPart = b.alloc(uint64(ncb*nc*WGSize)*4, "atax_part")
	b.dY = b.alloc(uint64(p.NY)*4, "atax_y")
	b.driver.MemCopyH2D(b.context, b.dA, b.a)
	b.driver.MemCopyH2D(b.context, b.dX, b.x)
	fmt.Printf("ATAX,config,nx=%d,ny=%d,rows_per_chunk=%d,k1_wgs=%d,k2_wgs=%d,k2red_wgs=%d,"+
		"a_mb=%.1f\n", p.NX, p.NY, p.RowsPerChunk, p.NX, ncb*nc, ncb,
		float64(p.NX)*float64(p.NY)*4/1e6)

	q := b.driver.CreateCommandQueue(b.context)
	b.launch(q, b.k1, p.NX, &K1Args{A: b.dA, X: b.dX, Tmp: b.dTmp, NX: int32(p.NX), NY: int32(p.NY)})
	b.launch(q, b.k2, ncb*nc, &K2Args{A: b.dA, Tmp: b.dTmp, Part: b.dPart, NX: int32(p.NX),
		NY: int32(p.NY), RowsPerChunk: int32(p.RowsPerChunk), NChunk: int32(nc)})
	b.launch(q, b.k2red, ncb, &K2RedArgs{Part: b.dPart, Y: b.dY, NY: int32(p.NY), NChunk: int32(nc)})
	b.driver.DrainCommandQueue(q)
}

func (b *Benchmark) launch(q *driver.CommandQueue, co *insts.HsaCo, wgs int, args interface{}) {
	b.driver.EnqueueLaunchKernel(q, co, [3]uint32{uint32(wgs * WGSize), 1, 1},
		[3]uint16{WGSize, 1, 1}, args)
}

// Verify compares y with the float64 reference (relative 1e-4).
func (b *Benchmark) Verify() {
	b.driver.SetMagicCopyCoherent(true)
	p := b.Params
	got := make([]float32, p.NY)
	b.driver.MemCopyD2H(b.context, got, b.dY)
	ref := Reference(b.a, b.x, p.NX, p.NY)
	bad, maxRel := 0, 0.0
	for j, r := range ref {
		rel := math.Abs(float64(got[j])-r) / math.Max(math.Abs(r), 1)
		maxRel = math.Max(maxRel, rel)
		if !(rel <= 1e-4) {
			if bad < 5 {
				log.Printf("atax: mismatch at %d: got %g want %g", j, got[j], r)
			}
			bad++
		}
	}
	fmt.Printf("ATAX,verify,ny=%d,max_rel_err=%.3g,mismatches=%d\n", p.NY, maxRel, bad)
	if bad > 0 {
		log.Fatalf("atax: verification failed")
	}
	log.Printf("Passed!")
}
