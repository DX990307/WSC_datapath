package writeback

import (
	"github.com/sarchlab/akita/v3/mem/cache"
	"github.com/sarchlab/akita/v3/mem/mem"
	memtrace "github.com/sarchlab/akita/v3/mem/trace"
	"github.com/sarchlab/akita/v3/sim"
	"github.com/sarchlab/akita/v3/tracing"
)

type writeBufferStage struct {
	cache *Cache

	writeBufferCapacity int
	maxInflightFetch    int
	maxInflightEviction int

	pendingEvictions []*transaction
	inflightFetch    []*transaction
	inflightEviction []*transaction
}

func (wb *writeBufferStage) Tick(now sim.VTimeInSec) bool {
	madeProgress := false

	madeProgress = wb.write(now) || madeProgress
	madeProgress = wb.processReturnRsp(now) || madeProgress
	newTransactionProgress := wb.processNewTransaction(now)
	madeProgress = newTransactionProgress || madeProgress

	return madeProgress
}

func (wb *writeBufferStage) processNewTransaction(now sim.VTimeInSec) bool {
	item := wb.cache.writeBufferBuffer.Peek()
	if item == nil {
		return false
	}

	trans := item.(*transaction)
	if wb.cache.adaptivePairAdapter != nil &&
		(trans.action != writeBufferFetch || trans.read == nil ||
			trans.fetchReadReq != nil) {
		wb.cache.adaptivePairAdapter.invalidateTransaction(wb.cache, trans)
	}
	// A second real demand can be issued as the peer of an earlier queue-head
	// demand without being removed from the FIFO out of order.  When its stale
	// queue entry reaches the head, the physical read is already in flight (or
	// has already returned), so only the queue entry is retired.  No demand was
	// held while waiting for this peer.
	if trans.pairedDemandQueueEntry && trans.fetchReadReq != nil {
		trans.pairedDemandQueueEntry = false
		trans.writeBufferReady = false
		wb.cache.writeBufferBuffer.Pop()
		return true
	}
	switch trans.action {
	case writeBufferFetch:
		return wb.processWriteBufferFetch(now, trans)
	case writeBufferEvictAndWrite:
		return wb.processWriteBufferEvictAndWrite(now, trans)
	case writeBufferEvictAndFetch:
		return wb.processWriteBufferFetchAndEvict(now, trans)
	case writeBufferFlush:
		return wb.processWriteBufferFlush(now, trans, true)
	default:
		panic("unknown transaction action")
	}
}

func (wb *writeBufferStage) processWriteBufferFetch(
	now sim.VTimeInSec,
	trans *transaction,
) bool {
	if wb.findDataLocally(trans) {
		return wb.sendFetchedDataToBank(now, trans)
	}

	return wb.fetchFromBottom(now, trans)
}

func (wb *writeBufferStage) findDataLocally(trans *transaction) bool {
	for _, e := range wb.inflightEviction {
		if e.evictingAddr == trans.fetchAddress {
			trans.fetchedData = e.evictingData
			return true
		}
	}

	for _, e := range wb.pendingEvictions {
		if e.evictingAddr == trans.fetchAddress {
			trans.fetchedData = e.evictingData
			return true
		}
	}
	return false
}

func (wb *writeBufferStage) sendFetchedDataToBank(
	now sim.VTimeInSec,
	trans *transaction,
) bool {
	bankNum := bankID(trans.block,
		wb.cache.directory.WayAssociativity(),
		len(wb.cache.dirToBankBuffers))
	bankBuf := wb.cache.writeBufferToBankBuffers[bankNum]

	if !bankBuf.CanPush() {
		trans.fetchedData = nil
		return false
	}

	trans.mshrEntry.Data = trans.fetchedData
	trans.action = bankWriteFetched
	wb.combineData(trans.mshrEntry)

	wb.cache.mshr.Remove(trans.mshrEntry.PID, trans.mshrEntry.Address)
	wb.cache.untrackGranularityPending(
		now, trans.mshrEntry.PID, trans.mshrEntry.Address)

	bankBuf.Push(trans)

	wb.cache.writeBufferBuffer.Pop()

	// log.Printf("%.10f, %s, wb data fetched locally， %s, %04X, %04X, (%d, %d), %v\n",
	// 	now, wb.cache.Name(),
	// 	trans.accessReq().Meta().ID,
	// 	trans.accessReq().GetAddress(), trans.block.Tag,
	// 	trans.block.SetID, trans.block.WayID,
	// 	trans.fetchedData,
	// )

	return true
}

