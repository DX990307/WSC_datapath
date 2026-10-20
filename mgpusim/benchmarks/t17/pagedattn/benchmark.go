// Package pagedattn implements the T17 W-A workload: one layer of
// PagedAttention decode (vLLM, SOSP'23) with the Llama-3-8B attention shape
// (32 query heads, 8 KV heads, head dim 128, GQA 4:1) and a FlashDecoding-
// style split over the context:
//
//   - pa_partial: one work-group (256 threads) per (sequence, KV head,
//     partition of PartTokens tokens). It reads the block table, gathers the
//     K blocks, computes the scores of the 4 query heads that share the KV
//     head, the partition-local softmax (v_exp_f32) and the P x V sum over
//     the gathered V blocks; it writes an unnormalised partial output plus
//     the partition max and exp-sum.
//   - pa_reduce: one wavefront per (sequence, query head) merges the
//     partitions (no atomics).
//
// All index structures (block tables, the (sequence, partition) work list)
// are built on the host: block tables come from a simulation of vLLM's free
// block queue under a random admit/finish history, so each sequence's blocks
// are scattered over the whole KV pool. Buffers use the default allocator
// (each buffer split into 48 contiguous parts, one per GPM).
package pagedattn

import (
	// embed hsaco files
	_ "embed"
	"fmt"
	"log"
	"math"
	"strings"

	"github.com/sarchlab/mgpusim/v3/driver"
	"github.com/sarchlab/mgpusim/v3/insts"
	"github.com/sarchlab/mgpusim/v3/kernels"
)

//go:embed kernels.hsaco
var hsacoBytes []byte

// PartialArgs are the arguments of pa_partial.
type PartialArgs struct {
	Q, KCache, VCache          driver.Ptr
	TmpOut, MaxLogits, ExpSums driver.Ptr
	KernargGap                 int32 // unused, see kernels.cl
	GapPadding                 int32
	BlockTables, SeqLens, Work driver.Ptr
	ScaleLog2                  float32
	NumKVHeads                 int32
	MaxBlocks                  int32
	PartTokens                 int32
	MaxParts                   int32
	Padding                    int32
	HiddenGlobalOffsetX        int64
	HiddenGlobalOffsetY        int64
	HiddenGlobalOffsetZ        int64
}

// ReduceArgs are the arguments of pa_reduce.
type ReduceArgs struct {
	TmpOut, MaxLogits, ExpSums, SeqLens, Out driver.Ptr
	NumQHeads                                int32
	PartTokens                               int32
	MaxParts                                 int32
	Padding                                  int32
	HiddenGlobalOffsetX                      int64
	HiddenGlobalOffsetY                      int64
	HiddenGlobalOffsetZ                      int64
}

const (
	partialWGSize = 256
	reduceWGSize  = 64
)

// PrintGroups (P12, default off) prints each decode step's work list, so
// that pa_partial work-group w = item*KVHeads + kvHead can be mapped to its
// (sequence, partition): "PAGEDATTN,work,step=<s>,items=<seq>:<part>;...".
var PrintGroups bool

// Benchmark is the PagedAttention decode benchmark.
type Benchmark struct {
	driver  *driver.Driver
	context *driver.Context
	gpus    []int

	partialCo, reduceCo *insts.HsaCo

	Params Params
	// Used only for the printed expected-remote estimate.
	NumGPMs   int
	CUsPerGPM int

	verify bool
	prob   *Problem

	dQ, dK, dV, dBT, dLens, dWork driver.Ptr
	dTmp, dML, dES, dOut          driver.Ptr

	// Decode steps 1.. (Params.Steps > 1): their own lengths, work lists,
	// partial and final outputs (so no kernel reads a line that an earlier
	// step's kernel left in an L1, which the model does not invalidate).
	steps []stepBuffers
}

type stepBuffers struct {
	dLens, dWork, dTmp, dML, dES, dOut driver.Ptr
}

