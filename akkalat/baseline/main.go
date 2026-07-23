package main

import (
	"flag"
	"math/rand"
	"runtime"

	_ "net/http/pprof"

	"github.com/sarchlab/akkalat/baseline/runner"
	"github.com/sarchlab/akkalat/benchmarkselection"
)

var benchmarkFlag = flag.String("benchmark", "fir",
	"Which benchmark to run")

var benchmarksize = flag.Int("benchmark-size", 4096,
	"Which benchmark to run")

func main() {
	flag.Parse()
	runtime.GOMAXPROCS(4)
	// Keep separately launched baseline and mechanism configurations on the
	// same generated input. Individual benchmarks may deliberately override
	// this seed; CSR workloads additionally use a generator-private seed.
	rand.Seed(1)
	// http.ListenAndServe("localhost:6060", nil)
	runner := new(runner.Runner).ParseFlag().Init()

	benchmark := benchmarkselection.SelectBenchmark(
		*benchmarkFlag, runner.Driver())

	runner.AddBenchmark(benchmark)
	runner.Run()
}
