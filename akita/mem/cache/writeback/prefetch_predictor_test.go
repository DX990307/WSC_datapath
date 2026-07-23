package writeback

import (
	"testing"

	"github.com/sarchlab/akita/v3/mem/vm"
)

func TestDemandStridePredictorRequiresRealRepeatedStride(t *testing.T) {
	p := NewDemandStridePredictor(8, 64)
	if p.Stats().Capacity != 8 {
		t.Fatalf("reported capacity = %d, want 8", p.Stats().Capacity)
	}
	stream := DemandStreamKey{PID: vm.PID(1), Source: 7}
	for _, address := range []uint64{0x1000, 0x1080} {
		if observation := p.ObserveRealDemand(stream, address); observation.HasCandidate || observation.InstallPattern != nil {
			t.Fatal("predictor acted before a repeated real stride")
		}
	}
	observation := p.ObserveRealDemand(stream, 0x1100)
	if observation.InstallPattern == nil || observation.HasCandidate {
		t.Fatal("conservative predictor did not establish PATTERN before prediction")
	}
	p.SetPatternInstalled(observation.Token, true)
	observation = p.ObserveRealDemand(stream, 0x1180)
	if !observation.HasCandidate || observation.Candidate != 0x1200 {
		t.Fatalf("candidate = %#x, want %#x", observation.Candidate, uint64(0x1200))
	}
}

func TestDemandStridePredictorUsesOnlyRealDemandTraining(t *testing.T) {
	p := NewDemandStridePredictor(4, 64)
	stream := DemandStreamKey{PID: 2, Source: 11}
	_ = p.ObserveRealDemand(stream, 0x2000)
	_ = p.ObserveRealDemand(stream, 0x2040)
	install := p.ObserveRealDemand(stream, 0x2080)
	p.SetPatternInstalled(install.Token, true)
	candidate := p.ObserveRealDemand(stream, 0x20c0)
	if !candidate.HasCandidate {
		t.Fatal("trained stream did not generate a candidate")
	}
	if stats := p.Stats(); stats.RealDemands != 4 ||
		stats.CandidatesGenerated != 1 {
		t.Fatalf("unexpected predictor stats: %+v", stats)
	}
}

func TestDemandStridePredictorUnusedFeedbackRetiresPattern(t *testing.T) {
	p := NewDemandStridePredictor(4, 64)
	stream := DemandStreamKey{PID: 3, Source: 13}
	_ = p.ObserveRealDemand(stream, 0x3000)
	_ = p.ObserveRealDemand(stream, 0x3040)
	install := p.ObserveRealDemand(stream, 0x3080)
	p.SetPatternInstalled(install.Token, true)
	candidate := p.ObserveRealDemand(stream, 0x30c0)
	key, ok := p.Penalize(candidate.Token)
	if !ok || key.Type != FilterPattern {
		t.Fatal("unused feedback did not return the PATTERN key")
	}
	next := p.ObserveRealDemand(stream, 0x3100)
	if next.HasCandidate || next.InstallPattern == nil {
		t.Fatal("retired pattern did not require retraining")
	}
}

func TestPairedReadLateFeedbackKeepsBoundedDirectSiblingTrial(t *testing.T) {
	p := NewPageLocalDemandStridePredictor(8, 64, 4096)
	p.EnableCandidateOnPatternEstablishment()
	stream := DemandStreamKey{PID: 1, Source: 2, StreamID: 3}
	_ = p.ObserveRealDemand(stream, 0x1000)
	_ = p.ObserveRealDemand(stream, 0x1040)
	observation := p.ObserveRealDemand(stream, 0x1080)
	p.SetPatternInstalled(observation.Token, true)

	if !p.MarkIssued(observation.Token) || p.MarkIssued(observation.Token) {
		t.Fatal("unproven paired stream was not limited to one training request")
	}
	p.ResolvePairedRead(observation.Token, true)
	if p.HasTimelyProof(observation.Token) {
		t.Fatal("late direct-sibling trial established timely proof")
	}
	next := p.ObserveRealDemand(stream, 0x10c0)
	if !next.HasCandidate || next.InstallPattern != nil ||
		next.Token.Lookahead != 1 {
		t.Fatalf("late feedback changed direct-sibling relation: %+v", next)
	}
	if !p.MarkIssued(next.Token) || p.MarkIssued(next.Token) {
		t.Fatal("late feedback did not retain one bounded retry")
	}
	p.ResolvePairedRead(next.Token, false)
	if !p.HasTimelyProof(next.Token) ||
		!p.MarkIssued(next.Token) || !p.MarkIssued(next.Token) {
		t.Fatal("timely proof did not enable steady paired-read issue")
	}
	stats := p.Stats()
	if stats.LateFeedback != 1 || stats.TimelyFeedback != 1 ||
		stats.LateDistanceIncreases != 0 {
		t.Fatalf("paired feedback was not classified: %+v", stats)
	}
}