func (wb *writeBufferStage) fetchFromBottom(
	now sim.VTimeInSec,
	trans *transaction,
) bool {
	if wb.cache.adaptivePairEnabled && trans.read != nil &&
		!trans.prefetch && trans.fetchReadReq == nil {
		return wb.processAdaptivePairFetch(now, trans)
	}
	if wb.cache.granularityAdaptationEnabled && trans.read != nil &&
		!trans.prefetch && trans.granularityCandidate != nil &&
		wb.issueGranularityFetch(now, trans) {
		wb.cache.writeBufferBuffer.Pop()
		return true
	}
	if !wb.issueSingleFetch(now, trans) {
		return false
	}
	wb.cache.writeBufferBuffer.Pop()
	return true
}

func (wb *writeBufferStage) issueSingleFetch(
	now sim.VTimeInSec,
	trans *transaction,
) bool {
	if wb.tooManyInflightFetches() {
		return false
	}

	if !wb.cache.bottomSender.CanSend(1) {
		return false
	}

	lowModulePort := wb.cache.lowModuleFinder.Find(trans.fetchAddress)
	read := mem.ReadReqBuilder{}.
		WithSrc(wb.cache.bottomPort).
		WithDst(lowModulePort).
		WithPID(trans.fetchPID).
		WithAddress(trans.fetchAddress).
		WithByteSize(1 << wb.cache.log2BlockSize).
		WithStreamID(accessReqStreamID(trans.accessReq())).
		WithLocalStreamID(accessReqLocalStreamID(trans.accessReq())).
		WithLocalPairHint(accessReqLocalPairHint(trans.accessReq())).
		WithInfo(accessReqInfo(trans.accessReq())).
		Build()
	wb.cache.bottomSender.Send(read)
	memtrace.LinkObservationRequestFromRequest(
		trans.accessReq().Meta().ID, read.Meta().ID, "l2_dram_read")
	memtrace.ObservationTransitionByRequest(
		trans.accessReq().Meta().ID,
		"l2_dram_request_issued", "l2_to_dram", now)
	wb.recordDRAMReadSend(now, read, trans)

	trans.fetchReadReq = read
	wb.inflightFetch = append(wb.inflightFetch, trans)
	wb.cache.localMemoryPathStats.DRAMReadRequests++
	if wb.cache.granularityAdaptationEnabled {
		wb.cache.granularityStats.FrontendSingle64Descriptors++
		wb.cache.granularityStats.FrontendReadBytes +=
			uint64(1) << wb.cache.log2BlockSize
	}
	if trans.prefetch {
		wb.cache.markLocalPrefetchDRAMIssued(
			trans.fetchPID, trans.fetchAddress)
	}

	tracing.TraceReqInitiate(read, wb.cache,
		tracing.MsgIDAtReceiver(trans.req(), wb.cache))

	return true
}

