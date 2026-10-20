package npbmg

import (
	"fmt"
	"math"
)

// Class is a NAS MG problem class.
type Class struct {
	Name       byte
	N          int // interior grid points per side
	Iterations int
	L2         float64 // published final L2 norm (fp64, Iterations cycles)
}

// Classes are the NAS classes (tier2 nas_mg.h).
var Classes = map[byte]Class{
	'S': {'S', 32, 4, 0.5307707005734e-04},
	'W': {'W', 128, 4, 0.6467329375339e-05},
	'A': {'A', 256, 4, 0.2433365309069e-05},
	'B': {'B', 256, 20, 0.1800564401355e-05},
	'C': {'C', 512, 20, 0.5706732285740e-06},
}

// OperatorA is the residual operator a = (-8/3, 0, 1/6, 1/12).
var OperatorA = [4]float64{-8.0 / 3, 0, 1.0 / 6, 1.0 / 12}

// Smoother returns the smoother coefficients c of the class.
func Smoother(c Class) [4]float64 {
	if c.Name == 'S' || c.Name == 'W' || c.Name == 'A' {
		return [4]float64{-3.0 / 8, 1.0 / 32, -1.0 / 64, 0}
	}
	return [4]float64{-3.0 / 17, 1.0 / 33, -1.0 / 61, 0}
}

// Layout is the level hierarchy: level k has width 2^(k+1)+2 (one periodic
// ghost on each side), all levels in one array at Offset[k].
type Layout struct {
	Width  []int
	Offset []int
	Cells  int
}

// NewLayout returns the hierarchy of an n^3 grid.
func NewLayout(n int) Layout {
	var l Layout
	for m := 2; m <= n; m *= 2 {
		l.Width = append(l.Width, m+2)
		l.Offset = append(l.Offset, l.Cells)
		l.Cells += (m + 2) * (m + 2) * (m + 2)
	}
	return l
}

// Levels returns the level count.
func (l Layout) Levels() int { return len(l.Width) }

func randomStep(s uint64) uint64 { return (s * 1220703125) & (1<<46 - 1) }

// Input returns NAS zran3's right-hand side v (width n+2, periodic ghosts):
// +1 at the ten largest and -1 at the ten smallest values of the 46-bit
// random stream (seed 314159265), x fastest.
func Input(n int) []float64 {
	p := n + 2
	v := make([]float64, p*p*p)
	type cand struct {
		val uint64
		idx int
	}
	var large, small [10]cand
	for i := range small {
		small[i] = cand{1 << 46, 0}
	}
	s := uint64(314159265)
	for z := 1; z <= n; z++ {
		for y := 1; y <= n; y++ {
			for x := 1; x <= n; x++ {
				s = randomStep(s)
				i := (z*p+y)*p + x
				if s > large[0].val {
					large[0] = cand{s, i}
					for j := 0; j < 9 && large[j].val > large[j+1].val; j++ {
						large[j], large[j+1] = large[j+1], large[j]
					}
				}
				if s < small[0].val {
					small[0] = cand{s, i}
					for j := 0; j < 9 && small[j].val < small[j+1].val; j++ {
						small[j], small[j+1] = small[j+1], small[j]
					}
				}
			}
		}
	}
	for j := 9; j >= 0; j-- {
		v[small[j].idx] = -1
	}
	for j := 9; j >= 0; j-- {
		v[large[j].idx] = 1
	}
	comm3(v, p)
	return v
}

func comm3(u []float64, n int) {
	at := func(z, y, x int) int { return (z*n+y)*n + x }
	for z := 1; z < n-1; z++ {
		for y := 1; y < n-1; y++ {
			u[at(z, y, 0)] = u[at(z, y, n-2)]
			u[at(z, y, n-1)] = u[at(z, y, 1)]
		}
	}
	for z := 1; z < n-1; z++ {
		for x := 0; x < n; x++ {
			u[at(z, 0, x)] = u[at(z, n-2, x)]
			u[at(z, n-1, x)] = u[at(z, 1, x)]
		}
	}
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			u[at(0, y, x)] = u[at(n-2, y, x)]
			u[at(n-1, y, x)] = u[at(1, y, x)]
		}
	}
}

