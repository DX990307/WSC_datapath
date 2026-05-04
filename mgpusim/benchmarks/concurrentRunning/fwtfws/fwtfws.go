package fwtfws

import (
	"github.com/sarchlab/akita/v3/sim"
	// "github.com/sarchlab/mgpusim/v3/benchmarks/amdappsdk/fastwalshtransform"

	"github.com/sarchlab/mgpusim/v3/benchmarks/concurrentRunning/subBenchmarks/fws"
	"github.com/sarchlab/mgpusim/v3/benchmarks/concurrentRunning/subBenchmarks/fwt"
	"github.com/sarchlab/mgpusim/v3/driver"
)

type Benchmark struct {
	driver           *driver.Driver
	context          *driver.Context
	gpus             []int
	useUnifiedMemory bool
	queues           []*driver.CommandQueue
	engine           sim.Engine

	subBenchmarkFWS *fws.Benchmark
	subBenchmarkFWT *fwt.Benchmark
	queueList       []*driver.CommandQueue
}

type BtArg struct {
	Input               driver.Ptr
	Stage               uint32
	PassOfStage         uint32
	Direction           uint32
	Padding             uint32
	HiddenGlobalOffsetX int64
	HiddenGlobalOffsetY int64
	HiddenGlobalOffsetZ int64
}

type fwtArgs struct {
	TArray driver.Ptr
	Step   uint32
}

func (b *Benchmark) configFWS() {
	b.subBenchmarkFWS = fws.NewBenchmark(b.driver)
	b.subBenchmarkFWS.NumNodes = 1024 * 4
	b.subBenchmarkFWS.NumIterations = 1

	GPU := 49
	b.subBenchmarkFWS.SelectGPU([]int{GPU})
}

func (b *Benchmark) configFWT() {
	b.subBenchmarkFWT = fwt.NewBenchmark(b.driver)
	b.subBenchmarkFWT.Length = 65536 * 4

	GPU := 50
	b.subBenchmarkFWT.SelectGPU([]int{GPU})
}

func (b *Benchmark) SelectGPU(gpus []int) {
	if len(gpus) > 1 {
		panic("Conv2D benchmark can only run on a single GPU for now.")
	}

	b.gpus = gpus
}

func NewBenchmark(driver *driver.Driver) *Benchmark {
	b := new(Benchmark)
	b.driver = driver
	b.context = b.driver.Init()
	b.configFWS()
	b.configFWT()

	return b
}

func (b *Benchmark) SetUnifiedMemory() {
	b.useUnifiedMemory = true
	b.subBenchmarkFWS.SetUnifiedMemory()
	b.subBenchmarkFWT.SetUnifiedMemory()
}

func (b *Benchmark) exec() {
	prQueue := b.subBenchmarkFWS.Exec()
	b.queueList = append(b.queueList, prQueue)

	fwsQueue := b.subBenchmarkFWT.Exec()
	b.queueList = append(b.queueList, fwsQueue...)

	for _, queue := range b.queueList {
		b.driver.DrainCommandQueue(queue)
	}
}

func (b *Benchmark) Run() {
	b.exec()
}

func (b *Benchmark) Verify() {
}
