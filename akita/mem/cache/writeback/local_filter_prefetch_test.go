package writeback

import (
	"testing"

	"github.com/sarchlab/akita/v3/mem/mem"
	"github.com/sarchlab/akita/v3/mem/vm"
	"github.com/sarchlab/akita/v3/sim"
)

func newLocalFilterPrefetchTestCache() *Cache {
	return MakeBuilder().
		WithEngine(sim.NewSerialEngine()).
		WithByteSize(8 * 64).
		WithWayAssociativity(2).
		WithNumReqPerCycle(1).
		WithDirectoryLatency(1).
		WithTypedFilterConfig(TypedFilterConfig{
			Mode: TypedFilterCuckoo, Capacity: 128, CriticalReserve: 16,
			LookupLatencyCycles: 1,
			LookupWidth:         4, UpdateLatencyCycles: 1, UpdateWidth: 4,
		}).
		WithFilterCoupledPrefetch(true).
		WithFilterCoupledPrefetchStreams(8).
		Build("L2")
}

func TestFilterPrefetchDisabledKeepsBaselineStateUnallocated(t *testing.T) {
	cache := MakeBuilder().
		WithEngine(sim.NewSerialEngine()).
		WithByteSize(8 * 64).
		WithWayAssociativity(2).
		WithNumReqPerCycle(1).
		WithDirectoryLatency(1).
		Build("L2")

	if cache.filterPrefetchEnabled || cache.requestFilter != nil ||
		cache.filterPrefetcher != nil || cache.localPrefetchPatternFilters != nil ||
		cache.localPrefetchByLine != nil || cache.localPrefetchByBlock != nil {
		t.Fatal("default-off L2 allocated Filter/prefetch state")
	}

	cache.observeLocalReadDemand(
		cache.Engine.CurrentTime(), localPrefetchDemand(cache, 1, 0x1000))
	if stats := cache.GetLocalFilterPrefetchStats(); stats.Enabled || stats.RealReadDemands != 0 || stats.Candidates != 0 {
		t.Fatalf("disabled prefetcher changed baseline demand state: %+v", stats)
	}
}

func localPrefetchDemand(cache *Cache, pid vm.PID, address uint64) *mem.ReadReq {
	return mem.ReadReqBuilder{}.
		WithSrc(cache.topPort).
		WithDst(cache.topPort).
		WithPID(pid).
		WithAddress(address).
		WithByteSize(64).
		Build()
}

func TestLocalFilterPrefetchNeedsPatternAndIssuesOne64BLine(t *testing.T) {
	cache := newLocalFilterPrefetchTestCache()
	pid := vm.PID(3)
	cache.observeLocalReadDemand(1, localPrefetchDemand(cache, pid, 0x1000))
	cache.observeLocalReadDemand(2, localPrefetchDemand(cache, pid, 0x1040))
	cache.observeLocalReadDemand(3, localPrefetchDemand(cache, pid, 0x1080))
	if cache.localPrefetchCandidate == nil {
		t.Fatal("PATTERN establishment did not propose the next 64-B line")
	}
	if !cache.runLocalFilterPrefetch(4) {
		t.Fatal("ready candidate did not make progress")
	}
	stats := cache.GetLocalFilterPrefetchStats()
	if stats.Candidates != 1 || stats.Issued != 1 || stats.Outstanding != 1 {
		t.Fatalf("unexpected prefetch stats: %+v", stats)
	}
	item := cache.dirStageBuffer.Peek()
	trans, ok := item.(*transaction)
	if !ok || !trans.prefetch || trans.read.AccessByteSize != 64 ||
		trans.read.Address != 0x10c0 {
		t.Fatalf("issued transaction = %#v", item)
	}
	if !trans.residentFilterChecked || !trans.residentFilterNegative ||
		!trans.residentFastMiss {
		t.Fatal("issued candidate did not carry its RESIDENT-negative proof")
	}
	line, _ := getCacheLineID(0x10c0, cache.log2BlockSize)
	if !cache.requestFilter.ExactContains(TypedFilterKey{
		PID: pid, Address: line, Type: FilterPending,
	}) {
		t.Fatal("issued candidate is not represented by PENDING")
	}
	cache.completeLocalPrefetchFill(trans, cache.directory.FindVictim(line))
	if stats := cache.GetLocalFilterPrefetchStats(); stats.Outstanding != 0 ||
		stats.Fills != 1 {
		t.Fatalf("completed prefetch lifecycle = %+v", stats)
	}
	if cache.requestFilter.ExactContains(TypedFilterKey{
		PID: pid, Address: line, Type: FilterPending,
	}) {
		t.Fatal("completed speculative response retained stale PENDING")
	}
}

