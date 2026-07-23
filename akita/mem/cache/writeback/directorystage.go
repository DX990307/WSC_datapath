package writeback

import (
	"fmt"

	"github.com/sarchlab/akita/v3/mem/cache"
	"github.com/sarchlab/akita/v3/mem/mem"
	memtrace "github.com/sarchlab/akita/v3/mem/trace"
	"github.com/sarchlab/akita/v3/mem/vm"
	"github.com/sarchlab/akita/v3/pipelining"
	"github.com/sarchlab/akita/v3/sim"
	"github.com/sarchlab/akita/v3/tracing"
)

type dirPipelineItem struct {
	trans *transaction
}

func (i dirPipelineItem) TaskID() string {
	return i.trans.id + "_dir_pipeline"
}

type directoryStage struct {
	cache    *Cache
	pipeline pipelining.Pipeline
	buf      sim.Buffer
}

func (ds *directoryStage) Tick(now sim.VTimeInSec) (madeProgress bool) {
	// Advance requests that were already resident at the start of this cycle.
	// Accepting after the Tick prevents a newly accepted request from consuming
	// its first directory-latency stage at the same timestamp.
	madeProgress = ds.pipeline.Tick(now) || madeProgress
	madeProgress = ds.processTransaction(now) || madeProgress
	madeProgress = ds.acceptNewTransaction(now) || madeProgress

	return madeProgress
}

func (ds *directoryStage) processTransaction(
	now sim.VTimeInSec,
) bool {
	madeProgress := false

	for i := 0; i < ds.cache.numReqPerCycle; i++ {
		item := ds.buf.Peek()
		if item == nil {
			break
		}

		trans := item.(dirPipelineItem).trans

		addr := trans.accessReq().GetAddress()
		cacheLineID, _ := getCacheLineID(addr, ds.cache.log2BlockSize)
		if _, evicting := ds.cache.evictingList[cacheLineID]; evicting {
			break
		}
		if trans.read != nil {
			madeProgress = ds.doRead(now, trans) || madeProgress
			continue
		}

		madeProgress = ds.doWrite(now, trans) || madeProgress
	}

	return madeProgress
}

