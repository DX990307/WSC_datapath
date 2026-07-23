package benchmarkselection

import (
	"github.com/sarchlab/mgpusim/v3/benchmarks"
	"github.com/sarchlab/mgpusim/v3/benchmarks/LLMbenchmarks/bert"
	"github.com/sarchlab/mgpusim/v3/benchmarks/LLMbenchmarks/gpt"
	"github.com/sarchlab/mgpusim/v3/benchmarks/LLMbenchmarks/llmop"
	"github.com/sarchlab/mgpusim/v3/benchmarks/LLMbenchmarks/resnet"
	"github.com/sarchlab/mgpusim/v3/benchmarks/TensorParallelismSample/layer_benchmarks/conv2d"
	"github.com/sarchlab/mgpusim/v3/benchmarks/amdappsdk/bitonicsort"
	"github.com/sarchlab/mgpusim/v3/benchmarks/amdappsdk/fastwalshtransform"
	"github.com/sarchlab/mgpusim/v3/benchmarks/amdappsdk/floydwarshall"
	"github.com/sarchlab/mgpusim/v3/benchmarks/amdappsdk/matrixmultiplication"
	"github.com/sarchlab/mgpusim/v3/benchmarks/amdappsdk/matrixtranspose"
	"github.com/sarchlab/mgpusim/v3/benchmarks/amdappsdk/nbody"
	"github.com/sarchlab/mgpusim/v3/benchmarks/amdappsdk/simpleconvolution"
	"github.com/sarchlab/mgpusim/v3/benchmarks/concurrentRunning/btfwt"
	"github.com/sarchlab/mgpusim/v3/benchmarks/concurrentRunning/fwtfws"
	"github.com/sarchlab/mgpusim/v3/benchmarks/concurrentRunning/fwtmt"
	"github.com/sarchlab/mgpusim/v3/benchmarks/concurrentRunning/kmsc"
	"github.com/sarchlab/mgpusim/v3/benchmarks/concurrentRunning/mmfir"
	"github.com/sarchlab/mgpusim/v3/benchmarks/concurrentRunning/prfws"
	"github.com/sarchlab/mgpusim/v3/benchmarks/concurrentRunning/prsc"
	"github.com/sarchlab/mgpusim/v3/benchmarks/concurrentRunning/scsc"
	"github.com/sarchlab/mgpusim/v3/benchmarks/concurrentRunning/spmvmt"

	// "github.com/sarchlab/mgpusim/v3/benchmarks/dnn/layer_benchmarks/conv2d"
	"github.com/sarchlab/mgpusim/v3/benchmarks/dnn/layer_benchmarks/avgpooling"
	"github.com/sarchlab/mgpusim/v3/benchmarks/dnn/layer_benchmarks/fulllayer"
	"github.com/sarchlab/mgpusim/v3/benchmarks/dnn/layer_benchmarks/im2col"
	"github.com/sarchlab/mgpusim/v3/benchmarks/dnn/layer_benchmarks/maxpooling"
	"github.com/sarchlab/mgpusim/v3/benchmarks/dnn/layer_benchmarks/relu"
	"github.com/sarchlab/mgpusim/v3/benchmarks/dnn/training_benchmarks/lenet"
	"github.com/sarchlab/mgpusim/v3/benchmarks/dnn/training_benchmarks/minerva"
	"github.com/sarchlab/mgpusim/v3/benchmarks/dnn/training_benchmarks/vgg16"
	"github.com/sarchlab/mgpusim/v3/benchmarks/heteromark/aes"
	"github.com/sarchlab/mgpusim/v3/benchmarks/heteromark/fir"
	"github.com/sarchlab/mgpusim/v3/benchmarks/heteromark/kmeans"
	"github.com/sarchlab/mgpusim/v3/benchmarks/heteromark/pagerank"
	"github.com/sarchlab/mgpusim/v3/benchmarks/llm/kvcache"
	"github.com/sarchlab/mgpusim/v3/benchmarks/polybench/atax"
	"github.com/sarchlab/mgpusim/v3/benchmarks/polybench/bicg"
	"github.com/sarchlab/mgpusim/v3/benchmarks/rodinia/nw"
	"github.com/sarchlab/mgpusim/v3/benchmarks/shoc/fft"
	"github.com/sarchlab/mgpusim/v3/benchmarks/shoc/spmv"
	"github.com/sarchlab/mgpusim/v3/benchmarks/shoc/stencil2d"
	"github.com/sarchlab/mgpusim/v3/driver"
)

