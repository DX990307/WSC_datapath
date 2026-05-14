package main

import (
	"flag"

	"github.com/sarchlab/mgpusim/v3/benchmarks/dnn/layer_benchmarks/fulllayer"
	"github.com/sarchlab/mgpusim/v3/samples/runner"
)

var n = flag.Int("N", 1, "batch size")
var input = flag.Int("input-dim", 1, "input size")
var output = flag.Int("output-dim", 1, "output size")
var enableBackward = flag.Bool("enable-backward", false, "enable backward")

func main() {
	flag.Parse()

	runner := new(runner.Runner).ParseFlag().Init()

	benchmark := fulllayer.NewBenchmark(runner.Driver())
	benchmark.N = *n
	benchmark.InputDim = *input
	benchmark.OutputDim = *output
	benchmark.EnableBackward = *enableBackward

	runner.AddBenchmark(benchmark)

	runner.Run()
}
