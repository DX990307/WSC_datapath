// Package llmop runs one synthetic LLM operator per benchmark invocation.
package llmop

import (
	"flag"
	"log"

	"github.com/sarchlab/mgpusim/v3/benchmarks"
	"github.com/sarchlab/mgpusim/v3/benchmarks/LLMbenchmarks/operators"
	"github.com/sarchlab/mgpusim/v3/driver"
)

var _ benchmarks.Benchmark = (*Benchmark)(nil)

var (
	opFlag = flag.String(
		"op", "linear",
		"LLM operator: embedding, linear, split-linear, layernorm, gelu, residual-add, row-softmax, causal-mask, attention, causal-attention, mlp, batchnorm2d, or transfer.")
	rowsFlag = flag.Int(
		"rows", 1, "Operator row count.")
	hiddenFlag = flag.Int(
		"hidden", 1, "Hidden dimension.")
	inputDimFlag = flag.Int(
		"input-dim", 1, "Linear input dimension.")
	outputDimFlag = flag.Int(
		"output-dim", 1, "Linear output dimension.")
	splitKFlag = flag.Int(
		"split-k", 1, "Split-K count for split-linear.")
	elementsFlag = flag.Int(
		"elements", 0, "Element count for elementwise operators.")
	colsFlag = flag.Int(
		"cols", 0, "Column count for row-softmax.")
	seqLenFlag = flag.Int(
		"seq-len", 0, "Sequence length for attention-like operators.")
	batchSizeFlag = flag.Int(
		"batch-size", 1, "Batch size for attention-like operators.")
	numHeadsFlag = flag.Int(
		"num-heads", 1, "Attention head count.")
	intermediateFlag = flag.Int(
		"intermediate", 0, "MLP intermediate dimension.")
	transferBytesFlag = flag.Int(
		"transfer-bytes", 0, "Synthetic transfer size in bytes.")
	srcGPUsFlag = flag.String(
		"src-gpus", "", "Source GPU placement metadata for transfer ops.")
	dstGPUsFlag = flag.String(
		"dst-gpus", "", "Destination GPU placement metadata for transfer ops.")
	copyGPUsFlag = flag.String(
		"copy-gpus", "", "Copy GPU placement metadata for transfer ops.")
	logSubtasksFlag = flag.Bool(
		"llmop-log-subtasks", false, "Print LLM operator subtask progress.")
)

// Benchmark executes a single synthetic LLM operator.
type Benchmark struct {
	driver *driver.Driver
	ctx    *driver.Context
	gpus   []int

	to  *operators.GPUOperator
	ops *operators.Operator

	useUnifiedMemory bool
}

// NewBenchmarkFromFlags creates an llmop benchmark configured by command flags.
func NewBenchmarkFromFlags(driver *driver.Driver) *Benchmark {
	return &Benchmark{
		driver: driver,
		ctx:    driver.Init(),
	}
}

// SelectGPU selects GPUs.
func (b *Benchmark) SelectGPU(gpus []int) {
	b.gpus = gpus
}

// SetUnifiedMemory records the unified memory preference.
func (b *Benchmark) SetUnifiedMemory() {
	b.useUnifiedMemory = true
}

// Run executes the configured operator.
func (b *Benchmark) Run() {
	if len(b.gpus) == 0 {
		log.Panic("llmop benchmark requires at least one GPU")
	}

	b.driver.SelectGPU(b.ctx, b.gpus[0])
	b.to = operators.NewGPUOperator(b.driver, b.ctx)
	b.to.ReportTime()
	b.ops = operators.NewOperator(
		b.driver, b.ctx, b.to, "LLMOp", *logSubtasksFlag)

	switch *opFlag {
	case "embedding":
		b.runEmbedding()
	case "linear":
		b.runLinear()
	case "split-linear":
		b.runSplitLinear()
	case "layernorm":
		b.runLayerNorm()
	case "gelu":
		b.runGELU()
	case "residual-add":
		b.runResidualAdd()
	case "row-softmax":
		b.runRowSoftmax()
	case "causal-mask":
		b.runCausalMask()
	case "attention":
		b.runAttention(false)
	case "causal-attention":
		b.runAttention(true)
	case "mlp":
		b.runMLP()
	case "batchnorm2d":
		b.runBatchNorm2D()
	case "transfer":
		b.runTransfer()
	default:
		log.Panicf("unknown -op %q", *opFlag)
	}
}

// Verify is intentionally empty for synthetic operator benchmarks.
func (b *Benchmark) Verify() {
}

func (b *Benchmark) runEmbedding() {
	out := b.ops.Embedding("embedding", positive(*rowsFlag, "rows"), positive(*hiddenFlag, "hidden"))
	b.ops.Free(out)
}

func (b *Benchmark) runLinear() {
	rows := positive(*rowsFlag, "rows")
	inputDim := positive(*inputDimFlag, "input-dim")
	outputDim := positive(*outputDimFlag, "output-dim")
	input := b.to.Zeros([]int{rows, inputDim})
	out := b.ops.Linear("linear", input, rows, inputDim, outputDim)
	b.ops.Free(input)
	b.ops.Free(out)
}

