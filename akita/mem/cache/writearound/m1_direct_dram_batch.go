package writearound

import (
	"github.com/sarchlab/akita/v3/mem/mem"
	memtrace "github.com/sarchlab/akita/v3/mem/trace"
	"github.com/sarchlab/akita/v3/mem/vm"
	"github.com/sarchlab/akita/v3/sim"
	"github.com/sarchlab/akita/v3/tracing"
)

const m1DirectDRAMAULines = 2

func (c *Cache) ensureM1DirectDRAMState() {
	if c.m1DirectDRAMBatches == nil {
		c.m1DirectDRAMBatches =
			make(map[m1DirectDRAMBatchKey]*m1DirectDRAMBatchEntry)
	}
}

func (c *Cache) enqueueM1DirectDRAMBatch(
	now sim.VTimeInSec,
	trans *transaction,
	cacheLineID uint64,
	dramPort sim.Port,
) bool {
	if trans == nil || dramPort == nil {
		return false
	}

	c.ensureM1DirectDRAMState()
	key := c.m1DirectDRAMKey(trans.PID(), cacheLineID, dramPort)
	entry := c.m1DirectDRAMBatches[key]
	if entry == nil {
		for len(c.m1DirectDRAMBatchOrder) >= c.m1Config.DirectDRAMBatchEntries {
			if !c.processM1DirectDRAMBatches(
				now, true, m1DrainCapacity) {
				return false
			}
		}
		entry = &m1DirectDRAMBatchEntry{
			id:            c.m1NextDirectDRAMBatchID,
			key:           key,
			dramPort:      dramPort,
			oldestArrival: now,
			newestArrival: now,
			lineSet:       make(map[uint64]bool),
		}
		c.m1NextDirectDRAMBatchID++
		c.m1DirectDRAMBatches[key] = entry
		c.m1DirectDRAMBatchOrder = append(c.m1DirectDRAMBatchOrder, key)
		c.m1Stats.L1VDirectDRAMBatchesCreated++
	}

	entry.lineSet[cacheLineID] = true
	entry.transactions = append(entry.transactions, trans)
	entry.newestArrival = now
	trans.m1DirectDRAMBatchArrival = now
	trans.m1DirectDRAMBatchID = entry.id

	return true
}

func (c *Cache) m1DirectDRAMBatchLines() int {
	lines := c.m1Config.DirectDRAMBatchLines
	if lines <= 0 || lines > m1DirectDRAMAULines {
		lines = m1DirectDRAMAULines
	}
	return lines
}

func (c *Cache) m1DirectDRAMKey(
	pid vm.PID,
	cacheLineID uint64,
	dramPort sim.Port,
) m1DirectDRAMBatchKey {
	lineBytes := uint64(1 << c.log2BlockSize)
	auBytes := lineBytes * m1DirectDRAMAULines
	portName := ""
	if dramPort != nil {
		portName = dramPort.Name()
	}
	return m1DirectDRAMBatchKey{
		pid:          pid,
		dramPortName: portName,
		auBase:       cacheLineID / auBytes * auBytes,
	}
}

func (c *Cache) processM1DirectDRAMBatches(
	now sim.VTimeInSec,
	force bool,
	forceReason int,
) bool {
	if !c.m1DirectDramBypassEnabled ||
		len(c.m1DirectDRAMBatchOrder) == 0 {
		return false
	}

	index, reason, ok := c.selectM1DirectDRAMBatch(
		now, force, forceReason)
	if !ok {
		return false
	}
	key := c.m1DirectDRAMBatchOrder[index]
	entry := c.m1DirectDRAMBatches[key]
	if entry == nil || len(entry.transactions) == 0 {
		c.removeM1DirectDRAMBatchAt(index)
		return false
	}

	if !c.issueM1DirectDRAMBatch(now, entry) {
		return false
	}

	c.recordM1DirectDRAMDrain(now, entry, reason)
	c.removeM1DirectDRAMBatchAt(index)
	return true
}

func (c *Cache) selectM1DirectDRAMBatch(
	now sim.VTimeInSec,
	force bool,
	forceReason int,
) (int, int, bool) {
	selected := -1
	reason := forceReason
	for i, key := range c.m1DirectDRAMBatchOrder {
		entry := c.m1DirectDRAMBatches[key]
		if entry == nil {
			continue
		}
		if len(entry.lineSet) >= c.m1DirectDRAMBatchLines() {
			return i, m1DrainFull, true
		}
		if c.m1Expired(
			now, entry.oldestArrival, c.m1Config.DirectDRAMBatchWaitNS) {
			return i, m1DrainTimeout, true
		}
		if selected < 0 {
			selected = i
		}
	}
	if force && selected >= 0 {
		return selected, reason, true
	}
	return 0, 0, false
}

