package writeback

import (
	"github.com/sarchlab/akita/v3/mem/cache"
	"github.com/sarchlab/akita/v3/mem/mem"
	"github.com/sarchlab/akita/v3/mem/vm"
	"github.com/sarchlab/akita/v3/sim"
)

type localPrefetchLineKey struct {
	pid  vm.PID
	line uint64
}

type localPrefetchCandidate struct {
	pid        vm.PID
	line       uint64
	address    uint64
	token      PatternToken
	patternKey TypedFilterKey
	filters    [3]*TypedCuckooFilter
	lookups    [3]TypedFilterLookup
	ungated    bool
}

type localPrefetchRecord struct {
	key              localPrefetchLineKey
	token            PatternToken
	patternKey       TypedFilterKey
	block            *cache.Block
	useful           bool
	dramIssued       bool
	ungated          bool
	demandSeen       bool
	lateFeedbackSent bool
}

// LocalFilterPrefetchStats reports both removed candidates and useful work.
// Every issued request is one ordinary 64-B logical cacheline request.
type LocalFilterPrefetchStats struct {
	Enabled                  bool
	RealReadDemands          uint64
	Candidates               uint64
	PatternInstalls          uint64
	PatternInstallDrops      uint64
	CandidateBusyDrops       uint64
	PatternNegativeDrops     uint64
	ResidentPositiveDrops    uint64
	PendingPositiveDrops     uint64
	WrongSliceDrops          uint64
	DemandPriorityDrops      uint64
	ControllerBusyDrops      uint64
	OutputBusyDrops          uint64
	MSHRDrops                uint64
	DemandMSHRCoveredDrops   uint64
	PrefetchMSHRCoveredDrops uint64
	VictimDrops              uint64
	PendingInsertDrops       uint64
	Issued                   uint64
	Outstanding              uint64
	RedundantRaces           uint64
	Useful                   uint64
	Timely                   uint64
	Late                     uint64
	LateAfterDRAMIssue       uint64
	DemandMerges             uint64
	DemandWonRaces           uint64
	Unused                   uint64
	UnusedEvictions          uint64
	UnusedResetRetirements   uint64
	CurrentPrefetchOnlyLines uint64
	PeakPrefetchOnlyLines    uint64
	Fills                    uint64
	AdditionalDRAMReads      uint64
	DemandDelayEvents        uint64
	MSHRHeadroomDrops        uint64
	DemandPathBusyDrops      uint64
	TimelyProofBusyIssues    uint64
	TrainingPendingDrops     uint64
	OutstandingCapacityDrops uint64
	PredictorOnlyCandidates  uint64
	UngatedCandidates        uint64
}

func (c *Cache) GetLocalFilterPrefetchStats() LocalFilterPrefetchStats {
	stats := c.localPrefetchStats
	stats.Enabled = c.filterPrefetchEnabled
	if c.localPrefetchOutstanding > 0 {
		stats.Outstanding = uint64(c.localPrefetchOutstanding)
	}
	return stats
}

// GetLocalFilterPrefetchPredictorStats returns one copy for a shared GPU-level
// predictor so reporting does not multiply counters by the slice count.
func (c *Cache) GetLocalFilterPrefetchPredictorStats() (
	DemandStridePredictorStats,
	bool,
) {
	if c.filterPrefetcher == nil || !c.localPrefetchLeader {
		return DemandStridePredictorStats{}, false
	}
	return c.filterPrefetcher.Stats(), true
}