func TestEarlyLocalCandidateStillRequiresPatternFilterAdmission(t *testing.T) {
	cache := newLocalFilterPrefetchTestCache()
	pid := vm.PID(16)
	cache.observeLocalReadDemand(1, localPrefetchDemand(cache, pid, 0xd000))
	cache.observeLocalReadDemand(2, localPrefetchDemand(cache, pid, 0xd040))
	for i := 0; i < cache.requestFilter.config.UpdateWidth; i++ {
		accepted := cache.requestFilter.TryScheduleUpdate(3, TypedFilterKey{
			PID: pid, Address: uint64(0xe000 + i*64), Type: FilterSeen,
		}, false)
		if !accepted {
			t.Fatal("test setup could not occupy the Filter update ports")
		}
	}
	cache.observeLocalReadDemand(3, localPrefetchDemand(cache, pid, 0xd080))
	stats := cache.GetLocalFilterPrefetchStats()
	if stats.Candidates != 1 || stats.PatternInstallDrops != 1 ||
		stats.Issued != 0 || cache.localPrefetchCandidate != nil {
		t.Fatalf("early candidate bypassed failed PATTERN admission: %+v", stats)
	}
}

func TestLocalFilterPrefetchDemandFeedbackMarksInflightLineUseful(t *testing.T) {
	cache := newLocalFilterPrefetchTestCache()
	pid := vm.PID(4)
	for i, address := range []uint64{0x2000, 0x2040, 0x2080} {
		cache.observeLocalReadDemand(
			sim.VTimeInSec(i+1), localPrefetchDemand(cache, pid, address))
	}
	cache.runLocalFilterPrefetch(4)
	prefetch := cache.dirStageBuffer.Peek().(*transaction)
	line, _ := getCacheLineID(0x20c0, cache.log2BlockSize)
	entry := cache.mshr.Add(pid, line)
	entry.Requests = append(entry.Requests, prefetch)
	cache.observeLocalReadDemand(5, localPrefetchDemand(cache, pid, 0x20c0))
	stats := cache.GetLocalFilterPrefetchStats()
	if stats.Useful != 1 {
		t.Fatalf("real demand did not consume inflight prefetch: %+v", stats)
	}
	if cache.filterPrefetcher.Stats().UsefulFeedback != 1 {
		t.Fatal("useful feedback did not reach the shared predictor state")
	}
}

func TestSharedL2FrontendPredictorPreservesStreamAcrossSlices(t *testing.T) {
	cache0 := newLocalFilterPrefetchTestCache()
	cache1 := newLocalFilterPrefetchTestCache()
	ConnectFilterCoupledPrefetchGroup([]*Cache{cache0, cache1}, 8, 128, 4096)
	pid := vm.PID(5)
	demands := []struct {
		cache   *Cache
		address uint64
	}{
		{cache0, 0x1000}, {cache0, 0x1040},
		{cache1, 0x1080},
	}
	for i, demand := range demands {
		demand.cache.observeLocalReadDemand(
			sim.VTimeInSec(i+1),
			localPrefetchDemand(demand.cache, pid, demand.address),
		)
	}
	if cache0.localPrefetchCandidate != nil ||
		cache1.localPrefetchCandidate == nil {
		t.Fatal("cross-slice stream did not dispatch candidate to owning slice")
	}
	if !cache1.runLocalFilterPrefetch(5) ||
		cache1.GetLocalFilterPrefetchStats().Issued != 1 {
		t.Fatal("owning slice did not issue the shared predictor candidate")
	}
}

