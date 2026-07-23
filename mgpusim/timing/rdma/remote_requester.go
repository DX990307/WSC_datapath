package rdma

import (
	"fmt"

	"github.com/sarchlab/akita/v3/mem/cache/writeback"
	"github.com/sarchlab/akita/v3/mem/mem"
	memtrace "github.com/sarchlab/akita/v3/mem/trace"
	"github.com/sarchlab/akita/v3/sim"
)

// ConfigureRemoteDataPath applies the requester-side remote-data mechanism.
func (c *Comp) ConfigureRemoteDataPath(config RemoteDataPathConfig) {
	c.remoteConfig = normalizeRemoteDataPathConfig(config)
	c.RemoteDataPathStats.Enabled = c.remoteConfig.Enabled
	c.RemoteDataPathStats.DedupEnabled =
		c.remoteConfig.Enabled && !c.remoteConfig.DisableDedup
	c.RemoteDataPathStats.BatchingEnabled =
		c.remoteConfig.Enabled && !c.remoteConfig.DisableBatching
	c.RemoteDataPathStats.RequesterL2Enabled =
		c.remoteConfig.Enabled && !c.remoteConfig.DisableRequesterL2
	c.RemoteDataPathStats.FilterPrefetchEnabled =
		c.remoteConfig.Enabled && c.remoteConfig.EnableFilterPrefetch
	c.RemoteDataPathStats.MaxBatchLines = uint64(c.remoteConfig.MaxBatchLines)
	c.RemoteDataPathStats.MaxBatches = uint64(c.remoteConfig.MaxBatches)
	c.RemoteDataPathStats.LineEntryCapacity =
		uint64(c.remoteOutstandingCapacity())
	c.RemoteDataPathStats.WaiterEntryCapacity =
		uint64(c.remoteOutstandingCapacity())
	c.RemoteDataPathStats.OwnerChildLineCapacity =
		uint64(c.remoteOwnerChildLineCapacity())
	if c.remoteConfig.Enabled {
		c.ensureRemoteDataPathState()
	}
	if c.remoteConfig.Enabled && c.remoteConfig.EnableFilterPrefetch &&
		c.remotePrefetcher == nil {
		c.remotePrefetcher = writeback.NewPageLocalDemandStridePredictor(
			c.remoteConfig.PrefetchEntries, remoteLineBytes, remotePageBytes)
	}
}

// GetRemoteDataPathStats returns a snapshot of the mechanism counters.
func (c *Comp) GetRemoteDataPathStats() RemoteDataPathStats {
	stats := c.RemoteDataPathStats
	if c.remotePrefetcher != nil {
		stats.PrefetchPredictor = c.remotePrefetcher.Stats()
	}
	return stats
}

func (c *Comp) ensureRemoteDataPathState() {
	if c.remoteBatches == nil {
		c.remoteBatches = make(map[remoteBatchKey]*remoteBatch)
	}
	if c.remoteLines == nil {
		c.remoteLines = make(map[remoteLineKey]*remoteLineEntry)
	}
	if c.remoteFilterLookups == nil {
		c.remoteFilterLookups = make(map[string]writeback.TypedFilterLookup)
	}
	if c.remoteHintResults == nil {
		c.remoteHintResults = make(map[string]bool)
	}
	if c.remotePrefetchCandidates == nil {
		c.remotePrefetchCandidates = make(map[string]*remotePrefetchCandidate)
	}
	if c.remotePrefetchObserved == nil {
		c.remotePrefetchObserved = make(map[string]bool)
	}
	if c.remotePatternFilters == nil {
		c.remotePatternFilters = make(
			map[writeback.TypedFilterKey]*writeback.TypedCuckooFilter)
	}
	if c.remoteProbes == nil {
		c.remoteProbes = make(map[string]*remoteProbe)
	}
	if c.remoteSingleInflight == nil {
		c.remoteSingleInflight = make(map[string]*remoteLineEntry)
	}
	if c.remoteBitmapInflight == nil {
		c.remoteBitmapInflight = make(map[string]*remoteBatch)
	}
	if c.remoteFillInflight == nil {
		c.remoteFillInflight = make(map[string]*remoteLineEntry)
	}
	if c.remoteEpochs == nil {
		c.remoteEpochs = make(map[remoteLineIdentity]uint64)
	}
	if c.remoteUncacheable == nil {
		c.remoteUncacheable = make(map[remoteLineIdentity]bool)
	}
	if c.remoteOwnerSubReqs == nil {
		c.remoteOwnerSubReqs = make(map[string]*remoteOwnerSubReq)
	}
	if c.remoteOwnerBatches == nil {
		c.remoteOwnerBatches = make(map[string]*remoteOwnerBatch)
	}
}

// primeRemoteMetadata starts all metadata reads needed by the head request as
// early as possible in the fixed RDMA pipeline. The tickets still complete
// through the typed filter's latency-bearing, width-bounded interface.
func (c *Comp) primeRemoteMetadata(now sim.VTimeInSec, msg sim.Msg) {
	if !c.remoteConfig.Enabled {
		return
	}
	read, ok := msg.(*mem.ReadReq)
	if !ok || !remoteReadBatchable(read) {
		return
	}
	dst := c.RemoteRDMAAddressTable.Find(read.Address)
	c.primeRemotePrefetch(now, read, dst)
	identity := c.remoteIdentity(read, dst)
	filter := c.requestFilterForAddress(identity.lineAddr)
	if filter == nil || filter.Mode() == writeback.TypedFilterDisabled {
		return
	}
	key := remoteLineKey{
		remoteLineIdentity: identity,
		epoch:              c.remoteEpochs[identity],
	}
	negativeResolved := c.remoteBlockedNegativeReqID == read.ID &&
		c.remoteBlockedNegativeKey == key
	if !c.remoteConfig.DisableDedup && !negativeResolved {
		c.primeRemoteFilterLookup(
			now, read.ID,
			remoteTypedFilterKey(identity, writeback.FilterPending),
		)
	}
	if c.remoteConfig.DisableRequesterL2 || c.remoteCacheModules == nil ||
		c.remoteUncacheable[identity] {
		return
	}
	for _, kind := range []writeback.TypedFilterKeyType{
		writeback.FilterSeen, writeback.FilterResident,
	} {
		lookupID := fmt.Sprintf("%s/%d", read.ID, kind)
		if _, complete := c.remoteHintResults[lookupID]; complete {
			continue
		}
		typedKey := remoteTypedFilterKey(identity, kind)
		if kind == writeback.FilterResident {
			typedKey.Owner = 0
		}
		c.primeRemoteFilterLookup(now, lookupID, typedKey)
	}
}

func (c *Comp) primeRemoteFilterLookup(
	now sim.VTimeInSec,
	lookupID string,
	key writeback.TypedFilterKey,
) {
	if _, started := c.remoteFilterLookups[lookupID]; started {
		return
	}
	filter := c.requestFilterForAddress(key.Address)
	if filter == nil || filter.Mode() == writeback.TypedFilterDisabled {
		return
	}
	lookup, accepted := filter.StartLookup(now, key)
	if !accepted {
		c.TickLater(now)
		return
	}
	c.remoteFilterLookups[lookupID] = lookup
	c.TickLater(now)
}

