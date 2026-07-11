package rdma

import (
	"fmt"
	"math/bits"

	"github.com/sarchlab/akita/v3/mem/mem"
	memtrace "github.com/sarchlab/akita/v3/mem/trace"
	"github.com/sarchlab/akita/v3/sim"
)

// ConfigureRemoteDataPath applies the requester-side remote-data mechanism.
func (c *Comp) ConfigureRemoteDataPath(config RemoteDataPathConfig) {
	c.remoteConfig = normalizeRemoteDataPathConfig(config)
	c.RemoteDataPathStats.Enabled = c.remoteConfig.Enabled
	c.RemoteDataPathStats.AUPrefetchEnabled = c.remoteConfig.AUPrefetch
	c.RemoteDataPathStats.DedupEnabled =
		c.remoteConfig.Enabled && !c.remoteConfig.DisableDedup
	c.RemoteDataPathStats.BatchingEnabled =
		c.remoteConfig.Enabled && !c.remoteConfig.DisableBatching
	c.RemoteDataPathStats.RequesterL2Enabled =
		c.remoteConfig.Enabled && !c.remoteConfig.DisableRequesterL2
	c.RemoteDataPathStats.MaxBatchLines = uint64(c.remoteConfig.MaxBatchLines)
	c.RemoteDataPathStats.MaxWaitNS = c.remoteConfig.MaxWaitNS
	c.RemoteDataPathStats.MaxBatches = uint64(c.remoteConfig.MaxBatches)
	c.RemoteDataPathStats.ReuseTableEntries =
		uint64(c.remoteConfig.ReuseTableEntries)
	if c.remoteConfig.Enabled {
		c.ensureRemoteDataPathState()
	}
}

// GetRemoteDataPathStats returns a snapshot of the mechanism counters.
func (c *Comp) GetRemoteDataPathStats() RemoteDataPathStats {
	return c.RemoteDataPathStats
}