func TestSharedPatternStaysAtInstallerWhileCandidateTargetsAnotherSlice(
	t *testing.T,
) {
	cache0 := newLocalFilterPrefetchTestCache()
	cache1 := newLocalFilterPrefetchTestCache()
	ConnectFilterCoupledPrefetchGroup([]*Cache{cache0, cache1}, 8, 128, 4096)
	pid := vm.PID(26)
	for i, address := range []uint64{0x1000, 0x1040, 0x1080} {
		cache1.observeLocalReadDemand(
			sim.VTimeInSec(i+1),
			localPrefetchDemand(cache1, pid, address),
		)
	}
	if !cache1.runLocalFilterPrefetch(4) {
		t.Fatal("first candidate did not issue")
	}
	cache1.observeLocalReadDemand(
		5, localPrefetchDemand(cache1, pid, 0x10c0))
	candidate := cache0.localPrefetchCandidate
	if candidate == nil {
		t.Fatal("next interleaved candidate did not reach the other slice")
	}
	if candidate.filters[0] != cache1.requestFilter ||
		candidate.filters[1] != cache0.requestFilter ||
		candidate.filters[2] != cache0.requestFilter {
		t.Fatal("PATTERN and line-local lookups used incorrect slice ownership")
	}
	if got := cache1.localPrefetchPatternFilters[candidate.patternKey]; got != cache1.requestFilter {
		t.Fatal("PATTERN metadata migrated away from its installer")
	}
}

func TestLocalDemandMergeReportsLatePrefetch(t *testing.T) {
	cache := newLocalFilterPrefetchTestCache()
	pid := vm.PID(6)
	for i, address := range []uint64{0x3000, 0x3040, 0x3080} {
		cache.observeLocalReadDemand(
			sim.VTimeInSec(i+1), localPrefetchDemand(cache, pid, address))
	}
	cache.runLocalFilterPrefetch(4)
	line, _ := getCacheLineID(0x30c0, cache.log2BlockSize)
	entry := cache.mshr.Add(pid, line)
	entry.Requests = append(
		entry.Requests, cache.dirStageBuffer.Peek().(*transaction))
	cache.markLocalPrefetchDRAMIssued(pid, line)
	cache.observeLocalReadDemand(5, localPrefetchDemand(cache, pid, line))
	stats := cache.GetLocalFilterPrefetchStats()
	if stats.Useful != 1 || stats.DemandMerges != 1 || stats.Late != 1 ||
		stats.LateAfterDRAMIssue != 1 || stats.AdditionalDRAMReads != 1 {
		t.Fatalf("late merge accounting = %+v", stats)
	}
}

func TestLocalDemandAheadOfPrefetchIsNotCountedUseful(t *testing.T) {
	cache := newLocalFilterPrefetchTestCache()
	pid := vm.PID(14)
	for i, address := range []uint64{0xb000, 0xb040, 0xb080} {
		cache.observeLocalReadDemand(
			sim.VTimeInSec(i+1), localPrefetchDemand(cache, pid, address))
	}
	cache.runLocalFilterPrefetch(4)
	cache.observeLocalReadDemand(5, localPrefetchDemand(cache, pid, 0xb0c0))
	cache.finishLocalPrefetchWithoutFill(pid, 0xb0c0)
	stats := cache.GetLocalFilterPrefetchStats()
	if stats.Useful != 0 || stats.DemandWonRaces != 1 ||
		stats.RedundantRaces != 1 {
		t.Fatalf("demand-won race accounting = %+v", stats)
	}
	if predictor := cache.filterPrefetcher.Stats(); predictor.UsefulFeedback != 0 || predictor.LateFeedback != 1 {
		t.Fatalf("demand-won feedback = %+v", predictor)
	}
}

func TestDemandArrivalAdvancesLookaheadBeforeGeneratingNextCandidate(t *testing.T) {
	cache := newLocalFilterPrefetchTestCache()
	ConnectFilterCoupledPrefetchGroup([]*Cache{cache}, 8, 64, 4096)
	pid := vm.PID(18)
	for i, address := range []uint64{0x11000, 0x11040, 0x11080} {
		cache.observeLocalReadDemand(
			sim.VTimeInSec(i+1), localPrefetchDemand(cache, pid, address))
	}
	if !cache.runLocalFilterPrefetch(4) {
		t.Fatal("first candidate did not issue")
	}
	cache.observeLocalReadDemand(
		5, localPrefetchDemand(cache, pid, 0x110c0))
	next := cache.localPrefetchCandidate
	if next == nil || next.token.Lookahead != 2 || next.address != 0x11140 {
		t.Fatalf("next candidate did not use immediate late feedback: %+v", next)
	}
	cache.finishLocalPrefetchWithoutFill(pid, 0x110c0)
	predictor := cache.filterPrefetcher.Stats()
	if predictor.LateFeedback != 1 ||
		predictor.LateDistanceIncreases != 1 ||
		cache.GetLocalFilterPrefetchStats().DemandWonRaces != 1 {
		t.Fatalf("demand-won feedback was not single-counted: %+v", predictor)
	}
}