func (ds *directoryStage) acceptNewTransaction(now sim.VTimeInSec) bool {
	madeProgress := false

	for i := 0; i < ds.cache.numReqPerCycle; i++ {
		// Preserve the ordinary cache's pipeline-first admission order. When
		// the resident filter is present, inspect the head request first so a
		// confirmed miss can use the independent fast-miss path even while the
		// tag pipeline is full.
		pipelineReady := false
		if !ds.cache.residentFilterEnabled {
			if !ds.pipeline.CanAccept() {
				break
			}
			pipelineReady = true
		}

		item := ds.cache.dirStageBuffer.Peek()
		if item == nil {
			break
		}

		trans := item.(*transaction)
		// Local M1 admission may already carry a reliable RESIDENT-negative
		// result for this exact candidate line. It is the same proof consumed by
		// the ordinary fast-miss path, so forward it directly to the
		// post-directory buffer without a duplicate Filter or tag lookup.
		if trans.prefetch && trans.residentFilterChecked && trans.residentFastMiss &&
			ds.buf.CanPush() {
			req := trans.accessReq()
			memtrace.ObservationTransitionByRequest(
				req.Meta().ID, "l2_directory_start", "l2_filter", now)
			memtrace.RecordMemoryPathL2DirStart(
				ds.cache.Name(), req.Meta().ID, accessReqInfo(req), now)
			ds.buf.Push(dirPipelineItem{trans: trans})
			ds.cache.dirStageBuffer.Pop()
			madeProgress = true
			continue
		}
		if ds.canUseResidentFastMiss(trans) && ds.buf.CanPush() &&
			!trans.residentFilterChecked {
			req := trans.accessReq()
			line, _ := getCacheLineID(
				req.GetAddress(), ds.cache.log2BlockSize)
			// The exact L2 MSHR and RESIDENT metadata are logically checked in
			// parallel.  An exact MSHR hit is already authoritative, so it can
			// enter the ordinary post-directory path without waiting for either
			// the tag array or an otherwise unused Filter result.  This is an
			// MSHR reuse, not a bypass: doRead still performs the merge and all
			// responses remain attached to the exact entry.
			if trans.read != nil && !trans.read.LookupOnly &&
				ds.cache.mshr.Query(req.GetPID(), line) != nil {
				trans.residentFilterChecked = true
				trans.residentParallelMSHR = true
				memtrace.ObservationTransitionByRequest(
					req.Meta().ID, "l2_directory_start", "l2_mshr", now)
				memtrace.RecordMemoryPathL2DirStart(
					ds.cache.Name(), req.Meta().ID, accessReqInfo(req), now)
				ds.buf.Push(dirPipelineItem{trans: trans})
				ds.cache.dirStageBuffer.Pop()
				madeProgress = true
				continue
			}
			if trans.residentFilterLookup == nil {
				lookup, accepted := ds.cache.startResidentLookup(
					now, req.GetPID(), line)
				if !accepted {
					break
				}
				trans.residentFilterLookup = lookup
				if lookup != nil {
					madeProgress = true
					continue
				}
			}
			mayContain, reliable, ready := ds.cache.completeResidentLookup(
				now, trans.residentFilterLookup)
			if !ready {
				break
			}
			trans.residentFilterLookup = nil
			trans.residentFilterChecked = true
			if reliable && !mayContain {
				trans.residentFilterNegative = true
				// The cache slice and DRAM bank share the same configured
				// interleave. Ordinary filter-only reads bypass only when there
				// is no live miss. M1 deliberately treats the reliable negative
				// as an optimistic latency bypass even under concurrency; exact
				// MSHR/resource checks and the ordinary fill path remain below.
				// Writes retain their shortcut because a full-line write does
				// not create a DRAM read.
				canBypass := trans.read == nil ||
					ds.cache.granularityAdaptationEnabled ||
					len(ds.cache.mshr.AllEntries()) == 0
				if trans.read != nil && !canBypass {
					ds.cache.residentFilterStats.ReadBusyFallbacks++
				}
				trans.residentFastMiss = canBypass
				if canBypass {
					if trans.read != nil {
						ds.cache.residentFilterStats.ReadNegativeBypasses++
					} else {
						ds.cache.residentFilterStats.WriteNegativeBypasses++
						if ds.isWritingFullLine(trans.write) {
							ds.cache.residentFilterStats.WriteFullLineBypasses++
						} else {
							ds.cache.residentFilterStats.WritePartialBypasses++
						}
					}
					memtrace.ObservationTransitionByRequest(
						req.Meta().ID, "l2_directory_start", "l2_filter", now)
					memtrace.RecordMemoryPathL2DirStart(
						ds.cache.Name(), req.Meta().ID, accessReqInfo(req), now)
					ds.buf.Push(dirPipelineItem{trans: trans})
					ds.cache.dirStageBuffer.Pop()
					madeProgress = true
					continue
				}
			}
			trans.residentFilterPositive = reliable && mayContain
			// M1 uses the one-cycle Filter as the timing access for every
			// reliable read classification. A possible match still performs the
			// exact directory lookup in doRead, but it need not traverse the
			// modeled tag-latency pipeline first. This changes latency, not lookup
			// width or correctness: a false positive becomes an ordinary miss and
			// an unreliable result retains the baseline pipeline.
			if trans.read != nil && trans.residentFilterPositive &&
				ds.cache.granularityAdaptationEnabled && ds.buf.CanPush() {
				req := trans.accessReq()
				ds.cache.residentFilterStats.ReadPositiveFastPaths++
				memtrace.ObservationTransitionByRequest(
					req.Meta().ID, "l2_directory_start", "l2_filter", now)
				memtrace.RecordMemoryPathL2DirStart(
					ds.cache.Name(), req.Meta().ID, accessReqInfo(req), now)
				ds.buf.Push(dirPipelineItem{trans: trans})
				ds.cache.dirStageBuffer.Pop()
				madeProgress = true
				continue
			}
		}
		if !pipelineReady && !ds.pipeline.CanAccept() {
			break
		}
		if req := trans.accessReq(); req != nil {
			memtrace.ObservationTransitionByRequest(
				req.Meta().ID, "l2_directory_start", "l2_lookup", now)
			memtrace.RecordMemoryPathL2DirStart(
				ds.cache.Name(),
				req.Meta().ID,
				accessReqInfo(req),
				now,
			)
		}
		ds.pipeline.Accept(now, dirPipelineItem{trans})
		ds.cache.dirStageBuffer.Pop()

		madeProgress = true
	}

	return madeProgress
}