// ConnectFilterCoupledPrefetchGroup shares one bounded predictor at a GPU's
// L2 front end while retaining exactly one physical metadata Filter per slice.
// The candidate is dispatched to the slice selected by the existing
// interleaving; no cache data or MSHR state is shared.
func ConnectFilterCoupledPrefetchGroup(
	caches []*Cache,
	predictorEntries int,
	interleave uint64,
	pageBytes uint64,
) {
	if len(caches) == 0 {
		return
	}
	lineBytes := uint64(1) << caches[0].log2BlockSize
	predictor := NewPageLocalDemandStridePredictor(
		predictorEntries, lineBytes, pageBytes)
	predictor.EnableCandidateOnPatternEstablishment()
	predictor.EnableExponentialLateLookahead()
	predictor.EnablePageFrontierClamping()
	patternFilters := make(map[TypedFilterKey]*TypedCuckooFilter)
	leaderAssigned := false
	for _, cache := range caches {
		if cache == nil || !cache.filterPrefetchEnabled {
			continue
		}
		cache.filterPrefetcher = predictor
		cache.localPrefetchPatternFilters = patternFilters
		cache.localPrefetchPeers = caches
		cache.localPrefetchInterleave = interleave
		cache.localPrefetchLeader = !leaderAssigned
		leaderAssigned = true
	}
}

func (c *Cache) observeLocalReadDemand(now sim.VTimeInSec, read *mem.ReadReq) {
	if !c.filterPrefetchEnabled || c.filterPrefetcher == nil ||
		c.requestFilter == nil || read == nil {
		return
	}
	line, _ := getCacheLineID(read.Address, c.log2BlockSize)
	c.localPrefetchStats.RealReadDemands++
	c.markLocalPrefetchUseful(read.PID, line)

	stream := DemandStreamKey{PID: read.PID, StreamID: read.StreamID}
	if read.Src != nil {
		stream.Source = uint64(TypedFilterOwnerID(read.Src.Name()))
	}
	observation := c.filterPrefetcher.ObserveRealDemandWithMinimumLookahead(
		stream, read.Address, c.localPrefetchMinimumLookahead())
	if observation.DeletePattern != nil {
		filter := c.localPrefetchPatternFilters[*observation.DeletePattern]
		if filter != nil {
			filter.ScheduleUpdate(now, *observation.DeletePattern, true)
		}
		delete(c.localPrefetchPatternFilters, *observation.DeletePattern)
	}
	if observation.InstallPattern != nil {
		installed := c.filterPrefetchPredictorOnly || c.filterPrefetchUngated
		if !installed {
			installed = c.requestFilter.TryScheduleUpdate(
				now, *observation.InstallPattern, false)
		}
		c.filterPrefetcher.SetPatternInstalled(observation.Token, installed)
		if installed {
			if !c.filterPrefetchPredictorOnly && !c.filterPrefetchUngated {
				c.localPrefetchPatternFilters[*observation.InstallPattern] =
					c.requestFilter
			}
			c.localPrefetchStats.PatternInstalls++
		} else {
			c.localPrefetchStats.PatternInstallDrops++
		}
	}
	if !observation.HasCandidate {
		return
	}
	c.localPrefetchStats.Candidates++
	target := c.localPrefetchTarget(observation.Candidate)
	if target == nil || target.requestFilter == nil {
		c.localPrefetchStats.WrongSliceDrops++
		return
	}
	if target.localPrefetchCandidate != nil {
		target.localPrefetchStats.CandidateBusyDrops++
		return
	}
	if c.filterPrefetchPredictorOnly {
		c.localPrefetchStats.PredictorOnlyCandidates++
		return
	}
	candidateLine, _ := getCacheLineID(observation.Candidate, c.log2BlockSize)
	if c.filterPrefetchUngated {
		target.localPrefetchStats.UngatedCandidates++
		target.localPrefetchCandidate = &localPrefetchCandidate{
			pid: read.PID, line: candidateLine,
			address: observation.Candidate, token: observation.Token,
			patternKey: observation.Token.Key, ungated: true,
		}
		target.TickLater(now)
		return
	}
	patternFilter := c.localPrefetchPatternFilters[observation.Token.Key]
	if patternFilter == nil {
		target.localPrefetchStats.PatternNegativeDrops++
		return
	}
	patternKey := observation.Token.Key
	residentKey := TypedFilterKey{
		PID: read.PID, Address: candidateLine, Type: FilterResident,
	}
	pendingKey := residentKey
	pendingKey.Type = FilterPending
	keys := [3]TypedFilterKey{patternKey, residentKey, pendingKey}
	candidate := &localPrefetchCandidate{
		pid: read.PID, line: candidateLine,
		address: observation.Candidate,
		token:   observation.Token, patternKey: patternKey,
	}
	// PATTERN belongs to the shared predictor stream and remains in the slice
	// that installed it. RESIDENT and PENDING belong to the candidate line and
	// are checked in its physical target slice. Keeping this ownership avoids
	// migrating identical PATTERN metadata on every interleaved cacheline.
	candidate.filters = [3]*TypedCuckooFilter{
		patternFilter, target.requestFilter, target.requestFilter,
	}
	for i, key := range keys {
		lookup, accepted := candidate.filters[i].StartLookup(now, key)
		if !accepted {
			target.localPrefetchStats.CandidateBusyDrops++
			return
		}
		candidate.lookups[i] = lookup
	}
	target.localPrefetchCandidate = candidate
	target.TickLater(now)
}

