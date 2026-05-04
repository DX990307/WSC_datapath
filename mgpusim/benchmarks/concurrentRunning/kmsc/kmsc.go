package kmsc

import (
	"github.com/sarchlab/akita/v3/sim"
	// "github.com/sarchlab/mgpusim/v3/benchmarks/amdappsdk/fastwalshtransform"

	"github.com/sarchlab/mgpusim/v3/benchmarks/concurrentRunning/subBenchmarks/km"
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

	subBenchmarkKM *km.Benchmark
	subBenchmarkSC *sc.Benchmark
	queueList      []*driver.CommandQueue
}

func (b *Benchmark) configkm() {
	b.subBenchmarkKM = km.NewBenchmark(b.driver)
	b.subBenchmarkKM.NumPoints = 1048576
	b.subBenchmarkKM.NumClusters = 8 / 4
	b.subBenchmarkKM.NumFeatures = 32 / 16
	b.subBenchmarkKM.MaxIter = 3 * 6

	GPU := 49
	b.subBenchmarkKM.SelectGPU([]int{GPU})
}

func (b *Benchmark) configSC() {
	b.subBenchmarkSC = sc.NewBenchmark(b.driver)
	b.subBenchmarkSC.Height = 2048
	b.subBenchmarkSC.Width = 2048 * 4
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
	b.configkm()
	b.configSC()

	return b
}

func (b *Benchmark) SetUnifiedMemory() {
	b.useUnifiedMemory = true
	b.subBenchmarkKM.SetUnifiedMemory()
	b.subBenchmarkSC.SetUnifiedMemory()
}

func (b *Benchmark) exec() {
	prQueue := b.subBenchmarkKM.Exec()
	b.queueList = append(b.queueList, prQueue...)

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
