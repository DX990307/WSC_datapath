package ivfflat

import (
	"os"
	"testing"
	"time"
)

// TestBuildLarge times Build at a candidate full size (IVF_LARGE=1).
func TestBuildLarge(t *testing.T) {
	if os.Getenv("IVF_LARGE") == "" {
		t.Skip("set IVF_LARGE=1")
	}
	t0 := time.Now()
	x, err := Build(Params{N: 1 << 20, NList: 4096, NProbe: 16, Q: 512, K: 10, Seed: 1})
	if err != nil {
		t.Fatal(err)
	}
	max := 0
	for q := 0; q < x.Q; q++ {
		if n := int(x.QItems[q+1] - x.QItems[q]); n > max {
			max = n
		}
	}
	t.Logf("build %v, items %d, scanned %d, max items/query %d", time.Since(t0), x.NumItems(), x.ScannedVectors(), max)
}