// DefaultParams returns a small default size (batch 2). The T17 target
// shape is batch 32 with lengths from the Azure conversation trace (mean
// about 1.26K tokens at a random point of the decode phase).
func DefaultParams() Params {
	return Params{
		Batch:      2,
		KVHeads:    8,
		PartTokens: 64,
		PoolFactor: 2.0,
		Dist:       LenDist{Mean: 1024, Sigma: 0.8, Min: 16, Max: 8192},
		LenSource:  "azure",
		Churn:      ChurnConfig{Rounds: 8, Occupancy: 0.9, FinishP: 0.5},
		Seed:       1,
	}
}

// NewBenchmark returns a PagedAttention decode benchmark.
func NewBenchmark(d *driver.Driver) *Benchmark {
	b := &Benchmark{driver: d, Params: DefaultParams(), NumGPMs: 48, CUsPerGPM: 28}
	b.context = d.Init()
	b.partialCo = kernels.LoadProgramFromMemory(hsacoBytes, "pa_partial")
	b.reduceCo = kernels.LoadProgramFromMemory(hsacoBytes, "pa_reduce")
	if b.partialCo == nil || b.reduceCo == nil {
		log.Panic("pagedattn: failed to load kernels")
	}
	return b
}

// SelectGPU selects the GPU (the runner's unified GPU).
func (b *Benchmark) SelectGPU(gpus []int) { b.gpus = gpus }

// SetUnifiedMemory is not supported.
func (b *Benchmark) SetUnifiedMemory() { panic("pagedattn does not support unified memory") }

// EnableVerification keeps host copies of K and V for Verify (the runner
// calls it before Run when -verify is set).
func (b *Benchmark) EnableVerification() { b.verify = true }

// Run generates the input, copies it and launches the two kernels.
func (b *Benchmark) Run() {
	b.driver.SelectGPU(b.context, b.gpus[0])
	// Verify copies results back; make the magic D2H copy read through the
	// write-back L2 (no simulated time; does not affect the kernels).
	b.driver.SetMagicCopyCoherent(true)
	b.prob = Generate(b.Params, b.verify)
	if err := b.prob.CheckBounds(); err != nil {
		log.Panicf("pagedattn: bounds check failed: %v", err)
	}
	b.report()
	b.initMem()
	b.exec()
}

func (b *Benchmark) alloc(bytes uint64, name string) driver.Ptr {
	if bytes == 0 {
		bytes = 4
	}
	p := b.driver.AllocateMemory(b.context, bytes)
	b.driver.RegisterMemoryObject(b.context, p, bytes, name)
	return p
}