func TestEagerPredictorProposesCandidateWhenPatternIsEstablished(t *testing.T) {
	p := NewPageLocalDemandStridePredictor(8, 64, 4096)
	p.EnableCandidateOnPatternEstablishment()
	stream := DemandStreamKey{PID: 7, Source: 29}
	_ = p.ObserveRealDemand(stream, 0x4000)
	_ = p.ObserveRealDemand(stream, 0x4040)
	observation := p.ObserveRealDemand(stream, 0x4080)
	if observation.InstallPattern == nil || !observation.HasCandidate ||
		observation.Candidate != 0x40c0 {
		t.Fatalf("eager pattern establishment = %+v", observation)
	}
}

func TestLocalPredictorUsesDemandHorizonWithoutFixedDistance(t *testing.T) {
	p := NewPageLocalDemandStridePredictor(8, 64, 4096)
	p.EnableCandidateOnPatternEstablishment()
	stream := DemandStreamKey{PID: 7, Source: 31}
	_ = p.ObserveRealDemandWithMinimumLookahead(stream, 0x5000, 1)
	_ = p.ObserveRealDemandWithMinimumLookahead(stream, 0x5040, 1)
	observation := p.ObserveRealDemandWithMinimumLookahead(
		stream, 0x5080, 8)
	if !observation.HasCandidate || observation.Candidate != 0x5280 ||
		observation.Token.Lookahead != 8 {
		t.Fatalf("demand-horizon candidate = %+v", observation)
	}
	if p.Stats().HorizonDistanceIncreases != 1 {
		t.Fatalf("demand horizon was not recorded: %+v", p.Stats())
	}
}

func TestDemandStridePredictorIsBoundedAndIgnoresStaleFeedback(t *testing.T) {
	p := NewDemandStridePredictor(1, 64)
	first := DemandStreamKey{PID: 1, Source: 1}
	second := DemandStreamKey{PID: 1, Source: 2}
	_ = p.ObserveRealDemand(first, 0x1000)
	_ = p.ObserveRealDemand(first, 0x1040)
	install := p.ObserveRealDemand(first, 0x1080)
	p.SetPatternInstalled(install.Token, true)
	candidate := p.ObserveRealDemand(first, 0x10c0)
	replacement := p.ObserveRealDemand(second, 0x2000)
	if replacement.DeletePattern == nil {
		t.Fatal("bounded replacement did not retire the old PATTERN")
	}
	p.Reward(candidate.Token)
	if p.Stats().StaleFeedbackIgnored != 1 {
		t.Fatal("stale feedback modified a replacement stream")
	}
}

func TestTypedFilterPatternSharesLowPriorityBudget(t *testing.T) {
	f := NewTypedCuckooFilter(TypedFilterConfig{
		Capacity: 8, CriticalReserve: 4, SlotsPerBucket: 2,
		FingerprintBits: 13, Mode: TypedFilterCuckoo,
		LookupWidth: 4, UpdateWidth: 4,
	})
	key := TypedFilterKey{
		PID: 1, Owner: 9, Address: encodeSignedStride(64), Type: FilterPattern,
	}
	if !f.Insert(key) || !f.Contains(key) {
		t.Fatal("PATTERN did not share the physical typed Filter")
	}
	if f.Stats().ByType[FilterPattern].Occupancy != 1 {
		t.Fatal("PATTERN occupancy was not reported independently")
	}
}

