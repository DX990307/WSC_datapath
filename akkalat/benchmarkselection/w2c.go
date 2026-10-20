package benchmarkselection

import (
	"flag"

	"github.com/sarchlab/mgpusim/v3/benchmarks"
	"github.com/sarchlab/mgpusim/v3/benchmarks/w2/atax"
	"github.com/sarchlab/mgpusim/v3/benchmarks/w2/fft2d"
	"github.com/sarchlab/mgpusim/v3/benchmarks/w2/npbis"
	"github.com/sarchlab/mgpusim/v3/benchmarks/w2/shocsort"
	"github.com/sarchlab/mgpusim/v3/driver"
)

// W2 group c workloads (ported from gpu_benchmarks tier2). Defaults are
// small smoke sizes.
var (
	w2SortNFlag    = flag.Int("shocsort-n", shocsort.DefaultParams().N, "w2-shocsort: key/value pairs (multiple of 1024)")
	w2SortSeedFlag = flag.Int64("shocsort-seed", shocsort.DefaultParams().Seed, "w2-shocsort: key generator seed")

	w2AtaxNXFlag    = flag.Int("atax-nx", atax.DefaultParams().NX, "w2-atax: rows of A")
	w2AtaxNYFlag    = flag.Int("atax-ny", atax.DefaultParams().NY, "w2-atax: columns of A")
	w2AtaxChunkFlag = flag.Int("atax-rows-per-chunk", atax.DefaultParams().RowsPerChunk,
		"w2-atax: rows per work-group of the column pass (A^T tmp)")
	w2AtaxSeedFlag = flag.Int64("atax-seed", atax.DefaultParams().Seed, "w2-atax: input generator seed")

	w2FFTNFlag    = flag.Int("fft2d-n", fft2d.DefaultParams().N, "w2-fft2d: matrix size N (512, 1024, 2048 or 4096)")
	w2FFTSeedFlag = flag.Int64("fft2d-seed", fft2d.DefaultParams().Seed, "w2-fft2d: input generator seed")

	w2ISNFlag = flag.Int("npbis-n", npbis.DefaultParams().N,
		"w2-npbis: keys (multiple of 2048; 2^16/2^20/2^23/2^25 = NPB class S/W/A/B, else MAX_KEY = N/16)")
	w2ISIterFlag = flag.Int("npbis-iterations", npbis.DefaultParams().Iterations, "w2-npbis: rank() iterations (upstream 10)")
)

// w2cBenchmark returns the W2 group-c benchmark called name, or nil.
func w2cBenchmark(name string, d *driver.Driver) benchmarks.Benchmark {
	switch name {
	case "w2-shocsort":
		b := shocsort.NewBenchmark(d)
		b.Params = shocsort.Params{N: *w2SortNFlag, Seed: *w2SortSeedFlag}
		return b
	case "w2-atax":
		b := atax.NewBenchmark(d)
		b.Params = atax.Params{NX: *w2AtaxNXFlag, NY: *w2AtaxNYFlag,
			RowsPerChunk: *w2AtaxChunkFlag, Seed: *w2AtaxSeedFlag}
		return b
	case "w2-fft2d":
		b := fft2d.NewBenchmark(d)
		b.Params = fft2d.Params{N: *w2FFTNFlag, Seed: *w2FFTSeedFlag}
		return b
	case "w2-npbis":
		b := npbis.NewBenchmark(d)
		b.Params = npbis.Params{N: *w2ISNFlag, Iterations: *w2ISIterFlag}
		return b
	}
	return nil
}
