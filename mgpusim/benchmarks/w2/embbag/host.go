package embbag

import (
	"fmt"
	"math/rand"
)

// Dim is the embedding dimension (fixed by the kernel).
const Dim = 128

// Params is the input size.
type Params struct {
	Tables  int     // embedding tables (sparse features)
	Rows    int     // rows per table
	Batch   int     // samples
	Pooling int     // lookups per (sample, table)
	Alpha   float64 // Zipf exponent of the row popularity (> 1)
	Seed    int64
}

// Validate checks the parameters.
func (p Params) Validate() error {
	if p.Tables <= 0 || p.Rows <= 0 || p.Batch <= 0 || p.Pooling <= 0 {
		return fmt.Errorf("embbag: sizes must be positive: %+v", p)
	}
	if p.Alpha <= 1 {
		return fmt.Errorf("embbag: alpha must be > 1 (got %g)", p.Alpha)
	}
	if uint64(p.Tables)*uint64(p.Rows) >= 1<<31 {
		return fmt.Errorf("embbag: tables*rows too large")
	}
	return nil
}

// Problem is a generated input.
type Problem struct {
	Params
	Emb []float32 // [T][R][D]
	Idx []int32   // [B*T][P], bag = b*T + t
}

// NumBags is Batch * Tables.
func (p *Problem) NumBags() int { return p.Batch * p.Tables }

// EmbValue is the deterministic initial value of element i of the tables
// (a hash, uniform in [-0.05, 0.05), the scale of DLRM's init).
func EmbValue(seed int64, i uint64) float32 {
	x := i*0x9E3779B97F4A7C15 + uint64(seed)*0xD1B54A32D192ED03
	x ^= x >> 30
	x *= 0xBF58476D1CE4E5B9
	x ^= x >> 27
	x *= 0x94D049BB133111EB
	x ^= x >> 31
	return (float32(x>>40)/float32(1<<24) - 0.5) * 0.1
}

// Generate builds the tables and the power-law lookup indices. Popularity
// rank k (Zipf, exponent Alpha) maps to a row through a per-table random
// permutation, so hot rows are spread over the table (hashed ids).
func Generate(p Params) *Problem {
	pr := &Problem{Params: p}
	n := uint64(p.Tables) * uint64(p.Rows) * Dim
	pr.Emb = make([]float32, n)
	for i := range pr.Emb {
		pr.Emb[i] = EmbValue(p.Seed, uint64(i))
	}
	rng := rand.New(rand.NewSource(p.Seed))
	perms := make([][]int32, p.Tables)
	zipf := make([]*rand.Zipf, p.Tables)
	for t := range perms {
		perm := rng.Perm(p.Rows)
		perms[t] = make([]int32, p.Rows)
		for i, v := range perm {
			perms[t][i] = int32(v)
		}
		zipf[t] = rand.NewZipf(rand.New(rand.NewSource(p.Seed*1000+int64(t))), p.Alpha, 1, uint64(p.Rows-1))
	}
	pr.Idx = make([]int32, pr.NumBags()*p.Pooling)
	for b := 0; b < p.Batch; b++ {
		for t := 0; t < p.Tables; t++ {
			bag := b*p.Tables + t
			for k := 0; k < p.Pooling; k++ {
				pr.Idx[bag*p.Pooling+k] = perms[t][zipf[t].Uint64()]
			}
		}
	}
	return pr
}

// Reference is the CPU embedding bag (fp32, same summation order as the
// kernel).
func (p *Problem) Reference() []float32 {
	out := make([]float32, p.NumBags()*Dim)
	for bag := 0; bag < p.NumBags(); bag++ {
		t := bag % p.Tables
		for k := 0; k < p.Pooling; k++ {
			r := int(p.Idx[bag*p.Pooling+k])
			row := p.Emb[(t*p.Rows+r)*Dim : (t*p.Rows+r+1)*Dim]
			for d := 0; d < Dim; d++ {
				out[bag*Dim+d] += row[d]
			}
		}
	}
	return out
}

// DistinctRows is the number of distinct (table, row) pairs looked up.
func (p *Problem) DistinctRows() int {
	seen := make(map[int64]struct{})
	for bag := 0; bag < p.NumBags(); bag++ {
		t := int64(bag % p.Tables)
		for k := 0; k < p.Pooling; k++ {
			seen[t*int64(p.Rows)+int64(p.Idx[bag*p.Pooling+k])] = struct{}{}
		}
	}
	return len(seen)
}
