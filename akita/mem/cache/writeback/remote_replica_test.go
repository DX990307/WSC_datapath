package writeback

import (
	"testing"

	"github.com/sarchlab/akita/v3/mem/mem"
	"github.com/sarchlab/akita/v3/mem/vm"
	"github.com/sarchlab/akita/v3/sim"
)

func TestResidentAndRemoteViewsShareOnePhysicalTypedFilter(t *testing.T) {
	cache := MakeBuilder().
		WithEngine(sim.NewSerialEngine()).
		WithByteSize(4 * 64).
		WithWayAssociativity(2).
		WithResidentFilter(true).
		WithRemoteReplicaFilter(true).
		Build("L2")

	resident, ok := cache.residentFilter.(*typedFilterLineView)
	if !ok {
		t.Fatalf("resident view has type %T", cache.residentFilter)
	}
	remote, ok := cache.remoteReplicaFilter.(*typedFilterLineView)
	if !ok {
		t.Fatalf("remote view has type %T", cache.remoteReplicaFilter)
	}
	if resident.filter == nil || resident.filter != cache.requestFilter ||
		remote.filter != cache.requestFilter {
		t.Fatal("compatibility views allocated separate physical Filters")
	}
}

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
	cache.untrackResidentBlock(block)
	if cache.remoteReplicaMayContain(fill.PID, fill.Address) {
		t.Fatal("evicted remote line remains in shared RESIDENT metadata")
	}
}

func TestUnusedSpeculativeRemoteFillRetiresPatternInSameSliceFilter(t *testing.T) {
	cache := MakeBuilder().
		WithEngine(sim.NewSerialEngine()).
		WithByteSize(4 * 64).
		WithWayAssociativity(2).
		WithTypedFilterConfig(TypedFilterConfig{
			Capacity: 128, CriticalReserve: 16,
			Mode: TypedFilterCuckoo, LookupWidth: 4, UpdateWidth: 4,
		}).
		WithRemoteReplicaFilter(true).
		Build("L2")
	pattern := TypedFilterKey{
		PID: 2, Owner: 77, Address: 3, Type: FilterPattern,
	}
	if !cache.requestFilter.Insert(pattern) {
		t.Fatal("pattern setup failed")
	}
	fill := &mem.RemoteDataFill{
		Address: 0x1800, PID: 2, Data: make([]byte, 64),
		HasPattern: true, PatternOwner: pattern.Owner,
		PatternAddress: pattern.Address,
	}
	if !cache.installRemoteDataFill(fill) {
		t.Fatal("speculative remote fill was not installed")
	}
	block := cache.directory.Lookup(fill.PID, fill.Address)
	cache.untrackRemoteReplica(block)
	if cache.requestFilter.ExactContains(pattern) {
		t.Fatal("unused requester-L2 line did not retire its PATTERN")
	}
	if cache.GetRemoteReplicaStats().UnusedPatternRetirements != 1 {
		t.Fatal("unused pattern feedback was not counted")
	}
}

func TestUsefulSpeculativeRemoteFillRetainsPatternInSameSliceFilter(t *testing.T) {
	cache := MakeBuilder().
		WithEngine(sim.NewSerialEngine()).
		WithByteSize(4 * 64).
		WithWayAssociativity(2).
		WithTypedFilterConfig(TypedFilterConfig{
			Capacity: 128, CriticalReserve: 16,
			Mode: TypedFilterCuckoo, LookupWidth: 4, UpdateWidth: 4,
		}).
		WithRemoteReplicaFilter(true).
		Build("L2")
	pattern := TypedFilterKey{
		PID: 2, Owner: 77, Address: 3, Type: FilterPattern,
	}
	if !cache.requestFilter.Insert(pattern) {
		t.Fatal("pattern setup failed")
	}
	fill := &mem.RemoteDataFill{
		Address: 0x1800, PID: 2, Data: make([]byte, 64),
		HasPattern: true, PatternOwner: pattern.Owner,
		PatternAddress: pattern.Address,
	}
	if !cache.installRemoteDataFill(fill) {
		t.Fatal("speculative remote fill was not installed")
	}
	block := cache.directory.Lookup(fill.PID, fill.Address)
	if !cache.recordRemoteReplicaHit(block, fill.PID, fill.Address) {
		t.Fatal("real requester-L2 hit was not recorded")
	}
	cache.untrackRemoteReplica(block)
	if !cache.requestFilter.ExactContains(pattern) {
		t.Fatal("useful requester-L2 line incorrectly retired its PATTERN")
	}
	stats := cache.GetRemoteReplicaStats()
	if stats.UsefulTwoTouchFills != 1 ||
		stats.UnusedPatternRetirements != 0 {
		t.Fatalf("unexpected useful feedback accounting: %+v", stats)
	}
}