func (ds *directoryStage) canUseResidentFastMiss(trans *transaction) bool {
	if !ds.cache.residentFilterEnabled || trans == nil {
		return false
	}
	if trans.read != nil && trans.read.LookupOnly {
		return false
	}
	return trans.accessReq() != nil
}

func (ds *directoryStage) Reset(now sim.VTimeInSec) {
	ds.pipeline.Clear()
	ds.buf.Clear()
	ds.cache.dirStageBuffer.Clear()
}

func (ds *directoryStage) doRead(
	now sim.VTimeInSec,
	trans *transaction,
) bool {
	cachelineID, _ := getCacheLineID(
		trans.read.Address, ds.cache.log2BlockSize)

	if trans.read.LookupOnly {
		block := ds.cache.directory.Lookup(trans.read.PID, cachelineID)
		if block == nil || block.IsLocked {
			return ds.handleLookupOnlyMiss(now, trans, block)
		}
		if !ds.cache.remoteReplicaMatches(
			block, trans.read.PID, cachelineID,
		) {
			ds.cache.remoteReplicaStats.FilterFalsePositives++
		}
		return ds.handleReadHit(now, trans, block)
	}

	mshrEntry := ds.cache.mshr.Query(trans.read.PID, cachelineID)
	if mshrEntry != nil {
		if trans.prefetch {
			ds.dropRedundantPrefetch(trans)
			ds.buf.Pop()
			return true
		}
		ds.cache.markLocalPrefetchUseful(
			trans.read.PID, cachelineID)
		if trans.residentParallelMSHR {
			ds.cache.residentFilterStats.ReadParallelMSHRMerges++
		} else if ds.cache.residentFilterEnabled &&
			trans.residentFilterNegative {
			ds.cache.residentFilterStats.ReadNegativeMSHRMerges++
		}
		return ds.handleReadMSHRHit(now, trans, mshrEntry)
	}

	block := ds.cache.directory.Lookup(
		trans.read.PID, cachelineID)
	if block != nil {
		if trans.prefetch {
			ds.dropRedundantPrefetch(trans)
			ds.buf.Pop()
			return true
		}
		ds.cache.markLocalPrefetchUseful(
			trans.read.PID, cachelineID)
		return ds.handleReadHit(now, trans, block)
	}
	if trans.residentFilterPositive {
		ds.cache.residentFilterStats.FalsePositives++
	}

	return ds.handleReadMiss(now, trans)
}

func (ds *directoryStage) handleLookupOnlyMiss(
	now sim.VTimeInSec,
	trans *transaction,
	block *cache.Block,
) bool {
	if !ds.cache.topSender.CanSend(1) {
		return false
	}

	rsp := mem.CacheLookupRspBuilder{}.
		WithSendTime(now).
		WithSrc(ds.cache.topPort).
		WithDst(trans.read.Src).
		WithRspTo(trans.read.ID).
		WithHit(false).
		WithGeneration(ds.cache.remoteReplicaGeneration).
		Build()
	ds.cache.topSender.Send(rsp)
	ds.buf.Pop()
	ds.cache.removeInflightTransaction(trans)
	cachelineID, _ := getCacheLineID(
		trans.read.Address, ds.cache.log2BlockSize)
	if ds.cache.remoteReplicaMatches(block, trans.read.PID, cachelineID) {
		ds.cache.remoteReplicaStats.FilterTruePositiveUnavailable++
	} else {
		ds.cache.remoteReplicaStats.FilterFalsePositives++
	}
	ds.recordMemoryPathCacheResult(now, trans, "lookup-only-miss")
	memtrace.RecordMemoryPathCacheComplete(
		ds.cache.Name(),
		trans.read.ID,
		accessReqInfo(trans.read),
		trans.read.GetAddress(),
		trans.read.GetByteSize(),
		uint64(trans.read.GetPID()),
		accessReqOp(trans.read),
		now,
	)
	tracing.TraceReqComplete(trans.read, ds.cache)
	return true
}

