package writeback

import (
	"github.com/sarchlab/akita/v3/mem/cache"
	"github.com/sarchlab/akita/v3/mem/vm"
	"github.com/sarchlab/akita/v3/sim"
)

const granularityPageBytes = uint64(4096)

type granularityLineKey struct {
	pid  vm.PID
	line uint64
}

type granularityCandidate struct {
	pid              vm.PID
	address          uint64
	line             uint64
	pairProbeOnly    bool
	pairProbeBypass  bool
	token            PatternToken
	hasToken         bool
	gated            bool
	patternFilter    *TypedCuckooFilter
	patternLookup    TypedFilterLookup
	patternLookupSet bool
	lookups          [2]TypedFilterLookup
	lookupSet        [2]bool
	results          [2]bool
}

type granularityRecord struct {
	key      granularityLineKey
	token    PatternToken
	hasToken bool
	block    *cache.Block
	useful   bool
	filled   bool
}

// GranularityAdaptationStats separates prediction, Filter decisions,
// controller requests, and the eventual usefulness of the sibling line. A
// paired-read descriptor is not counted as one native HBM transaction here;
// the DRAM controller reports its two physical 64-B transactions separately.
type GranularityAdaptationStats struct {
	Enabled                      bool
	WithoutFilter                bool
	AlwaysExpand                 bool
	PredictorOnly                bool
	RealReadDemands              uint64
	PatternsEstablished          uint64
	PatternInsertDrops           uint64
	PatternFilterPositives       uint64
	PatternNegativeDrops         uint64
	PredictorThrottledDrops      uint64
	PredictedCandidates          uint64
	SiblingCandidates            uint64
	PredictorOnlyCandidates      uint64
	CandidateNotSiblingDrops     uint64
	PageBoundaryDrops            uint64
	WrongSliceDrops              uint64
	WrongControllerDrops         uint64
	FilterBusyDrops              uint64
	FilterNotReadyDrops          uint64
	FilterUnreliableDrops        uint64
	ResidentFilterPositives      uint64
	PendingFilterPositives       uint64
	ResidentNegativeLookupSkips  uint64
	PendingNegativeLookupSkips   uint64
	ResidentExactLookups         uint64
	PendingExactLookups          uint64
	ResidentExactSuppressions    uint64
	PendingExactSuppressions     uint64
	ResidentFalsePositives       uint64
	PendingFalsePositives        uint64
	MSHRPressureDrops            uint64
	InflightCapacityDrops        uint64
	DRAMQueuePressureDrops       uint64
	VictimUnavailableDrops       uint64
	RemoteVictimProtectionDrops  uint64
	CleanVictimDisplacements     uint64
	PendingInsertDrops           uint64
	DemandPendingInsertFailures  uint64
	ExpansionAttempts            uint64
	AcceptedExpansions           uint64
	DemandPairReadyOpportunities uint64
	DemandPairFilterProbes       uint64
	DemandPairFilterPositives    uint64
	DemandPairFilterNegatives    uint64
	DemandPairAggregates         uint64
	DemandPairResourceDrops      uint64
	FrontendSingle64Descriptors  uint64
	FrontendPairedReadAggregates uint64
	FrontendReadBytes            uint64
	SiblingFills                 uint64
	SiblingInflightMerges        uint64
	SiblingL2Hits                uint64
	UsefulSiblingLines           uint64
	TimelySiblingLines           uint64
	LateSiblingLines             uint64
	UnusedSiblingLines           uint64
	UnusedSiblingEvictions       uint64
	UnusedSiblingResetRetires    uint64
	CurrentSiblingOnlyLines      uint64
	PeakSiblingOnlyLines         uint64
	UsefulSiblingBytes           uint64
	WastedSiblingBytes           uint64
}

// granularityExactLookupPlan turns reliable membership results into the
// precise checks that still need to be performed. A reliable negative is the
// Filter's useful fast path: it proves absence from that summarized structure
// and avoids an additional sibling lookup. A positive is only a hint and must
// be confirmed by the exact directory or MSHR.
func granularityExactLookupPlan(
	candidate *granularityCandidate,
) (checkResident, checkPending bool) {
	if candidate == nil || !candidate.gated {
		if candidate != nil && candidate.pairProbeOnly {
			return false, true
		}
		return true, true
	}
	if candidate.pairProbeOnly {
		return false, candidate.results[1]
	}
	return candidate.results[0], candidate.results[1]
}

