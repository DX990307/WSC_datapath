package benchmarkselection

import "testing"

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
