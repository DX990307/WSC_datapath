package shocsort

import (
	"fmt"
	"math/rand"
	"sort"
)

// Fixed CTA sizes of SHOC sort.
const (
	SortBlockSize = 128 // radixSortBlocks, 4 keys per thread
	ScanBlockSize = 256 // findRadixOffsets, scan, vectorAddUniform4, reorderData
	SortBits      = 32
	RadixBits     = 4
)

// Params is the input size.
type Params struct {
	N    int // key/value pairs, multiple of 1024
	Seed int64
}

// Validate checks the size rules of the SHOC pipeline.
func (p Params) Validate() error {
	if p.N < 1024 || p.N%1024 != 0 {
		return fmt.Errorf("shocsort: size %d must be a positive multiple of 1024", p.N)
	}
	return nil
}

// Generate returns uniform random 32-bit keys (as upstream gpu_benchmarks;
// SHOC itself uses i %% 1024) and values = source index.
func Generate(p Params) (keys, vals []uint32) {
	rng := rand.New(rand.NewSource(p.Seed))
	keys = make([]uint32, p.N)
	vals = make([]uint32, p.N)
	for i := range keys {
		keys[i] = rng.Uint32()
		vals[i] = uint32(i)
	}
	return keys, vals
}

// ScanNumBlocks is the number of scan WGs for n elements (4 per thread).
func ScanNumBlocks(n int) int {
	nb := (n + 4*ScanBlockSize - 1) / (4 * ScanBlockSize)
	if nb < 1 {
		nb = 1
	}
	return nb
}

// ScanLevels returns the block-sum buffer sizes of the recursive scan of n
// elements (Sort.cu): one entry per level with more than one block, then a
// final one-element buffer.
func ScanLevels(n int) []int {
	var sizes []int
	for {
		nb := ScanNumBlocks(n)
		if nb > 1 {
			sizes = append(sizes, nb)
		}
		n = nb
		if n <= 1 {
			break
		}
	}
	return append(sizes, 1)
}

// Reference returns the stable sort of the pairs by key (the LSD radix sort
// is stable, so the GPU result must equal it exactly).
func Reference(keys, vals []uint32) (sk, sv []uint32) {
	idx := make([]int, len(keys))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool { return keys[idx[a]] < keys[idx[b]] })
	sk = make([]uint32, len(keys))
	sv = make([]uint32, len(keys))
	for i, j := range idx {
		sk[i], sv[i] = keys[j], vals[j]
	}
	return sk, sv
}

// KernelWGs returns the WG counts of one radix pass: radixSortBlocks,
// findRadixOffsets, reorderData, and the scan launches (scan + vectorAdd).
func KernelWGs(n int) (radix, find, reorder, scanWGs, scanLaunches int) {
	radix = n / 4 / SortBlockSize
	find = n / 2 / ScanBlockSize
	reorder = find
	m := 16 * reorder
	for {
		nb := ScanNumBlocks(m)
		scanWGs += nb
		scanLaunches++
		if nb <= 1 {
			break
		}
		scanWGs += nb // vectorAddUniform4
		scanLaunches++
		m = nb
	}
	return
}
