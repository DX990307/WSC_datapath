// Package probe implements the T11 probe benchmark. It places a buffer on an
// owner GPM, runs a read or write kernel on a runner GPM, and reports the
// simulated time of every pass. With AllGPMs set, every GPM reads its own
// shard concurrently (one queue per GPM).
package probe

import (
	// embed hsaco files
	_ "embed"
	"fmt"
	"log"

	"github.com/sarchlab/mgpusim/v3/driver"
	"github.com/sarchlab/mgpusim/v3/insts"
	"github.com/sarchlab/mgpusim/v3/kernels"
)

// Kernel modes.
const (
	ModeRead  = 0
	ModeWrite = 1
)

// TouchArgs defines the arguments of the touch kernel.
type TouchArgs struct {
	Buf                 driver.Ptr
	Sink                driver.Ptr
	N                   int32
	Mode                int32
	HiddenGlobalOffsetX int64
	HiddenGlobalOffsetY int64
	HiddenGlobalOffsetZ int64
}

// Benchmark defines the probe benchmark.
type Benchmark struct {
	driver  *driver.Driver
	context *driver.Context
	hsaco   *insts.HsaCo

	Owner   int    // driver GPU ID that holds the buffer
	Runner  int    // driver GPU ID that runs the kernel
	Bytes   uint64 // buffer size (per GPM when AllGPMs is set)
	Mode    int
	Passes  int
	Threads int // work-items per launch
	AllGPMs bool
	// SharedCtx puts every GPM's queue in one context (T12 regression).
	SharedCtx bool
}

//go:embed kernels.hsaco
var hsacoBytes []byte

// NewBenchmark returns a probe benchmark with the T11 defaults.
func NewBenchmark(d *driver.Driver) *Benchmark {
	b := new(Benchmark)
	b.driver = d
	b.context = d.Init()
	b.hsaco = kernels.LoadProgramFromMemory(hsacoBytes, "touch")

	b.Owner = 5
	b.Runner = 5
	b.Bytes = 2 << 20
	b.Mode = ModeRead
	b.Passes = 2
	b.Threads = 32 * 4 * 256

	return b
}

// SelectGPU ignores the runner's GPU list; the probe places data and kernels
// explicitly.
func (b *Benchmark) SelectGPU(gpus []int) {}

// SetUnifiedMemory is not supported by the probe.
func (b *Benchmark) SetUnifiedMemory() {
	panic("probe does not support unified memory")
}

// Run runs the probe.
func (b *Benchmark) Run() {
	if b.AllGPMs {
		b.runAllGPMs()
		return
	}
	b.runSingle()
}

func (b *Benchmark) runSingle() {
	s := NewSPMD(b.driver, b.context, []int{b.Runner})

	buf := s.AllocFor(0, b.Owner, b.Bytes)
	sink := s.AllocFor(0, b.Runner, 64)
	fmt.Printf("PROBE_CONFIG owner=%d runner=%d bytes=%d mode=%d passes=%d threads=%d buf=0x%x\n",
		b.Owner, b.Runner, b.Bytes, b.Mode, b.Passes, b.Threads, uint64(buf))

	for p := 1; p <= b.Passes; p++ {
		start := s.Now()
		s.RunPhase(b.hsaco, []Launch{b.touch(0, buf, sink)})
		b.reportPass(p, start, s.Now())
	}
}

func (b *Benchmark) runAllGPMs() {
	gpus := make([]int, len(b.driver.GPUs))
	for i := range gpus {
		gpus[i] = i + 1
	}

	newSPMD := NewSPMD
	if b.SharedCtx {
		newSPMD = NewSharedCtxSPMD
	}
	s := newSPMD(b.driver, b.context, gpus)
	shards := s.AllocShards(b.Bytes)
	sinks := s.AllocShards(64)
	s.UploadPtrTable(shards)
	fmt.Printf("PROBE_CONFIG all_gpms=%d bytes_per_gpm=%d mode=%d passes=%d threads=%d shared_ctx=%t\n",
		len(gpus), b.Bytes, b.Mode, b.Passes, b.Threads, b.SharedCtx)

	for p := 1; p <= b.Passes; p++ {
		launches := make([]Launch, len(gpus))
		for i := range gpus {
			launches[i] = b.touch(i, shards[i], sinks[i])
		}

		start := s.Now()
		s.RunPhase(b.hsaco, launches)
		b.reportPass(p, start, s.Now())
	}
}

func (b *Benchmark) touch(rank int, buf, sink driver.Ptr) Launch {
	return Launch{
		Rank: rank,
		Grid: [3]uint32{uint32(b.Threads), 1, 1},
		WG:   [3]uint16{256, 1, 1},
		Args: &TouchArgs{
			Buf:  buf,
			Sink: sink,
			N:    int32(b.Bytes / 4),
			Mode: int32(b.Mode),
		},
	}
}

func (b *Benchmark) reportPass(pass int, start, end float64) {
	fmt.Printf("PROBE_PASS pass=%d start_ns=%.3f end_ns=%.3f elapsed_ns=%.3f\n",
		pass, start*1e9, end*1e9, (end-start)*1e9)
}

// Verify is not used by T11 (timing only).
func (b *Benchmark) Verify() {
	log.Printf("probe: verify is not implemented")
}
