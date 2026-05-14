package main

import (
	"flag"
	"log"
	"math"

	"github.com/sarchlab/mgpusim/v3/benchmarks/llm/kvcache"
	"github.com/sarchlab/mgpusim/v3/samples/runner"
)

var modeFlag = flag.String("mode", "decode",
	"KV-cache mode: scan, decode, or decode-30b.")
var numLayersFlag = flag.Int("layers", 0,
	"Number of transformer layers. 0 keeps the selected mode default.")
var numHeadsFlag = flag.Int("heads", 0,
	"Number of query heads. 0 keeps the selected mode default.")
var numKVHeadsFlag = flag.Int("kv-heads", 0,
	"Number of KV heads. 0 keeps the selected mode default.")
var seqLenFlag = flag.Int("seq-len", 0,
	"KV-cache sequence length. 0 keeps the selected mode default.")
var headDimFlag = flag.Int("head-dim", 0,
	"Attention head dimension. 0 keeps the selected mode default.")
var decodeStepsFlag = flag.Int("decode-steps", 0,
	"Number of decode tokens to model. 0 keeps the selected mode default.")
var seqBlockFlag = flag.Int("seq-block", 0,
	"Sequence block size per QK/value-reduce workgroup. 0 keeps the selected mode default.")
var wgSizeFlag = flag.Uint("wg-size", 0,
	"Workgroup size. 0 keeps the selected mode default.")

func main() {
	flag.Parse()

	runner := new(runner.Runner).ParseFlag().Init()

	benchmark := newBenchmark(*modeFlag, runner)
	applyOverrides(benchmark)

	runner.AddBenchmark(benchmark)
	runner.Run()
}

func newBenchmark(mode string, r *runner.Runner) *kvcache.Benchmark {
	switch mode {
	case "scan":
		return kvcache.NewBenchmark(r.Driver())
	case "decode":
		return kvcache.NewDecodeBenchmark(r.Driver())
	case "decode-30b":
		return kvcache.NewDecode30BBenchmark(r.Driver())
	default:
		log.Fatalf("unknown -mode %q; expected scan, decode, or decode-30b", mode)
		return nil
	}
}

func applyOverrides(benchmark *kvcache.Benchmark) {
	if *numLayersFlag > 0 {
		benchmark.NumLayers = *numLayersFlag
	}
	if *numHeadsFlag > 0 {
		benchmark.NumHeads = *numHeadsFlag
	}
	if *numKVHeadsFlag > 0 {
		benchmark.NumKVHeads = *numKVHeadsFlag
	}
	if *seqLenFlag > 0 {
		benchmark.SeqLen = *seqLenFlag
	}
	if *headDimFlag > 0 {
		benchmark.HeadDim = *headDimFlag
	}
	if *decodeStepsFlag > 0 {
		benchmark.DecodeStep = *decodeStepsFlag
	}
	if *seqBlockFlag > 0 {
		benchmark.SeqBlock = *seqBlockFlag
	}
	if *wgSizeFlag > 0 {
		if *wgSizeFlag > math.MaxUint16 {
			log.Fatalf("-wg-size=%d exceeds uint16 limit", *wgSizeFlag)
		}
		benchmark.WGSize = uint16(*wgSizeFlag)
	}
}
