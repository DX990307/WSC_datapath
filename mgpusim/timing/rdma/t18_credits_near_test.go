package rdma

import (
	"math/rand"
	"testing"

	"github.com/sarchlab/akita/v3/sim"
)

// ISCA'27 v3 credit policy "near" (t18_credits.go).

func nearTestSelves() [][2]int {
	var out [][2]int
	for y := 0; y < 7; y++ {
		for x := 0; x < 7; x++ {
			if x == 3 && y == 3 {
				continue
			}
			out = append(out, [2]int{x, y})
		}
	}
	return out
}

func TestT18NearCreditsAddUpToW(t *testing.T) {
	for _, self := range nearTestSelves() {
		hops := t18MeshHops(self)
		for _, r := range []int{0, 8, 16, 32} {
			a := newT18CreditAllocatorPolicy(64, T18CreditNear, r, 3, hops)
			sum := 0
			for i, c := range a.credits {
				sum += c
				if hops[i] < 0 && (c != 0 || a.near[i]) {
					t.Fatalf("self %v: requester itself got credit %d near %v",
						self, c, a.near[i])
				}
				if a.near[i] != (r > 0 && hops[i] >= 0 && hops[i] <= 3) {
					t.Fatalf("self %v R=%d: dest %d (%d hops) near=%v",
						self, r, i, hops[i], a.near[i])
				}
			}
			if sum+a.nearReserve != 64 || a.genWindow != 64-r {
				t.Fatalf("self %v R=%d: general credits %d + near %d != 64",
					self, r, sum, a.nearReserve)
			}
			// The general zone is the rtt split of W - R.
			want := t18RTTProportionalCredits(64-r, hops)
			for i := range want {
				if want[i] != a.credits[i] {
					t.Fatalf("self %v R=%d: credits differ from rtt(W-R)", self, r)
				}
			}
		}
	}
}

func TestT18NearFarNeverUsesNearZone(t *testing.T) {
	// W=8, R=3, near <= 3 hops: dest 1 is 1 hop, dest 2 is 10 hops.
	a := newT18CreditAllocatorPolicy(8, T18CreditNear, 3, 3, []int{-1, 1, 10})
	none := func(int) bool { return false }
	sent := 0
	for {
		ok, _ := a.CanSend(2, none)
		if !ok {
			break
		}
		if a.Acquire(2) {
			t.Fatal("far destination took a near-zone slot")
		}
		sent++
	}
	if sent != 5 || a.nearTotal != 0 || a.genTotal != 5 {
		t.Fatalf("far sent %d (general %d near %d), want 5 = W - R", sent,
			a.genTotal, a.nearTotal)
	}
	// The near zone is still free for the near destination only.
	for i := 0; i < 3; i++ {
		ok, borrow := a.CanSend(1, none)
		if !ok || borrow {
			t.Fatalf("near send %d refused (ok=%v borrow=%v)", i, ok, borrow)
		}
		if !a.Acquire(1) {
			t.Fatalf("near send %d did not use the near zone", i)
		}
	}
	if ok, _ := a.CanSend(1, none); ok {
		t.Fatal("window full: near must wait")
	}
	if ok, _ := a.CanSend(2, none); ok {
		t.Fatal("window full: far must wait")
	}
	// A near-zone release does not let the far destination in.
	a.Release(1, true)
	if ok, _ := a.CanSend(2, none); ok {
		t.Fatal("far destination admitted into a freed near-zone slot")
	}
	if ok, _ := a.CanSend(1, none); !ok {
		t.Fatal("near destination must reuse its zone")
	}
	// A general release admits both; the near one goes to the near zone.
	a.Release(2, false)
	if ok, _ := a.CanSend(2, none); !ok {
		t.Fatal("far destination refused a free general slot")
	}
	if !a.Acquire(1) {
		t.Fatal("near destination skipped its free near-zone slot")
	}
	if ok, _ := a.CanSend(2, none); !ok {
		t.Fatal("general slot must still be free for far")
	}
}

