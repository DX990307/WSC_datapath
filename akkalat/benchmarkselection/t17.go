package benchmarkselection

import (
	"flag"

	"github.com/sarchlab/akkalat/baseline/runner"
	"github.com/sarchlab/mgpusim/v3/benchmarks/t17/isacheck"
	"github.com/sarchlab/mgpusim/v3/benchmarks/t17/moe"
	"github.com/sarchlab/mgpusim/v3/benchmarks/t17/pagedattn"
	"github.com/sarchlab/mgpusim/v3/benchmarks/t17/rgat"
	"github.com/sarchlab/mgpusim/v3/driver"
)

// T17 W-A PagedAttention decode size knobs. Defaults are a small smoke size;
// the T17 target shape is -pa-batch=32 (lengths from the Azure trace).
var (
	paDefaults       = pagedattn.DefaultParams()
	paBatchFlag      = flag.Int("pa-batch", paDefaults.Batch, "pagedattn: sequences in the decode batch")
	paKVHeadsFlag    = flag.Int("pa-kv-heads", paDefaults.KVHeads, "pagedattn: KV heads (query heads = 4x)")
	paPartTokensFlag = flag.Int("pa-part-tokens", paDefaults.PartTokens, "pagedattn: tokens per partition (multiple of 16, <= 512)")
	paLenSourceFlag  = flag.String("pa-len-source", paDefaults.LenSource,
		"pagedattn: context lengths: azure (Azure LLM inference trace 2023, conversation) or lognormal")
	paLenMeanFlag     = flag.Float64("pa-len-mean", paDefaults.Dist.Mean, "pagedattn: mean context length (lognormal source)")
	paLenSigmaFlag    = flag.Float64("pa-len-sigma", paDefaults.Dist.Sigma, "pagedattn: sigma of ln(context length) (lognormal source)")
	paLenMinFlag      = flag.Int("pa-len-min", paDefaults.Dist.Min, "pagedattn: minimum context length")
	paLenMaxFlag      = flag.Int("pa-len-max", paDefaults.Dist.Max, "pagedattn: maximum context length")
	paPoolFactorFlag  = flag.Float64("pa-pool-factor", paDefaults.PoolFactor, "pagedattn: KV pool blocks / blocks the batch needs")
	paChurnRoundsFlag = flag.Int("pa-churn-rounds", paDefaults.Churn.Rounds, "pagedattn: allocate/free rounds before the batch is allocated")
	paSeedFlag        = flag.Int64("pa-seed", paDefaults.Seed, "pagedattn: input generator seed")
	paStepsFlag       = flag.Int("pa-steps", 1,
		"pagedattn: consecutive decode steps (P12; every sequence grows by one token per step); 1 = the T17 input")
	t17PrintGroupsFlag = flag.Bool("t17-print-groups", false,
		"pagedattn, moe: print the work list / GEMM tiles that map work-groups to sequences / experts (P12)")
)

func newPagedAttn(d *driver.Driver) *pagedattn.Benchmark {
	b := pagedattn.NewBenchmark(d)
	p := &b.Params
	p.Batch = *paBatchFlag
	p.KVHeads = *paKVHeadsFlag
	p.PartTokens = *paPartTokensFlag
	p.LenSource = *paLenSourceFlag
	p.Dist.Mean = *paLenMeanFlag
	p.Dist.Sigma = *paLenSigmaFlag
	p.Dist.Min = *paLenMinFlag
	p.Dist.Max = *paLenMaxFlag
	p.PoolFactor = *paPoolFactorFlag
	p.Churn.Rounds = *paChurnRoundsFlag
	p.Seed = *paSeedFlag
	p.Steps = *paStepsFlag
	pagedattn.PrintGroups = *t17PrintGroupsFlag
	b.CUsPerGPM = runner.CUsPerGPM()
	return b
}

// T17 W-B MoE size knobs. Defaults are a small smoke size; the T17 target
// shape is -moe-tokens=256 -moe-hidden=1024 -moe-ffn=704 -moe-topk=6.
var (
	moeDefaults   = moe.DefaultParams()
	moeTokensFlag = flag.Int("moe-tokens", moeDefaults.Tokens, "moe: tokens in the decode batch")
	moeHiddenFlag = flag.Int("moe-hidden", moeDefaults.Hidden, "moe: hidden dimension (multiple of 64)")
	moeFFNFlag    = flag.Int("moe-ffn", moeDefaults.FFN, "moe: expert intermediate dimension (multiple of 64)")
	moeTopKFlag   = flag.Int("moe-topk", moeDefaults.TopK, "moe: experts per token (64 routed experts)")
	moeSkewFlag   = flag.Float64("moe-skew", moeDefaults.Skew,
		"moe: target max/mean expert load via a per-expert routing bias (0 = uniform, no bias)")
	moeSeedFlag = flag.Int64("moe-seed", moeDefaults.Seed, "moe: input generator seed")
)

func newMoE(d *driver.Driver) *moe.Benchmark {
	b := moe.NewBenchmark(d)
	b.Params = moe.Params{
		Tokens: *moeTokensFlag,
		Hidden: *moeHiddenFlag,
		FFN:    *moeFFNFlag,
		TopK:   *moeTopKFlag,
		Skew:   *moeSkewFlag,
		Seed:   *moeSeedFlag,
	}
	moe.PrintGroups = *t17PrintGroupsFlag
	b.CUsPerGPM = runner.CUsPerGPM()
	return b
}

// T17 W-C R-GAT knobs. Defaults: a small synthetic graph with the IGBH
// schema; the T17 shape is -rgat-data=<IGBH dir> -rgat-seeds=512
// -rgat-fanouts=10,5.
var (
	rgatDefaults    = rgat.DefaultParams()
	rgatDataFlag    = flag.String("rgat-data", "", "rgat: IGBH directory (e.g. .../igbh/tiny); empty = synthetic graph")
	rgatSeedsFlag   = flag.Int("rgat-seeds", rgatDefaults.Seeds, "rgat: paper seed nodes in the mini-batch")
	rgatFanoutsFlag = flag.String("rgat-fanouts", "10,5", "rgat: per-relation fanouts, input layer first")
	rgatHiddenFlag  = flag.Int("rgat-hidden", rgatDefaults.Hidden, "rgat: hidden dimension, all heads concatenated (multiple of 64)")
	rgatHeadsFlag   = flag.Int("rgat-heads", rgatDefaults.Heads,
		"rgat: attention heads (GATConv concat); each head takes rgat-hidden/rgat-heads columns")
	rgatSeedFlag   = flag.Int64("rgat-seed", rgatDefaults.Seed, "rgat: sampling and weight seed")
	rgatPapersFlag = flag.Int("rgat-synth-papers", rgatDefaults.SynthPapers, "rgat: synthetic graph papers")
	rgatDimFlag    = flag.Int("rgat-synth-dim", rgatDefaults.SynthDim, "rgat: synthetic feature dimension")
)

func newRGAT(d *driver.Driver) *rgat.Benchmark {
	b := rgat.NewBenchmark(d)
	b.Params = rgat.Params{
		DataDir:     *rgatDataFlag,
		SynthPapers: *rgatPapersFlag,
		SynthDim:    *rgatDimFlag,
		Seeds:       *rgatSeedsFlag,
		Fanouts:     parseInts(*rgatFanoutsFlag),
		Hidden:      *rgatHiddenFlag,
		Heads:       *rgatHeadsFlag,
		Seed:        *rgatSeedFlag,
	}
	return b
}

func newISACheck(d *driver.Driver) *isacheck.Benchmark { return isacheck.NewBenchmark(d) }
