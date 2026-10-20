package npbis

import "fmt"

// Kernel geometry (kernels.cl).
const (
	WGSize    = 256
	KeysPerTh = 8
	Tile      = WGSize * KeysPerTh // keys per is_count / is_scatter WG
	BPW       = 16                 // buckets per is_colscan WG
	MaxNB     = 1024
	MaxW      = 2048
	MaxIter   = 10 // upstream MAX_ITERATIONS
)

// Params is the problem: N keys in [0, MaxKey).
type Params struct {
	N          int
	Iterations int // rank() calls (upstream 10)
}

// Class returns NPB's MAX_KEY for the class sizes and N/16 otherwise.
func (p Params) MaxKey() int {
	switch p.N {
	case 1 << 16:
		return 1 << 11
	case 1 << 20:
		return 1 << 16
	case 1 << 23:
		return 1 << 19
	case 1 << 25:
		return 1 << 21
	}
	return p.N / 16
}

// Buckets returns NB (buckets) and log2 W (key values per bucket).
func (p Params) Buckets() (nb, logw int) {
	mk := p.MaxKey()
	nb = mk / 256
	if nb > MaxNB {
		nb = MaxNB
	}
	w := mk / nb
	for 1<<logw < w {
		logw++
	}
	return nb, logw
}

// Validate checks the size rules.
func (p Params) Validate() error {
	mk := p.MaxKey()
	if p.N < Tile || p.N%Tile != 0 {
		return fmt.Errorf("npbis: N = %d must be a multiple of %d", p.N, Tile)
	}
	if mk&(mk-1) != 0 || mk < 256 {
		return fmt.Errorf("npbis: MAX_KEY = %d must be a power of two >= 256", mk)
	}
	if nb, logw := p.Buckets(); 1<<logw > MaxW || nb*(1<<logw) != mk {
		return fmt.Errorf("npbis: MAX_KEY = %d too large (bucket width %d > %d)", mk, 1<<logw, MaxW)
	}
	if p.Iterations < 1 || p.Iterations > MaxIter {
		return fmt.Errorf("npbis: iterations %d not in 1..%d", p.Iterations, MaxIter)
	}
	return nil
}

// randlc is NPB's 46-bit linear congruential generator.
func randlc(x *float64, a float64) float64 {
	const r23 = 1.0 / 8388608.0
	const r46 = r23 * r23
	const t23 = 8388608.0
	const t46 = t23 * t23
	t1 := r23 * a
	a1 := float64(int(t1))
	a2 := a - t23*a1
	t1 = r23 * *x
	x1 := float64(int(t1))
	x2 := *x - t23*x1
	t1 = a1*x2 + a2*x1
	t2 := float64(int(r23 * t1))
	z := t1 - t23*t2
	t3 := t23*z + a2*x2
	t4 := float64(int(r46 * t3))
	*x = t3 - t46*t4
	return r46 * *x
}

// CreateSeq is upstream create_seq (seed 314159265, a = 1220703125): each
// key is MAX_KEY/4 times the sum of four uniform draws.
func CreateSeq(n, maxKey int) []int32 {
	seed, a := 314159265.0, 1220703125.0
	k := float64(maxKey / 4)
	keys := make([]int32, n)
	for i := range keys {
		x := randlc(&seed, a)
		x += randlc(&seed, a)
		x += randlc(&seed, a)
		x += randlc(&seed, a)
		keys[i] = int32(k * x)
	}
	return keys
}

// Modify is the per-iteration key perturbation of rank().
func Modify(keys []int32, it, maxKey int) {
	keys[it] = int32(it)
	keys[it+MaxIter] = int32(maxKey - it)
}

// RefRank is upstream's host mirror: key_buff1[k] = #keys <= k.
func RefRank(keys []int32, maxKey int) []int32 {
	r := make([]int32, maxKey)
	for _, k := range keys {
		r[k]++
	}
	for i := 0; i < maxKey-1; i++ {
		r[i+1] += r[i]
	}
	return r
}