func (ds *directoryStage) handleReadMSHRHit(
	now sim.VTimeInSec,
	trans *transaction,
	mshrEntry *cache.MSHREntry,
) bool {
	leaderRequestID := ""
	if len(mshrEntry.Requests) > 0 {
		if leader, ok := mshrEntry.Requests[0].(*transaction); ok &&
			leader.accessReq() != nil {
			leaderRequestID = leader.accessReq().Meta().ID
		}
	}
	trans.mshrEntry = mshrEntry
	mshrEntry.Requests = append(mshrEntry.Requests, trans)
	ds.buf.Pop()

	tracing.AddTaskStep(
		tracing.MsgIDAtReceiver(trans.read, ds.cache),
		ds.cache,
		"read-mshr-hit",
	)
	ds.recordMemoryPathCacheResult(now, trans, "read-mshr-hit")
	memtrace.MarkObservationL2MSHRFollower(
		trans.read.Meta().ID, leaderRequestID, now)
	ds.recordL2AccessSource(now, trans, "l2_mshr")

	return true
}

func (ds *directoryStage) handleReadHit(
	now sim.VTimeInSec,
	trans *transaction,
	block *cache.Block,
) bool {
	if block.IsLocked {
		return false
	}
	tracing.AddTaskStep(
		tracing.MsgIDAtReceiver(trans.read, ds.cache),
		ds.cache,
		"read-hit",
	)
	// fmt.Printf("%.10f, %s, dir read hit, %s, %04X, %04X, (%d, %d), %v\n",
	// 	now, ds.cache.Name(),
	// 	trans.read.ID,
	// 	trans.read.Address,
	// 	(trans.read.GetAddress()>>ds.cache.log2BlockSize)<<ds.cache.log2BlockSize,
	// 	block.SetID, block.WayID,
	// 	nil,
	// )

	ok := ds.readFromBank(trans, block)
	if ok {
		ds.recordMemoryPathCacheResult(now, trans, "read-hit")
		memtrace.ObservationTransitionByRequest(
			trans.read.Meta().ID, "l2_lookup_result", "l2_bank", now)
		ds.recordL2AccessSource(now, trans, "l2_cache")
	}
	return ok
}

func (ds *directoryStage) handleReadMiss(
	now sim.VTimeInSec,
	trans *transaction,
) bool {
	req := trans.read
	cacheLineID, _ := getCacheLineID(req.Address, ds.cache.log2BlockSize)

	if ds.cache.mshr.IsFull() {
		if trans.prefetch {
			ds.dropRedundantPrefetch(trans)
			ds.buf.Pop()
			ds.cache.localPrefetchStats.MSHRDrops++
			return true
		}
		if ds.cache.localPrefetchOutstanding > 0 {
			ds.cache.localPrefetchStats.DemandDelayEvents++
		}
		ds.cache.residentFilterStats.MSHRFullStalls++
		return false
	}

	victim := ds.cache.directory.FindVictim(cacheLineID)
	if trans.prefetch {
		if victim == nil {
			ds.dropRedundantPrefetch(trans)
			ds.buf.Pop()
			ds.cache.localPrefetchStats.VictimDrops++
			return true
		}
		bankNum := bankID(
			victim, ds.cache.directory.WayAssociativity(),
			len(ds.cache.dirToBankBuffers))
		if victim.IsValid || victim.IsLocked ||
			victim.ReadCount > 0 ||
			!ds.cache.dirToBankBuffers[bankNum].CanPush() {
			ds.dropRedundantPrefetch(trans)
			ds.buf.Pop()
			ds.cache.localPrefetchStats.VictimDrops++
			return true
		}
	}
	if victim.IsLocked || victim.ReadCount > 0 {
		return false
	}

	if ds.needEviction(victim) {
		ok := ds.evict(now, trans, victim)
		if ok {
			tracing.AddTaskStep(
				tracing.MsgIDAtReceiver(trans.read, ds.cache),
				ds.cache,
				"read-miss",
			)
			ds.recordMemoryPathCacheResult(now, trans, "read-miss")
			memtrace.ObservationTransitionByRequest(
				trans.read.Meta().ID, "l2_lookup_result", "l2_write_buffer", now)

			// fmt.Printf("%.10f, %s, dir read miss, %s, %04X, %04X, (%d, %d), %v\n",
			// 	now, ds.cache.Name(),
			// 	trans.read.ID,
			// 	trans.read.Address,
			// 	(trans.read.GetAddress()>>ds.cache.log2BlockSize)<<ds.cache.log2BlockSize,
			// 	victim.SetID, victim.WayID,
			// 	nil,
			// )
		}

		return ok
	}

	ok := ds.fetch(now, trans, victim)
	if ok {
		tracing.AddTaskStep(
			tracing.MsgIDAtReceiver(trans.read, ds.cache),
			ds.cache,
			"read-miss",
		)
		ds.recordMemoryPathCacheResult(now, trans, "read-miss")
		memtrace.ObservationTransitionByRequest(
			trans.read.Meta().ID, "l2_lookup_result", "l2_write_buffer", now)

		// fmt.Printf("%.10f, %s, dir read miss, %s, %04X, %04X, (%d, %d), %v\n",
		// 	now, ds.cache.Name(),
		// 	trans.read.ID,
		// 	trans.read.Address,
		// 	(trans.read.GetAddress()>>ds.cache.log2BlockSize)<<ds.cache.log2BlockSize,
		// 	victim.SetID, victim.WayID,
		// 	nil,
		// )
	}

	return ok
}