func TestLateArrivalThenPrefetchLedMergeCountsUsefulOnce(t *testing.T) {
	cache := newLocalFilterPrefetchTestCache()
	ConnectFilterCoupledPrefetchGroup([]*Cache{cache}, 8, 64, 4096)
	pid := vm.PID(19)
	for i, address := range []uint64{0x12000, 0x12040, 0x12080} {
		cache.observeLocalReadDemand(
			sim.VTimeInSec(i+1), localPrefetchDemand(cache, pid, address))
	}
	if !cache.runLocalFilterPrefetch(4) {
		t.Fatal("candidate did not issue")
	}
	prefetch := cache.dirStageBuffer.Peek().(*transaction)
	cache.observeLocalReadDemand(
		5, localPrefetchDemand(cache, pid, 0x120c0))
	line, _ := getCacheLineID(0x120c0, cache.log2BlockSize)
	entry := cache.mshr.Add(pid, line)
	entry.Requests = append(entry.Requests, prefetch)
	cache.markLocalPrefetchUseful(pid, line)

	stats := cache.GetLocalFilterPrefetchStats()
	predictor := cache.filterPrefetcher.Stats()
	if stats.Useful != 1 || stats.Late != 1 ||
		predictor.UsefulFeedback != 1 || predictor.LateFeedback != 1 ||
		predictor.LateDistanceIncreases != 1 {
		t.Fatalf("late arrival/merge feedback = local %+v predictor %+v", stats, predictor)
	}
}

func TestLocalCandidateDropsWhenControllerHasDemandWork(t *testing.T) {
	cache := newLocalFilterPrefetchTestCache()
	pid := vm.PID(7)
	for i, address := range []uint64{0x4000, 0x4040, 0x4080, 0x40c0} {
		cache.observeLocalReadDemand(
			sim.VTimeInSec(i+1), localPrefetchDemand(cache, pid, address))
	}
	cache.writeBufferBuffer.Push(&transaction{})
	cache.runLocalFilterPrefetch(5)
	stats := cache.GetLocalFilterPrefetchStats()
	if stats.ControllerBusyDrops != 1 || stats.Issued != 0 {
		t.Fatalf("controller-busy candidate was not dropped: %+v", stats)
	}
}

func TestLocalCandidateDropsWhenMSHRIsFull(t *testing.T) {
	cache := newLocalFilterPrefetchTestCache()
	pid := vm.PID(12)
	for i, address := range []uint64{0x9000, 0x9040, 0x9080, 0x90c0} {
		cache.observeLocalReadDemand(
			sim.VTimeInSec(i+1), localPrefetchDemand(cache, pid, address))
	}
	cache.mshrCapacity = 0
	if !cache.runLocalFilterPrefetch(5) {
		t.Fatal("candidate did not finish its non-blocking MSHR check")
	}
	stats := cache.GetLocalFilterPrefetchStats()
	if stats.MSHRDrops != 1 || stats.Issued != 0 {
		t.Fatalf("MSHR-full candidate was not dropped: %+v", stats)
	}
}

