// Package moe implements the T17 W-B workload: one MoE layer of routed
// experts in a decode batch, shaped like DeepSeekMoE 16B scaled down
// (default hidden 1024, expert dimension 704, 64 routed experts, top-6,
// 256 tokens; no shared expert).
//
// Kernels (no atomics, pull only, kernels.cl):
//
//	moe_gate     WG per token: gate GEMV, softmax, top-k (prob + bias) in LDS
//	(host)       the gate's top-k is copied back (coherent magic copy, not
//	             timed), grouped by expert on the host (permutation, inverse,
//	             GEMM row tiles; cross-checked against a float64 host gate)
//	             and copied to the GPU
//	moe_dispatch WG per slot: gather x rows into expert order
//	moe_up       WG per (row tile, 64 columns of F): SwiGLU(xs W1, xs W3)
//	moe_down     WG per (row tile, 64 columns of H): act W2
//	moe_combine  WG per token: pull its top-k expert outputs, weighted sum
//
// Expert load skew: a per-expert bias z_e * k (z_e ~ N(0,1) from the seed)
// is added to the routing score only (DeepSeek-V3 style bias, combine
// weights stay the softmax probabilities); k is calibrated on the host so
// the max/mean expert load reaches Params.Skew (0 = no bias).
package moe

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

// PrintGroups (P12, default off) prints the GEMM row tiles, so that the
// moe_up / moe_down work-group w (tile w / n-tiles, column block w % n-tiles)
// can be mapped to its expert: "MOE,tiles,<expert>:<first slot>:<rows>;...".
var PrintGroups bool

// HiddenArgs are the hidden kernel arguments (exported: the driver
// serialises the argument structs by reflection).
type HiddenArgs struct {
	HiddenGlobalOffsetX int64
	HiddenGlobalOffsetY int64
	HiddenGlobalOffsetZ int64
}

// GateArgs are the arguments of moe_gate.
type GateArgs struct {
	X, Wg, Bias, TopKIdx, TopKW driver.Ptr
	Hidden, TopK                int32
	HiddenArgs
}

// DispatchArgs are the arguments of moe_dispatch.
type DispatchArgs struct {
	X, PermToken, Xs driver.Ptr
	Hidden, Pad      int32
	HiddenArgs
}

// UpArgs are the arguments of moe_up.
type UpArgs struct {
	Xs, W1, W3, Tiles, Act    driver.Ptr
	Hidden, FFN, NTilesN, Pad int32
	HiddenArgs
}

// DownArgs are the arguments of moe_down.
type DownArgs struct {
	Act, W2, Tiles, Ys        driver.Ptr
	Hidden, FFN, NTilesN, Pad int32
	HiddenArgs
}

// CombineArgs are the arguments of moe_combine.
type CombineArgs struct {
	Ys, InvSlot, TopKW, Out driver.Ptr
	Hidden, TopK            int32
	HiddenArgs
}

const wgSize = 256

// Benchmark is the MoE layer benchmark.
type Benchmark struct {
	driver  *driver.Driver
	context *driver.Context
	gpus    []int

	gate, dispatch, up, down, combine *insts.HsaCo

	Params    Params
	NumGPMs   int
	CUsPerGPM int

	verify   bool
	prob     *Problem
	routing  *Routing
	hostTopK []int32
	gpuTopK  []int32
	gpuTopW  []float32
	selDiff  int

	dX, dWg, dBias, dIdx, dW, dW1, dW3, dW2   driver.Ptr
	dPerm, dInv, dTiles, dXs, dAct, dYs, dOut driver.Ptr
}

// DefaultParams returns a small default size; the T17 target shape is
// Tokens 256, Hidden 1024, FFN 704, TopK 6.
func DefaultParams() Params {
	return Params{Tokens: 32, Hidden: 256, FFN: 192, TopK: 6, Skew: 1.75, Seed: 1}
}

