package benchmarkselection

import (
	"flag"
	"strconv"
	"strings"

	"github.com/sarchlab/akkalat/baseline/runner"
	"github.com/sarchlab/mgpusim/v3/benchmarks/microbench/platbench"
	"github.com/sarchlab/mgpusim/v3/driver"
)

var platTestFlag = flag.String("plat-test", "latency",
	"platbench item: latency, bandwidth, hopmatrix, remotebw, incast, patterns, kernel, remotefrac")
var platSrcFlag = flag.Int("plat-src", 1, "platbench: driver GPU ID that runs the kernel")
var platDstsFlag = flag.String("plat-dsts", "1",
	"platbench: driver GPU IDs that hold the data (0 = all GPMs, hopmatrix)")
var platSourcesFlag = flag.String("plat-sources", "1",
	"platbench hopmatrix: source driver GPU IDs (0 = all GPMs)")
var platFootprintsFlag = flag.String("plat-footprints", "1M",
	"platbench: footprints, e.g. 4K,64K,1M,64M")
var platGroupsFlag = flag.String("plat-groups", "A,B",
	"platbench latency: A = page-local ring, B = every step crosses a page")
var platStepsFlag = flag.Int("plat-steps", 20000, "platbench latency/hopmatrix: measured chase steps")
var platWarmLimitFlag = flag.String("plat-warm-limit", "8M",
	"platbench latency: warm up (one full ring pass) footprints up to this size")
var platCUsFlag = flag.String("plat-cus", "1,2,4,8,16,24,32,38",
	"platbench bandwidth: active CU counts (values above the GPM's CU count are skipped)")
var platModesFlag = flag.String("plat-modes", "read",
	"platbench bandwidth: read,write,copy; incast: shared,disjoint")
var platWGsFlag = flag.String("plat-wgs", "1,2,4,8,16,32,64,128,256",
	"platbench remotebw: reader work-group counts")
var platKsFlag = flag.String("plat-ks", "1,2,4,8,16,47", "platbench incast: numbers of readers")
var platPatternsFlag = flag.String("plat-patterns", "alltoall,neighbor,transpose,ring,snake",
	"platbench patterns")
var platChunkFlag = flag.String("plat-chunk", "256K", "platbench patterns: bytes read from each source")
var platFractionsFlag = flag.String("plat-fractions", "0,0.1,0.25,0.5,1",
	"platbench remotefrac (T16): remote fractions f, one SPMD phase each")
var platMixItersFlag = flag.Int("plat-mix-iters", 64,
	"platbench remotefrac: 1-KB chunks read per wavefront per phase")
var platMixWGsFlag = flag.Int("plat-mix-wgs", 0,
	"platbench remotefrac: work-groups per GPM (0 = CUs per GPM x 8)")

func parseInts(s string) []int {
	var out []int
	for _, f := range strings.Split(s, ",") {
		if f = strings.TrimSpace(f); f != "" {
			v, err := strconv.Atoi(f)
			if err != nil {
				panic(err)
			}
			out = append(out, v)
		}
	}
	return out
}

func parseFloats(s string) []float64 {
	var out []float64
	for _, f := range strings.Split(s, ",") {
		if f = strings.TrimSpace(f); f != "" {
			v, err := strconv.ParseFloat(f, 64)
			if err != nil {
				panic(err)
			}
			out = append(out, v)
		}
	}
	return out
}

func parseSize(f string) uint64 {
	f = strings.TrimSpace(strings.ToUpper(f))
	mult := uint64(1)
	switch {
	case strings.HasSuffix(f, "K"):
		mult, f = 1<<10, strings.TrimSuffix(f, "K")
	case strings.HasSuffix(f, "M"):
		mult, f = 1<<20, strings.TrimSuffix(f, "M")
	case strings.HasSuffix(f, "G"):
		mult, f = 1<<30, strings.TrimSuffix(f, "G")
	}
	v, err := strconv.ParseUint(f, 10, 64)
	if err != nil {
		panic(err)
	}
	return v * mult
}

func parseSizes(s string) []uint64 {
	var out []uint64
	for _, f := range strings.Split(s, ",") {
		if strings.TrimSpace(f) != "" {
			out = append(out, parseSize(f))
		}
	}
	return out
}

func parseStrings(s string) []string {
	var out []string
	for _, f := range strings.Split(s, ",") {
		if f = strings.TrimSpace(f); f != "" {
			out = append(out, f)
		}
	}
	return out
}

func newPlatbench(d *driver.Driver) *platbench.Benchmark {
	b := platbench.NewBenchmark(d)
	b.Test = *platTestFlag
	b.Config = flag.Lookup("gpm-preset").Value.String()
	b.CUsPerGPM = runner.CUsPerGPM()
	b.Src = *platSrcFlag
	b.Dsts = parseInts(*platDstsFlag)
	b.Sources = parseInts(*platSourcesFlag)
	b.Footprints = parseSizes(*platFootprintsFlag)
	b.Groups = parseStrings(*platGroupsFlag)
	b.Steps = *platStepsFlag
	b.WarmLimit = parseSize(*platWarmLimitFlag)
	b.CUs = parseInts(*platCUsFlag)
	b.Modes = parseStrings(*platModesFlag)
	b.WGs = parseInts(*platWGsFlag)
	b.Ks = parseInts(*platKsFlag)
	b.Patterns = parseStrings(*platPatternsFlag)
	b.ChunkBytes = parseSize(*platChunkFlag)
	b.Fractions = parseFloats(*platFractionsFlag)
	b.MixIters = *platMixItersFlag
	b.MixWGs = *platMixWGsFlag
	return b
}
