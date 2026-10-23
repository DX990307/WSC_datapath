package benchmarkselection

import (
	"flag"
	"testing"

	"github.com/sarchlab/akkalat/baseline/runner"
)

func TestT16SmallOnlyMatchesNewNames(t *testing.T) {
	for _, name := range []string{"pagerank", "im2col", "matrixtranspose",
		"spmv", "kmeans", "matrixmultiplication", "aes", "fir"} {
		if b := t16SmallBenchmark(name, nil); b != nil {
			t.Errorf("%s must keep its default input", name)
		}
	}
	for name, g := range t16SmallDefaultWGPerCU {
		if g <= 0 {
			t.Errorf("%s: no proposal", name)
		}
	}
	if len(t16SmallDefaultWGPerCU) != 7 {
		t.Errorf("want 7 workloads, got %d", len(t16SmallDefaultWGPerCU))
	}
}

// Strong scaling (mesh9): on a 9x9 mesh the T16 small grids and the
// allreduce ranks keep their 7x7 (48-GPM) sizes unless -problem-gpms is set.
func TestProblemSizeFollowsProblemGPMsNotMesh(t *testing.T) {
	defer func() {
		_ = flag.Set("mesh-size", "7")
		_ = flag.Set("problem-gpms", "48")
	}()
	base := t16TotalCUs()
	if err := flag.Set("mesh-size", "9"); err != nil {
		t.Fatal(err)
	}
	if runner.NumGPMs() != 80 {
		t.Fatalf("NumGPMs = %d on 9x9", runner.NumGPMs())
	}
	if got := t16TotalCUs(); got != base || got != 48*runner.CUsPerGPM() {
		t.Fatalf("9x9 strong scaling: t16TotalCUs = %d, want %d", got, base)
	}
	if r := allreduceParams().Ranks; r != 48 {
		t.Fatalf("9x9 strong scaling: allreduce ranks = %d, want 48", r)
	}
	if err := flag.Set("problem-gpms", "80"); err != nil {
		t.Fatal(err)
	}
	if got := t16TotalCUs(); got != 80*runner.CUsPerGPM() {
		t.Fatalf("weak scaling: t16TotalCUs = %d, want %d", got, 80*runner.CUsPerGPM())
	}
}

// The scaled 9x9 config (mi200s, 16 CUs per GPM) keeps the 7x7 problem with
// -problem-cus-per-gpm=28.
func TestProblemSizeFollowsProblemCUs(t *testing.T) {
	defer func() {
		_ = flag.Set("gpm-preset", "hpca")
		_ = flag.Set("problem-cus-per-gpm", "0")
	}()
	if err := flag.Set("gpm-preset", "mi200s"); err != nil {
		t.Fatal(err)
	}
	if runner.CUsPerGPM() != 16 || t16TotalCUs() != 48*16 {
		t.Fatalf("mi200s: %d CUs per GPM, t16TotalCUs %d", runner.CUsPerGPM(), t16TotalCUs())
	}
	if err := flag.Set("problem-cus-per-gpm", "28"); err != nil {
		t.Fatal(err)
	}
	if t16TotalCUs() != 1344 || allreduceParams().CUs != 28 {
		t.Fatalf("problem CUs: t16TotalCUs %d, allreduce CUs %d", t16TotalCUs(), allreduceParams().CUs)
	}
}
