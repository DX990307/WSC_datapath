package writeback

import (
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/sarchlab/akita/v3/mem/cache"
	"github.com/sarchlab/akita/v3/mem/mem"
	"github.com/sarchlab/akita/v3/sim"
)

func TestGranularityBuildsTwoIndependent64BReads(t *testing.T) {
	parentRead := mem.ReadReqBuilder{}.
		WithPID(7).
		WithAddress(0x1040).
		WithByteSize(64).
		WithStreamID(9).
		WithInfo("demand").
		Build()
	trans := &transaction{
		read: parentRead, fetchPID: 7, fetchAddress: 0x1040,
	}
	wb := &writeBufferStage{cache: &Cache{log2BlockSize: 6}}
	demand, sibling := wb.buildGranularityPairedReads(
		trans, 0x1000, nil, 64)

	if demand.ID == sibling.ID {
		t.Fatal("paired members shared a request ID")
	}
	if demand.PairedReadID == "" ||
		demand.PairedReadID != sibling.PairedReadID {
		t.Fatal("paired members did not share one non-empty Pair ID")
	}
	if demand.PairedReadPart != mem.PairedReadDemand ||
		sibling.PairedReadPart != mem.PairedReadSibling {
		t.Fatalf("wrong pair roles: demand=%d sibling=%d",
			demand.PairedReadPart, sibling.PairedReadPart)
	}
	if demand.Address != 0x1040 || sibling.Address != 0x1000 {
		t.Fatalf("wrong independent addresses: demand=%#x sibling=%#x",
			demand.Address, sibling.Address)
	}
	if demand.AccessByteSize != 64 || sibling.AccessByteSize != 64 {
		t.Fatalf("paired members must remain 64B: demand=%d sibling=%d",
			demand.AccessByteSize, sibling.AccessByteSize)
	}
	descriptor := mem.PairedReadReqBuilder{}.
		WithReads(demand, sibling).
		Build()
	if descriptor.Demand != demand || descriptor.Sibling != sibling {
		t.Fatal("descriptor did not preserve independent child requests")
	}
}

func TestGranularityBuildsReadyDemandPairAsIndependent64BReads(t *testing.T) {
	firstReq := mem.ReadReqBuilder{}.
		WithPID(7).
		WithAddress(0x1000).
		WithByteSize(64).
		WithStreamID(9).
		WithLocalStreamID(11).
		WithInfo("first-demand").
		Build()
	peerReq := mem.ReadReqBuilder{}.
		WithPID(7).
		WithAddress(0x1040).
		WithByteSize(64).
		WithStreamID(10).
		WithLocalStreamID(12).
		WithInfo("peer-demand").
		Build()
	first := &transaction{
		read: firstReq, fetchPID: 7, fetchAddress: 0x1000,
	}
	peer := &transaction{
		read: peerReq, fetchPID: 7, fetchAddress: 0x1040,
	}
	wb := &writeBufferStage{cache: &Cache{log2BlockSize: 6}}
	demandRead, peerRead := wb.buildDemandPairedReads(
		first, peer, nil, 64)

	if demandRead.ID == peerRead.ID || demandRead.PairedReadID == "" ||
		demandRead.PairedReadID != peerRead.PairedReadID {
		t.Fatal("ready demands were not represented as independent pair members")
	}
	if demandRead.Address != 0x1000 || peerRead.Address != 0x1040 ||
		demandRead.AccessByteSize != 64 || peerRead.AccessByteSize != 64 {
		t.Fatalf("wrong ready-demand pair geometry: first=%#x/%d peer=%#x/%d",
			demandRead.Address, demandRead.AccessByteSize,
			peerRead.Address, peerRead.AccessByteSize)
	}
	if demandRead.PairedReadPart != mem.PairedReadDemand ||
		peerRead.PairedReadPart != mem.PairedReadSibling {
		t.Fatal("ready-demand pair roles were not preserved")
	}
	if peerRead.StreamID != 10 || peerRead.LocalStreamID != 12 ||
		peerRead.Info != "peer-demand" {
		t.Fatal("peer request metadata was replaced by speculative metadata")
	}
}

