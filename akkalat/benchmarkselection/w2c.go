package benchmarkselection

import (
	"flag"

	"github.com/sarchlab/mgpusim/v3/benchmarks"
	"github.com/sarchlab/mgpusim/v3/benchmarks/w2/shocsort"
	"github.com/sarchlab/mgpusim/v3/driver"
)

// W2 group c workloads (ported from gpu_benchmarks tier2). Defaults are
// small smoke sizes.
var (
	w2SortNFlag    = flag.Int("shocsort-n", shocsort.DefaultParams().N, "w2-shocsort: key/value pairs (multiple of 1024)")
	w2SortSeedFlag = flag.Int64("shocsort-seed", shocsort.DefaultParams().Seed, "w2-shocsort: key generator seed")
)

// w2cBenchmark returns the W2 group-c benchmark called name, or nil.
func w2cBenchmark(name string, d *driver.Driver) benchmarks.Benchmark {
	switch name {
	case "w2-shocsort":
		b := shocsort.NewBenchmark(d)
		b.Params = shocsort.Params{N: *w2SortNFlag, Seed: *w2SortSeedFlag}
		return b
	}
	return nil
}
