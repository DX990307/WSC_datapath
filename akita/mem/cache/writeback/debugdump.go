package writeback

import (
	"fmt"
	"strings"

	"github.com/sarchlab/akita/v3/mem/mem"
	"github.com/sarchlab/akita/v3/sim"
)

// DebugDump (diagnostic) describes the cache's pipeline state: buffer
// levels, the head of the directory stage, MSHR entries and the write
// buffer's fetches and evictions.
func (c *Cache) DebugDump() string {
	var b strings.Builder
	lvl := func(x sim.Buffer) string { return fmt.Sprintf("%d/%d", x.Size(), x.Capacity()) }
	nb := func(p sim.Port) int {
		if l, ok := p.(*sim.LimitNumMsgPort); ok {
			return l.NumBuffered()
		}
		return -1
	}
	fmt.Fprintf(&b, "%s state=%d inflight=%d evicting=%d dirBuf=%s mshrBuf=%s wbBuf=%s topBuf=%d bottomBuf=%d topCanSend=%v bottomCanSend=%v mshr=%d/%d",
		c.Name(), c.state, len(c.inFlightTransactions), len(c.evictingList), lvl(c.dirStageBuffer),
		lvl(c.mshrStageBuffer), lvl(c.writeBufferBuffer), nb(c.topPort), nb(c.bottomPort),
		c.topSender.CanSend(1), c.bottomSender.CanSend(1), len(c.mshr.AllEntries()), c.mshrCapacity)
	for i, x := range c.dirToBankBuffers {
		fmt.Fprintf(&b, " dir2bank%d=%s", i, lvl(x))
	}
	for i, x := range c.writeBufferToBankBuffers {
		fmt.Fprintf(&b, " wb2bank%d=%s", i, lvl(x))
	}
	for i, bs := range c.bankStages {
		fmt.Fprintf(&b, " bank%d(inflight=%d down=%d post=%d)", i, bs.inflightTransCount,
			bs.downwardInflightTransCount, bs.postPipelineBuf.Size())
	}
	wb := c.writeBuffer
	fmt.Fprintf(&b, " wb(pendEvict=%d inflFetch=%d/%d inflEvict=%d/%d cap=%d)\n", len(wb.pendingEvictions),
		len(wb.inflightFetch), wb.maxInflightFetch, len(wb.inflightEviction), wb.maxInflightEviction,
		wb.writeBufferCapacity)
	desc := func(t *transaction) string {
		r := t.accessReq()
		kind := "W"
		if _, ok := r.(*mem.ReadReq); ok {
			kind = "R"
		}
		src := ""
		if r != nil && r.Meta().Src != nil {
			src = r.Meta().Src.Name()
		}
		line, _ := getCacheLineID(r.GetAddress(), c.log2BlockSize)
		return fmt.Sprintf("%s addr=%#x pid=%d src=%s action=%d evicting=%v fetch=%#x fetchSent=%v",
			kind, r.GetAddress(), r.GetPID(), src, t.action, c.evictingList[line], t.fetchAddress, t.fetchReadReq != nil)
	}
	if it := c.dirStageBuffer.Peek(); it != nil {
		fmt.Fprintf(&b, "  dirHead %s\n", desc(it.(dirPipelineItem).trans))
	}
	if it := c.writeBufferBuffer.Peek(); it != nil {
		fmt.Fprintf(&b, "  wbHead %s\n", desc(it.(*transaction)))
	}
	for i, e := range c.mshr.AllEntries() {
		if i >= 8 {
			break
		}
		fmt.Fprintf(&b, "  mshr addr=%#x pid=%d reqs=%d readSent=%v\n", e.Address, e.PID, len(e.Requests), e.ReadReq != nil)
	}
	for i, t := range wb.inflightFetch {
		if i >= 4 {
			break
		}
		fmt.Fprintf(&b, "  inflFetch %s\n", desc(t))
	}
	return b.String()
}
