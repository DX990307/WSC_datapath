package layers

import (
	"fmt"
	"math/rand"

	"github.com/sarchlab/mgpusim/v3/benchmarks/TensorParallelismSample/tensor"
	"github.com/sarchlab/mgpusim/v3/driver"
)

type GPUstatus struct {
	GPUID     int
	GPUCanUse bool
}

// Conv2D is a regular convolutional layer.
type Conv2D struct {
	layerIndex int
	To         tensor.Operator

	inputSize, outputSize   []int
	kernelSize              []int
	stride                  []int
	padding                 []int
	im2ColMatrixList        []tensor.Tensor
	weightMatrixList        []tensor.Tensor
	biasMatrixList          []tensor.Tensor
	biasMatrixTransposeList []tensor.Tensor
	outputMatrixList        []tensor.Tensor
	outputTransposeList     []tensor.Tensor

	parameters tensor.Tensor
	weights    tensor.Tensor
	bias       tensor.Tensor

	gpu1Status GPUstatus
	gpu2Status GPUstatus
	gpu3Status GPUstatus
	gpu4Status GPUstatus

	ForwardInput tensor.Tensor

	QueueList []*driver.CommandQueue
}

// NewConv2D creates a new Conv2D layer.
func NewConv2D(
	index int,
	To tensor.Operator,
	inputSize, kernelSize, stride, padding []int, loop int,
) *Conv2D {
	argumentsMustBeValid(inputSize, kernelSize, stride, padding)

	l := &Conv2D{
		layerIndex:              index,
		To:                      To,
		inputSize:               inputSize,
		kernelSize:              kernelSize,
		stride:                  stride,
		padding:                 padding,
		gpu1Status:              GPUstatus{GPUID: 49, GPUCanUse: true},
		gpu2Status:              GPUstatus{GPUID: 50, GPUCanUse: true},
		gpu3Status:              GPUstatus{GPUID: 51, GPUCanUse: true},
		gpu4Status:              GPUstatus{GPUID: 52, GPUCanUse: true},
		im2ColMatrixList:        make([]tensor.Tensor, loop),
		weightMatrixList:        make([]tensor.Tensor, loop),
		biasMatrixList:          make([]tensor.Tensor, loop),
		biasMatrixTransposeList: make([]tensor.Tensor, loop),
		outputMatrixList:        make([]tensor.Tensor, loop),
		outputTransposeList:     make([]tensor.Tensor, loop),
		QueueList:               make([]*driver.CommandQueue, 0),
	}

	l.calculateOutputSize()
	l.allocateBuffers()

	return l
}

func (l *Conv2D) allocateBuffers() {
	l.parameters = l.To.Create([]int{l.numParam()}, 49)
	l.weights = l.To.Slice(l.parameters, 0, l.numWeight())
	l.bias = l.To.Slice(l.parameters, l.numWeight(), l.numWeight()+l.numBias())
}

func (l *Conv2D) numParam() int {
	return l.numWeight() + l.numBias()
}

func (l *Conv2D) numWeight() int {
	return l.kernelSize[0] * l.kernelSize[1] * l.kernelSize[2] * l.kernelSize[3]
}

func (l *Conv2D) numBias() int {
	return l.kernelSize[0]
}

// Randomize will randomly initialize the layer parmeters.
func (l *Conv2D) Randomize() {
	// numWeightPerKernel := l.numWeight() / l.kernelSize[0]
	weights := make([]float64, l.numWeight())
	for i := 0; i < l.numWeight(); i++ {
		weights[i] = (rand.Float64() - 0.5) / float64(l.numWeight())
	}
	l.To.Init(l.weights, weights)

	numBias := l.numBias()
	bias := make([]float64, numBias)
	for i := 0; i < numBias; i++ {
		bias[i] = rand.Float64()*2 - 1
	}
	l.To.Init(l.bias, bias)
}

// // Gradients returns all the gradients of the layer.
// func (l *Conv2D) Gradients() tensor.Tensor {
// 	return l.gradients
// }

// Parameters returns all the parameters of the layer.
func (l *Conv2D) Parameters() tensor.Tensor {
	return l.parameters
}

func (l *Conv2D) calculateOutputSize() {
	height := (l.inputSize[1]-l.kernelSize[2]+2*l.padding[0])/l.stride[0] + 1
	width := (l.inputSize[2]-l.kernelSize[3]+2*l.padding[1])/l.stride[1] + 1
	channel := l.kernelSize[0]
	l.outputSize = []int{channel, height, width}
}

func argumentsMustBeValid(inputSize, kernelSize, stride, padding []int) {
	inputOutputMustBe3D(inputSize)
	kernelMustBe4D(kernelSize)
	inputChannelMustMatchKernelChannel(inputSize, kernelSize)
	inputImageShouldNotBeSmallerThanKernel(inputSize, kernelSize)
	strideMustBe2D(stride)
	paddingMustBe2D(padding)
}

func inputOutputMustBe3D(size []int) {
	if len(size) != 3 {
		panic("input or output must be 3D (channel, height, width).")
	}
}

func kernelMustBe4D(size []int) {
	if len(size) != 4 {
		panic("kernel must be 4D (out channel, in channel, height, width)")
	}
}

