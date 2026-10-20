package platbench

import "testing"

func TestMeshHelpersAllSizes(t *testing.T) {
	defer func(w int) { MeshWidth = w }(MeshWidth)
	for _, w := range []int{3, 5, 7} {
		MeshWidth = w
		n := w*w - 1
		seen := map[int]bool{}
		for g := 1; g <= n; g++ {
			x, y := meshXY(g)
			if x == w/2 && y == w/2 {
				t.Fatalf("w=%d: GPU %d on the center tile", w, g)
			}
			if gpuAt(x, y) != g {
				t.Fatalf("w=%d: gpuAt(meshXY(%d)) = %d", w, g, gpuAt(x, y))
			}
			seen[TileOf(g)] = true
		}
		if len(seen) != n || gpuAt(w/2, w/2) != 0 {
			t.Fatalf("w=%d: tiles not a bijection", w)
		}
		if len(snakeOrder()) != n {
			t.Fatalf("w=%d: snake has %d GPMs", w, len(snakeOrder()))
		}
	}
	MeshWidth = 7
	if TileOf(24) != 23 || TileOf(25) != 25 || Hops(1, 48) != 12 {
		t.Fatal("7x7 mapping changed")
	}
}
