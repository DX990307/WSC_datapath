// Package gups is the W2 random read-modify-write workload: GUPS /
// RandomAccess after gpu_benchmarks tier2/altis_gups (Altis gups.cu,
// 4*N LFSR XOR updates, seed 1, polynomial 7, table[i] = i initially).
//
// Deviations from upstream: plain load / xor / store instead of atomicXor or
// CAS (HPCC allows <= 1% wrong table entries; the simulator has no global
// atomics); each thread's stream start state is computed on the host
// (upstream: square-table jump on the GPU, timed); the update count is split
// evenly as q or q+1 per thread instead of t*U/T bounds; UpdateMult can
// raise the update count above 4*N.
//
// Simulator note: every update touches a random 4 KB page, so tables larger
// than the per-GPM L2 TLB reach (512 x 4 KB = 2 MB) make the run TLB / GMMU
// bound (8 MB table: L2 TLB queues full, ~10x slower per access than a 2 MB
// table).
package gups

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

// KernelArgs are the arguments of gups_update.
type KernelArgs struct {
	Table, Starts  driver.Ptr
	Mask, Q, R, Pd uint32
	HiddenArgs
}

// DefaultParams is the smallest recommended size: 2^22 words (32 MB),
// 2^24 updates, 1344 work-groups (one per CU), 48 updates per thread.
// With few updates per thread the lanes of a wave hold nearby, correlated
// LFSR states and collide (emu: 1.1% wrong entries at 12 updates per
// thread, 0.04% at 48).
func DefaultParams() Params { return Params{LogTable: 22, WGs: 1344, UpdateMult: 4} }

// Benchmark is the GUPS benchmark.
type Benchmark struct {
	driver  *driver.Driver
	context *driver.Context
	gpus    []int
	kernel  *insts.HsaCo

	Params Params
	dTable driver.Ptr
}

// NewBenchmark returns a GUPS benchmark.
func NewBenchmark(d *driver.Driver) *Benchmark {
	b := &Benchmark{driver: d, Params: DefaultParams()}
	b.context = d.Init()
	b.kernel = kernels.LoadProgramFromMemory(hsacoBytes, "gups_update")
	if b.kernel == nil {
		log.Panic("gups: failed to load kernel")
	}
	return b
}

// SelectGPU selects the GPU.
func (b *Benchmark) SelectGPU(gpus []int) { b.gpus = gpus }

// SetUnifiedMemory is not supported.
func (b *Benchmark) SetUnifiedMemory() { panic("gups does not support unified memory") }

// EnableVerification is a no-op.
func (b *Benchmark) EnableVerification() {}

// Run uploads the table and start states and launches the update kernel.
func (b *Benchmark) Run() {
	p := b.Params
	if err := p.Validate(); err != nil {
		log.Panic(err)
	}
	b.driver.SelectGPU(b.context, b.gpus[0])
	b.driver.SetMagicCopyCoherent(true)
	tb := uint64(p.TableSize()) * 8
	b.dTable = b.driver.AllocateMemory(b.context, tb)
	b.driver.RegisterMemoryObject(b.context, b.dTable, tb, "gups_table")
	sb := uint64(p.Threads()) * 8
	dStarts := b.driver.AllocateMemory(b.context, sb)
	b.driver.RegisterMemoryObject(b.context, dStarts, sb, "gups_starts")
	b.driver.MemCopyH2D(b.context, b.dTable, p.InitialTable())
	b.driver.MemCopyH2D(b.context, dStarts, p.Starts())
	q, r := p.Partition()
	fmt.Printf("GUPS,config,table_words=%d,table_mb=%.1f,updates=%d,update_mult=%d,threads=%d,wgs=%d,"+
		"updates_per_thread=%d+%d\n", p.TableSize(), float64(tb)/1e6, p.Updates(), p.UpdateMult,
		p.Threads(), p.WGs, q, r)
	cq := b.driver.CreateCommandQueue(b.context)
	args := KernelArgs{Table: b.dTable, Starts: dStarts, Mask: uint32(p.TableSize() - 1), Q: q, R: r}
	b.driver.EnqueueLaunchKernel(cq, b.kernel, [3]uint32{uint32(p.Threads()), 1, 1},
		[3]uint16{256, 1, 1}, &args)
	b.driver.DrainCommandQueue(cq)
}

// Verify counts wrong table entries against the serial stream; HPCC
// accepts <= 1% at 4 updates per word (lost updates of the non-atomic
// read-modify-write); the limit scales with UpdateMult / 4 (same lost-update
// rate per update).
func (b *Benchmark) Verify() {
	p := b.Params
	ref := p.Reference()
	got := make([]uint64, len(ref))
	b.driver.MemCopyD2H(b.context, got, b.dTable)
	bad := 0
	for i := range ref {
		if got[i] != ref[i] {
			bad++
		}
	}
	frac := float64(bad) / float64(len(ref))
	fmt.Printf("GUPS,verify,entries=%d,wrong=%d,wrong_fraction=%.5f\n", len(ref), bad, frac)
	limit := 0.01 * math.Max(1, float64(p.UpdateMult)/4)
	if frac > limit {
		log.Fatalf("gups: %.3f%% wrong entries > %.3g%%", 100*frac, 100*limit)
	}
	log.Printf("Passed!")
}
