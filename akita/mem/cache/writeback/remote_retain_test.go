package writeback

import (
	"testing"

	"github.com/sarchlab/akita/v3/mem/vm"
	"github.com/sarchlab/akita/v3/sim"
)

func TestRemoteRetainTrackerWindows(t *testing.T) {
	tr := NewRemoteRetainTracker(RemoteRetainConfig{Log2RegionSize: 12})
	a := tr.RegisterRequester()
	b := tr.RegisterRequester()

	// Host copy before the first kernel.
	tr.NoteWrite(0, 0x10000, 8192) // regions 0x10, 0x11
	since := tr.BeginBoundary(a, 1)
	if !tr.WrittenSince(0x10040, since) || !tr.WrittenSince(0x11fc0, since) {
		t.Fatal("host-copied regions not written at the first boundary")
	}
	if tr.WrittenSince(0x12000, since) {
		t.Fatal("untouched region reported written")
	}
	sinceB := tr.BeginBoundary(b, 1)

	// Kernel 1 is read-only: the next boundary keeps everything.
	sinceA := tr.BeginBoundary(a, 10)
	if tr.WrittenSince(0x10040, sinceA) {
		t.Fatal("region written before the previous boundary reported again")
	}
	// Kernel 2 writes region 0x10 (remote or local write at the home).
	tr.NoteWrite(15, 0x10080, 64)
	// B launches kernel 3 first, then A.
	if !tr.WrittenSince(0x10000, tr.BeginBoundary(b, 20)) {
		t.Fatal("B missed the kernel-2 write")
	}
	if !tr.WrittenSince(0x10000, tr.BeginBoundary(a, 21)) {
		t.Fatal("A missed the kernel-2 write")
	}
	_ = sinceB
	// A write by an early GPM between B's and A's launch of kernel 4 is seen
	// by A at this boundary and by B at its next one.
	tr.BeginBoundary(b, 30)
	tr.NoteWrite(31, 0x11000, 4)
	if !tr.WrittenSince(0x11000, tr.BeginBoundary(a, 32)) {
		t.Fatal("A missed a write between the two launches")
	}
	if !tr.WrittenSince(0x11000, tr.BeginBoundary(b, 40)) {
		t.Fatal("B missed a write made after its launch")
	}
	if tr.WrittenSince(0x11000, tr.BeginBoundary(a, 41)) {
		t.Fatal("A reported a write twice")
	}
}

func TestRemoteRetainSignatureIsConservative(t *testing.T) {
	tr := NewRemoteRetainTracker(RemoteRetainConfig{
		Log2RegionSize: 12, SignatureBits: 64, SignatureHashes: 2})
	a := tr.RegisterRequester()
	tr.BeginBoundary(a, 0)
	for i := uint64(0); i < 40; i++ {
		tr.NoteWrite(1, (1<<33)+i*4096, 64)
	}
	since := tr.BeginBoundary(a, 2)
	for i := uint64(0); i < 40; i++ {
		if !tr.WrittenSince((1<<33)+i*4096, since) {
			t.Fatalf("signature missed written region %d", i)
		}
	}
	fp := 0
	for i := uint64(40); i < 400; i++ {
		if tr.WrittenSince((1<<33)+i*4096, since) {
			fp++
		}
	}
	if fp == 0 || uint64(fp) != tr.Stats().SignatureFalsePositive {
		t.Fatalf("false positives %d, stats %d", fp,
			tr.Stats().SignatureFalsePositive)
	}
	// Another home's signature is separate.
	if tr.WrittenSince((2<<33)+0x5000, since) {
		t.Fatal("unwritten home reported written")
	}
}

func TestInvalidateWrittenRemoteReplicasKeepsReadOnlyLines(t *testing.T) {
	cache := MakeBuilder().
		WithEngine(sim.NewSerialEngine()).
		WithByteSize(8 * 64).
		WithWayAssociativity(2).
		WithResidentFilter(true).
		WithRemoteReplicaFilter(true).
		Build("L2")
	tr := NewRemoteRetainTracker(RemoteRetainConfig{Log2RegionSize: 12})
	id := tr.RegisterRequester()
	cache.SetHomeWriteObserver(tr)
	tr.BeginBoundary(id, 0)

	// Kernel k reads 0x1000 (page 1) and 0x2040 (page 2) remotely.
	for _, addr := range []uint64{0x1000, 0x2040} {
		if !cache.installRemoteDataFill(boundaryTestFill(addr, 1)) {
			t.Fatalf("setup fill %#x dropped", addr)
		}
	}
	boundary := func(now sim.VTimeInSec) (int, int) {
		since := tr.BeginBoundary(id, now)
		return cache.InvalidateWrittenRemoteReplicas(now,
			func(line uint64) bool { return tr.WrittenSince(line, since) })
	}
	// k -> k+1: nothing written, both kept.
	if inv, ret := boundary(5); inv != 0 || ret != 2 {
		t.Fatalf("boundary 1: invalidated %d retained %d", inv, ret)
	}
	for _, addr := range []uint64{0x1000, 0x2040} {
		block := cache.directory.Lookup(vm.PID(4), addr)
		if block == nil || !cache.remoteReplicaMayContain(vm.PID(4), addr) {
			t.Fatalf("retained replica %#x not valid", addr)
		}
		cache.recordRemoteReplicaHit(block, vm.PID(4), addr)
	}
	// Kernel k+1 writes another line of page 2 at its home.
	tr.NoteWrite(7, 0x2080, 64)
	// k+1 -> k+2: page 2 dropped, page 1 kept.
	if inv, ret := boundary(10); inv != 1 || ret != 1 {
		t.Fatalf("boundary 2: invalidated %d retained %d", inv, ret)
	}
	if cache.directory.Lookup(vm.PID(4), 0x2040) != nil ||
		cache.remoteReplicaMayContain(vm.PID(4), 0x2040) {
		t.Fatal("replica of a written page survived")
	}
	if cache.directory.Lookup(vm.PID(4), 0x1000) == nil {
		t.Fatal("read-only replica dropped")
	}
	s := cache.GetRemoteRetainBoundaryStats()
	if s.Boundaries != 2 || s.LinesRetained != 3 || s.LinesInvalidated != 1 ||
		s.RetainedHits != 2 || s.RetainedLinesUsed != 2 {
		t.Fatalf("stats %+v", s)
	}
	// Fills sent before the boundary are still dropped.
	if cache.installRemoteDataFill(boundaryTestFill(0x2040, 10)) {
		t.Fatal("fill sent at the boundary installed")
	}
}

func TestHomeWriteObserverDefaultNil(t *testing.T) {
	cache := MakeBuilder().
		WithEngine(sim.NewSerialEngine()).
		WithByteSize(8 * 64).
		WithWayAssociativity(2).
		Build("L2")
	cache.noteHomeWrite(0, 0x1000, 64) // must not panic
	if (cache.GetRemoteRetainBoundaryStats() != RemoteRetainBoundaryStats{}) {
		t.Fatal("A1 stats changed while off")
	}
}
