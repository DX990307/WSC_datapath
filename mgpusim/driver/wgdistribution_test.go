package driver

import "testing"

func TestBalancedWGDist(t *testing.T) {
	cus := make([]int, 80)
	for i := range cus {
		cus[i] = 28
	}
	d := balancedWGDist(1344, cus)
	if d[80] != 1344 || d[0] != 0 {
		t.Fatalf("ends %d %d", d[0], d[80])
	}
	for i := 0; i < 80; i++ {
		n := d[i+1] - d[i]
		if n < 16 || n > 17 {
			t.Fatalf("GPM %d gets %d WGs, want 16 or 17", i, n)
		}
	}
	// 48 equal GPMs and 1344 WGs: the same 28 per GPM as the ceil split.
	d = balancedWGDist(1344, cus[:48])
	for i := 0; i < 48; i++ {
		if d[i+1]-d[i] != 28 {
			t.Fatalf("7x7: GPM %d gets %d", i, d[i+1]-d[i])
		}
	}
	// Fewer WGs than GPMs: each WG on its own GPM.
	d = balancedWGDist(64, cus)
	used := 0
	for i := 0; i < 80; i++ {
		if n := d[i+1] - d[i]; n > 1 {
			t.Fatalf("GPM %d gets %d of 64 WGs", i, n)
		} else if n == 1 {
			used++
		}
	}
	if used != 64 {
		t.Fatalf("64 WGs on %d GPMs", used)
	}
}
