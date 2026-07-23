package writeback

import (
	"testing"

	"github.com/sarchlab/akita/v3/mem/mem"
	"github.com/sarchlab/akita/v3/mem/vm"
	"github.com/sarchlab/akita/v3/sim"
)

func newResidentFilterTestCache() *Cache {
	return MakeBuilder().
		WithEngine(sim.NewSerialEngine()).
		WithByteSize(4 * 64).
		WithWayAssociativity(2).
		WithNumReqPerCycle(1).
		WithDirectoryLatency(10).
		WithResidentFilter(true).
		Build("L2")
}

func TestResidentFilterReadNegativeSkipsDirectoryButKeepsMSHR(t *testing.T) {
	cache := newResidentFilterTestCache()
	pid := vm.PID(1)
	line := uint64(0x1000)
	read := mem.ReadReqBuilder{}.
		WithSrc(cache.topPort).
		WithDst(cache.topPort).
		WithPID(pid).
		WithAddress(line).
		WithByteSize(64).
		Build()
	if err := cache.topPort.Recv(read); err != nil {
		t.Fatal(err)
	}
	if !cache.topParser.Tick(1) {
		t.Fatal("top parser did not accept read")
	}

	cache.dirStage.Tick(2) // Filter lookup and direct post-filter enqueue.
	cache.dirStage.Tick(3) // Modeled filter latency completes.
	cache.dirStage.Tick(4) // MSHR allocation and normal L2 miss handling.

	entry := cache.mshr.Query(pid, line)
	if entry == nil || len(entry.Requests) != 1 {
		t.Fatal("filter-negative read did not retain the L2 MSHR/fill path")
	}
	if cache.residentFilter.Contains(pid, line) {
		t.Fatal("line entered the resident filter before its DRAM fill returned")
	}

	follower := mem.ReadReqBuilder{}.
		WithSrc(cache.topPort).
		WithDst(cache.topPort).
		WithPID(pid).
		WithAddress(line).
		WithByteSize(64).
		Build()
	if err := cache.topPort.Recv(follower); err != nil {
		t.Fatal(err)
	}
	if !cache.topParser.Tick(5) {
		t.Fatal("top parser did not accept MSHR follower")
	}
	cache.dirStage.Tick(6)
	cache.dirStage.Tick(7)
	cache.dirStage.Tick(8)
	if len(entry.Requests) != 2 {
		t.Fatal("filter-negative follower did not merge into the exact L2 MSHR")
	}
	stats := cache.GetResidentFilterStats()
	if stats.ReadNegativeBypasses != 1 || stats.Negatives != 1 ||
		stats.ReadNegativeMSHRMerges != 0 ||
		stats.ReadParallelMSHRMerges != 1 {
		t.Fatalf("read negative stats = %+v", stats)
	}

	leader := entry.Requests[0].(*transaction)
	leader.action = bankWriteFetched
	entry.Data = make([]byte, 64)
	bankNum := bankID(
		entry.Block,
		cache.directory.WayAssociativity(),
		len(cache.bankStages),
	)
	stage := cache.bankStages[bankNum]
	stage.inflightTransCount = 1
	if !stage.finalizeBankWriteFetched(9, leader) {
		t.Fatal("completed fill did not reach the L2 bank")
	}
	if !cache.residentFilter.Contains(pid, line) {
		t.Fatal("completed L2 fill was not inserted into the resident filter")
	}
}

func TestResidentFilterReadNegativeFallsBackWhenBankHasLiveMiss(t *testing.T) {
	cache := newResidentFilterTestCache()
	pid := vm.PID(29)
	busyLine := uint64(0x2000)
	entry := cache.mshr.Add(pid, busyLine)
	entry.Requests = append(entry.Requests, &transaction{})

	line := uint64(0x1000)
	read := mem.ReadReqBuilder{}.
		WithSrc(cache.topPort).
		WithDst(cache.topPort).
		WithPID(pid).
		WithAddress(line).
		WithByteSize(64).
		Build()
	if err := cache.topPort.Recv(read); err != nil {
		t.Fatal(err)
	}
	if !cache.topParser.Tick(1) {
		t.Fatal("top parser did not accept read")
	}
	cache.dirStage.Tick(2)

	if cache.mshr.Query(pid, line) != nil {
		t.Fatal("busy-bank negative bypassed the ordinary directory lookup")
	}
	stats := cache.GetResidentFilterStats()
	if stats.ReadBusyFallbacks != 1 || stats.ReadNegativeBypasses != 0 {
		t.Fatalf("busy-bank fallback stats = %+v", stats)
	}
}