func (wb *writeBufferStage) issueGranularityFetch(
	now sim.VTimeInSec,
	trans *transaction,
) bool {
	candidate := trans.granularityCandidate
	trans.granularityCandidate = nil
	if candidate == nil {
		return false
	}
	stats := &wb.cache.granularityStats
	stats.ExpansionAttempts++
	if !wb.cache.completeGranularityFilterLookups(now, candidate) {
		return false
	}

	lineBytes := uint64(1) << wb.cache.log2BlockSize
	base := trans.fetchAddress & ^(2*lineBytes - 1)
	sibling := base
	if sibling == trans.fetchAddress {
		sibling += lineBytes
	}
	if candidate.line != sibling || candidate.address != sibling {
		stats.CandidateNotSiblingDrops++
		return false
	}
	if trans.fetchAddress/granularityPageBytes != sibling/granularityPageBytes {
		stats.PageBoundaryDrops++
		return false
	}
	if !wb.cache.ownsAddress(sibling) {
		stats.WrongSliceDrops++
		return false
	}
	if wb.cache.lowModuleFinder.Find(base) !=
		wb.cache.lowModuleFinder.Find(sibling) {
		stats.WrongControllerDrops++
		return false
	}

	checkResident, checkPending := granularityExactLookupPlan(candidate)
	if checkPending {
		stats.PendingExactLookups++
		entry := wb.cache.mshr.Query(candidate.pid, sibling)
		if entry != nil {
			stats.PendingExactSuppressions++
			lowerPort := wb.cache.lowModuleFinder.Find(base)
			if wb.issueReadyDemandPair(
				now, trans, entry, lowerPort, lineBytes) {
				return true
			}
			return false
		}
		if candidate.gated {
			stats.PendingFalsePositives++
		}
	} else {
		stats.PendingNegativeLookupSkips++
	}
	// A Filter-only real-demand probe is never allowed to manufacture a
	// sibling request. Its only successful outcome is the exact ready-demand
	// pair handled above; otherwise the mandatory demand falls back now.
	if candidate.pairProbeOnly {
		return false
	}
	if checkResident {
		stats.ResidentExactLookups++
		block := wb.cache.directory.Lookup(candidate.pid, sibling)
		if block != nil {
			stats.ResidentExactSuppressions++
			return false
		}
		if candidate.gated {
			stats.ResidentFalsePositives++
		}
	} else {
		stats.ResidentNegativeLookupSkips++
	}

	if wb.cache.mshr.IsFull() {
		stats.MSHRPressureDrops++
		return false
	}
	if len(wb.inflightFetch)+2 > wb.maxInflightFetch {
		stats.InflightCapacityDrops++
		return false
	}
	if !wb.cache.bottomSender.CanSend(1) {
		return false
	}
	lowerPort := wb.cache.lowModuleFinder.Find(base)
	if lowerPort != nil {
		if admission, ok := lowerPort.Component().(interface {
			CanAcceptPhysicalAccesses(int) bool
		}); ok && !admission.CanAcceptPhysicalAccesses(2) {
			stats.DRAMQueuePressureDrops++
			return false
		}
	}
	victim := wb.cache.directory.FindVictim(sibling)
	admissible, protectsRemote := wb.cache.granularityVictimAdmissible(victim)
	if protectsRemote {
		stats.RemoteVictimProtectionDrops++
		return false
	}
	if !admissible {
		stats.VictimUnavailableDrops++
		return false
	}
	if victim.IsValid {
		stats.CleanVictimDisplacements++
	}
	pendingKey := TypedFilterKey{
		PID: candidate.pid, Address: sibling,
		Type: FilterGranularityPending,
	}
	if candidate.gated && !wb.cache.requestFilter.TryScheduleUpdate(
		now, pendingKey, false) {
		stats.PendingInsertDrops++
		return false
	}
	if candidate.hasToken && wb.cache.granularityPredictor != nil &&
		!wb.cache.granularityPredictor.MarkIssued(candidate.token) {
		stats.PredictorThrottledDrops++
		if candidate.gated {
			wb.cache.requestFilter.ScheduleUpdate(now, pendingKey, true)
		}
		return false
	}

	siblingTrans := wb.allocateGranularitySibling(
		now, trans, candidate, victim)
	demandRead, siblingRead := wb.buildGranularityPairedReads(
		trans, sibling, lowerPort, lineBytes)
	descriptor := mem.PairedReadReqBuilder{}.
		WithSrc(wb.cache.bottomPort).
		WithDst(lowerPort).
		WithReads(demandRead, siblingRead).
		Build()
	// The descriptor makes both children visible to the controller together.
	// Each child still has its own ID, address, transaction, and response.
	wb.cache.bottomSender.Send(descriptor)
	memtrace.LinkObservationRequestFromRequest(
		trans.accessReq().Meta().ID, demandRead.Meta().ID, "l2_dram_read")
	memtrace.ObservationTransitionByRequest(
		trans.accessReq().Meta().ID,
		"l2_dram_request_issued", "l2_to_dram", now)
	wb.recordDRAMReadSend(now, demandRead, trans)

	trans.fetchReadReq = demandRead
	siblingTrans.fetchReadReq = siblingRead
	wb.inflightFetch = append(wb.inflightFetch, trans, siblingTrans)
	wb.cache.localMemoryPathStats.DRAMReadRequests += 2
	stats.AcceptedExpansions++
	stats.FrontendPairedReadAggregates++
	stats.FrontendReadBytes += 2 * lineBytes

	tracing.TraceReqInitiate(demandRead, wb.cache,
		tracing.MsgIDAtReceiver(trans.req(), wb.cache))
	tracing.TraceReqInitiate(siblingRead, wb.cache,
		tracing.MsgIDAtReceiver(trans.req(), wb.cache))
	return true
}

