package benchmarkselection

import (
	"flag"

	"github.com/sarchlab/mgpusim/v3/benchmarks"
	"github.com/sarchlab/mgpusim/v3/benchmarks/w2/bfs"
	"github.com/sarchlab/mgpusim/v3/benchmarks/w2/graphtc"
	"github.com/sarchlab/mgpusim/v3/benchmarks/w2/npbcg"
	"github.com/sarchlab/mgpusim/v3/benchmarks/w2/sssp"
	"github.com/sarchlab/mgpusim/v3/driver"
)

// W2 group a: graph and sparse-solver ports from gpu_benchmarks tier2
// (graph_tc, rodinia_bfs, lonestar_sssp, npb_cg). Defaults are small smoke
// sizes. The graph workloads share an R-MAT / Kronecker generator
// (2^scale vertices, edge-factor * 2^scale edges).
var (
	tcScaleFlag    = flag.Int("tc-scale", graphtc.DefaultParams().Scale, "graphtc: log2 vertices of the R-MAT graph")
	tcEFFlag       = flag.Int("tc-edge-factor", graphtc.DefaultParams().EdgeFactor, "graphtc: generated edges per vertex")
	tcSeedFlag     = flag.Int64("tc-seed", graphtc.DefaultParams().Seed, "graphtc: graph generator seed")
	bfsScaleFlag   = flag.Int("bfs-scale", bfs.DefaultParams().Scale, "bfs: log2 vertices of the R-MAT graph")
	bfsEFFlag      = flag.Int("bfs-edge-factor", bfs.DefaultParams().EdgeFactor, "bfs: generated edges per vertex")
	bfsSeedFlag    = flag.Int64("bfs-seed", bfs.DefaultParams().Seed, "bfs: graph generator / source seed")
	ssspScaleFlag  = flag.Int("sssp-scale", sssp.DefaultParams().Scale, "sssp: log2 vertices of the R-MAT graph")
	ssspEFFlag     = flag.Int("sssp-edge-factor", sssp.DefaultParams().EdgeFactor, "sssp: generated edges per vertex")
	ssspSeedFlag   = flag.Int64("sssp-seed", sssp.DefaultParams().Seed, "sssp: graph / weight / source seed")
	ssspRoundsFlag = flag.Int("sssp-max-rounds", 0,
		"sssp: cap on Bellman-Ford rounds (0 = until no change, as upstream)")
	cgClassFlag = flag.String("cg-class", npbcg.DefaultParams().Class, "npbcg: NAS class S/W/A/B/C")
	cgOuterFlag = flag.Int("cg-outer", npbcg.DefaultParams().Outer, "npbcg: outer (inverse-power) iterations; NAS: 15 or 75")
	cgInnerFlag = flag.Int("cg-inner", npbcg.DefaultParams().Inner, "npbcg: CG iterations per outer iteration; NAS: 25")
)

func w2aBenchmark(name string, d *driver.Driver) benchmarks.Benchmark {
	switch name {
	case "graphtc":
		b := graphtc.NewBenchmark(d)
		b.Params = graphtc.Params{Scale: *tcScaleFlag, EdgeFactor: *tcEFFlag, Seed: *tcSeedFlag}
		return b
	case "bfs":
		b := bfs.NewBenchmark(d)
		b.Params = bfs.Params{Scale: *bfsScaleFlag, EdgeFactor: *bfsEFFlag, Seed: *bfsSeedFlag}
		return b
	case "sssp":
		b := sssp.NewBenchmark(d)
		b.Params = sssp.Params{Scale: *ssspScaleFlag, EdgeFactor: *ssspEFFlag, Seed: *ssspSeedFlag,
			MaxRounds: *ssspRoundsFlag}
		return b
	case "npbcg":
		b := npbcg.NewBenchmark(d)
		b.Params = npbcg.Params{Class: *cgClassFlag, Outer: *cgOuterFlag, Inner: *cgInnerFlag}
		return b
	}
	return nil
}