func TestM1ResidentNegativeBypassesWhileOtherMissIsLive(t *testing.T) {
	cache := newResidentFilterTestCache()
	cache.granularityAdaptationEnabled = true
	pid := vm.PID(30)
	busyLine := uint64(0x2000)
	entry := cache.mshr.Add(pid, busyLine)
	entry.Requests = append(entry.Requests, &transaction{})

	line := uint64(0x1000)
	read := mem.ReadReqBuilder{}.
		WithSrc(cache.topPort).
		WithDst(cache.topPort).
		WithPID(pid).
		WithAddress(line).
		WithByteSize(64).
		Build()
	if err := cache.topPort.Recv(read); err != nil {
		t.Fatal(err)
	}
	if !cache.topParser.Tick(1) {
		t.Fatal("top parser did not accept M1 read")
	}
	cache.dirStage.Tick(2)
	cache.dirStage.Tick(3)
	cache.dirStage.Tick(4)

	if cache.mshr.Query(pid, line) == nil {
		t.Fatal("M1 RESIDENT-negative did not bypass the tag latency")
	}
	stats := cache.GetResidentFilterStats()
	if stats.ReadNegativeBypasses != 1 || stats.ReadBusyFallbacks != 0 {
		t.Fatalf("M1 busy-bank bypass stats = %+v", stats)
	}
}

func TestM1ResidentPositiveSkipsModeledTagLatencyButKeepsExactHit(t *testing.T) {
	cache := newResidentFilterTestCache()
	cache.granularityAdaptationEnabled = true
	cache.dirStage.pipeline.Accept(
		1,
		dirPipelineItem{trans: &transaction{id: "pipeline-occupier"}},
	)
	pid := vm.PID(31)
	line := uint64(0x3000)
	block := cache.directory.FindVictim(line)
	block.PID = pid
	block.Tag = line
	block.IsValid = true
	block.IsLocked = false
	cache.directory.Visit(block)
	if !cache.residentFilter.Insert(pid, line) {
		t.Fatal("could not seed positive resident metadata")
	}

	read := mem.ReadReqBuilder{}.
		WithSrc(cache.topPort).
		WithDst(cache.topPort).
		WithPID(pid).
		WithAddress(line).
		WithByteSize(64).
		Build()
	if err := cache.topPort.Recv(read); err != nil {
		t.Fatal(err)
	}
	if !cache.topParser.Tick(1) {
		t.Fatal("top parser did not accept M1 read")
	}
	if !cache.dirStage.acceptNewTransaction(2) {
		t.Fatal("M1 resident positive stalled behind tag pipeline")
	}
	if cache.dirStageBuffer.Peek() != nil || cache.dirStage.buf.Peek() == nil {
		t.Fatal("M1 resident positive did not reach exact post-filter lookup")
	}
	stats := cache.GetResidentFilterStats()
	if stats.Positives != 1 || stats.ReadPositiveFastPaths != 1 {
		t.Fatalf("M1 positive fast-path stats = %+v", stats)
	}
}

func TestM1ResidentFalsePositiveFastPathStillAllocatesExactMiss(t *testing.T) {
	cache := newResidentFilterTestCache()
	cache.granularityAdaptationEnabled = true
	pid := vm.PID(32)
	line := uint64(0x4000)
	if !cache.residentFilter.Insert(pid, line) {
		t.Fatal("could not seed false-positive resident metadata")
	}
	read := mem.ReadReqBuilder{}.
		WithSrc(cache.topPort).
		WithDst(cache.topPort).
		WithPID(pid).
		WithAddress(line).
		WithByteSize(64).
		Build()
	if err := cache.topPort.Recv(read); err != nil {
		t.Fatal(err)
	}
	if !cache.topParser.Tick(1) {
		t.Fatal("top parser did not accept M1 false-positive read")
	}
	cache.dirStage.Tick(2)
	cache.dirStage.Tick(3)
	if cache.mshr.Query(pid, line) == nil {
		t.Fatal("Filter false positive suppressed the exact L2 miss")
	}
	stats := cache.GetResidentFilterStats()
	if stats.ReadPositiveFastPaths != 1 || stats.FalsePositives != 1 {
		t.Fatalf("M1 false-positive fast-path stats = %+v", stats)
	}
}

