// Package maxpooling defines a benchmark for the max-pooling layer.
package maxpooling

import (
	"github.com/sarchlab/mgpusim/v3/benchmarks/dnn/gputensor"
	"github.com/sarchlab/mgpusim/v3/benchmarks/dnn/layers"
	"github.com/sarchlab/mgpusim/v3/benchmarks/dnn/tensor"
	"github.com/sarchlab/mgpusim/v3/driver"
)

// Benchmark is a benchmark for the max-pooling layer.
type Benchmark struct {
	driver           *driver.Driver
	context          *driver.Context
	gpus             []int
	useUnifiedMemory bool

	N, C, H, W                int
	outputH, outputW          int
	KernelHeight, KernelWidth int
	PadX, PadY                int
	StrideX, StrideY          int
	EnableBackward            bool

	layer    *layers.MaxPoolingLayer
	operator *gputensor.GPUOperator

	forwardIn  tensor.Tensor
	backwardIn tensor.Tensor
}

// NewBenchmark creates a new max-pooling benchmark.
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
		panic("MaxPooling benchmark can only run on a single GPU for now.")
	}

	b.gpus = gpus
}

// SetUnifiedMemory configures the benchmark to use unified memory.
func (b *Benchmark) SetUnifiedMemory() {
	b.useUnifiedMemory = true
}

// Run runs the benchmark.
func (b *Benchmark) Run() {
	b.driver.SelectGPU(b.context, b.gpus[0])
	b.calculateOutputSize()
	b.initMem()
	b.exec()
}

func (b *Benchmark) calculateOutputSize() {
	b.outputH = (b.H+2*b.PadY-b.KernelHeight)/b.StrideY + 1
	b.outputW = (b.W+2*b.PadX-b.KernelWidth)/b.StrideX + 1
}

func (b *Benchmark) initMem() {
	b.layer = layers.NewMaxPoolingLayer(
		b.operator,
		[]int{b.KernelHeight, b.KernelWidth},
		[]int{b.PadY, b.PadX},
		[]int{b.StrideY, b.StrideX},
	)

	b.forwardIn = b.operator.Zeros([]int{b.N, b.C, b.H, b.W})

	if b.EnableBackward {
		b.backwardIn = b.operator.Zeros([]int{b.N, b.C, b.outputH, b.outputW})
	}
}

func (b *Benchmark) exec() {
	b.layer.Forward(b.forwardIn)

	if b.EnableBackward {
		b.layer.Backward(b.backwardIn)
	}
}

// Verify does nothing for now.
func (b *Benchmark) Verify() {
}
