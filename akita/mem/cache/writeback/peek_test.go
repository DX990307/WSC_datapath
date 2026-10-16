package writeback

import (
	"testing"

	"github.com/sarchlab/akita/v3/mem/cache"
	"github.com/sarchlab/akita/v3/mem/mem"
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
		evictingPID: 2, evictingAddr: 0x1080, evictingData: bytesOf(64, 0xdd), // other PID
	}}

	buf := bytesOf(0xc0, 0x11) // [0x1000, 0x10c0)
	c.PeekDirty(1, 0x1000, buf)
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
	c.PeekDirty(1, 0x1002, small)
	if small[0] != 0xaa || small[1] != 0 {
		t.Fatalf("window %v", small)
	}
	// directory block wins over an older eviction of the same line
	fill(0x1080, true, nil, 0xee)
	line := bytesOf(64, 0)
	c.PeekDirty(1, 0x1080, line)
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
	c.PeekDirty(1, 0x1000, bytesOf(128, 0))
}

func bytesOf(n int, v byte) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = v
	}
	return b
}