func inputChannelMustMatchKernelChannel(inputSize, kernelSize []int) {
	if inputSize[0] != kernelSize[1] {
		panic("input channel size does not match the 2nd dimension of the kernel.")
	}
}

func strideMustBe2D(stride []int) {
	if len(stride) != 2 {
		panic("stride must be 2D (vertical stride, horizontal stride)")
	}
}

func paddingMustBe2D(padding []int) {
	if len(padding) != 2 {
		panic("stride must have 2 numbers (vertical padding, horizontal padding)")
	}
}

func inputImageShouldNotBeSmallerThanKernel(inputSize, kernelSize []int) {
	if inputSize[1] < kernelSize[2] {
		panic("input height is smaller than kernel height")
	}

	if inputSize[2] < kernelSize[3] {
		panic("input width is smaller than kernel width")
	}
}

// Forward calculates the forward propagation results of the layer.
func (l *Conv2D) Forward(input tensor.Tensor) (tensor.Tensor, *driver.CommandQueue) {

	im2ColMatrix, _ := l.To.Im2Col(input,
		[]int{l.kernelSize[2], l.kernelSize[3]},
		l.padding, l.stride, []int{1, 1}, 49, 49)
	weightMatrix := l.To.Reshape(l.weights,
		[]int{l.kernelSize[0], im2ColMatrix.Size()[0]})

	biasMatrix := l.To.Repeat(l.bias, im2ColMatrix.Size()[1])
	biasMatrix.SetSize([]int{im2ColMatrix.Size()[1], l.kernelSize[0]})
	biasMatrixTranspose, _ := l.To.Transpose(biasMatrix, []int{1, 0}, 49, 49)
	// biasMatrixTranspose := l.to.Zeros(
	// []int{l.kernelSize[0], im2ColMatrix.Size()[1]})

	outputMatrix, _ := l.To.Gemm(false, false, 1.0, 1.0,
		weightMatrix, im2ColMatrix, biasMatrixTranspose, 49, 49)

	outputMatrix.SetSize(
		[]int{
			l.kernelSize[0],
			input.Size()[0],
			l.outputSize[1],
			l.outputSize[2],
		})
	outputTranspose, _ := l.To.Transpose(outputMatrix, []int{1, 0, 2, 3}, 49, 49)
	outputTranspose.SetDescriptor("NCHW")

	// l.to.Free(im2ColMatrix)
	// l.to.Free(weightMatrix)
	// l.to.Free(biasMatrix)
	// l.to.Free(biasMatrixTranspose)
	// l.to.Free(outputMatrix)

	return outputTranspose, nil
}

func (l *Conv2D) Execute(input tensor.Tensor, loop int, i int) {
	fmt.Printf("i: %d\n", i)
	for gpuid := 49; gpuid <= 52; gpuid++ {
		if gpuid == l.gpu1Status.GPUID && i < loop-3 {
			// l.ForwardInput = l.To.Clone(input)
			im2ColMatrix, queue := l.To.Im2Col(input,
				[]int{l.kernelSize[2], l.kernelSize[3]},
				l.padding, l.stride, []int{1, 1}, 49, 49)
			l.im2ColMatrixList[i] = im2ColMatrix
			l.QueueList = append(l.QueueList, queue)
		}
		if gpuid == l.gpu2Status.GPUID && i < loop-2 && i > 0 {
			l.weightMatrixList[i-1] = l.To.Reshape(l.weights,
				[]int{l.kernelSize[0], l.im2ColMatrixList[i-1].Size()[0]})
			biasMatrix := l.To.Repeat(l.bias, l.im2ColMatrixList[i-1].Size()[1])
			biasMatrix.SetSize([]int{l.im2ColMatrixList[i-1].Size()[1], l.kernelSize[0]})
			biasMatrixTranspose, queue := l.To.Transpose(biasMatrix, []int{1, 0}, 50, 50)
			l.biasMatrixList[i-1] = biasMatrix
			l.biasMatrixTransposeList[i-1] = biasMatrixTranspose
			l.QueueList = append(l.QueueList, queue)
		}
		if gpuid == l.gpu3Status.GPUID && i < loop-1 && i > 1 {
			outputMatrix, queue := l.To.Gemm(false, false, 1.0, 1.0, l.weightMatrixList[i-2],
				l.im2ColMatrixList[i-2], l.biasMatrixTransposeList[i-2], 51, 51)
			l.outputMatrixList[i-2] = outputMatrix
			for j := range queue {
				if queue[j] != nil {
					l.QueueList = append(l.QueueList, queue[j])
				}
			}
		}
		if gpuid == l.gpu4Status.GPUID && i < loop && i > 2 {
			l.outputMatrixList[i-3].SetSize(
				[]int{
					l.kernelSize[0],
					input.Size()[0],
					l.outputSize[1],
					l.outputSize[2],
				})
			outputTranspose, queue := l.To.Transpose(l.outputMatrixList[i-3], []int{1, 0, 2, 3}, 52, 52)
			l.outputTransposeList[i-3] = outputTranspose
			l.QueueList = append(l.QueueList, queue)

		}
		if i == loop {
			for j := range l.outputTransposeList {
				l.outputTransposeList[j].SetDescriptor("NCHW")
			}
		}

	}

}