func (c *Cache) granularityVictimAdmissible(
	victim *cache.Block,
) (admissible, protectsRemote bool) {
	if victim == nil || victim.IsDirty || victim.IsLocked ||
		victim.ReadCount > 0 {
		return false, false
	}
	if _, remote := c.remoteReplicaBlocks[victim]; remote {
		return false, true
	}
	return true, false
}

// ConnectGranularityAdaptationGroup shares one bounded demand predictor among
// the L2 slices of a GPU.  Each slice retains its own physical Cuckoo Filter,
// MSHRs, directory, and data array.
func ConnectGranularityAdaptationGroup(
	caches []*Cache,
	predictorEntries int,
	pageBytes uint64,
) {
	if len(caches) == 0 {
		return
	}
	lineBytes := uint64(1) << caches[0].log2BlockSize
	predictor := NewPageLocalDemandStridePredictor(
		predictorEntries, lineBytes, pageBytes)
	predictor.EnableCandidateOnPatternEstablishment()
	predictor.EnableFeedbackGatedIssue()
	patternFilters := make(map[TypedFilterKey]*TypedCuckooFilter)
	leaderAssigned := false
	for _, cache := range caches {
		if cache == nil || !cache.granularityAdaptationEnabled {
			continue
		}
		cache.granularityPredictor = predictor
		cache.granularityPatternFilters = patternFilters
		cache.granularityLeader = !leaderAssigned
		leaderAssigned = true
	}
}

func (c *Cache) GetGranularityAdaptationStats() GranularityAdaptationStats {
	stats := c.granularityStats
	stats.Enabled = c.granularityAdaptationEnabled
	stats.WithoutFilter = c.granularityWithoutFilter
	stats.AlwaysExpand = c.granularityAlwaysExpand
	stats.PredictorOnly = c.granularityPredictorOnly
	return stats
}

func (c *Cache) GetGranularityAdaptationPredictorStats() (
	DemandStridePredictorStats,
	bool,
) {
	if c.granularityPredictor == nil || !c.granularityLeader {
		return DemandStridePredictorStats{}, false
	}
	return c.granularityPredictor.Stats(), true
}

