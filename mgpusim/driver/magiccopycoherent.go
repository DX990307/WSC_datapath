package driver

import "github.com/sarchlab/akita/v3/mem/vm"

// MagicCopyPeeker is a cache that can overlay, functionally, the bytes it
// holds that are newer than memory (dirty lines, pending write-backs) onto a
// buffer read from memory. akita's writeback.Cache implements it.
type MagicCopyPeeker interface {
	PeekDirty(pid vm.PID, addr uint64, buf []byte)
}

// RegisterMagicCopyPeekers records the caches a coherent magic D2H copy
// reads through. Registering changes nothing until coherent copies are
// enabled.
func (d *Driver) RegisterMagicCopyPeekers(peekers []MagicCopyPeeker) {
	d.magicCopyPeekers = append(d.magicCopyPeekers, peekers...)
}

// SetMagicCopyCoherent turns coherent magic D2H copies on or off (default
// off). When on, a magic D2H copy (-magic-memory-copy) returns the
// up-to-date data: memory overlaid with the dirty bytes of the registered
// caches, read functionally without simulated time and without changing any
// cache state. Without it, a magic D2H copy reads memory only and misses
// data still dirty in the write-back L2. Copies without magic copy flush the
// caches first and are unaffected.
func (d *Driver) SetMagicCopyCoherent(on bool) { d.magicCopyCoherent = on }

// MagicCopyCoherent reports whether coherent magic D2H copies are on.
func (d *Driver) MagicCopyCoherent() bool { return d.magicCopyCoherent }

func (d *Driver) peekCachedData(pid vm.PID, pAddr uint64, buf []byte) {
	if !d.magicCopyCoherent {
		return
	}
	for _, p := range d.magicCopyPeekers {
		p.PeekDirty(pid, pAddr, buf)
	}
}