func TestGranularityFindsOnlyReadyUnissuedRealDemand(t *testing.T) {
	read := mem.ReadReqBuilder{}.
		WithPID(1).
		WithAddress(0x1040).
		WithByteSize(64).
		Build()
	peer := &transaction{
		action: writeBufferFetch, read: read,
		writeBufferReady: true, fetchPID: 1, fetchAddress: 0x1040,
	}
	entry := cache.NewMSHREntry()
	entry.Requests = append(entry.Requests, peer)
	if got := readyUnissuedDemand(entry); got != peer {
		t.Fatal("ready real demand was not recognized")
	}

	peer.writeBufferReady = false
	if readyUnissuedDemand(entry) != nil {
		t.Fatal("demand not yet in the write buffer was paired")
	}
	peer.writeBufferReady = true
	peer.fetchReadReq = mem.ReadReqBuilder{}.WithByteSize(64).Build()
	if readyUnissuedDemand(entry) != nil {
		t.Fatal("already-issued demand was paired a second time")
	}
	peer.fetchReadReq = nil
	peer.prefetch = true
	if readyUnissuedDemand(entry) != nil {
		t.Fatal("speculative request was classified as a real-demand peer")
	}
}

func TestGranularityIssuesReadyDemandPairWithoutCreatingSibling(t *testing.T) {
	component := sim.NewTickingComponent("L2", nil, sim.GHz, nil)
	sendBuffer := sim.NewBuffer("SendBuffer", 4)
	cacheModule := &Cache{
		TickingComponent: component,
		bottomSender:     sim.NewBufferedSender(nil, sendBuffer),
		log2BlockSize:    6,
	}
	wb := &writeBufferStage{cache: cacheModule, maxInflightFetch: 8}
	makeTrans := func(address uint64) *transaction {
		read := mem.ReadReqBuilder{}.
			WithPID(1).
			WithAddress(address).
			WithByteSize(64).
			Build()
		return &transaction{
			action: writeBufferFetch, read: read,
			fetchPID: 1, fetchAddress: address,
			writeBufferReady: true,
		}
	}
	demand := makeTrans(0x1000)
	peer := makeTrans(0x1040)
	entry := cache.NewMSHREntry()
	entry.Requests = append(entry.Requests, peer)

	if !wb.issueReadyDemandPair(0, demand, entry, nil, 64) {
		t.Fatal("ready adjacent real demands did not issue as a pair")
	}
	if sendBuffer.Size() != 1 {
		t.Fatalf("paired demands used %d frontend descriptors", sendBuffer.Size())
	}
	descriptor, ok := sendBuffer.Peek().(*mem.PairedReadReq)
	if !ok || descriptor.Demand.AccessByteSize != 64 ||
		descriptor.Sibling.AccessByteSize != 64 {
		t.Fatal("ready-demand descriptor did not retain two 64-B members")
	}
	if len(wb.inflightFetch) != 2 || demand.fetchReadReq == nil ||
		peer.fetchReadReq == nil || !peer.pairedDemandQueueEntry {
		t.Fatal("ready-demand pair lifecycle was not installed")
	}
	stats := cacheModule.granularityStats
	if stats.DemandPairReadyOpportunities != 1 ||
		stats.DemandPairAggregates != 1 ||
		stats.AcceptedExpansions != 0 || stats.SiblingFills != 0 {
		t.Fatalf("real-demand pairing was counted as speculation: %+v", stats)
	}
	if cacheModule.localMemoryPathStats.DRAMReadRequests != 2 {
		t.Fatal("ready-demand pair did not count two physical read requests")
	}
}

func TestGranularityRetiresAlreadyIssuedPeerQueueEntry(t *testing.T) {
	buffer := sim.NewBuffer("WriteBuffer", 2)
	peer := &transaction{
		action: writeBufferFetch,
		fetchReadReq: mem.ReadReqBuilder{}.
			WithByteSize(64).
			Build(),
		writeBufferReady:       true,
		pairedDemandQueueEntry: true,
	}
	buffer.Push(peer)
	wb := &writeBufferStage{cache: &Cache{writeBufferBuffer: buffer}}
	if !wb.processNewTransaction(0) {
		t.Fatal("already-issued peer queue entry did not retire")
	}
	if buffer.Size() != 0 || peer.pairedDemandQueueEntry ||
		peer.writeBufferReady {
		t.Fatal("already-issued peer queue state was not cleared")
	}
}

