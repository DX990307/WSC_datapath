package pagedattn

import (
	"math"
	"math/rand"
	"sort"
	"testing"
)

func TestSampleSeqLensMeanAndClip(t *testing.T) {
	d := LenDist{Mean: 1024, Sigma: 0.8, Min: 16, Max: 1 << 20}
	rng := rand.New(rand.NewSource(7))
	lens := SampleSeqLens(rng, 200000, d)
	sum := 0.0
	logs := 0.0
	for _, l := range lens {
		sum += float64(l)
		logs += math.Log(float64(l))
	}
	mean := sum / float64(len(lens))
	if math.Abs(mean-1024)/1024 > 0.02 {
		t.Fatalf("mean %f, want about 1024", mean)
	}
	if mu := logs / float64(len(lens)); math.Abs(mu-d.Mu()) > 0.01 {
		t.Fatalf("mean of ln %f, want %f", mu, d.Mu())
	}

	clip := LenDist{Mean: 1024, Sigma: 2, Min: 100, Max: 2000}
	for _, l := range SampleSeqLens(rng, 10000, clip) {
		if l < 100 || l > 2000 {
			t.Fatalf("length %d outside [100, 2000]", l)
		}
	}
}

func TestBlockPoolFIFOAndReverseFree(t *testing.T) {
	p := NewBlockPool(6)
	a := []int{p.Alloc(), p.Alloc(), p.Alloc()}
	if a[0] != 0 || a[1] != 1 || a[2] != 2 {
		t.Fatalf("alloc order %v", a)
	}
	p.Free(a)
	var got []int
	for p.NumFree() > 0 {
		got = append(got, p.Alloc())
	}
	want := []int{3, 4, 5, 2, 1, 0}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("free-queue order %v, want %v", got, want)
		}
	}
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on exhausted pool")
		}
	}()
	p.Alloc()
}

func TestBuildBlockTablesValidAndScattered(t *testing.T) {
	d := LenDist{Mean: 1024, Sigma: 0.8, Min: 16, Max: 8192}
	rng := rand.New(rand.NewSource(3))
	lens := SampleSeqLens(rng, 32, d)
	need := 0
	for _, l := range lens {
		need += blocksFor(l)
	}
	pool := NewBlockPool(2 * need)
	tables := BuildBlockTables(rng, pool, lens, d, ChurnConfig{Rounds: 8, Occupancy: 0.9, FinishP: 0.5})
	seen := map[int]bool{}
	contiguous, pairs := 0, 0
	for s, tb := range tables {
		if len(tb) != blocksFor(lens[s]) {
			t.Fatalf("seq %d: %d blocks for %d tokens", s, len(tb), lens[s])
		}
		for j, b := range tb {
			if b < 0 || b >= pool.NumBlocks() || seen[b] {
				t.Fatalf("seq %d: bad or duplicate block %d", s, b)
			}
			seen[b] = true
			if j > 0 {
				pairs++
				if b == tb[j-1]+1 {
					contiguous++
				}
			}
		}
	}
	if pool.NumFree() != pool.NumBlocks()-need {
		t.Fatalf("free %d, want %d", pool.NumFree(), pool.NumBlocks()-need)
	}
	// After the churn the blocks of a sequence must be scattered: few
	// logically adjacent blocks are physically adjacent, and the blocks of
	// one sequence span most of the pool.
	if frac := float64(contiguous) / float64(pairs); frac > 0.5 {
		t.Fatalf("%.2f of adjacent blocks are contiguous; not scattered", frac)
	}
	var all []int
	for b := range seen {
		all = append(all, b)
	}
	sort.Ints(all)
	if span := all[len(all)-1] - all[0]; span < pool.NumBlocks()/2 {
		t.Fatalf("blocks span %d of %d", span, pool.NumBlocks())
	}
}

func TestWorkList(t *testing.T) {
	work, maxParts := WorkList([]int{1, 64, 65, 200}, 64)
	want := []int32{0, 0, 1, 0, 2, 0, 2, 1, 3, 0, 3, 1, 3, 2, 3, 3}
	if maxParts != 4 || len(work) != len(want) {
		t.Fatalf("work %v maxParts %d", work, maxParts)
	}
	for i := range want {
		if work[i] != want[i] {
			t.Fatalf("work %v, want %v", work, want)
		}
	}
}

func smallParams() Params {
	pr := DefaultParams()
	pr.Batch = 3
	pr.KVHeads = 2
	pr.PartTokens = 32
	pr.Dist = LenDist{Mean: 100, Sigma: 0.8, Min: 16, Max: 400}
	return pr
}

