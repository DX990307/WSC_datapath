// Package isacheck runs one small kernel whose code contains the
// instructions added to the simulator for T17 (v_mad_i64_i32,
// v_alignbit_b32, s_ashr_i64, s_addk_i32, s_cmpk_eq_i32, s_load_dwordx16)
// and checks the results on the host (Verify). Registered as t17-isacheck.
package isacheck

import (
	// embed hsaco files
	_ "embed"
	"fmt"
	"log"
	"math/rand"

	"github.com/sarchlab/mgpusim/v3/driver"
	"github.com/sarchlab/mgpusim/v3/insts"
	"github.com/sarchlab/mgpusim/v3/kernels"
)

//go:embed kernels.hsaco
var hsacoBytes []byte

// Args are the arguments of isa_check.
type Args struct {
	A, B, C, Hi, Lo, OutMad, OutAlign, OutScalar driver.Ptr
	K, N                                         int32
	KK                                           int64
	HiddenGlobalOffsetX                          int64
	HiddenGlobalOffsetY                          int64
	HiddenGlobalOffsetZ                          int64
}

// Benchmark is the ISA check.
type Benchmark struct {
	driver  *driver.Driver
	context *driver.Context
	gpus    []int
	co      *insts.HsaCo

	N  int
	K  int32
	KK int64

	a, b      []int32
	c         []int64
	hi, lo    []uint32
	dMad      driver.Ptr
	dAlign    driver.Ptr
	dScalar   driver.Ptr
	mad       []int64
	align     []uint32
	scalarOut []int32
}

// NewBenchmark returns the check with 1024 elements.
func NewBenchmark(d *driver.Driver) *Benchmark {
	b := &Benchmark{driver: d, N: 1024, K: 0x1234, KK: -0x123456789ab}
	b.context = d.Init()
	b.co = kernels.LoadProgramFromMemory(hsacoBytes, "isa_check")
	if b.co == nil {
		log.Panic("isacheck: failed to load kernel")
	}
	return b
}

// SelectGPU selects the GPU.
func (b *Benchmark) SelectGPU(gpus []int) { b.gpus = gpus }

// SetUnifiedMemory is not supported.
func (b *Benchmark) SetUnifiedMemory() { panic("isacheck does not support unified memory") }

func (b *Benchmark) alloc(bytes int, data interface{}) driver.Ptr {
	p := b.driver.AllocateMemory(b.context, uint64(bytes))
	if data != nil {
		b.driver.MemCopyH2D(b.context, p, data)
	}
	return p
}

// Run launches the kernel.
func (b *Benchmark) Run() {
	b.driver.SelectGPU(b.context, b.gpus[0])
	rng := rand.New(rand.NewSource(1))
	n := b.N
	b.a, b.b = make([]int32, n), make([]int32, n)
	b.c = make([]int64, n)
	b.hi, b.lo = make([]uint32, n), make([]uint32, n)
	for i := 0; i < n; i++ {
		b.a[i] = int32(rng.Uint32())
		b.b[i] = int32(rng.Uint32())
		b.c[i] = int64(rng.Uint64())
		b.hi[i] = rng.Uint32()
		b.lo[i] = rng.Uint32()
	}
	args := Args{
		A: b.alloc(4*n, b.a), B: b.alloc(4*n, b.b), C: b.alloc(8*n, b.c),
		Hi: b.alloc(4*n, b.hi), Lo: b.alloc(4*n, b.lo),
		OutMad: b.alloc(8*n, nil), OutAlign: b.alloc(4*n, nil), OutScalar: b.alloc(4*n, nil),
		K: b.K, N: int32(n), KK: b.KK,
	}
	b.dMad, b.dAlign, b.dScalar = args.OutMad, args.OutAlign, args.OutScalar
	q := b.driver.CreateCommandQueue(b.context)
	b.driver.EnqueueLaunchKernel(q, b.co, [3]uint32{uint32(n), 1, 1}, [3]uint16{64, 1, 1}, &args)
	b.driver.DrainCommandQueue(q)
}

// Verify compares with the GCN3 semantics computed on the host (emulation
// mode; the D2H copies read DRAM).
func (b *Benchmark) Verify() {
	n := b.N
	mad := make([]int64, n)
	align := make([]uint32, n)
	sc := make([]int32, n)
	b.driver.MemCopyD2H(b.context, mad, b.dMad)
	b.driver.MemCopyD2H(b.context, align, b.dAlign)
	b.driver.MemCopyD2H(b.context, sc, b.dScalar)
	s := int32(b.KK >> 35)
	for j := int32(0); j < b.K; j++ {
		s = (s ^ j) + 0x2100
	}
	if b.K == 0x1234 {
		s += 7
	}
	bad := 0
	for i := 0; i < n; i++ {
		wantMad := int64(b.a[i])*int64(b.b[i]) + b.c[i]
		v := uint64(b.hi[i])<<32 | uint64(b.lo[i])
		wantAlign := uint32(v >> (uint32(b.b[i]) & 31))
		if mad[i] != wantMad || align[i] != wantAlign || sc[i] != s+int32(i) {
			if bad < 5 {
				log.Printf("isacheck %d: mad %d/%d align %x/%x scalar %d/%d", i,
					mad[i], wantMad, align[i], wantAlign, sc[i], s+int32(i))
			}
			bad++
		}
	}
	fmt.Printf("ISACHECK,elements=%d,mismatches=%d\n", n, bad)
	if bad > 0 {
		log.Fatalf("isacheck failed")
	}
	log.Printf("Passed!")
}