func TestGranularityResponsesMatchOneInflightMemberByRequestID(t *testing.T) {
	demand := mem.ReadReqBuilder{}.
		WithByteSize(64).
		WithPairedRead("pair", mem.PairedReadDemand).
		Build()
	sibling := mem.ReadReqBuilder{}.
		WithByteSize(64).
		WithPairedRead("pair", mem.PairedReadSibling).
		Build()
	demandTrans := &transaction{fetchReadReq: demand}
	siblingTrans := &transaction{fetchReadReq: sibling}
	wb := &writeBufferStage{inflightFetch: []*transaction{
		demandTrans, siblingTrans,
	}}

	fetches := wb.findInflightFetchesByFetchReadReqID(demand.ID)
	if len(fetches) != 1 || fetches[0] != demandTrans {
		t.Fatal("demand response was coupled to sibling completion")
	}
	wb.removeInflightFetch(demandTrans)
	if len(wb.inflightFetch) != 1 || wb.inflightFetch[0] != siblingTrans {
		t.Fatal("retiring demand also retired its unfinished sibling")
	}
}

func TestGranularityPredictorOnlyProducesDirectSiblingOnPairBoundary(
	t *testing.T,
) {
	predictor := NewPageLocalDemandStridePredictor(8, 64, 4096)
	predictor.EnableCandidateOnPatternEstablishment()
	stream := DemandStreamKey{PID: 1, StreamID: 7}
	_ = predictor.ObserveRealDemand(stream, 0x1000)
	_ = predictor.ObserveRealDemand(stream, 0x1040)
	observation := predictor.ObserveRealDemand(stream, 0x1080)
	if !observation.HasCandidate || observation.Candidate != 0x10c0 {
		t.Fatalf("expected direct upper sibling, got %+v", observation)
	}
	predictor.SetPatternInstalled(observation.Token, true)
	observation = predictor.ObserveRealDemand(stream, 0x10c0)
	if !observation.HasCandidate || observation.Candidate != 0x1100 {
		t.Fatalf("expected next stride candidate, got %+v", observation)
	}
	// 0x1100 is not the sibling of 0x10c0; the M1 admission path must drop
	// this cross-region prediction and wait for the lower half demand.
	lineBytes := uint64(64)
	base := uint64(0x10c0) & ^(2*lineBytes - 1)
	sibling := base
	if sibling == 0x10c0 {
		sibling += lineBytes
	}
	if observation.Candidate == sibling {
		t.Fatal("cross-region candidate was incorrectly classified as sibling")
	}
}

func TestGranularityReliableNegativesSkipExactSiblingLookups(t *testing.T) {
	candidate := &granularityCandidate{gated: true}
	checkResident, checkPending := granularityExactLookupPlan(candidate)
	if checkResident || checkPending {
		t.Fatalf("reliable negatives must skip both exact lookups, got resident=%v pending=%v",
			checkResident, checkPending)
	}
}

func TestGranularityFilterPositivesRequireExactConfirmation(t *testing.T) {
	candidate := &granularityCandidate{gated: true, results: [2]bool{true, false}}
	checkResident, checkPending := granularityExactLookupPlan(candidate)
	if !checkResident || checkPending {
		t.Fatalf("resident positive must be confirmed alone, got resident=%v pending=%v",
			checkResident, checkPending)
	}

	candidate.results = [2]bool{false, true}
	checkResident, checkPending = granularityExactLookupPlan(candidate)
	if checkResident || !checkPending {
		t.Fatalf("pending positive must be confirmed alone, got resident=%v pending=%v",
			checkResident, checkPending)
	}
}

func TestGranularityWithoutFilterChecksExactState(t *testing.T) {
	checkResident, checkPending := granularityExactLookupPlan(
		&granularityCandidate{gated: false})
	if !checkResident || !checkPending {
		t.Fatalf("ungated diagnostic must check exact state, got resident=%v pending=%v",
			checkResident, checkPending)
	}
}

func TestGranularityIgnoresWritesAndSpeculativeReads(t *testing.T) {
	c := &Cache{granularityAdaptationEnabled: true}
	write := mem.WriteReqBuilder{}.
		WithAddress(0x1000).
		WithData(make([]byte, 4)).
		WithDirtyMask([]bool{true, true, true, true}).
		Build()
	c.observeGranularityDemand(0, &transaction{write: write})
	read := mem.ReadReqBuilder{}.
		WithAddress(0x1000).
		WithByteSize(64).
		Build()
	c.observeGranularityDemand(0, &transaction{read: read, prefetch: true})
	if c.granularityStats.RealReadDemands != 0 {
		t.Fatal("write or speculative read trained the M1 predictor")
	}
}

