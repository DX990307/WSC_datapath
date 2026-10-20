// Package fft2d is a 2D complex FFT (fp32, forward then inverse) with
// explicit global transposes, built on the SHOC FFT512 codelet
// (gpu_benchmarks tier2/shoc_fft, cufft_2d pattern) for the CuPath wafer
// (W2 workload set). The transposes are a regular all-to-all across the 48
// GPMs. See kernels.cl.
package fft2d

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

// FFT512Args are the arguments of fft2d_fft512.
type FFT512Args struct {
	Work    driver.Ptr
	LogN, M int32
	Dir     float32
	Pad     int32
	HiddenArgs
}

// RadixArgs are the arguments of fft2d_radixm.
type RadixArgs struct {
	In, Out    driver.Ptr
	LogN, M    int32
	Dir, Scale float32
	HiddenArgs
}

// TransposeArgs are the arguments of fft2d_transpose.
type TransposeArgs struct {
	In, Out driver.Ptr
	LogN    int32
	Pad     int32
	HiddenArgs
}

// Benchmark is the 2D FFT.
type Benchmark struct {
	driver  *driver.Driver
	context *driver.Context
	gpus    []int

	fft512, radix, transpose *insts.HsaCo

	Params Params

	verify  bool
	data    []float32
	fwd     []float32
	dA, dB  driver.Ptr
	inB     bool
	kernels int
	wgs     int
}

// DefaultParams is the smallest size (one 512-point codelet per row).
func DefaultParams() Params { return Params{N: 512, Seed: 42} }

// NewBenchmark returns the benchmark.
func NewBenchmark(d *driver.Driver) *Benchmark {
	b := &Benchmark{driver: d, Params: DefaultParams()}
	b.context = d.Init()
	b.fft512 = kernels.LoadProgramFromMemory(hsacoBytes, "fft2d_fft512")
	b.radix = kernels.LoadProgramFromMemory(hsacoBytes, "fft2d_radixm")
	b.transpose = kernels.LoadProgramFromMemory(hsacoBytes, "fft2d_transpose")
	for _, co := range []*insts.HsaCo{b.fft512, b.radix, b.transpose} {
		if co == nil {
			log.Panic("fft2d: failed to load kernels")
		}
	}
	return b
}

// SelectGPU selects the GPU.
func (b *Benchmark) SelectGPU(gpus []int) { b.gpus = gpus }

// SetUnifiedMemory is not supported.
func (b *Benchmark) SetUnifiedMemory() { panic("fft2d does not support unified memory") }

// EnableVerification keeps the forward result for Verify.
func (b *Benchmark) EnableVerification() { b.verify = true }

func (b *Benchmark) alloc(bytes uint64, name string) driver.Ptr {
	p := b.driver.AllocateMemory(b.context, bytes)
	b.driver.RegisterMemoryObject(b.context, p, bytes, name)
	return p
}

// Run does the forward 2D FFT then the inverse, in place in A (B is the
// transpose / radix scratch).
func (b *Benchmark) Run() {
	p := b.Params
	if err := p.Validate(); err != nil {
		log.Panic(err)
	}
	b.driver.SelectGPU(b.context, b.gpus[0])
	b.data = Generate(p)
	bytes := uint64(p.N) * uint64(p.N) * 8
	b.dA = b.alloc(bytes, "fft2d_a")
	b.dB = b.alloc(bytes, "fft2d_b")
	b.driver.MemCopyH2D(b.context, b.dA, b.data)
	q := b.driver.CreateCommandQueue(b.context)
	b.kernels, b.wgs, b.inB = 0, 0, false
	b.pass2D(q, -1)
	if b.verify {
		b.driver.DrainCommandQueue(q)
		b.driver.SetMagicCopyCoherent(true)
		b.fwd = make([]float32, 2*p.N*p.N)
		b.driver.MemCopyD2H(b.context, b.fwd, b.dA)
	}
	b.pass2D(q, 1)
	b.driver.DrainCommandQueue(q)
	radixWGs := 0
	if p.M() > 1 {
		radixWGs = p.N * 512 / 256
	}
	fmt.Printf("FFT2D,config,n=%d,m=%d,kernels=%d,total_wgs=%d,fft512_wgs=%d,radix_wgs=%d,"+
		"transpose_wgs=%d,matrix_mb=%.1f\n", p.N, p.M(), b.kernels, b.wgs, p.N*p.M(),
		radixWGs, (p.N/32)*(p.N/32), float64(bytes)/1e6)
}

