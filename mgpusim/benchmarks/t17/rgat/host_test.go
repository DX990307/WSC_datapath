package rgat

import (
	"encoding/binary"
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
)

func TestSampleBlocks(t *testing.T) {
	g := SyntheticIGBH(500, 32, 3)
	rng := rand.New(rand.NewSource(1))
	seeds := []int32{1, 5, 9, 40, 77}
	fan := []int{4, 2}
	s := SampleBlocks(g, "paper", seeds, fan, rng)
	m := NewModel(s, g.FeatDim, 64, 1, 1)
	if err := s.Check(g, m); err != nil {
		t.Fatal(err)
	}
	// last layer: destinations are the seeds
	if len(s.Dst[1]) != 1 || len(s.Dst[1]["paper"]) != len(seeds) {
		t.Fatalf("layer 1 dst %v", s.Dst[1])
	}
	// layer 0 destinations start with layer 1's and contain every sampled source
	for typ, d1 := range s.Dst[1] {
		for i, v := range d1 {
			if s.Dst[0][typ][i] != v {
				t.Fatalf("layer 0 %s does not start with layer 1 dst", typ)
			}
		}
	}
	for _, b := range s.Blocks[1] {
		in := map[int32]bool{}
		for _, v := range s.Dst[0][b.SrcType] {
			in[v] = true
		}
		for _, v := range b.SrcNodes {
			if !in[v] {
				t.Fatalf("%s source %d not a layer-0 destination", b.RelName, v)
			}
		}
	}
	// sampled neighbours are real in-neighbours, distinct, at most fanout
	for l, blocks := range s.Blocks {
		for _, b := range blocks {
			adj := g.in[b.Rel]
			for i, d := range b.DstNodes {
				nb := map[int32]bool{}
				for _, v := range adj.src[adj.ptr[d]:adj.ptr[d+1]] {
					nb[v] = true
				}
				deg := int(adj.ptr[d+1] - adj.ptr[d])
				got := b.RowPtr[i+1] - b.RowPtr[i]
				want := deg
				if want > fan[l] {
					want = fan[l]
				}
				if int(got) != want {
					t.Fatalf("%s dst %d: %d sampled of %d (fanout %d)", b.RelName, d, got, deg, fan[l])
				}
				seen := map[int32]bool{}
				for e := b.RowPtr[i]; e < b.RowPtr[i+1]; e++ {
					v := b.SrcNodes[b.ColIdx[e]]
					if !nb[v] || seen[v] {
						t.Fatalf("%s dst %d: bad neighbour %d", b.RelName, d, v)
					}
					seen[v] = true
				}
			}
		}
	}
}

// The kernels compute er as x_dst . (W a_r); the reference projects the
// destination first. They must agree (checked through the float64 path).
func TestReferenceAttentionFactorisation(t *testing.T) {
	g := SyntheticIGBH(200, 32, 5)
	s := SampleBlocks(g, "paper", []int32{3, 4}, []int{3, 2}, rand.New(rand.NewSource(2)))
	feat := func(tp string, id int32) []float32 {
		r := make([]float32, g.FeatDim)
		g.Feats[tp].Row(int(id), r)
		return r
	}
	for _, heads := range []int{1, 2, 4} {
		m := NewModel(s, g.FeatDim, 128, heads, 2)
		ref := Reference(s, m, feat)
		b := s.Blocks[0][0]
		k := [2]int{0, b.Rel}
		x := feat(b.DstType, b.DstNodes[0])
		d := m.Hidden / heads
		if len(m.V[k]) != heads*len(x) {
			t.Fatalf("heads %d: len(V) = %d", heads, len(m.V[k]))
		}
		for hd := 0; hd < heads; hd++ {
			a, c := 0.0, 0.0
			for i := range x {
				a += float64(x[i]) * float64(m.V[k][hd*len(x)+i])
			}
			for h := hd * d; h < (hd+1)*d; h++ {
				p := 0.0
				for i := range x {
					p += float64(x[i]) * float64(m.W[k][i*m.Hidden+h])
				}
				c += p * float64(m.AR[k][h])
			}
			if math.Abs(a-c) > 1e-4 {
				t.Fatalf("heads %d head %d: x.v = %g, (xW).a = %g", heads, hd, a, c)
			}
		}
		if len(ref[1]["paper"]) != 2*m.Hidden {
			t.Fatalf("output size %d", len(ref[1]["paper"]))
		}
	}
}

// TestReferenceHeadsDiffer checks that the heads have their own attention:
// with more than one head the outputs differ from the single-head ones.
func TestReferenceHeadsDiffer(t *testing.T) {
	g := SyntheticIGBH(200, 32, 5)
	s := SampleBlocks(g, "paper", []int32{3, 4, 5}, []int{5, 3}, rand.New(rand.NewSource(2)))
	feat := func(tp string, id int32) []float32 {
		r := make([]float32, g.FeatDim)
		g.Feats[tp].Row(int(id), r)
		return r
	}
	one := Reference(s, NewModel(s, g.FeatDim, 128, 1, 2), feat)[1]["paper"]
	four := Reference(s, NewModel(s, g.FeatDim, 128, 4, 2), feat)[1]["paper"]
	diff := 0.0
	for i := range one {
		diff = math.Max(diff, math.Abs(one[i]-four[i]))
	}
	if diff < 1e-6 {
		t.Fatal("4-head output equals the single-head output")
	}
}

func writeNpy(t *testing.T, path, descr string, shape string, data []byte) {
	hdr := fmt.Sprintf("{'descr': '%s', 'fortran_order': False, 'shape': %s, }", descr, shape)
	for (10+len(hdr)+1)%64 != 0 {
		hdr += " "
	}
	hdr += "\n"
	b := []byte("\x93NUMPY\x01\x00")
	b = binary.LittleEndian.AppendUint16(b, uint16(len(hdr)))
	b = append(b, hdr...)
	b = append(b, data...)
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestNpyReaders(t *testing.T) {
	dir := t.TempDir()
	var f []byte
	for i := 0; i < 6; i++ {
		f = binary.LittleEndian.AppendUint32(f, math.Float32bits(float32(i)+0.5))
	}
	fp := filepath.Join(dir, "feat.npy")
	writeNpy(t, fp, "<f4", "(3, 2)", f)
	m, err := openNpyMatrix(fp, "<f4")
	if err != nil {
		t.Fatal(err)
	}
	row := make([]float32, 2)
	m.Row(2, row)
	if m.rows != 3 || m.cols != 2 || row[0] != 4.5 || row[1] != 5.5 {
		t.Fatalf("rows %d cols %d row %v", m.rows, m.cols, row)
	}
	var e []byte
	for _, v := range []int64{0, 7, 1, 8, 2, 9} {
		e = binary.LittleEndian.AppendUint64(e, uint64(v))
	}
	ep := filepath.Join(dir, "edge_index.npy")
	writeNpy(t, ep, "<i8", "(3, 2)", e)
	src, dst, err := readEdgeIndex(ep)
	if err != nil || src[2] != 2 || dst[2] != 9 || dst[0] != 7 {
		t.Fatalf("edges %v %v %v", src, dst, err)
	}
}
