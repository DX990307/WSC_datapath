package writearound

import (
	"github.com/sarchlab/akita/v3/mem/cache"
	"github.com/sarchlab/akita/v3/mem/mem"
	memtrace "github.com/sarchlab/akita/v3/mem/trace"
	"github.com/sarchlab/akita/v3/mem/vm"
	"github.com/sarchlab/akita/v3/sim"
	"github.com/sarchlab/akita/v3/tracing"
)

type bottomParser struct {
	cache *Cache
}

func (p *bottomParser) Tick(now sim.VTimeInSec) bool {
	if p.processPort(now, p.cache.bottomPort) {
		return true
	}
	return p.processPort(now, p.cache.directDramPort)
}

func (p *bottomParser) processPort(
	now sim.VTimeInSec,
	port sim.Port,
) bool {
	if port == nil {
		return false
	}
	item := port.Peek()
	if item == nil {
		return false
	}

	switch rsp := item.(type) {
	case *mem.WriteDoneRsp:
		return p.processDoneRsp(now, port, rsp)
	case *mem.DataReadyRsp:
		return p.processDataReady(now, port, rsp)
	case *mem.RemoteDataFill:
		return p.processRemoteDataFill(now, port, rsp)
	default:
		panic("cannot process response")
	}
}

func (p *bottomParser) processDoneRsp(
	now sim.VTimeInSec,
	port sim.Port,
	done *mem.WriteDoneRsp,
) bool {
	trans := p.findTransactionByWriteToBottomID(done.GetRspTo())
	if trans == nil || trans.fetchAndWrite {
		port.Retrieve(now)
		return true
	}

	for _, t := range trans.preCoalesceTransactions {
		t.done = true
	}

	p.removeTransaction(trans)
	port.Retrieve(now)
	p.cache.releaseBottomTransaction(trans)

	tracing.TraceReqFinalize(trans.writeToBottom, p.cache)
	memtrace.RecordMemoryPathL1VBottomResponse(
		p.cache.Name(),
		trans.id,
		done.Meta().ID,
		done.Meta().SendTime,
		now,
		done.Meta().Src,
		done.Meta().Dst,
	)
	recordMemoryPathCacheComplete(p.cache.Name(), trans, now)
	memtrace.RecordMemoryPathL1VMSHRWakeup(p.cache.Name(), trans.id, now)
	tracing.EndTask(trans.id, p.cache)

	return true
}

func (p *bottomParser) processDataReady(
	now sim.VTimeInSec,
	port sim.Port,
	dr *mem.DataReadyRsp,
) bool {
	transactions := p.findTransactionsByReadToBottomID(dr.GetRspTo())
	if len(transactions) == 0 {
		port.Retrieve(now)
		return true
	}
	trans := transactions[0]
	pid := trans.readToBottom.PID
	if trans.remoteDataFill {
		return p.processRemoteDataReady(now, port, dr, trans, pid)
	}
	if trans.directDramBypass {
		return p.processDirectDramDataReady(now, port, dr, transactions)
	}

	bankBuf := p.getBankBuf(trans.block)
	if !bankBuf.CanPush() {
		return false
	}

	addr := trans.Address()
	cachelineID := (addr >> p.cache.log2BlockSize) << p.cache.log2BlockSize
	data := dr.Data
	dirtyMask := make([]bool, 1<<p.cache.log2BlockSize)
	mshrEntry := p.cache.mshr.Query(pid, cachelineID)
	memtrace.RecordMemoryPathL1VBottomResponse(
		p.cache.Name(),
		trans.id,
		dr.Meta().ID,
		dr.Meta().SendTime,
		now,
		dr.Meta().Src,
		dr.Meta().Dst,
	)
	p.mergeMSHRData(mshrEntry, data, dirtyMask)
	p.finalizeMSHRTrans(mshrEntry, data, now)
	p.cache.mshr.Remove(pid, cachelineID)

	trans.bankAction = bankActionWriteFetched
	trans.data = data
	trans.writeFetchedDirtyMask = dirtyMask
	bankBuf.Push(trans)

	p.removeTransaction(trans)
	port.Retrieve(now)
	p.cache.releaseBottomTransaction(trans)

	tracing.TraceReqFinalize(trans.readToBottom, p.cache)

	return true
}