// pass2D: rows, transpose, rows, transpose (result back in A).
func (b *Benchmark) pass2D(q *driver.CommandQueue, dir float32) {
	b.rows(q, dir)
	b.launch(q, b.transpose, (b.Params.N/32)*(b.Params.N/32), 256,
		&TransposeArgs{In: b.cur(), Out: b.other(), LogN: int32(b.Params.LogN())})
	b.swap()
	b.rows(q, dir)
	b.launch(q, b.transpose, (b.Params.N/32)*(b.Params.N/32), 256,
		&TransposeArgs{In: b.cur(), Out: b.other(), LogN: int32(b.Params.LogN())})
	b.swap()
	if b.inB {
		log.Panic("fft2d: result not in A")
	}
}

// rows applies the length-N FFT to every row of the current buffer; for
// m > 1 the result lands in the other buffer.
func (b *Benchmark) rows(q *driver.CommandQueue, dir float32) {
	p := b.Params
	m := p.M()
	b.launch(q, b.fft512, p.N*m, 64,
		&FFT512Args{Work: b.cur(), LogN: int32(p.LogN()), M: int32(m), Dir: dir})
	if m > 1 {
		scale := float32(1)
		if dir > 0 {
			scale = 1 / float32(m)
		}
		b.launch(q, b.radix, p.N*512/256, 256, &RadixArgs{In: b.cur(), Out: b.other(),
			LogN: int32(p.LogN()), M: int32(m), Dir: dir, Scale: scale})
		b.swap()
	}
}

func (b *Benchmark) cur() driver.Ptr {
	if b.inB {
		return b.dB
	}
	return b.dA
}

func (b *Benchmark) other() driver.Ptr {
	if b.inB {
		return b.dA
	}
	return b.dB
}

func (b *Benchmark) swap() { b.inB = !b.inB }

func (b *Benchmark) launch(q *driver.CommandQueue, co *insts.HsaCo, wgs, wgSize int, args interface{}) {
	b.kernels++
	b.wgs += wgs
	b.driver.EnqueueLaunchKernel(q, co, [3]uint32{uint32(wgs * wgSize), 1, 1},
		[3]uint16{uint16(wgSize), 1, 1}, args)
}

// Verify checks the forward result against a float64 2D FFT and the
// round trip against the input. Tolerance: max error <= 1e-3 * max |X|
// (forward) and 1e-4 (round trip, values in [-1, 1]).
func (b *Benchmark) Verify() {
	p := b.Params
	b.driver.SetMagicCopyCoherent(true)
	got := make([]float32, 2*p.N*p.N)
	b.driver.MemCopyD2H(b.context, got, b.dA)
	rt := 0.0
	for i := range got {
		rt = math.Max(rt, math.Abs(float64(got[i]-b.data[i])))
	}
	fwdErr, maxAbs := -1.0, 0.0
	if b.fwd != nil {
		ref := Reference2D(b.data, p.N)
		fwdErr = 0
		for i, r := range ref {
			maxAbs = math.Max(maxAbs, math.Hypot(real(r), imag(r)))
			e := math.Hypot(float64(b.fwd[2*i])-real(r), float64(b.fwd[2*i+1])-imag(r))
			fwdErr = math.Max(fwdErr, e)
		}
	}
	fmt.Printf("FFT2D,verify,n=%d,forward_max_err=%.3g,forward_max_abs=%.3g,roundtrip_max_err=%.3g\n",
		p.N, fwdErr, maxAbs, rt)
	if !(rt <= 1e-4) || !(fwdErr >= 0 && fwdErr <= 1e-3*maxAbs) {
		log.Fatalf("fft2d: verification failed")
	}
	log.Printf("Passed!")
}