// primeRemotePrefetch overlaps speculative metadata with the fixed RDMA input
// pipeline. It observes each real request once, but never delays that request
// if prediction or any Filter port is unavailable.
func (c *Comp) primeRemotePrefetch(
	now sim.VTimeInSec,
	read *mem.ReadReq,
	dst sim.Port,
) {
	if !c.remoteConfig.EnableFilterPrefetch ||
		c.remoteConfig.DisableBatching || c.remotePrefetcher == nil ||
		read == nil || c.remotePrefetchObserved[read.ID] {
		return
	}
	if len(c.remotePrefetchObserved) >= c.remoteOutstandingCapacity() {
		c.RemoteDataPathStats.PrefetchCapacityDrops++
		return
	}
	c.remotePrefetchObserved[read.ID] = true
	c.RemoteDataPathStats.PrefetchRealDemands++

	source := ""
	if read.Src != nil {
		source = read.Src.Name()
	}
	ownerName := ""
	if dst != nil {
		ownerName = dst.Name()
	}
	observation := c.remotePrefetcher.ObserveRealDemand(
		writeback.DemandStreamKey{
			PID: read.PID,
			Source: uint64(writeback.TypedFilterOwnerID(
				source + "->" + ownerName)),
		},
		read.Address,
	)
	if observation.DeletePattern != nil {
		if filter := c.remotePatternFilters[*observation.DeletePattern]; filter != nil {
			filter.ScheduleUpdate(now, *observation.DeletePattern, true)
		}
		delete(c.remotePatternFilters, *observation.DeletePattern)
	}
	realFilter := c.requestFilterForAddress(read.Address)
	if observation.InstallPattern != nil {
		installed := realFilter != nil && realFilter.TryScheduleUpdate(
			now, *observation.InstallPattern, false)
		c.remotePrefetcher.SetPatternInstalled(observation.Token, installed)
		if installed {
			c.remotePatternFilters[*observation.InstallPattern] = realFilter
			c.RemoteDataPathStats.PrefetchPatternInstalls++
		} else {
			c.RemoteDataPathStats.PrefetchPatternInstallDrops++
		}
	}
	if !observation.HasCandidate {
		return
	}
	c.RemoteDataPathStats.PrefetchCandidates++
	candidateDst := c.RemoteRDMAAddressTable.Find(observation.Candidate)
	if candidateDst == nil || dst == nil || candidateDst.Name() != dst.Name() ||
		remotePageAddress(observation.Candidate) != remotePageAddress(read.Address) {
		c.RemoteDataPathStats.PrefetchSameGroupDrops++
		return
	}
	identity := c.remoteIdentity(read, candidateDst)
	identity.lineAddr = remoteLineAddress(observation.Candidate)
	filter := c.requestFilterForAddress(identity.lineAddr)
	patternFilter := c.remotePatternFilters[observation.Token.Key]
	if filter == nil || patternFilter == nil {
		c.RemoteDataPathStats.PrefetchFilterDrops++
		return
	}
	// PATTERN follows the candidate to the slice that will eventually hold an
	// admitted requester-L2 line. This keeps unused-line feedback local while
	// still using exactly the existing per-slice physical Filters.
	if patternFilter != filter {
		if !filter.TryScheduleUpdate(now, observation.Token.Key, false) {
			c.RemoteDataPathStats.PrefetchFilterDrops++
			return
		}
		patternFilter.ScheduleUpdate(now, observation.Token.Key, true)
		patternFilter = filter
		c.remotePatternFilters[observation.Token.Key] = filter
	}
	residentKey := remoteTypedFilterKey(identity, writeback.FilterResident)
	residentKey.Owner = 0
	keys := [4]writeback.TypedFilterKey{
		observation.Token.Key,
		residentKey,
		remoteTypedFilterKey(identity, writeback.FilterPending),
		remoteTypedFilterKey(identity, writeback.FilterSeen),
	}
	candidate := &remotePrefetchCandidate{
		identity: identity, owner: candidateDst,
		token: observation.Token, patternKey: observation.Token.Key,
	}
	for i, key := range keys {
		lookupFilter := filter
		if i == 0 {
			lookupFilter = patternFilter
		}
		lookup, accepted := lookupFilter.StartLookup(now, key)
		if !accepted {
			c.RemoteDataPathStats.PrefetchFilterDrops++
			return
		}
		candidate.lookups[i] = lookup
	}
	c.remotePrefetchCandidates[read.ID] = candidate
}

func (c *Comp) takeRemotePrefetchCandidate(
	requestID string,
) *remotePrefetchCandidate {
	candidate := c.remotePrefetchCandidates[requestID]
	delete(c.remotePrefetchCandidates, requestID)
	return candidate
}

func (c *Comp) queueRemotePrefetchCandidate(
	now sim.VTimeInSec,
	read *mem.ReadReq,
	candidate *remotePrefetchCandidate,
) {
	if candidate == nil || read == nil {
		return
	}
	filter := c.requestFilterForAddress(candidate.identity.lineAddr)
	patternFilter := c.remotePatternFilters[candidate.patternKey]
	if filter == nil || patternFilter == nil {
		c.RemoteDataPathStats.PrefetchFilterDrops++
		return
	}
	results := [4]bool{}
	for i, lookup := range candidate.lookups {
		lookupFilter := filter
		if i == 0 {
			lookupFilter = patternFilter
		}
		possible, reliable, ready := lookupFilter.CompleteLookup(now, lookup)
		if !ready || !reliable {
			c.RemoteDataPathStats.PrefetchFilterDrops++
			return
		}
		results[i] = possible
	}
	if !results[0] {
		c.remotePrefetcher.InvalidatePattern(candidate.token)
		delete(c.remotePatternFilters, candidate.patternKey)
		c.RemoteDataPathStats.PrefetchFilterDrops++
		return
	}
	if results[1] || results[2] {
		c.RemoteDataPathStats.PrefetchFilterDrops++
		return
	}
	key := remoteLineKey{
		remoteLineIdentity: candidate.identity,
		epoch:              c.remoteEpochs[candidate.identity],
	}
	if c.remoteLines[key] != nil ||
		len(c.remoteLines) >= c.remoteOutstandingCapacity() {
		c.RemoteDataPathStats.PrefetchCapacityDrops++
		return
	}
	pendingKey := remoteTypedFilterKey(
		candidate.identity, writeback.FilterPending)
	if !filter.TryScheduleUpdate(now, pendingKey, false) {
		c.RemoteDataPathStats.PrefetchFilterDrops++
		return
	}
	entry := c.newRemoteDemandEntry(key, candidate.owner, read.Info)
	entry.state = remoteLinePendingBatch
	entry.speculative = true
	// A first-touch speculative response may occupy only an invalid requester-
	// L2 way. Real SEEN/waiter evidence relaxes that restriction to the normal
	// remote-clean replacement policy in the existing L2.
	entry.admit = true
	entry.seenAdmission = results[3]
	entry.patternToken = candidate.token
	entry.patternKey = candidate.patternKey
	c.insertRemoteLine(key, entry)
	c.remotePendingBatch = append(c.remotePendingBatch, entry)
}

