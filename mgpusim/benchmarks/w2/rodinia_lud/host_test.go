package rodinialud

import "testing"

// Unblocked in-place LU (no pivoting) gives the same factor as the blocked
// kernels up to rounding; the residual check must accept it and reject the
// unfactored matrix.
func TestResidual(t *testing.T) {
	n := 64
	a := Matrix(n, 42)
	lu := append([]float32(nil), a...)
	for k := 0; k < n; k++ {
		for i := k + 1; i < n; i++ {
			lu[i*n+k] /= lu[k*n+k]
			for j := k + 1; j < n; j++ {
				lu[i*n+j] -= lu[i*n+k] * lu[k*n+j]
			}
		}
	}
	if r := Residual(lu, a, n); r > 1e-6 {
		t.Fatalf("residual %g", r)
	}
	if r := Residual(a, a, n); r < 1e-2 {
		t.Fatalf("unfactored residual %g", r)
	}
}