func TestResidentFilterFullLineWriteNegativeAllocatesInL2(t *testing.T) {
	cache := newResidentFilterTestCache()
	pid := vm.PID(2)
	line := uint64(0x2000)
	data := make([]byte, 64)
	data[7] = 0x5a
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
		t.Fatal("top parser did not accept write")
	}

	cache.dirStage.Tick(2)
	cache.dirStage.Tick(3)
	cache.dirStage.Tick(4)

	block := cache.directory.Lookup(pid, line)
	if block == nil || !block.IsValid || !block.IsLocked {
		t.Fatal("full-line filter-negative write was not allocated in L2")
	}
	if cache.mshr.Query(pid, line) != nil {
		t.Fatal("full-line write unnecessarily allocated an MSHR/DRAM read")
	}
	if cache.residentFilter.Contains(pid, line) {
		t.Fatal("locked full-line write entered the resident filter before the bank write")
	}
	stats := cache.GetResidentFilterStats()
	if stats.WriteNegativeBypasses != 1 ||
		stats.WriteFullLineBypasses != 1 ||
		stats.WritePartialBypasses != 0 || stats.Negatives != 1 {
		t.Fatalf("write negative stats = %+v", stats)
	}

	numBanks := len(cache.bankStages)
	bankNum := bankID(
		block, cache.directory.WayAssociativity(), numBanks)
	trans := cache.dirToBankBuffers[bankNum].Peek().(*transaction)
	stage := cache.bankStages[bankNum]
	stage.inflightTransCount = 1
	stage.downwardInflightTransCount = 1
	if !stage.finalizeWriteHit(5, trans) {
		t.Fatal("full-line write did not complete in the L2 bank")
	}
	if !cache.residentFilter.Contains(pid, line) {
		t.Fatal("completed full-line write was not inserted into the resident filter")
	}
}

func TestResidentFilterPartialWriteNegativeRetainsRFO(t *testing.T) {
	cache := newResidentFilterTestCache()
	pid := vm.PID(5)
	line := uint64(0x6000)
	write := mem.WriteReqBuilder{}.
		WithSrc(cache.topPort).
		WithDst(cache.topPort).
		WithPID(pid).
		WithAddress(line).
		WithData([]byte{1, 2, 3, 4}).
		Build()
	if err := cache.topPort.Recv(write); err != nil {
		t.Fatal(err)
	}
	if !cache.topParser.Tick(1) {
		t.Fatal("top parser did not accept partial write")
	}

	cache.dirStage.Tick(2)
	cache.dirStage.Tick(3)
	cache.dirStage.Tick(4)

	if cache.mshr.Query(pid, line) == nil {
		t.Fatal("partial filter-negative write incorrectly skipped RFO")
	}
	stats := cache.GetResidentFilterStats()
	if stats.WriteNegativeBypasses != 1 ||
		stats.WriteFullLineBypasses != 0 ||
		stats.WritePartialBypasses != 1 {
		t.Fatalf("partial write negative stats = %+v", stats)
	}
}

func TestFullLineWriteFastPathRequiresAlignmentAndCompleteMask(t *testing.T) {
	cache := newResidentFilterTestCache()
	stage := cache.dirStage
	fullData := make([]byte, 64)
	fullMask := make([]bool, 64)
	for i := range fullMask {
		fullMask[i] = true
	}

	tests := []struct {
		name    string
		address uint64
		mask    []bool
		want    bool
	}{
		{name: "aligned implicit mask", address: 0x8000, want: true},
		{name: "aligned complete mask", address: 0x8000, mask: fullMask, want: true},
		{name: "unaligned", address: 0x8004, want: false},
		{name: "short mask", address: 0x8000, mask: fullMask[:63], want: false},
		{name: "partial mask", address: 0x8000, mask: append([]bool{false}, fullMask[1:]...), want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			write := mem.WriteReqBuilder{}.
				WithAddress(test.address).
				WithData(fullData).
				WithDirtyMask(test.mask).
				Build()
			if got := stage.isWritingFullLine(write); got != test.want {
				t.Fatalf("isWritingFullLine = %v, want %v", got, test.want)
			}
		})
	}
}

