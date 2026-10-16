package benchmarkselection

import (
	"flag"

	"github.com/sarchlab/akkalat/baseline/runner"
	"github.com/sarchlab/mgpusim/v3/benchmarks/t17/pagedattn"
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
