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
	tier2altisgups "github.com/sarchlab/mgpusim/v3/benchmarks/tier2/altis_gups"
	tier2cudatranspose "github.com/sarchlab/mgpusim/v3/benchmarks/tier2/cuda_transpose"
	tier2graphpr "github.com/sarchlab/mgpusim/v3/benchmarks/tier2/graph_pr"
	tier2heteromarkpagerank "github.com/sarchlab/mgpusim/v3/benchmarks/tier2/heteromark_pagerank"
	tier2lonestarsssp "github.com/sarchlab/mgpusim/v3/benchmarks/tier2/lonestar_sssp"
	tier2npbcg "github.com/sarchlab/mgpusim/v3/benchmarks/tier2/npb_cg"
	tier2parboilspmv "github.com/sarchlab/mgpusim/v3/benchmarks/tier2/parboil_spmv"
	tier2rodiniabfs "github.com/sarchlab/mgpusim/v3/benchmarks/tier2/rodinia_bfs"
	tier2rodiniahotspot "github.com/sarchlab/mgpusim/v3/benchmarks/tier2/rodinia_hotspot"
	tier2shocspmv "github.com/sarchlab/mgpusim/v3/benchmarks/tier2/shoc_spmv"
	tier2shocstencil2d "github.com/sarchlab/mgpusim/v3/benchmarks/tier2/shoc_stencil2d"
	"github.com/sarchlab/mgpusim/v3/driver"
)