// NewBenchmark returns an MoE benchmark.
func NewBenchmark(d *driver.Driver) *Benchmark {
	b := &Benchmark{driver: d, Params: DefaultParams(), NumGPMs: 48, CUsPerGPM: 28}
	b.context = d.Init()
	b.gate = kernels.LoadProgramFromMemory(hsacoBytes, "moe_gate")
	b.dispatch = kernels.LoadProgramFromMemory(hsacoBytes, "moe_dispatch")
	b.up = kernels.LoadProgramFromMemory(hsacoBytes, "moe_up")
	b.down = kernels.LoadProgramFromMemory(hsacoBytes, "moe_down")
	b.combine = kernels.LoadProgramFromMemory(hsacoBytes, "moe_combine")
	for _, co := range []*insts.HsaCo{b.gate, b.dispatch, b.up, b.down, b.combine} {
		if co == nil {
			log.Panic("moe: failed to load kernels")
		}
	}
	return b
}

// SelectGPU selects the GPU (the runner's unified GPU).
func (b *Benchmark) SelectGPU(gpus []int) { b.gpus = gpus }

// SetUnifiedMemory is not supported.
func (b *Benchmark) SetUnifiedMemory() { panic("moe does not support unified memory") }

// EnableVerification makes Run keep what Verify needs.
func (b *Benchmark) EnableVerification() { b.verify = true }

func (b *Benchmark) alloc(bytes uint64, name string) driver.Ptr {
	p := b.driver.AllocateMemory(b.context, bytes)
	b.driver.RegisterMemoryObject(b.context, p, bytes, name)
	return p
}

// Run launches the five kernels with the host routing step in between.
func (b *Benchmark) Run() {
	b.driver.SelectGPU(b.context, b.gpus[0])
	b.prob = Generate(b.Params)
	b.initMem()
	q := b.driver.CreateCommandQueue(b.context)
	p := b.prob
	T, K, H, F := p.Tokens, p.TopK, p.Hidden, p.FFN
	S := T * K

	// The gate output is copied back, grouped by expert on the host and the
	// index arrays copied to the GPU (magic copies, no simulated time). The
	// D2H copy must be coherent: the gate output is still dirty in the
	// write-back L2, and a plain magic copy reads DRAM only.
	b.driver.SetMagicCopyCoherent(true)
	ga := GateArgs{X: b.dX, Wg: b.dWg, Bias: b.dBias, TopKIdx: b.dIdx, TopKW: b.dW,
		Hidden: int32(H), TopK: int32(K)}
	b.launch(q, b.gate, T, &ga)
	b.driver.DrainCommandQueue(q)

	b.gpuTopK = make([]int32, S)
	b.gpuTopW = make([]float32, S)
	b.driver.MemCopyD2H(b.context, b.gpuTopK, b.dIdx)
	b.driver.MemCopyD2H(b.context, b.gpuTopW, b.dW)
	// Cross-check against the float64 host gate (near-ties may differ).
	b.hostTopK = p.HostTopK()
	b.selDiff = 0
	for i := range b.hostTopK {
		if b.gpuTopK[i] != b.hostTopK[i] {
			b.selDiff++
		}
	}
	r, err := BuildRouting(b.gpuTopK, T, K)
	if err == nil {
		err = r.Check(T, K)
	}
	if err != nil {
		log.Panicf("moe: routing from the GPU gate output: %v", err)
	}
	b.routing = r
	b.driver.MemCopyH2D(b.context, b.dPerm, r.PermToken)
	b.driver.MemCopyH2D(b.context, b.dInv, r.InvSlot)
	b.driver.MemCopyH2D(b.context, b.dTiles, r.Tiles)
	b.report()
	if PrintGroups {
		tiles := make([]string, r.NumTiles())
		for i := range tiles {
			tiles[i] = fmt.Sprintf("%d:%d:%d", r.Tiles[3*i], r.Tiles[3*i+1], r.Tiles[3*i+2])
		}
		fmt.Printf("MOE,tiles,up_ntiles=%d,down_ntiles=%d,tiles=%s\n",
			F/TileN, H/TileN, strings.Join(tiles, ";"))
	}
	fmt.Printf("MOE,gate_check,host_vs_gpu_topk_diffs=%d,of=%d\n", b.selDiff, S)

	da := DispatchArgs{X: b.dX, PermToken: b.dPerm, Xs: b.dXs, Hidden: int32(H)}
	b.launch(q, b.dispatch, S, &da)
	ua := UpArgs{Xs: b.dXs, W1: b.dW1, W3: b.dW3, Tiles: b.dTiles, Act: b.dAct,
		Hidden: int32(H), FFN: int32(F), NTilesN: int32(F / TileN)}
	b.launch(q, b.up, r.NumTiles()*(F/TileN), &ua)
	dn := DownArgs{Act: b.dAct, W2: b.dW2, Tiles: b.dTiles, Ys: b.dYs,
		Hidden: int32(H), FFN: int32(F), NTilesN: int32(H / TileN)}
	b.launch(q, b.down, r.NumTiles()*(H/TileN), &dn)
	ca := CombineArgs{Ys: b.dYs, InvSlot: b.dInv, TopKW: b.dW, Out: b.dOut,
		Hidden: int32(H), TopK: int32(K)}
	b.launch(q, b.combine, T, &ca)
	b.driver.DrainCommandQueue(q)
}

