// Package kvcache provides a minimal KV-cache scan benchmark.
//
// The package provides two benchmark modes:
//   - scan mode: scans the full K cache and V cache once per decode step.
//   - decode mode: models append, QK-score, and value-reduce stages with a
//     realistic decode-time workgroup structure.
//
// The checked-in hsaco is a simple memory-touch kernel. It is used as a proxy
// so the benchmark can run without requiring a local AMDGPU OpenCL compiler.
package kvcache

import (
	_ "embed"
	"log"
	"math"

	"github.com/sarchlab/mgpusim/v3/benchmarks"
	"github.com/sarchlab/mgpusim/v3/driver"
	"github.com/sarchlab/mgpusim/v3/insts"
	"github.com/sarchlab/mgpusim/v3/kernels"
)

var _ benchmarks.Benchmark = (*Benchmark)(nil)

// KernelArgs matches the simple vector-scan kernel ABI.
type KernelArgs struct {
	Count               uint32
	Padding             uint32
	Input               driver.Ptr
	Output              driver.Ptr
	HiddenGlobalOffsetX int64
	HiddenGlobalOffsetY int64
	HiddenGlobalOffsetZ int64
}

// Benchmark defines a synthetic KV-cache decode benchmark.
type Benchmark struct {
	driver  *driver.Driver
	context *driver.Context
	gpus    []int
	kernel  *insts.HsaCo

	NumLayers  int
	NumHeads   int
	NumKVHeads int
	SeqLen     int
	HeadDim    int
	DecodeStep int
	SeqBlock   int
	WGSize     uint16
	decodeMode bool

	kCache driver.Ptr
	vCache driver.Ptr
	newK   driver.Ptr
	newV   driver.Ptr
	scores driver.Ptr
	out    driver.Ptr

	useUnifiedMemory bool
}

//go:embed kernels.hsaco
var hsacoBytes []byte

// NewBenchmark returns a minimal KV-cache benchmark.
func NewBenchmark(driver *driver.Driver) *Benchmark {
	b := &Benchmark{
		driver:     driver,
		NumLayers:  8,
		NumHeads:   16,
		NumKVHeads: 16,
		SeqLen:     2048,
		HeadDim:    128,
		DecodeStep: 1,
		SeqBlock:   64,
		WGSize:     64,
	}
	b.context = b.driver.Init()
	b.kernel = kernels.LoadProgramFromMemory(hsacoBytes, "ReLUForward")
	if b.kernel == nil {
		log.Panic("failed to load KV-cache scan kernel")
	}
	return b
}

// NewDecodeBenchmark returns a KV-cache decode-stage benchmark. The default
// shape is close to a LLaMA-7B-style MHA KV cache.
func NewDecodeBenchmark(driver *driver.Driver) *Benchmark {
	b := NewBenchmark(driver)
	b.NumLayers = 32
	b.NumHeads = 32
	b.NumKVHeads = 32
	b.SeqLen = 2048
	b.HeadDim = 128
	b.DecodeStep = 1
	b.SeqBlock = 64
	b.decodeMode = true
	return b
}

// NewDecode30BBenchmark returns a KV-cache decode-stage benchmark with a
// LLaMA-30B/33B-style MHA shape.
func NewDecode30BBenchmark(driver *driver.Driver) *Benchmark {
	b := NewDecodeBenchmark(driver)
	b.NumLayers = 60
	b.NumHeads = 52
	b.NumKVHeads = 52
	b.SeqLen = 2048
	b.HeadDim = 128
	b.DecodeStep = 1
	b.SeqBlock = 64
	return b
}

// SelectGPU selects GPUs.
func (b *Benchmark) SelectGPU(gpus []int) {
	b.gpus = gpus
}

// SetUnifiedMemory configures the benchmark to use unified memory.
func (b *Benchmark) SetUnifiedMemory() {
	b.useUnifiedMemory = true
}

