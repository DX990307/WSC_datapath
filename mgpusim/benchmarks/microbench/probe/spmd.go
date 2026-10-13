package probe

import (
	"encoding/binary"
	"fmt"

	"github.com/sarchlab/mgpusim/v3/driver"
	"github.com/sarchlab/mgpusim/v3/insts"
)

// SPMD is a reusable skeleton for benchmarks that place one shard on each
// GPM and run one kernel per GPM in phases. It bypasses the runner's unified
// GPU: every GPM gets its own command queue, so each kernel runs only on the
// GPM it is launched to.
//
// Every rank also gets its own driver context (sharing one PID, so all ranks
// see the same virtual address space), as mccl's CommInitAllMultipleContexts
// does. With a single shared context, processing any rank's kernel launch
// marks every buffer of the context dirty, so the other ranks' kernarg copies
// trigger an L2 flush on all GPMs; the flush responses can then race with the
// copy responses and leave a queue stuck forever.
//
// The driver keeps the selected GPU in each context, so an SPMD must be
// driven from a single goroutine.
type SPMD struct {
	Driver *driver.Driver
	GPUs   []int // driver GPU IDs, starting from 1; rank i runs on GPUs[i]
	Ctxs   []*driver.Context
	Queues []*driver.CommandQueue
}

// Launch describes the kernel that one rank runs in a phase.
type Launch struct {
	Rank int
	Grid [3]uint32
	WG   [3]uint16
	Args interface{}
}

// NewSPMD creates one context and one command queue per GPM. All contexts
// share the PID of base.
func NewSPMD(d *driver.Driver, base *driver.Context, gpus []int) *SPMD {
	s := &SPMD{Driver: d, GPUs: gpus}
	for _, g := range gpus {
		s.mustBeGPU(g)
		ctx := d.InitWithExistingPID(base)
		d.SelectGPU(ctx, g)
		s.Ctxs = append(s.Ctxs, ctx)
		s.Queues = append(s.Queues, d.CreateCommandQueue(ctx))
	}
	return s
}

// AllocFor allocates a buffer whose pages live on the given GPM. The buffer is
// tracked by rank's context, so it should be the rank whose kernels write it.
func (s *SPMD) AllocFor(rank, gpu int, bytes uint64) driver.Ptr {
	s.mustBeGPU(gpu)
	ctx := s.Ctxs[rank]
	s.Driver.SelectGPU(ctx, gpu)
	ptr := s.Driver.AllocateMemory(ctx, bytes)
	s.Driver.SelectGPU(ctx, s.GPUs[rank])
	return ptr
}

// AllocShards allocates one shard per rank; shard i lives on GPUs[i].
func (s *SPMD) AllocShards(bytesPerShard uint64) []driver.Ptr {
	shards := make([]driver.Ptr, len(s.GPUs))
	for i, g := range s.GPUs {
		shards[i] = s.AllocFor(i, g, bytesPerShard)
	}
	return shards
}

// UploadPtrTable copies the addresses of all shards into a uint64 array on
// every GPM, so that a kernel on rank i can reach rank j's shard as
// table[j]. It returns the per-rank table addresses.
func (s *SPMD) UploadPtrTable(shards []driver.Ptr) []driver.Ptr {
	table := make([]uint64, len(shards))
	for i, p := range shards {
		table[i] = uint64(p)
	}

	bytes := uint64(binary.Size(table))
	tables := make([]driver.Ptr, len(s.GPUs))
	for i, g := range s.GPUs {
		tables[i] = s.AllocFor(i, g, bytes)
		s.Driver.MemCopyH2D(s.Ctxs[i], tables[i], table)
	}
	return tables
}

// RunPhase launches every kernel of a phase on its own GPM, then waits for
// all of them. Each rank's context stays selected on its own GPM because the
// driver places the code object, kernel arguments, and AQL packet on the
// selected GPU; otherwise instruction and kernarg fetches would be remote.
func (s *SPMD) RunPhase(co *insts.HsaCo, launches []Launch) {
	for _, l := range launches {
		s.Driver.EnqueueLaunchKernel(s.Queues[l.Rank], co, l.Grid, l.WG, l.Args)
	}

	for _, l := range launches {
		s.Driver.DrainCommandQueue(s.Queues[l.Rank])
	}
}

// Now returns the current simulated time in seconds.
func (s *SPMD) Now() float64 {
	return float64(s.Driver.Engine.CurrentTime())
}

// mustBeGPU panics if gpu is not a valid driver GPU ID.
func (s *SPMD) mustBeGPU(gpu int) {
	if gpu < 1 || gpu > len(s.Driver.GPUs) {
		panic(fmt.Sprintf("GPU %d out of range [1, %d]", gpu, len(s.Driver.GPUs)))
	}
}