// resid: r = v - A u (r may alias v), then comm3(r).
func resid(u, v, r []float64, n int, a [4]float64) {
	at := func(z, y, x int) int { return (z*n+y)*n + x }
	u1, u2 := make([]float64, n), make([]float64, n)
	for z := 1; z < n-1; z++ {
		for y := 1; y < n-1; y++ {
			for x := 0; x < n; x++ {
				u1[x] = u[at(z, y-1, x)] + u[at(z, y+1, x)] + u[at(z-1, y, x)] + u[at(z+1, y, x)]
				u2[x] = u[at(z-1, y-1, x)] + u[at(z-1, y+1, x)] + u[at(z+1, y-1, x)] + u[at(z+1, y+1, x)]
			}
			for x := 1; x < n-1; x++ {
				r[at(z, y, x)] = v[at(z, y, x)] - a[0]*u[at(z, y, x)] -
					a[2]*(u2[x]+u1[x-1]+u1[x+1]) - a[3]*(u2[x-1]+u2[x+1])
			}
		}
	}
	comm3(r, n)
}

// psinv: u += smoother(r), then comm3(u).
func psinv(r, u []float64, n int, c [4]float64) {
	at := func(z, y, x int) int { return (z*n+y)*n + x }
	r1, r2 := make([]float64, n), make([]float64, n)
	for z := 1; z < n-1; z++ {
		for y := 1; y < n-1; y++ {
			for x := 0; x < n; x++ {
				r1[x] = r[at(z, y-1, x)] + r[at(z, y+1, x)] + r[at(z-1, y, x)] + r[at(z+1, y, x)]
				r2[x] = r[at(z-1, y-1, x)] + r[at(z-1, y+1, x)] + r[at(z+1, y-1, x)] + r[at(z+1, y+1, x)]
			}
			for x := 1; x < n-1; x++ {
				u[at(z, y, x)] = u[at(z, y, x)] + c[0]*r[at(z, y, x)] +
					c[1]*(r[at(z, y, x-1)]+r[at(z, y, x+1)]+r1[x]) +
					c[2]*(r2[x]+r1[x-1]+r1[x+1])
			}
		}
	}
	comm3(u, n)
}

// rprj3 restricts r (width m) to s (width mj), then comm3(s).
func rprj3(r []float64, m int, s []float64, mj int) {
	R := func(z, y, x int) float64 { return r[(z*m+y)*m+x] }
	x1, y1 := make([]float64, m), make([]float64, m)
	for j3 := 1; j3 < mj-1; j3++ {
		i3 := 2*j3 - 1
		for j2 := 1; j2 < mj-1; j2++ {
			i2 := 2*j2 - 1
			for j1 := 1; j1 < mj; j1++ {
				i1 := 2*j1 - 1
				x1[i1] = R(i3+1, i2, i1) + R(i3+1, i2+2, i1) + R(i3, i2+1, i1) + R(i3+2, i2+1, i1)
				y1[i1] = R(i3, i2, i1) + R(i3+2, i2, i1) + R(i3, i2+2, i1) + R(i3+2, i2+2, i1)
			}
			for j1 := 1; j1 < mj-1; j1++ {
				i1 := 2*j1 - 1
				y2 := R(i3, i2, i1+1) + R(i3+2, i2, i1+1) + R(i3, i2+2, i1+1) + R(i3+2, i2+2, i1+1)
				x2 := R(i3+1, i2, i1+1) + R(i3+1, i2+2, i1+1) + R(i3, i2+1, i1+1) + R(i3+2, i2+1, i1+1)
				s[(j3*mj+j2)*mj+j1] = 0.5*R(i3+1, i2+1, i1+1) +
					0.25*(R(i3+1, i2+1, i1)+R(i3+1, i2+1, i1+2)+x2) +
					0.125*(x1[i1]+x1[i1+2]+y2) + 0.0625*(y1[i1]+y1[i1+2])
			}
		}
	}
	comm3(s, mj)
}

