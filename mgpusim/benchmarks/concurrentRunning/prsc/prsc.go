package prsc

import (
	"github.com/sarchlab/akita/v3/sim"
	// "github.com/sarchlab/mgpusim/v3/benchmarks/amdappsdk/fastwalshtransform"

	"github.com/sarchlab/mgpusim/v3/benchmarks/concurrentRunning/subBenchmarks/pr"
	"github.com/sarchlab/mgpusim/v3/benchmarks/concurrentRunning/subBenchmarks/sc"
	"github.com/sarchlab/mgpusim/v3/driver"
)

type Benchmark struct {
	driver           *driver.Driver
	context          *driver.Context
	gpus             []int
	useUnifiedMemory bool
	queues           []*driver.CommandQueue
	engine           sim.Engine

	subBenchmarkPR *pr.Benchmark
	subBenchmarkSC *sc.Benchmark
	queueList      []*driver.CommandQueue
}

func (b *Benchmark) configPR() {
	b.subBenchmarkPR = pr.NewBenchmark(b.driver)
	b.subBenchmarkPR.NumNodes = 262144 / 32
	b.subBenchmarkPR.NumConnections = 1048576 / 32
	b.subBenchmarkPR.MaxIterations = 1

	GPU := 49
	b.subBenchmarkPR.SelectGPU([]int{GPU})
}

func (b *Benchmark) configSC() {
	b.subBenchmarkSC = sc.NewBenchmark(b.driver)
	b.subBenchmarkSC.Height = 2048 / 2
	b.subBenchmarkSC.Width = 2048
	b.subBenchmarkSC.SetMaskSize(3)

	GPU := 50
	b.subBenchmarkSC.SelectGPU([]int{GPU})
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
	b.configSC()

	return b
}

func (b *Benchmark) SetUnifiedMemory() {
	b.useUnifiedMemory = true
	b.subBenchmarkPR.SetUnifiedMemory()
	b.subBenchmarkSC.SetUnifiedMemory()
}

func (b *Benchmark) exec() {
	prQueue := b.subBenchmarkPR.Exec()
	b.queueList = append(b.queueList, prQueue)

	fwsQueue := b.subBenchmarkSC.Exec()
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
