// Package conv2d defines a benchmark for the Convolutional Layer.
package conv2d

import (
	"github.com/sarchlab/akita/v3/sim"
	"github.com/sarchlab/mgpusim/v3/benchmarks/TensorParallelismSample/gputensor"
	"github.com/sarchlab/mgpusim/v3/benchmarks/TensorParallelismSample/layers"
	"github.com/sarchlab/mgpusim/v3/benchmarks/TensorParallelismSample/tensor"
	"github.com/sarchlab/mgpusim/v3/driver"
)

// A Benchmark is a benchmark for the Convolutional Layer.
type Benchmark struct {
	driver           *driver.Driver
	context          *driver.Context
	gpus             []int
	useUnifiedMemory bool
	engine           sim.Engine

	N, C, H, W                               int
	outputH, outputW                         int
	KernelChannel, KernelHeight, KernelWidth int
	PadX, PadY                               int
	StrideX, StrideY                         int
	EnableBackward                           bool

	layer    *layers.Conv2D
	operator *gputensor.GPUOperator

	forwardIn  tensor.Tensor
	backwardIn tensor.Tensor
}

// NewBenchmark creates a new Conv2D benchmark. It requires the GPU driver as an argument.
func NewBenchmark(driver *driver.Driver) *Benchmark {
	b := &Benchmark{
		driver: driver,
	}

	b.context = b.driver.Init()
	b.operator = gputensor.NewGPUOperator(b.driver, b.context)
	b.operator.ReportTime()

	return b
}

// EnableVerification configures the benchmark to verify the result.
func (b *Benchmark) EnableVerification() {
	b.operator.EnableVerification()
}

// SelectGPU selects the GPU to run the benchmark on.
func (b *Benchmark) SelectGPU(gpus []int) {
	if len(gpus) > 1 {
		panic("Conv2D benchmark can only run on a single GPU for now.")
	}

	b.gpus = gpus
}

// SetUnifiedMemory configures the benchmark to use unified memory.
func (b *Benchmark) SetUnifiedMemory() {
	b.useUnifiedMemory = true
}

// Run runs the benchmark.
func (b *Benchmark) Run() {
	// b.driver.SelectGPU(b.context, b.gpus[0])
	b.calculateOutputSize()
	// b.initMem()
	b.exec()
}

func (b *Benchmark) calculateOutputSize() {
	b.outputH = (b.H+2*b.PadY-b.KernelHeight)/b.StrideY + 1
	b.outputW = (b.W+2*b.PadX-b.KernelWidth)/b.StrideX + 1
}

func (b *Benchmark) initMem() {
	b.layer = layers.NewConv2D(
		0,
		b.operator,
		[]int{b.C, b.H, b.W},
		[]int{b.KernelChannel, b.C, b.KernelHeight, b.KernelWidth},
		[]int{b.StrideY, b.StrideX},
		[]int{b.PadY, b.PadX}, 4,
	)
	b.layer.Randomize()

	b.forwardIn = b.operator.Zeros([]int{b.N, b.C, b.H, b.W})

	if b.EnableBackward {
		b.backwardIn = b.operator.Zeros(
			[]int{b.N, b.KernelChannel, b.outputH, b.outputW})
	}
}

func (b *Benchmark) exec() {
	b.initMem()
	loop := 7
	for i := 0; i <= loop; i++ {
		b.layer.Execute(b.forwardIn, loop, i)
		// b.layer.Forward(b.forwardIn)

		for j := range b.layer.QueueList {
			b.driver.DrainCommandQueue(b.layer.QueueList[j])
		}
	}
}

// Verify does nothing for now.
func (b *Benchmark) Verify() {
}
