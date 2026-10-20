// Package lonestarbh is the W2 port of gpu_benchmarks tier2/lonestar_bh:
// 2-D Barnes-Hut, quadtree built on the host every timestep (as upstream),
// force traversal and Euler integration on the GPU.
//
// Deviations from upstream: the traversal is stackless (preorder tree with
// host-computed skip pointers) instead of a private stack[64] (scratch);
// bodies are sorted in tree order before every step (Lonestar's sort step,
// -bh-sort=false keeps upstream's random order); body count, step count
// and theta are flags (upstream: 32768 bodies, 10 steps, theta 0.5).
package lonestarbh

import (
	// embed hsaco files
	_ "embed"
	"fmt"
	"log"
	"math"
	"sort"

	"github.com/sarchlab/mgpusim/v3/driver"
	"github.com/sarchlab/mgpusim/v3/insts"
	"github.com/sarchlab/mgpusim/v3/kernels"
)

//go:embed kernels.hsaco
var hsacoBytes []byte

const wgSize = 64

// ForceArgs are the arguments of bh_force.
type ForceArgs struct {
	PX, PY, AX, AY, Node, Next driver.Ptr
	NumNodes, N                int32
	Theta2, Eps2               float32
	HiddenGlobalOffsetX        int64
	HiddenGlobalOffsetY        int64
	HiddenGlobalOffsetZ        int64
}

// IntegrateArgs are the arguments of bh_integrate.
type IntegrateArgs struct {
	PX, PY, VX, VY, AX, AY driver.Ptr
	N                      int32
	DT                     float32
	HiddenGlobalOffsetX    int64
	HiddenGlobalOffsetY    int64
	HiddenGlobalOffsetZ    int64
}

// Params are the size knobs.
type Params struct {
	Bodies int
	Steps  int
	Theta  float32
	Eps2   float32
	DT     float32
	Seed   uint32
	Sort   bool
}

// DefaultParams is a small smoke size.
func DefaultParams() Params {
	return Params{Bodies: 4096, Steps: 1, Theta: 0.5, Eps2: 0.01, DT: 0.001, Seed: 12345, Sort: true}
}

// Benchmark is the Barnes-Hut benchmark.
type Benchmark struct {
	driver  *driver.Driver
	context *driver.Context
	gpus    []int

	force, integ *insts.HsaCo
	Params       Params

	dPX, dPY, dVX, dVY, dAX, dAY driver.Ptr
	// state before the last step's kernels (for Verify)
	lastBodies *Bodies
	lastTree   *Tree
	wgs        int
}

// NewBenchmark returns the benchmark with the default size.
func NewBenchmark(d *driver.Driver) *Benchmark {
	b := &Benchmark{driver: d, Params: DefaultParams()}
	b.context = d.Init()
	b.force = kernels.LoadProgramFromMemory(hsacoBytes, "bh_force")
	b.integ = kernels.LoadProgramFromMemory(hsacoBytes, "bh_integrate")
	if b.force == nil || b.integ == nil {
		log.Panic("lonestar_bh: failed to load kernels")
	}
	return b
}

// SelectGPU selects the GPU.
func (b *Benchmark) SelectGPU(gpus []int) { b.gpus = gpus }

// SetUnifiedMemory is not supported.
func (b *Benchmark) SetUnifiedMemory() { panic("lonestar_bh does not support unified memory") }

func (b *Benchmark) alloc(bytes int, name string) driver.Ptr {
	if bytes == 0 {
		bytes = 4
	}
	p := b.driver.AllocateMemory(b.context, uint64(bytes))
	b.driver.RegisterMemoryObject(b.context, p, uint64(bytes), name)
	return p
}

func cloneBodies(s *Bodies) *Bodies {
	c := func(a []float32) []float32 { return append([]float32(nil), a...) }
	return &Bodies{c(s.X), c(s.Y), c(s.VX), c(s.VY), c(s.M)}
}