func TestResidentFilterTracksReplacementWithoutFalseNegative(t *testing.T) {
	cache := newResidentFilterTestCache()
	block := cache.directory.FindVictim(0x3000)
	block.PID = vm.PID(3)
	block.Tag = 0x3000
	block.IsValid = true
	cache.trackResidentBlock(block)
	if !cache.residentMayContain(block.PID, block.Tag) {
		t.Fatal("tracked line is a false negative")
	}

	cache.untrackResidentBlock(block)
	block.Tag = 0x4000
	cache.trackResidentBlock(block)
	if cache.residentFilter.Contains(block.PID, 0x3000) {
		t.Fatal("replaced line remained in resident filter")
	}
	if !cache.residentFilter.Contains(block.PID, 0x4000) {
		t.Fatal("replacement line is absent from resident filter")
	}
}

func TestResidentFilterNegativeBypassesFullDirectoryPipeline(t *testing.T) {
	cache := newResidentFilterTestCache()
	cache.dirStage.pipeline.Accept(
		1,
		dirPipelineItem{trans: &transaction{id: "pipeline-occupier"}},
	)
	if cache.dirStage.pipeline.CanAccept() {
		t.Fatal("test did not saturate the directory pipeline")
	}

	pid := vm.PID(4)
	line := uint64(0x5000)
	read := mem.ReadReqBuilder{}.
		WithSrc(cache.topPort).
		WithDst(cache.topPort).
		WithPID(pid).
		WithAddress(line).
		WithByteSize(64).
		Build()
	if err := cache.topPort.Recv(read); err != nil {
		t.Fatal(err)
	}
	if !cache.topParser.Tick(2) {
		t.Fatal("top parser did not accept read")
	}

	if !cache.dirStage.acceptNewTransaction(3) {
		t.Fatal("filter-negative request stalled behind full tag pipeline")
	}
	if cache.dirStageBuffer.Peek() != nil {
		t.Fatal("filter-negative request remained in the input queue")
	}
	if cache.dirStage.buf.Peek() == nil {
		t.Fatal("filter-negative request did not reach the post-directory buffer")
	}
	stats := cache.GetResidentFilterStats()
	if stats.ReadNegativeBypasses != 1 || stats.Negatives != 1 {
		t.Fatalf("read negative stats = %+v", stats)
	}
}

func TestPrevalidatedPrefetchNegativeDoesNotRepeatResidentLookup(t *testing.T) {
	cache := newResidentFilterTestCache()
	cache.dirStage.pipeline.Accept(
		1,
		dirPipelineItem{trans: &transaction{id: "pipeline-occupier"}},
	)
	pid := vm.PID(27)
	read := mem.ReadReqBuilder{}.
		WithSrc(cache.topPort).
		WithDst(cache.topPort).
		WithPID(pid).
		WithAddress(0x5800).
		WithByteSize(64).
		Build()
	trans := &transaction{
		id:                     "prefetch-negative",
		read:                   read,
		prefetch:               true,
		residentFilterChecked:  true,
		residentFilterNegative: true,
		residentFastMiss:       true,
	}
	cache.dirStageBuffer.Push(trans)
	queriesBefore := cache.GetResidentFilterStats().Queries
	if !cache.dirStage.acceptNewTransaction(2) {
		t.Fatal("prevalidated candidate stalled behind the full tag pipeline")
	}
	if cache.dirStageBuffer.Peek() != nil || cache.dirStage.buf.Peek() == nil {
		t.Fatal("prevalidated candidate did not reach the post-directory buffer")
	}
	if queriesAfter := cache.GetResidentFilterStats().Queries; queriesAfter != queriesBefore {
		t.Fatalf("RESIDENT lookup repeated: before=%d after=%d", queriesBefore, queriesAfter)
	}
}