// localPrefetchMinimumLookahead places the speculative line just beyond the
// average live-MSHR horizon already visible at the four-slice L2 front end.
// The per-slice speculative bound is one, so including that bounded entry is
// conservative and avoids scanning every request list on every demand. The
// result needs no fixed latency/distance parameter and remains page bounded.
func (c *Cache) localPrefetchMinimumLookahead() uint64 {
	peers := c.localPrefetchPeers
	if len(peers) == 0 {
		peers = []*Cache{c}
	}
	visibleEntries := 0
	for _, peer := range peers {
		if peer == nil || peer.mshr == nil {
			continue
		}
		visibleEntries += len(peer.mshr.AllEntries())
	}
	return uint64((visibleEntries+len(peers)-1)/len(peers) + 1)
}

func (c *Cache) runLocalFilterPrefetch(now sim.VTimeInSec) bool {
	candidate := c.localPrefetchCandidate
	if !c.filterPrefetchEnabled || candidate == nil || c.requestFilter == nil {
		return false
	}
	if !candidate.ungated {
		results := [3]bool{}
		for i, lookup := range candidate.lookups {
			filter := candidate.filters[i]
			if filter == nil {
				c.localPrefetchStats.CandidateBusyDrops++
				c.localPrefetchCandidate = nil
				return true
			}
			possible, reliable, ready := filter.CompleteLookup(now, lookup)
			if !ready {
				c.TickLater(now)
				return false
			}
			// Fail open for RESIDENT/PENDING, fail closed for PATTERN.
			if !reliable {
				if i == 0 {
					c.localPrefetchStats.PatternNegativeDrops++
				} else if i == 1 {
					c.localPrefetchStats.ResidentPositiveDrops++
				} else {
					c.localPrefetchStats.PendingPositiveDrops++
				}
				c.localPrefetchCandidate = nil
				return true
			}
			results[i] = possible
		}
		if !results[0] {
			c.localPrefetchStats.PatternNegativeDrops++
			c.filterPrefetcher.InvalidatePattern(candidate.token)
			delete(c.localPrefetchPatternFilters, candidate.patternKey)
			c.localPrefetchCandidate = nil
			return true
		}
		if results[1] {
			c.localPrefetchStats.ResidentPositiveDrops++
			c.localPrefetchCandidate = nil
			return true
		}
		if results[2] {
			c.localPrefetchStats.PendingPositiveDrops++
			c.localPrefetchCandidate = nil
			return true
		}
	}
	if entry := c.mshr.Query(candidate.pid, candidate.line); entry != nil {
		c.localPrefetchStats.MSHRDrops++
		if localPrefetchLeadsMSHR(entry) {
			c.localPrefetchStats.PrefetchMSHRCoveredDrops++
		} else {
			c.localPrefetchStats.DemandMSHRCoveredDrops++
			c.filterPrefetcher.ObserveDemandCoveredCandidate(candidate.token)
		}
		c.localPrefetchCandidate = nil
		return true
	}
	if !c.filterPrefetcher.CanIssue(candidate.token) {
		c.localPrefetchStats.TrainingPendingDrops++
		c.localPrefetchCandidate = nil
		return true
	}

	// Keep exactly one training request for an unproven stream. A stream that
	// has already delivered a timely line may use otherwise-free existing MSHR
	// credits below; this is required to sustain enough memory-level parallelism
	// to cover DRAM latency without introducing a separate prefetch queue or a
	// fixed prefetch-depth parameter.
	timelyProven := c.filterPrefetcher.HasTimelyProof(candidate.token)
	if c.localPrefetchOutstanding >= 1 && !timelyProven {
		c.localPrefetchStats.OutstandingCapacityDrops++
		c.localPrefetchCandidate = nil
		return true
	}

	// The candidate never waits for a demand-facing resource.
	if c.topPort.Peek() != nil {
		c.localPrefetchStats.DemandPriorityDrops++
		c.localPrefetchCandidate = nil
		return true
	}
	if c.writeBufferBuffer.Size() != 0 {
		c.localPrefetchStats.ControllerBusyDrops++
		c.localPrefetchCandidate = nil
		return true
	}
	if !c.dirStageBuffer.CanPush() || !c.bottomSender.CanSend(1) {
		c.localPrefetchStats.OutputBusyDrops++
		c.localPrefetchCandidate = nil
		return true
	}
	mshrEntries := len(c.mshr.AllEntries())
	if mshrEntries >= c.mshrCapacity {
		c.localPrefetchStats.MSHRDrops++
		c.localPrefetchCandidate = nil
		return true
	}
	// An unproven stream trains only while the target slice is fully idle. Once
	// this exact predictor generation has delivered a timely line, it may use
	// the single bounded speculative slot while ordinary demand MSHRs remain
	// live. The existing headroom check still reserves demand capacity, and the
	// front-end/controller/output checks above keep every issue work-conserving.
	// This feedback gate avoids both a tuned occupancy watermark and the nearly
	// zero coverage caused by requiring the whole slice to drain between every
	// useful steady-state prefetch.
	if mshrEntries != 0 && !timelyProven {
		c.localPrefetchStats.DemandPathBusyDrops++
		c.localPrefetchCandidate = nil
		return true
	}
	if mshrEntries != 0 {
		c.localPrefetchStats.TimelyProofBusyIssues++
	}
	// Each outstanding speculative line reserves one additional demand credit.
	// This scales with actual speculation instead of imposing a tuned occupancy
	// watermark, and prevents prefetches from consuming all visible MSHRs.
	if mshrEntries+c.localPrefetchOutstanding+1 >= c.mshrCapacity {
		c.localPrefetchStats.MSHRHeadroomDrops++
		c.localPrefetchCandidate = nil
		return true
	}
	victim := c.directory.FindVictim(candidate.line)
	if victim == nil || victim.IsValid || victim.IsLocked || victim.ReadCount > 0 {
		c.localPrefetchStats.VictimDrops++
		c.localPrefetchCandidate = nil
		return true
	}
	pendingKey := TypedFilterKey{
		PID: candidate.pid, Address: candidate.line, Type: FilterPending,
	}
	if !candidate.ungated &&
		!c.requestFilter.TryScheduleUpdate(now, pendingKey, false) {
		c.localPrefetchStats.PendingInsertDrops++
		c.localPrefetchCandidate = nil
		return true
	}

	read := mem.ReadReqBuilder{}.
		WithSendTime(now).
		WithSrc(c.topPort).
		WithDst(c.topPort).
		WithPID(candidate.pid).
		WithAddress(candidate.address).
		WithByteSize(1 << c.log2BlockSize).
		WithInfo("filter-coupled-prefetch").
		Build()
	trans := &transaction{
		id: sim.GetIDGenerator().Generate(), read: read, prefetch: true,
		l2Arrival: now,
		// The admission lookup already returned a reliable RESIDENT negative.
		// Carry that proof into the directory stage instead of paying for the
		// same Filter query twice.
		residentFilterChecked:  !candidate.ungated,
		residentFilterNegative: !candidate.ungated,
		residentFastMiss:       !candidate.ungated,
	}
	record := &localPrefetchRecord{
		key:   localPrefetchLineKey{pid: candidate.pid, line: candidate.line},
		token: candidate.token, patternKey: candidate.patternKey,
		ungated: candidate.ungated,
	}
	c.localPrefetchByLine[record.key] = record
	c.inFlightTransactions = append(c.inFlightTransactions, trans)
	c.dirStageBuffer.Push(trans)
	c.localPrefetchStats.Issued++
	c.localPrefetchOutstanding++
	if !c.filterPrefetcher.MarkIssued(candidate.token) {
		panic("local prefetch issue lost predictor training reservation")
	}
	c.localPrefetchCandidate = nil
	return true
}