// observeGranularityDemand trains only on accepted real reads.  Filter
// lookups begin at L2 arrival and overlap the ordinary directory/bank path;
// the demand never waits for them.
func (c *Cache) observeGranularityDemand(
	now sim.VTimeInSec,
	trans *transaction,
) {
	if !c.granularityAdaptationEnabled || trans == nil || trans.read == nil ||
		trans.read.LookupOnly || trans.prefetch {
		return
	}
	read := trans.read
	line, _ := getCacheLineID(read.Address, c.log2BlockSize)
	c.granularityStats.RealReadDemands++
	c.markGranularitySiblingUseful(read.PID, line)

	lineBytes := uint64(1) << c.log2BlockSize
	base := line & ^(2*lineBytes - 1)
	sibling := base
	if sibling == line {
		sibling += lineBytes
	}

	candidateAddress := sibling
	var token PatternToken
	hasToken := false
	// Probe every real demand for an already-existing adjacent miss.  This is
	// the M1 fast path explicitly allowed to ignore Filter lookup latency: it
	// reads the same per-slice Cuckoo Filter contents, but does not consume a
	// modeled lookup ticket or hold the mandatory request.  Prediction-created
	// work still uses the ordinary latency/width-bearing Filter path below.
	pairProbeOnly := !c.granularityAlwaysExpand
	if !c.granularityAlwaysExpand {
		if c.granularityPredictor == nil {
			if !pairProbeOnly {
				return
			}
		} else {
			streamID := read.LocalStreamID
			if streamID == 0 {
				streamID = read.StreamID
			}
			stream := DemandStreamKey{PID: read.PID, StreamID: streamID}
			if read.Src != nil {
				stream.Source = uint64(TypedFilterOwnerID(read.Src.Name()))
			}
			observation := c.granularityPredictor.ObserveRealDemand(
				stream, read.Address)
			if observation.DeletePattern != nil {
				filter := c.granularityPatternFilters[*observation.DeletePattern]
				if filter != nil {
					filter.ScheduleUpdate(now, *observation.DeletePattern, true)
				}
				delete(c.granularityPatternFilters, *observation.DeletePattern)
			}
			if observation.InstallPattern != nil {
				installed := c.granularityWithoutFilter ||
					c.granularityPredictorOnly
				if !installed && c.requestFilter != nil {
					installed = c.requestFilter.TryScheduleUpdate(
						now, *observation.InstallPattern, false)
					if installed {
						c.granularityPatternFilters[*observation.InstallPattern] =
							c.requestFilter
					}
				}
				c.granularityPredictor.SetPatternInstalled(
					observation.Token, installed)
				if installed {
					c.granularityStats.PatternsEstablished++
				} else {
					c.granularityStats.PatternInsertDrops++
				}
			}
			if observation.HasCandidate {
				c.granularityStats.PredictedCandidates++
				candidateAddress = observation.Candidate
				token = observation.Token
				hasToken = true
				pairProbeOnly = false
			} else if !pairProbeOnly {
				return
			}
		}
	}

	candidateLine, _ := getCacheLineID(candidateAddress, c.log2BlockSize)
	if candidateLine != sibling {
		c.granularityStats.CandidateNotSiblingDrops++
		if !read.LocalPairHint {
			return
		}
		// A non-sibling prediction cannot create an early fetch, but the
		// mandatory demand may still pair with an already-real sibling.
		candidateAddress = sibling
		candidateLine = sibling
		token = PatternToken{}
		hasToken = false
		pairProbeOnly = true
	}
	if !pairProbeOnly {
		c.granularityStats.SiblingCandidates++
	}
	if line/granularityPageBytes != candidateLine/granularityPageBytes {
		c.granularityStats.PageBoundaryDrops++
		return
	}
	if !c.ownsAddress(candidateLine) {
		c.granularityStats.WrongSliceDrops++
		return
	}
	// Standalone cache tests and partially connected components may not have
	// a lower-module finder. Pair formation is optional, so missing topology
	// metadata must preserve the ordinary demand path.
	if c.lowModuleFinder == nil {
		return
	}
	if c.lowModuleFinder.Find(line) != c.lowModuleFinder.Find(candidateLine) {
		c.granularityStats.WrongControllerDrops++
		return
	}
	if c.granularityPredictorOnly {
		if !pairProbeOnly {
			c.granularityStats.PredictorOnlyCandidates++
		}
		return
	}

	candidate := &granularityCandidate{
		pid: read.PID, address: candidateAddress, line: candidateLine,
		pairProbeOnly: pairProbeOnly,
		token:         token, hasToken: hasToken,
		gated: !c.granularityWithoutFilter,
	}
	if pairProbeOnly {
		c.granularityStats.DemandPairFilterProbes++
	}
	if candidate.gated {
		if c.requestFilter == nil {
			c.granularityStats.FilterUnreliableDrops++
			return
		}
		if candidate.pairProbeOnly {
			possible, reliable := c.requestFilter.Query(TypedFilterKey{
				PID: read.PID, Address: candidateLine,
				Type: FilterGranularityPending,
			})
			if !reliable {
				c.granularityStats.FilterUnreliableDrops++
				return
			}
			candidate.results[1] = possible
			candidate.pairProbeBypass = true
			if possible {
				c.granularityStats.DemandPairFilterPositives++
			} else {
				c.granularityStats.DemandPairFilterNegatives++
			}
			trans.granularityCandidate = candidate
			return
		}
		if candidate.hasToken {
			candidate.patternFilter =
				c.granularityPatternFilters[candidate.token.Key]
			if candidate.patternFilter == nil {
				c.granularityStats.PatternNegativeDrops++
				c.granularityPredictor.InvalidatePattern(candidate.token)
				return
			}
			lookup, accepted := candidate.patternFilter.StartLookup(
				now, candidate.token.Key)
			if !accepted {
				c.granularityStats.FilterBusyDrops++
				return
			}
			candidate.patternLookup = lookup
			candidate.patternLookupSet = true
		}
		keys := [2]TypedFilterKey{
			{PID: read.PID, Address: candidateLine, Type: FilterResident},
			{PID: read.PID, Address: candidateLine, Type: FilterGranularityPending},
		}
		for i, key := range keys {
			if candidate.pairProbeOnly && i == 0 {
				continue
			}
			lookup, accepted := c.requestFilter.StartLookup(now, key)
			if !accepted {
				c.granularityStats.FilterBusyDrops++
				return
			}
			candidate.lookups[i] = lookup
			candidate.lookupSet[i] = true
		}
	}
	trans.granularityCandidate = candidate
}