// issueReadyDemandPair opportunistically groups two real adjacent misses that
// already reached this slice's write buffer.  It never creates a speculative
// cacheline and never waits for a peer: an unready peer or unavailable
// resource returns false, after which the caller immediately issues the
// current demand through the ordinary singleton path.
func (wb *writeBufferStage) issueReadyDemandPair(
	now sim.VTimeInSec,
	trans *transaction,
	peerEntry *cache.MSHREntry,
	lowerPort sim.Port,
	lineBytes uint64,
) bool {
	peer := readyUnissuedDemand(peerEntry)
	if peer == nil || peer == trans || peer.fetchPID != trans.fetchPID ||
		peer.fetchAddress == trans.fetchAddress {
		return false
	}
	// A pending eviction can supply the peer locally.  Preserve that path
	// instead of manufacturing a redundant DRAM read.
	if wb.findDataLocally(peer) {
		return false
	}

	stats := &wb.cache.granularityStats
	stats.DemandPairReadyOpportunities++
	if len(wb.inflightFetch)+2 > wb.maxInflightFetch {
		stats.InflightCapacityDrops++
		stats.DemandPairResourceDrops++
		return false
	}
	if !wb.cache.bottomSender.CanSend(1) {
		stats.DemandPairResourceDrops++
		return false
	}
	if lowerPort != nil {
		if admission, ok := lowerPort.Component().(interface {
			CanAcceptPhysicalAccesses(int) bool
		}); ok && !admission.CanAcceptPhysicalAccesses(2) {
			stats.DRAMQueuePressureDrops++
			stats.DemandPairResourceDrops++
			return false
		}
	}

	demandRead, peerRead := wb.buildDemandPairedReads(
		trans, peer, lowerPort, lineBytes)
	descriptor := mem.PairedReadReqBuilder{}.
		WithSrc(wb.cache.bottomPort).
		WithDst(lowerPort).
		WithReads(demandRead, peerRead).
		Build()
	wb.cache.bottomSender.Send(descriptor)

	for _, member := range []struct {
		trans *transaction
		read  *mem.ReadReq
	}{
		{trans: trans, read: demandRead},
		{trans: peer, read: peerRead},
	} {
		memtrace.LinkObservationRequestFromRequest(
			member.trans.accessReq().Meta().ID,
			member.read.Meta().ID, "l2_dram_read")
		memtrace.ObservationTransitionByRequest(
			member.trans.accessReq().Meta().ID,
			"l2_dram_request_issued", "l2_to_dram", now)
		wb.recordDRAMReadSend(now, member.read, member.trans)
		tracing.TraceReqInitiate(member.read, wb.cache,
			tracing.MsgIDAtReceiver(member.trans.req(), wb.cache))
	}

	trans.fetchReadReq = demandRead
	trans.writeBufferReady = false
	peer.fetchReadReq = peerRead
	peer.granularityCandidate = nil
	peer.pairedDemandQueueEntry = true
	wb.inflightFetch = append(wb.inflightFetch, trans, peer)
	wb.cache.localMemoryPathStats.DRAMReadRequests += 2
	stats.DemandPairAggregates++
	stats.FrontendPairedReadAggregates++
	stats.FrontendReadBytes += 2 * lineBytes
	return true
}

func readyUnissuedDemand(entry *cache.MSHREntry) *transaction {
	if entry == nil || entry.Data != nil {
		return nil
	}
	for _, request := range entry.Requests {
		trans, ok := request.(*transaction)
		if !ok || trans == nil || trans.read == nil || trans.prefetch ||
			trans.granularitySibling || !trans.writeBufferReady ||
			trans.fetchReadReq != nil || trans.action != writeBufferFetch {
			continue
		}
		return trans
	}
	return nil
}

func (wb *writeBufferStage) buildDemandPairedReads(
	demand *transaction,
	peer *transaction,
	lowerPort sim.Port,
	lineBytes uint64,
) (demandRead, peerRead *mem.ReadReq) {
	pairID := sim.GetIDGenerator().Generate()
	build := func(trans *transaction, part mem.PairedReadPart) *mem.ReadReq {
		return mem.ReadReqBuilder{}.
			WithSrc(wb.cache.bottomPort).
			WithDst(lowerPort).
			WithPID(trans.fetchPID).
			WithAddress(trans.fetchAddress).
			WithByteSize(lineBytes).
			WithStreamID(accessReqStreamID(trans.accessReq())).
			WithLocalStreamID(accessReqLocalStreamID(trans.accessReq())).
			WithLocalPairHint(accessReqLocalPairHint(trans.accessReq())).
			WithInfo(accessReqInfo(trans.accessReq())).
			WithPairedRead(pairID, part).
			Build()
	}
	return build(demand, mem.PairedReadDemand),
		build(peer, mem.PairedReadSibling)
}