// refTxn is one in-flight transaction of the differential tests.
type refTxn struct {
	d    int
	near bool
}

// TestT18NearGeneralZoneIsRTT checks borrowing and reservation in the
// general zone against an rtt allocator of W - R slots fed with the
// general-zone transactions only.
func TestT18NearGeneralZoneIsRTT(t *testing.T) {
	rng := rand.New(rand.NewSource(18))
	for _, self := range nearTestSelves()[:12] {
		hops := t18MeshHops(self)
		const w, r = 64, 16
		a := newT18CreditAllocatorPolicy(w, T18CreditNear, r, 3, hops)
		ref := newT18CreditAllocator(w-r, true, hops)
		var live []refTxn
		waitingSet := map[int]bool{}
		waiting := func(e int) bool { return waitingSet[e] }
		for step := 0; step < 20000; step++ {
			if step%50 == 0 {
				waitingSet = map[int]bool{}
				for i := range hops {
					if hops[i] >= 0 && rng.Intn(3) == 0 {
						waitingSet[i] = true
					}
				}
			}
			if len(live) > 0 && rng.Intn(2) == 0 {
				k := rng.Intn(len(live))
				x := live[k]
				live = append(live[:k], live[k+1:]...)
				a.Release(x.d, x.near)
				if !x.near {
					ref.Release(x.d, false)
				}
				continue
			}
			d := rng.Intn(len(hops))
			if hops[d] < 0 {
				continue
			}
			ok, borrow := a.CanSend(d, waiting)
			nearFree := a.near[d] && a.nearTotal < r
			rok, rborrow := ref.CanSend(d, waiting)
			if nearFree {
				if !ok || borrow {
					t.Fatalf("near zone free but ok=%v borrow=%v", ok, borrow)
				}
			} else if ok != rok || borrow != rborrow {
				t.Fatalf("self %v step %d dest %d (%d hops): near (%v,%v) rtt (%v,%v)",
					self, step, d, hops[d], ok, borrow, rok, rborrow)
			}
			if !ok {
				continue
			}
			z := a.Acquire(d)
			if z != nearFree {
				t.Fatalf("zone %v, expected %v", z, nearFree)
			}
			if !z {
				ref.Acquire(d)
			}
			live = append(live, refTxn{d: d, near: z})
			if a.nearTotal > r || a.total > w || a.genTotal > w-r {
				t.Fatal("zone limits exceeded")
			}
		}
	}
}

// TestT18NearReserveZeroIsRTT: with R = 0 the near policy is rtt, decision
// for decision.
func TestT18NearReserveZeroIsRTT(t *testing.T) {
	rng := rand.New(rand.NewSource(64))
	for _, self := range nearTestSelves() {
		hops := t18MeshHops(self)
		a := newT18CreditAllocatorPolicy(64, T18CreditNear, 0, 3, hops)
		b := newT18CreditAllocator(64, true, hops)
		for i := range a.credits {
			if a.credits[i] != b.credits[i] || a.near[i] {
				t.Fatalf("self %v: R=0 credits differ from rtt", self)
			}
		}
		var live []int
		waitingSet := map[int]bool{}
		waiting := func(e int) bool { return waitingSet[e] }
		for step := 0; step < 5000; step++ {
			if step%40 == 0 {
				waitingSet = map[int]bool{}
				for i := range hops {
					if rng.Intn(4) == 0 {
						waitingSet[i] = true
					}
				}
			}
			if len(live) > 0 && rng.Intn(2) == 0 {
				k := rng.Intn(len(live))
				d := live[k]
				live = append(live[:k], live[k+1:]...)
				a.Release(d, false)
				b.Release(d, false)
				continue
			}
			d := rng.Intn(len(hops))
			if hops[d] < 0 {
				continue
			}
			ok, borrow := a.CanSend(d, waiting)
			ok2, borrow2 := b.CanSend(d, waiting)
			if ok != ok2 || borrow != borrow2 {
				t.Fatalf("self %v step %d: R=0 (%v,%v) rtt (%v,%v)",
					self, step, ok, borrow, ok2, borrow2)
			}
			if ok {
				if a.Acquire(d) || b.Acquire(d) {
					t.Fatal("near zone used with R=0 / rtt")
				}
				live = append(live, d)
			}
		}
	}
}