func TestDemandCoveredMSHRCandidateTrainsFartherLookahead(t *testing.T) {
	cache := newLocalFilterPrefetchTestCache()
	ConnectFilterCoupledPrefetchGroup([]*Cache{cache}, 8, 64, 4096)
	pid := vm.PID(20)
	for i, address := range []uint64{0x13000, 0x13040, 0x13080} {
		cache.observeLocalReadDemand(
			sim.VTimeInSec(i+1), localPrefetchDemand(cache, pid, address))
	}
	line, _ := getCacheLineID(0x130c0, cache.log2BlockSize)
	entry := cache.mshr.Add(pid, line)
	entry.Requests = append(entry.Requests, &transaction{})
	if !cache.runLocalFilterPrefetch(4) {
		t.Fatal("demand-covered candidate did not finish")
	}
	stats := cache.GetLocalFilterPrefetchStats()
	predictor := cache.filterPrefetcher.Stats()
	if stats.MSHRDrops != 1 || stats.DemandMSHRCoveredDrops != 1 ||
		stats.PrefetchMSHRCoveredDrops != 0 || stats.Issued != 0 ||
		predictor.DemandCoveredFeedback != 1 ||
		predictor.CoveredDistanceIncreases != 1 {
		t.Fatalf("demand-covered MSHR feedback = local %+v predictor %+v",
			stats, predictor)
	}
	cache.observeLocalReadDemand(
		5, localPrefetchDemand(cache, pid, 0x130c0))
	next := cache.localPrefetchCandidate
	if next == nil || next.token.Lookahead != 2 ||
		next.address != 0x13140 {
		t.Fatalf("demand-covered MSHR did not train farther lookahead: %+v", next)
	}
}

func TestLocalCandidateDropsWhenOutputIsBusy(t *testing.T) {
	cache := newLocalFilterPrefetchTestCache()
	pid := vm.PID(13)
	for i, address := range []uint64{0xa000, 0xa040, 0xa080, 0xa0c0} {
		cache.observeLocalReadDemand(
			sim.VTimeInSec(i+1), localPrefetchDemand(cache, pid, address))
	}
	for cache.dirStageBuffer.CanPush() {
		cache.dirStageBuffer.Push(&transaction{})
	}
	if !cache.runLocalFilterPrefetch(5) {
		t.Fatal("candidate did not finish its non-blocking output check")
	}
	stats := cache.GetLocalFilterPrefetchStats()
	if stats.OutputBusyDrops != 1 || stats.Issued != 0 {
		t.Fatalf("output-busy candidate was not dropped: %+v", stats)
	}
}

func TestLocalCandidatePreservesDemandMSHRHeadroom(t *testing.T) {
	cache := newLocalFilterPrefetchTestCache()
	pid := vm.PID(15)
	for i, address := range []uint64{0xc000, 0xc040, 0xc080, 0xc0c0} {
		cache.observeLocalReadDemand(
			sim.VTimeInSec(i+1), localPrefetchDemand(cache, pid, address))
	}
	cache.mshrCapacity = 1
	if !cache.runLocalFilterPrefetch(5) {
		t.Fatal("candidate did not finish its MSHR-headroom check")
	}
	stats := cache.GetLocalFilterPrefetchStats()
	if stats.MSHRHeadroomDrops != 1 || stats.Issued != 0 {
		t.Fatalf("candidate consumed the final demand MSHR credit: %+v", stats)
	}
}

func TestLocalCandidateDropsWhenDemandMissIsLive(t *testing.T) {
	cache := newLocalFilterPrefetchTestCache()
	pid := vm.PID(27)
	for i, address := range []uint64{0x18000, 0x18040, 0x18080} {
		cache.observeLocalReadDemand(
			sim.VTimeInSec(i+1), localPrefetchDemand(cache, pid, address))
	}
	entry := cache.mshr.Add(pid, 0x19000)
	entry.Requests = append(entry.Requests, &transaction{})
	if !cache.runLocalFilterPrefetch(4) {
		t.Fatal("candidate did not finish its demand-path-idle check")
	}
	stats := cache.GetLocalFilterPrefetchStats()
	if stats.DemandPathBusyDrops != 1 || stats.Issued != 0 {
		t.Fatalf("demand-busy candidate was not dropped: %+v", stats)
	}
}