func (p *bottomParser) processDirectDramDataReady(
	now sim.VTimeInSec,
	port sim.Port,
	dr *mem.DataReadyRsp,
	transactions []*transaction,
) bool {
	if !p.canPushDirectDramFetchedLines(transactions) {
		return false
	}

	lineBytes := uint64(1 << p.cache.log2BlockSize)
	for _, trans := range transactions {
		cachelineID := (trans.Address() >> p.cache.log2BlockSize) <<
			p.cache.log2BlockSize
		offset := cachelineID - trans.readToBottom.Address
		if offset+lineBytes > uint64(len(dr.Data)) {
			panic("direct DRAM batch response is smaller than requested line")
		}

		data := append([]byte(nil), dr.Data[offset:offset+lineBytes]...)
		dirtyMask := make([]bool, lineBytes)
		mshrEntry := p.cache.mshr.Query(trans.PID(), cachelineID)
		if mshrEntry == nil {
			panic("direct DRAM batch response without MSHR entry")
		}

		memtrace.RecordMemoryPathM1DirectDRAMResponse(
			p.cache.Name(),
			trans.id,
			trans.readToBottom.Meta().ID,
			dr.Meta().ID,
			trans.readToBottom.Meta().SendTime,
			dr.Meta().SendTime,
			now,
			trans.readToBottom.Meta().Src,
			trans.readToBottom.Meta().Dst,
			dr.Meta().Src,
			dr.Meta().Dst,
		)
		p.mergeMSHRData(mshrEntry, data, dirtyMask)
		memtrace.RecordMemoryPathDataSource(
			p.cache.Name(),
			accessReqInfo(trans.accessReq()),
			cachelineID,
			lineBytes,
			now-trans.readToBottom.SendTime,
			now,
			"read",
			"dram",
		)
		p.cache.m1Stats.L1VDirectBypassResponses++

		p.finalizeMSHRTrans(mshrEntry, data, now)
		p.cache.mshr.Remove(trans.PID(), cachelineID)
		p.sendM1DirectL2Fill(now, trans, data)

		trans.bankAction = bankActionWriteFetched
		trans.data = data
		trans.writeFetchedDirtyMask = dirtyMask
		p.getBankBuf(trans.block).Push(trans)

		p.removeTransaction(trans)
		p.cache.releaseBottomTransaction(trans)
	}

	port.Retrieve(now)
	tracing.TraceReqFinalize(transactions[0].readToBottom, p.cache)
	return true
}

func (p *bottomParser) canPushDirectDramFetchedLines(
	transactions []*transaction,
) bool {
	required := make(map[sim.Buffer]int)
	for _, trans := range transactions {
		bankBuf := p.getBankBuf(trans.block)
		required[bankBuf]++
		if bankBuf.Capacity()-bankBuf.Size() < required[bankBuf] {
			return false
		}
	}
	return true
}

func (p *bottomParser) processRemoteDataReady(
	now sim.VTimeInSec,
	port sim.Port,
	dr *mem.DataReadyRsp,
	trans *transaction,
	pid vm.PID,
) bool {
	addr := trans.Address()
	cachelineID := (addr >> p.cache.log2BlockSize) << p.cache.log2BlockSize
	data := append([]byte(nil), dr.Data...)
	dirtyMask := make([]bool, 1<<p.cache.log2BlockSize)
	mshrEntry := p.cache.mshr.Query(pid, cachelineID)
	memtrace.RecordMemoryPathL1VBottomResponse(
		p.cache.Name(),
		trans.id,
		dr.Meta().ID,
		dr.Meta().SendTime,
		now,
		dr.Meta().Src,
		dr.Meta().Dst,
	)
	p.mergeMSHRData(mshrEntry, data, dirtyMask)
	p.cache.remoteDataCache.fillDemand(pid, cachelineID, data)
	p.finalizeMSHRTrans(mshrEntry, data, now)
	p.cache.mshr.Remove(pid, cachelineID)

	p.removeTransaction(trans)
	port.Retrieve(now)
	p.cache.releaseBottomTransaction(trans)

	tracing.TraceReqFinalize(trans.readToBottom, p.cache)
	return true
}

