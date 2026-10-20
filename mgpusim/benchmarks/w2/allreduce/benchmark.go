// Package allreduce is the W2 collective workload: data-parallel gradient
// allreduce over R = 48 copies (one per GPM), as reduce-scatter followed by
// all-gather, after the access pattern of gpu_benchmarks
// tier3/nccl_reduce_scatter + tier3/nccl_allgather (NCCL ring collectives
// across GPUs; here one unified wafer, so each rank's copy is a row of one
// buffer and both steps are pull kernels: every GPM reads the 47 other
// copies of its shard, then every GPM reads the 47 other shards).
//
// Size: G = Ranks * CUs * 256 * PerCU floats per copy, so the
// reduce-scatter has PerCU work-groups per CU and work-group w runs on the
// GPM that owns shard w (contiguous dispatch); the all-gather has
// Ranks * G / 1024 work-groups (float4 per thread).
package allreduce

import (
	// embed hsaco files
	_ "embed"
	"fmt"
	"log"

	"github.com/sarchlab/mgpusim/v3/driver"
	"github.com/sarchlab/mgpusim/v3/insts"
	"github.com/sarchlab/mgpusim/v3/kernels"
)

//go:embed kernels.hsaco
var hsacoBytes []byte

// HiddenArgs are the hidden kernel arguments.
type HiddenArgs struct {
	HiddenGlobalOffsetX int64
	HiddenGlobalOffsetY int64
	HiddenGlobalOffsetZ int64
}

// RSArgs are the arguments of ar_reduce_scatter.
type RSArgs struct {
	In, Red  driver.Ptr
	Ranks, G int32
	HiddenArgs
}

// AGArgs are the arguments of ar_all_gather.
type AGArgs struct {
	Red, Out driver.Ptr
	G4, Pad  int32
	HiddenArgs
}

// Params is the input size.
type Params struct {
	Ranks int // gradient copies (48 = one per GPM)
	CUs   int // CUs per GPM
	PerCU int // reduce-scatter work-groups per CU
}

// G is the floats per copy.
func (p Params) G() int { return p.Ranks * p.CUs * 256 * p.PerCU }

// Validate checks the parameters.
func (p Params) Validate() error {
	if p.Ranks <= 0 || p.CUs <= 0 || p.PerCU <= 0 {
		return fmt.Errorf("allreduce: bad parameters %+v", p)
	}
	if uint64(p.Ranks)*uint64(p.G()) >= 1<<32 {
		return fmt.Errorf("allreduce: too large")
	}
	return nil
}

// Value is the deterministic input element e of rank r: a multiple of
// 1/16 below 4, so every fp32 sum over <= 2^18 ranks is exact.
func Value(r, e int) float32 { return float32((r*131+e*7)%61) / 16 }

// Reference is the exact reduced vector.
func (p Params) Reference() []float32 {
	red := make([]float32, p.G())
	for e := range red {
		var s float32
		for r := 0; r < p.Ranks; r++ {
			s += Value(r, e)
		}
		red[e] = s
	}
	return red
}

// DefaultParams is the smallest aligned size on 28-CU GPMs: G = 344064
// floats (1.3 MB) per copy, 1344 + 16128 work-groups.
func DefaultParams() Params { return Params{Ranks: 48, CUs: 28, PerCU: 1} }

// Benchmark is the allreduce benchmark.
type Benchmark struct {
	driver  *driver.Driver
	context *driver.Context
	gpus    []int
	rs, ag  *insts.HsaCo

	Params Params
	dOut   driver.Ptr
}

// NewBenchmark returns an allreduce benchmark.
func NewBenchmark(d *driver.Driver) *Benchmark {
	b := &Benchmark{driver: d, Params: DefaultParams()}
	b.context = d.Init()
	b.rs = kernels.LoadProgramFromMemory(hsacoBytes, "ar_reduce_scatter")
	b.ag = kernels.LoadProgramFromMemory(hsacoBytes, "ar_all_gather")
	if b.rs == nil || b.ag == nil {
		log.Panic("allreduce: failed to load kernels")
	}
	return b
}

// SelectGPU selects the GPU.
func (b *Benchmark) SelectGPU(gpus []int) { b.gpus = gpus }

// SetUnifiedMemory is not supported.
func (b *Benchmark) SetUnifiedMemory() { panic("allreduce does not support unified memory") }

// EnableVerification is a no-op.
func (b *Benchmark) EnableVerification() {}

func (b *Benchmark) alloc(bytes uint64, name string) driver.Ptr {
	p := b.driver.AllocateMemory(b.context, bytes)
	b.driver.RegisterMemoryObject(b.context, p, bytes, name)
	return p
}

// Run uploads the copies and launches reduce-scatter and all-gather.
func (b *Benchmark) Run() {
	p := b.Params
	if err := p.Validate(); err != nil {
		log.Panic(err)
	}
	b.driver.SelectGPU(b.context, b.gpus[0])
	b.driver.SetMagicCopyCoherent(true)
	G, R := p.G(), p.Ranks
	in := make([]float32, R*G)
	for r := 0; r < R; r++ {
		for e := 0; e < G; e++ {
			in[r*G+e] = Value(r, e)
		}
	}
	bytes := uint64(R*G) * 4
	dIn := b.alloc(bytes, "ar_in")
	dRed := b.alloc(uint64(G)*4, "ar_red")
	b.dOut = b.alloc(bytes, "ar_out")
	b.driver.MemCopyH2D(b.context, dIn, in)
	rsWG, agWG := G/256, R*G/1024
	fmt.Printf("ALLREDUCE,config,ranks=%d,floats_per_copy=%d,copy_mb=%.2f,total_in_mb=%.1f\n",
		R, G, float64(G)*4/1e6, float64(bytes)/1e6)
	fmt.Printf("ALLREDUCE,kernels,count=2,rs_wgs=%d,ag_wgs=%d,total_wgs=%d\n", rsWG, agWG, rsWG+agWG)
	q := b.driver.CreateCommandQueue(b.context)
	rs := RSArgs{In: dIn, Red: dRed, Ranks: int32(R), G: int32(G)}
	b.driver.EnqueueLaunchKernel(q, b.rs, [3]uint32{uint32(G), 1, 1}, [3]uint16{256, 1, 1}, &rs)
	ag := AGArgs{Red: dRed, Out: b.dOut, G4: int32(G / 4)}
	b.driver.EnqueueLaunchKernel(q, b.ag, [3]uint32{uint32(R * G / 4), 1, 1}, [3]uint16{256, 1, 1}, &ag)
	b.driver.DrainCommandQueue(q)
}

// Verify checks every copy of the output against the exact sum.
func (b *Benchmark) Verify() {
	p := b.Params
	red := p.Reference()
	G := p.G()
	got := make([]float32, p.Ranks*G)
	b.driver.MemCopyD2H(b.context, got, b.dOut)
	bad := 0
	for i, v := range got {
		if v != red[i%G] {
			if bad < 5 {
				log.Printf("allreduce: out[%d][%d] = %g want %g", i/G, i%G, v, red[i%G])
			}
			bad++
		}
	}
	fmt.Printf("ALLREDUCE,verify,elements=%d,mismatches=%d\n", len(got), bad)
	if bad > 0 {
		log.Fatalf("allreduce: verification failed")
	}
	log.Printf("Passed!")
}
