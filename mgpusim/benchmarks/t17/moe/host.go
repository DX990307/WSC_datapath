package moe

import (
	"fmt"
	"math"
	"math/rand"
	"sort"
)

// Constants compiled into kernels.cl.
const (
	NumExperts = 64 // NE: routed experts (DeepSeekMoE 16B has 64)
	TileM      = 32 // TM: slots per GEMM tile
	TileN      = 64 // TN: output columns per GEMM tile
	TileK      = 32 // TK: reduction chunk
)

// Params collects every knob of the generator.
type Params struct {
	Tokens int // decode batch
	Hidden int // model dimension (multiple of 64)
	FFN    int // expert intermediate dimension (multiple of 64)
	TopK   int
	// Skew is the target max/mean expert load. A per-expert bias added to
	// the routing score (not to the combine weight) is scaled until the
	// host-side routing reaches it; 0 disables the bias ("uniform").
	Skew float64
	Seed int64
}

// Problem holds the generated inputs (expert weights are generated per
// expert on demand, see ExpertWeights).
type Problem struct {
	Params
	X     []float32 // [T][H]
	Wg    []float32 // [H][E]
	Bias  []float32 // [E]
	BiasZ []float64 // [E] unscaled bias direction
	BiasK float64   // chosen bias scale
}

// Validate checks the shape constraints of the kernels.
func (p Params) Validate() error {
	switch {
	case p.Tokens <= 0 || p.TopK <= 0 || p.TopK > NumExperts:
		return fmt.Errorf("moe: bad tokens %d / top-k %d", p.Tokens, p.TopK)
	case p.Hidden <= 0 || p.Hidden%TileN != 0 || p.Hidden%TileK != 0 || p.Hidden%4 != 0:
		return fmt.Errorf("moe: hidden %d must be a multiple of %d", p.Hidden, TileN)
	case p.FFN <= 0 || p.FFN%TileN != 0 || p.FFN%TileK != 0:
		return fmt.Errorf("moe: ffn %d must be a multiple of %d", p.FFN, TileN)
	case uint64(NumExperts)*uint64(p.Hidden)*uint64(p.FFN) >= 1<<32:
		return fmt.Errorf("moe: expert weights exceed 2^32 elements (32-bit kernel offsets)")
	case uint64(p.Tokens*p.TopK)*uint64(p.Hidden+p.FFN) >= 1<<32:
		return fmt.Errorf("moe: activations exceed 2^32 elements")
	}
	return nil
}

func uniform(rng *rand.Rand, out []float32, a float64) {
	for i := range out {
		out[i] = float32((rng.Float64()*2 - 1) * a)
	}
}

// ExpertWeights returns expert e's W1, W3 ([H][F]) and W2 ([F][H]),
// uniform with variance 1/fan_in, deterministic in (seed, e).
func ExpertWeights(seed int64, e, hidden, ffn int) (w1, w3, w2 []float32) {
	rng := rand.New(rand.NewSource(seed*7919 + int64(e) + 1))
	w1 = make([]float32, hidden*ffn)
	w3 = make([]float32, hidden*ffn)
	w2 = make([]float32, ffn*hidden)
	uniform(rng, w1, math.Sqrt(3/float64(hidden)))
	uniform(rng, w3, math.Sqrt(3/float64(hidden)))
	uniform(rng, w2, math.Sqrt(3/float64(ffn)))
	return
}

// Generate builds x, the gate weights and the calibrated bias.
func Generate(pr Params) *Problem {
	if err := pr.Validate(); err != nil {
		panic(err)
	}
	rng := rand.New(rand.NewSource(pr.Seed))
	p := &Problem{Params: pr}
	p.X = make([]float32, pr.Tokens*pr.Hidden)
	uniform(rng, p.X, 1)
	p.Wg = make([]float32, pr.Hidden*NumExperts)
	uniform(rng, p.Wg, math.Sqrt(3/float64(pr.Hidden)))
	p.BiasZ = make([]float64, NumExperts)
	for e := range p.BiasZ {
		p.BiasZ[e] = rng.NormFloat64()
	}
	probs := p.GateProbs()
	p.BiasK = CalibrateBias(probs, p.BiasZ, pr.TopK, pr.Skew)
	p.Bias = make([]float32, NumExperts)
	for e := range p.Bias {
		p.Bias[e] = float32(p.BiasK * p.BiasZ[e])
	}
	return p
}