const (
	targetFootprintBytes = 1 * 1024 * 1024 * 1024

	// These sizes keep each benchmark near 1 GiB while respecting
	// power-of-two, square, tiled, or CSR-shaped inputs.
	singleBufferBytes         = targetFootprintBytes
	twoBufferFloat32Elements  = targetFootprintBytes / (2 * 4)
	powerOfTwoFloat32Elements = 1 << 28
	denseSquareDim            = 16384
	twoBufferSquareDim        = 12288
	threeBufferSquareDim      = 8192
	stencilRows               = 16386
	stencilCols               = 8194
	csrDim                    = 1 << 23
	csrConnections            = 100_000_000
	csrSparsity               = 100_000_000.0 / (1 << 23) / (1 << 23)
	bfsNodes                  = 1 << 23

	matmulOuterDim      = 256
	matmulKDim          = 524288
	matmulSmokeOuterDim = 64
	matmulSmokeKDim     = 2097152

	im2colImageDim          = 2048
	kmeansPoints            = 1 << 22
	kmeansFeatures          = 32
	fftMiB                  = 1024
	simpleConvolutionHeight = 16384
	simpleConvolutionWidth  = 8192
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
		aes.Length = singleBufferBytes
		benchmark = aes
	case "heteromark_aes":
		aes := aes.NewBenchmark(driver)
		aes.Length = singleBufferBytes
		benchmark = aes
	case "atax":
		atax := atax.NewBenchmark(driver)
		atax.NX = denseSquareDim
		atax.NY = denseSquareDim
		benchmark = atax
	case "polybench_atax":
		atax := atax.NewBenchmark(driver)
		atax.NX = denseSquareDim
		atax.NY = denseSquareDim
		benchmark = atax
	case "altis_gups":
		spmv := tier2altisgups.NewBenchmark(driver)
		spmv.Dim = csrDim
		spmv.Sparsity = csrSparsity
		benchmark = spmv
	case "bicg":
		bicg := bicg.NewBenchmark(driver)
		bicg.NX = denseSquareDim
		bicg.NY = denseSquareDim
		benchmark = bicg
	case "polybench_bicg":
		bicg := bicg.NewBenchmark(driver)
		bicg.NX = denseSquareDim
		bicg.NY = denseSquareDim
		benchmark = bicg
	case "bitonicsort":
		bitonicsort := bitonicsort.NewBenchmark(driver)
		bitonicsort.Length = powerOfTwoFloat32Elements
		benchmark = bitonicsort
	case "bert":
		benchmark = bert.NewBenchmark(driver)
	case "conv2d":
		benchmark = newConv2DBenchmark(driver, 1, 3, 512, 512, 3, 4, 4, 0, 0, 1, 1)
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
	case "cuda_transpose":
		matrixtranspose := tier2cudatranspose.NewBenchmark(driver)
		matrixtranspose.Width = twoBufferSquareDim
		benchmark = matrixtranspose
	case "maxpooling":
		maxpooling := maxpooling.NewBenchmark(driver)
		maxpooling.N = 1
		maxpooling.C = 64
		maxpooling.H = 384
		maxpooling.W = 384
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
		avgpooling.C = 64
		avgpooling.H = 320
		avgpooling.W = 320
		avgpooling.KernelHeight = 7
		avgpooling.KernelWidth = 7
		avgpooling.PadX = 0
		avgpooling.PadY = 0
		avgpooling.StrideX = 1
		avgpooling.StrideY = 1
		benchmark = avgpooling
	case "fulllayer":
		fulllayer := fulllayer.NewBenchmark(driver)
		fulllayer.N = 512
		fulllayer.InputDim = 3072
		fulllayer.OutputDim = 3072
		fulllayer.RandomizeParameters = false
		benchmark = fulllayer
	case "fulllayer-large":
		fulllayer := fulllayer.NewBenchmark(driver)
		fulllayer.N = 512
		fulllayer.InputDim = 3072
		fulllayer.OutputDim = 3072
		fulllayer.RandomizeParameters = false
		benchmark = fulllayer
	case "fulllayer-gemm-tiny":
		fulllayer := fulllayer.NewBenchmark(driver)
		fulllayer.N = 256
		fulllayer.InputDim = 3072
		fulllayer.OutputDim = 3072
		fulllayer.RandomizeParameters = false
		benchmark = fulllayer
	case "fulllayer-gemm-debug":
		fulllayer := fulllayer.NewBenchmark(driver)
		fulllayer.N = 512
		fulllayer.InputDim = 3072
		fulllayer.OutputDim = 3072
		fulllayer.RandomizeParameters = false
		benchmark = fulllayer
	case "fulllayer-7bcompute":
		fulllayer := fulllayer.NewBenchmark(driver)
		fulllayer.N = 416
		fulllayer.InputDim = 3072
		fulllayer.OutputDim = 3072
		fulllayer.RandomizeParameters = false
		benchmark = fulllayer
	case "fulllayer-1gb":
		fulllayer := fulllayer.NewBenchmark(driver)
		fulllayer.N = 512
		fulllayer.InputDim = 3072
		fulllayer.OutputDim = 3072
		fulllayer.RandomizeParameters = false
		benchmark = fulllayer
	case "fastwalshtransform":
		fastwalshtransform := fastwalshtransform.NewBenchmark(driver)
		fastwalshtransform.Length = powerOfTwoFloat32Elements
		benchmark = fastwalshtransform
	case "fir":
		fir := fir.NewBenchmark(driver)
		fir.Length = twoBufferFloat32Elements
		benchmark = fir
	case "heteromark_fir":
		fir := fir.NewBenchmark(driver)
		fir.Length = twoBufferFloat32Elements
		benchmark = fir
	case "fft":
		fft := fft.NewBenchmark(driver)
		fft.Bytes = fftMiB
		fft.Passes = 4
		benchmark = fft
	case "shoc_fft":
		fft := fft.NewBenchmark(driver)
		fft.Bytes = fftMiB
		fft.Passes = 4
		benchmark = fft
	case "floydwarshall":
		floydwarshall := floydwarshall.NewBenchmark(driver)
		floydwarshall.NumNodes = twoBufferSquareDim
		floydwarshall.NumIterations = 1
		benchmark = floydwarshall
	case "gpt":
		benchmark = gpt.NewBenchmark(driver)
	case "graph_pr":
		pagerank := tier2graphpr.NewBenchmark(driver)
		pagerank.NumNodes = csrDim
		pagerank.NumConnections = csrConnections
		pagerank.MaxIterations = 1
		benchmark = pagerank
	case "heteromark_pagerank":
		pagerank := tier2heteromarkpagerank.NewBenchmark(driver)
		pagerank.NumNodes = csrDim
		pagerank.NumConnections = csrConnections
		pagerank.MaxIterations = 1
		benchmark = pagerank
	case "im2col":
		im2col := im2col.NewBenchmark(driver)
		im2col.N = 1
		im2col.C = 3
		im2col.H = im2colImageDim
		im2col.W = im2colImageDim
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
		kmeans.NumPoints = kmeansPoints
		kmeans.NumClusters = 8
		kmeans.NumFeatures = kmeansFeatures
		kmeans.MaxIter = 3
		benchmark = kmeans
	case "kvcache":
		kvcache := kvcache.NewBenchmark(driver)
		kvcache.NumLayers = 4
		kvcache.NumHeads = 8
		kvcache.NumKVHeads = 8
		kvcache.SeqLen = 1024
		kvcache.HeadDim = 128
		kvcache.DecodeStep = 1
		benchmark = kvcache
	case "kvcache-decode":
		kvcache := kvcache.NewDecodeBenchmark(driver)
		kvcache.NumLayers = 4
		kvcache.NumHeads = 16
		kvcache.NumKVHeads = 16
		kvcache.SeqLen = 512
		kvcache.HeadDim = 128
		kvcache.SeqBlock = 64
		kvcache.DecodeStep = 1
		benchmark = kvcache
	case "kvcache-decode-30b":
		kvcache := kvcache.NewDecode30BBenchmark(driver)
		kvcache.NumLayers = 4
		kvcache.NumHeads = 16
		kvcache.NumKVHeads = 16
		kvcache.SeqLen = 512
		kvcache.HeadDim = 128
		kvcache.SeqBlock = 64
		kvcache.DecodeStep = 1
		benchmark = kvcache
	case "llmop":
		benchmark = llmop.NewBenchmarkFromFlags(driver)
	case "lonestar_sssp":
		bfsBenchmark := tier2lonestarsssp.NewBenchmark(driver)
		bfsBenchmark.NumNode = bfsNodes
		bfsBenchmark.Degree = 20
		bfsBenchmark.MaxDepth = 20
		benchmark = bfsBenchmark
	case "matrixmultiplication":
		matrixmultiplication := matrixmultiplication.NewBenchmark(driver)
		matrixmultiplication.X = matmulOuterDim
		matrixmultiplication.Y = matmulKDim
		matrixmultiplication.Z = matmulOuterDim
		benchmark = matrixmultiplication
	case "matrixmultiplication-pipeline-smoke":
		matrixmultiplication := matrixmultiplication.NewBenchmark(driver)
		matrixmultiplication.X = matmulSmokeOuterDim
		matrixmultiplication.Y = matmulSmokeKDim
		matrixmultiplication.Z = matmulSmokeOuterDim
		benchmark = matrixmultiplication
	case "matrixmultiplication-llm-prefill-attn":
		matrixmultiplication := matrixmultiplication.NewBenchmark(driver)
		matrixmultiplication.X = 3072
		matrixmultiplication.Y = 512
		matrixmultiplication.Z = 3072
		benchmark = matrixmultiplication
	case "matrixmultiplication-llm-decode-attn":
		matrixmultiplication := matrixmultiplication.NewBenchmark(driver)
		matrixmultiplication.X = 3072
		matrixmultiplication.Y = 256
		matrixmultiplication.Z = 3072
		benchmark = matrixmultiplication
	case "matrixmultiplication-llm-prefill-mlp-up":
		matrixmultiplication := matrixmultiplication.NewBenchmark(driver)
		matrixmultiplication.X = 1536
		matrixmultiplication.Y = 512
		matrixmultiplication.Z = 6144
		benchmark = matrixmultiplication
	case "matrixmultiplication-llm-decode-mlp-up":
		matrixmultiplication := matrixmultiplication.NewBenchmark(driver)
		matrixmultiplication.X = 1536
		matrixmultiplication.Y = 256
		matrixmultiplication.Z = 6144
		benchmark = matrixmultiplication
	case "matrixmultiplication-llm-prefill-mlp-down":
		matrixmultiplication := matrixmultiplication.NewBenchmark(driver)
		matrixmultiplication.X = 6144
		matrixmultiplication.Y = 512
		matrixmultiplication.Z = 1536
		benchmark = matrixmultiplication
	case "matrixmultiplication-llm-decode-mlp-down":
		matrixmultiplication := matrixmultiplication.NewBenchmark(driver)
		matrixmultiplication.X = 6144
		matrixmultiplication.Y = 256
		matrixmultiplication.Z = 1536
		benchmark = matrixmultiplication
	case "matrixmultiplication-middletile":
		matrixmultiplication := matrixmultiplication.NewMiddleTileBenchmark(driver)
		matrixmultiplication.X = matmulOuterDim
		matrixmultiplication.Y = matmulKDim
		matrixmultiplication.Z = matmulOuterDim
		benchmark = matrixmultiplication
	case "matrixmultiplication-middletile-pipeline-smoke":
		matrixmultiplication := matrixmultiplication.NewMiddleTileBenchmark(driver)
		matrixmultiplication.X = matmulSmokeOuterDim
		matrixmultiplication.Y = matmulSmokeKDim
		matrixmultiplication.Z = matmulSmokeOuterDim
		benchmark = matrixmultiplication
	case "matrixtranspose":
		matrixtranspose := matrixtranspose.NewBenchmark(driver)
		matrixtranspose.Width = twoBufferSquareDim
		benchmark = matrixtranspose
	case "matrixtranspose-middletile":
		matrixtranspose := matrixtranspose.NewMiddleTileBenchmark(driver)
		matrixtranspose.Width = twoBufferSquareDim
		benchmark = matrixtranspose
	case "nbody":
		nbody := nbody.NewBenchmark(driver)
		nbody.NumParticles = targetFootprintBytes / (4 * 4 * 4)
		nbody.NumIterations = 16
		benchmark = nbody
	case "nw":
		nw := nw.NewBenchmark(driver)
		nw.SetLength(threeBufferSquareDim)
		benchmark = nw
	case "rodinia_nw":
		nw := nw.NewBenchmark(driver)
		nw.SetLength(threeBufferSquareDim)
		benchmark = nw
	case "npb_cg":
		spmv := tier2npbcg.NewBenchmark(driver)
		spmv.Dim = csrDim
		spmv.Sparsity = csrSparsity
		benchmark = spmv
	case "parboil_spmv":
		spmv := tier2parboilspmv.NewBenchmark(driver)
		spmv.Dim = csrDim
		spmv.Sparsity = csrSparsity
		benchmark = spmv
	case "pagerank":
		pagerank := pagerank.NewBenchmark(driver)
		pagerank.NumNodes = csrDim
		pagerank.NumConnections = csrConnections
		pagerank.MaxIterations = 1
		benchmark = pagerank
	case "relu":
		relu := relu.NewBenchmark(driver)
		relu.Length = twoBufferFloat32Elements
		benchmark = relu
	case "resnet":
		benchmark = resnet.NewBenchmark(driver)
	case "rodinia_bfs":
		bfsBenchmark := tier2rodiniabfs.NewBenchmark(driver)
		bfsBenchmark.NumNode = bfsNodes
		bfsBenchmark.Degree = 20
		bfsBenchmark.MaxDepth = 20
		benchmark = bfsBenchmark
	case "rodinia_hotspot":
		stencil2d := tier2rodiniahotspot.NewBenchmark(driver)
		stencil2d.NumRows = stencilRows
		stencil2d.NumCols = stencilCols
		stencil2d.NumIteration = 3
		benchmark = stencil2d
	case "simpleconvolution":
		simpleconvolution := simpleconvolution.NewBenchmark(driver)
		simpleconvolution.Height = simpleConvolutionHeight
		simpleconvolution.Width = simpleConvolutionWidth
		simpleconvolution.SetMaskSize(3)
		benchmark = simpleconvolution
	case "spmv":
		spmv := spmv.NewBenchmark(driver)
		spmv.Dim = csrDim
		spmv.Sparsity = csrSparsity
		benchmark = spmv
	case "shoc_spmv":
		spmv := tier2shocspmv.NewBenchmark(driver)
		spmv.Dim = csrDim
		spmv.Sparsity = csrSparsity
		benchmark = spmv
	case "stencil2d":
		stencil2d := stencil2d.NewBenchmark(driver)
		stencil2d.NumRows = stencilRows
		stencil2d.NumCols = stencilCols
		stencil2d.NumIteration = 3
		benchmark = stencil2d
	case "shoc_stencil2d":
		stencil2d := tier2shocstencil2d.NewBenchmark(driver)
		stencil2d.NumRows = stencilRows
		stencil2d.NumCols = stencilCols
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
		vgg16.MaxBatchPerEpoch = 1
		vgg16.BatchSize = 1
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