func (c *Cache) ownsAddress(address uint64) bool {
	if !c.interleaving || c.interleavingUnits <= 1 {
		return true
	}
	span := uint64(c.interleavingBlocks) << c.log2BlockSize
	if span == 0 {
		return false
	}
	return int((address/span)%uint64(c.interleavingUnits)) == c.interleavingIndex
}

func (c *Cache) localPrefetchTarget(address uint64) *Cache {
	if len(c.localPrefetchPeers) == 0 || c.localPrefetchInterleave == 0 {
		if c.ownsAddress(address) {
			return c
		}
		return nil
	}
	index := address / c.localPrefetchInterleave %
		uint64(len(c.localPrefetchPeers))
	return c.localPrefetchPeers[index]
}

func (c *Cache) markLocalPrefetchUseful(pid vm.PID, line uint64) {
	record := c.localPrefetchByLine[localPrefetchLineKey{pid: pid, line: line}]
	if record == nil || record.useful {
		return
	}
	record.demandSeen = true
	if record.block != nil && record.block.IsValid && !record.block.IsLocked &&
		record.block.PID == pid && record.block.Tag == line {
		c.consumeLocalPrefetch(record, record.lateFeedbackSent)
		return
	}
	entry := c.mshr.Query(pid, line)
	if !localPrefetchLeadsMSHR(entry) {
		if !record.lateFeedbackSent {
			c.filterPrefetcher.ObserveLate(record.token)
			record.lateFeedbackSent = true
		}
		return
	}
	c.consumeLocalPrefetch(record, true)
}

