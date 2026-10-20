package moe

import (
	"math"
	"testing"
)

func TestValidate(t *testing.T) {
	p := DefaultParams()
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	full := Params{Tokens: 256, Hidden: 1024, FFN: 704, TopK: 6, Skew: 1.75, Seed: 1}
	if err := full.Validate(); err != nil {
		t.Fatal(err)
	}
	p.FFN = 100
	if p.Validate() == nil {
		t.Fatal("FFN 100 accepted")
	}
}

func TestTopKTieBreakAndOrder(t *testing.T) {
	prob := make([]float64, NumExperts)
	bias := make([]float64, NumExperts)
	prob[5], prob[3], prob[9] = 0.3, 0.3, 0.2
	bias[9] = 0.2
	got := TopK(prob, bias, 3)
	// 9 scores 0.2 + 0.2 = 0.4 and goes first; 3 and 5 tie at 0.3 (lower
	// id first).
	want := []int{9, 3, 5}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("top-k %v, want %v", got, want)
		}
	}
}

func TestCalibrateBiasReachesSkew(t *testing.T) {
	pr := Params{Tokens: 256, Hidden: 128, FFN: 64, TopK: 6, Skew: 0, Seed: 3}
	p := Generate(pr)
	probs := p.GateProbs()
	base := MaxOverMean(Loads(route(probs, p.BiasZ, pr.TopK, 0)))
	for _, target := range []float64{1.75, 2.0} {
		if target <= base {
			continue
		}
		k := CalibrateBias(probs, p.BiasZ, pr.TopK, target)
		got := MaxOverMean(Loads(route(probs, p.BiasZ, pr.TopK, k)))
		if math.Abs(got-target) > 0.15 {
			t.Fatalf("target %g: got max/mean %g (scale %g, unbiased %g)", target, got, k, base)
		}
	}
	if k := CalibrateBias(probs, p.BiasZ, pr.TopK, 0); k != 0 {
		t.Fatalf("skew 0 gave scale %g", k)
	}
}

func TestBuildRouting(t *testing.T) {
	// 3 tokens, top-2
	idx := []int32{4, 1, 1, 4, 4, 7}
	r, err := BuildRouting(idx, 3, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Check(3, 2); err != nil {
		t.Fatal(err)
	}
	if r.Loads[1] != 2 || r.Loads[4] != 3 || r.Loads[7] != 1 {
		t.Fatalf("loads %v", r.Loads)
	}
	// expert 1: slots 0,1 = tokens 0,1; expert 4: slots 2,3,4 = tokens 0,1,2
	wantPerm := []int32{0, 1, 0, 1, 2, 2}
	for i, v := range wantPerm {
		if r.PermToken[i] != v {
			t.Fatalf("perm %v want %v", r.PermToken, wantPerm)
		}
	}
	if r.NumTiles() != 3 {
		t.Fatalf("%d tiles", r.NumTiles())
	}
	if _, err := BuildRouting([]int32{2, 2}, 1, 2); err == nil {
		t.Fatal("duplicate expert accepted")
	}

	// large expert splits into TileM-row tiles
	big := make([]int32, 100) // all to expert 0
	rb, err := BuildRouting(big, 100, 1)
	if err != nil {
		t.Fatal(err)
	}
	if rb.NumTiles() != 4 || rb.Tiles[3*3+2] != 100-3*TileM {
		t.Fatalf("tiles %v", rb.Tiles)
	}
	if err := rb.Check(100, 1); err != nil {
		t.Fatal(err)
	}
}

func TestReferenceSingleExpertMatchesManual(t *testing.T) {
	pr := Params{Tokens: 2, Hidden: 64, FFN: 64, TopK: 1, Seed: 5}
	p := Generate(pr)
	idx := []int32{3, 3}
	w := []float32{0.5, 1}
	out := p.Reference(idx, w)
	w1, w3, w2 := ExpertWeights(p.Seed, 3, 64, 64)
	tok := 1
	act := make([]float64, 64)
	for f := 0; f < 64; f++ {
		g, u := 0.0, 0.0
		for h := 0; h < 64; h++ {
			x := float64(p.X[tok*64+h])
			g += x * float64(w1[h*64+f])
			u += x * float64(w3[h*64+f])
		}
		act[f] = g / (1 + math.Exp(-g)) * u
	}
	y := 0.0
	for f := 0; f < 64; f++ {
		y += act[f] * float64(w2[f*64+7])
	}
	if math.Abs(float64(out[tok*64+7])-y) > 1e-6 {
		t.Fatalf("reference %g manual %g", out[tok*64+7], y)
	}
}
