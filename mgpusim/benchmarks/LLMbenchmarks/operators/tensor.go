package operators

import "github.com/sarchlab/mgpusim/v3/driver"

// Tensor is the local tensor interface used by LLMbenchmarks.
//
// This is merged from the older dnn/tensor package so LLM workloads can use
// tensor and GPU operator functionality without importing the dnn benchmark
// tree.
type Tensor interface {
	// Dim returns the number of dimensions of the tensor.
	Dim() int

	// NumElement returns the total number of elements in the tensor.
	NumElement() int

	// Size returns the length of the tensor in each dimension, from the
	// outermost dimension to the innermost dimension.
	Size() []int

	// SetSize sets the size of the tensor.
	SetSize([]int)

	// Vector returns the data of the tensor represented in a pure vector.
	// Here, we use float64. However, the concrete tensor implementation can
	// use lower-precision numbers.
	Vector() []float64

	// Descriptor represents what each dimension of the tensor represents.
	//
	// Tokens that represent the meaning of the dimensions include N, C, H, W.
	Descriptor() string

	// SetDescriptor sets the descriptor of the tensor.
	SetDescriptor(d string)
}

// SimpleTensor is a CPU-side multi-dimensional tensor.
type SimpleTensor struct {
	size       []int
	data       []float64
	descriptor string
}

// Dim returns the number of dimensions that the tensor has.
func (t SimpleTensor) Dim() int {
	return len(t.size)
}

// NumElement returns the total number of elements in the tensor.
func (t SimpleTensor) NumElement() int {
	n := 1

	for _, s := range t.size {
		n *= s
	}

	return n
}

// Size returns the size of the tensor.
func (t SimpleTensor) Size() []int {
	return t.size
}

// SetSize sets the size of the tensor.
func (t *SimpleTensor) SetSize(newSize []int) {
	t.size = newSize
}

// Vector returns the raw data of the tensor.
func (t SimpleTensor) Vector() []float64 {
	return t.data
}

// Descriptor returns the descriptor of the tensor.
func (t SimpleTensor) Descriptor() string {
	return t.descriptor
}

// SetDescriptor sets the descriptor of the tensor.
func (t *SimpleTensor) SetDescriptor(d string) {
	t.descriptor = d
}

// GPUTensor is a GPU-side multi-dimensional tensor.
type GPUTensor struct {
	driver *driver.Driver
	ctx    *driver.Context

	size []int
	ptr  driver.Ptr

	descriptor string
}

// Dim returns the number of dimensions that the tensor has.
func (t GPUTensor) Dim() int {
	return len(t.size)
}

// NumElement returns the number of elements in the tensor.
func (t GPUTensor) NumElement() int {
	n := 1

	for _, d := range t.size {
		n *= d
	}

	return n
}

// Size returns the size of the tensor on each dimension.
func (t GPUTensor) Size() []int {
	return t.size
}

// SetSize sets the size of the tensor.
func (t *GPUTensor) SetSize(s []int) {
	t.size = make([]int, len(s))
	copy(t.size, s)
}

// Descriptor returns the descriptor of the tensor.
func (t GPUTensor) Descriptor() string {
	return t.descriptor
}

// SetDescriptor sets the descriptor of the tensor.
func (t *GPUTensor) SetDescriptor(d string) {
	t.descriptor = d
}

// Vector copies the data from the GPU to the simulator.
func (t *GPUTensor) Vector() []float64 {
	raw := make([]float32, t.NumElement())

	t.driver.MemCopyD2H(t.ctx, raw, t.ptr)

	out := make([]float64, t.NumElement())
	for i := 0; i < t.NumElement(); i++ {
		out[i] = float64(raw[i])
	}

	return out
}

// Ptr returns the GPU pointer of the tensor.
func (t *GPUTensor) Ptr() driver.Ptr {
	return t.ptr
}