func TestPageLocalPredictorLearnsLookaheadFromLateFeedback(t *testing.T) {
	p := NewPageLocalDemandStridePredictor(8, 64, 4096)
	stream := DemandStreamKey{PID: 4, Source: 17}
	for _, address := range []uint64{0x1000, 0x1040} {
		_ = p.ObserveRealDemand(stream, address)
	}
	install := p.ObserveRealDemand(stream, 0x1080)
	p.SetPatternInstalled(install.Token, true)
	first := p.ObserveRealDemand(stream, 0x10c0)
	if !first.HasCandidate || first.Candidate != 0x1100 ||
		first.Token.Lookahead != 1 {
		t.Fatalf("first candidate = %+v", first)
	}
	p.RewardWithTimeliness(first.Token, true)
	second := p.ObserveRealDemand(stream, 0x1100)
	if !second.HasCandidate || second.Candidate != 0x1180 ||
		second.Token.Lookahead != 2 {
		t.Fatalf("late feedback did not advance lookahead: %+v", second)
	}
	stats := p.Stats()
	if stats.LateFeedback != 1 || stats.LateDistanceIncreases != 1 ||
		stats.CandidateLookaheadMax != 2 {
		t.Fatalf("adaptive lookahead stats = %+v", stats)
	}
}

func TestPageLocalPredictorAdvancesOncePerLateDistance(t *testing.T) {
	p := NewPageLocalDemandStridePredictor(8, 64, 4096)
	stream := DemandStreamKey{PID: 6, Source: 23}
	for _, address := range []uint64{0x2000, 0x2040} {
		_ = p.ObserveRealDemand(stream, address)
	}
	install := p.ObserveRealDemand(stream, 0x2080)
	p.SetPatternInstalled(install.Token, true)
	first := p.ObserveRealDemand(stream, 0x20c0)
	second := p.ObserveRealDemand(stream, 0x2100)
	if first.Token.Lookahead != 1 || second.Token.Lookahead != 1 {
		t.Fatalf("overlapping candidates did not use the same distance: first=%+v second=%+v", first, second)
	}

	p.ObserveLate(first.Token)
	p.ObserveLate(second.Token)
	next := p.ObserveRealDemand(stream, 0x2140)
	if !next.HasCandidate || next.Token.Lookahead != 2 ||
		next.Candidate != 0x21c0 {
		t.Fatalf("late feedback inflated distance more than once: %+v", next)
	}
	stats := p.Stats()
	if stats.LateFeedback != 2 || stats.LateDistanceIncreases != 1 ||
		stats.StaleDistanceFeedback != 1 {
		t.Fatalf("overlapping late feedback stats = %+v", stats)
	}
}

func TestPageLocalPredictorConvergesExponentiallyAfterRepeatedLateFeedback(t *testing.T) {
	p := NewPageLocalDemandStridePredictor(8, 64, 4096)
	p.EnableExponentialLateLookahead()
	stream := DemandStreamKey{PID: 9, Source: 37}
	for _, address := range []uint64{0x3000, 0x3040} {
		_ = p.ObserveRealDemand(stream, address)
	}
	install := p.ObserveRealDemand(stream, 0x3080)
	p.SetPatternInstalled(install.Token, true)

	first := p.ObserveRealDemand(stream, 0x30c0)
	p.ObserveLate(first.Token) // 1 -> 2
	second := p.ObserveRealDemand(stream, 0x3100)
	if second.Token.Lookahead != 2 || second.Candidate != 0x3180 {
		t.Fatalf("first late feedback = %+v", second)
	}
	p.ObserveLate(second.Token) // 2 -> 4
	third := p.ObserveRealDemand(stream, 0x3140)
	if third.Token.Lookahead != 4 || third.Candidate != 0x3240 {
		t.Fatalf("repeated late feedback did not double distance: %+v", third)
	}
	if stats := p.Stats(); stats.LateDistanceIncreases != 2 ||
		stats.CandidateLookaheadMax != 4 {
		t.Fatalf("exponential lookahead stats = %+v", stats)
	}
}

func TestPageLocalPredictorExponentialLookaheadStopsAtPageBound(t *testing.T) {
	p := NewPageLocalDemandStridePredictor(8, 64, 256)
	p.EnableExponentialLateLookahead()
	entry := &demandStrideEntry{lookahead: 2}
	if !p.increaseLookahead(PatternToken{Lookahead: 2}, entry) ||
		entry.lookahead != 3 {
		t.Fatalf("lookahead escaped page-derived bound: %+v", entry)
	}
}

