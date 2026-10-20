package fft2d

import (
	"fmt"
	"math"
	"math/cmplx"
	"math/rand"
)

// Params is the problem size: an N x N complex matrix, N = 512 * M.
type Params struct {
	N    int
	Seed int64
}

// Validate checks N = 512 * m, m in {1, 2, 4, 8}.
func (p Params) Validate() error {
	switch p.N {
	case 512, 1024, 2048, 4096:
		return nil
	}
	return fmt.Errorf("fft2d: N = %d must be 512, 1024, 2048 or 4096", p.N)
}

// M is N / 512.
func (p Params) M() int { return p.N / 512 }

// LogN is log2(N).
func (p Params) LogN() int {
	l := 0
	for 1<<l < p.N {
		l++
	}
	return l
}

// Generate returns N*N complex values (interleaved re, im) uniform in
// [-1, 1] as SHOC's FFT init.
func Generate(p Params) []float32 {
	rng := rand.New(rand.NewSource(p.Seed))
	d := make([]float32, 2*p.N*p.N)
	for i := range d {
		d[i] = rng.Float32()*2 - 1
	}
	return d
}

// fft is an in-place radix-2 FFT (sign -1 forward), float64.
func fft(x []complex128, sign float64) {
	n := len(x)
	for i, j := 1, 0; i < n; i++ {
		bit := n >> 1
		for ; j&bit != 0; bit >>= 1 {
			j ^= bit
		}
		j ^= bit
		if i < j {
			x[i], x[j] = x[j], x[i]
		}
	}
	for l := 2; l <= n; l <<= 1 {
		w := cmplx.Exp(complex(0, sign*2*math.Pi/float64(l)))
		for i := 0; i < n; i += l {
			wk := complex(1, 0)
			for k := 0; k < l/2; k++ {
				u, v := x[i+k], x[i+k+l/2]*wk
				x[i+k], x[i+k+l/2] = u+v, u-v
				wk *= w
			}
		}
	}
}

// Reference2D returns the forward 2D DFT of d (unnormalised), float64.
func Reference2D(d []float32, n int) []complex128 {
	x := make([]complex128, n*n)
	for i := range x {
		x[i] = complex(float64(d[2*i]), float64(d[2*i+1]))
	}
	row := make([]complex128, n)
	for r := 0; r < n; r++ {
		fft(x[r*n:(r+1)*n], -1)
	}
	for c := 0; c < n; c++ {
		for r := 0; r < n; r++ {
			row[r] = x[r*n+c]
		}
		fft(row, -1)
		for r := 0; r < n; r++ {
			x[r*n+c] = row[r]
		}
	}
	return x
}
