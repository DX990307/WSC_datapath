package altiscfd

import (
	"fmt"
	"math"
	"math/rand"
	"sort"
)

type point [3]int
type tet [4]point
type face [3]point

func sub(a, b point) point { return point{a[0] - b[0], a[1] - b[1], a[2] - b[2]} }
func dot(a, b point) int   { return a[0]*b[0] + a[1]*b[1] + a[2]*b[2] }
func cross(a, b point) point {
	return point{a[1]*b[2] - a[2]*b[1], a[2]*b[0] - a[0]*b[2], a[0]*b[1] - a[1]*b[0]}
}
func less(a, b point) bool {
	for k := 0; k < 3; k++ {
		if a[k] != b[k] {
			return a[k] < b[k]
		}
	}
	return false
}

func faceOf(t tet, opposite int, shift point) face {
	var f face
	j := 0
	for v := 0; v < 4; v++ {
		if v != opposite {
			for k := 0; k < 3; k++ {
				f[j][k] = t[v][k] + shift[k]
			}
			j++
		}
	}
	s := f[:]
	sort.Slice(s, func(a, b int) bool { return less(s[a], s[b]) })
	return f
}

type neighbor struct {
	offset point
	typ    int
}

// topology: the six tetrahedra of a unit cube (one per axis permutation),
// their face neighbours and inward area vectors (tier2 cfd_reference.h).
type topology struct {
	tetra    [6]tet
	adjacent [6][4]neighbor
	normal   [6][4][3]float32
}

func nextPermutation(p []int) bool {
	i := len(p) - 2
	for i >= 0 && p[i] >= p[i+1] {
		i--
	}
	if i < 0 {
		return false
	}
	j := len(p) - 1
	for p[j] <= p[i] {
		j--
	}
	p[i], p[j] = p[j], p[i]
	for a, b := i+1, len(p)-1; a < b; a, b = a+1, b-1 {
		p[a], p[b] = p[b], p[a]
	}
	return true
}

func newTopology() *topology {
	tp := &topology{}
	perm := []int{0, 1, 2}
	t := 0
	for {
		q := tet{{0, 0, 0}, {0, 0, 0}, {0, 0, 0}, {1, 1, 1}}
		q[1][perm[0]] = 1
		q[2] = q[1]
		q[2][perm[1]] = 1
		tp.tetra[t] = q
		t++
		if !nextPermutation(perm) {
			break
		}
	}
	for t := 0; t < 6; t++ {
		for f := 0; f < 4; f++ {
			key := faceOf(tp.tetra[t], f, point{})
			a := cross(sub(key[1], key[0]), sub(key[2], key[0]))
			sign := -1
			if dot(a, sub(tp.tetra[t][f], key[0])) > 0 {
				sign = 1
			}
			for k := 0; k < 3; k++ {
				tp.normal[t][f][k] = 0.5 * float32(sign*a[k])
			}
			matches := 0
			for z := -1; z <= 1; z++ {
				for y := -1; y <= 1; y++ {
					for x := -1; x <= 1; x++ {
						for other := 0; other < 6; other++ {
							if x == 0 && y == 0 && z == 0 && other == t {
								continue
							}
							for g := 0; g < 4; g++ {
								if faceOf(tp.tetra[other], g, point{x, y, z}) == key {
									tp.adjacent[t][f] = neighbor{point{x, y, z}, other}
									matches++
								}
							}
						}
					}
				}
			}
			if matches != 1 {
				panic("altis_cfd: tetrahedral face must have one neighbour")
			}
		}
	}
	return tp
}

// Farfield returns the far-field conserved variables (upstream host code).
func Farfield() [5]float32 {
	var u [5]float32
	u[0] = 1.4
	sos := float32(math.Sqrt(float64(float32(1.4) * 1.0 / u[0])))
	speed := 1.2 * sos
	u[1] = u[0] * speed
	u[4] = u[0]*(0.5*(speed*speed)) + 1.0/float32(1.4-1.0)
	return u
}