func (c *Comp) tryProcessRemoteReqFromL1(
	now sim.VTimeInSec,
	req mem.AccessReq,
	dst sim.Port,
	firstSeen sim.VTimeInSec,
) (handled, progress bool) {
	if !c.remoteConfig.Enabled {
		return false, false
	}
	c.ensureRemoteDataPathState()

	read, ok := req.(*mem.ReadReq)
	if !ok || !remoteReadBatchable(read) {
		if batch := c.conflictingRemoteBatch(req, dst); batch != nil {
			return true, c.flushRemoteBatch(now, batch, flushReasonConflict)
		}
		if _, isWrite := req.(*mem.WriteReq); isWrite {
			for _, identity := range c.remoteIdentities(req, dst) {
				if c.hasUnsentRemoteRead(identity) {
					return true, false
				}
			}
		}
		return false, false
	}
	identity := c.remoteIdentity(read, dst)
	key := remoteLineKey{
		remoteLineIdentity: identity,
		epoch:              c.remoteEpochs[identity],
	}
	var existing *remoteLineEntry
	negativeRetry := c.remoteBlockedNegativeReqID == read.ID &&
		c.remoteBlockedNegativeKey == key
	if !negativeRetry && !c.remoteConfig.DisableDedup {
		mayContain, ready, filterProgress := c.remotePendingMayContain(
			now, read.ID, identity)
		if !ready {
			return true, filterProgress
		}
		if mayContain {
			existing = c.remoteLines[key]
			if existing == nil {
				c.RemoteDataPathStats.InflightFilterFalsePositives++
			}
		}
		if existing == nil {
			// Preserve the completed negative while this request performs the
			// SEEN/RESIDENT metadata lookups or waits for a bounded line entry.
			// A filter false positive followed by an exact miss has reached the
			// same safe conclusion and also need not be queried again.
			c.rememberRemoteBlockedNegative(read.ID, key)
		}
	}
	if entry := existing; entry != nil {
		c.clearRemoteBlockedNegative(read.ID)
		if c.remoteOutstandingReads >= c.remoteOutstandingCapacity() {
			c.RemoteDataPathStats.WaiterEntryFullStalls++
			return true, false
		}
		c.addRemoteWaiter(entry, read, now, firstSeen, 0)
		c.queueRemotePrefetchCandidate(now, read, c.takeRemotePrefetchCandidate(read.ID))
		if len(entry.data) == int(remoteLineBytes) {
			c.queueRemoteReady(entry)
		}
		c.consumeRemoteRead(now, read)
		c.RemoteDataPathStats.DuplicateReads++
		switch entry.state {
		case remoteLineInflight:
			c.RemoteDataPathStats.InflightMerges++
		case remoteLineReady:
			c.RemoteDataPathStats.ReadyMerges++
		default:
			c.RemoteDataPathStats.CollectingMerges++
		}
		return true, true
	}
	if c.remoteConfig.DisableDedup && c.remoteLines[key] != nil {
		for c.remoteLines[key] != nil {
			c.remoteEpochs[identity]++
			key.epoch = c.remoteEpochs[identity]
		}
	}
	// The line table is a bounded coalescing/dedup resource separate from the
	// packet-granular RDMA outstanding table. Existing lines above are allowed
	// to merge even when no new line entry can be allocated.
	if len(c.remoteLines) >= c.remoteOutstandingCapacity() {
		c.rememberRemoteBlockedNegative(read.ID, key)
		c.RemoteDataPathStats.LineEntryFullStalls++
		return true, false
	}
	if c.remoteOutstandingReads >= c.remoteOutstandingCapacity() {
		c.rememberRemoteBlockedNegative(read.ID, key)
		c.RemoteDataPathStats.WaiterEntryFullStalls++
		return true, false
	}
	c.clearRemoteBlockedNegative(read.ID)
	if c.remoteConfig.DisableBatching &&
		!c.canAcceptRequesterOutstanding(1) {
		return true, false
	}

	probeEligible := !c.remoteConfig.DisableRequesterL2 &&
		c.remoteCacheModules != nil && !c.remoteUncacheable[identity]
	seenHit := false
	residentPossible := false
	if probeEligible {
		var ready, lookupProgress bool
		seenHit, ready, lookupProgress = c.remoteHintMayContain(
			now, read.ID, identity, writeback.FilterSeen)
		if !ready {
			return true, lookupProgress
		}
		residentPossible, ready, lookupProgress = c.remoteHintMayContain(
			now, read.ID, identity, writeback.FilterResident)
		if !ready {
			return true, lookupProgress
		}
	}
	// A positive is only a hint. SEEN requests also probe so the existing L2
	// returns an exact hit/miss and the current fill generation. Cold negative
	// requests bypass that otherwise guaranteed-miss L2 lookup.
	shouldProbe := residentPossible || seenHit
	pendingReady, pendingProgress := c.remotePendingInsertReady(
		now, read.ID, identity)
	if !pendingReady {
		return true, pendingProgress
	}
	if shouldProbe {
		probe := mem.ReadReqBuilder{}.
			WithSendTime(now).
			WithSrc(c.ToL2).
			WithDst(c.remoteCacheModules.Find(identity.lineAddr)).
			WithAddress(identity.lineAddr).
			WithByteSize(remoteLineBytes).
			WithPID(read.PID).
			WithInfo(read.Info).
			WithLookupOnly().
			Build()
		if probe.Dst != nil {
			if err := c.ToL2.Send(probe); err != nil {
				return true, false
			}
			entry := c.newRemoteDemandEntry(key, dst, read.Info)
			entry.prefetchCandidate = c.takeRemotePrefetchCandidate(read.ID)
			entry.admit = seenHit
			entry.seenAdmission = seenHit
			if seenHit {
				c.RemoteDataPathStats.SecondTouchAdmissions++
				c.RemoteDataPathStats.TwoTouchCandidates++
			}
			c.addRemoteWaiter(entry, read, now, firstSeen, 0)
			c.insertRemoteLine(key, entry)
			if c.remoteConfig.DisableBatching {
				c.recordRequesterOutstandingPeak()
			}
			c.remoteProbes[probe.ID] = &remoteProbe{
				entry: entry,
				req:   probe,
				sent:  now,
			}
			c.consumeRemoteRead(now, read)
			c.clearRemoteHintResults(read.ID)
			return true, true
		}
	}

	if probeEligible && !shouldProbe {
		c.RemoteDataPathStats.L2OneTouchProbeBypasses++
	}
	entry := c.newRemoteDemandEntry(key, dst, read.Info)
	entry.admit = seenHit
	entry.seenAdmission = seenHit
	if seenHit {
		c.RemoteDataPathStats.SecondTouchAdmissions++
		c.RemoteDataPathStats.TwoTouchCandidates++
	}
	entry.state = remoteLinePendingBatch
	c.addRemoteWaiter(entry, read, now, firstSeen, 0)
	c.insertRemoteLine(key, entry)
	c.remotePendingBatch = append(c.remotePendingBatch, entry)
	c.queueRemotePrefetchCandidate(now, read, c.takeRemotePrefetchCandidate(read.ID))
	if c.remoteConfig.DisableBatching {
		c.recordRequesterOutstandingPeak()
	}
	c.consumeRemoteRead(now, read)
	c.clearRemoteHintResults(read.ID)
	return true, true
}

func (c *Comp) remoteHintMayContain(
	now sim.VTimeInSec,
	reqID string,
	identity remoteLineIdentity,
	kind writeback.TypedFilterKeyType,
) (possible, ready, progress bool) {
	filter := c.requestFilterForAddress(identity.lineAddr)
	if filter == nil || filter.Mode() == writeback.TypedFilterDisabled {
		if kind == writeback.FilterResident {
			return true, true, false
		}
		return false, true, false
	}

	lookupID := fmt.Sprintf("%s/%d", reqID, kind)
	if result, complete := c.remoteHintResults[lookupID]; complete {
		return result, true, false
	}
	key := remoteTypedFilterKey(identity, kind)
	if kind == writeback.FilterResident {
		key.Owner = 0
	}
	lookup, started := c.remoteFilterLookups[lookupID]
	if !started {
		var accepted bool
		lookup, accepted = filter.StartLookup(now, key)
		if !accepted {
			c.TickLater(now)
			return false, false, false
		}
		c.remoteFilterLookups[lookupID] = lookup
		progress = true
	}

	possible, reliable, complete := filter.CompleteLookup(now, lookup)
	if !complete {
		c.TickLater(now)
		return false, false, true
	}
	delete(c.remoteFilterLookups, lookupID)
	if kind == writeback.FilterSeen {
		c.RemoteDataPathStats.SeenQueries++
		if possible && reliable {
			c.RemoteDataPathStats.SeenHits++
			if !filter.ExactContains(key) {
				c.RemoteDataPathStats.SeenFalsePositives++
			}
		} else {
			c.RemoteDataPathStats.SeenNegatives++
		}
	} else if kind == writeback.FilterResident {
		c.RemoteDataPathStats.ResidentQueries++
		if possible && reliable {
			c.RemoteDataPathStats.ResidentPositives++
		} else {
			c.RemoteDataPathStats.ResidentNegatives++
		}
	}
	if !reliable {
		possible = kind == writeback.FilterResident
	}
	c.remoteHintResults[lookupID] = possible
	return possible, true, true
}