func TestGenerateBoundsAndFootprint(t *testing.T) {
	p := Generate(smallParams(), true)
	if err := p.CheckBounds(); err != nil {
		t.Fatal(err)
	}
	p.BlockTable[0] = int32(p.NumBlocks)
	if err := p.CheckBounds(); err == nil {
		t.Fatal("out-of-pool block not detected")
	}

	pr := DefaultParams()
	pr.Batch = 32
	pr.Dist.Mean = 1024
	big := Generate(pr, false)
	fp := big.Footprint()
	tokens := 0
	for _, l := range big.Lens {
		tokens += l
	}
	if fp.KV != uint64(tokens)*8*128*4*2 {
		t.Fatalf("KV footprint %d for %d tokens", fp.KV, tokens)
	}
}

// partitioned recomputes the output the way the two kernels do (log2
// domain, per-partition max/sum, then merge) to check the decomposition
// against the textbook reference.
func partitioned(p *Problem) []float32 {
	nq := p.QHeads()
	bf := p.BlockFloats()
	out := make([]float32, p.Batch*nq*HeadDim)
	sl := math.Log2E / math.Sqrt(HeadDim)
	for s := 0; s < p.Batch; s++ {
		np := (p.Lens[s] + p.PartTokens - 1) / p.PartTokens
		for qh := 0; qh < nq; qh++ {
			kvh := qh / GQA
			q := p.Q[(s*nq+qh)*HeadDim:]
			ms := make([]float64, np)
			ls := make([]float64, np)
			os := make([][]float64, np)
			for part := 0; part < np; part++ {
				t0 := part * p.PartTokens
				t1 := t0 + p.PartTokens
				if t1 > p.Lens[s] {
					t1 = p.Lens[s]
				}
				sc := make([]float64, t1-t0)
				m := math.Inf(-1)
				for t := t0; t < t1; t++ {
					k := p.K[p.Tables[s][t/BlockSize]*bf+(kvh*BlockSize+t%BlockSize)*HeadDim:]
					dot := 0.0
					for d := 0; d < HeadDim; d++ {
						dot += float64(q[d]) * float64(k[d])
					}
					sc[t-t0] = dot * sl
					m = math.Max(m, sc[t-t0])
				}
				os[part] = make([]float64, HeadDim)
				for t := t0; t < t1; t++ {
					w := math.Exp2(sc[t-t0] - m)
					ls[part] += w
					v := p.V[p.Tables[s][t/BlockSize]*bf+(kvh*BlockSize+t%BlockSize)*HeadDim:]
					for d := 0; d < HeadDim; d++ {
						os[part][d] += w * float64(v[d])
					}
				}
				ms[part] = m
			}
			gm := math.Inf(-1)
			for _, m := range ms {
				gm = math.Max(gm, m)
			}
			gl := 0.0
			acc := make([]float64, HeadDim)
			for part := range ms {
				w := math.Exp2(ms[part] - gm)
				gl += w * ls[part]
				for d := range acc {
					acc[d] += w * os[part][d]
				}
			}
			for d := range acc {
				out[(s*nq+qh)*HeadDim+d] = float32(acc[d] / gl)
			}
		}
	}
	return out
}

func TestPartitionedMatchesReference(t *testing.T) {
	p := Generate(smallParams(), true)
	ref := p.Reference()
	got := partitioned(p)
	for i := range ref {
		if math.Abs(float64(ref[i]-got[i])) > 1e-5 {
			t.Fatalf("element %d: partitioned %g reference %g", i, got[i], ref[i])
		}
	}
}

func TestWGAndPageMapping(t *testing.T) {
	// 4200 WGs on 48 GPMs x 28 CUs: 4 WGs per CU, 112 per GPM, 38 GPMs busy.
	if g := wgGPM(111, 4200, 48, 28); g != 0 {
		t.Fatalf("wg 111 on GPM %d", g)
	}
	if g := wgGPM(112, 4200, 48, 28); g != 1 {
		t.Fatalf("wg 112 on GPM %d", g)
	}
	if a := activeGPMs(4200, 48, 28); a != 38 {
		t.Fatalf("active GPMs %d", a)
	}
	// 96 pages over 48 GPMs: 2 pages each.
	if g := pageGPM(2*4096, 96*4096, 48); g != 1 {
		t.Fatalf("page 2 on GPM %d", g)
	}
	if g := pageGPM(0, 10*4096, 48); g != 0 {
		t.Fatalf("small buffer on GPM %d", g)
	}
}
