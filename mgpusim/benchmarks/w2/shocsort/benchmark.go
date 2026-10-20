// Package shocsort is the SHOC Sort benchmark (CUDPP 4-bit LSD radix sort of
// 32-bit key/value pairs, 8 passes of 4 bits), ported from gpu_benchmarks
// tier2/shoc_sort (verbatim SHOC sort_kernel.cu kernels) to OpenCL for the
// CuPath wafer (W2 workload set). Each pass: radixSortBlocks (block-local
// sort in LDS) -> findRadixOffsets (per-block digit counts) -> recursive
// block-sum scan of the 16 x blocks counters -> reorderData (global
// all-to-all scatter). No atomics upstream. Deviations: see kernels.cl (LDS
// layout of reorderData, digit mask as an argument, int scan flags).
package shocsort

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

// RadixArgs are the arguments of radixSortBlocks.
type RadixArgs struct {
	KeysOut, ValuesOut, KeysIn, ValuesIn driver.Ptr
	NBits                                int32
	StartBit                             uint32
	HiddenArgs
}

// FindArgs are the arguments of findRadixOffsets.
type FindArgs struct {
	Keys, Counters, BlockOffsets     driver.Ptr
	StartBit, TotalBlocks, RadixMask uint32
	Pad                              int32
	HiddenArgs
}

// ReorderArgs are the arguments of reorderData.
type ReorderArgs struct {
	OutKeys, OutValues, Keys, Values, BlockOffsets, Offsets driver.Ptr
	StartBit, TotalBlocks, RadixMask                        uint32
	Pad                                                     int32
	HiddenArgs
}

// ScanArgs are the arguments of scan.
type ScanArgs struct {
	Out, In, BlockSums          driver.Ptr
	N, FullBlock, StoreSum, Pad int32
	HiddenArgs
}

// VAddArgs are the arguments of vectorAddUniform4.
type VAddArgs struct {
	Vector, Uniforms driver.Ptr
	N, Pad           int32
	HiddenArgs
}

// Benchmark is the SHOC radix sort.
type Benchmark struct {
	driver  *driver.Driver
	context *driver.Context
	gpus    []int

	radix, find, reorder, scan, vadd *insts.HsaCo

	Params Params

	keys, vals                          []uint32
	dKeys, dVals, dTempKeys, dTempVals  driver.Ptr
	dCounters, dCounterSums, dBlockOffs driver.Ptr
	dScanSums                           []driver.Ptr
}

// DefaultParams is a small smoke size (upstream default 4M pairs).
func DefaultParams() Params { return Params{N: 65536, Seed: 42} }

// NewBenchmark returns the benchmark.
func NewBenchmark(d *driver.Driver) *Benchmark {
	b := &Benchmark{driver: d, Params: DefaultParams()}
	b.context = d.Init()
	b.radix = kernels.LoadProgramFromMemory(hsacoBytes, "radixSortBlocks")
	b.find = kernels.LoadProgramFromMemory(hsacoBytes, "findRadixOffsets")
	b.reorder = kernels.LoadProgramFromMemory(hsacoBytes, "reorderData")
	b.scan = kernels.LoadProgramFromMemory(hsacoBytes, "scan")
	b.vadd = kernels.LoadProgramFromMemory(hsacoBytes, "vectorAddUniform4")
	for _, co := range []*insts.HsaCo{b.radix, b.find, b.reorder, b.scan, b.vadd} {
		if co == nil {
			log.Panic("shocsort: failed to load kernels")
		}
	}
	return b
}

// SelectGPU selects the GPU.
func (b *Benchmark) SelectGPU(gpus []int) { b.gpus = gpus }

// SetUnifiedMemory is not supported.
func (b *Benchmark) SetUnifiedMemory() { panic("shocsort does not support unified memory") }

func (b *Benchmark) alloc(bytes uint64, name string) driver.Ptr {
	p := b.driver.AllocateMemory(b.context, bytes)
	b.driver.RegisterMemoryObject(b.context, p, bytes, name)
	return p
}