func TestGranularityPendingTracksOrdinaryDemandLifecycle(t *testing.T) {
	filter := NewTypedCuckooFilter(TypedFilterConfig{
		Mode: TypedFilterExact, LookupWidth: 2, UpdateWidth: 2,
	})
	c := &Cache{
		granularityAdaptationEnabled: true,
		requestFilter:                filter,
	}
	key := TypedFilterKey{PID: 1, Address: 0x1000, Type: FilterGranularityPending}
	c.trackGranularityPending(0, key.PID, key.Address)
	if !filter.ExactContains(key) {
		t.Fatal("ordinary demand was not summarized as granularity pending")
	}
	c.untrackGranularityPending(1, key.PID, key.Address)
	if filter.ExactContains(key) {
		t.Fatal("completed ordinary demand remained granularity pending")
	}
}

func TestGranularityUnreliableFilterFallsBack(t *testing.T) {
	filter := NewTypedCuckooFilter(TypedFilterConfig{
		Mode: TypedFilterCuckoo, LookupWidth: 2, UpdateWidth: 2,
	})
	filter.stats.ByType[FilterResident].Reliable = false
	resident, ok := filter.StartLookup(0, TypedFilterKey{
		PID: 1, Address: 0x1000, Type: FilterResident,
	})
	if !ok {
		t.Fatal("resident lookup was not accepted")
	}
	pending, ok := filter.StartLookup(0, TypedFilterKey{
		PID: 1, Address: 0x1000, Type: FilterGranularityPending,
	})
	if !ok {
		t.Fatal("pending lookup was not accepted")
	}
	c := &Cache{requestFilter: filter}
	candidate := &granularityCandidate{
		gated: true, lookups: [2]TypedFilterLookup{resident, pending},
		lookupSet: [2]bool{true, true},
	}
	if c.completeGranularityFilterLookups(0, candidate) {
		t.Fatal("unreliable Filter result admitted paired-read work")
	}
	if c.granularityStats.FilterUnreliableDrops != 1 {
		t.Fatalf("unreliable fallback counter = %d",
			c.granularityStats.FilterUnreliableDrops)
	}
}

func TestGranularityNotReadyFilterFallsBackWithoutWaiting(t *testing.T) {
	filter := NewTypedCuckooFilter(TypedFilterConfig{
		Mode: TypedFilterCuckoo, LookupLatencyCycles: 2,
		LookupWidth: 2, UpdateWidth: 2,
	})
	resident, ok := filter.StartLookup(0, TypedFilterKey{
		PID: 1, Address: 0x1000, Type: FilterResident,
	})
	if !ok {
		t.Fatal("resident lookup was not accepted")
	}
	pending, ok := filter.StartLookup(0, TypedFilterKey{
		PID: 1, Address: 0x1000, Type: FilterGranularityPending,
	})
	if !ok {
		t.Fatal("pending lookup was not accepted")
	}
	c := &Cache{requestFilter: filter}
	candidate := &granularityCandidate{
		gated: true, lookups: [2]TypedFilterLookup{resident, pending},
		lookupSet: [2]bool{true, true},
	}
	if c.completeGranularityFilterLookups(0, candidate) {
		t.Fatal("not-ready Filter result admitted paired-read work")
	}
	if c.granularityStats.FilterNotReadyDrops != 1 {
		t.Fatalf("not-ready fallback counter = %d",
			c.granularityStats.FilterNotReadyDrops)
	}
}

