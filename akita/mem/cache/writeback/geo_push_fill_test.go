package writeback

import (
	"testing"

	"github.com/sarchlab/akita/v3/mem/mem"
	"github.com/sarchlab/akita/v3/mem/vm"
	"github.com/sarchlab/akita/v3/sim"
)

func geoPushFill(addr uint64, origin sim.VTimeInSec) *mem.RemoteDataFill {
	return mem.RemoteDataFillBuilder{}.
		WithPID(vm.PID(1)).
		WithAddress(addr).
		WithData(make([]byte, 64)).
		WithGeneration(99). // ignored for pushes
		WithPush(origin).
		Build()
}

func TestGeoPushFillInstallsAndCountsSeparately(t *testing.T) {
	cache := MakeBuilder().
		WithEngine(sim.NewSerialEngine()).
		WithByteSize(4 * 64).
		WithWayAssociativity(2).
		WithRemoteReplicaFilter(true).
		Build("L2")
	fill := geoPushFill(0x1000, 5)
	if !fill.Push || !fill.RequireInvalidVictim {
		t.Fatal("push fill must be a push that never evicts")
	}
	if !cache.installRemoteDataFill(fill) {
		t.Fatal("push fill dropped")
	}
	// Already resident: dropped.
	if cache.installRemoteDataFill(geoPushFill(0x1000, 6)) {
		t.Fatal("push of a resident line installed")
	}
	block := cache.directory.Lookup(1, 0x1000)
	cache.recordRemoteReplicaHit(block, 1, 0x1000)
	cache.recordRemoteReplicaHit(block, 1, 0x1000)
	s := cache.GetRemoteReplicaStats()
	if s.PushFills != 2 || s.PushInstalled != 1 || s.PushDroppedResident != 1 ||
		s.PushUseful != 1 || s.PushHits != 2 {
		t.Fatalf("push stats %+v", s)
	}
	if s.CleanFills != 0 || s.InstalledFills != 0 || s.ReplicaProbeHits != 0 {
		t.Fatalf("push counted as an M3 fill: %+v", s)
	}
}

func TestGeoPushFillDroppedWhenStaleOrNoInvalidWay(t *testing.T) {
	cache := MakeBuilder().
		WithEngine(sim.NewSerialEngine()).
		WithByteSize(2 * 64).
		WithWayAssociativity(2).
		WithRemoteReplicaFilter(true).
		Build("L2")
	cache.InvalidateRemoteReplicas(10)
	if cache.installRemoteDataFill(geoPushFill(0x40, 10)) {
		t.Fatal("push read before the kernel boundary installed")
	}
	if !cache.installRemoteDataFill(geoPushFill(0x40, 11)) ||
		!cache.installRemoteDataFill(geoPushFill(0x80, 11)) {
		t.Fatal("fresh pushes dropped")
	}
	// Both ways valid now: a push never evicts.
	if cache.installRemoteDataFill(geoPushFill(0xc0, 12)) {
		t.Fatal("push evicted a valid line")
	}
	s := cache.GetRemoteReplicaStats()
	if s.PushDroppedStale != 1 || s.PushInstalled != 2 || s.PushDropped != 2 {
		t.Fatalf("push stats %+v", s)
	}
	cache.InvalidateRemoteReplicas(20)
	s = cache.GetRemoteReplicaStats()
	if s.PushUnusedRetirements != 2 {
		t.Fatalf("unused push retirements = %d", s.PushUnusedRetirements)
	}
}