func (ds *directoryStage) dropRedundantPrefetch(trans *transaction) {
	if trans == nil || !trans.prefetch || trans.read == nil {
		return
	}
	ds.cache.finishLocalPrefetchWithoutFill(trans.read.PID, trans.read.Address)
	ds.cache.removeInflightTransaction(trans)
}

func (ds *directoryStage) doWrite(
	now sim.VTimeInSec,
	trans *transaction,
) bool {
	write := trans.write
	cachelineID, _ := getCacheLineID(write.Address, ds.cache.log2BlockSize)

	mshrEntry := ds.cache.mshr.Query(write.PID, cachelineID)
	if mshrEntry != nil {
		ok := ds.doWriteMSHRHit(now, trans, mshrEntry)
		tracing.AddTaskStep(
			tracing.MsgIDAtReceiver(trans.write, ds.cache),
			ds.cache,
			"write-mshr-hit",
		)
		if ok {
			ds.recordMemoryPathCacheResult(now, trans, "write-mshr-hit")
			leaderRequestID := ""
			if len(mshrEntry.Requests) > 1 {
				if leader, ok := mshrEntry.Requests[0].(*transaction); ok &&
					leader.accessReq() != nil {
					leaderRequestID = leader.accessReq().Meta().ID
				}
			}
			memtrace.MarkObservationL2MSHRFollower(
				trans.write.Meta().ID, leaderRequestID, now)
			ds.recordL2AccessSource(now, trans, "l2_mshr")
		}

		return ok
	}

	block := ds.cache.directory.Lookup(trans.write.PID, cachelineID)
	if block != nil {
		ok := ds.doWriteHit(trans, block)
		if ok {
			tracing.AddTaskStep(
				tracing.MsgIDAtReceiver(trans.write, ds.cache),
				ds.cache,
				"write-hit",
			)
			ds.recordMemoryPathCacheResult(now, trans, "write-hit")
			memtrace.ObservationTransitionByRequest(
				trans.write.Meta().ID, "l2_lookup_result", "l2_bank", now)
			ds.recordL2AccessSource(now, trans, "l2_cache")
		}

		return ok
	}
	if trans.residentFilterPositive {
		ds.cache.residentFilterStats.FalsePositives++
	}

	ok := ds.doWriteMiss(now, trans)
	if ok {
		tracing.AddTaskStep(
			tracing.MsgIDAtReceiver(trans.write, ds.cache),
			ds.cache,
			"write-miss",
		)
		ds.recordMemoryPathCacheResult(now, trans, "write-miss")
		memtrace.ObservationTransitionByRequest(
			trans.write.Meta().ID, "l2_lookup_result", "l2_write_buffer", now)
	}

	return ok
}

