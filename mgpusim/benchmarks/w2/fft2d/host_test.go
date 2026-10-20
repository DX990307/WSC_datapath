package fft2d

import (
	"math"
	"math/cmplx"
	"testing"
)

func TestFFTMatchesDFT(t *testing.T) {
	n := 16
	x := make([]complex128, n)
	for i := range x {
		x[i] = complex(float64(i%5)-2, float64(i%3))
	}
	want := make([]complex128, n)
	for k := 0; k < n; k++ {
		for j := 0; j < n; j++ {
			want[k] += x[j] * cmplx.Exp(complex(0, -2*math.Pi*float64(j*k)/float64(n)))
		}
	}
	fft(x, -1)
	for k := range x {
		if cmplx.Abs(x[k]-want[k]) > 1e-9 {
			t.Fatalf("k=%d got %v want %v", k, x[k], want[k])
		}
	}
}

// TestSplitRadix checks the N = 512*m decomposition used by the kernels
// (512-point FFTs over n = m*n1 + n2, twiddle W_N^(n2*k1), m-point DFTs to
// k = k1 + 512*k2) on the host.
func TestSplitRadix(t *testing.T) {
	for _, m := range []int{2, 4, 8} {
		n := 512 * m
		x := make([]complex128, n)
		for i := range x {
			x[i] = complex(math.Sin(float64(i)*0.37), math.Cos(float64(i)*0.11))
		}
		want := append([]complex128(nil), x...)
		fft(want, -1)
		y := make([]complex128, n)
		sub := make([]complex128, 512)
		for n2 := 0; n2 < m; n2++ {
			for n1 := 0; n1 < 512; n1++ {
				sub[n1] = x[m*n1+n2]
			}
			fft(sub, -1)
			for k1 := 0; k1 < 512; k1++ {
				y[m*k1+n2] = sub[k1] * cmplx.Exp(complex(0, -2*math.Pi*float64(n2*k1)/float64(n)))
			}
		}
		for k1 := 0; k1 < 512; k1++ {
			for k2 := 0; k2 < m; k2++ {
				var s complex128
				for n2 := 0; n2 < m; n2++ {
					s += y[m*k1+n2] * cmplx.Exp(complex(0, -2*math.Pi*float64(n2*k2)/float64(m)))
				}
				if cmplx.Abs(s-want[k1+512*k2]) > 1e-6 {
					t.Fatalf("m=%d k1=%d k2=%d", m, k1, k2)
				}
			}
		}
	}
}

func TestValidate(t *testing.T) {
	if (Params{N: 2048}).Validate() != nil || (Params{N: 1000}).Validate() == nil {
		t.Fatal("validate")
	}
	if (Params{N: 4096}).LogN() != 12 || (Params{N: 4096}).M() != 8 {
		t.Fatal("logn")
	}
}