func (c *Cache) completeGranularityFilterLookups(
	now sim.VTimeInSec,
	candidate *granularityCandidate,
) bool {
	if candidate == nil || !candidate.gated {
		return true
	}
	if candidate.pairProbeBypass {
		return true
	}
	if candidate.hasToken {
		if candidate.patternFilter == nil || !candidate.patternLookupSet {
			c.granularityStats.FilterUnreliableDrops++
			return false
		}
		possible, reliable, ready := candidate.patternFilter.CompleteLookup(
			now, candidate.patternLookup)
		if !ready {
			c.granularityStats.FilterNotReadyDrops++
			return false
		}
		if !reliable {
			c.granularityStats.FilterUnreliableDrops++
			return false
		}
		if !possible {
			c.granularityStats.PatternNegativeDrops++
			c.granularityPredictor.InvalidatePattern(candidate.token)
			delete(c.granularityPatternFilters, candidate.token.Key)
			return false
		}
		if !c.granularityPredictor.CanIssue(candidate.token) {
			c.granularityStats.PredictorThrottledDrops++
			return false
		}
		c.granularityStats.PatternFilterPositives++
	}
	possible := [2]bool{}
	for i := range candidate.lookups {
		if !candidate.lookupSet[i] {
			if candidate.pairProbeOnly && i == 0 {
				continue
			}
			c.granularityStats.FilterUnreliableDrops++
			return false
		}
		result, reliable, ready := c.requestFilter.CompleteLookup(
			now, candidate.lookups[i])
		if !ready {
			c.granularityStats.FilterNotReadyDrops++
			return false
		}
		if !reliable {
			c.granularityStats.FilterUnreliableDrops++
			return false
		}
		possible[i] = result
		candidate.results[i] = result
	}
	if possible[0] {
		c.granularityStats.ResidentFilterPositives++
	}
	if possible[1] {
		c.granularityStats.PendingFilterPositives++
	}
	if candidate.pairProbeOnly {
		if possible[1] {
			c.granularityStats.DemandPairFilterPositives++
		} else {
			c.granularityStats.DemandPairFilterNegatives++
		}
	}
	return true
}

func (c *Cache) trackGranularityPending(
	now sim.VTimeInSec,
	pid vm.PID,
	line uint64,
) {
	if (!c.granularityAdaptationEnabled && !c.adaptivePairEnabled) ||
		c.granularityPredictorOnly ||
		c.requestFilter == nil {
		return
	}
	if !c.requestFilter.ScheduleUpdate(now, TypedFilterKey{
		PID: pid, Address: line, Type: FilterGranularityPending,
	}, false) {
		c.granularityStats.DemandPendingInsertFailures++
		if c.adaptivePairEnabled {
			c.adaptivePairStats.PendingFilterInsertFailure++
		}
	}
}

func (c *Cache) untrackGranularityPending(
	now sim.VTimeInSec,
	pid vm.PID,
	line uint64,
) {
	if (!c.granularityAdaptationEnabled && !c.adaptivePairEnabled) ||
		c.granularityPredictorOnly ||
		c.requestFilter == nil {
		return
	}
	c.requestFilter.ScheduleUpdate(now, TypedFilterKey{
		PID: pid, Address: line, Type: FilterGranularityPending,
	}, true)
}

