package benchmarkselection

import (
	"flag"
	"log"

	"github.com/sarchlab/mgpusim/v3/benchmarks/w2/altis_cfd"
	"github.com/sarchlab/mgpusim/v3/benchmarks/w2/lonestar_bh"
	"github.com/sarchlab/mgpusim/v3/benchmarks/w2/npb_mg"
	"github.com/sarchlab/mgpusim/v3/benchmarks/w2/rodinia_lud"
	"github.com/sarchlab/mgpusim/v3/driver"
)

// W2 group d ports of gpu_benchmarks tier2 (lonestar_bh, altis_cfd,
// rodinia_lud, npb_mg). Defaults are small smoke sizes.
var (
	bhDefaults  = lonestarbh.DefaultParams()
	bhBodies    = flag.Int("bh-bodies", bhDefaults.Bodies, "lonestar_bh: bodies")
	bhSteps     = flag.Int("bh-steps", bhDefaults.Steps, "lonestar_bh: timesteps (tree rebuilt on the host each step)")
	bhTheta     = flag.Float64("bh-theta", float64(bhDefaults.Theta), "lonestar_bh: opening angle")
	bhSort      = flag.Bool("bh-sort", bhDefaults.Sort, "lonestar_bh: sort bodies in tree order every step (Lonestar); false = upstream order")
	bhSeed      = flag.Uint("bh-seed", uint(bhDefaults.Seed), "lonestar_bh: xorshift32 seed (upstream 12345)")
	cfdDefaults = altiscfd.DefaultParams()
	cfdCells    = flag.Int("cfd-cells", cfdDefaults.Cells, "altis_cfd: tetrahedral cells (upstream 97000)")
	cfdSteps    = flag.Int("cfd-steps", cfdDefaults.Steps, "altis_cfd: timesteps (3 RK stages each; upstream 10)")
	cfdBlock    = flag.Int("cfd-block", cfdDefaults.Block, "altis_cfd: threads per WG")
	cfdShuffle  = flag.Bool("cfd-shuffle", cfdDefaults.Shuffle, "altis_cfd: relabel the cells randomly (unstructured numbering)")
	cfdSeed     = flag.Int64("cfd-seed", cfdDefaults.Seed, "altis_cfd: shuffle seed")
	ludDefaults = rodinialud.DefaultParams()
	ludN        = flag.Int("lud-n", ludDefaults.N, "rodinia_lud: matrix size N (multiple of 16)")
	ludSeed     = flag.Int64("lud-seed", ludDefaults.Seed, "rodinia_lud: matrix seed")
	mgDefaults  = npbmg.DefaultParams()
	mgClass     = flag.String("mg-class", string(mgDefaults.Class), "npb_mg: NAS class S, W, A, B or C")
	mgCycles    = flag.Int("mg-cycles", mgDefaults.Cycles, "npb_mg: V-cycles (NAS: 4 for S/W/A)")
	mgBlock     = flag.Int("mg-block", mgDefaults.Block, "npb_mg: max WG size of resid/psinv")
)

func newLonestarBH(d *driver.Driver) *lonestarbh.Benchmark {
	b := lonestarbh.NewBenchmark(d)
	b.Params.Bodies = *bhBodies
	b.Params.Steps = *bhSteps
	b.Params.Theta = float32(*bhTheta)
	b.Params.Sort = *bhSort
	b.Params.Seed = uint32(*bhSeed)
	return b
}

func newAltisCFD(d *driver.Driver) *altiscfd.Benchmark {
	b := altiscfd.NewBenchmark(d)
	b.Params = altiscfd.Params{Cells: *cfdCells, Steps: *cfdSteps, Block: *cfdBlock,
		Shuffle: *cfdShuffle, Seed: *cfdSeed}
	return b
}

func newRodiniaLUD(d *driver.Driver) *rodinialud.Benchmark {
	b := rodinialud.NewBenchmark(d)
	b.Params = rodinialud.Params{N: *ludN, Seed: *ludSeed}
	return b
}

func newNPBMG(d *driver.Driver) *npbmg.Benchmark {
	if len(*mgClass) != 1 {
		log.Panicf("npb_mg: bad class %q", *mgClass)
	}
	b := npbmg.NewBenchmark(d)
	b.Params = npbmg.Params{Class: (*mgClass)[0], Cycles: *mgCycles, Block: *mgBlock}
	return b
}
