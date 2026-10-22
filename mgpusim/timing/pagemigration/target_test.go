package pagemigration

import "testing"

// testManager: devices 1..48 on tiles 0..48 without the centre tile 24.
func testManager(t Target) (*Manager, map[int]uint64) {
	cfg := DefaultConfig()
	cfg.Policy = PolicyAccessCounter
	cfg.Target = t
	gpms := make([]*GPM, 49)
	devOf := map[int]uint64{}
	d := uint64(1)
	for tile := 0; tile < 49; tile++ {
		if tile == 24 {
			continue
		}
		gpms[d] = &GPM{DeviceID: d, Tile: tile}
		devOf[tile] = d
		d++
	}
	m := &Manager{cfg: cfg, gpms: gpms, load: make([]float64, 49)}
	return m, devOf
}

func TestMedianAvoidsCentreAndUsesLoad(t *testing.T) {
	m, dev := testManager(TargetMedian)
	c := make([]uint32, 49)
	// four readers symmetric around the centre (tiles 17, 23, 25, 31):
	// the true median is tile 24 (the CPU); the four neighbours tie.
	for _, tile := range []int{17, 23, 25, 31} {
		c[dev[tile]] = 100
	}
	m.load[dev[17]] = 5
	m.load[dev[23]] = 1
	m.load[dev[25]] = 9
	m.load[dev[31]] = 3
	dst, dec, _ := m.chooseTarget(c, dev[0], 0)
	if dec != DecideMigrate || dst != dev[23] {
		t.Fatalf("want least-loaded centre neighbour %d, got %d (%v)", dev[23], dst, dec)
	}
}

func TestMedianWeighted(t *testing.T) {
	m, dev := testManager(TargetMedian)
	c := make([]uint32, 49)
	c[dev[0]] = 10  // (0,0)
	c[dev[6]] = 10  // (6,0)
	c[dev[48]] = 30 // (6,6)
	dst, dec, _ := m.chooseTarget(c, dev[42], 0)
	// x median = 6, y median = 6 (weight 30 of 50 at y = 6) -> tile 48
	if dec != DecideMigrate || dst != dev[48] {
		t.Fatalf("got %d %v", dst, dec)
	}
}

func TestNoMigrateConditions(t *testing.T) {
	m, dev := testManager(TargetMedian)
	c := make([]uint32, 49)
	c[dev[10]] = 50
	if _, dec, _ := m.chooseTarget(c, dev[10], 0); dec != DecideSamePlace {
		t.Fatalf("target = owner must not migrate, got %v", dec)
	}
	// readers on both sides of the owner: the median saves < 10 %.
	c = make([]uint32, 49)
	c[dev[21]] = 50 // (0,3)
	c[dev[27]] = 50 // (6,3)
	c[dev[22]] = 1  // (1,3)
	// owner at (3,2) = tile 17: cost 50*4+50*4+1*3 = 403; best tile on row 3
	// e.g. (1,3): 50*1+50*5 = 300 -> saving 25 %, migrate
	if _, dec, _ := m.chooseTarget(c, dev[17], 0); dec != DecideMigrate {
		t.Fatalf("want migrate, got %v", dec)
	}
	m.cfg.MinSaving = 0.3
	if _, dec, _ := m.chooseTarget(c, dev[17], 0); dec != DecideSmallSaving {
		t.Fatalf("want small saving, got %v", dec)
	}
}

func TestClassRules(t *testing.T) {
	m, dev := testManager(TargetClass)
	c := make([]uint32, 49)
	c[dev[0]] = 70
	c[dev[48]] = 30
	dst, dec, kind := m.chooseTarget(c, dev[10], 0)
	if dec != DecideMigrate || dst != dev[0] || kind != "dominant" {
		t.Fatalf("dominant: got %d %v %s", dst, dec, kind)
	}
	// every GPM reads: broadcast, spread over k = 4 tiles page by page
	c = make([]uint32, 49)
	for _, d := range dev {
		c[d] = 10
	}
	seen := map[uint64]bool{}
	for page := uint64(0); page < 8; page++ {
		dst, dec, kind := m.chooseTarget(c, dev[0], page)
		if dec != DecideMigrate || kind != "broadcast" {
			t.Fatalf("broadcast: %v %s", dec, kind)
		}
		seen[dst] = true
	}
	if len(seen) != 4 || !seen[dev[17]] || !seen[dev[23]] || !seen[dev[25]] || !seen[dev[31]] {
		t.Fatalf("broadcast spread over %v", seen)
	}
	m.cfg.BroadcastK = 0
	if _, dec, _ := m.chooseTarget(c, dev[0], 0); dec != DecideBroadcastStay {
		t.Fatalf("k = 0 must stay, got %v", dec)
	}
	// few readers, no dominant one -> median
	c = make([]uint32, 49)
	c[dev[0]] = 40
	c[dev[2]] = 30
	c[dev[14]] = 30
	if _, _, kind := m.chooseTarget(c, dev[48], 0); kind != "median" {
		t.Fatalf("want median, got %s", kind)
	}
}

func TestLoadDecay(t *testing.T) {
	m, dev := testManager(TargetMedian)
	m.cfg.LoadWindowNS = 1000
	m.noteServed(0, dev[3])
	m.noteServed(0, dev[3])
	m.noteServed(2.5e-6, dev[4])
	if m.load[dev[3]] != 0.5 || m.load[dev[4]] != 1 {
		t.Fatalf("decay: %v %v", m.load[dev[3]], m.load[dev[4]])
	}
}
