package npbcg

import (
	"bytes"
	"encoding/binary"
	"math"
	"testing"
)

// The fp64 port reproduces the published class S zeta after the full NAS
// iteration count (checks the makea port and the CG loop).
func TestClassSZeta(t *testing.T) {
	c := Classes["S"]
	a := MakeMatrix(c)
	res := Reference(a, c, c.Iterations, 25)
	z := res.Zeta[len(res.Zeta)-1]
	if math.Abs(z-c.Zeta)/c.Zeta > 1e-10 {
		t.Fatalf("zeta %.15g, want %.15g", z, c.Zeta)
	}
	t.Logf("class S nnz %d zeta %.15g", len(a.Values), z)
}

// An fp32 run of the GPU's kernel sequence stays close to fp64 after one
// outer iteration (the tolerance Verify uses).
func TestFP32Sequence(t *testing.T) {
	c := Classes["S"]
	a := MakeMatrix(c)
	ref := Reference(a, c, 1, 25)
	x32, zeta := hostFP32(a, c, 1, 25, 64)
	if e := math.Abs(zeta-ref.Zeta[0]) / ref.Zeta[0]; e > zetaTol {
		t.Fatalf("fp32 zeta %.9g vs %.9g", zeta, ref.Zeta[0])
	}
	if e := maxRelErr(x32, ref.X); e > vecTol {
		t.Fatalf("fp32 x error %g", e)
	}
}

func TestArgSizes(t *testing.T) {
	for _, c := range []struct {
		v    interface{}
		want int
	}{{&InitArgs{}, 4*8 + 8 + 24}, {&SpmvArgs{}, 5*8 + 8 + 24}, {&DotArgs{}, 3*8 + 8 + 24},
		{&ReduceArgs{}, 2*8 + 8 + 24}, {&AxpyArgs{}, 8*8 + 8 + 24},
		{&DirectionArgs{}, 5*8 + 8 + 24}, {&NormalizeArgs{}, 3*8 + 8 + 24}} {
		var buf bytes.Buffer
		if err := binary.Write(&buf, binary.LittleEndian, c.v); err != nil || buf.Len() != c.want {
			t.Fatalf("%T: %v %d want %d", c.v, err, buf.Len(), c.want)
		}
	}
}