const (
	oneGiB = 1024 * 1024 * 1024

	// Traditional standalone sizes target about 1GiB of device-side data.
	// Some workloads need power-of-two, square, or CSR-shaped inputs, so the
	// target is intentionally approximate rather than byte-exact.
	oneGiBFloat32Elements           = oneGiB / 4
	oneGiBTwoFloat32BuffersElements = oneGiB / (2 * 4)
)

func newConv2DBenchmark(
	gpuDriver *driver.Driver,
	n, c, h, w int,
	kernelChannel, kernelHeight, kernelWidth int,
	padX, padY, strideX, strideY int,
) *conv2d.Benchmark {
	conv2dBenchmark := conv2d.NewBenchmark(gpuDriver)
	conv2dBenchmark.N = n
	conv2dBenchmark.C = c
	conv2dBenchmark.H = h
	conv2dBenchmark.W = w
	conv2dBenchmark.KernelChannel = kernelChannel
	conv2dBenchmark.KernelHeight = kernelHeight
	conv2dBenchmark.KernelWidth = kernelWidth
	conv2dBenchmark.PadX = padX
	conv2dBenchmark.PadY = padY
	conv2dBenchmark.StrideX = strideX
	conv2dBenchmark.StrideY = strideY
	return conv2dBenchmark
}

