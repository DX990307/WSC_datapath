package benchmarkselection

import (
	"flag"

	"github.com/sarchlab/akkalat/baseline/runner"
	"github.com/sarchlab/mgpusim/v3/benchmarks/t17/moe"
	"github.com/sarchlab/mgpusim/v3/benchmarks/t17/pagedattn"
	"github.com/sarchlab/mgpusim/v3/benchmarks/t17/rgat"
	"github.com/sarchlab/mgpusim/v3/driver"
)

// T17 W-A PagedAttention decode size knobs. Defaults are a small smoke size;
// the T17 target shape is -pa-batch=32 -pa-len-mean=1024.
var (
	paDefaults        = pagedattn.DefaultParams()
	paBatchFlag       = flag.Int("pa-batch", paDefaults.Batch, "pagedattn: sequences in the decode batch")
	paKVHeadsFlag     = flag.Int("pa-kv-heads", paDefaults.KVHeads, "pagedattn: KV heads (query heads = 4x)")
	paPartTokensFlag  = flag.Int("pa-part-tokens", paDefaults.PartTokens, "pagedattn: tokens per partition (multiple of 16, <= 512)")
	paLenMeanFlag     = flag.Float64("pa-len-mean", paDefaults.Dist.Mean, "pagedattn: mean context length (log-normal)")
	paLenSigmaFlag    = flag.Float64("pa-len-sigma", paDefaults.Dist.Sigma, "pagedattn: sigma of ln(context length)")
	paLenMinFlag      = flag.Int("pa-len-min", paDefaults.Dist.Min, "pagedattn: minimum context length")
	paLenMaxFlag      = flag.Int("pa-len-max", paDefaults.Dist.Max, "pagedattn: maximum context length")
	paPoolFactorFlag  = flag.Float64("pa-pool-factor", paDefaults.PoolFactor, "pagedattn: KV pool blocks / blocks the batch needs")
	paChurnRoundsFlag = flag.Int("pa-churn-rounds", paDefaults.Churn.Rounds, "pagedattn: allocate/free rounds before the batch is allocated")
	paSeedFlag        = flag.Int64("pa-seed", paDefaults.Seed, "pagedattn: input generator seed")
)

func newPagedAttn(d *driver.Driver) *pagedattn.Benchmark {
	b := pagedattn.NewBenchmark(d)
	p := &b.Params
	p.Batch = *paBatchFlag
	p.KVHeads = *paKVHeadsFlag
	p.PartTokens = *paPartTokensFlag
	p.Dist.Mean = *paLenMeanFlag
	p.Dist.Sigma = *paLenSigmaFlag
	p.Dist.Min = *paLenMinFlag
	p.Dist.Max = *paLenMaxFlag
	p.PoolFactor = *paPoolFactorFlag
	p.Churn.Rounds = *paChurnRoundsFlag
	p.Seed = *paSeedFlag
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
	b.CUsPerGPM = runner.CUsPerGPM()
	return b
}

func newRGATMini(d *driver.Driver) *rgat.Benchmark { return rgat.NewBenchmark(d) }
