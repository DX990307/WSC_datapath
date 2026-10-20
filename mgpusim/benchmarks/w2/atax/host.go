package atax

import (
	"fmt"
	"math/rand"
)

// WGSize is the work-group size of all three kernels.
const WGSize = 256

// Params is the problem size.
type Params struct {
	NX, NY       int
	RowsPerChunk int // rows per atax_k2 work-group
	Seed         int64
}

// Validate checks the sizes.
func (p Params) Validate() error {
	if p.NX < 1 || p.NY < 1 || p.RowsPerChunk < 1 {
		return fmt.Errorf("atax: bad sizes %+v", p)
	}
	return nil
}

// NumChunks is the number of row chunks of atax_k2.
func (p Params) NumChunks() int { return (p.NX + p.RowsPerChunk - 1) / p.RowsPerChunk }

// NumColBlocks is the number of 256-column blocks.
func (p Params) NumColBlocks() int { return (p.NY + WGSize - 1) / WGSize }

// Generate returns A (NX x NY) and x (NY), values (rand % 100) / 10 as
// upstream.
func Generate(p Params) (a, x []float32) {
	rng := rand.New(rand.NewSource(p.Seed))
	a = make([]float32, p.NX*p.NY)
	for i := range a {
		a[i] = float32(rng.Intn(100)) / 10
	}
	x = make([]float32, p.NY)
	for i := range x {
		x[i] = float32(rng.Intn(100)) / 10
	}
	return a, x
}

// Reference is cpu_atax (cpu_reference.h) in float64.
func Reference(a, x []float32, nx, ny int) []float64 {
	tmp := make([]float64, nx)
	for i := 0; i < nx; i++ {
		s := 0.0
		for j := 0; j < ny; j++ {
			s += float64(a[i*ny+j]) * float64(x[j])
		}
		tmp[i] = s
	}
	y := make([]float64, ny)
	for i := 0; i < nx; i++ {
		for j := 0; j < ny; j++ {
			y[j] += float64(a[i*ny+j]) * tmp[i]
		}
	}
	return y
}
