// Package altiscfd is the W2 port of gpu_benchmarks tier2/altis_cfd: the
// Altis euler3d finite-volume solver (unstructured tetrahedral mesh, 4 face
// neighbours per cell read through an index list, 5 SoA conserved
// variables). Per timestep: copy, step factor, then three RK stages of flux
// + update (2 + 2*3 kernels).
//
// Deviations: the input is the tier2 synthetic tetrahedral mesh (the
// upstream random mesh is invalid, see tier2 README), cell count, timesteps
// and an optional random relabelling of the cells (-cfd-shuffle) are flags;
// upstream __constant__ far-field values are a 17-float global buffer.
package altiscfd

import (
	// embed hsaco files
	_ "embed"
	"fmt"
	"log"
	"math"

	"github.com/sarchlab/mgpusim/v3/driver"
	"github.com/sarchlab/mgpusim/v3/insts"
	"github.com/sarchlab/mgpusim/v3/kernels"
)

//go:embed kernels.hsaco
var hsacoBytes []byte

// Params are the size knobs.
type Params struct {
	Cells   int
	Steps   int
	Block   int
	Shuffle bool
	Seed    int64
}

// DefaultParams is a small smoke size.
func DefaultParams() Params {
	return Params{Cells: 6000, Steps: 1, Block: 256, Seed: 1}
}

// CopyArgs are the arguments of cfd_copy.
type CopyArgs struct {
	N                   int32
	Pad                 int32
	State, Old          driver.Ptr
	HiddenGlobalOffsetX int64
	HiddenGlobalOffsetY int64
	HiddenGlobalOffsetZ int64
}

// StepArgs are the arguments of cfd_step_factor.
type StepArgs struct {
	N                   int32
	Pad                 int32
	Vars, Areas, Factor driver.Ptr
	HiddenGlobalOffsetX int64
	HiddenGlobalOffsetY int64
	HiddenGlobalOffsetZ int64
}

// FluxArgs are the arguments of cfd_flux.
type FluxArgs struct {
	N                               int32
	Pad                             int32
	Nbrs, Normals, Vars, Fluxes, FF driver.Ptr
	HiddenGlobalOffsetX             int64
	HiddenGlobalOffsetY             int64
	HiddenGlobalOffsetZ             int64
}

// RKArgs are the arguments of cfd_rk_update.
type RKArgs struct {
	J, N                      int32
	Old, Vars, Factor, Fluxes driver.Ptr
	HiddenGlobalOffsetX       int64
	HiddenGlobalOffsetY       int64
	HiddenGlobalOffsetZ       int64
}

// Benchmark is the CFD benchmark.
type Benchmark struct {
	driver  *driver.Driver
	context *driver.Context
	gpus    []int

	kCopy, kStep, kFlux, kRK *insts.HsaCo
	Params                   Params

	mesh   *Mesh
	dState driver.Ptr
}

// NewBenchmark returns the benchmark with the default size.
func NewBenchmark(d *driver.Driver) *Benchmark {
	b := &Benchmark{driver: d, Params: DefaultParams()}
	b.context = d.Init()
	b.kCopy = kernels.LoadProgramFromMemory(hsacoBytes, "cfd_copy")
	b.kStep = kernels.LoadProgramFromMemory(hsacoBytes, "cfd_step_factor")
	b.kFlux = kernels.LoadProgramFromMemory(hsacoBytes, "cfd_flux")
	b.kRK = kernels.LoadProgramFromMemory(hsacoBytes, "cfd_rk_update")
	if b.kCopy == nil || b.kStep == nil || b.kFlux == nil || b.kRK == nil {
		log.Panic("altis_cfd: failed to load kernels")
	}
	return b
}

// SelectGPU selects the GPU.
func (b *Benchmark) SelectGPU(gpus []int) { b.gpus = gpus }

// SetUnifiedMemory is not supported.
func (b *Benchmark) SetUnifiedMemory() { panic("altis_cfd does not support unified memory") }

func (b *Benchmark) upload(name string, data interface{}, bytes int) driver.Ptr {
	p := b.driver.AllocateMemory(b.context, uint64(bytes))
	b.driver.RegisterMemoryObject(b.context, p, uint64(bytes), name)
	if data != nil {
		b.driver.MemCopyH2D(b.context, p, data)
	}
	return p
}