func TestConservativePredictorRetainsLinearLateLookahead(t *testing.T) {
	p := NewPageLocalDemandStridePredictor(8, 64, 4096)
	entry := &demandStrideEntry{lookahead: 2}
	if !p.increaseLookahead(PatternToken{Lookahead: 2}, entry) ||
		entry.lookahead != 3 {
		t.Fatalf("shared conservative predictor changed policy: %+v", entry)
	}
}

func TestDemandCoveredCandidateAdvancesWithoutSpeculativeIssue(t *testing.T) {
	p := NewPageLocalDemandStridePredictor(8, 64, 4096)
	p.EnableCandidateOnPatternEstablishment()
	p.EnableExponentialLateLookahead()
	stream := DemandStreamKey{PID: 4, Source: 11, StreamID: 0xabc}
	_ = p.ObserveRealDemand(stream, 0x6000)
	_ = p.ObserveRealDemand(stream, 0x6040)
	covered := p.ObserveRealDemand(stream, 0x6080)
	p.SetPatternInstalled(covered.Token, true)
	p.ObserveDemandCoveredCandidate(covered.Token)
	next := p.ObserveRealDemand(stream, 0x60c0)
	if !next.HasCandidate || next.Token.Lookahead != 2 ||
		next.Candidate != 0x6140 {
		t.Fatalf("demand-covered feedback did not advance candidate: %+v", next)
	}
	stats := p.Stats()
	if stats.DemandCoveredFeedback != 1 ||
		stats.CoveredDistanceIncreases != 1 ||
		stats.LateFeedback != 0 || stats.LateDistanceIncreases != 0 {
		t.Fatalf("demand-covered feedback accounting = %+v", stats)
	}
}

func TestRewardAfterObservedLateDoesNotDoubleCountDistanceFeedback(t *testing.T) {
	p := NewPageLocalDemandStridePredictor(8, 64, 4096)
	stream := DemandStreamKey{PID: 8, Source: 31}
	for _, address := range []uint64{0x5000, 0x5040} {
		_ = p.ObserveRealDemand(stream, address)
	}
	install := p.ObserveRealDemand(stream, 0x5080)
	p.SetPatternInstalled(install.Token, true)
	candidate := p.ObserveRealDemand(stream, 0x50c0)
	p.ObserveLate(candidate.Token)
	p.RewardAfterObservedLate(candidate.Token)
	stats := p.Stats()
	if stats.UsefulFeedback != 1 || stats.LateFeedback != 1 ||
		stats.LateDistanceIncreases != 1 {
		t.Fatalf("late-then-useful feedback was counted twice: %+v", stats)
	}
}

func TestPageLocalPredictorDoesNotCrossPageBoundary(t *testing.T) {
	p := NewPageLocalDemandStridePredictor(8, 64, 4096)
	stream := DemandStreamKey{PID: 5, Source: 19}
	for _, address := range []uint64{0x1f00, 0x1f40} {
		_ = p.ObserveRealDemand(stream, address)
	}
	install := p.ObserveRealDemand(stream, 0x1f80)
	p.SetPatternInstalled(install.Token, true)
	observation := p.ObserveRealDemand(stream, 0x1fc0)
	if observation.HasCandidate {
		t.Fatalf("cross-page candidate was generated: %+v", observation)
	}
}

func TestLocalPredictorClampsHorizonToPageFrontier(t *testing.T) {
	p := NewPageLocalDemandStridePredictor(8, 64, 4096)
	p.EnableCandidateOnPatternEstablishment()
	p.EnablePageFrontierClamping()
	stream := DemandStreamKey{PID: 5, Source: 21}
	_ = p.ObserveRealDemandWithMinimumLookahead(stream, 0x1f00, 8)
	_ = p.ObserveRealDemandWithMinimumLookahead(stream, 0x1f40, 8)
	observation := p.ObserveRealDemandWithMinimumLookahead(
		stream, 0x1f80, 8)
	if !observation.HasCandidate || observation.Candidate != 0x1fc0 ||
		observation.Token.Lookahead != 1 {
		t.Fatalf("page-frontier candidate = %+v", observation)
	}
	if p.Stats().PageFrontierClamps != 1 {
		t.Fatalf("page clamp was not recorded: %+v", p.Stats())
	}
}