func (c *Cache) markGranularitySiblingUseful(pid vm.PID, line uint64) {
	record := c.granularityByLine[granularityLineKey{pid: pid, line: line}]
	if record == nil || record.useful {
		return
	}
	record.useful = true
	c.granularityStats.UsefulSiblingLines++
	c.granularityStats.UsefulSiblingBytes += uint64(1) << c.log2BlockSize
	late := !record.filled
	if late {
		c.granularityStats.LateSiblingLines++
	} else {
		c.granularityStats.TimelySiblingLines++
	}
	if record.hasToken && c.granularityPredictor != nil {
		c.granularityPredictor.ResolvePairedRead(record.token, late)
	}
	if record.filled && record.block != nil && record.block.IsValid &&
		!record.block.IsLocked && record.block.PID == pid &&
		record.block.Tag == line {
		c.granularityStats.SiblingL2Hits++
		delete(c.granularityByBlock, record.block)
		delete(c.granularityByLine, record.key)
		if c.granularityStats.CurrentSiblingOnlyLines > 0 {
			c.granularityStats.CurrentSiblingOnlyLines--
		}
		return
	}
	if entry := c.mshr.Query(pid, line); granularitySiblingLeadsMSHR(entry) {
		c.granularityStats.SiblingInflightMerges++
	}
}

func granularitySiblingLeadsMSHR(entry *cache.MSHREntry) bool {
	if entry == nil {
		return false
	}
	for _, raw := range entry.Requests {
		trans, ok := raw.(*transaction)
		if ok && trans.granularitySibling {
			return true
		}
	}
	return false
}

func (c *Cache) completeGranularitySiblingFill(
	trans *transaction,
	block *cache.Block,
) {
	if trans == nil || !trans.granularitySibling || trans.read == nil {
		return
	}
	line, _ := getCacheLineID(trans.read.Address, c.log2BlockSize)
	key := granularityLineKey{pid: trans.read.PID, line: line}
	c.granularityStats.SiblingFills++
	record := c.granularityByLine[key]
	if record == nil {
		return
	}
	record.filled = true
	if record.useful {
		delete(c.granularityByLine, key)
		return
	}
	record.block = block
	c.granularityByBlock[block] = record
	c.granularityStats.CurrentSiblingOnlyLines++
	if c.granularityStats.CurrentSiblingOnlyLines >
		c.granularityStats.PeakSiblingOnlyLines {
		c.granularityStats.PeakSiblingOnlyLines =
			c.granularityStats.CurrentSiblingOnlyLines
	}
}

func (c *Cache) retireUnusedGranularitySibling(block *cache.Block) {
	if block == nil {
		return
	}
	record := c.granularityByBlock[block]
	if record == nil {
		return
	}
	delete(c.granularityByBlock, block)
	delete(c.granularityByLine, record.key)
	if c.granularityStats.CurrentSiblingOnlyLines > 0 {
		c.granularityStats.CurrentSiblingOnlyLines--
	}
	if !record.useful {
		c.granularityStats.UnusedSiblingLines++
		c.granularityStats.UnusedSiblingEvictions++
		c.granularityStats.WastedSiblingBytes += uint64(1) << c.log2BlockSize
		if record.hasToken && c.granularityPredictor != nil {
			if key, ok := c.granularityPredictor.Penalize(record.token); ok {
				filter := c.granularityPatternFilters[key]
				if filter != nil {
					now := sim.VTimeInSec(0)
					if c.Engine != nil {
						now = c.Engine.CurrentTime()
					}
					filter.ScheduleUpdate(now, key, true)
				}
				delete(c.granularityPatternFilters, key)
			}
		}
	}
}

func (c *Cache) resetGranularityAdaptation() {
	if !c.granularityAdaptationEnabled {
		return
	}
	for _, record := range c.granularityByBlock {
		if record == nil || record.useful {
			continue
		}
		c.granularityStats.UnusedSiblingLines++
		c.granularityStats.UnusedSiblingResetRetires++
		c.granularityStats.WastedSiblingBytes += uint64(1) << c.log2BlockSize
	}
	if c.requestFilter != nil {
		c.requestFilter.ClearType(FilterGranularityPending)
	}
	for key, filter := range c.granularityPatternFilters {
		if filter != nil {
			filter.Delete(key)
		}
		delete(c.granularityPatternFilters, key)
	}
	if c.granularityPredictor != nil {
		c.granularityPredictor.Reset()
	}
	clear(c.granularityByLine)
	clear(c.granularityByBlock)
	c.granularityStats.CurrentSiblingOnlyLines = 0
}