// interp adds the prolongation of z (width mm) to u (width n).
func interp(z []float64, mm int, u []float64, n int) {
	Z := func(a, b, c int) float64 { return z[(a*mm+b)*mm+c] }
	U := func(a, b, c int) int { return (a*n+b)*n + c }
	z1, z2, z3 := make([]float64, mm), make([]float64, mm), make([]float64, mm)
	for i3 := 0; i3 < mm-1; i3++ {
		for i2 := 0; i2 < mm-1; i2++ {
			for i1 := 0; i1 < mm; i1++ {
				z1[i1] = Z(i3, i2+1, i1) + Z(i3, i2, i1)
				z2[i1] = Z(i3+1, i2, i1) + Z(i3, i2, i1)
				z3[i1] = Z(i3+1, i2+1, i1) + Z(i3+1, i2, i1) + z1[i1]
			}
			for i1 := 0; i1 < mm-1; i1++ {
				u[U(2*i3, 2*i2, 2*i1)] += Z(i3, i2, i1)
				u[U(2*i3, 2*i2, 2*i1+1)] += 0.5 * (Z(i3, i2, i1+1) + Z(i3, i2, i1))
				u[U(2*i3, 2*i2+1, 2*i1)] += 0.5 * z1[i1]
				u[U(2*i3, 2*i2+1, 2*i1+1)] += 0.25 * (z1[i1] + z1[i1+1])
				u[U(2*i3+1, 2*i2, 2*i1)] += 0.5 * z2[i1]
				u[U(2*i3+1, 2*i2, 2*i1+1)] += 0.25 * (z2[i1] + z2[i1+1])
				u[U(2*i3+1, 2*i2+1, 2*i1)] += 0.25 * z3[i1]
				u[U(2*i3+1, 2*i2+1, 2*i1+1)] += 0.125 * (z3[i1] + z3[i1+1])
			}
		}
	}
}

// Norm returns the L2 and Linf norms of the interior of r (width n+2).
func Norm(r []float64, n int) (float64, float64) {
	p := n + 2
	s, mx := 0.0, 0.0
	for z := 1; z <= n; z++ {
		for y := 1; y <= n; y++ {
			for x := 1; x <= n; x++ {
				v := r[(z*p+y)*p+x]
				s += v * v
				mx = math.Max(mx, math.Abs(v))
			}
		}
	}
	return math.Sqrt(s / float64(n*n*n)), mx
}

// Result is the reference state after the run.
type Result struct {
	U, R, V []float64
	Norms   [4]float64 // initial L2, Linf, final L2, Linf
}

// Reference runs the serial NAS MG recurrence (tier2 mg_reference.h) in
// float64 for the given number of V-cycles.
func Reference(c Class, cycles int) *Result {
	l := NewLayout(c.N)
	res := &Result{U: make([]float64, l.Cells), R: make([]float64, l.Cells), V: Input(c.N)}
	a, sm := OperatorA, Smoother(c)
	lv := func(buf []float64, k int) []float64 {
		w := l.Width[k]
		return buf[l.Offset[k] : l.Offset[k]+w*w*w]
	}
	top := l.Levels() - 1
	n := l.Width[top]
	resid(lv(res.U, top), res.V, lv(res.R, top), n, a)
	res.Norms[0], res.Norms[1] = Norm(lv(res.R, top), c.N)
	for it := 0; it < cycles; it++ {
		for k := top; k > 0; k-- {
			rprj3(lv(res.R, k), l.Width[k], lv(res.R, k-1), l.Width[k-1])
		}
		for i := range lv(res.U, 0) {
			lv(res.U, 0)[i] = 0
		}
		psinv(lv(res.R, 0), lv(res.U, 0), l.Width[0], sm)
		for k := 1; k < top; k++ {
			uk := lv(res.U, k)
			for i := range uk {
				uk[i] = 0
			}
			interp(lv(res.U, k-1), l.Width[k-1], uk, l.Width[k])
			resid(uk, lv(res.R, k), lv(res.R, k), l.Width[k], a)
			psinv(lv(res.R, k), uk, l.Width[k], sm)
		}
		interp(lv(res.U, top-1), l.Width[top-1], lv(res.U, top), n)
		resid(lv(res.U, top), res.V, lv(res.R, top), n, a)
		psinv(lv(res.R, top), lv(res.U, top), n, sm)
		resid(lv(res.U, top), res.V, lv(res.R, top), n, a)
	}
	res.Norms[2], res.Norms[3] = Norm(lv(res.R, top), c.N)
	return res
}

// String describes the class.
func (c Class) String() string { return fmt.Sprintf("%c(%d^3)", c.Name, c.N) }