func TestTimelyProvenStreamMayIssueWithLiveDemandMSHR(t *testing.T) {
	cache := newLocalFilterPrefetchTestCache()
	ConnectFilterCoupledPrefetchGroup([]*Cache{cache}, 8, 64, 4096)
	pid := vm.PID(28)
	for i, address := range []uint64{0x19000, 0x19040, 0x19080} {
		cache.observeLocalReadDemand(
			sim.VTimeInSec(i+1), localPrefetchDemand(cache, pid, address))
	}
	if !cache.runLocalFilterPrefetch(4) {
		t.Fatal("training candidate did not issue")
	}
	prefetch := cache.dirStageBuffer.Pop().(*transaction)
	line, _ := getCacheLineID(prefetch.read.Address, cache.log2BlockSize)
	block := cache.directory.FindVictim(line)
	block.PID = pid
	block.Tag = line
	block.IsValid = true
	block.IsLocked = false
	cache.completeLocalPrefetchFill(prefetch, block)
	cache.observeLocalReadDemand(
		5, localPrefetchDemand(cache, pid, prefetch.read.Address))

	if candidate := cache.localPrefetchCandidate; candidate == nil {
		t.Fatal("timely feedback did not generate the next candidate")
	}
	entry := cache.mshr.Add(pid, 0x1a000)
	entry.Requests = append(entry.Requests, &transaction{})
	// Model another proven prefetch already in flight. Timely streams use
	// existing free MSHR headroom instead of a fixed one-line depth.
	cache.localPrefetchOutstanding = 1
	if !cache.runLocalFilterPrefetch(6) {
		t.Fatal("proven candidate did not finish its admission check")
	}
	stats := cache.GetLocalFilterPrefetchStats()
	if stats.Issued != 2 || stats.Outstanding != 2 ||
		stats.TimelyProofBusyIssues != 1 ||
		stats.DemandPathBusyDrops != 0 {
		t.Fatalf("timely-proof admission = %+v", stats)
	}
}

func TestLocalPrefetchAllowsOneOutstandingLinePerSlice(t *testing.T) {
	cache := newLocalFilterPrefetchTestCache()
	pid := vm.PID(21)
	for i, address := range []uint64{0x14000, 0x14040, 0x14080} {
		read := localPrefetchDemand(cache, pid, address)
		read.StreamID = 1
		cache.observeLocalReadDemand(sim.VTimeInSec(i+1), read)
	}
	if !cache.runLocalFilterPrefetch(4) {
		t.Fatal("first stream did not issue its candidate")
	}
	for i, address := range []uint64{0x15000, 0x15040, 0x15080} {
		read := localPrefetchDemand(cache, pid, address)
		read.StreamID = 2
		cache.observeLocalReadDemand(sim.VTimeInSec(i+5), read)
	}
	if !cache.runLocalFilterPrefetch(8) {
		t.Fatal("second stream did not complete its bounded admission check")
	}
	stats := cache.GetLocalFilterPrefetchStats()
	if stats.Issued != 1 || stats.Outstanding != 1 ||
		stats.OutstandingCapacityDrops != 1 {
		t.Fatalf("per-slice outstanding bound = %+v", stats)
	}
}

func TestLocalPrefetchLookaheadTracksAverageLiveMSHRHorizon(t *testing.T) {
	cache0 := newLocalFilterPrefetchTestCache()
	cache1 := newLocalFilterPrefetchTestCache()
	ConnectFilterCoupledPrefetchGroup([]*Cache{cache0, cache1}, 8, 128, 4096)
	pid := vm.PID(22)
	for i := 0; i < 2; i++ {
		entry := cache0.mshr.Add(pid, uint64(0x16000+i*64))
		entry.Requests = append(entry.Requests, &transaction{})
		entry = cache1.mshr.Add(pid, uint64(0x17000+i*64))
		entry.Requests = append(entry.Requests, &transaction{})
	}
	if got := cache0.localPrefetchMinimumLookahead(); got != 3 {
		t.Fatalf("minimum lookahead = %d, want average demand horizon + 1 = 3", got)
	}
}

func TestPredictorOnlyCountsCandidateWithoutIssuing(t *testing.T) {
	cache := newLocalFilterPrefetchTestCache()
	cache.filterPrefetchPredictorOnly = true
	pid := vm.PID(8)
	for i, address := range []uint64{0x5000, 0x5040, 0x5080, 0x50c0} {
		cache.observeLocalReadDemand(
			sim.VTimeInSec(i+1), localPrefetchDemand(cache, pid, address))
	}
	stats := cache.GetLocalFilterPrefetchStats()
	if stats.PredictorOnlyCandidates != 2 || stats.Issued != 0 ||
		cache.localPrefetchCandidate != nil {
		t.Fatalf("predictor-only mode issued or lost coverage: %+v", stats)
	}
}