func (c *Cache) issueM1DirectDRAMBatch(
	now sim.VTimeInSec,
	entry *m1DirectDRAMBatchEntry,
) bool {
	if entry == nil || entry.dramPort == nil ||
		c.directDramPort == nil || !c.directDramPort.CanSend() {
		return false
	}

	minAddr, maxAddr := entry.m1AddressRange()
	lineBytes := uint64(1 << c.log2BlockSize)
	spanLines := int((maxAddr-minAddr)/lineBytes) + 1
	if spanLines != len(entry.lineSet) {
		return false
	}

	infos := make([]interface{}, 0, len(entry.transactions))
	for _, trans := range entry.transactions {
		infos = append(infos, accessReqInfo(trans.accessReq()))
	}
	read := mem.ReadReqBuilder{}.
		WithSendTime(now).
		WithSrc(c.directDramPort).
		WithDst(entry.dramPort).
		WithPID(entry.transactions[0].PID()).
		WithAddress(minAddr).
		WithByteSize(uint64(spanLines) * lineBytes).
		WithInfo(memtrace.WithMemoryPathBatchInfo(infos...)).
		Build()

	if err := c.directDramPort.Send(read); err != nil {
		return false
	}

	for _, trans := range entry.transactions {
		cacheLineID := c.m1LineID(trans)
		trans.readToBottom = read
		if mshrEntry := c.mshr.Query(trans.PID(), cacheLineID); mshrEntry != nil {
			mshrEntry.ReadReq = read
		}
		memtrace.RecordMemoryPathL1VBatchWait(
			c.Name(), trans.id, trans.m1DirectDRAMBatchArrival, now)
	}
	c.m1Stats.L1VDirectBypassIssued += uint64(len(entry.transactions))
	if spanLines > 1 {
		c.m1Stats.L1VDirectDRAMMultiLineReads++
	} else {
		c.m1Stats.L1VDirectDRAMSingleLineReads++
	}

	tracing.TraceReqInitiate(read, c, entry.transactions[0].id)
	return true
}

func (c *Cache) recordM1DirectDRAMDrain(
	now sim.VTimeInSec,
	entry *m1DirectDRAMBatchEntry,
	reason int,
) {
	lines := len(entry.lineSet)
	c.m1Stats.L1VDirectDRAMBatchesDrained++
	c.m1Stats.L1VDirectDRAMLinesInBatches += uint64(lines)
	if uint64(lines) > c.m1Stats.L1VDirectDRAMMaxLinesPerBatch {
		c.m1Stats.L1VDirectDRAMMaxLinesPerBatch = uint64(lines)
	}
	c.m1Stats.L1VDirectDRAMTotalWaitNS +=
		m1VTimeToNS(now - entry.oldestArrival)
	c.m1Stats.L1VDirectDRAMWaitSamples++
	if lines <= 1 {
		c.m1Stats.L1VDirectDRAMSingletonFallbacks++
	}

	switch reason {
	case m1DrainFull:
		c.m1Stats.L1VDirectDRAMFullDrains++
	case m1DrainTimeout:
		c.m1Stats.L1VDirectDRAMTimeoutDrains++
	case m1DrainCapacity:
		c.m1Stats.L1VDirectDRAMCapacityDrains++
	default:
		c.m1Stats.L1VDirectDRAMManualDrains++
	}
}

func (c *Cache) removeM1DirectDRAMBatchAt(index int) {
	key := c.m1DirectDRAMBatchOrder[index]
	delete(c.m1DirectDRAMBatches, key)
	c.m1DirectDRAMBatchOrder = append(
		c.m1DirectDRAMBatchOrder[:index],
		c.m1DirectDRAMBatchOrder[index+1:]...)
}

func (entry *m1DirectDRAMBatchEntry) m1AddressRange() (uint64, uint64) {
	var minAddr, maxAddr uint64
	first := true
	for addr := range entry.lineSet {
		if first || addr < minAddr {
			minAddr = addr
		}
		if first || addr > maxAddr {
			maxAddr = addr
		}
		first = false
	}
	return minAddr, maxAddr
}
