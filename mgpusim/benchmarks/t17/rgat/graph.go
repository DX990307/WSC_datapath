package rgat

import (
	"encoding/binary"
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Relation is one edge type of the heterogeneous graph; messages flow from
// Src nodes to Dst nodes. Edge k goes from SrcIDs[k] to DstIDs[k].
type Relation struct {
	Name     string
	Src, Dst string
	SrcIDs   []int32
	DstIDs   []int32
}

// FeatureReader returns feature rows of one node type.
type FeatureReader interface {
	Row(id int, out []float32)
}

// Graph is a heterogeneous graph with FeatDim-dimensional node features.
type Graph struct {
	Name     string
	Types    []string
	NumNodes map[string]int
	FeatDim  int
	Rels     []Relation
	Feats    map[string]FeatureReader

	in []csr // per relation: in-neighbours by destination
}

type csr struct {
	ptr []int32 // [numDst+1]
	src []int32
}

// IGBH relations: the six edge files of IGBH, "cites" symmetrised and the
// other five also used in the reverse direction ("rev_" prefix), as the
// MLPerf Inference RGAT / IGB heterogeneous DGL loader does (11 relations).
var igbhEdges = []struct{ dir, name, src, dst string }{
	{"paper__cites__paper", "cites", "paper", "paper"},
	{"paper__written_by__author", "written_by", "paper", "author"},
	{"author__affiliated_to__institute", "affiliated_to", "author", "institute"},
	{"paper__topic__fos", "topic", "paper", "fos"},
	{"paper__published__journal", "published", "paper", "journal"},
	{"paper__venue__conference", "venue", "paper", "conference"},
}

// LoadIGBH reads an IGB heterogeneous dataset (processed/ directory of the
// igb_heterogeneous_<size>.tar.gz release). Features are read row by row
// from the .npy files on demand.
func LoadIGBH(dir string) (*Graph, error) {
	if _, err := os.Stat(filepath.Join(dir, "processed")); err == nil {
		dir = filepath.Join(dir, "processed")
	}
	g := &Graph{Name: "igbh:" + dir, NumNodes: map[string]int{}, Feats: map[string]FeatureReader{}}
	for _, t := range []string{"paper", "author", "institute", "fos", "journal", "conference"} {
		f, err := openNpyMatrix(filepath.Join(dir, t, "node_feat.npy"), "<f4")
		if err != nil {
			return nil, err
		}
		if g.FeatDim == 0 {
			g.FeatDim = f.cols
		} else if f.cols != g.FeatDim {
			return nil, fmt.Errorf("igbh: %s features have %d dims, not %d", t, f.cols, g.FeatDim)
		}
		g.Types = append(g.Types, t)
		g.NumNodes[t] = f.rows
		g.Feats[t] = f
	}
	for _, e := range igbhEdges {
		src, dst, err := readEdgeIndex(filepath.Join(dir, e.dir, "edge_index.npy"))
		if err != nil {
			return nil, err
		}
		if e.name == "cites" {
			g.Rels = append(g.Rels, Relation{e.name, e.src, e.dst,
				append(append([]int32{}, src...), dst...), append(append([]int32{}, dst...), src...)})
			continue
		}
		g.Rels = append(g.Rels,
			Relation{e.name, e.src, e.dst, src, dst},
			Relation{"rev_" + e.name, e.dst, e.src, dst, src})
	}
	return g, g.finish()
}

// finish checks the edge ids and builds the in-neighbour CSRs.
func (g *Graph) finish() error {
	g.in = make([]csr, len(g.Rels))
	for ri, r := range g.Rels {
		ns, nd := g.NumNodes[r.Src], g.NumNodes[r.Dst]
		ptr := make([]int32, nd+1)
		for k := range r.DstIDs {
			s, d := r.SrcIDs[k], r.DstIDs[k]
			if s < 0 || int(s) >= ns || d < 0 || int(d) >= nd {
				return fmt.Errorf("%s: edge %d (%d -> %d) out of range", r.Name, k, s, d)
			}
			ptr[d+1]++
		}
		for i := 1; i <= nd; i++ {
			ptr[i] += ptr[i-1]
		}
		next := append([]int32(nil), ptr[:nd]...)
		src := make([]int32, len(r.SrcIDs))
		for k := range r.DstIDs {
			d := r.DstIDs[k]
			src[next[d]] = r.SrcIDs[k]
			next[d]++
		}
		g.in[ri] = csr{ptr, src}
	}
	return nil
}

// NumEdges returns the number of edges of all relations.
func (g *Graph) NumEdges() int {
	n := 0
	for _, r := range g.Rels {
		n += len(r.SrcIDs)
	}
	return n
}

// ---- .npy reading (little-endian, C order)

var npyShape = regexp.MustCompile(`'shape':\s*\(([0-9,\s]*)\)`)
var npyDescr = regexp.MustCompile(`'descr':\s*'([^']*)'`)

type npyMatrix struct {
	path       string
	f          *os.File
	off        int64
	rows, cols int
}

func npyHeader(f *os.File) (descr string, shape []int, off int64, err error) {
	head := make([]byte, 10)
	if _, err = f.ReadAt(head, 0); err != nil {
		return
	}
	if string(head[1:6]) != "NUMPY" {
		return "", nil, 0, fmt.Errorf("%s: not an npy file", f.Name())
	}
	var hlen int64
	switch head[6] {
	case 1:
		hlen, off = int64(binary.LittleEndian.Uint16(head[8:10])), 10
	default:
		b := make([]byte, 4)
		if _, err = f.ReadAt(b, 8); err != nil {
			return
		}
		hlen, off = int64(binary.LittleEndian.Uint32(b)), 12
	}
	h := make([]byte, hlen)
	if _, err = f.ReadAt(h, off); err != nil {
		return
	}
	off += hlen
	hs := string(h)
	if strings.Contains(hs, "'fortran_order': True") {
		return "", nil, 0, fmt.Errorf("%s: fortran order not supported", f.Name())
	}
	m := npyDescr.FindStringSubmatch(hs)
	s := npyShape.FindStringSubmatch(hs)
	if m == nil || s == nil {
		return "", nil, 0, fmt.Errorf("%s: bad npy header %q", f.Name(), hs)
	}
	descr = m[1]
	for _, p := range strings.Split(s[1], ",") {
		if p = strings.TrimSpace(p); p != "" {
			v, e := strconv.Atoi(p)
			if e != nil {
				return "", nil, 0, e
			}
			shape = append(shape, v)
		}
	}
	return descr, shape, off, nil
}

func openNpyMatrix(path, wantDescr string) (*npyMatrix, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	descr, shape, off, err := npyHeader(f)
	if err != nil {
		return nil, err
	}
	if descr != wantDescr || len(shape) != 2 {
		return nil, fmt.Errorf("%s: dtype %s shape %v, want %s 2-D", path, descr, shape, wantDescr)
	}
	return &npyMatrix{path, f, off, shape[0], shape[1]}, nil
}

// Row reads one float32 row.
func (m *npyMatrix) Row(id int, out []float32) {
	buf := make([]byte, 4*m.cols)
	if _, err := m.f.ReadAt(buf, m.off+int64(id)*int64(len(buf))); err != nil {
		panic(fmt.Sprintf("%s row %d: %v", m.path, id, err))
	}
	for i := range out[:m.cols] {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(buf[4*i:]))
	}
}

