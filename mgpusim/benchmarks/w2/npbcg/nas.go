package npbcg

import (
	"math"
	"sort"
)

// Class is a NAS CG problem class (npb_cg/nas_cg.h).
type Class struct {
	Name       string
	N, Nonzer  int
	Iterations int
	Shift      float64
	Zeta       float64
}

// Classes are the standard NAS CG classes.
var Classes = map[string]Class{
	"S": {"S", 1400, 7, 15, 10.0, 8.5971775078648},
	"W": {"W", 7000, 8, 15, 12.0, 10.362595087124},
	"A": {"A", 14000, 11, 15, 20.0, 17.130235054029},
	"B": {"B", 75000, 13, 75, 60.0, 22.712745482631},
	"C": {"C", 150000, 15, 75, 110.0, 28.973605592845},
}

const rcond = 0.1

// Matrix is a CSR matrix.
type Matrix struct {
	Offsets, Columns []int32
	Values           []float64
}

func random(state *uint64) float64 {
	*state = (*state * 1220703125) & ((1 << 46) - 1)
	return float64(*state) * (1.0 / 70368744177664.0)
}

// MakeMatrix is nas_cg::make_matrix (the NAS makea sparse outer-product
// construction, seed 314159265, one discarded draw).
func MakeMatrix(c Class) *Matrix {
	n, stride := c.N, c.Nonzer+1
	counts := make([]int, n)
	columns := make([]int, n*stride)
	elements := make([]float64, n*stride)
	scales := make([]float64, n)
	var state uint64 = 314159265
	random(&state)
	nn1 := 1
	for nn1 < n {
		nn1 *= 2
	}
	for i := 0; i < n; i++ {
		used := 0
		for used < c.Nonzer {
			value := random(&state)
			column := int(float64(nn1) * random(&state))
			if column >= n {
				continue
			}
			dup := false
			for k := 0; k < used; k++ {
				if columns[i*stride+k] == column {
					dup = true
				}
			}
			if dup {
				continue
			}
			columns[i*stride+used] = column
			elements[i*stride+used] = value
			used++
		}
		diag := 0
		for diag < used && columns[i*stride+diag] != i {
			diag++
		}
		if diag == used {
			columns[i*stride+used] = i
			used++
		}
		elements[i*stride+diag] = 0.5
		counts[i] = used
	}
	incOff := make([]int, n+1)
	for i := 0; i < n; i++ {
		for k := 0; k < counts[i]; k++ {
			incOff[columns[i*stride+k]+1]++
		}
	}
	for i := 0; i < n; i++ {
		incOff[i+1] += incOff[i]
	}
	cursor := append([]int(nil), incOff...)
	incidence := make([]int, incOff[n])
	for i := 0; i < n; i++ {
		for k := 0; k < counts[i]; k++ {
			col := columns[i*stride+k]
			incidence[cursor[col]] = i*stride + k
			cursor[col]++
		}
	}
	ratio := math.Pow(rcond, 1.0/float64(n))
	size := 1.0
	for i := 0; i < n; i++ {
		scales[i] = size
		size *= ratio
	}
	a := &Matrix{Offsets: make([]int32, n+1)}
	seen := make([]int, n)
	for i := range seen {
		seen[i] = -1
	}
	sums := make([]float64, n)
	var touched []int
	for row := 0; row < n; row++ {
		touched = touched[:0]
		for p := incOff[row]; p < incOff[row+1]; p++ {
			pos := incidence[p]
			outer := pos / stride
			scale := scales[outer] * elements[pos]
			for k := 0; k < counts[outer]; k++ {
				col := columns[outer*stride+k]
				value := elements[outer*stride+k] * scale
				if col == row && row == outer {
					value = value + rcond - c.Shift
				}
				if seen[col] != row {
					seen[col] = row
					sums[col] = 0
					touched = append(touched, col)
				}
				sums[col] += value
			}
		}
		sort.Ints(touched)
		for _, col := range touched {
			a.Columns = append(a.Columns, int32(col))
			a.Values = append(a.Values, sums[col])
		}
		a.Offsets[row+1] = int32(len(a.Columns))
	}
	return a
}

func (a *Matrix) spmv(x, y []float64) {
	for row := range y {
		s := 0.0
		for k := a.Offsets[row]; k < a.Offsets[row+1]; k++ {
			s += a.Values[k] * x[a.Columns[k]]
		}
		y[row] = s
	}
}

func dot(a, b []float64) float64 {
	s := 0.0
	for i := range a {
		s += a[i] * b[i]
	}
	return s
}

// Result holds the reference state after the outer iterations.
type Result struct {
	X, Z, Zeta, Residual []float64
}

// Reference is nas_cg::reference in fp64 for the given outer and inner
// iteration counts (NAS: c.Iterations outer, 25 inner).
func Reference(a *Matrix, c Class, outer, inner int) Result {
	n := c.N
	x := make([]float64, n)
	for i := range x {
		x[i] = 1
	}
	z, p, q, r := make([]float64, n), make([]float64, n), make([]float64, n), make([]float64, n)
	res := Result{}
	for o := 0; o < outer; o++ {
		for i := range z {
			z[i], q[i] = 0, 0
		}
		copy(r, x)
		copy(p, r)
		rho := dot(r, r)
		for k := 0; k < inner; k++ {
			a.spmv(p, q)
			alpha := rho / dot(p, q)
			prev := rho
			for i := 0; i < n; i++ {
				z[i] += alpha * p[i]
				r[i] -= alpha * q[i]
			}
			rho = dot(r, r)
			beta := rho / prev
			for i := 0; i < n; i++ {
				p[i] = r[i] + beta*p[i]
			}
		}
		a.spmv(z, r)
		s := 0.0
		for i := 0; i < n; i++ {
			d := x[i] - r[i]
			s += d * d
		}
		res.Residual = append(res.Residual, math.Sqrt(s))
		res.Zeta = append(res.Zeta, c.Shift+1.0/dot(x, z))
		inv := 1.0 / math.Sqrt(dot(z, z))
		for i := 0; i < n; i++ {
			x[i] = inv * z[i]
		}
	}
	res.X = x
	res.Z = z
	return res
}