func (p *bottomParser) processRemoteDataFill(
	now sim.VTimeInSec,
	port sim.Port,
	fill *mem.RemoteDataFill,
) bool {
	blockSize := uint64(1 << p.cache.log2BlockSize)
	cachelineID := fill.Address / blockSize * blockSize
	if p.cache.remoteDataCacheEnabled() &&
		uint64(len(fill.Data)) == blockSize {
		p.cache.remoteDataCache.fillPrefetch(fill.PID, cachelineID, fill.Data)
	} else if p.cache.remoteDataCache != nil {
		p.cache.remoteDataCache.stats.PrefetchFillDrops++
	}
	port.Retrieve(now)
	return true
}

func (p *bottomParser) mergeMSHRData(
	mshrEntry *cache.MSHREntry,
	data []byte,
	dirtyMask []bool,
) {
	for _, t := range mshrEntry.Requests {
		trans := t.(*transaction)

		if trans.write == nil {
			continue
		}

		write := trans.write
		offset := write.Address - mshrEntry.Block.Tag
		for i := 0; i < len(write.Data); i++ {
			if write.DirtyMask[i] {
				data[offset+uint64(i)] = write.Data[i]
				dirtyMask[offset+uint64(i)] = true
			}
		}
	}
}

func (p *bottomParser) finalizeMSHRTrans(
	mshrEntry *cache.MSHREntry,
	data []byte,
	now sim.VTimeInSec,
) {
	for _, t := range mshrEntry.Requests {
		trans := t.(*transaction)
		if trans.read != nil {
			for _, preCTrans := range trans.preCoalesceTransactions {
				read := preCTrans.read
				offset := read.Address - mshrEntry.Block.Tag
				preCTrans.data = data[offset : offset+read.AccessByteSize]
				preCTrans.done = true
			}
		} else {
			for _, preCTrans := range trans.preCoalesceTransactions {
				preCTrans.done = true
			}
		}
		p.removeTransaction(trans)

		recordMemoryPathCacheComplete(p.cache.Name(), trans, now)
		memtrace.RecordMemoryPathL1VMSHRWakeup(p.cache.Name(), trans.id, now)
		tracing.EndTask(trans.id, p.cache)
	}
}

func (p *bottomParser) findTransactionByWriteToBottomID(
	id string,
) *transaction {
	for _, trans := range p.cache.postCoalesceTransactions {
		if trans.writeToBottom != nil && trans.writeToBottom.ID == id {
			return trans
		}
	}
	return nil
}

func (p *bottomParser) findTransactionByReadToBottomID(
	id string,
) *transaction {
	for _, trans := range p.cache.postCoalesceTransactions {
		if trans.readToBottom != nil && trans.readToBottom.ID == id {
			return trans
		}
	}
	return nil
}

func (p *bottomParser) findTransactionsByReadToBottomID(
	id string,
) []*transaction {
	transactions := make([]*transaction, 0, 2)
	for _, trans := range p.cache.postCoalesceTransactions {
		if trans.readToBottom != nil && trans.readToBottom.ID == id {
			transactions = append(transactions, trans)
		}
	}
	return transactions
}

func (p *bottomParser) removeTransaction(trans *transaction) {
	for i, t := range p.cache.postCoalesceTransactions {
		if t == trans {
			p.cache.postCoalesceTransactions = append(
				(p.cache.postCoalesceTransactions)[:i],
				(p.cache.postCoalesceTransactions)[i+1:]...)
			return
		}
	}
}

func (p *bottomParser) getBankBuf(block *cache.Block) sim.Buffer {
	numWaysPerSet := p.cache.wayAssociativity
	blockID := block.SetID*numWaysPerSet + block.WayID
	bankID := blockID % len(p.cache.bankBufs)
	return p.cache.bankBufs[bankID]
}
