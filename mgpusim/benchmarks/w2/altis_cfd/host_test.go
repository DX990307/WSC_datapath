package altiscfd

import (
	"math"
	"testing"
)

func TestMesh(t *testing.T) {
	for _, shuffle := range []bool{false, true} {
		m := NewMesh(1000, 256, shuffle, 3)
		if err := m.Check(); err != nil {
			t.Fatal(err)
		}
		n := m.Padded
		walls := 0
		for i := 0; i < m.Cells; i++ {
			var sum [3]float64
			for f := 0; f < 4; f++ {
				for k := 0; k < 3; k++ {
					sum[k] += float64(m.Normals[i+(f+4*k)*n])
				}
				nb := m.Neighbors[i+f*n]
				if nb == -1 {
					walls++
				}
				if nb < 0 {
					continue
				}
				// reciprocal neighbour with the opposite normal
				ok := false
				for g := 0; g < 4; g++ {
					if m.Neighbors[int(nb)+g*n] == int32(i) {
						ok = true
						for k := 0; k < 3; k++ {
							if m.Normals[int(nb)+(g+4*k)*n] != -m.Normals[i+(f+4*k)*n] {
								t.Fatalf("cell %d face %d: normals not opposite", i, f)
							}
						}
					}
				}
				if !ok {
					t.Fatalf("cell %d face %d: neighbour %d not reciprocal", i, f, nb)
				}
			}
			for k := 0; k < 3; k++ {
				if math.Abs(sum[k]) > 1e-6 {
					t.Fatalf("cell %d: normals sum to %v", i, sum)
				}
			}
		}
		if walls == 0 {
			t.Fatal("no slip-wall faces")
		}
	}
	// the reference moves the state and stays finite
	m := NewMesh(600, 64, false, 1)
	s := Reference(m, 2)
	moved := false
	for i, v := range s {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			t.Fatalf("state[%d] = %g", i, v)
		}
		if math.Abs(v-float64(m.Initial[i])) > 1e-6 {
			moved = true
		}
	}
	if !moved {
		t.Fatal("reference did not change the state")
	}
}

func TestSimulate32MatchesReference(t *testing.T) {
	m := NewMesh(3000, 256, true, 2)
	st, _, _, _ := Simulate32(m, 2)
	ref := Reference(m, 2)
	for i := range ref {
		if e := math.Abs(float64(st[i])-ref[i]) / (1 + math.Abs(ref[i])); e > 1e-5 {
			t.Fatalf("state[%d] %g vs %g", i, st[i], ref[i])
		}
	}
}
