package driver

import "testing"

type fakePeeker struct{ calls int }

func (f *fakePeeker) PeekDirty(addr uint64, buf []byte) {
	f.calls++
	buf[0] = byte(addr)
}

func TestMagicCopyCoherentOptIn(t *testing.T) {
	d := &Driver{}
	f := &fakePeeker{}
	d.RegisterMagicCopyPeekers([]MagicCopyPeeker{f})
	buf := []byte{0}
	d.peekCachedData(7, buf)
	if f.calls != 0 || buf[0] != 0 {
		t.Fatal("peeker used while coherent copies are off")
	}
	d.SetMagicCopyCoherent(true)
	d.peekCachedData(7, buf)
	if f.calls != 1 || buf[0] != 7 {
		t.Fatal("peeker not used while coherent copies are on")
	}
}