func TestT18CreditPolicyConfig(t *testing.T) {
	for _, c := range []struct {
		cfg  T18Config
		want T18CreditPolicy
	}{
		{T18Config{}, T18CreditShared},
		{T18Config{DistCredits: true}, T18CreditRTT},
		{T18Config{CreditPolicy: T18CreditRTT}, T18CreditRTT},
		{T18Config{CreditPolicy: T18CreditNear, NearReserve: 16, NearHops: 3}, T18CreditNear},
	} {
		if got := c.cfg.EffectiveCreditPolicy(); got != c.want {
			t.Fatalf("%+v: policy %v, want %v", c.cfg, got, c.want)
		}
		if c.want != T18CreditShared && !c.cfg.RequesterActive() {
			t.Fatalf("%+v: a credit policy must enable the T18 requester", c.cfg)
		}
	}
	for _, s := range []string{"shared", "rtt", "near"} {
		p, err := ParseT18CreditPolicy(s)
		if err != nil || p.String() != s {
			t.Fatalf("parse %q: %v %v", s, p, err)
		}
	}
	if _, err := ParseT18CreditPolicy("dist"); err == nil {
		t.Fatal("bad policy accepted")
	}
}

// TestM1M2ComposedUsesNearAllocator: the M1+M2 composition (m1CanSend /
// m1Acquire / m1Release) goes through the same allocator, near zone included.
func TestM1M2ComposedUsesNearAllocator(t *testing.T) {
	c, _, _, _, nearGPU := newRemoteDataPathTestComp(t,
		RemoteDataPathConfig{
			Enabled:            true,
			DisableDedup:       true,
			DisableRequesterL2: true,
			MaxBatchLines:      8,
			MaxBatches:         8,
		})
	farGPU := &remoteTestPort{name: "FarGPU"}
	c.ConfigureT18(T18Config{ReqMerge: true, Window: 4,
		CreditPolicy: T18CreditNear, NearReserve: 2, NearHops: 3})
	c.SetT18Topology([2]int{0, 0},
		[]sim.Port{c.ToOutside, nearGPU, farGPU},
		[][2]int{{0, 0}, {1, 0}, {6, 6}})
	if !c.m1Composed() {
		t.Fatal("M1 did not compose with M2")
	}
	a := c.t18.credits
	if a.policy != T18CreditNear || a.genWindow != 2 || !a.near[1] || a.near[2] {
		t.Fatalf("allocator %+v", a)
	}
	c.m1Acquire(1e-9, farGPU, "f1")
	c.m1Acquire(1e-9, farGPU, "f2")
	if c.m1CanSend(farGPU) {
		t.Fatal("far destination admitted beyond W - R")
	}
	if !c.m1CanSend(nearGPU) {
		t.Fatal("near destination refused with the near zone free")
	}
	c.m1Acquire(1e-9, nearGPU, "n1")
	c.m1Acquire(1e-9, nearGPU, "n2")
	if a.nearTotal != 2 || a.total != 4 || c.m1CanSend(nearGPU) {
		t.Fatalf("near zone %d total %d", a.nearTotal, a.total)
	}
	c.m1Release(2e-9, "n1")
	if c.m1CanSend(farGPU) || !c.m1CanSend(nearGPU) {
		t.Fatal("a near-zone release must admit only near destinations")
	}
	c.m1Release(2e-9, "f1")
	if !c.m1CanSend(farGPU) {
		t.Fatal("a general release must admit the far destination")
	}
	c.m1Release(3e-9, "n2")
	c.m1Release(3e-9, "f2")
	if a.total != 0 || a.nearTotal != 0 || a.genTotal != 0 {
		t.Fatalf("slots left: %d", a.total)
	}
}
