package writeback

import (
	"testing"

	"github.com/sarchlab/akita/v3/mem/cache"
	"github.com/sarchlab/akita/v3/mem/mem"
	"github.com/sarchlab/akita/v3/sim"
)

func TestPeekDirty(t *testing.T) {
	dir := cache.NewDirectory(4, 2, 64, cache.NewLRUVictimFinder())
	c := &Cache{
		log2BlockSize: 6,
		directory:     dir,
		storage:       mem.NewStorage(4 * 2 * 64),
		writeBuffer:   &writeBufferStage{},
	}
	fill := func(lineAddr uint64, dirty bool, mask []bool, v byte) {
		b := dir.FindVictim(lineAddr)
		b.PID, b.Tag, b.IsValid, b.IsDirty, b.DirtyMask = 1, lineAddr, true, dirty, mask
		data := make([]byte, 64)
		for i := range data {
			data[i] = v
		}
		if err := c.storage.Write(b.CacheAddress, data); err != nil {
			t.Fatal(err)
		}
	}
	mask := make([]bool, 64)
	mask[1], mask[2] = true, true
	fill(0x1000, true, mask, 0xaa) // bytes 1,2 dirty
	fill(0x1040, false, nil, 0xbb) // clean: ignored
	c.writeBuffer.inflightEviction = []*transaction{{
		evictingPID: 1, evictingAddr: 0x1080, evictingData: bytesOf(64, 0xcc),
	}}
	c.writeBuffer.pendingEvictions = []*transaction{{
		evictingPID: 0, evictingAddr: 0x1100, evictingData: bytesOf(64, 0xdd), // out of range
	}}

	buf := bytesOf(0xc0, 0x11) // [0x1000, 0x10c0)
	c.PeekDirty(0x1000, buf)
	for i, b := range buf {
		want := byte(0x11)
		switch {
		case i == 1 || i == 2:
			want = 0xaa
		case i >= 0x80:
			want = 0xcc
		}
		if b != want {
			t.Fatalf("byte %#x = %#x, want %#x", i, b, want)
		}
	}

	// unaligned window inside a dirty line
	small := bytesOf(2, 0)
	c.PeekDirty(0x1002, small)
	if small[0] != 0xaa || small[1] != 0 {
		t.Fatalf("window %v", small)
	}
	// directory block wins over an older eviction of the same line
	fill(0x1080, true, nil, 0xee)
	line := bytesOf(64, 0)
	c.PeekDirty(0x1080, line)
	if line[0] != 0xee || line[63] != 0xee {
		t.Fatalf("dirty block not preferred: %#x", line[0])
	}

	// an interleaved slice skips addresses of other slices instead of
	// panicking in the address converter
	dir.AddrConverter = mem.InterleavingConverter{
		InterleavingSize: 64, TotalNumOfElements: 2, CurrentElementIndex: 1}
	if c.mayHold(0x1000) || !c.mayHold(0x1040) {
		t.Fatal("wrong slice ownership")
	}
	c.PeekDirty(0x1000, bytesOf(128, 0))
}

func bytesOf(n int, v byte) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = v
	}
	return b
}

// TestPeekDirtyPhysicalLinesWithPIDZero reproduces the timing-mode failure of
// coherent magic copies: an L2 slice built like the mi200 GPM L2 holds a
// line written through the address translator, which issues physical
// requests with PID 0, while the driver copies for the context's PID (1).
// The line must be found by its physical address.
func TestPeekDirtyPhysicalLinesWithPIDZero(t *testing.T) {
	const slices, slice = 8, 3
	c := MakeBuilder().
		WithEngine(sim.NewSerialEngine()).
		WithFreq(1*sim.GHz).
		WithLog2BlockSize(6).
		WithWayAssociativity(16).
		WithByteSize(256*1024).
		WithInterleaving(2, slices, slice). // 128 B per slice
		Build("L2")

	addr := uint64(0x200007000 + slice*128 + 64) // second line of the slice
	write := mem.WriteReqBuilder{}.
		WithAddress(addr).
		WithPID(0). // as issued by the address translator
		WithData(bytesOf(64, 0x5a)).
		Build()
	block := c.directory.FindVictim(addr)
	block.Tag, block.PID, block.IsValid, block.IsDirty =
		write.Address, write.PID, true, true
	if err := c.storage.Write(block.CacheAddress, write.Data); err != nil {
		t.Fatal(err)
	}

	page := bytesOf(4096, 0)
	c.PeekDirty(0x200007000, page) // the whole page, all slices' lines
	for i, b := range page {
		want := byte(0)
		if uint64(i) >= addr-0x200007000 && uint64(i) < addr-0x200007000+64 {
			want = 0x5a
		}
		if b != want {
			t.Fatalf("byte %#x = %#x, want %#x", i, b, want)
		}
	}
}
