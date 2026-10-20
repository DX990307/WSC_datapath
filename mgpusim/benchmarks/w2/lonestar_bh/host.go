package lonestarbh

import (
	"fmt"
	"math"
)

// Bodies holds the body arrays (structure of arrays, as upstream).
type Bodies struct {
	X, Y, VX, VY, M []float32
}

// N returns the number of bodies.
func (b *Bodies) N() int { return len(b.X) }

// xorshift32 as upstream.
type xorshift32 uint32

func (s *xorshift32) next() uint32 {
	v := uint32(*s)
	v ^= v << 13
	v ^= v >> 17
	v ^= v << 5
	*s = xorshift32(v)
	return v
}

func (s *xorshift32) float(lo, hi float32) float32 {
	return lo + float32(s.next()&0xFFFFFF)/float32(0xFFFFFF)*(hi-lo)
}

// GenerateBodies reproduces the upstream input: bodies in a disk of radius
// 0.1..10, small random velocities, masses 0.5..2 (xorshift32, seed 12345
// upstream).
func GenerateBodies(n int, seed uint32) *Bodies {
	if seed == 0 {
		seed = 12345
	}
	s := xorshift32(seed)
	b := &Bodies{make([]float32, n), make([]float32, n), make([]float32, n),
		make([]float32, n), make([]float32, n)}
	for i := 0; i < n; i++ {
		r := s.float(0.1, 10)
		a := s.float(0, 2*3.14159265)
		b.X[i] = r * float32(math.Cos(float64(a)))
		b.Y[i] = r * float32(math.Sin(float64(a)))
		b.VX[i] = s.float(-0.1, 0.1)
		b.VY[i] = s.float(-0.1, 0.1)
		b.M[i] = s.float(0.5, 2)
	}
	return b
}

type qnode struct {
	cx, cy, mass float32
	bx, by, bw   float32
	child        [4]int32
	body         int32
}

// quadTree is the upstream CPU quadtree (one body per leaf).
type quadTree struct {
	nodes []qnode
	maxD  int
}

func (t *quadTree) newNode(bx, by, bw float32) int32 {
	t.nodes = append(t.nodes, qnode{bx: bx, by: by, bw: bw, child: [4]int32{-1, -1, -1, -1}, body: -1})
	return int32(len(t.nodes) - 1)
}

func (t *quadTree) quadrant(n int32, x, y float32) int {
	nd := &t.nodes[n]
	midx := nd.bx + nd.bw*0.5
	midy := nd.by + nd.bw*0.5
	q := 0
	if x >= midx {
		q |= 1
	}
	if y >= midy {
		q |= 2
	}
	return q
}

func (t *quadTree) childFor(n int32, q int) int32 {
	if t.nodes[n].child[q] == -1 {
		cbw := t.nodes[n].bw * 0.5
		cbx, cby := t.nodes[n].bx, t.nodes[n].by
		if q&1 != 0 {
			cbx += cbw
		}
		if q&2 != 0 {
			cby += cbw
		}
		c := t.newNode(cbx, cby, cbw)
		t.nodes[n].child[q] = c
	}
	return t.nodes[n].child[q]
}

func (t *quadTree) insert(n int32, body int, b *Bodies, depth int) {
	if depth > t.maxD {
		t.maxD = depth
	}
	if depth > 200 {
		panic("lonestar_bh: quadtree too deep (coincident bodies?)")
	}
	x, y, m := b.X[body], b.Y[body], b.M[body]
	if t.nodes[n].mass == 0 && t.nodes[n].body == -1 {
		t.nodes[n].body = int32(body)
		t.nodes[n].cx, t.nodes[n].cy, t.nodes[n].mass = x, y, m
		return
	}
	if old := t.nodes[n].body; old >= 0 {
		t.nodes[n].body = -1
		c := t.childFor(n, t.quadrant(n, b.X[old], b.Y[old]))
		t.insert(c, int(old), b, depth+1)
	}
	c := t.childFor(n, t.quadrant(n, x, y))
	t.insert(c, body, b, depth+1)
	nd := &t.nodes[n]
	total := nd.mass + m
	nd.cx = (nd.cx*nd.mass + x*m) / total
	nd.cy = (nd.cy*nd.mass + y*m) / total
	nd.mass = total
}

func buildQuadTree(b *Bodies) *quadTree {
	n := b.N()
	t := &quadTree{nodes: make([]qnode, 0, 4*n)}
	minx, miny, maxx, maxy := b.X[0], b.Y[0], b.X[0], b.Y[0]
	for i := 1; i < n; i++ {
		minx = min(minx, b.X[i])
		miny = min(miny, b.Y[i])
		maxx = max(maxx, b.X[i])
		maxy = max(maxy, b.Y[i])
	}
	w := max(maxx-minx, maxy-miny) * 1.001
	t.newNode(minx-0.0005*w, miny-0.0005*w, w)
	for i := 0; i < n; i++ {
		t.insert(0, i, b, 0)
	}
	return t
}