func (wb *writeBufferStage) buildGranularityPairedReads(
	trans *transaction,
	sibling uint64,
	lowerPort sim.Port,
	lineBytes uint64,
) (demandRead, siblingRead *mem.ReadReq) {
	pairID := sim.GetIDGenerator().Generate()
	demandRead = mem.ReadReqBuilder{}.
		WithSrc(wb.cache.bottomPort).
		WithDst(lowerPort).
		WithPID(trans.fetchPID).
		WithAddress(trans.fetchAddress).
		WithByteSize(lineBytes).
		WithStreamID(accessReqStreamID(trans.accessReq())).
		WithLocalStreamID(accessReqLocalStreamID(trans.accessReq())).
		WithLocalPairHint(accessReqLocalPairHint(trans.accessReq())).
		WithInfo(accessReqInfo(trans.accessReq())).
		WithPairedRead(pairID, mem.PairedReadDemand).
		Build()
	siblingRead = mem.ReadReqBuilder{}.
		WithSrc(wb.cache.bottomPort).
		WithDst(lowerPort).
		WithPID(trans.fetchPID).
		WithAddress(sibling).
		WithByteSize(lineBytes).
		WithStreamID(accessReqStreamID(trans.accessReq())).
		WithLocalStreamID(accessReqLocalStreamID(trans.accessReq())).
		WithInfo("granularity-sibling").
		WithPairedRead(pairID, mem.PairedReadSibling).
		Build()
	return demandRead, siblingRead
}

func (wb *writeBufferStage) allocateGranularitySibling(
	now sim.VTimeInSec,
	parent *transaction,
	candidate *granularityCandidate,
	victim *cache.Block,
) *transaction {
	line := candidate.line
	pid := candidate.pid
	read := mem.ReadReqBuilder{}.
		WithSendTime(now).
		WithSrc(wb.cache.topPort).
		WithDst(wb.cache.topPort).
		WithPID(pid).
		WithAddress(line).
		WithByteSize(1 << wb.cache.log2BlockSize).
		WithStreamID(accessReqStreamID(parent.accessReq())).
		WithLocalStreamID(accessReqLocalStreamID(parent.accessReq())).
		WithInfo("granularity-sibling").
		Build()
	trans := &transaction{
		id: sim.GetIDGenerator().Generate(), read: read, prefetch: true,
		granularitySibling: true, l2Arrival: parent.l2Arrival,
		fetchPID: pid, fetchAddress: line, block: victim,
		action: writeBufferFetch,
	}

	wb.cache.untrackRemoteReplica(victim)
	wb.cache.untrackResidentBlock(victim)
	victim.IsLocked = true
	victim.Tag = line
	victim.PID = pid
	victim.IsValid = true
	wb.cache.directory.Visit(victim)
	entry := wb.cache.mshr.Add(pid, line)
	entry.Block = victim
	entry.Requests = append(entry.Requests, trans)
	trans.mshrEntry = entry
	wb.cache.inFlightTransactions = append(
		wb.cache.inFlightTransactions, trans)
	record := &granularityRecord{
		key:   granularityLineKey{pid: pid, line: line},
		token: candidate.token, hasToken: candidate.hasToken,
	}
	wb.cache.granularityByLine[record.key] = record
	return trans
}

func (wb *writeBufferStage) recordDRAMReadSend(
	now sim.VTimeInSec,
	read *mem.ReadReq,
	trans *transaction,
) {
	if !trans.dramIssueRecorded {
		latencyNS := float64(now-trans.l2Arrival) * 1e9
		stats := &wb.cache.localMemoryPathStats
		stats.MissToDRAMIssueSamples++
		stats.MissToDRAMIssueTotalNS += latencyNS
		if latencyNS > stats.MissToDRAMIssueMaxNS {
			stats.MissToDRAMIssueMaxNS = latencyNS
		}
		if trans.residentFastMiss {
			stats.FastMissIssueSamples++
			stats.FastMissIssueTotalNS += latencyNS
			if latencyNS > stats.FastMissIssueMaxNS {
				stats.FastMissIssueMaxNS = latencyNS
			}
		}
		trans.dramIssueRecorded = true
	}
	memtrace.RecordMemoryPathL2WriteBufferSend(
		wb.cache.Name(),
		accessReqInfo(trans.accessReq()),
		read.Meta().ID,
		now,
	)
}

