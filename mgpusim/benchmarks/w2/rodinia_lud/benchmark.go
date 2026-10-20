// Package rodinialud is the W2 port of gpu_benchmarks tier2/rodinia_lud:
// Rodinia's blocked LU decomposition (no pivoting), 16x16 tiles in LDS.
// For every block step k: lud_diagonal (1 WG), lud_perimeter (2(nb-k-1)
// WGs), lud_internal ((nb-k-1)^2 WGs). The k-th row/column panel is read
// by every WG of the step (an incast whose owner moves with k).
//
// Deviations: the input matrix uses Go's math/rand (seed 42) instead of C
// rand(); values (rand%10+1)*0.1, diagonal = row sum + 1, as upstream.
// lud_diagonal is a single WG per step (inherent to the algorithm), and the
// last steps have few WGs.
package rodinialud

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

// BSize is the tile size.
const BSize = 16

// Args are the arguments of lud_diagonal and lud_internal.
type Args struct {
	A                   driver.Ptr
	N, Offset           int32
	HiddenGlobalOffsetX int64
	HiddenGlobalOffsetY int64
	HiddenGlobalOffsetZ int64
}

// PeriArgs are the arguments of lud_perimeter.
type PeriArgs struct {
	A                   driver.Ptr
	N, Offset, Half     int32
	Pad                 int32
	HiddenGlobalOffsetX int64
	HiddenGlobalOffsetY int64
	HiddenGlobalOffsetZ int64
}

// Params are the size knobs.
type Params struct {
	N    int
	Seed int64
}

// DefaultParams is a small smoke size.
func DefaultParams() Params { return Params{N: 256, Seed: 42} }

// Benchmark is the LUD benchmark.
type Benchmark struct {
	driver  *driver.Driver
	context *driver.Context
	gpus    []int

	diag, peri, internal *insts.HsaCo
	Params               Params

	orig []float32
	dA   driver.Ptr
}

// NewBenchmark returns the benchmark with the default size.
func NewBenchmark(d *driver.Driver) *Benchmark {
	b := &Benchmark{driver: d, Params: DefaultParams()}
	b.context = d.Init()
	b.diag = kernels.LoadProgramFromMemory(hsacoBytes, "lud_diagonal")
	b.peri = kernels.LoadProgramFromMemory(hsacoBytes, "lud_perimeter")
	b.internal = kernels.LoadProgramFromMemory(hsacoBytes, "lud_internal")
	if b.diag == nil || b.peri == nil || b.internal == nil {
		log.Panic("rodinia_lud: failed to load kernels")
	}
	return b
}

// SelectGPU selects the GPU.
func (b *Benchmark) SelectGPU(gpus []int) { b.gpus = gpus }

// SetUnifiedMemory is not supported.
func (b *Benchmark) SetUnifiedMemory() { panic("rodinia_lud does not support unified memory") }

// Matrix returns the diagonally dominant input (row-major n x n).
func Matrix(n int, seed int64) []float32 {
	rng := rand.New(rand.NewSource(seed))
	a := make([]float32, n*n)
	for i := 0; i < n; i++ {
		var rowSum float32
		for j := 0; j < n; j++ {
			a[i*n+j] = float32(rng.Intn(10)+1) * 0.1
			if i != j {
				rowSum += a[i*n+j]
			}
		}
		a[i*n+i] = rowSum + 1
	}
	return a
}

// Run uploads the matrix and launches every block step.
func (b *Benchmark) Run() {
	n := b.Params.N
	if n < BSize || n%BSize != 0 || n > 46340 {
		log.Panicf("rodinia_lud: N=%d must be a multiple of %d (<= 46340)", n, BSize)
	}
	b.driver.SelectGPU(b.context, b.gpus[0])
	b.driver.SetMagicCopyCoherent(true)
	b.orig = Matrix(n, b.Params.Seed)
	bytes := uint64(4 * n * n)
	b.dA = b.driver.AllocateMemory(b.context, bytes)
	b.driver.RegisterMemoryObject(b.context, b.dA, bytes, "lud_a")
	b.driver.MemCopyH2D(b.context, b.dA, b.orig)
	q := b.driver.CreateCommandQueue(b.context)
	nb := n / BSize
	wg := [3]uint16{BSize, BSize, 1}
	launches, wgs := 0, 0
	for k := 0; k < nb; k++ {
		b.driver.EnqueueLaunchKernel(q, b.diag, [3]uint32{BSize, BSize, 1}, wg,
			&Args{A: b.dA, N: int32(n), Offset: int32(k)})
		launches++
		wgs++
		half := nb - k - 1
		if half > 0 {
			b.driver.EnqueueLaunchKernel(q, b.peri, [3]uint32{uint32(2 * half * BSize), BSize, 1}, wg,
				&PeriArgs{A: b.dA, N: int32(n), Offset: int32(k), Half: int32(half)})
			b.driver.EnqueueLaunchKernel(q, b.internal,
				[3]uint32{uint32(half * BSize), uint32(half * BSize), 1}, wg,
				&Args{A: b.dA, N: int32(n), Offset: int32(k)})
			launches += 2
			wgs += 2*half + half*half
		}
	}
	fmt.Printf("LUD,config,n=%d,block_steps=%d,kernels=%d,total_wgs=%d,first_internal_wgs=%d,matrix_mb=%.1f\n",
		n, nb, launches, wgs, (nb-1)*(nb-1), float64(bytes)/1e6)
	b.driver.DrainCommandQueue(q)
}

// Residual returns ||A - LU||_F / ||A||_F for the in-place factor lu.
func Residual(lu, a []float32, n int) float64 {
	var err, norm float64
	row := make([]float64, n)
	for i := 0; i < n; i++ {
		for j := range row {
			row[j] = 0
		}
		for k := 0; k <= i; k++ {
			l := 1.0
			if k < i {
				l = float64(lu[i*n+k])
			}
			if l == 0 {
				continue
			}
			for j := k; j < n; j++ {
				row[j] += l * float64(lu[k*n+j])
			}
		}
		for j := 0; j < n; j++ {
			d := float64(a[i*n+j]) - row[j]
			err += d * d
			norm += float64(a[i*n+j]) * float64(a[i*n+j])
		}
	}
	return math.Sqrt(err / norm)
}

// Verify checks the residual (upstream criterion < 1e-4).
func (b *Benchmark) Verify() {
	n := b.Params.N
	lu := make([]float32, n*n)
	b.driver.MemCopyD2H(b.context, lu, b.dA)
	r := Residual(lu, b.orig, n)
	fmt.Printf("LUD,verify,n=%d,relative_residual=%.3e\n", n, r)
	if !(r < 1e-4) {
		log.Fatalf("rodinia_lud: residual %g", r)
	}
	log.Printf("Passed!")
}