func TestDemandStridePredictorSeparatesInstructionStreams(t *testing.T) {
	p := NewDemandStridePredictor(64, 64)
	first := DemandStreamKey{PID: 1, Source: 7, StreamID: 0x100}
	second := DemandStreamKey{PID: 1, Source: 7, StreamID: 0x200}
	if p.index(first) == p.index(second) {
		t.Fatal("test instruction streams unexpectedly alias")
	}
	for _, address := range []uint64{0x1000, 0x1040} {
		_ = p.ObserveRealDemand(first, address)
	}
	install := p.ObserveRealDemand(first, 0x1080)
	p.SetPatternInstalled(install.Token, true)

	// An unrelated load PC from the same L1 must not reset the first stream.
	_ = p.ObserveRealDemand(second, 0x8000)
	observation := p.ObserveRealDemand(first, 0x10c0)
	if !observation.HasCandidate || observation.Candidate != 0x1100 {
		t.Fatalf("instruction-stream alias destroyed prediction: %+v", observation)
	}
}

func TestUnprovenStreamKeepsOneTrainingPrefetch(t *testing.T) {
	p := NewPageLocalDemandStridePredictor(8, 64, 4096)
	p.EnableCandidateOnPatternEstablishment()
	p.EnableExponentialLateLookahead()
	stream := DemandStreamKey{PID: 3, Source: 9, StreamID: 0xabc}
	_ = p.ObserveRealDemand(stream, 0x3000)
	_ = p.ObserveRealDemand(stream, 0x3040)
	first := p.ObserveRealDemand(stream, 0x3080)
	p.SetPatternInstalled(first.Token, true)
	if !p.CanIssue(first.Token) || !p.MarkIssued(first.Token) {
		t.Fatal("first training candidate was not admitted")
	}
	second := p.ObserveRealDemand(stream, 0x30c0)
	if p.CanIssue(second.Token) {
		t.Fatal("unproven stream admitted overlapping training work")
	}

	p.ObserveLate(first.Token)
	third := p.ObserveRealDemand(stream, 0x3100)
	if third.Token.Lookahead != 2 || !p.CanIssue(third.Token) ||
		!p.MarkIssued(third.Token) {
		t.Fatalf("late trial did not release farther training: %+v", third)
	}
	p.RewardWithTimeliness(third.Token, false)
	fourth := p.ObserveRealDemand(stream, 0x3140)
	if !p.CanIssue(fourth.Token) || !p.MarkIssued(fourth.Token) {
		t.Fatal("timely stream did not enter continuous issue mode")
	}
	fifth := p.ObserveRealDemand(stream, 0x3180)
	if !p.CanIssue(fifth.Token) || !p.MarkIssued(fifth.Token) {
		t.Fatal("proven stream was limited to one training request")
	}
}

func TestFeedbackGatedStreamRequiresFeedbackAfterTimelyProof(t *testing.T) {
	p := NewPageLocalDemandStridePredictor(8, 64, 4096)
	p.EnableCandidateOnPatternEstablishment()
	p.EnableFeedbackGatedIssue()
	stream := DemandStreamKey{PID: 4, Source: 10, StreamID: 0xdef}
	_ = p.ObserveRealDemand(stream, 0x4000)
	_ = p.ObserveRealDemand(stream, 0x4040)
	first := p.ObserveRealDemand(stream, 0x4080)
	p.SetPatternInstalled(first.Token, true)
	if !p.CanIssue(first.Token) || !p.MarkIssued(first.Token) {
		t.Fatal("first feedback-gated candidate was not admitted")
	}
	second := p.ObserveRealDemand(stream, 0x40c0)
	if p.CanIssue(second.Token) {
		t.Fatal("feedback-gated stream admitted overlapping sibling")
	}

	p.ResolvePairedRead(first.Token, false)
	third := p.ObserveRealDemand(stream, 0x4100)
	if !p.HasTimelyProof(third.Token) || !p.CanIssue(third.Token) ||
		!p.MarkIssued(third.Token) {
		t.Fatal("timely feedback did not release one next sibling")
	}
	fourth := p.ObserveRealDemand(stream, 0x4140)
	if p.CanIssue(fourth.Token) {
		t.Fatal("timely proof incorrectly enabled unlimited issue")
	}
	p.ResolvePairedRead(third.Token, true)
	fifth := p.ObserveRealDemand(stream, 0x4180)
	if !p.CanIssue(fifth.Token) {
		t.Fatal("late feedback did not release the next trial")
	}
}
