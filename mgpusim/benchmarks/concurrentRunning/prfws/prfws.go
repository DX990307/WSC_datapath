package prfws

import (
	"github.com/sarchlab/akita/v3/sim"
	// "github.com/sarchlab/mgpusim/v3/benchmarks/amdappsdk/fastwalshtransform"
	"github.com/sarchlab/mgpusim/v3/benchmarks/concurrentRunning/subBenchmarks/fws"
	"github.com/sarchlab/mgpusim/v3/benchmarks/concurrentRunning/subBenchmarks/pr"
	"github.com/sarchlab/mgpusim/v3/driver"
)

type Benchmark struct {
	driver           *driver.Driver
	context          *driver.Context
	gpus             []int
	useUnifiedMemory bool
	queues           []*driver.CommandQueue
	engine           sim.Engine

	subBenchmarkPR  *pr.Benchmark
	subBenchmarkFWS *fws.Benchmark
	queueList       []*driver.CommandQueue
}

type PrArgs struct {
	NumRows   uint32
	Padding   uint32
	RowOffset driver.Ptr
	Col       driver.Ptr
	Val       driver.Ptr
	Vals      driver.LocalPtr
	Padding2  uint32
	X         driver.Ptr
	Y         driver.Ptr
}

type fwsArgs struct {
	OutputPathDistanceMatrix driver.Ptr
	OutputPathMatrix         driver.Ptr

	NumNodes uint32
	Pass     uint32
}

func (b *Benchmark) configPR() {
	b.subBenchmarkPR = pr.NewBenchmark(b.driver)
	b.subBenchmarkPR.NumNodes = 262144 / 32
	b.subBenchmarkPR.NumConnections = 1048576 / 32
	b.subBenchmarkPR.MaxIterations = 1

	GPU := 49
	b.subBenchmarkPR.SelectGPU([]int{GPU})
}

func (b *Benchmark) configFWS() {
	b.subBenchmarkFWS = fws.NewBenchmark(b.driver)
	b.subBenchmarkFWS.NumNodes = 1024 / 2
	b.subBenchmarkFWS.NumIterations = 1

	GPU := 50
	b.subBenchmarkFWS.SelectGPU([]int{GPU})
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
	b.configPR()
	b.configFWS()

	return b
}

func (b *Benchmark) SetUnifiedMemory() {
	b.useUnifiedMemory = true
	b.subBenchmarkPR.SetUnifiedMemory()
	b.subBenchmarkFWS.SetUnifiedMemory()
}

func (b *Benchmark) exec() {
	prQueue := b.subBenchmarkPR.Exec()
	b.queueList = append(b.queueList, prQueue)

	fwsQueue := b.subBenchmarkFWS.Exec()
	b.queueList = append(b.queueList, fwsQueue)

	for _, queue := range b.queueList {
		b.driver.DrainCommandQueue(queue)
	}
}

func (b *Benchmark) Run() {
	b.exec()
}

func (b *Benchmark) Verify() {
}