func localPrefetchLeadsMSHR(entry *cache.MSHREntry) bool {
	if entry == nil {
		return false
	}
	for _, request := range entry.Requests {
		trans, ok := request.(*transaction)
		if !ok {
			continue
		}
		return trans.prefetch
	}
	return false
}

func (c *Cache) consumeLocalPrefetch(
	record *localPrefetchRecord,
	late bool,
) {
	if record == nil || record.useful {
		return
	}
	record.useful = true
	c.localPrefetchStats.Useful++
	if late {
		c.localPrefetchStats.DemandMerges++
		c.localPrefetchStats.Late++
		if record.dramIssued {
			c.localPrefetchStats.LateAfterDRAMIssue++
		}
	} else {
		c.localPrefetchStats.Timely++
	}
	if late && record.lateFeedbackSent {
		c.filterPrefetcher.RewardAfterObservedLate(record.token)
	} else {
		c.filterPrefetcher.RewardWithTimeliness(record.token, late)
	}
	if record.block != nil {
		delete(c.localPrefetchByBlock, record.block)
		delete(c.localPrefetchByLine, record.key)
		if c.localPrefetchStats.CurrentPrefetchOnlyLines > 0 {
			c.localPrefetchStats.CurrentPrefetchOnlyLines--
		}
	}
}

