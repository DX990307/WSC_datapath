package benchmarkselection

// P12 (T18 M1 sharing): an opt-in floydwarshall input whose kernels all run
// to completion on the wafer. Only the "floydwarshall-p12small" name selects
// it; "floydwarshall" keeps its input.

import (
	"flag"
	"fmt"
	"os"

	"github.com/sarchlab/mgpusim/v3/benchmarks"
	"github.com/sarchlab/mgpusim/v3/benchmarks/amdappsdk/floydwarshall"
	"github.com/sarchlab/mgpusim/v3/driver"
)

var (
	p12FWNodesFlag = flag.Int("p12-fw-nodes", 2048,
		"floydwarshall-p12small: nodes (multiple of 8; (nodes/8)^2 WGs per pass)")
	p12FWPassesFlag = flag.Int("p12-fw-passes", 4,
		"floydwarshall-p12small: passes k = 0..passes-1 (one kernel each)")
)

func p12SmallBenchmark(name string, d *driver.Driver) benchmarks.Benchmark {
	switch name {
	case "floydwarshall-p12small":
		fw := floydwarshall.NewBenchmark(d)
		fw.NumNodes = uint32(*p12FWNodesFlag)
		fw.NumIterations = uint32(*p12FWPassesFlag)
		wgs := (*p12FWNodesFlag / 8) * (*p12FWNodesFlag / 8)
		fmt.Fprintf(os.Stderr, "[P12small] %s: NumNodes=%d NumIterations=%d, %d WGs per kernel (%.2f per CU)\n",
			name, fw.NumNodes, fw.NumIterations, wgs, float64(wgs)/float64(t16TotalCUs()))
		return fw
	}
	return nil
}
