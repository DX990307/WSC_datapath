package writeback

import (
	"testing"

	"github.com/sarchlab/akita/v3/mem/mem"
	"github.com/sarchlab/akita/v3/mem/vm"
	"github.com/sarchlab/akita/v3/sim"
)

func boundaryTestFill(addr uint64, sendTime sim.VTimeInSec) *mem.RemoteDataFill {
	fill := &mem.RemoteDataFill{
		Address: addr,
		PID:     vm.PID(4),
		Data:    make([]byte, 64),
	}
	fill.SendTime = sendTime
	return fill
}

func TestInvalidateRemoteReplicasDropsOnlyReplicaLines(t *testing.T) {
	cache := MakeBuilder().
		WithEngine(sim.NewSerialEngine()).
		WithByteSize(8 * 64).
		WithWayAssociativity(2).
		WithResidentFilter(true).
		WithRemoteReplicaFilter(true).
		Build("L2")
	for _, addr := range []uint64{0x1000, 0x1040} {
		if !cache.installRemoteDataFill(boundaryTestFill(addr, 1)) {
			t.Fatalf("setup fill %#x dropped", addr)
		}
	}
	// An ordinary (local) clean line must survive the invalidation.
	local := cache.directory.FindVictim(0x2080)
	local.PID, local.Tag, local.IsValid = vm.PID(4), 0x2080, true
	cache.directory.Visit(local)

	if n := cache.InvalidateRemoteReplicas(5); n != 2 {
		t.Fatalf("invalidated %d lines, want 2", n)
	}
	for _, addr := range []uint64{0x1000, 0x1040} {
		if cache.directory.Lookup(vm.PID(4), addr) != nil {
			t.Fatalf("replica %#x still valid after the boundary", addr)
		}
		if cache.remoteReplicaMayContain(vm.PID(4), addr) {
			t.Fatalf("replica %#x still in the filter", addr)
		}
	}
	if cache.directory.Lookup(vm.PID(4), 0x2080) == nil {
		t.Fatal("local line was invalidated")
	}
	if got := cache.GetRemoteReplicaStats().CurrentRemoteReplicas; got != 0 {
		t.Fatalf("current replicas = %d, want 0", got)
	}
	b := cache.GetRemoteReplicaBoundaryStats()
	if b.Invalidations != 1 || b.LinesInvalidated != 2 {
		t.Fatalf("boundary stats = %+v", b)
	}

	// A fill sent before (or at) the boundary carries pre-boundary data.
	if cache.installRemoteDataFill(boundaryTestFill(0x1000, 5)) {
		t.Fatal("fill sent at the boundary was installed")
	}
	if !cache.installRemoteDataFill(boundaryTestFill(0x1000, 6)) {
		t.Fatal("fill sent after the boundary was dropped")
	}
	if cache.GetRemoteReplicaBoundaryStats().StaleFillDrops != 1 {
		t.Fatal("stale fill drop not counted")
	}
}

func TestRemoteFillsAreUnaffectedWithoutBoundary(t *testing.T) {
	cache := MakeBuilder().
		WithEngine(sim.NewSerialEngine()).
		WithByteSize(8 * 64).
		WithWayAssociativity(2).
		WithRemoteReplicaFilter(true).
		Build("L2")
	// SendTime 0 fills must install as before when no boundary was seen.
	if !cache.installRemoteDataFill(boundaryTestFill(0x1000, 0)) {
		t.Fatal("fill dropped without any kernel boundary")
	}
	if (cache.GetRemoteReplicaBoundaryStats() != RemoteReplicaBoundaryStats{}) {
		t.Fatal("boundary stats changed without a boundary")
	}
}

func TestInvalidateRemoteReplicasWithoutReplicaFilterIsNoop(t *testing.T) {
	cache := MakeBuilder().
		WithEngine(sim.NewSerialEngine()).
		WithByteSize(8 * 64).
		WithWayAssociativity(2).
		Build("L2")
	if n := cache.InvalidateRemoteReplicas(1); n != 0 {
		t.Fatalf("invalidated %d lines without M3", n)
	}
	if cache.replicaBoundarySet {
		t.Fatal("boundary recorded without M3")
	}
}