func (c *Comp) clearRemoteHintResults(reqID string) {
	delete(c.remoteHintResults,
		fmt.Sprintf("%s/%d", reqID, writeback.FilterSeen))
	delete(c.remoteHintResults,
		fmt.Sprintf("%s/%d", reqID, writeback.FilterResident))
}

func (c *Comp) markRemoteSeen(identity remoteLineIdentity) {
	filter := c.requestFilterForAddress(identity.lineAddr)
	if filter == nil || filter.Mode() == writeback.TypedFilterDisabled {
		return
	}
	key := remoteTypedFilterKey(identity, writeback.FilterSeen)
	if filter.ExactContains(key) {
		return
	}
	if !filter.ScheduleUpdate(c.Engine.CurrentTime(), key, false) {
		c.RemoteDataPathStats.SeenInsertFailures++
	}
}

func (c *Comp) deleteRemoteSeen(identity remoteLineIdentity) {
	filter := c.requestFilterForAddress(identity.lineAddr)
	if filter == nil || filter.Mode() == writeback.TypedFilterDisabled {
		return
	}
	key := remoteTypedFilterKey(identity, writeback.FilterSeen)
	if filter.ExactContains(key) {
		filter.ScheduleUpdate(c.Engine.CurrentTime(), key, true)
	}
}

func (c *Comp) requestFilterForAddress(
	address uint64,
) *writeback.TypedCuckooFilter {
	if len(c.requestFilters) == 0 || c.requestFilterInterleave == 0 {
		return nil
	}
	index := address / c.requestFilterInterleave % uint64(len(c.requestFilters))
	return c.requestFilters[index]
}

func remoteTypedFilterKey(
	identity remoteLineIdentity,
	kind writeback.TypedFilterKeyType,
) writeback.TypedFilterKey {
	return writeback.TypedFilterKey{
		PID:     identity.pid,
		Owner:   writeback.TypedFilterOwnerID(identity.ownerName),
		Address: identity.lineAddr,
		Type:    kind,
	}
}

// remoteInflightMayContain remains as a test/debug snapshot helper. The live
// path uses remotePendingMayContain so lookup latency and width are modeled.
func (c *Comp) remoteInflightMayContain(identity remoteLineIdentity) bool {
	filter := c.requestFilterForAddress(identity.lineAddr)
	if filter == nil {
		return false
	}
	possible, reliable := filter.Query(remoteTypedFilterKey(
		identity, writeback.FilterPending))
	return possible && reliable
}

func (c *Comp) remotePendingMayContain(
	now sim.VTimeInSec,
	reqID string,
	identity remoteLineIdentity,
) (mayContain, ready, progress bool) {
	filter := c.requestFilterForAddress(identity.lineAddr)
	if filter == nil || filter.Mode() == writeback.TypedFilterDisabled {
		c.RemoteDataPathStats.ExactTableLookups++
		return true, true, false
	}

	lookup, started := c.remoteFilterLookups[reqID]
	if !started {
		var accepted bool
		lookup, accepted = filter.StartLookup(
			now, remoteTypedFilterKey(identity, writeback.FilterPending))
		if !accepted {
			// A busy shared per-slice port is real backpressure, not a reason to
			// bypass the modeled interface. Retry on the next RDMA cycle.
			c.TickLater(now)
			return false, false, false
		}
		c.remoteFilterLookups[reqID] = lookup
		progress = true
	}

	possible, reliable, complete := filter.CompleteLookup(now, lookup)
	if !complete {
		c.TickLater(now)
		return false, false, true
	}
	delete(c.remoteFilterLookups, reqID)
	c.RemoteDataPathStats.InflightFilterQueries++
	if !reliable {
		c.RemoteDataPathStats.ExactTableLookups++
		return true, true, true
	}
	if possible {
		c.RemoteDataPathStats.InflightFilterPositives++
		c.RemoteDataPathStats.ExactTableLookups++
		return true, true, true
	}
	c.RemoteDataPathStats.InflightFilterNegatives++
	c.RemoteDataPathStats.ExactTableLookupsAvoided++
	return false, true, true
}

func (c *Comp) remotePendingInsertReady(
	now sim.VTimeInSec,
	_ string,
	identity remoteLineIdentity,
) (ready, progress bool) {
	filter := c.requestFilterForAddress(identity.lineAddr)
	if filter == nil || filter.Mode() == writeback.TypedFilterDisabled {
		return true, false
	}
	key := remoteTypedFilterKey(identity, writeback.FilterPending)
	if filter.ExactContains(key) {
		return true, false
	}
	// The exact line entry and the metadata write are launched together. The
	// shared update port still serializes visibility; a same-key lookup before
	// ReadyAt fails open to the exact line table instead of stalling the remote
	// request behind a bookkeeping write.
	if !filter.ScheduleUpdate(now, key, false) {
		c.RemoteDataPathStats.InflightFilterInsertFailures++
	}
	return true, true
}

func (c *Comp) rememberRemoteBlockedNegative(
	reqID string,
	key remoteLineKey,
) {
	c.remoteBlockedNegativeReqID = reqID
	c.remoteBlockedNegativeKey = key
}

func (c *Comp) clearRemoteBlockedNegative(reqID string) {
	if c.remoteBlockedNegativeReqID != reqID {
		return
	}
	c.remoteBlockedNegativeReqID = ""
	c.remoteBlockedNegativeKey = remoteLineKey{}
}

func (c *Comp) insertRemoteLine(
	key remoteLineKey,
	entry *remoteLineEntry,
) {
	c.remoteLines[key] = entry
	if uint64(len(c.remoteLines)) > c.RemoteDataPathStats.PeakLineEntries {
		c.RemoteDataPathStats.PeakLineEntries = uint64(len(c.remoteLines))
	}
	filter := c.requestFilterForAddress(key.lineAddr)
	if filter == nil {
		return
	}
	typedKey := remoteTypedFilterKey(
		key.remoteLineIdentity, writeback.FilterPending)
	if filter.ExactContains(typedKey) {
		return
	}
	if !filter.Insert(typedKey) {
		c.RemoteDataPathStats.InflightFilterInsertFailures++
	}
}

func (c *Comp) newRemoteDemandEntry(
	key remoteLineKey,
	dst sim.Port,
	info interface{},
) *remoteLineEntry {
	return &remoteLineEntry{
		key:   key,
		owner: dst,
		state: remoteLineProbing,
		info:  info,
	}
}