// FFBuffer returns the 17 far-field constants the flux kernel reads
// (variables, then the momentum x/y/z and density-energy flux
// contributions computed with pressure 1 and velocity (1.2, 0, 0), as
// upstream's upload_farfield).
func FFBuffer() []float32 {
	f := Farfield()
	p := float32(1)
	vel := [3]float32{1.2, 0, 0}
	m := [3]float32{f[1], f[2], f[3]}
	out := make([]float32, 17)
	copy(out, f[:])
	// fc_momentum_x/y/z
	for a := 0; a < 3; a++ { // momentum component a
		for d := 0; d < 3; d++ { // direction
			v := vel[d] * m[a]
			if a == d {
				v += p
			}
			out[5+3*a+d] = v
		}
	}
	dep := f[4] + p
	for d := 0; d < 3; d++ {
		out[14+d] = vel[d] * dep
	}
	return out
}

// Mesh is the solver input (padded SoA arrays).
type Mesh struct {
	Cells, Padded, Side int
	Neighbors           []int32   // 4 * Padded
	Normals             []float32 // 12 * Padded
	Areas               []float32 // Padded
	Initial             []float32 // 5 * Padded
}

// NewMesh builds the tier2 synthetic tetrahedral mesh (unit cubes split into
// six tetrahedra, slip wall at y = 0, far field elsewhere, dyadic
// perturbation of the free stream). With shuffle, the cells are relabelled
// by a random permutation (an unstructured mesh with poor numbering).
func NewMesh(cells, block int, shuffle bool, seed int64) *Mesh {
	padded := (cells + block - 1) / block * block
	side := 1
	for 6*side*side*side < cells {
		side++
	}
	n := padded
	m := &Mesh{Cells: cells, Padded: padded, Side: side, Neighbors: make([]int32, 4*n),
		Normals: make([]float32, 12*n), Areas: make([]float32, n), Initial: make([]float32, 5*n)}
	for i := range m.Areas {
		m.Areas[i] = 1.0 / 6.0
	}
	label := make([]int, cells)
	for i := range label {
		label[i] = i
	}
	if shuffle {
		rng := rand.New(rand.NewSource(seed))
		rng.Shuffle(cells, func(a, b int) { label[a], label[b] = label[b], label[a] })
	}
	tp := newTopology()
	for c := 0; c < cells; c++ {
		i := label[c]
		cube, t := c/6, c%6
		x, y, z := cube%side, (cube/side)%side, cube/(side*side)
		for f := 0; f < 4; f++ {
			a := tp.adjacent[t][f]
			xx, yy, zz := x+a.offset[0], y+a.offset[1], z+a.offset[2]
			other := 6*((zz*side+yy)*side+xx) + a.typ
			nb := int32(-2)
			if xx >= 0 && xx < side && yy >= 0 && yy < side && zz >= 0 && zz < side && other >= 0 && other < cells {
				nb = int32(label[other])
			}
			q := faceOf(tp.tetra[t], f, point{})
			if nb < 0 && y == 0 && q[0][1] == 0 && q[1][1] == 0 && q[2][1] == 0 {
				nb = -1
			}
			m.Neighbors[i+f*n] = nb
			for k := 0; k < 3; k++ {
				m.Normals[i+(f+k*4)*n] = tp.normal[t][f][k]
			}
		}
		perturb := float32((11*x+7*y+3*z+t)%33-16) / 256
		density := 1.4 + perturb
		pressure := 1 + 0.5*perturb
		speed := float32(1.2)
		m.Initial[i] = density
		m.Initial[i+n] = density * speed
		m.Initial[i+2*n] = 0
		m.Initial[i+3*n] = 0
		m.Initial[i+4*n] = density*(0.5*(speed*speed)) + pressure/float32(1.4-1.0)
	}
	last := cells - 1
	for i := cells; i < padded; i++ {
		for f := 0; f < 4; f++ {
			m.Neighbors[i+f*n] = m.Neighbors[last+f*n]
		}
		for f := 0; f < 12; f++ {
			m.Normals[i+f*n] = m.Normals[last+f*n]
		}
		for f := 0; f < 5; f++ {
			m.Initial[i+f*n] = m.Initial[last+f*n]
		}
	}
	return m
}