func (wb *writeBufferStage) processWriteBufferEvictAndWrite(
	now sim.VTimeInSec,
	trans *transaction,
) bool {
	if wb.writeBufferFull() {
		return false
	}

	bankNum := bankID(
		trans.block,
		wb.cache.directory.WayAssociativity(),
		len(wb.cache.dirToBankBuffers),
	)
	bankBuf := wb.cache.writeBufferToBankBuffers[bankNum]

	if !bankBuf.CanPush() {
		return false
	}

	trans.action = bankWriteHit
	bankBuf.Push(trans)

	wb.pendingEvictions = append(wb.pendingEvictions, trans)
	wb.cache.writeBufferBuffer.Pop()

	// log.Printf("%.10f, %s, wb evict and write， %s, %04X, %04X, (%d, %d), %v\n",
	// 	now, wb.cache.Name(),
	// 	trans.accessReq().Meta().ID,
	// 	trans.accessReq().GetAddress(), trans.block.Tag,
	// 	trans.block.SetID, trans.block.WayID,
	// 	trans.evictingData,
	// )

	return true
}

func (wb *writeBufferStage) processWriteBufferFetchAndEvict(
	now sim.VTimeInSec,
	trans *transaction,
) bool {
	ok := wb.processWriteBufferFlush(now, trans, false)
	if ok {
		trans.action = writeBufferFetch
		return true
	}

	// log.Printf("%.10f, %s, wb fetch and evict， %s, %04X, %04X, (%d, %d), %v\n",
	// 	now, wb.cache.Name(),
	// 	trans.write.ID,
	// 	trans.write.Address, trans.block.Tag,
	// 	trans.block.SetID, trans.block.WayID,
	// 	trans.evictingData,
	// )

	return false
}

func (wb *writeBufferStage) processWriteBufferFlush(
	now sim.VTimeInSec,
	trans *transaction,
	popAfterDone bool,
) bool {
	if wb.writeBufferFull() {
		return false
	}

	wb.pendingEvictions = append(wb.pendingEvictions, trans)

	if popAfterDone {
		wb.cache.writeBufferBuffer.Pop()
	}

	return true
}

func (wb *writeBufferStage) write(now sim.VTimeInSec) bool {
	if len(wb.pendingEvictions) == 0 {
		return false
	}

	trans := wb.pendingEvictions[0]

	if wb.tooManyInflightEvictions() {
		return false
	}

	if !wb.cache.bottomSender.CanSend(1) {
		return false
	}

	lowModulePort := wb.cache.lowModuleFinder.Find(trans.evictingAddr)
	write := mem.WriteReqBuilder{}.
		WithSrc(wb.cache.bottomPort).
		WithDst(lowModulePort).
		WithPID(trans.evictingPID).
		WithAddress(trans.evictingAddr).
		WithData(trans.evictingData).
		WithDirtyMask(trans.evictingDirtyMask).
		Build()
	wb.cache.bottomSender.Send(write)

	trans.evictionWriteReq = write
	wb.pendingEvictions = wb.pendingEvictions[1:]
	wb.inflightEviction = append(wb.inflightEviction, trans)

	tracing.TraceReqInitiate(write, wb.cache,
		tracing.MsgIDAtReceiver(trans.req(), wb.cache))

	// log.Printf("%.10f, %s, wb write to bottom， %s, %04X, %04X, (%d, %d), %v\n",
	// 	now, wb.cache.Name(),
	// 	trans.accessReq().Meta().ID,
	// 	trans.evictingAddr, trans.evictingAddr,
	// 	trans.block.SetID, trans.block.WayID,
	// 	trans.evictingData,findInflightFetchByFetchReadReqID
	// )

	return true
}

func (wb *writeBufferStage) processReturnRsp(now sim.VTimeInSec) bool {
	msg := wb.cache.bottomPort.Peek()
	if msg == nil {
		return false
	}

	switch msg := msg.(type) {
	case *mem.DataReadyRsp:
		return wb.processDataReadyRsp(now, msg)
	case *mem.WriteDoneRsp:
		return wb.processWriteDoneRsp(now, msg)
	default:
		panic("unknown msg type")
	}
}

func (wb *writeBufferStage) processDataReadyRsp(
	now sim.VTimeInSec,
	dataReady *mem.DataReadyRsp,
) bool {
	fetches := wb.findInflightFetchesByFetchReadReqID(dataReady.RespondTo)
	if !wb.canPushFetchedDataToBanks(fetches) {
		return false
	}
	if wb.cache.adaptivePairAdapter != nil {
		wb.cache.adaptivePairAdapter.captureResponse(
			wb.cache, dataReady, fetches[0].fetchReadReq)
	}
	for _, trans := range fetches {
		wb.completeFetchedData(now, dataReady, trans)
	}
	wb.cache.bottomPort.Retrieve(now)
	tracing.TraceReqFinalize(fetches[0].fetchReadReq, wb.cache)

	return true
}