func (c *Comp) addRemoteWaiter(
	entry *remoteLineEntry,
	read *mem.ReadReq,
	arrival, firstSeen sim.VTimeInSec,
	_ uint8,
) {
	entry.waiters = append(entry.waiters, remoteWaiter{
		req:       read,
		arrival:   arrival,
		firstSeen: firstSeen,
	})
	if entry.speculative && !entry.speculativeUseful {
		entry.speculativeUseful = true
		entry.admit = true
		c.RemoteDataPathStats.PrefetchUseful++
		if c.remotePrefetcher != nil {
			c.remotePrefetcher.Reward(entry.patternToken)
		}
		// The predicted line is no longer speculative once a real request
		// owns it. In particular, an unsent takeover must fall back to an
		// ordinary 64-B demand transaction instead of being discarded by
		// the standalone-speculation guard together with its waiters.
		entry.speculative = false
	}
	if len(entry.waiters) >= 2 && !entry.admit &&
		!c.remoteConfig.DisableRequesterL2 &&
		!c.remoteUncacheable[entry.key.remoteLineIdentity] {
		entry.admit = true
		entry.multipleDemandAdmission = true
		c.RemoteDataPathStats.TwoTouchCandidates++
		c.RemoteDataPathStats.MultipleDemandAdmissions++
	}
}

func (c *Comp) consumeRemoteRead(now sim.VTimeInSec, read *mem.ReadReq) {
	memtrace.RecordMemoryPathRDMARequestFromL1(
		c.Name(), read.Info, read.ID, read.SendTime,
		c.firstSeenFromL1Req[read.ID], read.Src, read.Dst,
	)
	c.ToL1.Retrieve(now)
	delete(c.remotePrefetchObserved, read.ID)
	delete(c.remotePrefetchCandidates, read.ID)
	c.forgetSeen(c.firstSeenFromL1Req, read.ID)
	c.RemoteDataPathStats.LogicalRemoteReads++
	c.remoteOutstandingReads++
	if uint64(c.remoteOutstandingReads) >
		c.RemoteDataPathStats.PeakWaiterEntries {
		c.RemoteDataPathStats.PeakWaiterEntries =
			uint64(c.remoteOutstandingReads)
	}
}

func (c *Comp) remoteIdentity(
	req mem.AccessReq,
	dst sim.Port,
) remoteLineIdentity {
	ownerName := ""
	if dst != nil {
		ownerName = dst.Name()
	}
	return remoteLineIdentity{
		ownerName: ownerName,
		pid:       req.GetPID(),
		lineAddr:  remoteLineAddress(req.GetAddress()),
	}
}

func (c *Comp) remoteIdentities(
	req mem.AccessReq,
	dst sim.Port,
) []remoteLineIdentity {
	first := remoteLineAddress(req.GetAddress())
	last := first
	if req.GetByteSize() > 0 {
		last = remoteLineAddress(req.GetAddress() + req.GetByteSize() - 1)
	}
	count := int((last-first)/remoteLineBytes) + 1
	identities := make([]remoteLineIdentity, 0, count)
	for line := first; line <= last; line += remoteLineBytes {
		identity := c.remoteIdentity(req, dst)
		identity.lineAddr = line
		identities = append(identities, identity)
	}
	return identities
}

func (c *Comp) remoteOutstandingCapacity() int {
	capacity := c.remoteConfig.MaxBatches * c.remoteConfig.MaxBatchLines * 2
	if capacity < 1 {
		return 1
	}
	return capacity
}

func (c *Comp) remoteOwnerChildLineCapacity() int {
	capacity := c.remoteConfig.MaxBatches * c.remoteConfig.MaxBatchLines
	if capacity < 1 {
		return 1
	}
	return capacity
}

func (c *Comp) conflictingRemoteBatch(
	req mem.AccessReq,
	dst sim.Port,
) *remoteBatch {
	ownerName := ""
	if dst != nil {
		ownerName = dst.Name()
	}
	firstPage := remotePageAddress(req.GetAddress())
	lastPage := firstPage
	if req.GetByteSize() > 0 {
		lastPage = remotePageAddress(req.GetAddress() + req.GetByteSize() - 1)
	}
	for page := firstPage; page <= lastPage; page += remotePageBytes {
		batch := c.remoteBatches[remoteBatchKey{
			ownerName: ownerName,
			pid:       req.GetPID(),
			pageAddr:  page,
		}]
		if batch != nil {
			return batch
		}
	}
	return nil
}

func (c *Comp) hasUnsentRemoteRead(identity remoteLineIdentity) bool {
	for key, entry := range c.remoteLines {
		if key.remoteLineIdentity != identity {
			continue
		}
		switch entry.state {
		case remoteLineProbing, remoteLinePendingBatch, remoteLineCollecting:
			return true
		}
	}
	return false
}

func (c *Comp) noteLegacyRemoteReqSent(req mem.AccessReq, dst sim.Port) {
	if !c.remoteConfig.Enabled {
		return
	}
	if _, ok := req.(*mem.WriteReq); !ok {
		return
	}
	for _, identity := range c.remoteIdentities(req, dst) {
		c.remoteEpochs[identity]++
		c.remoteUncacheable[identity] = true
		c.deleteRemoteSeen(identity)
		if filter := c.requestFilterForAddress(identity.lineAddr); filter != nil {
			resident := writeback.TypedFilterKey{
				PID: identity.pid, Address: identity.lineAddr,
				Type: writeback.FilterResident,
			}
			if filter.ExactContains(resident) {
				filter.ScheduleUpdate(c.Engine.CurrentTime(), resident, true)
			}
		}
	}
}

func (c *Comp) isRemoteProbeRsp(rsp mem.AccessRsp) bool {
	lookup, ok := rsp.(*mem.CacheLookupRsp)
	return ok && c.remoteProbes[lookup.GetRspTo()] != nil
}

func (c *Comp) processRemoteProbeRsp(
	now sim.VTimeInSec,
	rsp mem.AccessRsp,
) bool {
	lookup := rsp.(*mem.CacheLookupRsp)
	probe := c.remoteProbes[lookup.GetRspTo()]
	if probe == nil {
		return false
	}
	entry := probe.entry
	c.recordRemoteProbeLatency(now, probe.sent)
	entry.replicaGeneration = lookup.Generation
	delete(c.remoteProbes, lookup.GetRspTo())
	c.ToL2.Retrieve(now)

	if lookup.Hit {
		if entry.prefetchCandidate != nil {
			c.RemoteDataPathStats.PrefetchStandalonePrevented++
			entry.prefetchCandidate = nil
		}
		if len(lookup.Data) != int(remoteLineBytes) {
			panic("remote L2 lookup hit returned an invalid cache line")
		}
		entry.data = append([]byte(nil), lookup.Data...)
		entry.state = remoteLineReady
		entry.fromRemote = false
		c.queueRemoteReady(entry)
		c.RemoteDataPathStats.L2ProbeHits++
		return true
	}

	entry.state = remoteLinePendingBatch
	c.remotePendingBatch = append(c.remotePendingBatch, entry)
	if entry.prefetchCandidate != nil && len(entry.waiters) > 0 {
		c.queueRemotePrefetchCandidate(
			now, entry.waiters[0].req, entry.prefetchCandidate)
		entry.prefetchCandidate = nil
	}
	c.RemoteDataPathStats.L2ProbeMisses++
	return true
}

func (c *Comp) processRemotePendingBatches(now sim.VTimeInSec) bool {
	if len(c.remotePendingBatch) == 0 {
		return false
	}
	entry := c.remotePendingBatch[0]
	if c.remoteConfig.DisableBatching {
		if !c.sendRemoteEntryDirect(now, entry) {
			return false
		}
		c.remotePendingBatch = c.remotePendingBatch[1:]
		c.recordRequesterOutstandingPeak()
		return true
	}
	added, progress := c.tryAddRemoteEntryToBatch(now, entry)
	if added {
		c.remotePendingBatch = c.remotePendingBatch[1:]
		return true
	}
	return progress
}