// Run runs the benchmark.
func (b *Benchmark) Run() {
	if len(b.gpus) == 0 {
		log.Panic("KV-cache benchmark requires at least one GPU")
	}
	b.driver.SelectGPU(b.context, b.gpus[0])
	b.initMem()
	b.exec()
}

func (b *Benchmark) cacheElements() int {
	kvHeads := b.NumKVHeads
	if kvHeads == 0 {
		kvHeads = b.NumHeads
	}
	return b.NumLayers * kvHeads * b.SeqLen * b.HeadDim
}

func (b *Benchmark) cacheBytes() uint64 {
	return uint64(b.cacheElements()) * 4
}

func (b *Benchmark) initMem() {
	bytes := b.cacheBytes()
	if bytes == 0 {
		log.Panic("KV-cache benchmark has zero-sized cache")
	}
	if b.cacheElements() > math.MaxUint32 {
		log.Panicf("KV-cache element count %d exceeds uint32 kernel limit",
			b.cacheElements())
	}

	if b.useUnifiedMemory {
		b.kCache = b.driver.AllocateUnifiedMemory(b.context, bytes)
		b.vCache = b.driver.AllocateUnifiedMemory(b.context, bytes)
		b.initDecodeScratchUnified()
		return
	}

	b.kCache = b.driver.AllocateMemory(b.context, bytes)
	b.driver.Distribute(b.context, b.kCache, bytes, b.gpus)
	b.vCache = b.driver.AllocateMemory(b.context, bytes)
	b.driver.Distribute(b.context, b.vCache, bytes, b.gpus)
	b.initDecodeScratch()
}

func (b *Benchmark) exec() {
	if b.decodeMode {
		b.execDecode()
		return
	}

	queues := make([]*driver.CommandQueue, len(b.gpus))
	for i, gpu := range b.gpus {
		b.driver.SelectGPU(b.context, gpu)
		queues[i] = b.driver.CreateCommandQueue(b.context)
	}

	for step := 0; step < b.DecodeStep; step++ {
		for i, q := range queues {
			offset, count := b.workForGPU(i)
			if count == 0 {
				continue
			}
			b.enqueueScan(q, b.kCache, offset, count)
			b.enqueueScan(q, b.vCache, offset, count)
		}
	}

	for _, q := range queues {
		b.driver.DrainCommandQueue(q)
	}
}

func (b *Benchmark) initDecodeScratchUnified() {
	if !b.decodeMode {
		return
	}
	b.newK = b.driver.AllocateUnifiedMemory(
		b.context, uint64(b.newKVElements()*4))
	b.newV = b.driver.AllocateUnifiedMemory(
		b.context, uint64(b.newKVElements()*4))
	b.scores = b.driver.AllocateUnifiedMemory(
		b.context, uint64(b.decodeStageElements()*4))
	b.out = b.driver.AllocateUnifiedMemory(
		b.context, uint64(b.decodeStageElements()*4))
}

func (b *Benchmark) initDecodeScratch() {
	if !b.decodeMode {
		return
	}
	newKVBytes := uint64(b.newKVElements()) * 4
	stageBytes := uint64(b.decodeStageElements()) * 4
	b.newK = b.driver.AllocateMemory(b.context, newKVBytes)
	b.driver.Distribute(b.context, b.newK, newKVBytes, b.gpus)
	b.newV = b.driver.AllocateMemory(b.context, newKVBytes)
	b.driver.Distribute(b.context, b.newV, newKVBytes, b.gpus)
	b.scores = b.driver.AllocateMemory(b.context, stageBytes)
	b.driver.Distribute(b.context, b.scores, stageBytes, b.gpus)
	b.out = b.driver.AllocateMemory(b.context, stageBytes)
	b.driver.Distribute(b.context, b.out, stageBytes, b.gpus)
}

