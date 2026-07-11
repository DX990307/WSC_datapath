package writeback

import (
	"testing"

	"github.com/sarchlab/akita/v3/mem/mem"
	"github.com/sarchlab/akita/v3/mem/vm"
	"github.com/sarchlab/akita/v3/sim"
)

func TestRemoteDataFillInstallsCleanLineAndUpdatesFilter(t *testing.T) {
	cache := MakeBuilder().
		WithEngine(sim.NewSerialEngine()).
		WithByteSize(4 * 64).
		WithWayAssociativity(2).
		WithRemoteReplicaFilter(true).
		Build("L2")
	data := make([]byte, 64)
	data[3] = 9
	fill := &mem.RemoteDataFill{
		Address: 0x1000,
		PID:     vm.PID(4),
		Data:    data,
	}

	if !cache.installRemoteDataFill(fill) {
		t.Fatal("valid remote fill was dropped")
	}
	if !cache.remoteReplicaMayContain(fill.PID, fill.Address) {
		t.Fatal("installed remote line is absent from the filter")
	}
	block := cache.directory.Lookup(fill.PID, fill.Address)
	if block == nil || block.IsDirty || !block.IsValid {
		t.Fatal("remote fill was not installed as a valid clean block")
	}
	stored, err := cache.storage.Read(block.CacheAddress, 64)
	if err != nil || stored[3] != 9 {
		t.Fatal("remote fill data was not written to L2 storage")
	}
	if !cache.recordRemoteReplicaHit(block, fill.PID, fill.Address) ||
		!cache.recordRemoteReplicaHit(block, fill.PID, fill.Address) {
		t.Fatal("installed remote replica was not tracked on lookup")
	}
	stats := cache.GetRemoteReplicaStats()
	if stats.ReplicaProbeHits != 2 || stats.UsefulTwoTouchFills != 1 {
		t.Fatalf("replica hit/useful counts = (%d, %d), want (2, 1)",
			stats.ReplicaProbeHits, stats.UsefulTwoTouchFills)
	}

	cache.untrackRemoteReplica(block)
	if cache.remoteReplicaMayContain(fill.PID, fill.Address) {
		t.Fatal("evicted remote line remains in the filter")
	}
}

func TestRemotePrefetchUsefulnessIsTrackedSeparately(t *testing.T) {
	cache := MakeBuilder().
		WithEngine(sim.NewSerialEngine()).
		WithByteSize(2 * 64).
		WithWayAssociativity(1).
		WithRemoteReplicaFilter(true).
		Build("L2")

	first := &mem.RemoteDataFill{
		Address:  0x1000,
		PID:      vm.PID(1),
		Data:     make([]byte, 64),
		Prefetch: true,
	}
	if !cache.installRemoteDataFill(first) {
		t.Fatal("prefetch fill was dropped")
	}
	block := cache.directory.Lookup(first.PID, first.Address)
	if !cache.recordRemoteReplicaHit(block, first.PID, first.Address) {
		t.Fatal("prefetch replica hit was not recorded")
	}
	stats := cache.GetRemoteReplicaStats()
	if stats.PrefetchInstalledFills != 1 ||
		stats.UsefulPrefetchFills != 1 ||
		stats.PrefetchReplicaHits != 1 {
		t.Fatalf("prefetch stats = installed %d, useful %d, hits %d",
			stats.PrefetchInstalledFills,
			stats.UsefulPrefetchFills,
			stats.PrefetchReplicaHits)
	}
}

func TestRemoteDataFillDoesNotOverwriteResidentLine(t *testing.T) {
	cache := MakeBuilder().
		WithEngine(sim.NewSerialEngine()).
		WithByteSize(4 * 64).
		WithWayAssociativity(2).
		WithRemoteReplicaFilter(true).
		Build("L2")
	first := &mem.RemoteDataFill{
		Address: 0x2000,
		PID:     vm.PID(1),
		Data:    make([]byte, 64),
	}
	first.Data[0] = 1
	if !cache.installRemoteDataFill(first) {
		t.Fatal("first fill was dropped")
	}
	late := &mem.RemoteDataFill{
		Address: first.Address,
		PID:     first.PID,
		Data:    make([]byte, 64),
	}
	late.Data[0] = 2
	if cache.installRemoteDataFill(late) {
		t.Fatal("late fill overwrote an existing line")
	}
	block := cache.directory.Lookup(first.PID, first.Address)
	stored, _ := cache.storage.Read(block.CacheAddress, 64)
	if stored[0] != 1 {
		t.Fatal("late response changed resident data")
	}
}

func TestLookupOnlyFalsePositiveDoesNotFetchFromDRAM(t *testing.T) {
	cache := MakeBuilder().
		WithEngine(sim.NewSerialEngine()).
		WithByteSize(4 * 64).
		WithWayAssociativity(2).
		WithDirectoryLatency(1).
		WithRemoteReplicaFilter(true).
		Build("L2")
	pid := vm.PID(2)
	line := uint64(0x3000)
	if !cache.remoteReplicaFilter.Insert(pid, line) {
		t.Fatal("could not create a deliberate filter false positive")
	}
	lookup := mem.ReadReqBuilder{}.
		WithSrc(cache.topPort).
		WithDst(cache.topPort).
		WithPID(pid).
		WithAddress(line).
		WithByteSize(64).
		WithLookupOnly().
		Build()
	if err := cache.topPort.Recv(lookup); err != nil {
		t.Fatal("could not inject lookup request")
	}
	if !cache.topParser.Tick(1) {
		t.Fatal("lookup was not accepted by the L2 top parser")
	}
	for cycle := 2; cycle < 10; cycle++ {
		cache.dirStage.Tick(sim.VTimeInSec(cycle))
	}

	if cache.mshr.Query(pid, line) != nil {
		t.Fatal("lookup-only miss allocated an MSHR")
	}
	if cache.writeBufferBuffer.Size() != 0 {
		t.Fatal("lookup-only miss entered the DRAM fetch path")
	}
	if len(cache.inFlightTransactions) != 0 {
		t.Fatal("lookup-only miss did not complete locally")
	}
}

func TestRemoteDataFillRejectsPreviousFlushGeneration(t *testing.T) {
	cache := MakeBuilder().
		WithEngine(sim.NewSerialEngine()).
		WithByteSize(4 * 64).
		WithWayAssociativity(2).
		WithRemoteReplicaFilter(true).
		Build("L2")
	cache.remoteReplicaGeneration = 1
	stale := &mem.RemoteDataFill{
		Address:    0x4000,
		PID:        vm.PID(3),
		Data:       make([]byte, 64),
		Generation: 0,
	}
	if cache.installRemoteDataFill(stale) {
		t.Fatal("a fill from before the flush generation was installed")
	}
	if cache.directory.Lookup(stale.PID, stale.Address) != nil {
		t.Fatal("stale fill changed the L2 directory")
	}
	stale.Generation = 1
	if !cache.installRemoteDataFill(stale) {
		t.Fatal("current-generation fill was rejected")
	}
}