func SelectBenchmark(name string, driver *driver.Driver) benchmarks.Benchmark {
	var benchmark benchmarks.Benchmark
	switch name {
	case "aes":
		aes := aes.NewBenchmark(driver)
		aes.Length = oneGiB
		benchmark = aes
	case "aes-pipeline-smoke":
		aes := aes.NewBenchmark(driver)
		aes.Length = 1 * 1024 * 1024
		benchmark = aes
	case "atax":
		atax := atax.NewBenchmark(driver)
		atax.NX = 12288
		atax.NY = 12288
		benchmark = atax
	case "bicg":
		bicg := bicg.NewBenchmark(driver)
		bicg.NX = 12288
		bicg.NY = 12288
		benchmark = bicg
	case "bitonicsort":
		bitonicsort := bitonicsort.NewBenchmark(driver)
		bitonicsort.Length = oneGiBFloat32Elements
		benchmark = bitonicsort
	case "bert":
		benchmark = bert.NewBenchmark(driver)
	case "conv2d":
		benchmark = newConv2DBenchmark(driver, 1, 3, 3072, 3072, 3, 4, 4, 0, 0, 1, 1)
	case "conv2d-llm-prefill-pointwise":
		// 1x1 conv as a linear-layer proxy over a prefill-sized token grid:
		// input [tokens=512, hidden=1024] -> output [tokens=512, hidden=4096].
		benchmark = newConv2DBenchmark(driver, 1, 1024, 16, 32, 4096, 1, 1, 0, 0, 1, 1)
	case "conv2d-llm-decode-pointwise":
		// Batched decode proxy with fewer rows while keeping high channel count.
		benchmark = newConv2DBenchmark(driver, 1, 1024, 8, 16, 4096, 1, 1, 0, 0, 1, 1)
	case "conv2d-llm-prefill-local":
		// High-channel 3x3 variant to keep conv2d spatial-neighbor reuse visible.
		benchmark = newConv2DBenchmark(driver, 1, 1024, 16, 32, 1024, 3, 3, 1, 1, 1, 1)
	case "conv2d-llm-decode-local":
		benchmark = newConv2DBenchmark(driver, 1, 1024, 8, 16, 1024, 3, 3, 1, 1, 1, 1)
	case "maxpooling":
		maxpooling := maxpooling.NewBenchmark(driver)
		maxpooling.N = 1
		maxpooling.C = 64
		maxpooling.H = 112
		maxpooling.W = 112
		maxpooling.KernelHeight = 2
		maxpooling.KernelWidth = 2
		maxpooling.PadX = 0
		maxpooling.PadY = 0
		maxpooling.StrideX = 2
		maxpooling.StrideY = 2
		benchmark = maxpooling
	case "avgpooling":
		avgpooling := avgpooling.NewBenchmark(driver)
		avgpooling.N = 1
		avgpooling.C = 512
		avgpooling.H = 7
		avgpooling.W = 7
		avgpooling.KernelHeight = 7
		avgpooling.KernelWidth = 7
		avgpooling.PadX = 0
		avgpooling.PadY = 0
		avgpooling.StrideX = 1
		avgpooling.StrideY = 1
		benchmark = avgpooling
	case "fulllayer":
		fulllayer := fulllayer.NewBenchmark(driver)
		fulllayer.N = 1
		fulllayer.InputDim = 1024
		fulllayer.OutputDim = 100
		benchmark = fulllayer
	case "fulllayer-large":
		fulllayer := fulllayer.NewBenchmark(driver)
		// A runnable large full-layer case for Photon validation. It is
		// much smaller than fulllayer-1gb but still has enough GEMM WGs:
		// ceil(512/16) * ceil(4096/16) = 8192.
		fulllayer.N = 512
		fulllayer.InputDim = 4096
		fulllayer.OutputDim = 4096
		fulllayer.RandomizeParameters = false
		benchmark = fulllayer
	case "fulllayer-gemm-tiny":
		fulllayer := fulllayer.NewBenchmark(driver)
		// A tiny smoke-test case for checking whether the fully connected
		// GEMM path completes at all. The old scalar GEMM kernel is very
		// slow in timing simulation, so keep K small here.
		// Compute: 1 * 128 * 128 = 16K MACs.
		// GEMM workgroups: ceil(1/16) * ceil(128/16) = 8.
		fulllayer.N = 1
		fulllayer.InputDim = 4096
		fulllayer.OutputDim = 4096
		fulllayer.RandomizeParameters = false
		benchmark = fulllayer
	case "fulllayer-gemm-debug":
		fulllayer := fulllayer.NewBenchmark(driver)
		// A small full-layer case for debugging whether GEMM itself
		// completes. Compute: 128 * 2048 * 2048 = 0.54B MACs.
		// GEMM workgroups: ceil(128/16) * ceil(2048/16) = 1024.
		fulllayer.N = 128
		fulllayer.InputDim = 2048
		fulllayer.OutputDim = 2048
		fulllayer.RandomizeParameters = false
		benchmark = fulllayer
	case "fulllayer-7bcompute":
		fulllayer := fulllayer.NewBenchmark(driver)
		// LLaMA-7B-style decode does roughly 6.5B linear-layer MACs per
		// generated token across all transformer layers. This single-GEMM
		// proxy is close to that scale:
		// 416 * 4096 * 4096 = 6.98B MACs.
		// GEMM workgroups: ceil(416/16) * ceil(4096/16) = 6656.
		fulllayer.N = 416
		fulllayer.InputDim = 4096
		fulllayer.OutputDim = 4096
		fulllayer.RandomizeParameters = false
		benchmark = fulllayer
	case "fulllayer-1gb":
		fulllayer := fulllayer.NewBenchmark(driver)
		// This shape uses the generic fully connected layer path. Its
		// persistent parameters and gradients are about 512MiB, while
		// forward-time clone/reshape/GEMM temporaries push peak footprint
		// close to 1GiB.
		fulllayer.N = 1024
		fulllayer.InputDim = 16384
		fulllayer.OutputDim = 4096
		fulllayer.RandomizeParameters = false
		benchmark = fulllayer
	case "fastwalshtransform":
		fastwalshtransform := fastwalshtransform.NewBenchmark(driver)
		fastwalshtransform.Length = oneGiBFloat32Elements
		benchmark = fastwalshtransform
	case "fir":
		fir := fir.NewBenchmark(driver)
		fir.Length = oneGiBTwoFloat32BuffersElements
		benchmark = fir
	case "fft":
		fft := fft.NewBenchmark(driver)
		fft.Bytes = 1024
		fft.Passes = 4
		benchmark = fft
	case "floydwarshall":
		floydwarshall := floydwarshall.NewBenchmark(driver)
		floydwarshall.NumNodes = 11584
		floydwarshall.NumIterations = 1
		benchmark = floydwarshall
	case "gpt":
		benchmark = gpt.NewBenchmark(driver)
	case "im2col":
		im2col := im2col.NewBenchmark(driver)
		im2col.N = 1
		im2col.C = 3
		im2col.H = 3000
		im2col.W = 3000
		im2col.KernelHeight = 3
		im2col.KernelWidth = 3
		im2col.PadX = 0
		im2col.PadY = 0
		im2col.StrideX = 1
		im2col.StrideY = 1
		im2col.DilateX = 1
		im2col.DilateY = 1
		benchmark = im2col
	case "kmeans":
		kmeans := kmeans.NewBenchmark(driver)
		// Preserve the original 32-feature, 8-cluster, two-iteration access
		// pattern while keeping transpose plus both membership-compute phases
		// inside the default 76,800-WG sampled window. 1,572,864 points gives
		// 512 workgroups per GPU per kernel: 3 * 48 * 512 = 73,728 total. The
		// previous 54,525,952-point setup was truncated during transpose and
		// never exercised the compute or inter-iteration reuse phases.
		kmeans.NumPoints = 1572864
		kmeans.NumClusters = 8
		kmeans.NumFeatures = 32
		kmeans.MaxIter = 2
		benchmark = kmeans
	case "kmeans-reuse-smoke":
		kmeans := kmeans.NewBenchmark(driver)
		// Phase-targeted diagnostic input. Setup-time magic copy initializes
		// the transposed feature matrix, then timing covers both membership
		// iterations: 2 * 48 * 8 = 768 workgroups. This isolates remote
		// request aggregation and cross-iteration reuse and is never included
		// in the traditional paper geomean.
		kmeans.NumPoints = 24576
		kmeans.NumClusters = 8
		kmeans.NumFeatures = 32
		kmeans.MaxIter = 2
		kmeans.PreloadTransposedFeatures()
		benchmark = kmeans
	case "kvcache":
		kvcache := kvcache.NewBenchmark(driver)
		kvcache.NumLayers = 8
		kvcache.NumHeads = 16
		kvcache.SeqLen = 2048
		kvcache.HeadDim = 128
		kvcache.DecodeStep = 1
		benchmark = kvcache
	case "kvcache-decode":
		kvcache := kvcache.NewDecodeBenchmark(driver)
		kvcache.NumLayers = 32
		kvcache.NumHeads = 32
		kvcache.NumKVHeads = 32
		kvcache.SeqLen = 2048
		kvcache.HeadDim = 128
		kvcache.SeqBlock = 64
		kvcache.DecodeStep = 1
		benchmark = kvcache
	case "kvcache-decode-30b":
		kvcache := kvcache.NewDecode30BBenchmark(driver)
		kvcache.NumLayers = 60
		kvcache.NumHeads = 52
		kvcache.NumKVHeads = 52
		kvcache.SeqLen = 2048
		kvcache.HeadDim = 128
		kvcache.SeqBlock = 64
		kvcache.DecodeStep = 1
		benchmark = kvcache
	case "llmop":
		benchmark = llmop.NewBenchmarkFromFlags(driver)
	case "matrixmultiplication":
		matrixmultiplication := matrixmultiplication.NewBenchmark(driver)
		matrixmultiplication.X = 2048 / 16
		matrixmultiplication.Y = 2048 * 512
		matrixmultiplication.Z = 2048 / 16
		benchmark = matrixmultiplication
	case "matrixmultiplication-pipeline-smoke":
		matrixmultiplication := matrixmultiplication.NewBenchmark(driver)
		matrixmultiplication.X = 64
		matrixmultiplication.Y = 8192
		matrixmultiplication.Z = 64
		benchmark = matrixmultiplication
	case "matrixmultiplication-llm-prefill-attn":
		matrixmultiplication := matrixmultiplication.NewBenchmark(driver)
		matrixmultiplication.X = 4096
		matrixmultiplication.Y = 1920
		matrixmultiplication.Z = 4096
		benchmark = matrixmultiplication
	case "matrixmultiplication-llm-decode-attn":
		matrixmultiplication := matrixmultiplication.NewBenchmark(driver)
		matrixmultiplication.X = 4096
		matrixmultiplication.Y = 192
		matrixmultiplication.Z = 4096
		benchmark = matrixmultiplication
	case "matrixmultiplication-llm-prefill-mlp-up":
		matrixmultiplication := matrixmultiplication.NewBenchmark(driver)
		matrixmultiplication.X = 4096
		matrixmultiplication.Y = 1920
		matrixmultiplication.Z = 11008
		benchmark = matrixmultiplication
	case "matrixmultiplication-llm-decode-mlp-up":
		matrixmultiplication := matrixmultiplication.NewBenchmark(driver)
		matrixmultiplication.X = 4096
		matrixmultiplication.Y = 192
		matrixmultiplication.Z = 11008
		benchmark = matrixmultiplication
	case "matrixmultiplication-llm-prefill-mlp-down":
		matrixmultiplication := matrixmultiplication.NewBenchmark(driver)
		matrixmultiplication.X = 11008
		matrixmultiplication.Y = 1920
		matrixmultiplication.Z = 4096
		benchmark = matrixmultiplication
	case "matrixmultiplication-llm-decode-mlp-down":
		matrixmultiplication := matrixmultiplication.NewBenchmark(driver)
		matrixmultiplication.X = 11008
		matrixmultiplication.Y = 192
		matrixmultiplication.Z = 4096
		benchmark = matrixmultiplication
	case "matrixmultiplication-middletile":
		matrixmultiplication := matrixmultiplication.NewMiddleTileBenchmark(driver)
		matrixmultiplication.X = 256
		matrixmultiplication.Y = 2048 * 256
		matrixmultiplication.Z = 256
		benchmark = matrixmultiplication
	case "matrixmultiplication-middletile-pipeline-smoke":
		matrixmultiplication := matrixmultiplication.NewMiddleTileBenchmark(driver)
		matrixmultiplication.X = 64
		matrixmultiplication.Y = 8192
		matrixmultiplication.Z = 64
		benchmark = matrixmultiplication
	case "matrixtranspose":
		matrixtranspose := matrixtranspose.NewBenchmark(driver)
		matrixtranspose.Width = 11520
		// matrixtranspose.Width = 4096 * 4
		// matrixtranspose.Width = 4096 / 2
		benchmark = matrixtranspose
	case "matrixtranspose-pipeline-smoke":
		matrixtranspose := matrixtranspose.NewBenchmark(driver)
		matrixtranspose.Width = 1024
		benchmark = matrixtranspose
	case "matrixtranspose-middletile":
		matrixtranspose := matrixtranspose.NewMiddleTileBenchmark(driver)
		matrixtranspose.Width = 8192 * 2
		benchmark = matrixtranspose
	case "nbody":
		nbody := nbody.NewBenchmark(driver)
		nbody.NumParticles = 104857600 * 2
		nbody.NumIterations = 1024
		benchmark = nbody
	case "nw":
		nw := nw.NewBenchmark(driver)
		nw.SetLength(6144)
		benchmark = nw
	case "pagerank":
		pagerank := pagerank.NewBenchmark(driver)
		pagerank.NumNodes = 1 << 20
		pagerank.NumConnections = 132644864
		pagerank.MaxIterations = 1
		benchmark = pagerank
	case "pagerank-pipeline-smoke":
		pagerank := pagerank.NewBenchmark(driver)
		pagerank.NumNodes = 1 << 13
		pagerank.NumConnections = 1 << 16
		pagerank.MaxIterations = 1
		benchmark = pagerank
	case "relu":
		relu := relu.NewBenchmark(driver)
		relu.Length = 10485760 * 32
		benchmark = relu
	case "resnet":
		benchmark = resnet.NewBenchmark(driver)
	case "simpleconvolution":
		simpleconvolution := simpleconvolution.NewBenchmark(driver)
		// Input plus output occupy approximately 512 MiB. Retain the 3x3 mask
		// so the benchmark still exposes adjacent-line convolution accesses.
		// The padded grid is also divisible by 48 GPUs * 64 work-items.
		simpleconvolution.Height = 8188
		simpleconvolution.Width = 8190
		simpleconvolution.SetMaskSize(3)
		benchmark = simpleconvolution
	case "spmv":
		spmv := spmv.NewBenchmark(driver)
		spmv.Dim = 1 << 20 * 85
		spmv.Sparsity = 0.000000001
		benchmark = spmv
	case "stencil2d":
		stencil2d := stencil2d.NewBenchmark(driver)
		stencil2d.NumRows = 8192
		stencil2d.NumCols = 16384
		stencil2d.NumIteration = 3
		benchmark = stencil2d
	case "lenet":
		lenet := lenet.NewBenchmark(driver)
		lenet.Epoch = 1
		lenet.MaxBatchPerEpoch = 2
		lenet.BatchSize = 32
		lenet.EnableTesting = false
		lenet.EnableVerification = false
		benchmark = lenet
	case "minerva":
		minerva := minerva.NewBenchmark(driver)
		minerva.Epoch = 1
		minerva.MaxBatchPerEpoch = 2
		minerva.BatchSize = 32
		minerva.EnableTesting = false
		minerva.EnableVerification = false
		benchmark = minerva
	case "vgg16":
		vgg16 := vgg16.NewBenchmark(driver)
		vgg16.Epoch = 1
		vgg16.MaxBatchPerEpoch = 2
		vgg16.BatchSize = 8
		vgg16.EnableTesting = false
		vgg16.EnableVerification = false
		benchmark = vgg16
	case "prfws":
		// This is a concurrent running benchmark that runs pagerank and fast walsh
		prfws := prfws.NewBenchmark(driver)
		benchmark = prfws

	case "btfwt":
		// This is a concurrent running benchmark that runs pagerank and fast walsh
		btfwt := btfwt.NewBenchmark(driver)
		benchmark = btfwt

	case "fwtfws":
		// This is a concurrent running benchmark that runs pagerank and fast walsh
		fwtfws := fwtfws.NewBenchmark(driver)
		benchmark = fwtfws

	case "kmsc":
		// This is a concurrent running benchmark that runs pagerank and fast walsh
		kmsc := kmsc.NewBenchmark(driver)
		benchmark = kmsc
	case "scsc":
		// This is a concurrent running benchmark that runs pagerank and fast walsh
		scsc := scsc.NewBenchmark(driver)
		benchmark = scsc
	case "mmfir":
		// This is a concurrent running benchmark that runs pagerank and fast walsh
		mmfir := mmfir.NewBenchmark(driver)
		benchmark = mmfir
	case "prsc":
		// This is a concurrent running benchmark that runs pagerank and fast walsh
		prsc := prsc.NewBenchmark(driver)
		benchmark = prsc
	case "fwtmt":
		// This is a concurrent running benchmark that runs fast walsh and matrix transpose
		fwtmt := fwtmt.NewBenchmark(driver)
		benchmark = fwtmt
	case "spmvmt":
		// This is a concurrent running benchmark that runs sparse matrix vector multiplication and matrix transpose
		spmvmt := spmvmt.NewBenchmark(driver)
		benchmark = spmvmt

	default:
		panic("Unknown benchmark")
	}

	return benchmark
}