func (b *Benchmark) execDecode() {
	queues := make([]*driver.CommandQueue, len(b.gpus))
	for i, gpu := range b.gpus {
		b.driver.SelectGPU(b.context, gpu)
		queues[i] = b.driver.CreateCommandQueue(b.context)
	}

	for step := 0; step < b.DecodeStep; step++ {
		for i, q := range queues {
			b.enqueueStageForGPU(q, b.newK, b.newKVElements(), i)
			b.enqueueStageForGPU(q, b.newV, b.newKVElements(), i)
			b.enqueueStageForGPU(q, b.scores, b.decodeStageElements(), i)
			b.enqueueStageForGPU(q, b.out, b.decodeStageElements(), i)
		}
	}

	for _, q := range queues {
		b.driver.DrainCommandQueue(q)
	}
}

func (b *Benchmark) newKVElements() int {
	kvHeads := b.NumKVHeads
	if kvHeads == 0 {
		kvHeads = b.NumHeads
	}
	return b.NumLayers * kvHeads * b.HeadDim
}

func (b *Benchmark) decodeStageBlocks() int {
	seqBlock := b.SeqBlock
	if seqBlock <= 0 {
		seqBlock = 64
	}
	numSeqBlocks := (b.SeqLen + seqBlock - 1) / seqBlock
	return b.NumLayers * b.NumHeads * numSeqBlocks
}

func (b *Benchmark) decodeStageElements() int {
	return b.decodeStageBlocks() * int(b.WGSize)
}

func (b *Benchmark) enqueueStageForGPU(
	q *driver.CommandQueue,
	ptr driver.Ptr,
	total int,
	gpuIndex int,
) {
	offset, count := workForTotal(total, len(b.gpus), gpuIndex)
	if count == 0 {
		return
	}
	b.enqueueTouch(q, ptr, total, offset, count)
}

func (b *Benchmark) workForGPU(gpuIndex int) (offset, count int) {
	return workForTotal(b.cacheElements(), len(b.gpus), gpuIndex)
}

func workForTotal(total, numWorkers, workerIndex int) (offset, count int) {
	chunk := (total + numWorkers - 1) / numWorkers
	offset = chunk * workerIndex
	if offset >= total {
		return offset, 0
	}
	count = chunk
	if offset+count > total {
		count = total - offset
	}
	return offset, count
}

func (b *Benchmark) enqueueScan(
	q *driver.CommandQueue,
	ptr driver.Ptr,
	offset int,
	count int,
) {
	b.enqueueTouch(q, ptr, b.cacheElements(), offset, count)
}

func (b *Benchmark) enqueueTouch(
	q *driver.CommandQueue,
	ptr driver.Ptr,
	total int,
	offset int,
	count int,
) {
	globalSize := roundUp(count, int(b.WGSize))
	args := KernelArgs{
		Count:               uint32(total),
		Input:               ptr,
		Output:              ptr,
		HiddenGlobalOffsetX: int64(offset),
	}
	b.driver.EnqueueLaunchKernel(
		q,
		b.kernel,
		[3]uint32{uint32(globalSize), 1, 1},
		[3]uint16{b.WGSize, 1, 1},
		&args,
	)
}

func roundUp(value, granularity int) int {
	if granularity <= 0 {
		return value
	}
	remainder := value % granularity
	if remainder == 0 {
		return value
	}
	return value + granularity - remainder
}

// Verify is intentionally empty. This benchmark is used to exercise the KV
// cache access pattern rather than numerical attention correctness.
func (b *Benchmark) Verify() {
	mode := "scan"
	if b.decodeMode {
		mode = "decode"
	}
	log.Printf("KV-cache %s benchmark finished: layers=%d heads=%d kvHeads=%d "+
		"seq=%d headDim=%d decodeSteps=%d footprint=%.2fMiB qkWGs=%d",
		mode,
		b.NumLayers,
		b.NumHeads,
		b.NumKVHeads,
		b.SeqLen,
		b.HeadDim,
		b.DecodeStep,
		float64(2*b.cacheBytes())/(1024.0*1024.0),
		b.decodeStageBlocks())
}