// Run builds the tree on the host and launches force + integrate per step.
func (b *Benchmark) Run() {
	pr := b.Params
	if pr.Bodies < 2 || pr.Steps < 1 {
		log.Panicf("lonestar_bh: bad params %+v", pr)
	}
	b.driver.SelectGPU(b.context, b.gpus[0])
	b.driver.SetMagicCopyCoherent(true)
	n := pr.Bodies
	bodies := GenerateBodies(n, pr.Seed)
	b.dPX = b.alloc(4*n, "bh_px")
	b.dPY = b.alloc(4*n, "bh_py")
	b.dVX = b.alloc(4*n, "bh_vx")
	b.dVY = b.alloc(4*n, "bh_vy")
	b.dAX = b.alloc(4*n, "bh_ax")
	b.dAY = b.alloc(4*n, "bh_ay")
	q := b.driver.CreateCommandQueue(b.context)
	grid := uint32((n + wgSize - 1) / wgSize * wgSize)
	b.wgs = int(grid) / wgSize
	var dNode, dNext driver.Ptr
	var nodeCap int
	for step := 0; step < pr.Steps; step++ {
		if step > 0 {
			b.driver.DrainCommandQueue(q)
			b.driver.MemCopyD2H(b.context, bodies.X, b.dPX)
			b.driver.MemCopyD2H(b.context, bodies.Y, b.dPY)
			b.driver.MemCopyD2H(b.context, bodies.VX, b.dVX)
			b.driver.MemCopyD2H(b.context, bodies.VY, b.dVY)
		}
		tree := BuildTree(bodies)
		if pr.Sort {
			SortByTree(bodies, tree)
		}
		if err := tree.Check(); err != nil {
			log.Panic(err)
		}
		nn := tree.NumNodes()
		if nn > nodeCap {
			// The tree grows only a little between steps; reallocate
			// with headroom when it does not fit.
			nodeCap = nn + nn/8
			dNode = b.alloc(16*nodeCap, fmt.Sprintf("bh_tree_node_s%d", step))
			dNext = b.alloc(4*nodeCap, fmt.Sprintf("bh_tree_next_s%d", step))
		}
		b.driver.MemCopyH2D(b.context, dNode, tree.Node)
		b.driver.MemCopyH2D(b.context, dNext, tree.Next)
		b.driver.MemCopyH2D(b.context, b.dPX, bodies.X)
		b.driver.MemCopyH2D(b.context, b.dPY, bodies.Y)
		b.driver.MemCopyH2D(b.context, b.dVX, bodies.VX)
		b.driver.MemCopyH2D(b.context, b.dVY, bodies.VY)
		if step == pr.Steps-1 {
			b.lastBodies = cloneBodies(bodies)
			b.lastTree = tree
		}
		fmt.Printf("BH,step,%d,bodies=%d,nodes=%d,leaves=%d,depth=%d,tree_mb=%.2f,wgs=%d\n",
			step, n, nn, tree.NumLeaves, tree.Depth, float64(20*nn)/1e6, b.wgs)
		fa := ForceArgs{PX: b.dPX, PY: b.dPY, AX: b.dAX, AY: b.dAY, Node: dNode, Next: dNext,
			NumNodes: int32(nn), N: int32(n), Theta2: pr.Theta * pr.Theta, Eps2: pr.Eps2}
		b.driver.EnqueueLaunchKernel(q, b.force, [3]uint32{grid, 1, 1}, [3]uint16{wgSize, 1, 1}, &fa)
		ia := IntegrateArgs{PX: b.dPX, PY: b.dPY, VX: b.dVX, VY: b.dVY, AX: b.dAX, AY: b.dAY,
			N: int32(n), DT: pr.DT}
		b.driver.EnqueueLaunchKernel(q, b.integ, [3]uint32{grid, 1, 1}, [3]uint16{wgSize, 1, 1}, &ia)
	}
	fmt.Printf("BH,config,bodies=%d,steps=%d,theta=%g,sort=%v,kernels=%d,wgs_per_kernel=%d\n",
		n, pr.Steps, pr.Theta, pr.Sort, 2*pr.Steps, b.wgs)
	b.driver.DrainCommandQueue(q)
}

// Verify compares the last step's accelerations and final positions with
// the host traversal (same tree, float32), and reports the Barnes-Hut error
// against the direct sum for a few bodies (information only).
func (b *Benchmark) Verify() {
	pr := b.Params
	bodies, tree := b.lastBodies, b.lastTree
	n := bodies.N()
	ax, ay, visits := Force(bodies, tree, pr.Theta, pr.Eps2)
	gax, gay := make([]float32, n), make([]float32, n)
	gx, gy := make([]float32, n), make([]float32, n)
	b.driver.MemCopyD2H(b.context, gax, b.dAX)
	b.driver.MemCopyD2H(b.context, gay, b.dAY)
	b.driver.MemCopyD2H(b.context, gx, b.dPX)
	b.driver.MemCopyD2H(b.context, gy, b.dPY)
	ref := cloneBodies(bodies)
	Integrate(ref, ax, ay, pr.DT)
	amax := 0.0
	for i := range ax {
		amax = math.Max(amax, math.Max(math.Abs(float64(ax[i])), math.Abs(float64(ay[i]))))
	}
	bad, maxErr := 0, 0.0
	cmp := func(name string, i int, got, want float32, scale float64) {
		e := math.Abs(float64(got) - float64(want))
		maxErr = math.Max(maxErr, e/scale)
		if !(e <= 1e-3*scale) {
			if bad < 5 {
				log.Printf("lonestar_bh: %s[%d] got %g want %g", name, i, got, want)
			}
			bad++
		}
	}
	for i := 0; i < n; i++ {
		cmp("ax", i, gax[i], ax[i], amax)
		cmp("ay", i, gay[i], ay[i], amax)
		cmp("x", i, gx[i], ref.X[i], 1+math.Abs(float64(ref.X[i])))
		cmp("y", i, gy[i], ref.Y[i], 1+math.Abs(float64(ref.Y[i])))
	}
	// Barnes-Hut approximation error vs. the direct sum (information)
	var bhErrs []float64
	cnt := min(n, 64)
	for s := 0; s < cnt; s++ {
		i := s * n / cnt
		fx, fy := DirectForce(bodies, i, float64(pr.Eps2), false)
		bhErrs = append(bhErrs, math.Hypot(float64(ax[i])-fx, float64(ay[i])-fy)/math.Max(math.Hypot(fx, fy), 1e-12))
	}
	sort.Float64s(bhErrs)
	bhErr := bhErrs[len(bhErrs)/2]
	fmt.Printf("BH,verify,elements=%d,max_rel_err=%.3g,mismatches=%d,avg_nodes_visited=%.1f,bh_vs_direct_median_rel=%.3g\n",
		4*n, maxErr, bad, float64(visits)/float64(n), bhErr)
	if bad > 0 {
		log.Fatalf("lonestar_bh: %d mismatches", bad)
	}
	log.Printf("Passed!")
}