// package benchmarkselection

// import (
// 	"github.com/sarchlab/mgpusim/v3/benchmarks"
// 	"github.com/sarchlab/mgpusim/v3/benchmarks/amdappsdk/bitonicsort"
// 	"github.com/sarchlab/mgpusim/v3/benchmarks/amdappsdk/fastwalshtransform"
// 	"github.com/sarchlab/mgpusim/v3/benchmarks/amdappsdk/floydwarshall"
// 	"github.com/sarchlab/mgpusim/v3/benchmarks/amdappsdk/matrixmultiplication"
// 	"github.com/sarchlab/mgpusim/v3/benchmarks/amdappsdk/matrixtranspose"
// 	"github.com/sarchlab/mgpusim/v3/benchmarks/amdappsdk/nbody"
// 	"github.com/sarchlab/mgpusim/v3/benchmarks/amdappsdk/simpleconvolution"
// 	"github.com/sarchlab/mgpusim/v3/benchmarks/dnn/layer_benchmarks/conv2d"
// 	"github.com/sarchlab/mgpusim/v3/benchmarks/dnn/layer_benchmarks/im2col"
// 	"github.com/sarchlab/mgpusim/v3/benchmarks/dnn/layer_benchmarks/relu"
// 	"github.com/sarchlab/mgpusim/v3/benchmarks/dnn/training_benchmarks/lenet"
// 	"github.com/sarchlab/mgpusim/v3/benchmarks/dnn/training_benchmarks/minerva"
// 	"github.com/sarchlab/mgpusim/v3/benchmarks/dnn/training_benchmarks/vgg16"
// 	"github.com/sarchlab/mgpusim/v3/benchmarks/heteromark/aes"
// 	"github.com/sarchlab/mgpusim/v3/benchmarks/heteromark/fir"
// 	"github.com/sarchlab/mgpusim/v3/benchmarks/heteromark/kmeans"
// 	"github.com/sarchlab/mgpusim/v3/benchmarks/heteromark/pagerank"
// 	"github.com/sarchlab/mgpusim/v3/benchmarks/polybench/atax"
// 	"github.com/sarchlab/mgpusim/v3/benchmarks/polybench/bicg"
// 	"github.com/sarchlab/mgpusim/v3/benchmarks/rodinia/nw"
// 	"github.com/sarchlab/mgpusim/v3/benchmarks/shoc/fft"
// 	"github.com/sarchlab/mgpusim/v3/benchmarks/shoc/spmv"
// 	"github.com/sarchlab/mgpusim/v3/benchmarks/shoc/stencil2d"
// 	"github.com/sarchlab/mgpusim/v3/driver"
// )