func (b *Benchmark) launch(q *driver.CommandQueue, co *insts.HsaCo, wgs int, args interface{}) {
	b.driver.EnqueueLaunchKernel(q, co, [3]uint32{uint32(wgs * wgSize), 1, 1},
		[3]uint16{wgSize, 1, 1}, args)
}

func (b *Benchmark) initMem() {
	p := b.prob
	T, K, H, F := p.Tokens, p.TopK, p.Hidden, p.FFN
	S := T * K
	b.dX = b.alloc(uint64(T*H*4), "moe_x")
	b.dWg = b.alloc(uint64(H*NumExperts*4), "moe_wg")
	b.dBias = b.alloc(NumExperts*4, "moe_bias")
	b.dIdx = b.alloc(uint64(S*4), "moe_topk_idx")
	b.dW = b.alloc(uint64(S*4), "moe_topk_w")
	wBytes := uint64(NumExperts) * uint64(H*F) * 4
	b.dW1 = b.alloc(wBytes, "moe_w1")
	b.dW3 = b.alloc(wBytes, "moe_w3")
	b.dW2 = b.alloc(wBytes, "moe_w2")
	b.dPerm = b.alloc(uint64(S*4), "moe_perm_token")
	b.dInv = b.alloc(uint64(S*4), "moe_inv_slot")
	b.dTiles = b.alloc(uint64(MaxTiles(S)*3*4), "moe_tiles")
	b.dXs = b.alloc(uint64(S*H*4), "moe_xs")
	b.dAct = b.alloc(uint64(S*F*4), "moe_act")
	b.dYs = b.alloc(uint64(S*H*4), "moe_ys")
	b.dOut = b.alloc(uint64(T*H*4), "moe_out")

	b.driver.MemCopyH2D(b.context, b.dX, p.X)
	b.driver.MemCopyH2D(b.context, b.dWg, p.Wg)
	b.driver.MemCopyH2D(b.context, b.dBias, p.Bias)
	per := uint64(H*F) * 4
	for e := 0; e < NumExperts; e++ {
		w1, w3, w2 := ExpertWeights(p.Seed, e, H, F)
		off := driver.Ptr(uint64(e) * per)
		b.driver.MemCopyH2D(b.context, b.dW1+off, w1)
		b.driver.MemCopyH2D(b.context, b.dW3+off, w3)
		b.driver.MemCopyH2D(b.context, b.dW2+off, w2)
	}
}

// Verify checks the gate kernel against the float64 host gate (same top-k,
// weights within 1e-5) and the layer output against the float64 reference
// computed with the GPU's routing. Tolerance: 1e-4 absolute + 1e-3
// relative. In timing mode it relies on the coherent magic D2H copy.
func (b *Benchmark) Verify() {
	p := b.prob
	K := p.TopK
	probs := p.GateProbs()
	selDiff, wErr := b.selDiff, 0.0
	for i := range b.gpuTopK {
		wErr = math.Max(wErr, math.Abs(float64(b.gpuTopW[i])-probs[i/K][b.gpuTopK[i]]))
	}
	got := make([]float32, p.Tokens*p.Hidden)
	b.driver.MemCopyD2H(b.context, got, b.dOut)
	ref := p.Reference(b.gpuTopK, b.gpuTopW)
	maxErr, bad := 0.0, 0
	for i := range ref {
		e := math.Abs(float64(got[i]) - float64(ref[i]))
		maxErr = math.Max(maxErr, e)
		if !(e <= 1e-4+1e-3*math.Abs(float64(ref[i]))) {
			if bad < 5 {
				log.Printf("moe: mismatch at %d: got %g want %g", i, got[i], ref[i])
			}
			bad++
		}
	}
	fmt.Printf("MOE,verify,gate_selection_diffs=%d,gate_weight_max_err=%.3g,"+
		"elements=%d,max_abs_err=%.3g,mismatches=%d\n",
		selDiff, wErr, len(ref), maxErr, bad)
	if bad > 0 || selDiff > 0 || wErr > 1e-5 {
		log.Fatalf("moe: verification failed")
	}
	log.Printf("Passed!")
}

