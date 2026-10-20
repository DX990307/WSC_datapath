// Package npbis is NAS IS (integer sort: rank() of near-gaussian keys),
// ported from gpu_benchmarks tier2/npb_is to OpenCL for the CuPath wafer
// (W2 workload set). Upstream's atomicAdd histogram is replaced by the
// bucketed, privatised MPI-IS scheme (per-WG tile counts, pull
// reduce-scatter of the counts, bucket scan, all-to-all key scatter,
// per-bucket ranking, final rank gather); see kernels.cl. key_buff1 is
// bit-identical to upstream's.
package npbis

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

// ModifyArgs are the arguments of is_modify.
type ModifyArgs struct {
	Key               driver.Ptr
	Iteration, MaxKey int32
	HiddenArgs
}

// CountArgs are the arguments of is_count.
type CountArgs struct {
	Key, C, R driver.Ptr
	NB, LogW  int32
	HiddenArgs
}

// ColScanArgs are the arguments of is_colscan.
type ColScanArgs struct {
	C, Off, Tot driver.Ptr
	NB, NT      int32
	HiddenArgs
}

// BucketScanArgs are the arguments of is_bucketscan.
type BucketScanArgs struct {
	Tot, BStart driver.Ptr
	NB, Per     int32
	HiddenArgs
}

// ScatterArgs are the arguments of is_scatter.
type ScatterArgs struct {
	Key, R, Off, BStart, KeyBuff2 driver.Ptr
	NB, LogW                      int32
	HiddenArgs
}

// RankArgs are the arguments of is_rank.
type RankArgs struct {
	KeyBuff2, BStart, KeyBuff1 driver.Ptr
	W, Per                     int32
	HiddenArgs
}

// GatherArgs are the arguments of is_gather.
type GatherArgs struct {
	Key, KeyBuff1, Rank driver.Ptr
	N, Pad              int32
	HiddenArgs
}

// Benchmark is NAS IS.
type Benchmark struct {
	driver  *driver.Driver
	context *driver.Context
	gpus    []int

	modify, count, colscan, bscan, scatter, rank, gather *insts.HsaCo

	Params Params

	keys                              []int32
	dKey, dC, dR, dOff, dTot, dBStart driver.Ptr
	dKeyBuff1, dKeyBuff2, dRank       driver.Ptr
}

// DefaultParams is class S, one rank() iteration (upstream: class A, 10).
func DefaultParams() Params { return Params{N: 1 << 16, Iterations: 1} }

// NewBenchmark returns the benchmark.
func NewBenchmark(d *driver.Driver) *Benchmark {
	b := &Benchmark{driver: d, Params: DefaultParams()}
	b.context = d.Init()
	load := func(name string) *insts.HsaCo {
		co := kernels.LoadProgramFromMemory(hsacoBytes, name)
		if co == nil {
			log.Panicf("npbis: failed to load %s", name)
		}
		return co
	}
	b.modify = load("is_modify")
	b.count = load("is_count")
	b.colscan = load("is_colscan")
	b.bscan = load("is_bucketscan")
	b.scatter = load("is_scatter")
	b.rank = load("is_rank")
	b.gather = load("is_gather")
	return b
}

// SelectGPU selects the GPU.
func (b *Benchmark) SelectGPU(gpus []int) { b.gpus = gpus }

// SetUnifiedMemory is not supported.
func (b *Benchmark) SetUnifiedMemory() { panic("npbis does not support unified memory") }

func (b *Benchmark) alloc(bytes uint64, name string) driver.Ptr {
	p := b.driver.AllocateMemory(b.context, bytes)
	b.driver.RegisterMemoryObject(b.context, p, bytes, name)
	return p
}

