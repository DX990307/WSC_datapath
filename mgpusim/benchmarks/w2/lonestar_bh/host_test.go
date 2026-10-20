package lonestarbh

import (
	"math"
	"sort"
	"testing"
)

func TestTreeAndTraversal(t *testing.T) {
	b := GenerateBodies(3000, 12345)
	tree := BuildTree(b)
	if err := tree.Check(); err != nil {
		t.Fatal(err)
	}
	if tree.NumLeaves != b.N() {
		t.Fatalf("%d leaves for %d bodies", tree.NumLeaves, b.N())
	}
	// root mass = total mass
	total := 0.0
	for _, m := range b.M {
		total += float64(m)
	}
	if math.Abs(float64(tree.Node[2])-total)/total > 1e-4 {
		t.Fatalf("root mass %g, total %g", tree.Node[2], total)
	}
	SortByTree(b, tree)
	for i, body := range tree.LeafBody {
		if body >= 0 && (tree.Node[4*i] != b.X[body] || tree.Node[4*i+1] != b.Y[body]) {
			t.Fatalf("leaf %d does not hold body %d after sorting", i, body)
		}
	}
	ax, ay, _ := Force(b, tree, 0.5, 0.01)
	// theta = 0.5: median Barnes-Hut error vs the direct sum is small
	// (upstream accepts up to 50 % per body: it skips pairs closer than
	// sqrt(eps2), the direct sum does not)
	var es []float64
	for i := 0; i < b.N(); i += 17 {
		fx, fy := DirectForce(b, i, 0.01, false)
		es = append(es, math.Hypot(float64(ax[i])-fx, float64(ay[i])-fy)/math.Hypot(fx, fy))
	}
	sort.Float64s(es)
	if es[len(es)/2] > 0.1 {
		t.Fatalf("median Barnes-Hut error %g vs direct sum", es[len(es)/2])
	}
	// theta = 0 opens every node: equals the direct sum
	ax0, ay0, _ := Force(b, tree, 0, 0.01)
	for i := 0; i < b.N(); i += 211 {
		fx, fy := DirectForce(b, i, 0.01, true)
		if e := math.Hypot(float64(ax0[i])-fx, float64(ay0[i])-fy) / math.Hypot(fx, fy); e > 1e-3 {
			t.Fatalf("theta=0 body %d error %g", i, e)
		}
	}
}