// Check verifies every neighbour index.
func (m *Mesh) Check() error {
	for i, nb := range m.Neighbors {
		if nb < -2 || int(nb) >= m.Cells {
			return fmt.Errorf("altis_cfd: neighbour %d of slot %d out of range", nb, i)
		}
	}
	return nil
}

type physical struct {
	pressure, speed, sound float64
	flux                   [5][3]float64
}

func phys(u [5]float64) physical {
	var p physical
	v := [3]float64{u[1] / u[0], u[2] / u[0], u[3] / u[0]}
	sq := v[0]*v[0] + v[1]*v[1] + v[2]*v[2]
	p.pressure = float64(float32(1.4)-1) * (u[4] - 0.5*u[0]*sq)
	p.speed = math.Sqrt(sq)
	p.sound = math.Sqrt(float64(float32(1.4)) * p.pressure / u[0])
	for d := 0; d < 3; d++ {
		p.flux[0][d] = u[1+d]
		p.flux[4][d] = (u[4] + p.pressure) * v[d]
		for k := 0; k < 3; k++ {
			p.flux[1+k][d] = v[d] * u[1+k]
			if k == d {
				p.flux[1+k][d] += p.pressure
			}
		}
	}
	return p
}

// Reference is the float64 CPU trajectory (tier2 cfd_reference.h): steps
// timesteps of copy, step factor and three RK flux/update stages. It returns
// the final state.
func Reference(m *Mesh, steps int) []float64 {
	n := m.Padded
	state := make([]float64, 5*n)
	for i, v := range m.Initial {
		state[i] = float64(v)
	}
	old := make([]float64, 5*n)
	flux := make([]float64, 5*n)
	factor := make([]float64, n)
	ffb := FFBuffer()
	var ff [5]float64
	for k := 0; k < 5; k++ {
		ff[k] = float64(ffb[k])
	}
	far := phys(ff)
	for d := 0; d < 3; d++ {
		for k := 0; k < 3; k++ {
			far.flux[1+k][d] = float64(ffb[5+3*k+d])
		}
		far.flux[4][d] = float64(ffb[14+d])
	}
	load := func(u []float64, i int) [5]float64 {
		var s [5]float64
		for k := 0; k < 5; k++ {
			s[k] = u[i+k*n]
		}
		return s
	}
	for it := 0; it < steps; it++ {
		copy(old, state)
		for i := 0; i < n; i++ {
			a := phys(load(state, i))
			factor[i] = 0.5 / (math.Sqrt(float64(m.Areas[i])) * (a.speed + a.sound))
		}
		for stage := 0; stage < 3; stage++ {
			for i := 0; i < n; i++ {
				u := load(state, i)
				a := phys(u)
				var sum [5]float64
				for f := 0; f < 4; f++ {
					nb := m.Neighbors[i+f*n]
					var nrm [3]float64
					l2 := 0.0
					for d := 0; d < 3; d++ {
						nrm[d] = float64(m.Normals[i+(f+4*d)*n])
						l2 += nrm[d] * nrm[d]
					}
					if nb == -1 {
						for d := 0; d < 3; d++ {
							sum[1+d] += nrm[d] * a.pressure
						}
						continue
					}
					v, b := ff, far
					if nb >= 0 {
						v = load(state, int(nb))
						b = phys(v)
						visc := -math.Sqrt(l2) * float64(float32(0.2)) * 0.5 * (a.speed + b.speed + a.sound + b.sound)
						for k := 0; k < 5; k++ {
							sum[k] += visc * (u[k] - v[k])
						}
					}
					for d := 0; d < 3; d++ {
						for k := 0; k < 5; k++ {
							sum[k] += 0.5 * nrm[d] * (a.flux[k][d] + b.flux[k][d])
						}
					}
				}
				for k := 0; k < 5; k++ {
					flux[i+k*n] = sum[k]
				}
			}
			for i := 0; i < n; i++ {
				scale := factor[i] / float64(4-stage)
				for k := 0; k < 5; k++ {
					state[i+k*n] = old[i+k*n] + scale*flux[i+k*n]
				}
			}
		}
	}
	return state
}