func (b *Benchmark) runSplitLinear() {
	rows := positive(*rowsFlag, "rows")
	inputDim := positive(*inputDimFlag, "input-dim")
	outputDim := positive(*outputDimFlag, "output-dim")
	splitK := positive(*splitKFlag, "split-k")
	input := b.to.Zeros([]int{rows, inputDim})
	out := b.ops.SplitKLinear("split-linear", input, rows, inputDim, outputDim, splitK)
	b.ops.Free(input)
	b.ops.Free(out)
}

func (b *Benchmark) runLayerNorm() {
	rows := positive(*rowsFlag, "rows")
	hidden := positive(*hiddenFlag, "hidden")
	input := b.to.Zeros([]int{rows, hidden})
	out := b.ops.LayerNorm("layernorm", input, rows, hidden)
	b.ops.Free(input)
	b.ops.Free(out)
}

func (b *Benchmark) runGELU() {
	input := b.to.Zeros([]int{elementCount()})
	out := b.ops.GELU("gelu", input)
	b.ops.Free(input)
	b.ops.Free(out)
}

func (b *Benchmark) runResidualAdd() {
	elements := elementCount()
	a := b.to.Zeros([]int{elements})
	c := b.to.Zeros([]int{elements})
	out := b.ops.ResidualAdd("residual-add", a, c)
	b.ops.Free(a)
	b.ops.Free(c)
	b.ops.Free(out)
}

func (b *Benchmark) runRowSoftmax() {
	rows := positive(*rowsFlag, "rows")
	cols := *colsFlag
	if cols <= 0 {
		cols = rows
	}
	input := b.to.Zeros([]int{rows, cols})
	out := b.ops.RowSoftmax(input, rows, cols)
	b.ops.Free(input)
	b.ops.Free(out)
}

func (b *Benchmark) runCausalMask() {
	rows := positive(*rowsFlag, "rows")
	seqLen := *seqLenFlag
	if seqLen <= 0 {
		seqLen = rows
	}
	batchSize := positive(*batchSizeFlag, "batch-size")
	scores := b.to.Zeros([]int{rows, rows})
	b.ops.ApplyCausalMask(scores, rows, seqLen, batchSize)
	b.ops.Free(scores)
}

func (b *Benchmark) runAttention(causal bool) {
	rows := positive(*rowsFlag, "rows")
	hidden := positive(*hiddenFlag, "hidden")
	seqLen := *seqLenFlag
	if seqLen <= 0 {
		seqLen = rows
	}
	batchSize := positive(*batchSizeFlag, "batch-size")
	numHeads := positive(*numHeadsFlag, "num-heads")
	input := b.to.Zeros([]int{rows, hidden})
	out := b.ops.SelfAttention(
		"attention", input, rows, hidden, numHeads, seqLen, batchSize, causal)
	b.ops.Free(input)
	b.ops.Free(out)
}

func (b *Benchmark) runMLP() {
	rows := positive(*rowsFlag, "rows")
	hidden := positive(*hiddenFlag, "hidden")
	intermediate := *intermediateFlag
	if intermediate <= 0 {
		intermediate = positive(*outputDimFlag, "output-dim")
	}
	input := b.to.Zeros([]int{rows, hidden})
	out := b.ops.MLP("mlp", input, rows, hidden, intermediate)
	b.ops.Free(input)
	b.ops.Free(out)
}

func (b *Benchmark) runBatchNorm2D() {
	rows := positive(*rowsFlag, "rows")
	hidden := positive(*hiddenFlag, "hidden")
	seqLen := *seqLenFlag
	if seqLen <= 0 {
		seqLen = 1
	}
	cols := *colsFlag
	if cols <= 0 {
		cols = seqLen
	}
	input := b.to.Zeros([]int{rows, hidden, seqLen, cols})
	out := b.ops.BatchNorm2DInference("batchnorm2d", input)
	b.ops.Free(input)
	b.ops.Free(out)
}

func (b *Benchmark) runTransfer() {
	_ = *srcGPUsFlag
	_ = *dstGPUsFlag
	_ = *copyGPUsFlag

	bytes := positive(*transferBytesFlag, "transfer-bytes")
	elements := (bytes + 3) / 4
	src := b.to.Zeros([]int{elements})
	dst := b.to.Zeros([]int{elements})
	b.to.Copy(dst, src)
	b.ops.Free(src)
	b.ops.Free(dst)
}

func elementCount() int {
	if *elementsFlag > 0 {
		return *elementsFlag
	}
	return positive(*rowsFlag, "rows") * positive(*hiddenFlag, "hidden")
}

func positive(value int, name string) int {
	if value <= 0 {
		log.Panicf("-%s must be positive, got %d", name, value)
	}
	return value
}