func TestGranularityPatternUsesSharedTypedFilter(t *testing.T) {
	filter := NewTypedCuckooFilter(TypedFilterConfig{
		Mode: TypedFilterExact, LookupWidth: 8, UpdateWidth: 8,
	})
	predictor := NewPageLocalDemandStridePredictor(8, 64, 4096)
	predictor.EnableCandidateOnPatternEstablishment()
	stream := DemandStreamKey{PID: 1, Source: 2, StreamID: 3}
	_ = predictor.ObserveRealDemand(stream, 0x1000)
	_ = predictor.ObserveRealDemand(stream, 0x1040)
	observation := predictor.ObserveRealDemand(stream, 0x1080)
	if observation.InstallPattern == nil || !observation.HasCandidate {
		t.Fatal("predictor did not establish the test pattern")
	}
	if !filter.TryScheduleUpdate(0, *observation.InstallPattern, false) {
		t.Fatal("shared Filter rejected the PATTERN test insertion")
	}
	predictor.SetPatternInstalled(observation.Token, true)
	patternLookup, _ := filter.StartLookup(0, observation.Token.Key)
	residentLookup, _ := filter.StartLookup(0, TypedFilterKey{
		PID: 1, Address: observation.Candidate, Type: FilterResident,
	})
	pendingLookup, _ := filter.StartLookup(0, TypedFilterKey{
		PID: 1, Address: observation.Candidate,
		Type: FilterGranularityPending,
	})
	c := &Cache{
		requestFilter: filter, granularityPredictor: predictor,
		granularityPatternFilters: map[TypedFilterKey]*TypedCuckooFilter{
			observation.Token.Key: filter,
		},
	}
	candidate := &granularityCandidate{
		hasToken: true, token: observation.Token, gated: true,
		patternFilter: filter, patternLookup: patternLookup,
		patternLookupSet: true,
		lookups:          [2]TypedFilterLookup{residentLookup, pendingLookup},
		lookupSet:        [2]bool{true, true},
	}
	if !c.completeGranularityFilterLookups(0, candidate) {
		t.Fatal("installed PATTERN did not admit the candidate")
	}
	stats := filter.Stats().ByType[FilterPattern]
	if stats.Queries != 1 || stats.Positives != 1 {
		t.Fatalf("PATTERN lookup was not measured: %+v", stats)
	}
}

func TestGranularityPairProbeDoesNotCrossTrainPredictorStreams(t *testing.T) {
	ctrl := gomock.NewController(t)
	finder := NewMockLowModuleFinder(ctrl)
	port := NewMockPort(ctrl)
	finder.EXPECT().Find(gomock.Any()).Return(port).AnyTimes()
	predictor := NewPageLocalDemandStridePredictor(8, 64, 4096)
	predictor.EnableCandidateOnPatternEstablishment()
	c := &Cache{
		granularityAdaptationEnabled: true,
		granularityWithoutFilter:     true,
		granularityPredictor:         predictor,
		granularityPatternFilters:    make(map[TypedFilterKey]*TypedCuckooFilter),
		granularityByLine:            make(map[granularityLineKey]*granularityRecord),
		log2BlockSize:                6,
		lowModuleFinder:              finder,
	}
	for i, input := range []struct {
		address uint64
		local   uint64
	}{
		{address: 0x1000, local: 11},
		{address: 0x1040, local: 22},
		{address: 0x1080, local: 11},
	} {
		read := mem.ReadReqBuilder{}.
			WithPID(1).
			WithAddress(input.address).
			WithByteSize(64).
			WithStreamID(7).
			WithLocalStreamID(input.local).
			WithLocalPairHint(true).
			Build()
		trans := &transaction{read: read}
		c.observeGranularityDemand(0, trans)
		if trans.granularityCandidate == nil ||
			!trans.granularityCandidate.pairProbeOnly {
			t.Fatalf("request %d did not retain only a real-demand pair probe", i)
		}
	}
	if c.granularityStats.PatternsEstablished != 0 {
		t.Fatalf("cross-workgroup sequence established %d patterns",
			c.granularityStats.PatternsEstablished)
	}
}