func TestUngatedPrefetchDoesNotConsultResidentMetadata(t *testing.T) {
	cache := newLocalFilterPrefetchTestCache()
	cache.filterPrefetchUngated = true
	pid := vm.PID(9)
	candidateLine := uint64(0x6100)
	cache.requestFilter.ScheduleUpdate(0, TypedFilterKey{
		PID: pid, Address: candidateLine, Type: FilterResident,
	}, false)
	for i, address := range []uint64{0x6000, 0x6040, 0x6080, 0x60c0} {
		cache.observeLocalReadDemand(
			sim.VTimeInSec(i+1), localPrefetchDemand(cache, pid, address))
	}
	if !cache.runLocalFilterPrefetch(5) {
		t.Fatal("ungated candidate did not make progress")
	}
	stats := cache.GetLocalFilterPrefetchStats()
	if stats.UngatedCandidates != 1 || stats.Issued != 1 ||
		stats.ResidentPositiveDrops != 0 {
		t.Fatalf("ungated mode unexpectedly consulted Filter: %+v", stats)
	}
}

func TestLocalPrefetchNeverReplacesAValidLine(t *testing.T) {
	cache := newLocalFilterPrefetchTestCache()
	pid := vm.PID(10)
	for _, set := range cache.directory.GetSets() {
		for _, block := range set.Blocks {
			block.IsValid = true
		}
	}
	for i, address := range []uint64{0x7000, 0x7040, 0x7080, 0x70c0} {
		cache.observeLocalReadDemand(
			sim.VTimeInSec(i+1), localPrefetchDemand(cache, pid, address))
	}
	if !cache.runLocalFilterPrefetch(5) {
		t.Fatal("candidate did not finish its non-blocking admission check")
	}
	stats := cache.GetLocalFilterPrefetchStats()
	if stats.VictimDrops != 1 || stats.Issued != 0 {
		t.Fatalf("prefetch replaced a valid line: %+v", stats)
	}
}

func TestFlushAccountsUnusedPrefetchFeedback(t *testing.T) {
	cache := newLocalFilterPrefetchTestCache()
	pid := vm.PID(11)
	for i, address := range []uint64{0x8000, 0x8040, 0x8080, 0x80c0} {
		cache.observeLocalReadDemand(
			sim.VTimeInSec(i+1), localPrefetchDemand(cache, pid, address))
	}
	if !cache.runLocalFilterPrefetch(5) {
		t.Fatal("candidate did not issue")
	}
	trans := cache.dirStageBuffer.Peek().(*transaction)
	line, _ := getCacheLineID(trans.read.Address, cache.log2BlockSize)
	cache.completeLocalPrefetchFill(trans, cache.directory.FindVictim(line))
	before := cache.GetLocalFilterPrefetchStats()
	if before.CurrentPrefetchOnlyLines != 1 ||
		before.PeakPrefetchOnlyLines != 1 {
		t.Fatalf("prefetch-only occupancy before reset = %+v", before)
	}
	cache.resetLocalFilterPrefetch()
	after := cache.GetLocalFilterPrefetchStats()
	if after.Unused != 1 || after.UnusedResetRetirements != 1 ||
		after.UnusedEvictions != 0 || after.CurrentPrefetchOnlyLines != 0 {
		t.Fatalf("flush discarded an unused prefetch without precise feedback: %+v", after)
	}
}

func TestUnusedPrefetchEvictionIsSeparatedFromResetRetirement(t *testing.T) {
	cache := newLocalFilterPrefetchTestCache()
	pid := vm.PID(17)
	for i, address := range []uint64{0xf000, 0xf040, 0xf080} {
		cache.observeLocalReadDemand(
			sim.VTimeInSec(i+1), localPrefetchDemand(cache, pid, address))
	}
	if !cache.runLocalFilterPrefetch(4) {
		t.Fatal("candidate did not issue")
	}
	trans := cache.dirStageBuffer.Peek().(*transaction)
	line, _ := getCacheLineID(trans.read.Address, cache.log2BlockSize)
	block := cache.directory.FindVictim(line)
	cache.completeLocalPrefetchFill(trans, block)
	cache.untrackResidentBlock(block)
	stats := cache.GetLocalFilterPrefetchStats()
	if stats.Unused != 1 || stats.UnusedEvictions != 1 ||
		stats.UnusedResetRetirements != 0 ||
		stats.CurrentPrefetchOnlyLines != 0 ||
		stats.PeakPrefetchOnlyLines != 1 {
		t.Fatalf("unused eviction accounting = %+v", stats)
	}
}