func (c *Comp) sendRemoteEntryDirect(
	now sim.VTimeInSec,
	entry *remoteLineEntry,
) bool {
	info := memtrace.WithL2RemoteInfo(
		entry.info, c.Name(), entry.key.ownerName)
	req := mem.ReadReqBuilder{}.
		WithSendTime(now).
		WithSrc(c.ToOutside).
		WithDst(entry.owner).
		WithAddress(entry.key.lineAddr).
		WithByteSize(remoteLineBytes).
		WithPID(entry.key.pid).
		WithInfo(info).
		Build()
	if err := c.ToOutside.Send(req); err != nil {
		return false
	}

	entry.state = remoteLineInflight
	entry.fromRemote = true
	c.remoteSingleInflight[req.ID] = entry
	c.RemoteDataPathStats.SingleReadPackets++
	c.RemoteDataPathStats.BatchSizeHistogram[1]++
	c.RemoteDataPathStats.WireLines++
	c.RemoteDataPathStats.DemandWireLines++
	c.RemoteDataPathStats.NetworkRequestBytes += uint64(req.Meta().TrafficBytes)

	line := remoteLineOffset(entry.key.lineAddr)
	direct := &remoteBatch{
		lineOrder: []uint64{line},
	}
	direct.lines[line] = entry
	c.recordRemoteBatchSent(now, direct, req.ID)
	return true
}

func (c *Comp) tryAddRemoteEntryToBatch(
	now sim.VTimeInSec,
	entry *remoteLineEntry,
) (added, progress bool) {
	key := remoteBatchKey{
		ownerName: entry.key.ownerName,
		pid:       entry.key.pid,
		pageAddr:  remotePageAddress(entry.key.lineAddr),
	}
	line := remoteLineOffset(entry.key.lineAddr)
	batch := c.remoteBatches[key]
	if entry.speculative && batch == nil {
		c.dropUnsentRemotePrefetch(entry)
		c.RemoteDataPathStats.PrefetchStandalonePrevented++
		c.RemoteDataPathStats.PrefetchNoExistingBatchDrops++
		return true, true
	}
	if batch != nil && batch.lineBitmap&(uint64(1)<<line) == 0 &&
		batch.lineCount() >= c.remoteConfig.MaxBatchLines {
		if entry.speculative {
			c.dropUnsentRemotePrefetch(entry)
			c.RemoteDataPathStats.PrefetchCapacityDrops++
			c.RemoteDataPathStats.PrefetchBatchFullDrops++
			return true, true
		}
		// Let the width-bounded egress scheduler send the full batch before
		// admitting another batch with the same key.
		return false, false
	}
	if batch == nil && len(c.remoteBatchOrder) >= c.remoteConfig.MaxBatches {
		// Egress is work-conserving and will make room this cycle whenever
		// the output port can accept a packet.
		return false, false
	}
	if batch == nil {
		oldest := now
		if len(entry.waiters) > 0 {
			oldest = entry.waiters[0].arrival
		}
		batch = &remoteBatch{
			key:       key,
			dst:       entry.owner,
			lineOrder: make([]uint64, 0, c.remoteConfig.MaxBatchLines),
			oldest:    oldest,
			createdAt: now,
			info:      entry.info,
		}
		c.remoteBatches[key] = batch
		c.remoteBatchOrder = append(c.remoteBatchOrder, key)
	}

	batch.lineBitmap |= uint64(1) << line
	batch.lineOrder = append(batch.lineOrder, line)
	batch.lines[line] = entry
	entry.batch = batch
	entry.state = remoteLineCollecting
	if entry.speculative {
		c.RemoteDataPathStats.PrefetchPiggybackLines++
	}
	return true, true
}

func (c *Comp) dropUnsentRemotePrefetch(entry *remoteLineEntry) {
	if entry == nil || !entry.speculative {
		return
	}
	if c.remoteLines[entry.key] == entry {
		delete(c.remoteLines, entry.key)
	}
	if filter := c.requestFilterForAddress(entry.key.lineAddr); filter != nil {
		filter.ScheduleUpdate(c.Engine.CurrentTime(), remoteTypedFilterKey(
			entry.key.remoteLineIdentity, writeback.FilterPending), true)
	}
}

func (c *Comp) processRemoteBatches(
	now sim.VTimeInSec,
	force bool,
) bool {
	madeProgress := false
	issued := 0
	for issued < c.effectivePipelineWidth() {
		if len(c.remoteBatchOrder) == 0 {
			break
		}
		key := c.remoteBatchOrder[0]
		batch := c.remoteBatches[key]
		if batch == nil {
			c.remoteBatchOrder = c.remoteBatchOrder[1:]
			madeProgress = true
			continue
		}
		reason := flushReasonIssue
		if force {
			reason = flushReasonDrain
		} else if batch.lineCount() >= c.remoteConfig.MaxBatchLines {
			reason = flushReasonFull
		}
		if !c.flushRemoteBatch(now, batch, reason) {
			// Keep ticking while a ready packet is held by output
			// backpressure, but do not consume additional issue width.
			return madeProgress || len(c.remoteBatchOrder) > 0
		}
		madeProgress = true
		issued++
	}
	if issued >= c.effectivePipelineWidth() && len(c.remoteBatchOrder) > 0 {
		c.RemoteDataPathStats.RequesterIssueWidthStalls++
	}
	return madeProgress || len(c.remoteBatchOrder) > 0
}

func (c *Comp) flushRemoteBatch(
	now sim.VTimeInSec,
	batch *remoteBatch,
	reason string,
) bool {
	if batch == nil || batch.lineCount() == 0 {
		return false
	}
	if !c.canAcceptRequesterOutstanding(1) {
		return false
	}

	if batch.lineCount() == 1 {
		line := batch.lineOrder[0]
		entry := batch.lines[line]
		info := memtrace.WithL2RemoteInfo(
			entry.info, c.Name(), entry.key.ownerName)
		req := mem.ReadReqBuilder{}.
			WithSendTime(now).
			WithSrc(c.ToOutside).
			WithDst(batch.dst).
			WithAddress(entry.key.lineAddr).
			WithByteSize(remoteLineBytes).
			WithPID(entry.key.pid).
			WithInfo(info).
			Build()
		if err := c.ToOutside.Send(req); err != nil {
			return false
		}
		c.recordRemoteBatchMetrics(now, batch, req.Meta().TrafficBytes)
		c.removeRemoteBatch(batch)
		entry.state = remoteLineInflight
		entry.batch = nil
		entry.fromRemote = true
		c.remoteSingleInflight[req.ID] = entry
		c.recordRequesterOutstandingPeak()
		c.RemoteDataPathStats.SingleReadPackets++
		c.recordRemoteBatchSent(now, batch, req.ID)
		c.countRemoteFlush(reason)
		return true
	}

	info := memtrace.WithL2RemoteInfo(
		batch.info, c.Name(), batch.key.ownerName)
	req := &BitmapReadReq{
		MsgMeta: sim.MsgMeta{
			ID:           sim.GetIDGenerator().Generate(),
			Src:          c.ToOutside,
			Dst:          batch.dst,
			SendTime:     now,
			TrafficBytes: bitmapReqTraffic,
		},
		PID:        batch.key.pid,
		PagePAddr:  batch.key.pageAddr,
		LineBitmap: batch.lineBitmap,
		Info:       info,
	}
	if err := c.ToOutside.Send(req); err != nil {
		return false
	}
	c.recordRemoteBatchMetrics(now, batch, req.Meta().TrafficBytes)
	c.removeRemoteBatch(batch)
	for _, line := range batch.lineOrder {
		entry := batch.lines[line]
		entry.state = remoteLineInflight
		entry.batch = nil
		entry.fromRemote = true
	}
	c.remoteBitmapInflight[req.ID] = batch
	c.recordRequesterOutstandingPeak()
	c.RemoteDataPathStats.BitmapPackets++
	c.RemoteDataPathStats.BitmapLines += uint64(batch.lineCount())
	c.recordRemoteBatchSent(now, batch, req.ID)
	c.countRemoteFlush(reason)
	return true
}