func (b *Benchmark) initMem() {
	p := b.prob
	nq := p.QHeads()
	kvBytes := uint64(p.NumBlocks) * uint64(p.BlockFloats()) * 4
	b.dQ = b.alloc(uint64(len(p.Q))*4, "pa_q")
	b.dK = b.alloc(kvBytes, "pa_k_cache")
	b.dV = b.alloc(kvBytes, "pa_v_cache")
	b.dBT = b.alloc(uint64(len(p.BlockTable))*4, "pa_block_tables")
	b.dLens = b.alloc(uint64(p.Batch)*4, "pa_seq_lens")
	b.dWork = b.alloc(uint64(len(p.Work))*4, "pa_work")
	parts := uint64(p.Batch * nq * p.MaxParts)
	b.dTmp = b.alloc(parts*HeadDim*4, "pa_tmp_out")
	b.dML = b.alloc(parts*4, "pa_max_logits")
	b.dES = b.alloc(parts*4, "pa_exp_sums")
	b.dOut = b.alloc(uint64(p.Batch*nq*HeadDim)*4, "pa_out")

	b.driver.MemCopyH2D(b.context, b.dQ, p.Q)
	b.driver.MemCopyH2D(b.context, b.dBT, p.BlockTable)
	lens := make([]int32, p.Batch)
	for i, l := range p.Lens {
		lens[i] = int32(l)
	}
	b.driver.MemCopyH2D(b.context, b.dLens, lens)
	b.driver.MemCopyH2D(b.context, b.dWork, p.Work)
	b.steps = nil
	for st := 1; st < p.NumSteps(); st++ {
		lens, work := p.StepInput(st)
		sfx := fmt.Sprintf("_s%d", st)
		sb := stepBuffers{
			dLens: b.alloc(uint64(p.Batch)*4, "pa_seq_lens"+sfx),
			dWork: b.alloc(uint64(len(work))*4, "pa_work"+sfx),
			dTmp:  b.alloc(parts*HeadDim*4, "pa_tmp_out"+sfx),
			dML:   b.alloc(parts*4, "pa_max_logits"+sfx),
			dES:   b.alloc(parts*4, "pa_exp_sums"+sfx),
			dOut:  b.alloc(uint64(p.Batch*nq*HeadDim)*4, "pa_out"+sfx),
		}
		l32 := make([]int32, p.Batch)
		for i, l := range lens {
			l32[i] = int32(l)
		}
		b.driver.MemCopyH2D(b.context, sb.dLens, l32)
		b.driver.MemCopyH2D(b.context, sb.dWork, work)
		b.steps = append(b.steps, sb)
	}

	// Copy only the blocks in use (the rest of the pool is never read).
	// Without verification the host never holds the whole pool.
	bf := p.BlockFloats()
	kb, vb := make([]float32, bf), make([]float32, bf)
	for _, t := range p.Tables {
		for _, blk := range t {
			k, v := kb, vb
			if p.K != nil {
				k, v = p.K[blk*bf:(blk+1)*bf], p.V[blk*bf:(blk+1)*bf]
			} else {
				FillBlock(b.Params.Seed, blk, k, v)
			}
			off := driver.Ptr(uint64(blk) * uint64(bf) * 4)
			b.driver.MemCopyH2D(b.context, b.dK+off, k)
			b.driver.MemCopyH2D(b.context, b.dV+off, v)
		}
	}
}

func (b *Benchmark) exec() {
	p := b.prob
	q := b.driver.CreateCommandQueue(b.context)
	for st := 0; st < p.NumSteps(); st++ {
		sb := stepBuffers{dLens: b.dLens, dWork: b.dWork, dTmp: b.dTmp,
			dML: b.dML, dES: b.dES, dOut: b.dOut}
		if st > 0 {
			sb = b.steps[st-1]
		}
		_, work := p.StepInput(st)
		if PrintGroups {
			items := make([]string, len(work)/2)
			for i := range items {
				items[i] = fmt.Sprintf("%d:%d", work[2*i], work[2*i+1])
			}
			fmt.Printf("PAGEDATTN,work,step=%d,kv_heads=%d,items=%s\n",
				st, p.KVHeads, strings.Join(items, ";"))
		}
		if b.driver.WGSteal() != nil {
			// T19 (only when WG stealing is on): the WGs of one sequence
			// form a group (pa_partial WG w = item * KV heads + head).
			w, kv := work, p.KVHeads
			b.driver.SetWGGroupHint(b.partialCo, func(flat int) int {
				return int(w[2*(flat/kv)])
			})
		}
		b.enqueueStep(q, sb, len(work)/2)
	}
	b.driver.DrainCommandQueue(q)
}