// func SelectBenchmark(name string, driver *driver.Driver) benchmarks.Benchmark {
// 	var benchmark benchmarks.Benchmark
// 	switch name {
// 	case "aes":
// 		aes := aes.NewBenchmark(driver)
// 		aes.Length = 1048576 * 4
// 		benchmark = aes
// 	case "atax":
// 		atax := atax.NewBenchmark(driver)
// 		atax.NX = 4096
// 		atax.NY = 4096
// 		benchmark = atax
// 	case "bicg":
// 		bicg := bicg.NewBenchmark(driver)
// 		bicg.NX = 4096 * 4
// 		bicg.NY = 4096 * 4
// 		benchmark = bicg
// 	case "bitonicsort":
// 		bitonicsort := bitonicsort.NewBenchmark(driver)
// 		bitonicsort.Length = 65536 * 4
// 		benchmark = bitonicsort
// 	case "conv2d":
// 		conv2d := conv2d.NewBenchmark(driver)
// 		conv2d.N = 8 / 2
// 		conv2d.C = 3
// 		conv2d.H = 300
// 		conv2d.W = 300
// 		conv2d.KernelChannel = 6
// 		conv2d.KernelHeight = 3
// 		conv2d.KernelWidth = 3
// 		conv2d.PadX = 1
// 		conv2d.PadY = 1
// 		conv2d.StrideX = 1
// 		conv2d.StrideY = 1
// 		benchmark = conv2d
// 	case "fastwalshtransform":
// 		fastwalshtransform := fastwalshtransform.NewBenchmark(driver)
// 		fastwalshtransform.Length = 1048576 * 8
// 		benchmark = fastwalshtransform
// 	case "fir":
// 		fir := fir.NewBenchmark(driver)
// 		fir.Length = 1048576 * 16
// 		// fir.Length = 1024 * 16 * 8
// 		benchmark = fir
// 	case "fft":
// 		fft := fft.NewBenchmark(driver)
// 		fft.Bytes = 128
// 		fft.Passes = 4
// 		benchmark = fft
// 	case "floydwarshall":
// 		floydwarshall := floydwarshall.NewBenchmark(driver)
// 		floydwarshall.NumNodes = 1024 * 2
// 		floydwarshall.NumIterations = 1024 / 256
// 		benchmark = floydwarshall
// 	case "im2col":
// 		im2col := im2col.NewBenchmark(driver)
// 		im2col.N = 16 / 8
// 		im2col.C = 3
// 		im2col.H = 256
// 		im2col.W = 256
// 		im2col.KernelHeight = 3
// 		im2col.KernelWidth = 3
// 		im2col.PadX = 0
// 		im2col.PadY = 0
// 		im2col.StrideX = 1
// 		im2col.StrideY = 1
// 		im2col.DilateX = 1
// 		im2col.DilateY = 1
// 		benchmark = im2col
// 	case "kmeans":
// 		kmeans := kmeans.NewBenchmark(driver)
// 		kmeans.NumPoints = 1048576
// 		kmeans.NumClusters = 8 / 4
// 		kmeans.NumFeatures = 32 / 16
// 		kmeans.MaxIter = 3 * 6
// 		benchmark = kmeans
// 	case "matrixmultiplication":
// 		matrixmultiplication := matrixmultiplication.NewBenchmark(driver)
// 		matrixmultiplication.X = 2048 / 16
// 		matrixmultiplication.Y = 2048 * 64
// 		matrixmultiplication.Z = 2048 / 16
// 		benchmark = matrixmultiplication
// 	case "matrixtranspose":
// 		matrixtranspose := matrixtranspose.NewBenchmark(driver)
// 		matrixtranspose.Width = 4096 * 2
// 		// matrixtranspose.Width = 4096 / 2
// 		benchmark = matrixtranspose
// 	case "nbody":
// 		nbody := nbody.NewBenchmark(driver)
// 		nbody.NumParticles = 104857600 * 2
// 		nbody.NumIterations = 1024
// 		benchmark = nbody
// 	case "nw":
// 		nw := nw.NewBenchmark(driver)
// 		nw.SetLength(8192 * 2)
// 		benchmark = nw
// 	case "pagerank":
// 		pagerank := pagerank.NewBenchmark(driver)
// 		pagerank.NumNodes = 262144 * 2
// 		pagerank.NumConnections = 1048576
// 		pagerank.MaxIterations = 3
// 		benchmark = pagerank
// 	case "relu":
// 		relu := relu.NewBenchmark(driver)
// 		relu.Length = 10485760 * 8
// 		benchmark = relu
// 	case "simpleconvolution":
// 		simpleconvolution := simpleconvolution.NewBenchmark(driver)
// 		simpleconvolution.Height = 2048
// 		simpleconvolution.Width = 2048 * 4
// 		simpleconvolution.SetMaskSize(3)
// 		benchmark = simpleconvolution
// 	case "spmv":
// 		spmv := spmv.NewBenchmark(driver)
// 		spmv.Dim = 10485760 / 2
// 		spmv.Sparsity = 0.000000001
// 		benchmark = spmv
// 	case "stencil2d":
// 		stencil2d := stencil2d.NewBenchmark(driver)
// 		stencil2d.NumRows = 4096 * 2
// 		stencil2d.NumCols = 4096
// 		stencil2d.NumIteration = 3
// 		benchmark = stencil2d
// 	case "lenet":
// 		lenet := lenet.NewBenchmark(driver)
// 		lenet.Epoch = 1
// 		lenet.MaxBatchPerEpoch = 2
// 		lenet.BatchSize = 32
// 		lenet.EnableTesting = false
// 		lenet.EnableVerification = false
// 		benchmark = lenet
// 	case "minerva":
// 		minerva := minerva.NewBenchmark(driver)
// 		minerva.Epoch = 1
// 		minerva.MaxBatchPerEpoch = 2
// 		minerva.BatchSize = 32
// 		minerva.EnableTesting = false
// 		minerva.EnableVerification = false
// 		benchmark = minerva
// 	case "vgg16":
// 		vgg16 := vgg16.NewBenchmark(driver)
// 		vgg16.Epoch = 1
// 		vgg16.MaxBatchPerEpoch = 2
// 		vgg16.BatchSize = 8
// 		vgg16.EnableTesting = false
// 		vgg16.EnableVerification = false
// 		benchmark = vgg16

// 	default:
// 		panic("Unknown benchmark")
// 	}

// 	return benchmark
// }
