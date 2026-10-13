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
// The driver keeps the selected GPU in the context, so an SPMD must be driven
// from a single goroutine.
type SPMD struct {
	Driver *driver.Driver
	Ctx    *driver.Context
	GPUs   []int // driver GPU IDs, starting from 1; rank i runs on GPUs[i]
	Queues []*driver.CommandQueue
}

// Launch describes the kernel that one rank runs in a phase.
type Launch struct {
	Rank int
	Grid [3]uint32
	WG   [3]uint16
	Args interface{}
}

// NewSPMD creates one command queue per GPM.
func NewSPMD(d *driver.Driver, ctx *driver.Context, gpus []int) *SPMD {
	s := &SPMD{Driver: d, Ctx: ctx, GPUs: gpus}
	for _, g := range gpus {
		d.SelectGPU(ctx, g)
		s.Queues = append(s.Queues, d.CreateCommandQueue(ctx))
	}
	return s
}

// AllocOn allocates a buffer whose pages live on the given GPM.
func (s *SPMD) AllocOn(gpu int, bytes uint64) driver.Ptr {
	s.Driver.SelectGPU(s.Ctx, gpu)
	return s.Driver.AllocateMemory(s.Ctx, bytes)
}

// AllocShards allocates one shard per rank; shard i lives on GPUs[i].
func (s *SPMD) AllocShards(bytesPerShard uint64) []driver.Ptr {
	shards := make([]driver.Ptr, len(s.GPUs))
	for i, g := range s.GPUs {
		shards[i] = s.AllocOn(g, bytesPerShard)
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
		tables[i] = s.AllocOn(g, bytes)
		s.Driver.MemCopyH2D(s.Ctx, tables[i], table)
	}
	return tables
}

// RunPhase launches every kernel of a phase on its own GPM, then waits for
// all of them. The GPM is selected before each enqueue because the driver
// places the code object, kernel arguments, and AQL packet on the selected
// GPU; otherwise instruction and kernarg fetches would become remote.
func (s *SPMD) RunPhase(co *insts.HsaCo, launches []Launch) {
	for _, l := range launches {
		s.Driver.SelectGPU(s.Ctx, s.GPUs[l.Rank])
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