func (wb *writeBufferStage) canPushFetchedDataToBanks(
	fetches []*transaction,
) bool {
	if len(fetches) == 1 {
		return wb.bankBufferForFetch(fetches[0]).CanPush()
	}

	required := make(map[sim.Buffer]int)
	for _, trans := range fetches {
		required[wb.bankBufferForFetch(trans)]++
	}
	for bankBuffer, count := range required {
		if bankBuffer.Capacity()-bankBuffer.Size() < count {
			return false
		}
	}
	return true
}

func (wb *writeBufferStage) bankBufferForFetch(
	trans *transaction,
) sim.Buffer {
	bankIndex := bankID(
		trans.block,
		wb.cache.directory.WayAssociativity(),
		len(wb.cache.dirToBankBuffers),
	)
	return wb.cache.writeBufferToBankBuffers[bankIndex]
}

func (wb *writeBufferStage) completeFetchedData(
	now sim.VTimeInSec,
	dataReady *mem.DataReadyRsp,
	trans *transaction,
) {
	bankBuf := wb.bankBufferForFetch(trans)

	trans.fetchedData = wb.extractFetchedCacheLine(dataReady.Data, trans)
	if trans.granularitySibling &&
		uint64(len(dataReady.Data)) != uint64(1)<<wb.cache.log2BlockSize {
		panic("paired-read sibling response is not one cacheline")
	}
	trans.action = bankWriteFetched
	trans.mshrEntry.Data = trans.fetchedData
	if !trans.granularitySibling {
		memtrace.ObservationTransitionByRequest(
			trans.fetchReadReq.Meta().ID,
			"l2_dram_response_received", "l2_fill_response", now)
		memtrace.MarkObservationSource(
			trans.fetchReadReq.Meta().ID, "dram", wb.cache.Name())
		memtrace.RecordMemoryPathL2DRAMResponse(
			wb.cache.Name(),
			accessReqInfo(trans.accessReq()),
			trans.fetchReadReq.Meta().ID,
			dataReady.Meta().ID,
			dataReady.Meta().SendTime,
			now,
			dataReady.Meta().Src,
			dataReady.Meta().Dst,
		)
	}
	wb.combineData(trans.mshrEntry)
	memtrace.RecordL2LocalDRAMFill(
		wb.cache.Name(),
		uint64(len(trans.fetchedData)),
		now-trans.fetchReadReq.SendTime,
		now,
		"read",
	)
	req := trans.accessReq()
	memtrace.RecordL2AccessSource(
		wb.cache.Name(),
		accessReqInfo(req),
		trans.fetchAddress,
		uint64(len(trans.fetchedData)),
		now-trans.fetchReadReq.SendTime,
		now,
		accessReqOp(req),
		"dram",
	)

	wb.cache.mshr.Remove(trans.mshrEntry.PID, trans.mshrEntry.Address)
	wb.cache.untrackGranularityPending(
		now, trans.mshrEntry.PID, trans.mshrEntry.Address)
	wb.forwardReadOnlyFill(trans)

	bankBuf.Push(trans)

	wb.removeInflightFetch(trans)

	// log.Printf("%.10f, %s, wb data fetched from bottom, %s, %04X, %04X, (%d, %d), %v\n",
	// 	now, wb.cache.Name(),
	// 	trans.accessReq().Meta().ID,
	// 	trans.accessReq().GetAddress(), trans.block.Tag,
	// 	trans.block.SetID, trans.block.WayID,
	// 	trans.fetchedData,
	// )

}

func (wb *writeBufferStage) extractFetchedCacheLine(
	data []byte,
	trans *transaction,
) []byte {
	lineBytes := uint64(1) << wb.cache.log2BlockSize
	read := trans.fetchReadReq
	if read == nil || read.AccessByteSize <= lineBytes {
		return data
	}
	if trans.fetchAddress < read.Address {
		panic("invalid wide DRAM response")
	}
	offset := trans.fetchAddress - read.Address
	if offset+lineBytes > uint64(len(data)) {
		panic("wide DRAM response does not contain requested cacheline")
	}
	cacheLine := make([]byte, lineBytes)
	copy(cacheLine, data[offset:offset+lineBytes])
	return cacheLine
}

