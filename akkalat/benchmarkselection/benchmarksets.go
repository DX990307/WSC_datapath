package benchmarkselection

import (
	"github.com/sarchlab/mgpusim/v3/benchmarks"
	"github.com/sarchlab/mgpusim/v3/benchmarks/amdappsdk/floydwarshall"

	// "github.com/sarchlab/mgpusim/v3/benchmarks/dnn/layer_benchmarks/conv2d"

	"github.com/sarchlab/mgpusim/v3/benchmarks/heteromark/pagerank"
	"github.com/sarchlab/mgpusim/v3/driver"
)

type BenchmarkSet struct {
	benchmark1 benchmarks.Benchmark
	benchmark2 benchmarks.Benchmark
}

func SelectBenchmarkset(name string, driver *driver.Driver) BenchmarkSet {
	// var benchmark benchmarks.Benchmark
	benchmarkSet := BenchmarkSet{}
	switch name {
	case "prfws":
		benchmarkSet := &BenchmarkSet{
			benchmark1: pagerank.NewBenchmark(driver),
			benchmark2: floydwarshall.NewBenchmark(driver),
		}
		// This is a concurrent running benchmark that runs pagerank and fast walsh
		pagerank := pagerank.NewBenchmark(driver)
		GPU := []int{49}        // Select the first GPU for pagerank
		pagerank.SelectGPU(GPU) // Select all GPUs
		pagerank.NumNodes = 262144 * 2
		pagerank.NumConnections = 1048576
		pagerank.MaxIterations = 3
		benchmarkSet.benchmark1 = pagerank

		floydwarshall := floydwarshall.NewBenchmark(driver)
		floydwarshall.NumNodes = 1024 * 2
		GPU = []int{50}              // Select the second GPU for fast walsh
		floydwarshall.SelectGPU(GPU) // Select all GPUs
		floydwarshall.NumIterations = 1024 / 256
		// benchmark = floydwarshall
		benchmarkSet.benchmark2 = floydwarshall

	default:
		panic("Unknown benchmark")
	}

	return benchmarkSet
}