func (b *Benchmark) report() {
	p, r := b.prob, b.routing
	T, K, H, F := p.Tokens, p.TopK, p.Hidden, p.FFN
	S := T * K
	active, minL, maxL := 0, S, 0
	for _, l := range r.Loads {
		if l > 0 {
			active++
		}
		minL = int(math.Min(float64(minL), float64(l)))
		maxL = int(math.Max(float64(maxL), float64(l)))
	}
	fmt.Printf("MOE,config,tokens=%d,hidden=%d,ffn=%d,experts=%d,topk=%d,skew_target=%g,"+
		"bias_scale=%.4g,seed=%d\n", T, H, F, NumExperts, K, p.Skew, p.BiasK, p.Seed)
	fmt.Printf("MOE,loads,max_over_mean=%.3f,max=%d,min=%d,mean=%.2f,active_experts=%d,tiles=%d\n",
		MaxOverMean(r.Loads), maxL, minL, float64(S)/NumExperts, active, r.NumTiles())
	nUp, nDown := r.NumTiles()*(F/TileN), r.NumTiles()*(H/TileN)
	fmt.Printf("MOE,kernels,count=5,gate_wgs=%d,dispatch_wgs=%d,up_wgs=%d,down_wgs=%d,"+
		"combine_wgs=%d,total_wgs=%d\n", T, S, nUp, nDown, T, T+S+nUp+nDown+T)
	weights := uint64(active) * uint64(H*F*3) * 4
	acts := uint64(S*H*4)*2*2 + uint64(S*F*4)*2 + uint64(T*H*4)*2
	gateB := uint64(H*NumExperts*4) + uint64(T*H*4)
	fmt.Printf("MOE,footprint,expert_weight_bytes=%d,activation_bytes=%d,gate_bytes=%d,"+
		"total_mb=%.1f\n", weights, acts, gateB, float64(weights+acts+gateB)/1e6)
	if b.NumGPMs > 0 && b.CUsPerGPM > 0 {
		fmt.Printf("MOE,estimate,up_weight_remote_fraction=%.3f,down_weight_remote_fraction=%.3f\n",
			b.weightRemote(F, nUp, false), b.weightRemote(H, nDown, true))
	}
}

// weightRemote estimates the fraction of expert-weight bytes read by
// moe_up (W1 and W3 [E][H][F]) or moe_down (W2 [E][F][H]) from a page on a
// GPM other than the reading WG's (default allocator, contiguous WG ranges).
func (b *Benchmark) weightRemote(ncols, nWG int, down bool) float64 {
	p, r := b.prob, b.routing
	H, F := p.Hidden, p.FFN
	rows, ld := H, F
	if down {
		rows, ld = F, H
	}
	bufBytes := uint64(NumExperts) * uint64(H*F) * 4
	ntn := ncols / TileN
	per := b.CUsPerGPM * ((nWG-1)/(b.NumGPMs*b.CUsPerGPM) + 1)
	pagesPerGPM := (bufBytes + 4095) / 4096 / uint64(b.NumGPMs)
	var remote, total float64
	for wg := 0; wg < nWG; wg++ {
		g := wg / per
		if g >= b.NumGPMs {
			g = b.NumGPMs - 1
		}
		e := uint64(r.Tiles[3*(wg/ntn)])
		n0 := uint64((wg % ntn) * TileN)
		for k := 0; k < rows; k++ {
			off := (e*uint64(H*F) + uint64(k)*uint64(ld) + n0) * 4
			owner := 0
			if pagesPerGPM > 0 {
				owner = int(off / 4096 / pagesPerGPM)
				if owner >= b.NumGPMs {
					owner = b.NumGPMs - 1
				}
			}
			total += TileN * 4
			if owner != g {
				remote += TileN * 4
			}
		}
	}
	return remote / total
}