// GateProbs returns the softmax gate probabilities [T][E] in float64.
func (p *Problem) GateProbs() [][]float64 {
	out := make([][]float64, p.Tokens)
	for t := range out {
		l := make([]float64, NumExperts)
		m := math.Inf(-1)
		for e := 0; e < NumExperts; e++ {
			s := 0.0
			for h := 0; h < p.Hidden; h++ {
				s += float64(p.X[t*p.Hidden+h]) * float64(p.Wg[h*NumExperts+e])
			}
			l[e] = s
			m = math.Max(m, s)
		}
		sum := 0.0
		for e := range l {
			l[e] = math.Exp(l[e] - m)
			sum += l[e]
		}
		for e := range l {
			l[e] /= sum
		}
		out[t] = l
	}
	return out
}

// TopK returns the k experts with the largest prob+bias (ties: lower id).
func TopK(prob []float64, bias []float64, k int) []int {
	idx := make([]int, len(prob))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool {
		return prob[idx[a]]+bias[idx[a]] > prob[idx[b]]+bias[idx[b]]
	})
	return idx[:k]
}

// Loads returns the number of (token, k) pairs routed to each expert.
func Loads(topk [][]int) []int {
	l := make([]int, NumExperts)
	for _, r := range topk {
		for _, e := range r {
			l[e]++
		}
	}
	return l
}

// MaxOverMean returns max/mean of the expert loads.
func MaxOverMean(loads []int) float64 {
	sum, mx := 0, 0
	for _, l := range loads {
		sum += l
		if l > mx {
			mx = l
		}
	}
	return float64(mx) / (float64(sum) / float64(len(loads)))
}

func route(probs [][]float64, z []float64, k int, scale float64) [][]int {
	bias := make([]float64, len(z))
	for i := range z {
		bias[i] = scale * z[i]
	}
	out := make([][]int, len(probs))
	for t, pr := range probs {
		out[t] = TopK(pr, bias, k)
	}
	return out
}

// CalibrateBias returns the bias scale whose routing gets closest to the
// target max/mean load (bisection over [0, 1]; the probabilities are about
// 1/E, so a scale of 1 dominates them). target <= the unbiased ratio gives 0.
func CalibrateBias(probs [][]float64, z []float64, k int, target float64) float64 {
	if target <= 0 {
		return 0
	}
	ratio := func(s float64) float64 { return MaxOverMean(Loads(route(probs, z, k, s))) }
	if ratio(0) >= target {
		return 0
	}
	lo, hi := 0.0, 1.0
	if ratio(hi) < target {
		return hi
	}
	for i := 0; i < 40; i++ {
		mid := (lo + hi) / 2
		if ratio(mid) < target {
			lo = mid
		} else {
			hi = mid
		}
	}
	return hi
}

// Routing is the host-built permutation (grouped by expert).
type Routing struct {
	Loads     []int
	Offsets   []int   // [E+1] first slot of each expert
	PermToken []int32 // [S] token of each slot
	InvSlot   []int32 // [T*K] slot of (token, k)
	Tiles     []int32 // [n][3] (expert, first slot, rows <= TileM)
}

// NumTiles returns the number of GEMM row tiles.
func (r *Routing) NumTiles() int { return len(r.Tiles) / 3 }

// MaxTiles bounds the tile count for any routing of s slots.
func MaxTiles(s int) int { return s/TileM + NumExperts }