func readEdgeIndex(path string) (src, dst []int32, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	descr, shape, off, err := npyHeader(f)
	if err != nil {
		return nil, nil, err
	}
	if descr != "<i8" || len(shape) != 2 || shape[1] != 2 {
		return nil, nil, fmt.Errorf("%s: dtype %s shape %v, want <i8 (E, 2)", path, descr, shape)
	}
	buf := make([]byte, 16*shape[0])
	if _, err = f.ReadAt(buf, off); err != nil {
		return nil, nil, err
	}
	src = make([]int32, shape[0])
	dst = make([]int32, shape[0])
	for k := range src {
		s := int64(binary.LittleEndian.Uint64(buf[16*k:]))
		d := int64(binary.LittleEndian.Uint64(buf[16*k+8:]))
		if s > math.MaxInt32 || d > math.MaxInt32 {
			return nil, nil, fmt.Errorf("%s: id beyond int32", path)
		}
		src[k], dst[k] = int32(s), int32(d)
	}
	return src, dst, nil
}

// ---- synthetic graph with the IGBH schema (tests, emulation checks)

type synthFeat struct {
	seed int64
	dim  int
	t    int
}

func (s synthFeat) Row(id int, out []float32) {
	rng := rand.New(rand.NewSource(s.seed*1000003 + int64(s.t)*7919 + int64(id)))
	for i := range out[:s.dim] {
		out[i] = rng.Float32()*2 - 1
	}
}

// SyntheticIGBH returns a small random graph with the IGBH node types and
// relations: scale papers, and each paper cites, is written by, has topics,
// etc. with small random degrees.
func SyntheticIGBH(papers, dim int, seed int64) *Graph {
	rng := rand.New(rand.NewSource(seed))
	g := &Graph{Name: fmt.Sprintf("synthetic(papers=%d)", papers), NumNodes: map[string]int{},
		Feats: map[string]FeatureReader{}, FeatDim: dim}
	counts := map[string]int{"paper": papers, "author": 3 * papers, "institute": papers/10 + 1,
		"fos": papers + 1, "journal": papers/10 + 1, "conference": papers/100 + 1}
	for i, t := range []string{"paper", "author", "institute", "fos", "journal", "conference"} {
		g.Types = append(g.Types, t)
		g.NumNodes[t] = counts[t]
		g.Feats[t] = synthFeat{seed, dim, i}
	}
	edges := func(n int, dst string, maxDeg int) (s, d []int32) {
		for i := 0; i < n; i++ {
			k := rng.Intn(maxDeg + 1)
			seen := map[int]bool{}
			for j := 0; j < k; j++ {
				t := rng.Intn(g.NumNodes[dst])
				if !seen[t] {
					seen[t] = true
					s, d = append(s, int32(i)), append(d, int32(t))
				}
			}
		}
		return
	}
	for _, e := range igbhEdges {
		maxDeg := 8
		if e.name == "venue" || e.name == "published" {
			maxDeg = 1
		}
		s, d := edges(g.NumNodes[e.src], e.dst, maxDeg)
		if e.name == "cites" {
			g.Rels = append(g.Rels, Relation{e.name, e.src, e.dst,
				append(append([]int32{}, s...), d...), append(append([]int32{}, d...), s...)})
			continue
		}
		g.Rels = append(g.Rels, Relation{e.name, e.src, e.dst, s, d},
			Relation{"rev_" + e.name, e.dst, e.src, d, s})
	}
	if err := g.finish(); err != nil {
		panic(err)
	}
	return g
}
