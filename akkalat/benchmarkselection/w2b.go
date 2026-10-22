package benchmarkselection

import (
	"flag"

	"github.com/sarchlab/akkalat/baseline/runner"
	"github.com/sarchlab/mgpusim/v3/benchmarks/w2/allreduce"
	"github.com/sarchlab/mgpusim/v3/benchmarks/w2/embbag"
	"github.com/sarchlab/mgpusim/v3/benchmarks/w2/gups"
	"github.com/sarchlab/mgpusim/v3/benchmarks/w2/ivfflat"
	"github.com/sarchlab/mgpusim/v3/driver"
)

// W2 group b workloads (embbag, ivfflat, gups, allreduce). Defaults are
// small smoke sizes.
var (
	ebDefaults    = embbag.DefaultParams()
	ebTablesFlag  = flag.Int("eb-tables", ebDefaults.Tables, "embbag: embedding tables")
	ebRowsFlag    = flag.Int("eb-rows", ebDefaults.Rows, "embbag: rows per table (128 fp32 each)")
	ebBatchFlag   = flag.Int("eb-batch", ebDefaults.Batch, "embbag: samples (work-groups = batch*tables/2)")
	ebPoolingFlag = flag.Int("eb-pooling", ebDefaults.Pooling, "embbag: lookups per (sample, table)")
	ebAlphaFlag   = flag.Float64("eb-alpha", ebDefaults.Alpha, "embbag: Zipf exponent of row popularity (> 1)")
	ebSeedFlag    = flag.Int64("eb-seed", ebDefaults.Seed, "embbag: input generator seed")

	ivfDefaults   = ivfflat.DefaultParams()
	ivfNFlag      = flag.Int("ivf-n", ivfDefaults.N, "ivfflat: database vectors (128-dim fp32)")
	ivfNListFlag  = flag.Int("ivf-nlist", ivfDefaults.NList, "ivfflat: inverted lists")
	ivfNProbeFlag = flag.Int("ivf-nprobe", ivfDefaults.NProbe, "ivfflat: lists probed per query")
	ivfQFlag      = flag.Int("ivf-queries", ivfDefaults.Q, "ivfflat: queries")
	ivfKFlag      = flag.Int("ivf-k", ivfDefaults.K, "ivfflat: results per query")
	ivfSeedFlag   = flag.Int64("ivf-seed", ivfDefaults.Seed, "ivfflat: input generator seed")

	gupsDefaults = gups.DefaultParams()
	gupsLogFlag  = flag.Int("gups-log-table", gupsDefaults.LogTable, "gups: log2 of the 64-bit-word table size")
	gupsWGsFlag  = flag.Int("gups-wgs", gupsDefaults.WGs, "gups: work-groups of 256 threads")
	gupsMultFlag = flag.Int("gups-update-mult", gupsDefaults.UpdateMult, "gups: updates per table word (HPCC: 4)")

	arPerCUFlag = flag.Int("ar-per-cu", allreduce.DefaultParams().PerCU,
		"allreduce: reduce-scatter work-groups per CU (copy = 48*CUs*256*this floats)")
)

func newEmbBag(d *driver.Driver) *embbag.Benchmark {
	b := embbag.NewBenchmark(d)
	b.Params = embbag.Params{Tables: *ebTablesFlag, Rows: *ebRowsFlag, Batch: *ebBatchFlag,
		Pooling: *ebPoolingFlag, Alpha: *ebAlphaFlag, Seed: *ebSeedFlag}
	return b
}

func newIVFFlat(d *driver.Driver) *ivfflat.Benchmark {
	b := ivfflat.NewBenchmark(d)
	b.Params = ivfflat.Params{N: *ivfNFlag, NList: *ivfNListFlag, NProbe: *ivfNProbeFlag,
		Q: *ivfQFlag, K: *ivfKFlag, Seed: *ivfSeedFlag}
	return b
}

func newGUPS(d *driver.Driver) *gups.Benchmark {
	b := gups.NewBenchmark(d)
	b.Params = gups.Params{LogTable: *gupsLogFlag, WGs: *gupsWGsFlag, UpdateMult: *gupsMultFlag}
	return b
}

func newAllReduce(d *driver.Driver) *allreduce.Benchmark {
	b := allreduce.NewBenchmark(d)
	b.Params = allreduceParams()
	return b
}

// allreduceParams: one gradient copy per GPM of the problem size
// (-problem-gpms, 48 by default on any mesh).
func allreduceParams() allreduce.Params {
	return allreduce.Params{Ranks: runner.ProblemGPMs(), CUs: runner.CUsPerGPM(), PerCU: *arPerCUFlag}
}
