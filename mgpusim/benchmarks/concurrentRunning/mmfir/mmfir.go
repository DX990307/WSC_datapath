package mmfir

import (
	"github.com/sarchlab/akita/v3/sim"
	// "github.com/sarchlab/mgpusim/v3/benchmarks/amdappsdk/fastwalshtransform"

	"github.com/sarchlab/mgpusim/v3/benchmarks/concurrentRunning/subBenchmarks/fir"
	"github.com/sarchlab/mgpusim/v3/benchmarks/concurrentRunning/subBenchmarks/mm"
	"github.com/sarchlab/mgpusim/v3/driver"
)

type Benchmark struct {
	driver           *driver.Driver
	context          *driver.Context
	gpus             []int
	useUnifiedMemory bool
	queues           []*driver.CommandQueue
	engine           sim.Engine

	subBenchmarkMM  *mm.Benchmark
	subBenchmarkFIR *fir.Benchmark
	queueList       []*driver.CommandQueue
}

func (b *Benchmark) configMM() {
	b.subBenchmarkMM = mm.NewBenchmark(b.driver)
	b.subBenchmarkMM.X = 2048 / 16
	b.subBenchmarkMM.Y = 2048 * 16
	b.subBenchmarkMM.Z = 2048 / 16

	GPU := 49
	b.subBenchmarkMM.SelectGPU([]int{GPU})
}

func (b *Benchmark) configFIR() {
	b.subBenchmarkFIR = fir.NewBenchmark(b.driver)
	b.subBenchmarkFIR.Length = 1048576 * 2
	// fir.Length = 1024 * 16 * 8

	GPU := 50
	b.subBenchmarkFIR.SelectGPU([]int{GPU})
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
	b.configMM()
	b.configFIR()

	return b
}

func (b *Benchmark) SetUnifiedMemory() {
	b.useUnifiedMemory = true
	b.subBenchmarkMM.SetUnifiedMemory()
	b.subBenchmarkFIR.SetUnifiedMemory()
}

func (b *Benchmark) exec() {
	prQueue := b.subBenchmarkMM.Exec()
	b.queueList = append(b.queueList, prQueue...)

	fwsQueue := b.subBenchmarkFIR.Exec()
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
