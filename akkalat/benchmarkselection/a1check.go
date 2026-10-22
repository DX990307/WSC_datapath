package benchmarkselection

import (
	"flag"

	"github.com/sarchlab/mgpusim/v3/benchmarks/microbench/a1check"
	"github.com/sarchlab/mgpusim/v3/driver"
)

var a1checkBytesFlag = flag.Int("a1check-bytes", 64<<10,
	"a1check: size of the read/written buffer A and the read-only buffer R")
var a1checkRoundsFlag = flag.Int("a1check-rounds", 2,
	"a1check: read-only rounds of R after the write")

func newA1Check(d *driver.Driver) *a1check.Benchmark {
	b := a1check.NewBenchmark(d)
	b.Bytes = *a1checkBytesFlag
	b.Rounds = *a1checkRoundsFlag
	return b
}