// Tree is the flattened tree the kernel walks: depth-first preorder
// (children in quadrant order 0..3), Node = (cx, cy, mass, w2), Next = the
// preorder index after the node's subtree (num_nodes at the end).
type Tree struct {
	Node      []float32 // 4 per node
	Next      []int32
	LeafBody  []int32 // body index of each leaf in preorder (-1 internal)
	Depth     int
	NumLeaves int
}

// NumNodes returns the node count.
func (t *Tree) NumNodes() int { return len(t.Next) }

// BuildTree builds the quadtree of b on the host and flattens it.
func BuildTree(b *Bodies) *Tree {
	q := buildQuadTree(b)
	out := &Tree{Node: make([]float32, 0, 4*len(q.nodes)), Next: make([]int32, 0, len(q.nodes)),
		LeafBody: make([]int32, 0, len(q.nodes)), Depth: q.maxD}
	var walk func(n int32)
	walk = func(n int32) {
		nd := &q.nodes[n]
		idx := len(out.Next)
		leaf := nd.child == [4]int32{-1, -1, -1, -1}
		w2 := float32(-1) // leaves: always accepted, also for theta = 0
		if !leaf {
			w2 = nd.bw * nd.bw
		}
		out.Node = append(out.Node, nd.cx, nd.cy, nd.mass, w2)
		out.Next = append(out.Next, 0)
		out.LeafBody = append(out.LeafBody, nd.body)
		if leaf {
			out.NumLeaves++
		}
		for _, c := range nd.child {
			if c >= 0 {
				walk(c)
			}
		}
		out.Next[idx] = int32(len(out.Next))
	}
	walk(0)
	return out
}

// SortByTree reorders the bodies in tree (preorder leaf) order, like
// Lonestar's sort kernel, so that neighbouring threads walk similar paths.
// It returns the permutation (new index -> old index).
func SortByTree(b *Bodies, t *Tree) []int32 {
	perm := make([]int32, 0, b.N())
	for _, body := range t.LeafBody {
		if body >= 0 {
			perm = append(perm, body)
		}
	}
	if len(perm) != b.N() {
		panic(fmt.Sprintf("lonestar_bh: %d leaves for %d bodies", len(perm), b.N()))
	}
	re := func(a []float32) {
		c := make([]float32, len(a))
		for i, p := range perm {
			c[i] = a[p]
		}
		copy(a, c)
	}
	re(b.X)
	re(b.Y)
	re(b.VX)
	re(b.VY)
	re(b.M)
	inv := make([]int32, len(perm))
	for i, p := range perm {
		inv[p] = int32(i)
	}
	for i, body := range t.LeafBody {
		if body >= 0 {
			t.LeafBody[i] = inv[body]
		}
	}
	return perm
}

// Check verifies every index the kernel dereferences.
func (t *Tree) Check() error {
	n := int32(t.NumNodes())
	for k, nx := range t.Next {
		if nx <= int32(k) || nx > n {
			return fmt.Errorf("lonestar_bh: next[%d] = %d out of range", k, nx)
		}
	}
	return nil
}

// Force is the host version of bh_force (same traversal and float32
// arithmetic order). It also returns the nodes visited.
func Force(b *Bodies, t *Tree, theta, eps2 float32) (ax, ay []float32, visits int64) {
	n := b.N()
	ax, ay = make([]float32, n), make([]float32, n)
	theta2 := theta * theta
	nn := int32(t.NumNodes())
	for i := 0; i < n; i++ {
		xi, yi := b.X[i], b.Y[i]
		var fx, fy float32
		k := int32(0)
		for k < nn {
			visits++
			nd := t.Node[4*k : 4*k+4]
			dx := nd[0] - xi
			dy := nd[1] - yi
			d2 := dx*dx + dy*dy + eps2
			if nd[3] < theta2*d2 {
				if nd[2] > 0 && d2 > eps2*2 {
					inv := float32(1 / math.Sqrt(float64(d2)))
					inv3 := inv * inv * inv
					fx += nd[2] * dx * inv3
					fy += nd[2] * dy * inv3
				}
				k = t.Next[k]
			} else {
				k++
			}
		}
		ax[i], ay[i] = fx, fy
	}
	return ax, ay, visits
}

// Integrate is the host version of bh_integrate.
func Integrate(b *Bodies, ax, ay []float32, dt float32) {
	for i := range b.X {
		b.VX[i] += ax[i] * dt
		b.VY[i] += ay[i] * dt
		b.X[i] += b.VX[i] * dt
		b.Y[i] += b.VY[i] * dt
	}
}

// DirectForce is the O(N^2) sum for body i (upstream's CPU check).
// excludeClose drops pairs with dist2 <= 2 eps2, as the traversal does.
func DirectForce(b *Bodies, i int, eps2 float64, excludeClose bool) (float64, float64) {
	var fx, fy float64
	for j := range b.X {
		if j == i {
			continue
		}
		dx := float64(b.X[j] - b.X[i])
		dy := float64(b.Y[j] - b.Y[i])
		d2 := dx*dx + dy*dy + eps2
		if excludeClose && float32(d2) <= 2*float32(eps2) {
			continue
		}
		inv := 1 / math.Sqrt(d2)
		fx += float64(b.M[j]) * dx * inv * inv * inv
		fy += float64(b.M[j]) * dy * inv * inv * inv
	}
	return fx, fy
}
