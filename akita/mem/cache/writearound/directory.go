package writearound

import (
	"strings"

	"github.com/sarchlab/akita/v3/mem/cache"
	"github.com/sarchlab/akita/v3/mem/mem"
	memtrace "github.com/sarchlab/akita/v3/mem/trace"
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

type directory struct {
	cache *Cache

	pipeline pipelining.Pipeline
	buf      sim.Buffer
}

func (d *directory) Tick(now sim.VTimeInSec) (madeProgress bool) {
	for i := 0; i < d.cache.numReqPerCycle; i++ {
		if !d.pipeline.CanAccept() {
			break
		}

		item := d.cache.dirBuf.Peek()
		if item == nil {
			break
		}

		trans := item.(*transaction)
		memtrace.ObservationTransition(
			trans.id, "l1_directory_start", "l1_lookup", now)
		memtrace.RecordMemoryPathL1VDirStart(
			d.cache.Name(), trans.id, now)
		d.pipeline.Accept(now, dirPipelineItem{trans})
		d.cache.dirBuf.Pop()

		madeProgress = true
	}

	madeProgress = d.pipeline.Tick(now) || madeProgress

	for i := 0; i < d.cache.numReqPerCycle; i++ {
		item := d.buf.Peek()
		if item == nil {
			break
		}

		trans := item.(dirPipelineItem).trans

		if trans.read != nil {
			madeProgress = d.processRead(now, trans) || madeProgress
			continue
		}

		madeProgress = d.processWrite(now, trans) || madeProgress
	}

	return madeProgress
}

func (d *directory) processRead(now sim.VTimeInSec, trans *transaction) bool {
	read := trans.read
	addr := read.Address
	pid := read.PID
	blockSize := uint64(1 << d.cache.log2BlockSize)
	cacheLineID := addr / blockSize * blockSize

	mshrEntry := d.cache.mshr.Query(pid, cacheLineID)
	if mshrEntry != nil {
		return d.processMSHRHit(now, trans, mshrEntry)
	}

	block := d.cache.directory.Lookup(pid, cacheLineID)
	if block != nil && block.IsValid {
		return d.processReadHit(now, trans, block)
	}

	return d.processReadMiss(now, trans)
}

func (d *directory) processMSHRHit(
	now sim.VTimeInSec,
	trans *transaction,
	mshrEntry *cache.MSHREntry,
) bool {
	leaderPathID := ""
	if len(mshrEntry.Requests) > 0 {
		if leader, ok := mshrEntry.Requests[0].(*transaction); ok {
			leaderPathID = leader.id
		}
	}
	mshrEntry.Requests = append(mshrEntry.Requests, trans)

	if trans.read != nil {
		tracing.AddTaskStep(trans.id, d.cache, "read-mshr-hit")
		d.recordMemoryPathCacheResult(now, trans, "read-mshr-hit")
	} else {
		tracing.AddTaskStep(trans.id, d.cache, "write-mshr-hit")
		d.recordMemoryPathCacheResult(now, trans, "write-mshr-hit")
	}
	memtrace.MarkObservationL1MSHRFollower(
		trans.id, leaderPathID, now)

	d.buf.Pop()

	return true
}

func (d *directory) processReadHit(
	now sim.VTimeInSec,
	trans *transaction,
	block *cache.Block,
) bool {
	if block.IsLocked {
		return false
	}

	bankBuf := d.getBankBuf(block)
	if !bankBuf.CanPush() {
		return false
	}

	trans.block = block
	trans.bankAction = bankActionReadHit
	block.ReadCount++
	d.cache.directory.Visit(block)
	bankBuf.Push(trans)

	d.buf.Pop()
	tracing.AddTaskStep(trans.id, d.cache, "read-hit")
	d.recordMemoryPathCacheResult(now, trans, "read-hit")

	return true
}

func (d *directory) processReadMiss(
	now sim.VTimeInSec,
	trans *transaction,
) bool {
	read := trans.read
	addr := read.Address
	blockSize := uint64(1 << d.cache.log2BlockSize)
	cacheLineID := addr / blockSize * blockSize

	victim := d.cache.directory.FindVictim(cacheLineID)
	if victim.IsLocked || victim.ReadCount > 0 {
		return false
	}

	if d.cache.mshr.IsFull() {
		return false
	}

	if !d.fetchFromBottom(now, trans, victim) {
		return false
	}

	d.buf.Pop()
	tracing.AddTaskStep(trans.id, d.cache, "read-miss")
	d.recordMemoryPathCacheResult(now, trans, "read-miss")

	return true
}

func (d *directory) processWrite(
	now sim.VTimeInSec,
	trans *transaction,
) bool {
	write := trans.write
	addr := write.Address
	pid := write.PID
	blockSize := uint64(1 << d.cache.log2BlockSize)
	cacheLineID := addr / blockSize * blockSize

	mshrEntry := d.cache.mshr.Query(pid, cacheLineID)
	if mshrEntry != nil {
		ok := d.writeBottom(now, trans)
		if ok {
			return d.processMSHRHit(now, trans, mshrEntry)
		}
		return false
	}

	block := d.cache.directory.Lookup(pid, cacheLineID)
	if block != nil && block.IsValid {
		return d.processWriteHit(now, trans, block)
	}

	return d.writeMiss(now, trans)
}

func (d *directory) writeMiss(
	now sim.VTimeInSec,
	trans *transaction,
) bool {
	if ok := d.writeBottom(now, trans); ok {
		tracing.AddTaskStep(trans.id, d.cache, "write-miss")
		d.recordMemoryPathCacheResult(now, trans, "write-miss")
		d.buf.Pop()
		return true
	}

	return false
}

func (d *directory) writeBottom(now sim.VTimeInSec, trans *transaction) bool {
	write := trans.write
	addr := write.Address
	bottomModule := d.cache.lowModuleFinder.Find(addr)

	if !d.cache.canSendToBottomModule(bottomModule) {
		return false
	}

	writeToBottom := mem.WriteReqBuilder{}.
		WithSendTime(now).
		WithSrc(d.cache.bottomPort).
		WithDst(bottomModule).
		WithAddress(addr).
		WithPID(write.PID).
		WithData(write.Data).
		WithDirtyMask(write.DirtyMask).
		WithInfo(d.memoryPathInfo(trans)).
		Build()

	err := d.cache.bottomPort.Send(writeToBottom)
	if err != nil {
		return false
	}

	trans.writeToBottom = writeToBottom
	if LocalBottomHook != nil {
		d.localBottom(bottomModule, addr, now)
	}
	if memtrace.PgclsEnabled() {
		d.pgclsLocal(bottomModule, addr, uint64(len(write.Data)), false, now)
	}
	memtrace.LinkObservationRequest(
		trans.id, writeToBottom.Meta().ID, "l1_bottom_write")
	d.cache.trackBottomTransaction(trans, bottomModule)

	tracing.TraceReqInitiate(writeToBottom, d.cache, trans.id)

	return true
}

func (d *directory) processWriteHit(
	now sim.VTimeInSec,
	trans *transaction,
	block *cache.Block,
) bool {
	if block.IsLocked || block.ReadCount > 0 {
		return false
	}

	bankBuf := d.getBankBuf(block)
	if !bankBuf.CanPush() {
		return false
	}

	if trans.writeToBottom == nil {
		ok := d.writeBottom(now, trans)
		if !ok {
			return false
		}
	}

	write := trans.write
	addr := write.Address
	blockSize := uint64(1 << d.cache.log2BlockSize)
	cacheLineID := addr / blockSize * blockSize
	block.IsLocked = true
	block.IsValid = true
	block.Tag = cacheLineID
	d.cache.directory.Visit(block)

	trans.bankAction = bankActionWrite
	trans.block = block
	bankBuf.Push(trans)

	tracing.AddTaskStep(trans.id, d.cache, "write-hit")
	d.recordMemoryPathCacheResult(now, trans, "write-hit")
	d.buf.Pop()

	return true
}

func (d *directory) fetchFromBottom(
	now sim.VTimeInSec,
	trans *transaction,
	victim *cache.Block,
) bool {
	addr := trans.Address()
	pid := trans.PID()
	blockSize := uint64(1 << d.cache.log2BlockSize)
	cacheLineID := addr / blockSize * blockSize

	bottomModule := d.cache.lowModuleFinder.Find(cacheLineID)

	if !d.cache.canSendToBottomModule(bottomModule) {
		return false
	}

	readToBottom := mem.ReadReqBuilder{}.
		WithSendTime(now).
		WithSrc(d.cache.bottomPort).
		WithDst(bottomModule).
		WithAddress(cacheLineID).
		WithPID(pid).
		WithByteSize(blockSize).
		WithStreamID(readStreamID(trans.read)).
		WithLocalStreamID(readLocalStreamID(trans.read)).
		WithLocalPairHint(readLocalPairHint(trans.read)).
		WithInfo(d.memoryPathInfo(trans)).
		Build()
	err := d.cache.bottomPort.Send(readToBottom)
	if err != nil {
		return false
	}

	tracing.TraceReqInitiate(readToBottom, d.cache, trans.id)
	trans.readToBottom = readToBottom
	if LocalBottomHook != nil {
		d.localBottom(bottomModule, cacheLineID, now)
	}
	if memtrace.PgclsEnabled() {
		d.pgclsLocal(bottomModule, cacheLineID, blockSize, true, now)
	}
	memtrace.LinkObservationRequest(
		trans.id, readToBottom.Meta().ID, "l1_bottom_read")
	trans.block = victim
	d.cache.trackBottomTransaction(trans, bottomModule)

	mshrEntry := d.cache.mshr.Add(pid, cacheLineID)
	mshrEntry.Requests = append(mshrEntry.Requests, trans)
	mshrEntry.ReadReq = readToBottom
	mshrEntry.Block = victim

	victim.Tag = cacheLineID
	victim.PID = pid
	victim.IsValid = true
	victim.IsLocked = true
	d.cache.directory.Visit(victim)

	return true
}

func readStreamID(read *mem.ReadReq) uint64 {
	if read == nil {
		return 0
	}
	return read.StreamID
}

func readLocalStreamID(read *mem.ReadReq) uint64 {
	if read == nil {
		return 0
	}
	return read.LocalStreamID
}

func readLocalPairHint(read *mem.ReadReq) bool {
	return read != nil && read.LocalPairHint
}

func (d *directory) getBankBuf(block *cache.Block) sim.Buffer {
	numWaysPerSet := d.cache.directory.WayAssociativity()
	blockID := block.SetID*numWaysPerSet + block.WayID
	bankID := blockID % len(d.cache.bankBufs)
	return d.cache.bankBufs[bankID]
}

func (d *directory) memoryPathInfo(trans *transaction) interface{} {
	if !memtrace.MemoryPathTraceEnabled() {
		return nil
	}
	return memtrace.WithMemoryPathInfo(nil, trans.id, "", "", "")
}

func (d *directory) recordMemoryPathCacheResult(
	now sim.VTimeInSec,
	trans *transaction,
	result string,
) {
	req := trans.accessReq()
	if req == nil {
		return
	}
	memtrace.RecordMemoryPathCacheResult(
		d.cache.Name(),
		trans.id,
		nil,
		trans.Address(),
		req.GetByteSize(),
		uint64(trans.PID()),
		accessReqOp(req),
		result,
		now,
	)
	memtrace.MarkObservationL1Result(trans.id, result)
	switch result {
	case "read-hit":
		memtrace.ObservationTransition(
			trans.id, "l1_lookup_result", "l1_bank", now)
	case "write-hit", "read-miss", "write-miss":
		memtrace.ObservationTransition(
			trans.id, "l1_lookup_result", "l1_downstream", now)
	}
}

// pgclsLocal records an L1V miss / write sent to a local L2 slice for the
// page classification statistics (remote ones are recorded by the RDMA).
func (d *directory) pgclsLocal(bottom sim.Port, addr, bytes uint64,
	isRead bool, now sim.VTimeInSec) {
	if bottom == nil || strings.Contains(bottom.Name(), "RDMA") {
		return
	}
	memtrace.PgclsAccess(d.cache.Name(), "", "", addr, bytes, isRead, "v", now)
}

// LocalBottomHook, when set (ISCA27 P20 -pm-count-local), is called for every
// L1 miss or write the cache sends to a local L2 slice (not to the RDMA).
// nil (the default) costs nothing.
var LocalBottomHook func(cacheName string, addr uint64, now sim.VTimeInSec)

func (d *directory) localBottom(bottom sim.Port, addr uint64, now sim.VTimeInSec) {
	if bottom == nil || strings.Contains(bottom.Name(), "RDMA") {
		return
	}
	LocalBottomHook(d.cache.Name(), addr, now)
}