func (ds *directoryStage) doWriteMSHRHit(
	now sim.VTimeInSec,
	trans *transaction,
	mshrEntry *cache.MSHREntry,
) bool {
	trans.mshrEntry = mshrEntry
	mshrEntry.Requests = append(mshrEntry.Requests, trans)
	ds.buf.Pop()

	return true
}

func (ds *directoryStage) doWriteHit(
	trans *transaction,
	block *cache.Block,
) bool {
	if block.IsLocked || block.ReadCount > 0 {
		return false
	}

	return ds.writeToBank(trans, block)
}

func (ds *directoryStage) doWriteMiss(
	now sim.VTimeInSec,
	trans *transaction,
) bool {
	write := trans.write

	if ds.isWritingFullLine(write) {
		return ds.writeFullLineMiss(now, trans)
	}
	return ds.writePartialLineMiss(now, trans)
}

func (ds *directoryStage) writeFullLineMiss(now sim.VTimeInSec, trans *transaction) bool {
	write := trans.write
	cachelineID, _ := getCacheLineID(write.Address, ds.cache.log2BlockSize)

	victim := ds.cache.directory.FindVictim(cachelineID)
	if victim.IsLocked || victim.ReadCount > 0 {
		return false
	}

	if ds.needEviction(victim) {
		ok := ds.evict(now, trans, victim)
		if ok {
			ds.recordL2AccessSource(now, trans, "write_allocate")
		}
		return ok
	}

	ok := ds.writeToBank(trans, victim)
	if ok {
		ds.recordL2AccessSource(now, trans, "write_allocate")
	}
	return ok
}

func (ds *directoryStage) writePartialLineMiss(
	now sim.VTimeInSec,
	trans *transaction,
) bool {
	write := trans.write
	cachelineID, _ := getCacheLineID(write.Address, ds.cache.log2BlockSize)

	if ds.cache.mshr.IsFull() {
		ds.cache.residentFilterStats.MSHRFullStalls++
		return false
	}

	victim := ds.cache.directory.FindVictim(cachelineID)
	if victim.IsLocked || victim.ReadCount > 0 {
		return false
	}

	// fmt.Printf("%.10f, %s, write partial line , %s, %04X, %04X, (%d, %d), %v\n",
	// 	now, ds.cache.Name(),
	// 	trans.write.ID,
	// 	trans.write.Address, cachelineID,
	// 	victim.SetID, victim.WayID,
	// 	write.Data,
	// )

	if ds.needEviction(victim) {

		ok := ds.evict(now, trans, victim)
		if ok {
			// fmt.Printf("%.10f, %s, dir read miss, %s, %04X, %04X, (%d, %d), %v\n",
			// 	now, ds.cache.Name(),
			// 	trans.read.ID,
			// 	trans.read.Address,
			// 	(trans.read.GetAddress()>>ds.cache.log2BlockSize)<<ds.cache.log2BlockSize,
			// 	victim.SetID, victim.WayID,
			// 	nil,
			// )
		}
		return ok
	}

	return ds.fetch(now, trans, victim)
}

func (ds *directoryStage) readFromBank(
	trans *transaction,
	block *cache.Block,
) bool {
	numBanks := len(ds.cache.dirToBankBuffers)
	bank := bankID(block, ds.cache.directory.WayAssociativity(), numBanks)
	bankBuf := ds.cache.dirToBankBuffers[bank]

	if !bankBuf.CanPush() {
		return false
	}

	ds.cache.directory.Visit(block)
	block.ReadCount++
	trans.block = block
	trans.action = bankReadHit
	ds.buf.Pop()
	bankBuf.Push(trans)
	return true
}

func (ds *directoryStage) writeToBank(
	trans *transaction,
	block *cache.Block,
) bool {
	numBanks := len(ds.cache.dirToBankBuffers)
	bank := bankID(block, ds.cache.directory.WayAssociativity(), numBanks)
	bankBuf := ds.cache.dirToBankBuffers[bank]

	if !bankBuf.CanPush() {
		return false
	}

	addr := trans.write.Address
	cachelineID, _ := getCacheLineID(addr, ds.cache.log2BlockSize)

	ds.cache.untrackRemoteReplica(block)
	ds.cache.untrackResidentBlock(block)
	ds.cache.directory.Visit(block)
	block.IsLocked = true
	block.Tag = cachelineID
	block.IsValid = true
	block.PID = trans.write.PID
	ds.cache.trackResidentBlock(block)
	trans.block = block
	trans.action = bankWriteHit
	ds.buf.Pop()
	bankBuf.Push(trans)

	return true
}

