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
	if wb.cache.dramAdapter != nil &&
		!wb.cache.dramBatchFetchBatchable(trans) {
		wb.cache.dramAdapter.invalidateTransaction(wb.cache, trans)
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
	if wb.cache.dramBatchEnabled() &&
		wb.cache.dramBatchFetchBatchable(trans) {
		return wb.processAdaptiveDRAMFetch(now, trans)
	}

	if !wb.issueSingleFetch(now, trans, false) {
		return false
	}
	wb.cache.writeBufferBuffer.Pop()
	return true
}

func (wb *writeBufferStage) issueSingleFetch(
	now sim.VTimeInSec,
	trans *transaction,
	countDRAMBatch bool,
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
	if countDRAMBatch {
		wb.cache.dramBatchStats.SingleLineReads++
	}

	tracing.TraceReqInitiate(read, wb.cache,
		tracing.MsgIDAtReceiver(trans.req(), wb.cache))

	return true
}

func (wb *writeBufferStage) recordDRAMReadSend(
	now sim.VTimeInSec,
	read *mem.ReadReq,
	trans *transaction,
) {
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
	if wb.cache.dramAdapter != nil {
		wb.cache.dramAdapter.captureResponse(
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
	trans.action = bankWriteFetched
	trans.mshrEntry.Data = trans.fetchedData
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
		panic("invalid DRAM batch response")
	}

	offset := trans.fetchAddress - read.Address
	if offset+lineBytes > uint64(len(data)) {
		panic("DRAM response does not contain the requested cache line")
	}

	cacheLine := make([]byte, lineBytes)
	copy(cacheLine, data[offset:offset+lineBytes])
	return cacheLine
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
	wb.cache.resetDRAMBatchState()
}
