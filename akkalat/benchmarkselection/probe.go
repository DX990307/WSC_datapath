package benchmarkselection

import (
	"flag"

	"github.com/sarchlab/mgpusim/v3/benchmarks/microbench/probe"
	"github.com/sarchlab/mgpusim/v3/driver"
)

var probeOwnerFlag = flag.Int("probe-owner", 5,
	"probe: driver GPU ID that holds the buffer")
var probeRunnerFlag = flag.Int("probe-runner", 5,
	"probe: driver GPU ID that runs the kernel")
var probeBytesFlag = flag.Uint64("probe-bytes", 2<<20,
	"probe: buffer size in bytes (per GPM with -probe-all-gpms)")
var probeModeFlag = flag.String("probe-mode", "read",
	"probe: read or write")
var probePassesFlag = flag.Int("probe-passes", 2,
	"probe: number of back-to-back launches")
var probeThreadsFlag = flag.Int("probe-threads", 32*4*256,
	"probe: work-items per launch")
var probeAllGPMsFlag = flag.Bool("probe-all-gpms", false,
	"probe: every GPM reads its own shard concurrently")

func newProbe(d *driver.Driver) *probe.Benchmark {
	b := probe.NewBenchmark(d)
	b.Owner = *probeOwnerFlag
	b.Runner = *probeRunnerFlag
	b.Bytes = *probeBytesFlag
	b.Passes = *probePassesFlag
	b.Threads = *probeThreadsFlag
	b.AllGPMs = *probeAllGPMsFlag

	switch *probeModeFlag {
	case "read":
		b.Mode = probe.ModeRead
	case "write":
		b.Mode = probe.ModeWrite
	default:
		panic("probe-mode must be read or write")
	}

	return b
}
