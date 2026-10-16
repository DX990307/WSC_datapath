package benchmarkselection

// T16 1d: opt-in small inputs whose every kernel can run to completion on the
// 48-GPM wafer (no -max-wg). They are only selected by the "<name>-t16small"
// benchmark names; every existing benchmark name keeps its input unchanged.
//
// Each input is sized from g, the target number of work-groups per CU of a
// kernel (48 GPMs x CUs per GPM; -t16-wg-per-cu, default 4), keeping the
// shape parameters of the default input (pagerank: average degree; spmv:
// non-zeros per row; matrixmultiplication: inner and output widths; im2col:
// 3 channels, 3x3 kernel; kmeans: 32 features, 8 clusters, 2 iterations).

import (
	"flag"
	"fmt"
	"math"
	"os"

	"github.com/sarchlab/akkalat/baseline/runner"
	"github.com/sarchlab/mgpusim/v3/benchmarks"
	"github.com/sarchlab/mgpusim/v3/benchmarks/amdappsdk/matrixmultiplication"
	"github.com/sarchlab/mgpusim/v3/benchmarks/amdappsdk/matrixtranspose"
	"github.com/sarchlab/mgpusim/v3/benchmarks/dnn/layer_benchmarks/im2col"
	"github.com/sarchlab/mgpusim/v3/benchmarks/heteromark/aes"
	"github.com/sarchlab/mgpusim/v3/benchmarks/heteromark/kmeans"
	"github.com/sarchlab/mgpusim/v3/benchmarks/heteromark/pagerank"
	"github.com/sarchlab/mgpusim/v3/benchmarks/shoc/spmv"
	"github.com/sarchlab/mgpusim/v3/driver"
)

var t16WGPerCUFlag = flag.Float64("t16-wg-per-cu", 4,
	"T16 *-t16small inputs: target work-groups per CU of each kernel")

const (
	t16NumGPM = 48
	// Default pagerank: 132,644,864 connections over 2^20 nodes.
	t16PagerankDegree = 132644864.0 / (1 << 20)
	// Default spmv: Dim = 85 * 2^20, sparsity 1e-9 -> 0.0891 non-zeros per row.
	t16SpmvNNZPerRow = float64(1<<20*85) * 0.000000001
)

func t16TotalCUs() int {
	return t16NumGPM * runner.CUsPerGPM()
}

func roundUp(v, m int) int { return (v + m - 1) / m * m }

// t16SmallBenchmark returns the small benchmark for name, or nil when name is
// not a *-t16small name.
func t16SmallBenchmark(name string, d *driver.Driver) benchmarks.Benchmark {
	g := *t16WGPerCUFlag
	wgs := int(math.Ceil(g * float64(t16TotalCUs())))
	var b benchmarks.Benchmark
	var params string
	switch name {
	case "pagerank-t16small":
		// One work-group (64 threads) per row.
		pr := pagerank.NewBenchmark(d)
		pr.NumNodes = uint32(wgs)
		pr.NumConnections = uint32(math.Round(t16PagerankDegree * float64(wgs)))
		pr.MaxIterations = 1
		params = fmt.Sprintf("NumNodes=%d NumConnections=%d MaxIterations=1",
			pr.NumNodes, pr.NumConnections)
		b = pr
	case "im2col-t16small":
		// WGs = ceil(F*F/8) * ceil(27/8) with F = H - 2.
		// Rounded down: more WGs than g per CU would raise the driver's
		// ceil(WGs / CUs) and leave the last GPMs without work.
		f := int(math.Floor(math.Sqrt(float64(wgs) * 8 / 4)))
		ic := im2col.NewBenchmark(d)
		ic.N, ic.C, ic.H, ic.W = 1, 3, f+2, f+2
		ic.KernelHeight, ic.KernelWidth = 3, 3
		ic.StrideX, ic.StrideY = 1, 1
		ic.DilateX, ic.DilateY = 1, 1
		wgs = (f*f + 7) / 8 * 4
		params = fmt.Sprintf("N=1 C=3 H=W=%d kernel=3x3", f+2)
		b = ic
	case "matrixtranspose-t16small":
		// WGs = (Width / 64)^2.
		side := int(math.Floor(math.Sqrt(float64(wgs)))) // rounded down, see im2col
		mt := matrixtranspose.NewBenchmark(d)
		mt.Width = side * 64
		wgs = side * side
		params = fmt.Sprintf("Width=%d", mt.Width)
		b = mt
	case "spmv-t16small":
		// One thread per row, 128 rows per work-group.
		sp := spmv.NewBenchmark(d)
		sp.Dim = int32(wgs * 128)
		sp.Sparsity = t16SpmvNNZPerRow / float64(sp.Dim)
		params = fmt.Sprintf("Dim=%d Sparsity=%.4g (%.4f nnz/row)",
			sp.Dim, sp.Sparsity, t16SpmvNNZPerRow)
		b = sp
	case "kmeans-t16small":
		// 64 points per work-group; transpose + 2 membership kernels.
		km := kmeans.NewBenchmark(d)
		km.NumPoints = wgs * 64
		km.NumClusters = 8
		km.NumFeatures = 32
		km.MaxIter = 2
		params = fmt.Sprintf("NumPoints=%d NumClusters=8 NumFeatures=32 MaxIter=2",
			km.NumPoints)
		b = km
	case "matrixmultiplication-t16small":
		// WGs = (Z/32) * (Y/32) with X = Z = 128 as in the default input.
		mm := matrixmultiplication.NewBenchmark(d)
		mm.X = 2048 / 16
		mm.Z = 2048 / 16
		mm.Y = uint32(roundUp(wgs*8, 32))
		wgs = int(mm.Y) / 8
		params = fmt.Sprintf("X=128 Y=%d Z=128", mm.Y)
		b = mm
	case "aes-t16small":
		// 64 threads x 16 bytes per work-group.
		a := aes.NewBenchmark(d)
		a.Length = wgs * 1024
		params = fmt.Sprintf("Length=%d", a.Length)
		b = a
	default:
		return nil
	}
	fmt.Fprintf(os.Stderr, "[T16small] %s: %s, %d WGs per kernel (%.2f per CU)\n",
		name, params, wgs, float64(wgs)/float64(t16TotalCUs()))
	return b
}