func TestGranularityPairProbeUsesOnlyPendingFilter(t *testing.T) {
	ctrl := gomock.NewController(t)
	finder := NewMockLowModuleFinder(ctrl)
	port := NewMockPort(ctrl)
	finder.EXPECT().Find(gomock.Any()).Return(port).AnyTimes()
	filter := NewTypedCuckooFilter(TypedFilterConfig{
		Mode: TypedFilterExact, LookupWidth: 2, UpdateWidth: 2,
	})
	predictor := NewPageLocalDemandStridePredictor(8, 64, 4096)
	c := &Cache{
		granularityAdaptationEnabled: true,
		granularityPredictor:         predictor,
		granularityPatternFilters:    make(map[TypedFilterKey]*TypedCuckooFilter),
		granularityByLine:            make(map[granularityLineKey]*granularityRecord),
		requestFilter:                filter,
		log2BlockSize:                6,
		lowModuleFinder:              finder,
	}
	read := mem.ReadReqBuilder{}.
		WithPID(1).
		WithAddress(0x1000).
		WithByteSize(64).
		WithLocalStreamID(7).
		WithLocalPairHint(true).
		Build()
	trans := &transaction{read: read}
	c.observeGranularityDemand(0, trans)
	candidate := trans.granularityCandidate
	if candidate == nil || !candidate.pairProbeOnly ||
		!candidate.pairProbeBypass {
		t.Fatal("no-pattern demand did not create a pair-only probe")
	}
	if candidate.lookupSet[0] || candidate.lookupSet[1] {
		t.Fatalf("bypassed pair probe unexpectedly reserved lookup ports: %v",
			candidate.lookupSet)
	}
	if !c.completeGranularityFilterLookups(0, candidate) {
		t.Fatal("reliable pending negative did not complete")
	}
	checkResident, checkPending := granularityExactLookupPlan(candidate)
	if checkResident || checkPending {
		t.Fatalf("negative pair probe requested exact lookup: resident=%v pending=%v",
			checkResident, checkPending)
	}
	if c.granularityStats.DemandPairFilterProbes != 1 ||
		c.granularityStats.DemandPairFilterNegatives != 1 {
		t.Fatalf("pair probe counters = %+v", c.granularityStats)
	}
	residentStats := filter.Stats().ByType[FilterResident]
	pendingStats := filter.Stats().ByType[FilterGranularityPending]
	if residentStats.Queries != 0 || pendingStats.Queries != 1 {
		t.Fatalf("unexpected Filter queries: resident=%+v pending=%+v",
			residentStats, pendingStats)
	}
}

func TestGranularityNoHintAndNoPatternUsesBypassedPairProbe(t *testing.T) {
	ctrl := gomock.NewController(t)
	finder := NewMockLowModuleFinder(ctrl)
	port := NewMockPort(ctrl)
	finder.EXPECT().Find(gomock.Any()).Return(port).AnyTimes()
	filter := NewTypedCuckooFilter(TypedFilterConfig{
		Mode: TypedFilterExact, LookupWidth: 1, UpdateWidth: 1,
	})
	predictor := NewPageLocalDemandStridePredictor(8, 64, 4096)
	c := &Cache{
		granularityAdaptationEnabled: true,
		granularityPredictor:         predictor,
		granularityPatternFilters:    make(map[TypedFilterKey]*TypedCuckooFilter),
		granularityByLine:            make(map[granularityLineKey]*granularityRecord),
		requestFilter:                filter,
		log2BlockSize:                6,
		lowModuleFinder:              finder,
	}
	read := mem.ReadReqBuilder{}.
		WithPID(1).
		WithAddress(0x1000).
		WithByteSize(64).
		WithLocalStreamID(7).
		Build()
	trans := &transaction{read: read}
	c.observeGranularityDemand(0, trans)
	candidate := trans.granularityCandidate
	if candidate == nil || !candidate.pairProbeOnly ||
		!candidate.pairProbeBypass ||
		c.granularityStats.DemandPairFilterProbes != 1 ||
		c.granularityStats.DemandPairFilterNegatives != 1 {
		t.Fatalf("unhinted real demand did not use the bypassed pair probe: %+v",
			c.granularityStats)
	}
	stats := filter.Stats()
	if stats.LookupPortStalls != 0 ||
		stats.ByType[FilterGranularityPending].Queries != 1 {
		t.Fatalf("bypassed pair probe consumed a modeled port: %+v", stats)
	}
}

func TestGranularityPairProbePositiveRequiresExactMSHR(t *testing.T) {
	candidate := &granularityCandidate{
		gated: true, pairProbeOnly: true, results: [2]bool{false, true},
	}
	checkResident, checkPending := granularityExactLookupPlan(candidate)
	if checkResident || !checkPending {
		t.Fatalf("positive pair probe must confirm only MSHR: resident=%v pending=%v",
			checkResident, checkPending)
	}
}

func TestGranularityDropsSiblingOwnedByAnotherL2Slice(t *testing.T) {
	c := &Cache{
		granularityAdaptationEnabled: true,
		granularityAlwaysExpand:      true,
		granularityWithoutFilter:     true,
		log2BlockSize:                6,
		interleaving:                 true,
		interleavingBlocks:           1,
		interleavingUnits:            4,
		interleavingIndex:            0,
	}
	read := mem.ReadReqBuilder{}.
		WithPID(1).
		WithAddress(0x1000).
		WithByteSize(64).
		Build()
	trans := &transaction{read: read}
	c.observeGranularityDemand(0, trans)
	if trans.granularityCandidate != nil {
		t.Fatal("cross-slice sibling was retained as a candidate")
	}
	if c.granularityStats.WrongSliceDrops != 1 {
		t.Fatalf("cross-slice drop counter = %d",
			c.granularityStats.WrongSliceDrops)
	}
}

