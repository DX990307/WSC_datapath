package allreduce

import "testing"

func TestParams(t *testing.T) {
	p := DefaultParams()
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	if p.G() != 344064 || p.G()/256 != 48*28 {
		t.Fatalf("G %d", p.G())
	}
	// a shard is whole 4 KB pages
	if (p.G()/p.Ranks*4)%4096 != 0 || (p.G()*4)%4096 != 0 {
		t.Fatal("shard or copy not page aligned")
	}
	small := Params{Ranks: 3, CUs: 1, PerCU: 1}
	red := small.Reference()
	for _, e := range []int{0, 5, 700} {
		want := Value(0, e) + Value(1, e) + Value(2, e)
		if red[e] != want {
			t.Fatalf("red[%d] %g want %g", e, red[e], want)
		}
	}
}