func TestResidentLookupIsPrimedBeforeDirectoryAdmission(t *testing.T) {
	cache := newResidentFilterTestCache()
	pid := vm.PID(21)
	line := uint64(0x7000)
	if !cache.residentFilter.Insert(pid, line) {
		t.Fatal("could not seed resident metadata")
	}
	read := mem.ReadReqBuilder{}.
		WithSrc(cache.topPort).
		WithDst(cache.topPort).
		WithPID(pid).
		WithAddress(line).
		WithByteSize(64).
		Build()
	if err := cache.topPort.Recv(read); err != nil {
		t.Fatal(err)
	}
	if !cache.topParser.Tick(1) {
		t.Fatal("top parser did not accept read")
	}
	trans := cache.dirStageBuffer.Peek().(*transaction)
	if trans.residentFilterLookup == nil {
		t.Fatal("top parser did not prime the resident lookup")
	}

	// At the next L2 cycle, the possible match enters the ordinary tag
	// pipeline immediately rather than paying a Filter cycle in front of it.
	if !cache.dirStage.acceptNewTransaction(2) {
		t.Fatal("primed resident lookup did not admit the request")
	}
	if cache.dirStageBuffer.Peek() != nil {
		t.Fatal("possible resident request retained an extra front-end cycle")
	}
	stats := cache.GetResidentFilterStats()
	if stats.PrimedLookups != 1 || stats.Positives != 1 ||
		stats.ReadNegativeBypasses != 0 || stats.ReadPositiveFastPaths != 0 {
		t.Fatalf("primed lookup stats = %+v", stats)
	}
}

func TestResidentFilterInsertFailureFailsOpenToTagLookup(t *testing.T) {
	cache := newResidentFilterTestCache()
	pid := vm.PID(12)
	// The production filter has twice as many slots as resident blocks, so a
	// full table is exceptional. Artificially saturate it to verify that even
	// this path cannot turn a real L2 hit into a false negative fast miss.
	keys := typedFilterKeysWithUniqueSignatures(
		t, cache.requestFilter, pid, FilterResident, 9,
	)
	for _, key := range keys[:8] {
		if !cache.residentFilter.Insert(pid, key.Address) {
			t.Fatalf("could not saturate resident filter at line %#x", key.Address)
		}
	}

	line := keys[8].Address
	block := cache.directory.FindVictim(line)
	block.PID = pid
	block.Tag = line
	block.IsValid = true
	block.IsLocked = false
	cache.directory.Visit(block)
	cache.trackResidentBlock(block)
	stats := cache.GetResidentFilterStats()
	if stats.Reliable || stats.InsertFailures != 1 {
		t.Fatalf("failed insertion did not disable fast negatives: %+v", stats)
	}
	if !cache.residentMayContain(pid, line) {
		t.Fatal("unreliable resident filter returned a false-negative miss")
	}
	stats = cache.GetResidentFilterStats()
	if stats.Queries != 0 || stats.Negatives != 0 {
		t.Fatal("fail-open lookup consulted an unreliable resident filter")
	}
}

func TestResidentFilterTransientUpdateVisibilityDoesNotDisableNegatives(t *testing.T) {
	cache := newResidentFilterTestCache()
	if !cache.residentFilterReliable {
		t.Fatal("resident filter did not start reliable")
	}

	// Port serialization or update latency can make one key temporarily
	// unavailable to a modeled lookup. That request must fall back to the tag
	// path, but the RESIDENT class remains usable for later requests.
	if possible := cache.recordResidentLookupResult(false, false); !possible {
		t.Fatal("transiently unavailable metadata did not fail open")
	}
	stats := cache.GetResidentFilterStats()
	if !stats.Reliable {
		t.Fatal("transient update visibility permanently disabled RESIDENT")
	}
	if stats.Queries != 0 || stats.Positives != 0 || stats.Negatives != 0 {
		t.Fatalf("transient fallback was counted as a completed lookup: %+v", stats)
	}
}