func TestSpeculativeFirstTouchFillUsesOnlyInvalidVictim(t *testing.T) {
	cache := MakeBuilder().
		WithEngine(sim.NewSerialEngine()).
		WithByteSize(64).
		WithWayAssociativity(1).
		WithRemoteReplicaFilter(true).
		Build("L2")
	first := &mem.RemoteDataFill{
		Address: 0x1000, PID: 1, Data: make([]byte, 64),
	}
	if !cache.installRemoteDataFill(first) {
		t.Fatal("initial remote-clean fill was dropped")
	}
	second := &mem.RemoteDataFill{
		Address: 0x2000, PID: 1, Data: make([]byte, 64),
		RequireInvalidVictim: true,
	}
	if cache.installRemoteDataFill(second) {
		t.Fatal("first-touch speculation replaced a valid remote-clean line")
	}
	if cache.directory.Lookup(first.PID, first.Address) == nil {
		t.Fatal("invalid-only attempt destroyed the existing line")
	}
	stats := cache.GetRemoteReplicaStats()
	if stats.SpeculativeInvalidOnlyAttempts != 1 ||
		stats.SpeculativeInvalidOnlyDrops != 1 {
		t.Fatalf("invalid-only accounting = %+v", stats)
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

func TestRemoteReplicaReplacementKeepsResidentAndReplicaFiltersConsistent(
	t *testing.T,
) {
	cache := MakeBuilder().
		WithEngine(sim.NewSerialEngine()).
		WithByteSize(64).
		WithWayAssociativity(1).
		WithResidentFilter(true).
		WithRemoteReplicaFilter(true).
		Build("L2")
	pid := vm.PID(9)
	first := &mem.RemoteDataFill{
		Address: 0x1000,
		PID:     pid,
		Data:    make([]byte, 64),
	}
	second := &mem.RemoteDataFill{
		Address: 0x2000,
		PID:     pid,
		Data:    make([]byte, 64),
	}
	if !cache.installRemoteDataFill(first) {
		t.Fatal("first remote replica was dropped")
	}
	if !cache.remoteReplicaFilter.Contains(pid, first.Address) ||
		!cache.residentFilter.Contains(pid, first.Address) {
		t.Fatal("first remote replica did not enter both filters")
	}

	if !cache.installRemoteDataFill(second) {
		t.Fatal("second remote replica did not replace the first")
	}
	if cache.remoteReplicaFilter.Contains(pid, first.Address) ||
		cache.residentFilter.Contains(pid, first.Address) {
		t.Fatal("replaced remote replica remained in a Cuckoo filter")
	}
	if !cache.remoteReplicaFilter.Contains(pid, second.Address) ||
		!cache.residentFilter.Contains(pid, second.Address) {
		t.Fatal("replacement remote replica is absent from a Cuckoo filter")
	}
	block := cache.directory.Lookup(pid, second.Address)
	if block == nil || !cache.remoteReplicaMatches(
		block, pid, second.Address,
	) {
		t.Fatal("replacement directory and replica metadata disagree")
	}
	stats := cache.GetRemoteReplicaStats()
	if stats.FillReplacedRemote != 1 ||
		stats.LocalCleanProtectionDrops != 0 ||
		stats.CurrentRemoteReplicas != 1 {
		t.Fatalf("unexpected replacement stats: %+v", stats)
	}
}

func TestLocalWriteConvertsRemoteReplicaToDirtyResidentWithoutRFO(
	t *testing.T,
) {
	cache := MakeBuilder().
		WithEngine(sim.NewSerialEngine()).
		WithByteSize(64).
		WithWayAssociativity(1).
		WithNumReqPerCycle(1).
		WithDirectoryLatency(1).
		WithResidentFilter(true).
		WithRemoteReplicaFilter(true).
		Build("L2")
	pid := vm.PID(10)
	line := uint64(0x3000)
	fill := &mem.RemoteDataFill{
		Address: line,
		PID:     pid,
		Data:    make([]byte, 64),
	}
	if !cache.installRemoteDataFill(fill) {
		t.Fatal("remote replica setup fill was dropped")
	}

	data := make([]byte, 64)
	data[11] = 0xa5
	write := mem.WriteReqBuilder{}.
		WithSrc(cache.topPort).
		WithDst(cache.topPort).
		WithPID(pid).
		WithAddress(line).
		WithData(data).
		Build()
	if err := cache.topPort.Recv(write); err != nil {
		t.Fatal(err)
	}
	if !cache.topParser.Tick(1) {
		t.Fatal("top parser did not accept local write")
	}
	cache.dirStage.Tick(2)
	cache.dirStage.Tick(3)
	cache.dirStage.Tick(4)

	block := cache.directory.Lookup(pid, line)
	if block == nil || !block.IsLocked {
		t.Fatal("local write did not lock the resident L2 block")
	}
	if cache.remoteReplicaFilter.Contains(pid, line) ||
		cache.remoteReplicaMatches(block, pid, line) {
		t.Fatal("local write retained stale remote-replica metadata")
	}
	if cache.residentFilter.Contains(pid, line) {
		t.Fatal("locked write entered the resident filter before data commit")
	}
	if cache.mshr.Query(pid, line) != nil {
		t.Fatal("full-line write hit unnecessarily issued an RFO")
	}

	bankNum := bankID(
		block, cache.directory.WayAssociativity(), len(cache.bankStages))
	trans := cache.dirToBankBuffers[bankNum].Peek().(*transaction)
	stage := cache.bankStages[bankNum]
	stage.inflightTransCount = 1
	stage.downwardInflightTransCount = 1
	if !stage.finalizeWriteHit(5, trans) {
		t.Fatal("local write did not commit to the L2 bank")
	}
	stored, err := cache.storage.Read(block.CacheAddress, 64)
	if err != nil || stored[11] != 0xa5 {
		t.Fatal("local write data was not committed to L2 storage")
	}
	if !block.IsValid || block.IsLocked || !block.IsDirty {
		t.Fatal("committed local write has invalid L2 block state")
	}
	if !cache.residentFilter.Contains(pid, line) {
		t.Fatal("committed local write is absent from the resident filter")
	}
	if !cache.remoteReplicaFilter.Contains(pid, line) {
		t.Fatal("committed local write is absent from shared RESIDENT metadata")
	}
	if cache.remoteReplicaMatches(block, pid, line) {
		t.Fatal("committed local write retained remote-fill provenance")
	}
}

func TestRemoteDataFillDoesNotDisplaceLocalCleanLine(t *testing.T) {
	cache := MakeBuilder().
		WithEngine(sim.NewSerialEngine()).
		WithByteSize(64).
		WithWayAssociativity(1).
		WithRemoteReplicaFilter(true).
		Build("L2")
	localPID := vm.PID(7)
	localLine := uint64(0x1000)
	block := cache.directory.FindVictim(localLine)
	block.PID = localPID
	block.Tag = localLine
	block.IsValid = true
	block.IsDirty = false
	cache.directory.Visit(block)

	fill := &mem.RemoteDataFill{
		Address: 0x2000,
		PID:     vm.PID(8),
		Data:    make([]byte, 64),
	}
	if cache.installRemoteDataFill(fill) {
		t.Fatal("remote fill displaced a local clean line")
	}
	if cache.directory.Lookup(localPID, localLine) != block {
		t.Fatal("local clean line changed after rejected remote fill")
	}
	stats := cache.GetRemoteReplicaStats()
	if stats.LocalCleanProtectionDrops != 1 || stats.DroppedFills != 1 {
		t.Fatalf("protection stats = %+v", stats)
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
