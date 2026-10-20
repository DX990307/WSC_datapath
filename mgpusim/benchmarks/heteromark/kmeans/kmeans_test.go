package kmeans

import "testing"

func TestTransposeFeaturesCPU(t *testing.T) {
	got := transposeFeaturesCPU(
		[]float32{0, 1, 2, 3, 4, 5},
		2,
		3,
	)
	want := []float32{0, 3, 1, 4, 2, 5}
	if len(got) != len(want) {
		t.Fatalf("length = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("element %d = %v, want %v", i, got[i], want[i])
		}
	}
}