func (c *Comp) ensureRemoteDataPathState() {
	if c.remoteBatches == nil {
		c.remoteBatches = make(map[remoteBatchKey]*remoteBatch)
	}
	if c.remoteLines == nil {
		c.remoteLines = make(map[remoteLineKey]*remoteLineEntry)
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
	if c.remoteReuse == nil && !c.remoteConfig.DisableRequesterL2 {
		c.remoteReuse = newRemoteReuseTable(c.remoteConfig.ReuseTableEntries)
	}
	if c.remoteOwnerSubReqs == nil {
		c.remoteOwnerSubReqs = make(map[string]*remoteOwnerSubReq)
	}
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
	if c.maxOutstanding <= 0 &&
		c.remoteOutstandingReads >= c.remoteOutstandingCapacity() {
		return true, false
	}

	identity := c.remoteIdentity(read, dst)
	key := remoteLineKey{
		remoteLineIdentity: identity,
		epoch:              c.remoteEpochs[identity],
	}
	if entry := c.remoteLines[key]; entry != nil &&
		!c.remoteConfig.DisableDedup {
		count := c.touchRemoteReuse(identity)
		c.addRemoteWaiter(entry, read, now, firstSeen, count)
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
	if !c.canAcceptRequesterOutstanding(1) {
		return true, false
	}

	if !c.remoteConfig.DisableRequesterL2 &&
		c.remoteCacheModules != nil && !c.remoteUncacheable[identity] {
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
			count := c.touchRemoteReuse(identity)
			entry := c.newRemoteDemandEntry(key, dst, read.Info)
			c.addRemoteWaiter(entry, read, now, firstSeen, count)
			c.remoteLines[key] = entry
			c.recordRequesterOutstandingPeak()
			c.remoteProbes[probe.ID] = &remoteProbe{
				entry: entry,
				req:   probe,
				sent:  now,
			}
			c.consumeRemoteRead(now, read)
			return true, true
		}
	}

	count := c.touchRemoteReuse(identity)
	entry := c.newRemoteDemandEntry(key, dst, read.Info)
	entry.state = remoteLinePendingBatch
	c.addRemoteWaiter(entry, read, now, firstSeen, count)
	c.remoteLines[key] = entry
	c.recordRequesterOutstandingPeak()
	c.remotePendingBatch = append(c.remotePendingBatch, entry)
	c.consumeRemoteRead(now, read)
	return true, true
}

func (c *Comp) touchRemoteReuse(identity remoteLineIdentity) uint8 {
	if c.remoteConfig.DisableRequesterL2 || c.remoteReuse == nil {
		return 0
	}
	return c.remoteReuse.Touch(identity)
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
	touchCount uint8,
) {
	if entry.wasPrefetch {
		// If the mate is demanded before the packet is sent, it is no longer
		// speculative wire traffic and should use normal demand admission.
		if entry.state == remoteLineCollecting && entry.batch != nil {
			c.RemoteDataPathStats.AUPrefetchConvertedDemand++
			line := remoteLineOffset(entry.key.lineAddr)
			entry.batch.prefetchBitmap &^= uint64(1) << line
			entry.wasPrefetch = false
		} else {
			c.RemoteDataPathStats.AUPrefetchDemandMerges++
		}
	}
	entry.waiters = append(entry.waiters, remoteWaiter{
		req:       read,
		arrival:   arrival,
		firstSeen: firstSeen,
	})
	if touchCount >= 2 && !entry.admit {
		entry.admit = true
		c.RemoteDataPathStats.TwoTouchCandidates++
	}
}

func (c *Comp) consumeRemoteRead(now sim.VTimeInSec, read *mem.ReadReq) {
	memtrace.RecordMemoryPathRDMARequestFromL1(
		c.Name(), read.Info, read.ID, read.SendTime,
		c.firstSeenFromL1Req[read.ID], read.Src, read.Dst,
	)
	c.ToL1.Retrieve(now)
	c.forgetSeen(c.firstSeenFromL1Req, read.ID)
	c.RemoteDataPathStats.LogicalRemoteReads++
	c.remoteOutstandingReads++
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

func (c *Comp) remoteOwnerOutstandingCapacity() int {
	capacity := c.remoteOutstandingCapacity()
	if capacity < 64 {
		return 64
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
		if c.remoteReuse != nil {
			c.remoteReuse.Delete(identity)
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
		lines: map[uint64]*remoteLineEntry{
			line: entry,
		},
	}
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
	if batch != nil && batch.lineBitmap&(uint64(1)<<line) == 0 &&
		batch.lineCount() >= c.remoteConfig.MaxBatchLines {
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
			lines:     make(map[uint64]*remoteLineEntry),
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
	c.addAUPrefetchLine(batch, line)
	return true, true
}

func (c *Comp) addAUPrefetchLine(batch *remoteBatch, demandLine uint64) {
	if !c.remoteConfig.AUPrefetch ||
		batch.lineCount() >= c.remoteConfig.MaxBatchLines {
		return
	}
	mate := demandLine ^ 1
	if mate >= 64 || batch.lineBitmap&(uint64(1)<<mate) != 0 {
		return
	}
	lineAddr := batch.key.pageAddr + mate*remoteLineBytes
	identity := remoteLineIdentity{
		ownerName: batch.key.ownerName,
		pid:       batch.key.pid,
		lineAddr:  lineAddr,
	}
	key := remoteLineKey{
		remoteLineIdentity: identity,
		epoch:              c.remoteEpochs[identity],
	}
	if c.remoteLines[key] != nil {
		return
	}
	if !c.canAcceptRequesterOutstanding(1) {
		return
	}
	entry := &remoteLineEntry{
		key:               key,
		owner:             batch.dst,
		state:             remoteLineCollecting,
		batch:             batch,
		wasPrefetch:       true,
		replicaGeneration: batch.lines[demandLine].replicaGeneration,
		info:              batch.info,
	}
	c.remoteLines[key] = entry
	c.recordRequesterOutstandingPeak()
	batch.lineBitmap |= uint64(1) << mate
	batch.prefetchBitmap |= uint64(1) << mate
	batch.lineOrder = append(batch.lineOrder, mate)
	batch.lines[mate] = entry
	c.RemoteDataPathStats.AUPrefetchCandidates++
}

func (c *Comp) processRemoteBatches(
	now sim.VTimeInSec,
	force bool,
) bool {
	madeProgress := false
	for issued := 0; issued < c.effectivePipelineWidth(); {
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
	for _, entry := range batch.lines {
		entry.state = remoteLineInflight
		entry.batch = nil
		entry.fromRemote = true
	}
	c.remoteBitmapInflight[req.ID] = batch
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
	prefetchLines := bits.OnesCount64(batch.prefetchBitmap)
	c.RemoteDataPathStats.BatchSizeHistogram[lineCount]++
	c.RemoteDataPathStats.BatchQueueWaitSamples++
	c.RemoteDataPathStats.WireLines += uint64(lineCount)
	c.RemoteDataPathStats.PrefetchWireLines += uint64(prefetchLines)
	c.RemoteDataPathStats.DemandWireLines += uint64(lineCount - prefetchLines)
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
	case "timeout":
		// Compatibility for tests or old callers that name the former
		// timeout reason directly. Runtime batching never uses this path.
		c.RemoteDataPathStats.TimeoutFlushes++
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
	c.RemoteDataPathStats.NetworkResponseBytes +=
		uint64(remoteLineBytes) + bitmapRspOverhead
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
	for _, line := range batch.lineOrder {
		data, ok := rsp.LineData[line]
		if !ok || len(data) != int(remoteLineBytes) {
			panic(fmt.Sprintf("bitmap response line %d is missing or invalid", line))
		}
		entry := batch.lines[line]
		entry.data = append([]byte(nil), data...)
		entry.state = remoteLineReady
		entry.fromRemote = true
		c.queueRemoteReady(entry)
	}
	c.RemoteDataPathStats.NetworkResponseBytes += uint64(
		bitmapRspOverhead + batch.lineCount()*int(remoteLineBytes))
	delete(c.remoteBitmapInflight, rsp.GetRspTo())
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
		(entry.admit || entry.wasPrefetch) &&
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
		dst := c.remoteCacheModules.Find(entry.key.lineAddr)
		if dst != nil {
			if entry.wasPrefetch {
				c.RemoteDataPathStats.PrefetchFillAttempts++
			} else {
				c.RemoteDataPathStats.TwoTouchFillAttempts++
			}
			fill := mem.RemoteDataFillBuilder{}.
				WithSendTime(now).
				WithSrc(c.ToL2).
				WithDst(dst).
				WithPID(entry.key.pid).
				WithAddress(entry.key.lineAddr).
				WithData(append([]byte(nil), entry.data...)).
				WithInfo(entry.info).
				WithGeneration(entry.replicaGeneration).
				WithPrefetch(entry.wasPrefetch).
				Build()
			if err := c.ToL2.Send(fill); err != nil {
				if entry.wasPrefetch {
					c.RemoteDataPathStats.PrefetchFillAttempts--
				} else {
					c.RemoteDataPathStats.TwoTouchFillAttempts--
				}
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
	if fillRsp.Installed {
		if entry.wasPrefetch {
			c.RemoteDataPathStats.PrefetchInstalledFills++
		} else {
			c.RemoteDataPathStats.TwoTouchInstalledFills++
		}
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
	}
}

func (c *Comp) resetRemoteDataPathHistory() {
	if c.remoteOutstandingReads != 0 {
		panic("RDMA drained with remote read waiters outstanding")
	}
	if c.remoteReuse != nil {
		c.remoteReuse.Reset()
	}
	clear(c.remoteEpochs)
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
		len(c.remoteOwnerPendingRsp) > 0
}