// BuildRouting groups the (token, k) pairs by expert (stable in token
// order), the counting sort a real MoE runtime does on the GPU.
func BuildRouting(topkIdx []int32, tokens, k int) (*Routing, error) {
	r := &Routing{Loads: make([]int, NumExperts), Offsets: make([]int, NumExperts+1)}
	if len(topkIdx) != tokens*k {
		return nil, fmt.Errorf("moe: %d top-k entries for %d x %d", len(topkIdx), tokens, k)
	}
	for i, e := range topkIdx {
		if e < 0 || int(e) >= NumExperts {
			return nil, fmt.Errorf("moe: top-k entry %d = expert %d out of range", i, e)
		}
		r.Loads[e]++
	}
	for e := 0; e < NumExperts; e++ {
		r.Offsets[e+1] = r.Offsets[e] + r.Loads[e]
	}
	next := append([]int(nil), r.Offsets[:NumExperts]...)
	r.PermToken = make([]int32, tokens*k)
	r.InvSlot = make([]int32, tokens*k)
	for t := 0; t < tokens; t++ {
		seen := map[int32]bool{}
		for j := 0; j < k; j++ {
			e := topkIdx[t*k+j]
			if seen[e] {
				return nil, fmt.Errorf("moe: token %d routed twice to expert %d", t, e)
			}
			seen[e] = true
			s := next[e]
			next[e]++
			r.PermToken[s] = int32(t)
			r.InvSlot[t*k+j] = int32(s)
		}
	}
	for e := 0; e < NumExperts; e++ {
		for s := r.Offsets[e]; s < r.Offsets[e+1]; s += TileM {
			rows := r.Offsets[e+1] - s
			if rows > TileM {
				rows = TileM
			}
			r.Tiles = append(r.Tiles, int32(e), int32(s), int32(rows))
		}
	}
	return r, nil
}

// Check verifies every index the kernels dereference.
func (r *Routing) Check(tokens, k int) error {
	s := tokens * k
	for i, t := range r.PermToken {
		if t < 0 || int(t) >= tokens {
			return fmt.Errorf("perm[%d] = token %d out of range", i, t)
		}
	}
	for i, v := range r.InvSlot {
		if v < 0 || int(v) >= s || int(r.PermToken[v]) != i/k {
			return fmt.Errorf("inv[%d] = slot %d inconsistent", i, v)
		}
	}
	covered := 0
	for i := 0; i < r.NumTiles(); i++ {
		e, s0, rows := r.Tiles[3*i], int(r.Tiles[3*i+1]), int(r.Tiles[3*i+2])
		if e < 0 || int(e) >= NumExperts || rows <= 0 || rows > TileM ||
			s0 < r.Offsets[e] || s0+rows > r.Offsets[e+1] {
			return fmt.Errorf("tile %d (%d, %d, %d) out of range", i, e, s0, rows)
		}
		covered += rows
	}
	if covered != s {
		return fmt.Errorf("tiles cover %d of %d slots", covered, s)
	}
	if r.NumTiles() > MaxTiles(s) {
		return fmt.Errorf("%d tiles > bound %d", r.NumTiles(), MaxTiles(s))
	}
	return nil
}

func silu(x float64) float64 { return x / (1 + math.Exp(-x)) }

// Reference computes out [T][H] in float64 for the given routing decision
// (topkIdx/topkW as produced by the gate kernel).
func (p *Problem) Reference(topkIdx []int32, topkW []float32) []float32 {
	out := make([]float64, p.Tokens*p.Hidden)
	H, F := p.Hidden, p.FFN
	byExpert := map[int][]int{} // expert -> list of t*K+j
	for i, e := range topkIdx {
		byExpert[int(e)] = append(byExpert[int(e)], i)
	}
	act := make([]float64, F)
	for e, list := range byExpert {
		w1, w3, w2 := ExpertWeights(p.Seed, e, H, F)
		for _, i := range list {
			t := i / p.TopK
			x := p.X[t*H : (t+1)*H]
			for f := 0; f < F; f++ {
				g, u := 0.0, 0.0
				for h := 0; h < H; h++ {
					g += float64(x[h]) * float64(w1[h*F+f])
					u += float64(x[h]) * float64(w3[h*F+f])
				}
				act[f] = silu(g) * u
			}
			w := float64(topkW[i])
			for h := 0; h < H; h++ {
				y := 0.0
				for f := 0; f < F; f++ {
					y += act[f] * float64(w2[f*H+h])
				}
				out[t*H+h] += w * y
			}
		}
	}
	res := make([]float32, len(out))
	for i, v := range out {
		res[i] = float32(v)
	}
	return res
}

// HostTopK returns the [T*K] expert ids the gate kernel selects (float64
// host gate: softmax probabilities + bias, ties to the lower id).
func (p *Problem) HostTopK() []int32 {
	probs := p.GateProbs()
	bias := make([]float64, NumExperts)
	for e := range bias {
		bias[e] = float64(p.Bias[e])
	}
	out := make([]int32, 0, p.Tokens*p.TopK)
	for t := range probs {
		for _, e := range TopK(probs[t], bias, p.TopK) {
			out = append(out, int32(e))
		}
	}
	return out
}