// enqueueStep launches pa_partial (items x KV heads work-groups) and
// pa_reduce for one decode step.
func (b *Benchmark) enqueueStep(q *driver.CommandQueue, sb stepBuffers, items int) {
	p := b.prob
	pa := PartialArgs{
		Q: b.dQ, KCache: b.dK, VCache: b.dV, BlockTables: b.dBT,
		SeqLens: sb.dLens, Work: sb.dWork,
		TmpOut: sb.dTmp, MaxLogits: sb.dML, ExpSums: sb.dES,
		ScaleLog2:  float32(math.Log2E / math.Sqrt(HeadDim)),
		NumKVHeads: int32(p.KVHeads),
		MaxBlocks:  int32(p.MaxBlocks),
		PartTokens: int32(p.PartTokens),
		MaxParts:   int32(p.MaxParts),
	}
	nWG := items * p.KVHeads
	b.driver.EnqueueLaunchKernel(q, b.partialCo,
		[3]uint32{uint32(nWG * partialWGSize), 1, 1},
		[3]uint16{partialWGSize, 1, 1}, &pa)

	ra := ReduceArgs{
		TmpOut: sb.dTmp, MaxLogits: sb.dML, ExpSums: sb.dES, SeqLens: sb.dLens,
		Out:        sb.dOut,
		NumQHeads:  int32(p.QHeads()),
		PartTokens: int32(p.PartTokens),
		MaxParts:   int32(p.MaxParts),
	}
	nRed := p.Batch * p.QHeads()
	b.driver.EnqueueLaunchKernel(q, b.reduceCo,
		[3]uint32{uint32(nRed * reduceWGSize), 1, 1},
		[3]uint16{reduceWGSize, 1, 1}, &ra)
}

// Verify compares the output with the float64 host reference. The tolerance
// is 1e-4 absolute + 1e-3 relative per element (outputs are convex
// combinations of V values in [-1, 1]; fp32 accumulation over at most a few
// thousand tokens stays well below it).
func (b *Benchmark) Verify() {
	p := b.prob
	if p.NumSteps() > 1 {
		log.Fatalf("pagedattn: -verify supports one decode step only (got %d)", p.NumSteps())
	}
	got := make([]float32, p.Batch*p.QHeads()*HeadDim)
	b.driver.MemCopyD2H(b.context, got, b.dOut)
	ref := p.Reference()
	maxErr, bad := 0.0, 0
	for i := range ref {
		e := math.Abs(float64(got[i]) - float64(ref[i]))
		if e > maxErr {
			maxErr = e
		}
		if !(e <= 1e-4+1e-3*math.Abs(float64(ref[i]))) {
			if bad < 5 {
				log.Printf("pagedattn: mismatch at %d: got %g want %g", i, got[i], ref[i])
			}
			bad++
		}
	}
	fmt.Printf("PAGEDATTN,verify,elements=%d,max_abs_err=%.3g,mismatches=%d\n",
		len(ref), maxErr, bad)
	if bad > 0 {
		log.Fatalf("pagedattn: %d mismatches", bad)
	}
	log.Printf("Passed!")
}

// report prints the generated configuration, kernel/WG counts, footprint
// and the host-side estimate of the remote fraction of KV bytes.
func (b *Benchmark) report() {
	p := b.prob
	pr := b.Params
	tokens, blocks, minL, maxL := 0, 0, math.MaxInt32, 0
	for _, l := range p.Lens {
		tokens += l
		blocks += blocksFor(l)
		if l < minL {
			minL = l
		}
		if l > maxL {
			maxL = l
		}
	}
	nWG := p.NumWork() * p.KVHeads
	nRed := p.Batch * p.QHeads()
	fp := p.Footprint()
	fmt.Printf("PAGEDATTN,config,batch=%d,q_heads=%d,kv_heads=%d,head_dim=%d,block=%d,"+
		"part_tokens=%d,lengths=%s,pool_factor=%g,churn_rounds=%d,seed=%d\n",
		p.Batch, p.QHeads(), p.KVHeads, HeadDim, BlockSize, p.PartTokens,
		pr.Source().Describe(), pr.PoolFactor, pr.Churn.Rounds, pr.Seed)
	fmt.Printf("PAGEDATTN,lens,tokens=%d,mean=%.1f,min=%d,max=%d,blocks=%d,pool_blocks=%d,"+
		"max_parts=%d\n",
		tokens, float64(tokens)/float64(p.Batch), minL, maxL, blocks, p.NumBlocks, p.MaxParts)
	fmt.Printf("PAGEDATTN,kernels,count=2,pa_partial_wgs=%d,pa_reduce_wgs=%d,total_wgs=%d\n",
		nWG, nRed, nWG+nRed)
	fmt.Printf("PAGEDATTN,footprint,kv_bytes=%d,q_bytes=%d,index_bytes=%d,partial_bytes=%d,"+
		"out_bytes=%d,total_bytes=%d,total_mb=%.1f,kv_pool_alloc_mb=%.1f\n",
		fp.KV, fp.Q, fp.Index, fp.Partial, fp.Out, fp.Total(), float64(fp.Total())/1e6,
		float64(2*p.NumBlocks*p.BlockFloats()*4)/1e6)
	if p.NumSteps() > 1 {
		last, lw := p.StepInput(p.NumSteps() - 1)
		lastTok, lastWG := 0, len(lw)/2*p.KVHeads
		for _, l := range last {
			lastTok += l
		}
		fmt.Printf("PAGEDATTN,steps,steps=%d,last_tokens=%d,last_pa_partial_wgs=%d,"+
			"max_blocks=%d,max_parts=%d\n", p.NumSteps(), lastTok, lastWG, p.MaxBlocks, p.MaxParts)
	}
	if b.NumGPMs > 0 && b.CUsPerGPM > 0 {
		fmt.Printf("PAGEDATTN,estimate,kv_remote_fraction=%.3f,active_gpms_partial=%d\n",
			b.expectedKVRemote(), activeGPMs(nWG, b.NumGPMs, b.CUsPerGPM))
	}
}