func (ds *directoryStage) evict(
	now sim.VTimeInSec,
	trans *transaction,
	victim *cache.Block,
) bool {
	bankNum := bankID(victim,
		ds.cache.directory.WayAssociativity(), len(ds.cache.dirToBankBuffers))
	bankBuf := ds.cache.dirToBankBuffers[bankNum]

	if !bankBuf.CanPush() {
		return false
	}

	var addr uint64
	var pid vm.PID
	if trans.read != nil {
		addr = trans.read.Address
		pid = trans.read.PID
	} else {
		addr = trans.write.Address
		pid = trans.write.PID
	}

	cacheLineID, _ := getCacheLineID(addr, ds.cache.log2BlockSize)

	ds.updateTransForEviction(now, trans, victim, pid, cacheLineID)
	ds.updateVictimBlockMetaData(victim, cacheLineID, pid)

	ds.buf.Pop()
	bankBuf.Push(trans)
	ds.cache.evictingList[trans.victim.Tag] = true

	// fmt.Printf("%.10f, %s, directory evict , %s, %04X, %04X, (%d, %d), %v\n",
	// 	now, ds.cache.Name(),
	// 	trans.accessReq().Meta().ID,
	// 	trans.accessReq().GetAddress(), trans.victim.Tag,
	// 	victim.SetID, victim.WayID,
	// 	nil,
	// )

	return true
}

func (ds *directoryStage) updateVictimBlockMetaData(
	victim *cache.Block,
	cacheLineID uint64,
	pid vm.PID,
) {
	ds.cache.untrackRemoteReplica(victim)
	ds.cache.untrackResidentBlock(victim)
	victim.Tag = cacheLineID
	victim.PID = pid
	victim.IsLocked = true
	victim.IsDirty = false
	ds.cache.directory.Visit(victim)
	// trackResidentBlock deliberately ignores this locked allocation. Read
	// fills and full-line writes insert only after the L2 bank has the data.
	ds.cache.trackResidentBlock(victim)
}

func (ds *directoryStage) updateTransForEviction(
	now sim.VTimeInSec,
	trans *transaction,
	victim *cache.Block,
	pid vm.PID,
	cacheLineID uint64,
) {
	trans.action = bankEvictAndFetch
	trans.victim = &cache.Block{
		PID:          victim.PID,
		Tag:          victim.Tag,
		CacheAddress: victim.CacheAddress,
		DirtyMask:    victim.DirtyMask,
	}
	trans.block = victim
	trans.evictingPID = trans.victim.PID
	trans.evictingAddr = trans.victim.Tag
	trans.evictingDirtyMask = victim.DirtyMask

	if ds.evictionNeedFetch(trans) {
		mshrEntry := ds.cache.mshr.Add(pid, cacheLineID)
		ds.cache.trackGranularityPending(now, pid, cacheLineID)
		mshrEntry.Block = victim
		mshrEntry.Requests = append(mshrEntry.Requests, trans)
		trans.mshrEntry = mshrEntry
		trans.fetchPID = pid
		trans.fetchAddress = cacheLineID
		trans.action = bankEvictAndFetch
	} else {
		trans.action = bankEvictAndWrite
	}
}

func (ds *directoryStage) evictionNeedFetch(t *transaction) bool {
	if t.write == nil {
		return true
	}

	if ds.isWritingFullLine(t.write) {
		return false
	}

	return true
}

