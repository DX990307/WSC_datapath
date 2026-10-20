package npbmg

import (
	"math"
	"testing"
)

// The float64 reference reproduces the published NAS class S result.
func TestReferenceClassS(t *testing.T) {
	c := Classes['S']
	r := Reference(c, c.Iterations)
	if e := math.Abs(r.Norms[2]-c.L2) / c.L2; e > 1e-8 {
		t.Fatalf("class S L2 %.13e, published %.13e (rel %g)", r.Norms[2], c.L2, e)
	}
}