// wgGPM mirrors driver.distributeWGToGPUs: contiguous ranges of
// CUsPerGPM*ceil(n/totalCUs) work-groups per GPM.
func wgGPM(wg, n, gpms, cus int) int {
	per := cus * ((n-1)/(gpms*cus) + 1)
	g := wg / per
	if g >= gpms {
		g = gpms - 1
	}
	return g
}

func activeGPMs(n, gpms, cus int) int {
	per := cus * ((n-1)/(gpms*cus) + 1)
	a := (n + per - 1) / per
	if a > gpms {
		a = gpms
	}
	return a
}

// pageGPM mirrors the default unified allocator: a buffer of numPages pages
// is split into gpms contiguous parts of numPages/gpms pages (the last part
// takes the remainder; buffers under gpms pages live on the first GPM).
func pageGPM(byteOff, bufBytes uint64, gpms int) int {
	const page = 4096
	numPages := (bufBytes + page - 1) / page
	per := numPages / uint64(gpms)
	if per == 0 {
		return 0
	}
	g := int(byteOff / page / per)
	if g >= gpms {
		g = gpms - 1
	}
	return g
}

// expectedKVRemote returns the fraction of K/V bytes whose page lives on a
// GPM other than the one running the reading pa_partial work-group.
func (b *Benchmark) expectedKVRemote() float64 {
	p := b.prob
	nWG := p.NumWork() * p.KVHeads
	bufBytes := uint64(p.NumBlocks) * uint64(p.BlockFloats()) * 4
	headBytes := uint64(BlockSize * HeadDim * 4)
	var remote, total float64
	for wg := 0; wg < nWG; wg++ {
		item, kvh := wg/p.KVHeads, wg%p.KVHeads
		s, part := int(p.Work[2*item]), int(p.Work[2*item+1])
		g := wgGPM(wg, nWG, b.NumGPMs, b.CUsPerGPM)
		ntok := p.Lens[s] - part*p.PartTokens
		if ntok > p.PartTokens {
			ntok = p.PartTokens
		}
		blk0 := part * p.PartTokens / BlockSize
		for j := 0; j < blocksFor(ntok); j++ {
			phys := uint64(p.Tables[s][blk0+j])
			off := (phys*uint64(p.KVHeads) + uint64(kvh)) * headBytes
			nt := ntok - j*BlockSize
			if nt > BlockSize {
				nt = BlockSize
			}
			bytes := float64(nt * HeadDim * 4 * 2)
			total += bytes
			if pageGPM(off, bufBytes, b.NumGPMs) != g {
				remote += bytes
			}
		}
	}
	if total == 0 {
		return 0
	}
	return remote / total
}