func (wb *writeBufferStage) forwardReadOnlyFill(trans *transaction) {
	if !wb.cache.fillForwarding || trans == nil || trans.mshrEntry == nil {
		return
	}

	reads := uint64(0)
	for _, request := range trans.mshrEntry.Requests {
		requestTrans, ok := request.(*transaction)
		if !ok || requestTrans.read == nil || requestTrans.read.LookupOnly {
			return
		}
		if requestTrans.prefetch {
			continue
		}
		reads++
	}
	if reads == 0 {
		return
	}

	wb.cache.fillForwardingStats.EligibleReadEntries++
	if !wb.cache.mshrStageBuffer.CanPush() {
		wb.cache.fillForwardingStats.BufferFallbacks++
		return
	}

	wb.cache.mshrStageBuffer.Push(trans.mshrEntry)
	trans.fillResponsesForwarded = true
	wb.cache.fillForwardingStats.ForwardedReadEntries++
	wb.cache.fillForwardingStats.ForwardedReads += reads
}

func (wb *writeBufferStage) combineData(mshrEntry *cache.MSHREntry) {
	mshrEntry.Block.DirtyMask = make([]bool, 1<<wb.cache.log2BlockSize)
	for _, t := range mshrEntry.Requests {
		trans := t.(*transaction)
		if trans.read != nil {
			continue
		}

		mshrEntry.Block.IsDirty = true
		write := trans.write
		_, offset := getCacheLineID(write.Address, wb.cache.log2BlockSize)
		for i := 0; i < len(write.Data); i++ {
			if write.DirtyMask == nil || write.DirtyMask[i] {
				index := offset + uint64(i)
				mshrEntry.Data[index] = write.Data[i]
				mshrEntry.Block.DirtyMask[index] = true
			}
		}
	}
}

func (wb *writeBufferStage) findInflightFetchesByFetchReadReqID(
	id string,
) []*transaction {
	fetches := make([]*transaction, 0, 2)
	for _, t := range wb.inflightFetch {
		if t.fetchReadReq.ID == id {
			fetches = append(fetches, t)
		}
	}

	if len(fetches) == 0 {
		panic("inflight read not found")
	}
	return fetches
}

func (wb *writeBufferStage) removeInflightFetch(f *transaction) {
	for i, trans := range wb.inflightFetch {
		if trans == f {
			wb.inflightFetch = append(
				wb.inflightFetch[:i],
				wb.inflightFetch[i+1:]...,
			)
			return
		}
	}

	panic("not found")
}

func (wb *writeBufferStage) processWriteDoneRsp(
	now sim.VTimeInSec,
	writeDone *mem.WriteDoneRsp,
) bool {
	for i := len(wb.inflightEviction) - 1; i >= 0; i-- {
		e := wb.inflightEviction[i]
		if e.evictionWriteReq.ID == writeDone.RespondTo {
			// log.Printf("%.10f, %s, wb write to bottom， %s, %04X, %04X, (%d, %d), %v\n",
			// 	now, wb.cache.Name(),
			// 	e.accessReq().Meta().ID,
			// 	e.evictingAddr, e.evictingAddr,
			// 	e.block.SetID, e.block.WayID,
			// 	e.evictingData,
			// )

			wb.inflightEviction = append(
				wb.inflightEviction[:i],
				wb.inflightEviction[i+1:]...,
			)
			memtrace.RecordL2LocalDRAMAccess(
				wb.cache.Name(),
				uint64(len(e.evictionWriteReq.Data)),
				now-e.evictionWriteReq.SendTime,
				now,
				"write",
			)
			wb.cache.bottomPort.Retrieve(now)
			tracing.TraceReqFinalize(e.evictionWriteReq, wb.cache)

			return true
		}
	}

	panic("write request not found")
}

func (wb *writeBufferStage) writeBufferFull() bool {
	numEntry := len(wb.pendingEvictions) + len(wb.inflightEviction)
	return numEntry >= wb.writeBufferCapacity
}

func (wb *writeBufferStage) tooManyInflightFetches() bool {
	return len(wb.inflightFetch) >= wb.maxInflightFetch
}

func (wb *writeBufferStage) tooManyInflightEvictions() bool {
	return len(wb.inflightEviction) >= wb.maxInflightEviction
}

func (wb *writeBufferStage) Reset(now sim.VTimeInSec) {
	wb.cache.writeBufferBuffer.Clear()
	if wb.cache.adaptivePairAdapter != nil {
		wb.cache.adaptivePairAdapter.reset(wb.cache)
	}
}