func (ds *directoryStage) fetch(
	now sim.VTimeInSec,
	trans *transaction,
	block *cache.Block,
) bool {
	var addr uint64
	var pid vm.PID
	var req mem.AccessReq
	if trans.read != nil {
		req = trans.read
		addr = trans.read.Address
		pid = trans.read.PID
	} else {
		req = trans.write
		addr = trans.write.Address
		pid = trans.write.PID
	}
	cacheLineID, _ := getCacheLineID(addr, ds.cache.log2BlockSize)

	bankNum := bankID(block,
		ds.cache.directory.WayAssociativity(), len(ds.cache.dirToBankBuffers))
	bankBuf := ds.cache.dirToBankBuffers[bankNum]

	if !bankBuf.CanPush() {
		return false
	}

	mshrEntry := ds.cache.mshr.Add(pid, cacheLineID)
	ds.cache.trackGranularityPending(now, pid, cacheLineID)
	trans.mshrEntry = mshrEntry
	trans.block = block
	ds.cache.untrackRemoteReplica(block)
	ds.cache.untrackResidentBlock(block)
	block.IsLocked = true
	block.Tag = cacheLineID
	block.PID = pid
	block.IsValid = true
	ds.cache.directory.Visit(block)

	tracing.AddTaskStep(
		tracing.MsgIDAtReceiver(req, ds.cache),
		ds.cache,
		fmt.Sprintf("add-mshr-entry-0x%x-0x%x", mshrEntry.Address, block.Tag),
	)

	ds.buf.Pop()

	trans.action = writeBufferFetch
	trans.fetchPID = pid
	trans.fetchAddress = cacheLineID
	bankBuf.Push(trans)

	mshrEntry.Block = block
	mshrEntry.Requests = append(mshrEntry.Requests, trans)

	return true
}

func (ds *directoryStage) isWritingFullLine(write *mem.WriteReq) bool {
	blockSize := 1 << ds.cache.log2BlockSize
	if write == nil || len(write.Data) != blockSize ||
		write.Address%uint64(blockSize) != 0 {
		return false
	}

	if write.DirtyMask != nil {
		if len(write.DirtyMask) != blockSize {
			return false
		}
		for _, dirty := range write.DirtyMask {
			if !dirty {
				return false
			}
		}
	}

	return true
}

func (ds *directoryStage) needEviction(victim *cache.Block) bool {
	return victim.IsValid && victim.IsDirty
}

func (ds *directoryStage) recordL2AccessSource(
	now sim.VTimeInSec,
	trans *transaction,
	sourceBase string,
) {
	req := trans.accessReq()
	if req == nil {
		return
	}
	cacheLineID, _ := getCacheLineID(req.GetAddress(), ds.cache.log2BlockSize)

	memtrace.RecordL2AccessSource(
		ds.cache.Name(),
		accessReqInfo(req),
		cacheLineID,
		req.GetByteSize(),
		0,
		now,
		accessReqOp(req),
		sourceBase,
	)
}

func (ds *directoryStage) recordMemoryPathCacheResult(
	now sim.VTimeInSec,
	trans *transaction,
	result string,
) {
	req := trans.accessReq()
	if req == nil {
		return
	}
	memtrace.RecordMemoryPathCacheResult(
		ds.cache.Name(),
		req.Meta().ID,
		accessReqInfo(req),
		req.GetAddress(),
		req.GetByteSize(),
		uint64(req.GetPID()),
		accessReqOp(req),
		result,
		now,
	)
	memtrace.MarkObservationL2Result(
		req.Meta().ID, ds.cache.Name(), result)
}

func accessReqInfo(req mem.AccessReq) interface{} {
	switch req := req.(type) {
	case *mem.ReadReq:
		return req.Info
	case *mem.WriteReq:
		return req.Info
	default:
		return nil
	}
}

func accessReqStreamID(req mem.AccessReq) uint64 {
	if read, ok := req.(*mem.ReadReq); ok && read != nil {
		return read.StreamID
	}
	return 0
}

func accessReqLocalStreamID(req mem.AccessReq) uint64 {
	if read, ok := req.(*mem.ReadReq); ok && read != nil {
		return read.LocalStreamID
	}
	return 0
}

func accessReqLocalPairHint(req mem.AccessReq) bool {
	read, ok := req.(*mem.ReadReq)
	return ok && read != nil && read.LocalPairHint
}

func accessReqOp(req mem.AccessReq) string {
	switch req.(type) {
	case *mem.ReadReq:
		return "read"
	case *mem.WriteReq:
		return "write"
	default:
		return "unknown"
	}
}