func (c *Cache) completeLocalPrefetchFill(
	trans *transaction,
	block *cache.Block,
) {
	if trans == nil || !trans.prefetch || trans.granularitySibling ||
		trans.read == nil {
		return
	}
	line, _ := getCacheLineID(trans.read.Address, c.log2BlockSize)
	key := localPrefetchLineKey{pid: trans.read.PID, line: line}
	record := c.localPrefetchByLine[key]
	if record == nil || !record.ungated {
		c.requestFilter.ScheduleUpdate(c.Engine.CurrentTime(), TypedFilterKey{
			PID: key.pid, Address: key.line, Type: FilterPending,
		}, true)
	}
	c.localPrefetchStats.Fills++
	if record == nil {
		return
	}
	if c.localPrefetchOutstanding > 0 {
		c.localPrefetchOutstanding--
	}
	if record.useful {
		delete(c.localPrefetchByLine, key)
		return
	}
	record.block = block
	c.localPrefetchByBlock[block] = record
	c.localPrefetchStats.CurrentPrefetchOnlyLines++
	if c.localPrefetchStats.CurrentPrefetchOnlyLines >
		c.localPrefetchStats.PeakPrefetchOnlyLines {
		c.localPrefetchStats.PeakPrefetchOnlyLines =
			c.localPrefetchStats.CurrentPrefetchOnlyLines
	}
}

func (c *Cache) finishLocalPrefetchWithoutFill(pid vm.PID, address uint64) {
	line, _ := getCacheLineID(address, c.log2BlockSize)
	key := localPrefetchLineKey{pid: pid, line: line}
	record := c.localPrefetchByLine[key]
	if record == nil || !record.ungated {
		c.requestFilter.ScheduleUpdate(c.Engine.CurrentTime(), TypedFilterKey{
			PID: pid, Address: line, Type: FilterPending,
		}, true)
	}
	delete(c.localPrefetchByLine, key)
	if record != nil && record.block == nil && c.localPrefetchOutstanding > 0 {
		c.localPrefetchOutstanding--
	}
	c.localPrefetchStats.RedundantRaces++
	if record != nil && record.demandSeen && !record.useful {
		c.localPrefetchStats.DemandWonRaces++
		if !record.lateFeedbackSent {
			c.filterPrefetcher.ObserveLate(record.token)
			record.lateFeedbackSent = true
		}
	}
}

func (c *Cache) retireUnusedLocalPrefetch(block *cache.Block) {
	if block == nil || c.filterPrefetcher == nil {
		return
	}
	record := c.localPrefetchByBlock[block]
	if record == nil {
		return
	}
	delete(c.localPrefetchByBlock, block)
	delete(c.localPrefetchByLine, record.key)
	if c.localPrefetchStats.CurrentPrefetchOnlyLines > 0 {
		c.localPrefetchStats.CurrentPrefetchOnlyLines--
	}
	if !record.useful {
		c.localPrefetchStats.Unused++
		c.localPrefetchStats.UnusedEvictions++
		c.filterPrefetcher.Penalize(record.token)
		if !record.ungated {
			c.requestFilter.ScheduleUpdate(
				c.Engine.CurrentTime(), record.patternKey, true)
			delete(c.localPrefetchPatternFilters, record.patternKey)
		}
	}
}

func (c *Cache) resetLocalFilterPrefetch() {
	if c.filterPrefetcher == nil {
		return
	}
	for _, record := range c.localPrefetchByBlock {
		if record == nil || record.useful {
			continue
		}
		c.localPrefetchStats.Unused++
		c.localPrefetchStats.UnusedResetRetirements++
		c.filterPrefetcher.Penalize(record.token)
	}
	if c.requestFilter != nil {
		for key := range c.localPrefetchByLine {
			c.requestFilter.Delete(TypedFilterKey{
				PID: key.pid, Address: key.line, Type: FilterPending,
			})
		}
		c.requestFilter.ClearType(FilterPattern)
	}
	c.filterPrefetcher.Reset()
	c.localPrefetchCandidate = nil
	clear(c.localPrefetchByLine)
	clear(c.localPrefetchByBlock)
	clear(c.localPrefetchPatternFilters)
	c.localPrefetchOutstanding = 0
	c.localPrefetchStats.CurrentPrefetchOnlyLines = 0
}

func (c *Cache) markLocalPrefetchDRAMIssued(pid vm.PID, address uint64) {
	line, _ := getCacheLineID(address, c.log2BlockSize)
	record := c.localPrefetchByLine[localPrefetchLineKey{pid: pid, line: line}]
	if record == nil || record.dramIssued {
		return
	}
	record.dramIssued = true
	c.localPrefetchStats.AdditionalDRAMReads++
}
