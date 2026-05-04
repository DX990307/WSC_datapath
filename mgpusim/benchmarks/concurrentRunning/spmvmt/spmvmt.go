package spmvmt

import (
	"github.com/sarchlab/akita/v3/sim"
	// "github.com/sarchlab/mgpusim/v3/benchmarks/amdappsdk/fastwalshtransform"

	"github.com/sarchlab/mgpusim/v3/benchmarks/concurrentRunning/subBenchmarks/mt"
	"github.com/sarchlab/mgpusim/v3/benchmarks/concurrentRunning/subBenchmarks/spmv"
	"github.com/sarchlab/mgpusim/v3/driver"
)

type Benchmark struct {
	driver           *driver.Driver
	context          *driver.Context
	gpus             []int
	useUnifiedMemory bool
	queues           []*driver.CommandQueue
	engine           sim.Engine

	subBenchmarkSPMV *spmv.Benchmark
	subBenchmarkMT   *mt.Benchmark
	queueList        []*driver.CommandQueue
}

func (b *Benchmark) configSPMV() {
	b.subBenchmarkSPMV = spmv.NewBenchmark(b.driver)
	b.subBenchmarkSPMV.Dim = 10485760 / 4
	b.subBenchmarkSPMV.Sparsity = 0.000000001

	GPU := 49
	b.subBenchmarkSPMV.SelectGPU([]int{GPU})
}

func (b *Benchmark) configMT() {
	b.subBenchmarkMT = mt.NewBenchmark(b.driver)
	b.subBenchmarkMT.Width = 4096 / 2
	// matrixtranspose.Width = 4096 / 2
	// benchmark = matrixtranspose

	GPU := 50
	b.subBenchmarkMT.SelectGPU([]int{GPU})
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
	b.configSPMV()
	b.configMT()

	return b
}

func (b *Benchmark) SetUnifiedMemory() {
	b.useUnifiedMemory = true
	b.subBenchmarkSPMV.SetUnifiedMemory()
	b.subBenchmarkMT.SetUnifiedMemory()
}

func (b *Benchmark) exec() {
	prQueue := b.subBenchmarkSPMV.Exec()
	b.queueList = append(b.queueList, prQueue)

	fwsQueue := b.subBenchmarkMT.Exec()
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
