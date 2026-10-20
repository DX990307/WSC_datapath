package atax

import (
	"math"
	"testing"
)

// TestTiledSumsMatchReference checks the tiling of kernels.cl (k1 row
// sums, k2 chunk partials in [colblock][chunk][256] order, k2red) on the
// host against cpu_atax.
func TestTiledSumsMatchReference(t *testing.T) {
	p := Params{NX: 300, NY: 520, RowsPerChunk: 64, Seed: 1}
	a, x := Generate(p)
	tmp := make([]float32, p.NX)
	for i := 0; i < p.NX; i++ {
		var s float32
		for j := 0; j < p.NY; j++ {
			s += a[i*p.NY+j] * x[j]
		}
		tmp[i] = s
	}
	nc, ncb := p.NumChunks(), p.NumColBlocks()
	part := make([]float32, ncb*nc*WGSize)
	for wg := 0; wg < ncb*nc; wg++ {
		cb, rc := wg/nc, wg%nc
		for l := 0; l < WGSize; l++ {
			j := cb*WGSize + l
			var s float32
			if j < p.NY {
				for i := rc * p.RowsPerChunk; i < (rc+1)*p.RowsPerChunk && i < p.NX; i++ {
					s += a[i*p.NY+j] * tmp[i]
				}
			}
			part[(cb*nc+rc)*WGSize+l] = s
		}
	}
	ref := Reference(a, x, p.NX, p.NY)
	for j := 0; j < p.NY; j++ {
		cb, l := j/WGSize, j%WGSize
		var s float32
		for rc := 0; rc < nc; rc++ {
			s += part[(cb*nc+rc)*WGSize+l]
		}
		if math.Abs(float64(s)-ref[j]) > 1e-4*math.Abs(ref[j]) {
			t.Fatalf("y[%d] = %g, want %g", j, s, ref[j])
		}
	}
}