// Run sorts the pairs (8 radix passes).
func (b *Benchmark) Run() {
	if err := b.Params.Validate(); err != nil {
		log.Panic(err)
	}
	b.driver.SelectGPU(b.context, b.gpus[0])
	n := b.Params.N
	b.keys, b.vals = Generate(b.Params)
	bytes := uint64(n) * 4
	b.dKeys = b.alloc(bytes, "sort_keys")
	b.dVals = b.alloc(bytes, "sort_vals")
	b.dTempKeys = b.alloc(bytes, "sort_temp_keys")
	b.dTempVals = b.alloc(bytes, "sort_temp_vals")
	numSortGroups := uint64(n / (4 * SortBlockSize))
	b.dCounters = b.alloc(32*numSortGroups*4, "sort_counters")
	b.dCounterSums = b.alloc(32*numSortGroups*4, "sort_counter_sums")
	b.dBlockOffs = b.alloc(32*numSortGroups*4, "sort_block_offsets")
	b.dScanSums = nil
	for i, s := range ScanLevels(n) {
		b.dScanSums = append(b.dScanSums, b.alloc(uint64(s)*4, fmt.Sprintf("sort_scan_sums%d", i)))
	}
	b.driver.MemCopyH2D(b.context, b.dKeys, b.keys)
	b.driver.MemCopyH2D(b.context, b.dVals, b.vals)

	radixWG, findWG, reorderWG, scanWG, scanL := KernelWGs(n)
	fmt.Printf("SHOCSORT,config,n=%d,passes=%d,radix_wgs=%d,find_wgs=%d,reorder_wgs=%d,"+
		"scan_wgs=%d,scan_launches=%d,kernels=%d,total_wgs=%d,footprint_mb=%.1f\n",
		n, SortBits/RadixBits, radixWG, findWG, reorderWG, scanWG, scanL,
		(3+scanL)*SortBits/RadixBits, (radixWG+findWG+reorderWG+scanWG)*SortBits/RadixBits,
		float64(4*bytes+3*32*numSortGroups*4)/1e6)

	q := b.driver.CreateCommandQueue(b.context)
	for bit := 0; bit < SortBits; bit += RadixBits {
		b.step(q, uint32(bit))
	}
	b.driver.DrainCommandQueue(q)
}

func (b *Benchmark) step(q *driver.CommandQueue, startbit uint32) {
	n := b.Params.N
	radixBlocks := n / 4 / SortBlockSize
	findBlocks := n / 2 / ScanBlockSize
	reorderBlocks := findBlocks

	ra := RadixArgs{KeysOut: b.dTempKeys, ValuesOut: b.dTempVals, KeysIn: b.dKeys,
		ValuesIn: b.dVals, NBits: RadixBits, StartBit: startbit}
	b.launch(q, b.radix, radixBlocks, SortBlockSize, &ra)
	fa := FindArgs{Keys: b.dTempKeys, Counters: b.dCounters, BlockOffsets: b.dBlockOffs,
		StartBit: startbit, TotalBlocks: uint32(findBlocks), RadixMask: 0xF}
	b.launch(q, b.find, findBlocks, ScanBlockSize, &fa)
	b.scanRecursive(q, b.dCounterSums, b.dCounters, 16*reorderBlocks, 0)
	oa := ReorderArgs{OutKeys: b.dKeys, OutValues: b.dVals, Keys: b.dTempKeys,
		Values: b.dTempVals, BlockOffsets: b.dBlockOffs, Offsets: b.dCounterSums,
		StartBit: startbit, TotalBlocks: uint32(reorderBlocks), RadixMask: 0xF}
	b.launch(q, b.reorder, reorderBlocks, ScanBlockSize, &oa)
}

func (b *Benchmark) scanRecursive(q *driver.CommandQueue, out, in driver.Ptr, n, level int) {
	nb := ScanNumBlocks(n)
	full := int32(0)
	if n == nb*4*ScanBlockSize {
		full = 1
	}
	store := int32(0)
	if nb > 1 {
		store = 1
	}
	sa := ScanArgs{Out: out, In: in, BlockSums: b.dScanSums[level], N: int32(n),
		FullBlock: full, StoreSum: store}
	b.launch(q, b.scan, nb, ScanBlockSize, &sa)
	if nb > 1 {
		b.scanRecursive(q, b.dScanSums[level], b.dScanSums[level], nb, level+1)
		va := VAddArgs{Vector: out, Uniforms: b.dScanSums[level], N: int32(n)}
		b.launch(q, b.vadd, nb, ScanBlockSize, &va)
	}
}

func (b *Benchmark) launch(q *driver.CommandQueue, co *insts.HsaCo, wgs, wgSize int, args interface{}) {
	b.driver.EnqueueLaunchKernel(q, co, [3]uint32{uint32(wgs * wgSize), 1, 1},
		[3]uint16{uint16(wgSize), 1, 1}, args)
}

// Verify compares the sorted pairs with a stable host sort (exact).
func (b *Benchmark) Verify() {
	b.driver.SetMagicCopyCoherent(true)
	n := b.Params.N
	gk := make([]uint32, n)
	gv := make([]uint32, n)
	b.driver.MemCopyD2H(b.context, gk, b.dKeys)
	b.driver.MemCopyD2H(b.context, gv, b.dVals)
	rk, rv := Reference(b.keys, b.vals)
	bad := 0
	for i := range rk {
		if gk[i] != rk[i] || gv[i] != rv[i] {
			if bad < 5 {
				log.Printf("shocsort: mismatch at %d: got (%d,%d) want (%d,%d)", i, gk[i], gv[i], rk[i], rv[i])
			}
			bad++
		}
	}
	fmt.Printf("SHOCSORT,verify,n=%d,mismatches=%d\n", n, bad)
	if bad > 0 {
		log.Fatalf("shocsort: verification failed")
	}
	log.Printf("Passed!")
}