func (c *Comp) recordRemoteBatchMetrics(
	now sim.VTimeInSec,
	batch *remoteBatch,
	requestBytes int,
) {
	lineCount := batch.lineCount()
	if lineCount < 1 || lineCount >= len(c.RemoteDataPathStats.BatchSizeHistogram) {
		panic("remote batch line count is outside the bitmap range")
	}
	c.RemoteDataPathStats.BatchSizeHistogram[lineCount]++
	c.RemoteDataPathStats.BatchQueueWaitSamples++
	c.RemoteDataPathStats.WireLines += uint64(lineCount)
	demandLines := 0
	for _, line := range batch.lineOrder {
		if entry := batch.lines[line]; entry != nil && !entry.speculative {
			demandLines++
		}
	}
	c.RemoteDataPathStats.DemandWireLines += uint64(demandLines)
	c.RemoteDataPathStats.PrefetchWireLines += uint64(lineCount - demandLines)
	c.RemoteDataPathStats.NetworkRequestBytes += uint64(requestBytes)

	waitNS := remoteDurationNS(now - batch.createdAt)
	c.RemoteDataPathStats.BatchQueueWaitTotalNS += waitNS
	if waitNS > c.RemoteDataPathStats.BatchQueueWaitMaxNS {
		c.RemoteDataPathStats.BatchQueueWaitMaxNS = waitNS
	}
}

func (c *Comp) recordRemoteBatchSent(
	now sim.VTimeInSec,
	batch *remoteBatch,
	networkID string,
) {
	registered := false
	for _, line := range batch.lineOrder {
		entry := batch.lines[line]
		for _, waiter := range entry.waiters {
			waitNS := remoteDurationNS(now - waiter.arrival)
			c.RemoteDataPathStats.PreNetworkWaitSamples++
			c.RemoteDataPathStats.PreNetworkWaitTotalNS += waitNS
			if waitNS > c.RemoteDataPathStats.PreNetworkWaitMaxNS {
				c.RemoteDataPathStats.PreNetworkWaitMaxNS = waitNS
			}
			if !registered {
				memtrace.RegisterMemoryPathNetworkMessage(
					waiter.req.Info, waiter.req.ID, networkID, "request")
				registered = true
			}
			memtrace.RecordMemoryPathRDMALocalRequestOutputWait(
				c.Name(), waiter.req.Info, waiter.req.ID,
				waiter.firstSeen, now,
			)
		}
	}
}

func (c *Comp) recordRemoteProbeLatency(
	now, sent sim.VTimeInSec,
) {
	latencyNS := remoteDurationNS(now - sent)
	c.RemoteDataPathStats.ProbeLatencySamples++
	c.RemoteDataPathStats.ProbeLatencyTotalNS += latencyNS
	if latencyNS > c.RemoteDataPathStats.ProbeLatencyMaxNS {
		c.RemoteDataPathStats.ProbeLatencyMaxNS = latencyNS
	}
}

func remoteDurationNS(duration sim.VTimeInSec) float64 {
	if duration <= 0 {
		return 0
	}
	return float64(duration) * 1e9
}

func (c *Comp) countRemoteFlush(reason string) {
	switch reason {
	case flushReasonFull:
		c.RemoteDataPathStats.FullFlushes++
	case flushReasonIssue:
		c.RemoteDataPathStats.WorkConservingFlushes++
	case flushReasonCapacity:
		c.RemoteDataPathStats.CapacityFlushes++
	case flushReasonConflict:
		c.RemoteDataPathStats.ConflictFlushes++
	case flushReasonDrain:
		c.RemoteDataPathStats.DrainFlushes++
	}
}

func (c *Comp) removeRemoteBatch(batch *remoteBatch) {
	delete(c.remoteBatches, batch.key)
	for i, key := range c.remoteBatchOrder {
		if key != batch.key {
			continue
		}
		c.remoteBatchOrder = append(
			c.remoteBatchOrder[:i], c.remoteBatchOrder[i+1:]...)
		return
	}
}

func (c *Comp) isRemoteSingleRsp(rsp mem.AccessRsp) bool {
	return c.remoteSingleInflight[rsp.GetRspTo()] != nil
}

func (c *Comp) processRemoteSingleRsp(
	now sim.VTimeInSec,
	rsp mem.AccessRsp,
) bool {
	entry := c.remoteSingleInflight[rsp.GetRspTo()]
	dataRsp, ok := rsp.(*mem.DataReadyRsp)
	if !ok || entry == nil {
		panic("remote single-line read returned an invalid response")
	}
	if len(dataRsp.Data) != int(remoteLineBytes) {
		panic("remote single-line read returned an invalid cache line")
	}
	entry.data = append([]byte(nil), dataRsp.Data...)
	entry.state = remoteLineReady
	entry.fromRemote = true
	delete(c.remoteSingleInflight, rsp.GetRspTo())
	c.RemoteDataPathStats.NetworkResponseBytes += uint64(rsp.Meta().TrafficBytes)
	c.ToOutside.Retrieve(now)
	c.queueRemoteReady(entry)
	return true
}

func (c *Comp) processBitmapRspFromOutside(
	now sim.VTimeInSec,
	rsp *BitmapReadRsp,
) bool {
	batch := c.remoteBitmapInflight[rsp.GetRspTo()]
	if batch == nil {
		panic("bitmap response has no matching requester batch")
	}
	for line, data := range rsp.LineData {
		if line >= 64 {
			panic(fmt.Sprintf("bitmap response line %d is outside the page", line))
		}
		lineBit := uint64(1) << line
		entry := batch.lines[line]
		if batch.lineBitmap&lineBit == 0 || entry == nil {
			panic(fmt.Sprintf("bitmap response line %d was not requested", line))
		}
		if len(data) != int(remoteLineBytes) {
			panic(fmt.Sprintf("bitmap response line %d is invalid", line))
		}
		if batch.responseBitmap&lineBit != 0 {
			panic(fmt.Sprintf("bitmap response line %d was returned twice", line))
		}
		entry.data = append([]byte(nil), data...)
		entry.state = remoteLineReady
		entry.fromRemote = true
		c.queueRemoteReady(entry)
		batch.responseBitmap |= lineBit
	}
	if len(rsp.LineData) == 0 {
		panic("bitmap response contains no cache lines")
	}
	c.RemoteDataPathStats.NetworkResponseBytes += uint64(rsp.Meta().TrafficBytes)
	if batch.responseBitmap == batch.lineBitmap {
		delete(c.remoteBitmapInflight, rsp.GetRspTo())
	}
	c.ToOutside.Retrieve(now)
	return true
}

func (c *Comp) queueRemoteReady(entry *remoteLineEntry) {
	if entry.queuedReady {
		return
	}
	entry.queuedReady = true
	c.remoteReady = append(c.remoteReady, entry)
}

