// Package fulllayer defines a benchmark for the fully connected layer.
package fulllayer

import (
	"fmt"

	"github.com/sarchlab/mgpusim/v3/benchmarks/dnn/gputensor"
	"github.com/sarchlab/mgpusim/v3/benchmarks/dnn/layers"
	"github.com/sarchlab/mgpusim/v3/benchmarks/dnn/tensor"
	"github.com/sarchlab/mgpusim/v3/driver"
)

// Benchmark is a benchmark for the fully connected layer.
type Benchmark struct {
	driver           *driver.Driver
	context          *driver.Context
	gpus             []int
	useUnifiedMemory bool

	N, InputDim, OutputDim int
	EnableBackward         bool
	RandomizeParameters    bool

	layer    *layers.FullyConnectedLayer
	operator *gputensor.GPUOperator

	forwardIn  tensor.Tensor
	backwardIn tensor.Tensor
	forwardOut tensor.Tensor
}

// NewBenchmark creates a new fully connected layer benchmark.
func NewBenchmark(driver *driver.Driver) *Benchmark {
	b := &Benchmark{
		driver:              driver,
		RandomizeParameters: true,
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
		panic("FullLayer benchmark can only run on a single GPU for now.")
	}

	b.gpus = gpus
}

// SetUnifiedMemory configures the benchmark to use unified memory.
func (b *Benchmark) SetUnifiedMemory() {
	b.useUnifiedMemory = true
}

// Run runs the benchmark.
func (b *Benchmark) Run() {
	b.logSubTask("select GPU %d", b.gpus[0])
	b.driver.SelectGPU(b.context, b.gpus[0])
	b.initMem()
	b.exec()
	b.logSubTask("done")
}

func (b *Benchmark) initMem() {
	b.logSubTask(
		"init shape N=%d inputDim=%d outputDim=%d enableBackward=%t",
		b.N, b.InputDim, b.OutputDim, b.EnableBackward)

	layers.SetFullConnectedLayerSubTaskLogging(true)
	b.logSubTask("create FullyConnectedLayer parameter tensors")
	b.layer = layers.NewFullyConnectedLayer(
		0,
		b.operator,
		b.InputDim,
		b.OutputDim,
	)
	if b.RandomizeParameters {
		b.logSubTask("randomize parameters")
		b.layer.Randomize()
	} else {
		b.logSubTask("skip parameter randomization")
	}

	b.logSubTask("allocate and clear forward input tensor [%d,%d]",
		b.N, b.InputDim)
	b.forwardIn = b.operator.Zeros([]int{b.N, b.InputDim})

	if b.EnableBackward {
		b.logSubTask("allocate and clear backward input tensor [%d,%d]",
			b.N, b.OutputDim)
		b.backwardIn = b.operator.Zeros([]int{b.N, b.OutputDim})
	}
}

func (b *Benchmark) exec() {
	b.logSubTask("forward: FullyConnectedLayer.Forward")
	b.forwardOut = b.layer.Forward(b.forwardIn)
	b.logSubTask("forward complete")

	if b.EnableBackward {
		b.logSubTask("backward: FullyConnectedLayer.Backward")
		b.layer.Backward(b.backwardIn)
		b.logSubTask("backward complete")
	}
}

func (b *Benchmark) logSubTask(format string, args ...interface{}) {
	fmt.Printf("[FullLayer] "+format+"\n", args...)
}

// Verify does nothing for now.
func (b *Benchmark) Verify() {
}