// Run builds the mesh and launches the solver.
func (b *Benchmark) Run() {
	pr := b.Params
	if pr.Cells < 1 || pr.Steps < 1 || pr.Block < 64 || pr.Block > 1024 {
		log.Panicf("altis_cfd: bad params %+v", pr)
	}
	b.driver.SelectGPU(b.context, b.gpus[0])
	b.driver.SetMagicCopyCoherent(true)
	m := NewMesh(pr.Cells, pr.Block, pr.Shuffle, pr.Seed)
	if err := m.Check(); err != nil {
		log.Panic(err)
	}
	if 12*m.Padded >= math.MaxInt32 {
		log.Panic("altis_cfd: mesh too large for 32-bit indices")
	}
	b.mesh = m
	n := m.Padded
	b.dState = b.upload("cfd_state", m.Initial, 20*n)
	dOld := b.upload("cfd_old", nil, 20*n)
	dFlux := b.upload("cfd_flux", nil, 20*n)
	dFactor := b.upload("cfd_factor", nil, 4*n)
	dAreas := b.upload("cfd_areas", m.Areas, 4*n)
	dNormals := b.upload("cfd_normals", m.Normals, 48*n)
	dNbrs := b.upload("cfd_neighbors", m.Neighbors, 16*n)
	dFF := b.upload("cfd_ff", FFBuffer(), 4*17)
	q := b.driver.CreateCommandQueue(b.context)
	grid := [3]uint32{uint32(n), 1, 1}
	wg := [3]uint16{uint16(pr.Block), 1, 1}
	for it := 0; it < pr.Steps; it++ {
		b.driver.EnqueueLaunchKernel(q, b.kCopy, grid, wg, &CopyArgs{N: int32(n), State: b.dState, Old: dOld})
		b.driver.EnqueueLaunchKernel(q, b.kStep, grid, wg,
			&StepArgs{N: int32(n), Vars: b.dState, Areas: dAreas, Factor: dFactor})
		for stage := 0; stage < 3; stage++ {
			b.driver.EnqueueLaunchKernel(q, b.kFlux, grid, wg, &FluxArgs{N: int32(n), Nbrs: dNbrs,
				Normals: dNormals, Vars: b.dState, Fluxes: dFlux, FF: dFF})
			b.driver.EnqueueLaunchKernel(q, b.kRK, grid, wg, &RKArgs{J: int32(stage), N: int32(n),
				Old: dOld, Vars: b.dState, Factor: dFactor, Fluxes: dFlux})
		}
	}
	fmt.Printf("CFD,config,cells=%d,padded=%d,cube_side=%d,steps=%d,block=%d,shuffle=%v,kernels=%d,wgs_per_kernel=%d,"+
		"footprint_mb=%.1f\n", pr.Cells, n, m.Side, pr.Steps, pr.Block, pr.Shuffle, 8*pr.Steps, n/pr.Block,
		float64(n*4*(5*3+1+1+12+4))/1e6)
	b.driver.DrainCommandQueue(q)
}

// Verify compares the final state with the float64 reference.
func (b *Benchmark) Verify() {
	m := b.mesh
	want := Reference(m, b.Params.Steps)
	got := make([]float32, len(want))
	b.driver.MemCopyD2H(b.context, got, b.dState)
	bad, maxErr := 0, 0.0
	for i := range want {
		e := math.Abs(float64(got[i]) - want[i])
		rel := e / (1 + math.Abs(want[i]))
		maxErr = math.Max(maxErr, rel)
		if !(rel <= 1e-4) {
			if bad < 5 {
				log.Printf("altis_cfd: state[%d] got %g want %g", i, got[i], want[i])
			}
			bad++
		}
	}
	fmt.Printf("CFD,verify,elements=%d,max_rel_err=%.3g,mismatches=%d\n", len(want), maxErr, bad)
	if bad > 0 {
		log.Fatalf("altis_cfd: %d mismatches", bad)
	}
	log.Printf("Passed!")
}
