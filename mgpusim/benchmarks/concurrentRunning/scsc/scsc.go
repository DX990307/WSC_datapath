package scsc

import (
	"github.com/sarchlab/akita/v3/sim"
	// "github.com/sarchlab/mgpusim/v3/benchmarks/amdappsdk/fastwalshtransform"

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

	subBenchmarkSC1 *sc.Benchmark
	subBenchmarkSC2 *sc.Benchmark
	queueList       []*driver.CommandQueue
}

func (b *Benchmark) configkm() {
	b.subBenchmarkSC1 = sc.NewBenchmark(b.driver)
	b.subBenchmarkSC1.Height = 2048 / 2
	b.subBenchmarkSC1.Width = 2048
	b.subBenchmarkSC1.SetMaskSize(3)

	GPU := 49
	b.subBenchmarkSC1.SelectGPU([]int{GPU})
}

func (b *Benchmark) configSC() {
	b.subBenchmarkSC2 = sc.NewBenchmark(b.driver)
	b.subBenchmarkSC2.Height = 2048 / 2
	b.subBenchmarkSC2.Width = 2048
	b.subBenchmarkSC2.SetMaskSize(3)

	GPU := 50
	b.subBenchmarkSC2.SelectGPU([]int{GPU})
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
	b.configkm()
	b.configSC()

	return b
}

func (b *Benchmark) SetUnifiedMemory() {
	b.useUnifiedMemory = true
	b.subBenchmarkSC1.SetUnifiedMemory()
	b.subBenchmarkSC2.SetUnifiedMemory()
}

func (b *Benchmark) exec() {
	prQueue := b.subBenchmarkSC1.Exec()
	b.queueList = append(b.queueList, prQueue...)

	fwsQueue := b.subBenchmarkSC2.Exec()
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