// Run runs Iterations rank() calls and the final rank gather.
func (b *Benchmark) Run() {
	p := b.Params
	if err := p.Validate(); err != nil {
		log.Panic(err)
	}
	b.driver.SelectGPU(b.context, b.gpus[0])
	mk := p.MaxKey()
	nb, logw := p.Buckets()
	w := 1 << logw
	nt := p.N / Tile
	b.keys = CreateSeq(p.N, mk)
	n4 := uint64(p.N) * 4
	b.dKey = b.alloc(n4, "is_key_array")
	b.dKeyBuff2 = b.alloc(n4, "is_key_buff2")
	b.dR = b.alloc(n4, "is_local_rank")
	b.dRank = b.alloc(n4, "is_rank")
	b.dKeyBuff1 = b.alloc(uint64(mk)*4, "is_key_buff1")
	b.dC = b.alloc(uint64(nt*nb)*4, "is_tile_counts")
	b.dOff = b.alloc(uint64(nt*nb)*4, "is_tile_offsets")
	b.dTot = b.alloc(uint64(nb)*4, "is_bucket_totals")
	b.dBStart = b.alloc(uint64(nb+1)*4, "is_bucket_start")
	b.driver.MemCopyH2D(b.context, b.dKey, b.keys)

	ncs := (nb + BPW - 1) / BPW
	perIter := 1 + nt + ncs + 1 + nt + nb
	fmt.Printf("NPBIS,config,n=%d,max_key=%d,iterations=%d,buckets=%d,bucket_width=%d,tiles=%d,"+
		"wgs_per_iter=%d(modify=1,count=%d,colscan=%d,bucketscan=1,scatter=%d,rank=%d),"+
		"gather_wgs=%d,total_wgs=%d,footprint_mb=%.1f\n",
		p.N, mk, p.Iterations, nb, w, nt, perIter, nt, ncs, nt, nb, p.N/WGSize,
		perIter*p.Iterations+p.N/WGSize, float64(4*n4+uint64(mk)*4+uint64(2*nt*nb)*4)/1e6)

	q := b.driver.CreateCommandQueue(b.context)
	for it := 1; it <= p.Iterations; it++ {
		b.launch(q, b.modify, 1, 64, &ModifyArgs{Key: b.dKey, Iteration: int32(it), MaxKey: int32(mk)})
		b.launch(q, b.count, nt, WGSize, &CountArgs{Key: b.dKey, C: b.dC, R: b.dR,
			NB: int32(nb), LogW: int32(logw)})
		b.launch(q, b.colscan, ncs, WGSize, &ColScanArgs{C: b.dC, Off: b.dOff, Tot: b.dTot,
			NB: int32(nb), NT: int32(nt)})
		b.launch(q, b.bscan, 1, WGSize, &BucketScanArgs{Tot: b.dTot, BStart: b.dBStart,
			NB: int32(nb), Per: int32((nb + WGSize - 1) / WGSize)})
		b.launch(q, b.scatter, nt, WGSize, &ScatterArgs{Key: b.dKey, R: b.dR, Off: b.dOff,
			BStart: b.dBStart, KeyBuff2: b.dKeyBuff2, NB: int32(nb), LogW: int32(logw)})
		b.launch(q, b.rank, nb, WGSize, &RankArgs{KeyBuff2: b.dKeyBuff2, BStart: b.dBStart,
			KeyBuff1: b.dKeyBuff1, W: int32(w), Per: int32(w / WGSize)})
	}
	b.launch(q, b.gather, p.N/WGSize, WGSize, &GatherArgs{Key: b.dKey, KeyBuff1: b.dKeyBuff1,
		Rank: b.dRank, N: int32(p.N)})
	b.driver.DrainCommandQueue(q)
}

func (b *Benchmark) launch(q *driver.CommandQueue, co *insts.HsaCo, wgs, wgSize int, args interface{}) {
	b.driver.EnqueueLaunchKernel(q, co, [3]uint32{uint32(wgs * wgSize), 1, 1},
		[3]uint16{uint16(wgSize), 1, 1}, args)
}

// Verify compares key_buff1 and the gathered ranks with the host mirror
// (exact) and prints upstream's partial-verification ranks.
func (b *Benchmark) Verify() {
	b.driver.SetMagicCopyCoherent(true)
	p := b.Params
	mk := p.MaxKey()
	keys := append([]int32(nil), b.keys...)
	for it := 1; it <= p.Iterations; it++ {
		Modify(keys, it, mk)
	}
	ref := RefRank(keys, mk)
	kb1 := make([]int32, mk)
	rank := make([]int32, p.N)
	b.driver.MemCopyD2H(b.context, kb1, b.dKeyBuff1)
	b.driver.MemCopyD2H(b.context, rank, b.dRank)
	bad := 0
	for i := range ref {
		if kb1[i] != ref[i] {
			if bad < 5 {
				log.Printf("npbis: key_buff1[%d] = %d, want %d", i, kb1[i], ref[i])
			}
			bad++
		}
	}
	badRank := 0
	for i, k := range keys {
		if rank[i] != ref[k] {
			badRank++
		}
	}
	fmt.Printf("NPBIS,verify,max_key=%d,key_buff1_mismatches=%d,rank_mismatches=%d\n", mk, bad, badRank)
	if bad > 0 || badRank > 0 {
		log.Fatalf("npbis: verification failed")
	}
	log.Printf("Passed!")
}
