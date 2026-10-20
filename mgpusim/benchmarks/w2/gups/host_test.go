package gups

import "testing"

// Applying each thread's slice of the stream from its host start state in
// any order gives the serial reference (XOR commutes).
func TestStartsReproduceStream(t *testing.T) {
	p := Params{LogTable: 10, WGs: 3, UpdateMult: 4}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	q, r := p.Partition()
	tab := p.InitialTable()
	starts := p.Starts()
	mask := uint64(p.TableSize() - 1)
	for th := p.Threads() - 1; th >= 0; th-- {
		s := starts[th]
		n := q
		if uint32(th) < r {
			n++
		}
		for i := uint32(0); i < n; i++ {
			s = Step(s)
			tab[s&mask] ^= s
		}
	}
	ref := p.Reference()
	for i := range ref {
		if tab[i] != ref[i] {
			t.Fatalf("entry %d differs", i)
		}
	}
	if Step(1<<63) != 7 || Step(3) != 6 {
		t.Fatal("step")
	}
}
