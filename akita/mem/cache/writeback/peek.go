package writeback

import (
	"github.com/sarchlab/akita/v3/mem/cache"
	"github.com/sarchlab/akita/v3/mem/mem"
)

// PeekDirty overlays onto buf, which holds the memory contents of
// [addr, addr+len(buf)), every byte this cache holds that is newer than
// memory: first the evictions still on their way to memory (in flight, then
// pending), then the dirty bytes of valid blocks. It is functional only: it
// changes no cache state and takes no simulated time. The driver uses it
// for coherent magic D2H copies.
//
// addr is a physical address. Lines are matched by address alone, not by
// PID: the L2 sits below the address translator, which issues the
// translated (physical) requests with PID 0, so every L2 block and eviction
// carries PID 0 whatever process wrote it; a physical frame belongs to one
// process, so the address identifies the data.
func (c *Cache) PeekDirty(addr uint64, buf []byte) {
	if len(buf) == 0 {
		return
	}

	blockSize := uint64(1) << c.log2BlockSize
	end := addr + uint64(len(buf))

	overlay := func(lineAddr uint64, data []byte, mask []bool) {
		for i := uint64(0); i < blockSize && i < uint64(len(data)); i++ {
			a := lineAddr + i
			if a < addr || a >= end {
				continue
			}
			if mask != nil && (int(i) >= len(mask) || !mask[i]) {
				continue
			}
			buf[a-addr] = data[i]
		}
	}

	inRange := func(lineAddr uint64) bool {
		return lineAddr < end && lineAddr+blockSize > addr
	}

	if c.writeBuffer != nil {
		for _, list := range [][]*transaction{
			c.writeBuffer.inflightEviction,
			c.writeBuffer.pendingEvictions,
		} {
			for _, t := range list {
				if inRange(t.evictingAddr) {
					overlay(t.evictingAddr, t.evictingData, t.evictingDirtyMask)
				}
			}
		}
	}

	first := addr &^ (blockSize - 1)
	for line := first; line < end; line += blockSize {
		if !c.mayHold(line) {
			continue
		}
		for _, block := range c.peekSet(line) {
			if !block.IsValid || !block.IsDirty || block.Tag != line {
				continue
			}
			data, err := c.storage.Read(block.CacheAddress, blockSize)
			if err != nil {
				panic(err)
			}
			overlay(line, data, block.DirtyMask)
		}
	}
}

// peekSet returns the blocks of the set the line maps to (all blocks for a
// directory whose set mapping is unknown), without touching LRU state.
func (c *Cache) peekSet(line uint64) []*cache.Block {
	d, ok := c.directory.(*cache.DirectoryImpl)
	if !ok {
		var all []*cache.Block
		for _, set := range c.directory.GetSets() {
			all = append(all, set.Blocks...)
		}
		return all
	}
	internal := line
	if d.AddrConverter != nil {
		internal = d.AddrConverter.ConvertExternalToInternal(line)
	}
	setID := int(internal / uint64(d.BlockSize) % uint64(d.NumSets))
	return d.Sets[setID].Blocks
}

// mayHold reports whether the address belongs to this cache's slice of an
// interleaved address space (the directory's address converter panics on
// addresses of other slices).
func (c *Cache) mayHold(addr uint64) bool {
	d, ok := c.directory.(*cache.DirectoryImpl)
	if !ok || d.AddrConverter == nil {
		return true
	}
	var conv mem.InterleavingConverter
	switch v := d.AddrConverter.(type) {
	case mem.InterleavingConverter:
		conv = v
	case *mem.InterleavingConverter:
		conv = *v
	default:
		return true
	}
	if addr < conv.Offset || conv.InterleavingSize == 0 {
		return false
	}
	a := addr - conv.Offset
	round := conv.InterleavingSize * uint64(conv.TotalNumOfElements)
	return int(a%round/conv.InterleavingSize) == conv.CurrentElementIndex
}
