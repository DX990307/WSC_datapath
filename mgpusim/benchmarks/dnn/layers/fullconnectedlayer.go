package layers

import (
	"fmt"
	"math/rand"

	"github.com/sarchlab/mgpusim/v3/benchmarks/dnn/tensor"
)

var fullConnectedLayerSubTaskLogging bool

// SetFullConnectedLayerSubTaskLogging enables or disables full-layer subtask
// progress logs. It is intended for benchmark debugging.
func SetFullConnectedLayerSubTaskLogging(enabled bool) {
	fullConnectedLayerSubTaskLogging = enabled
}

// A FullyConnectedLayer implements a fully connected layer.
type FullyConnectedLayer struct {
	layerIndex int
	to         tensor.Operator

	InputSize  int
	OutputSize int

	parameters      tensor.Tensor
	weights         tensor.Tensor
	bias            tensor.Tensor
	gradients       tensor.Tensor
	weightGradients tensor.Tensor
	biasGradients   tensor.Tensor
	forwardInput    tensor.Tensor
}

// NewFullyConnectedLayer creates a fully connected layer.
func NewFullyConnectedLayer(
	index int,
	to tensor.Operator,
	inputSize, outputSize int,
) *FullyConnectedLayer {
	numWeight := inputSize * outputSize
	numBias := outputSize
	numParams := numWeight + numBias

	l := &FullyConnectedLayer{
		layerIndex: index,
		to:         to,
		InputSize:  inputSize,
		OutputSize: outputSize,
		parameters: to.Create([]int{numParams}),
		gradients:  to.Create([]int{numParams}),
	}

	l.weights = to.Slice(l.parameters, 0, numWeight)
	l.bias = to.Slice(l.parameters, numWeight, numParams)
	l.weightGradients = to.Slice(l.gradients, 0, numWeight)
	l.biasGradients = to.Slice(l.gradients, numWeight, numParams)

	return l
}

// Randomize initialize the parameters of the layer randomly.
func (l *FullyConnectedLayer) Randomize() {
	numWeight := l.InputSize * l.OutputSize
	weights := make([]float64, numWeight)
	for i := 0; i < numWeight; i++ {
		weights[i] = (rand.Float64() - 0.5) / float64(l.InputSize) * 2
	}
	l.to.Init(l.weights, weights)

	numBias := l.OutputSize
	bias := make([]float64, numBias)
	for i := 0; i < numBias; i++ {
		bias[i] = rand.Float64()*2 - 1
	}
	l.to.Init(l.bias, bias)
}

// Forward performs the forward propagation operation.
func (l *FullyConnectedLayer) Forward(
	input tensor.Tensor,
) tensor.Tensor {
	l.logSubTask("Forward: clone input %v", input.Size())
	l.forwardInput = l.to.Clone(input)

	l.logSubTask("Forward: reshape input to [%d,%d]",
		input.Size()[0], l.InputSize)
	in := l.to.Reshape(input, []int{input.Size()[0], l.InputSize})
	l.logSubTask("Forward: reshape weight to [%d,%d]",
		l.InputSize, l.OutputSize)
	weightMat := l.to.Reshape(l.weights, []int{l.InputSize, l.OutputSize})
	l.logSubTask("Forward: repeat bias %d times", input.Size()[0])
	biasMat := l.to.Repeat(l.bias, input.Size()[0])
	l.logSubTask("Forward: reshape repeated bias to [%d,%d]",
		input.Size()[0], l.OutputSize)
	biasMatReshape := l.to.Reshape(biasMat,
		[]int{input.Size()[0], l.OutputSize})

	l.logSubTask("Forward: GEMM [%d,%d] x [%d,%d]",
		input.Size()[0], l.InputSize, l.InputSize, l.OutputSize)
	out := l.to.Gemm(false, false, 1, 1, in, weightMat, biasMatReshape)

	l.logSubTask("Forward: free temporary input reshape")
	l.to.Free(in)
	l.logSubTask("Forward: free temporary weight reshape")
	l.to.Free(weightMat)
	l.logSubTask("Forward: free repeated bias")
	l.to.Free(biasMat)
	l.logSubTask("Forward: free temporary bias reshape")
	l.to.Free(biasMatReshape)

	return out
}

// Backward calculate the weight, bias, and input gradients.
func (l *FullyConnectedLayer) Backward(
	input tensor.Tensor,
) tensor.Tensor {
	l.logSubTask("Backward: clear gradients")
	l.to.Clear(l.gradients)

	l.logSubTask("Backward: calculate weight gradients")
	l.calculateWeightGradients(input)
	l.logSubTask("Backward: calculate bias gradients")
	l.calculateBiasGradients(input)
	var output tensor.Tensor

	if l.layerIndex > 0 {
		l.logSubTask("Backward: calculate input gradients")
		output = l.calculateInputGradients(input)
	}

	l.logSubTask("Backward: free cloned forward input")
	l.to.Free(l.forwardInput)

	return output
}

func (l *FullyConnectedLayer) calculateWeightGradients(
	input tensor.Tensor,
) {
	forwardInMatrix := l.to.Reshape(l.forwardInput,
		[]int{l.forwardInput.Size()[0], l.InputSize})
	backwardInMatrix := l.to.Reshape(input,
		[]int{input.Size()[0], l.OutputSize})
	zeroMatrix := l.to.Zeros([]int{l.InputSize, l.OutputSize})

	g := l.to.Gemm(
		true, false,
		1, 1,
		forwardInMatrix, backwardInMatrix,
		zeroMatrix,
	)

	l.to.Copy(l.weightGradients, g)

	l.to.Free(forwardInMatrix)
	l.to.Free(backwardInMatrix)
	l.to.Free(zeroMatrix)
	l.to.Free(g)
}

func (l *FullyConnectedLayer) calculateBiasGradients(
	input tensor.Tensor,
) {
	g := l.to.Sum(input, []int{0})
	l.to.Copy(l.biasGradients, g)
	l.to.Free(g)
}

func (l *FullyConnectedLayer) calculateInputGradients(
	input tensor.Tensor,
) tensor.Tensor {
	weightMatrix := l.to.Reshape(l.weights, []int{l.InputSize, l.OutputSize})
	inputMatrix := l.to.Reshape(input, []int{input.Size()[0], l.OutputSize})
	zeroMatrix := l.to.Zeros([]int{input.Size()[0], l.InputSize})

	out := l.to.Gemm(false, true, 1, 1, inputMatrix, weightMatrix, zeroMatrix)

	l.to.Free(weightMatrix)
	l.to.Free(inputMatrix)
	l.to.Free(zeroMatrix)

	return out
}

// Parameters returns the parameters of the layer.
func (l FullyConnectedLayer) Parameters() tensor.Tensor {
	return l.parameters
}

// Gradients returns the gradients of the layer.
func (l FullyConnectedLayer) Gradients() tensor.Tensor {
	return l.gradients
}

func (l *FullyConnectedLayer) logSubTask(format string, args ...interface{}) {
	if !fullConnectedLayerSubTaskLogging {
		return
	}
	fmt.Printf("[FullLayer] "+format+"\n", args...)
}