func (c *Comp) processRemoteReady(now sim.VTimeInSec) bool {
	if len(c.remoteReady) == 0 {
		return false
	}
	entry := c.remoteReady[0]
	if len(entry.waiters) > 0 {
		waiter := entry.waiters[0]
		data := append([]byte(nil), entry.data...)
		rsp := mem.DataReadyRspBuilder{}.
			WithSendTime(now).
			WithSrc(c.ToL1).
			WithDst(waiter.req.Src).
			WithRspTo(waiter.req.ID).
			WithData(data).
			Build()
		if err := c.ToL1.Send(rsp); err != nil {
			return false
		}
		latencyNS := remoteDurationNS(now - waiter.req.SendTime)
		c.RemoteDataPathStats.LogicalReadLatencyTotalNS += latencyNS
		if latencyNS > c.RemoteDataPathStats.LogicalReadLatencyMaxNS {
			c.RemoteDataPathStats.LogicalReadLatencyMaxNS = latencyNS
		}
		entry.waiters = entry.waiters[1:]
		c.remoteOutstandingReads--
		c.RemoteDataPathStats.FanoutResponses++
		if entry.fromRemote {
			memtrace.RecordRemoteGPMAccess(
				c.Name(), entry.key.ownerName, uint64(len(data)),
				now-waiter.req.SendTime, now, "read")
		} else {
			c.RemoteDataPathStats.L2LogicalResponses++
		}
		return true
	}

	shouldFill := entry.fromRemote &&
		!c.remoteConfig.DisableRequesterL2 &&
		entry.admit &&
		!c.remoteUncacheable[entry.key.remoteLineIdentity]
	if shouldFill && c.remoteCacheModules != nil {
		if entry.fillComplete {
			c.removeRemoteLine(entry)
			c.remoteReady = c.remoteReady[1:]
			entry.queuedReady = false
			return true
		}
		if entry.fillInflight {
			c.remoteReady = c.remoteReady[1:]
			entry.queuedReady = false
			return true
		}
		if dst := c.remoteCacheModules.Find(entry.key.lineAddr); dst != nil {
			c.RemoteDataPathStats.TwoTouchFillAttempts++
			fillBuilder := mem.RemoteDataFillBuilder{}.
				WithSendTime(now).
				WithSrc(c.ToL2).
				WithDst(dst).
				WithPID(entry.key.pid).
				WithAddress(entry.key.lineAddr).
				WithData(append([]byte(nil), entry.data...)).
				WithInfo(entry.info).
				WithGeneration(entry.replicaGeneration)
			if entry.speculative {
				fillBuilder = fillBuilder.WithPattern(
					entry.patternKey.Owner, entry.patternKey.Address)
				if !entry.speculativeUseful && !entry.seenAdmission {
					fillBuilder = fillBuilder.WithRequireInvalidVictim()
				}
			}
			fill := fillBuilder.Build()
			if err := c.ToL2.Send(fill); err != nil {
				c.RemoteDataPathStats.TwoTouchFillAttempts--
				return false
			}
			entry.fillInflight = true
			c.remoteFillInflight[fill.ID] = entry
		}
	}

	c.remoteReady = c.remoteReady[1:]
	entry.queuedReady = false
	if !entry.fillInflight {
		c.removeRemoteLine(entry)
	}
	return true
}

func (c *Comp) isRemoteFillRsp(rsp mem.AccessRsp) bool {
	fillRsp, ok := rsp.(*mem.RemoteDataFillRsp)
	return ok && c.remoteFillInflight[fillRsp.GetRspTo()] != nil
}

func (c *Comp) processRemoteFillRsp(
	now sim.VTimeInSec,
	rsp mem.AccessRsp,
) bool {
	fillRsp := rsp.(*mem.RemoteDataFillRsp)
	entry := c.remoteFillInflight[fillRsp.GetRspTo()]
	if entry == nil {
		return false
	}
	delete(c.remoteFillInflight, fillRsp.GetRspTo())
	entry.fillInflight = false
	entry.fillComplete = true
	entry.fillInstalled = fillRsp.Installed
	if fillRsp.Installed {
		c.RemoteDataPathStats.TwoTouchInstalledFills++
	}
	c.ToL2.Retrieve(now)
	if len(entry.waiters) > 0 {
		c.queueRemoteReady(entry)
	} else {
		c.removeRemoteLine(entry)
	}
	return true
}

func (c *Comp) removeRemoteLine(entry *remoteLineEntry) {
	if c.remoteLines[entry.key] == entry {
		delete(c.remoteLines, entry.key)
		identity := entry.key.remoteLineIdentity
		if filter := c.requestFilterForAddress(identity.lineAddr); filter != nil {
			filter.ScheduleUpdate(c.Engine.CurrentTime(), remoteTypedFilterKey(
				identity, writeback.FilterPending), true)
		}
		if entry.speculative && !entry.speculativeUseful && !entry.fillInstalled {
			c.RemoteDataPathStats.PrefetchUnused++
			if c.remotePrefetcher != nil {
				c.remotePrefetcher.Penalize(entry.patternToken)
				if patternFilter := c.remotePatternFilters[entry.patternKey]; patternFilter != nil {
					patternFilter.ScheduleUpdate(
						c.Engine.CurrentTime(), entry.patternKey, true)
				}
				delete(c.remotePatternFilters, entry.patternKey)
			}
		} else if c.remoteUncacheable[identity] {
			c.RemoteDataPathStats.ReuseWriteUncacheableSkips++
			c.deleteRemoteSeen(identity)
		} else if !entry.fromRemote || entry.fillInstalled {
			// An exact requester-L2 hit or successful clean-fill supersedes SEEN.
			c.deleteRemoteSeen(identity)
		} else {
			if !entry.admit {
				c.RemoteDataPathStats.FirstTouchRemoteLines++
			}
			// A first transaction, or a clean-fill that could not find a safe
			// victim, remains reusable evidence for the next real demand.
			c.markRemoteSeen(identity)
		}
	}
}

func (c *Comp) resetRemoteDataPathHistory() {
	if c.remoteOutstandingReads != 0 {
		panic("RDMA drained with remote read waiters outstanding")
	}
	clear(c.remoteEpochs)
	c.remoteBlockedNegativeReqID = ""
	c.remoteBlockedNegativeKey = remoteLineKey{}
	for _, filter := range c.requestFilters {
		if filter != nil {
			filter.ClearType(writeback.FilterPending)
			filter.ClearType(writeback.FilterSeen)
			filter.ClearType(writeback.FilterPattern)
		}
	}
	if c.remotePrefetcher != nil {
		c.remotePrefetcher.Reset()
	}
	clear(c.remotePrefetchObserved)
	clear(c.remotePrefetchCandidates)
	clear(c.remotePatternFilters)
	clear(c.remoteFilterLookups)
	clear(c.remoteHintResults)
}

func (c *Comp) remoteDataPathHasPendingWork() bool {
	return len(c.remoteBatchOrder) > 0 ||
		len(c.remoteLines) > 0 ||
		len(c.remoteProbes) > 0 ||
		len(c.remotePendingBatch) > 0 ||
		len(c.remoteSingleInflight) > 0 ||
		len(c.remoteBitmapInflight) > 0 ||
		len(c.remoteFillInflight) > 0 ||
		len(c.remoteReady) > 0 ||
		len(c.remoteOwnerPendingReq) > 0 ||
		len(c.remoteOwnerSubReqs) > 0 ||
		len(c.remoteOwnerPendingRsp) > 0 ||
		len(c.remoteOwnerBatches) > 0
}