func TestGranularityDropsSiblingMappedToAnotherController(t *testing.T) {
	ctrl := gomock.NewController(t)
	finder := NewMockLowModuleFinder(ctrl)
	lower := NewMockPort(ctrl)
	upper := NewMockPort(ctrl)
	finder.EXPECT().Find(uint64(0x1000)).Return(lower)
	finder.EXPECT().Find(uint64(0x1040)).Return(upper)
	c := &Cache{
		granularityAdaptationEnabled: true,
		granularityAlwaysExpand:      true,
		granularityWithoutFilter:     true,
		log2BlockSize:                6,
		lowModuleFinder:              finder,
	}
	read := mem.ReadReqBuilder{}.
		WithPID(1).
		WithAddress(0x1000).
		WithByteSize(64).
		Build()
	trans := &transaction{read: read}
	c.observeGranularityDemand(0, trans)
	if trans.granularityCandidate != nil {
		t.Fatal("cross-controller sibling was retained as a candidate")
	}
	if c.granularityStats.WrongControllerDrops != 1 {
		t.Fatalf("cross-controller drop counter = %d",
			c.granularityStats.WrongControllerDrops)
	}
}

func TestGranularityFilterPortBusyDropsCandidateWithoutWaiting(t *testing.T) {
	ctrl := gomock.NewController(t)
	finder := NewMockLowModuleFinder(ctrl)
	port := NewMockPort(ctrl)
	finder.EXPECT().Find(uint64(0x1000)).Return(port)
	finder.EXPECT().Find(uint64(0x1040)).Return(port)
	filter := NewTypedCuckooFilter(TypedFilterConfig{
		Mode: TypedFilterCuckoo, LookupWidth: 1, UpdateWidth: 2,
	})
	c := &Cache{
		granularityAdaptationEnabled: true,
		granularityAlwaysExpand:      true,
		log2BlockSize:                6,
		requestFilter:                filter,
		lowModuleFinder:              finder,
	}
	read := mem.ReadReqBuilder{}.
		WithPID(1).
		WithAddress(0x1000).
		WithByteSize(64).
		Build()
	trans := &transaction{read: read}
	c.observeGranularityDemand(0, trans)
	if trans.granularityCandidate != nil {
		t.Fatal("Filter-port-busy candidate was retained")
	}
	if c.granularityStats.FilterBusyDrops != 1 {
		t.Fatalf("Filter busy drop counter = %d",
			c.granularityStats.FilterBusyDrops)
	}
}

func TestGranularitySiblingLeaderIdentifiesInflightMerge(t *testing.T) {
	entry := cache.NewMSHREntry()
	entry.Requests = append(entry.Requests, &transaction{granularitySibling: true})
	if !granularitySiblingLeadsMSHR(entry) {
		t.Fatal("paired sibling MSHR was not recognized")
	}
	if granularitySiblingLeadsMSHR(cache.NewMSHREntry()) {
		t.Fatal("ordinary empty MSHR was classified as a paired sibling")
	}
}

func TestGranularityRejectsUnsafeAndRemoteReuseVictims(t *testing.T) {
	c := &Cache{remoteReplicaBlocks: make(map[*cache.Block]*remoteReplicaRecord)}
	for name, victim := range map[string]*cache.Block{
		"nil":    nil,
		"dirty":  {IsDirty: true},
		"locked": {IsLocked: true},
		"reader": {ReadCount: 1},
	} {
		if ok, _ := c.granularityVictimAdmissible(victim); ok {
			t.Fatalf("%s victim was incorrectly admitted", name)
		}
	}
	clean := &cache.Block{IsValid: true}
	if ok, remote := c.granularityVictimAdmissible(clean); !ok || remote {
		t.Fatal("clean unused victim was rejected")
	}
	c.remoteReplicaBlocks[clean] = &remoteReplicaRecord{}
	if ok, remote := c.granularityVictimAdmissible(clean); ok || !remote {
		t.Fatal("M3 requester-L2 reuse victim was not protected")
	}
}
