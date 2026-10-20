package rdma

import (
	"testing"

	"github.com/sarchlab/akita/v3/sim"
)

func t18MeshHops(self [2]int) []int {
	var hops []int
	for y := 0; y < 7; y++ {
		for x := 0; x < 7; x++ {
			if x == 3 && y == 3 {
				continue
			}
			c := [2]int{x, y}
			if c == self {
				hops = append(hops, -1)
				continue
			}
			hops = append(hops, t18Hops(self, c))
		}
	}
	return hops
}

func TestT18CreditsProportionalToRTT(t *testing.T) {
	hops := t18MeshHops([2]int{0, 0})
	for _, w := range []int{64, 256, 1024, 4096} {
		credits := t18RTTProportionalCredits(w, hops)
		sum := 0
		for i, c := range credits {
			sum += c
			if hops[i] < 0 && c != 0 {
				t.Fatalf("self got %d credits", c)
			}
		}
		if sum != w {
			t.Fatalf("W=%d: credits sum to %d", w, sum)
		}
		// Monotone in hops (within rounding: at most 1 apart in the wrong order).
		for i := range hops {
			for j := range hops {
				if hops[i] >= 0 && hops[j] >= 0 && hops[i] < hops[j] &&
					credits[i] > credits[j]+1 {
					t.Fatalf("W=%d: %d hops got %d > %d hops %d",
						w, hops[i], credits[i], hops[j], credits[j])
				}
			}
		}
	}
	// W=4096, 1 hop vs 12 hops: ratio ~ RTT ratio (941/155).
	credits := t18RTTProportionalCredits(4096, hops)
	var c1, c12 int
	for i, h := range hops {
		if h == 1 {
			c1 = credits[i]
		}
		if h == 12 {
			c12 = credits[i]
		}
	}
	ratio := float64(c12) / float64(c1)
	want := t18RTTNS(12) / t18RTTNS(1)
	if ratio < want*0.95 || ratio > want*1.05 {
		t.Fatalf("credit ratio %.2f, want about %.2f", ratio, want)
	}
}

func TestT18CreditsSharedPool(t *testing.T) {
	a := newT18CreditAllocator(4, false, []int{-1, 1, 6})
	none := func(int) bool { return false }
	for i := 0; i < 4; i++ {
		ok, borrow := a.CanSend(1, none)
		if !ok || borrow {
			t.Fatalf("send %d refused in a shared pool", i)
		}
		a.Acquire(1)
	}
	if ok, _ := a.CanSend(2, none); ok {
		t.Fatal("full pool must refuse")
	}
	a.Release(1)
	if ok, _ := a.CanSend(2, none); !ok {
		t.Fatal("released slot must be usable")
	}
}

func TestT18CreditsBorrowing(t *testing.T) {
	// W=10 over two destinations with RTT 155 and 512 ns (1 and 6 hops).
	a := newT18CreditAllocator(10, true, []int{-1, 1, 6})
	if a.credits[1]+a.credits[2] != 10 || a.credits[1] >= a.credits[2] {
		t.Fatalf("credits %v", a.credits)
	}
	own1 := a.credits[1]
	none := func(int) bool { return false }
	// Destination 1 uses its own credits, then borrows idle ones.
	for i := 0; i < 10; i++ {
		ok, borrow := a.CanSend(1, none)
		if !ok {
			t.Fatalf("send %d refused although destination 2 is idle", i)
		}
		if borrow != (i >= own1) {
			t.Fatalf("send %d: borrow=%v, own credits %d", i, borrow, own1)
		}
		a.Acquire(1)
	}
	if ok, _ := a.CanSend(2, none); ok {
		t.Fatal("window full")
	}
	// Destination 2 now waits: borrowing must leave its credits free.
	for i := 0; i < 10; i++ {
		a.Release(1)
	}
	waiting2 := func(e int) bool { return e == 2 }
	for i := 0; i < own1; i++ {
		a.Acquire(1)
	}
	if ok, _ := a.CanSend(1, waiting2); ok {
		t.Fatal("destination 1 must not borrow destination 2's reserved credits")
	}
	for i := 0; i < a.credits[2]; i++ {
		ok, borrow := a.CanSend(2, waiting2)
		if !ok || borrow {
			t.Fatalf("destination 2 own send %d refused (ok=%v borrow=%v)", i, ok, borrow)
		}
		a.Acquire(2)
	}
	if ok, _ := a.CanSend(1, waiting2); ok {
		t.Fatal("window is full")
	}
}

func TestT18MulticastSplit(t *testing.T) {
	ports := []sim.Port{
		sim.NewLimitNumMsgPort(nil, 1, "A"),
		sim.NewLimitNumMsgPort(nil, 1, "B"),
		sim.NewLimitNumMsgPort(nil, 1, "C"),
	}
	owner := sim.NewLimitNumMsgPort(nil, 1, "O")
	data := make([]byte, 64)
	data[3] = 42
	m := &T18MulticastRsp{
		MsgMeta: sim.MsgMeta{ID: "m", Src: owner, Dst: ports[0], TrafficBytes: 74},
		Data:    data,
		Dsts:    ports,
		RspTos:  []string{"ra", "rb", "rc"},
	}
	child := m.SplitForDsts([]sim.Port{ports[1], ports[2]}).(*T18MulticastRsp)
	if child.ID == m.ID || child.Dst != ports[1] || len(child.Dsts) != 2 ||
		child.RspTos[0] != "rb" || child.RspTos[1] != "rc" {
		t.Fatalf("bad child %+v", child)
	}
	leaf := child.SplitForDsts([]sim.Port{ports[2]})
	rsp, ok := leaf.(interface{ GetRspTo() string })
	if !ok || rsp.GetRspTo() != "rc" || leaf.Meta().Dst != ports[2] ||
		leaf.Meta().Src != owner {
		t.Fatalf("bad leaf %+v", leaf)
	}
}
