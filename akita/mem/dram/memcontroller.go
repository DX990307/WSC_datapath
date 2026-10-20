package dram

import (
	"github.com/sarchlab/akita/v3/mem/dram/internal/addressmapping"
	"github.com/sarchlab/akita/v3/mem/dram/internal/cmdq"
	"github.com/sarchlab/akita/v3/mem/dram/internal/org"
	"github.com/sarchlab/akita/v3/mem/dram/internal/signal"
	"github.com/sarchlab/akita/v3/mem/dram/internal/trans"
	"github.com/sarchlab/akita/v3/mem/mem"
	memtrace "github.com/sarchlab/akita/v3/mem/trace"
	"github.com/sarchlab/akita/v3/sim"
	"github.com/sarchlab/akita/v3/tracing"
)

// Protocol defines the category of the memory controller.
type Protocol int

// A list of all supported DRAM protocols.
const (
	DDR3 Protocol = iota
	DDR4
	GDDR5
	GDDR5X
	GDDR6
	LPDDR
	LPDDR3
	LPDDR4
	HBM
	HBM2
	HMC
)

func (p Protocol) isGDDR() bool {
	return p == GDDR5 || p == GDDR5X || p == GDDR6
}

func (p Protocol) isHBM() bool {
	return p == HBM || p == HBM2
}

// A MemController handles read and write requests.
type MemController struct {
	*sim.TickingComponent

	topPort sim.Port

	storage             *mem.Storage
	addrConverter       mem.AddressConverter
	mappingConverter    mem.AddressConverter
	subTransSplitter    trans.SubTransSplitter
	addrMapper          addressmapping.Mapper
	subTransactionQueue trans.SubTransactionQueue
	cmdQueue            cmdq.CommandQueue
	channel             org.Channel

	// dualCommandBus issues a row and a column command in the same cycle;
	// an activate keeps the row bus busy for activateCommandCycles cycles,
	// until cycle rowBusFreeAt.
	dualCommandBus        bool
	activateCommandCycles int
	rowBusFreeAt          uint64

	// dramModelFeatures records whether any optional DRAM-model feature is
	// on; only then does GetDRAMModelStats report counters.
	dramModelFeatures bool
	dualIssueTicks    uint64
	rowBusBusyTicks   uint64

	inflightTransactions []*signal.Transaction

	physicalObserver         PhysicalDRAMObserver
	physicalIdentity         physicalDRAMIdentity
	physicalAccessBytes      uint64
	physicalReadAccesses     uint64
	physicalWriteAccesses    uint64
	frontEndReadRequests     uint64
	frontEndWriteRequests    uint64
	frontEndReadBytes        uint64
	frontEndWriteBytes       uint64
	pairedReadDescriptors    uint64
	pairedReadMembers        uint64
	pairedReadDemandMembers  uint64
	pairedReadSiblingMembers uint64
	physicalQueuedCommandIDs map[string]string
}

// PhysicalAccessStats counts fixed-size DRAM access units generated after
// request alignment and splitting.
type PhysicalAccessStats struct {
	AccessBytes           uint64
	ReadAccesses          uint64
	WriteAccesses         uint64
	FrontEndReadRequests  uint64
	FrontEndWriteRequests uint64
	FrontEndReadBytes     uint64
	FrontEndWriteBytes    uint64
	PairedReadDescriptors uint64
	PairedReadMembers     uint64
	PairedDemandMembers   uint64
	PairedSiblingMembers  uint64
}

// GetRowContinuationStats returns immediate same-row continuation counters.
func (c *MemController) GetRowContinuationStats() cmdq.RowContinuationStats {
	if queue, ok := c.cmdQueue.(*cmdq.CommandQueueImpl); ok {
		return queue.GetRowContinuationStats()
	}
	return cmdq.RowContinuationStats{}
}

// DRAMModelStats counts the work of the optional DRAM-model features: row
// hold, the dual command bus, all-bank refresh, and the tFAW window. A
// controller that uses none of the features reports the zero value.
type DRAMModelStats struct {
	// Enabled reports whether any DRAM-model feature is on: the mapping
	// interleave strip, an address layout, several channels, the dual
	// command bus, row hold, pipelined column commands, all-bank refresh,
	// tFAW, cross-rank tRRD, or the HBM2 turnaround.
	Enabled bool

	// RowHoldAutoPrechargeStops counts the column commands issued without
	// auto-precharge because their transaction uses the open row again.
	RowHoldAutoPrechargeStops uint64

	// DualIssueTicks counts the cycles in which the dual command bus issued
	// a row and a column command together.
	DualIssueTicks uint64
	// RowBusBusyTicks counts the cycles in which an activate still held the
	// row command bus, so that only a column command could issue.
	RowBusBusyTicks uint64

	// The refresh counters sum the org.RefreshStats of the channels, and
	// RefreshMaxWaitCycles is the longest wait of any channel. A refresh
	// that fell due after the controller's last tick is not counted.
	RefreshCommands         uint64
	RefreshRetroactive      uint64
	RefreshWaitCycles       uint64
	RefreshMaxWaitCycles    uint64
	RefreshForcedPrecharges uint64

	// FAWDeniedActivates counts, over the channels, the queries that found
	// an activate ready but refused it because the tFAW window was full. A
	// waiting activate counts once per query.
	FAWDeniedActivates uint64
}

// GetDRAMModelStats returns the counters of the optional DRAM-model
// features without changing DRAM timing or scheduling.
func (c *MemController) GetDRAMModelStats() DRAMModelStats {
	if !c.dramModelFeatures {
		return DRAMModelStats{}
	}

	stats := DRAMModelStats{
		Enabled: true,
		RowHoldAutoPrechargeStops: c.GetRowContinuationStats().
			RowHoldAutoPrechargeStops,
		DualIssueTicks:  c.dualIssueTicks,
		RowBusBusyTicks: c.rowBusBusyTicks,
	}

	for _, channel := range c.channelImpls() {
		stats.FAWDeniedActivates += channel.FAWDeniedActivates
		if channel.Refresh == nil {
			continue
		}

		refresh := channel.Refresh.Stats
		stats.RefreshCommands += refresh.Commands
		stats.RefreshRetroactive += refresh.Retroactive
		stats.RefreshWaitCycles += refresh.WaitCycles
		if refresh.MaxWaitCycles > stats.RefreshMaxWaitCycles {
			stats.RefreshMaxWaitCycles = refresh.MaxWaitCycles
		}
		stats.RefreshForcedPrecharges += refresh.ForcedPrecharges
	}

	return stats
}

// channelImpls returns the channels of the controller: its only channel, or
// the pseudo channels of a MultiChannel.
func (c *MemController) channelImpls() []*org.ChannelImpl {
	switch channel := c.channel.(type) {
	case *org.ChannelImpl:
		return []*org.ChannelImpl{channel}
	case *org.MultiChannel:
		return channel.Channels
	default:
		return nil
	}
}

// PhysicalAccessBytes returns the bytes transferred by one DRAM column
// command. Requests smaller than this unit still consume one full access.
func (c *MemController) PhysicalAccessBytes() uint64 {
	return c.physicalAccessBytes
}

// GetPhysicalAccessStats returns physical access-unit counts without changing
// DRAM timing or scheduling.
func (c *MemController) GetPhysicalAccessStats() PhysicalAccessStats {
	return PhysicalAccessStats{
		AccessBytes:           c.physicalAccessBytes,
		ReadAccesses:          c.physicalReadAccesses,
		WriteAccesses:         c.physicalWriteAccesses,
		FrontEndReadRequests:  c.frontEndReadRequests,
		FrontEndWriteRequests: c.frontEndWriteRequests,
		FrontEndReadBytes:     c.frontEndReadBytes,
		FrontEndWriteBytes:    c.frontEndWriteBytes,
		PairedReadDescriptors: c.pairedReadDescriptors,
		PairedReadMembers:     c.pairedReadMembers,
		PairedDemandMembers:   c.pairedReadDemandMembers,
		PairedSiblingMembers:  c.pairedReadSiblingMembers,
	}
}

// CanAcceptPhysicalAccesses exposes a read-only, best-effort admission hint
// to an upstream paired-read producer. Correctness never depends on this
// hint: a later race is still handled by the controller's normal backpressure.
// The count is in 64-B lines; an access unit smaller than 64 B needs several
// sub-transactions per line.
func (c *MemController) CanAcceptPhysicalAccesses(count int) bool {
	return count > 0 &&
		c.subTransactionQueue.CanPush(count*c.physicalAccessesPerLine())
}

// physicalAccessesPerLine returns the number of access units that one 64-B
// line occupies: 1 for 64-B and 128-B units, 2 for 32-B units.
func (c *MemController) physicalAccessesPerLine() int {
	if c.physicalAccessBytes == 0 || c.physicalAccessBytes >= 64 {
		return 1
	}

	return int(64 / c.physicalAccessBytes)
}

// Tick updates memory controller's internal state.
func (c *MemController) Tick(now sim.VTimeInSec) (madeProgress bool) {
	madeProgress = c.respond(now) || madeProgress
	madeProgress = c.respond(now) || madeProgress
	madeProgress = c.channel.Tick(now) || madeProgress
	madeProgress = c.issue(now) || madeProgress
	madeProgress = c.subTransactionQueue.Tick(now) || madeProgress
	madeProgress = c.parseTop(now) || madeProgress
	return madeProgress
}

func (c *MemController) parseTop(now sim.VTimeInSec) (madeProgress bool) {
	msg := c.topPort.Peek()
	if msg == nil {
		return false
	}

	transactions := make([]*signal.Transaction, 0, 2)
	switch msg := msg.(type) {
	case *mem.ReadReq:
		transactions = append(transactions, &signal.Transaction{Read: msg})
	case *mem.PairedReadReq:
		// The transport descriptor makes both independent children visible in
		// one admission step. Their IDs and responses remain unrelated.
		msg.Demand.SendTime = msg.SendTime
		msg.Sibling.SendTime = msg.SendTime
		transactions = append(transactions,
			&signal.Transaction{Read: msg.Demand},
			&signal.Transaction{Read: msg.Sibling})
	case *mem.WriteReq:
		transactions = append(transactions, &signal.Transaction{Write: msg})
	default:
		panic("unsupported memory-controller request")
	}

	totalPhysicalAccesses := 0
	for _, trans := range transactions {
		c.assignTransInternalAddress(trans)
		c.subTransSplitter.Split(trans)
		c.convertSubTransMappingAddress(trans)
		totalPhysicalAccesses += len(trans.SubTransactions)
	}
	if !c.subTransactionQueue.CanPush(totalPhysicalAccesses) {
		return false
	}

	for _, trans := range transactions {
		physicalAccesses := uint64(len(trans.SubTransactions))
		if trans.IsRead() {
			c.physicalReadAccesses += physicalAccesses
			c.frontEndReadRequests++
			c.frontEndReadBytes += trans.AccessByteSize()
			if trans.Read.PairedReadID != "" {
				c.pairedReadMembers++
				switch trans.Read.PairedReadPart {
				case mem.PairedReadDemand:
					c.pairedReadDescriptors++
					c.pairedReadDemandMembers++
				case mem.PairedReadSibling:
					c.pairedReadSiblingMembers++
				}
			}
		} else {
			c.physicalWriteAccesses += physicalAccesses
			c.frontEndWriteRequests++
			c.frontEndWriteBytes += trans.AccessByteSize()
		}

		c.subTransactionQueue.Push(trans)
		c.inflightTransactions = append(c.inflightTransactions, trans)

		if c.physicalObserver != nil {
			for _, st := range trans.SubTransactions {
				e := c.makePhysicalDRAMEvent(
					PhysicalDRAMSubtransactionArrive, now, st)
				e.SubtransactionQueueDepth =
					c.physicalSubtransactionQueueDepth()
				c.emitPhysicalDRAM(e)
			}
		}

		requestMsg := transactionRequestMessage(trans)
		tracing.TraceReqReceive(requestMsg, c)
		if req, ok := requestMsg.(mem.AccessReq); ok {
			memtrace.RecordMemoryPathDRAMRequestReceive(
				c.Name(),
				dramAccessReqInfo(req),
				req.Meta().ID,
				req.Meta().SendTime,
				now,
				req.Meta().Src,
				req.Meta().Dst,
			)
		}
		for _, st := range trans.SubTransactions {
			tracing.StartTaskWithSpecificLocation(
				st.ID,
				tracing.MsgIDAtReceiver(requestMsg, c),
				c,
				"sub-trans",
				"sub-trans",
				c.Name()+".SubTransQueue",
				nil,
			)
		}
	}
	c.topPort.Retrieve(now)

	// fmt.Printf("%.10f, %s, start transaction, %s, %x\n",
	// 	now, c.Name(), msg.Meta().ID, trans.InternalAddress)
	return true
}

func transactionRequestMessage(trans *signal.Transaction) sim.Msg {
	if trans.Read != nil {
		return trans.Read
	}
	return trans.Write
}

func (c *MemController) assignTransInternalAddress(trans *signal.Transaction) {
	if c.addrConverter != nil {
		trans.InternalAddress = c.addrConverter.ConvertExternalToInternal(
			trans.GlobalAddress())
		return
	}

	trans.InternalAddress = trans.GlobalAddress()
}

// convertSubTransMappingAddress rewrites the sub-transaction addresses into
// the domain of the DRAM address mapper. The transaction's InternalAddress,
// and therefore the storage access, is left unchanged.
func (c *MemController) convertSubTransMappingAddress(
	trans *signal.Transaction,
) {
	if c.mappingConverter == nil {
		return
	}

	for _, st := range trans.SubTransactions {
		st.Address = c.mappingConverter.ConvertExternalToInternal(st.Address)
	}
}

func (c *MemController) issue(now sim.VTimeInSec) (madeProgress bool) {
	if c.dualCommandBus {
		return c.issueOnDualCommandBus(now)
	}

	cmd := c.cmdQueue.GetCommandToIssue(now)
	if cmd == nil {
		return false
	}

	c.startCommand(now, cmd)

	return true
}

func (c *MemController) startCommand(now sim.VTimeInSec, cmd *signal.Command) {
	c.observePhysicalCommandIssue(now, cmd)
	c.channel.StartCommand(now, cmd)
	c.channel.UpdateTiming(now, cmd)
}

// busAwareQueue is a command queue that can restrict its selection to the
// commands of one command bus.
type busAwareQueue interface {
	GetCommandToIssueOn(now sim.VTimeInSec, bus cmdq.Bus) *signal.Command
}

var _ busAwareQueue = (*cmdq.CommandQueueImpl)(nil)

// issueOnDualCommandBus issues up to one row command and one column command
// in the same cycle. The second command is selected after the first one has
// started, so it sees the first command's effect on banks and timing.
func (c *MemController) issueOnDualCommandBus(
	now sim.VTimeInSec,
) (madeProgress bool) {
	queue, ok := c.cmdQueue.(busAwareQueue)
	if !ok {
		panic("the dual command bus needs a bus-aware command queue")
	}

	nowCycle := c.Freq.Cycle(now)
	rowBusFree := nowCycle >= c.rowBusFreeAt
	first := cmdq.AnyBus
	if !rowBusFree {
		c.rowBusBusyTicks++
		first = cmdq.ColumnBus
	}

	cmd := queue.GetCommandToIssueOn(now, first)
	if cmd == nil {
		// Keep ticking while an activate holds the row bus, so that a
		// waiting row command is not left to a sleeping ticker.
		return !rowBusFree
	}
	c.startCommand(now, cmd)
	c.occupyRowBus(nowCycle, cmd)

	second := cmdq.ColumnBus
	if cmdq.BusOf(cmd.Kind) == cmdq.ColumnBus {
		if !rowBusFree {
			return true
		}
		second = cmdq.RowBus
	}

	if cmd2 := queue.GetCommandToIssueOn(now, second); cmd2 != nil {
		c.startCommand(now, cmd2)
		c.occupyRowBus(nowCycle, cmd2)
		c.dualIssueTicks++
	}

	return true
}

func (c *MemController) occupyRowBus(nowCycle uint64, cmd *signal.Command) {
	switch {
	case cmdq.BusOf(cmd.Kind) != cmdq.RowBus:
	case cmd.Kind == signal.CmdKindActivate:
		c.rowBusFreeAt = nowCycle + uint64(c.activateCommandCycles)
	default:
		c.rowBusFreeAt = nowCycle + 1
	}
}

func (c *MemController) respond(now sim.VTimeInSec) (madeProgress bool) {
	for i, t := range c.inflightTransactions {
		if t.IsCompleted() {
			done := c.finalizeTransaction(now, t, i)
			if done {
				return true
			}
		}
	}

	return false
}

func (c *MemController) finalizeTransaction(
	now sim.VTimeInSec,
	t *signal.Transaction,
	i int,
) (done bool) {
	if t.Write != nil {
		done = c.finalizeWriteTrans(now, t, i)
		if done {
			tracing.TraceReqComplete(t.Write, c)
		}
	} else {
		done = c.finalizeReadTrans(now, t, i)
		if done {
			tracing.TraceReqComplete(t.Read, c)
		}
	}

	return done
}

func (c *MemController) finalizeWriteTrans(
	now sim.VTimeInSec,
	t *signal.Transaction,
	i int,
) (done bool) {
	err := c.storage.Write(t.InternalAddress, t.Write.Data)
	if err != nil {
		panic(err)
	}

	writeDone := mem.WriteDoneRspBuilder{}.
		WithSrc(c.topPort).
		WithDst(t.Write.Src).
		WithRspTo(t.Write.ID).
		WithSendTime(now).
		Build()
	sendErr := c.topPort.Send(writeDone)
	if sendErr == nil {
		c.observePhysicalTransactionComplete(now, t)
		memtrace.RecordMemoryPathDRAMResponse(
			c.Name(),
			dramAccessReqInfo(t.Write),
			t.Write.Meta().ID,
			writeDone.Meta().ID,
			now,
		)
		c.inflightTransactions = append(
			c.inflightTransactions[:i],
			c.inflightTransactions[i+1:]...)

		// fmt.Printf("%.10f, %s, finish transaction %s, %x\n",
		// 	now, c.Name(), t.Write.ID, t.InternalAddress)
		return true
	}

	return false
}

func (c *MemController) finalizeReadTrans(
	now sim.VTimeInSec,
	t *signal.Transaction,
	i int,
) (done bool) {
	data, err := c.storage.Read(t.InternalAddress, t.Read.AccessByteSize)
	if err != nil {
		panic(err)
	}

	dataReady := mem.DataReadyRspBuilder{}.
		WithSrc(c.topPort).
		WithDst(t.Read.Src).
		WithData(data).
		WithRspTo(t.Read.ID).
		WithSendTime(now).
		Build()
	sendErr := c.topPort.Send(dataReady)
	if sendErr == nil {
		c.observePhysicalTransactionComplete(now, t)
		memtrace.RecordMemoryPathDRAMResponse(
			c.Name(),
			dramAccessReqInfo(t.Read),
			t.Read.Meta().ID,
			dataReady.Meta().ID,
			now,
		)
		c.inflightTransactions = append(
			c.inflightTransactions[:i],
			c.inflightTransactions[i+1:]...)

		// fmt.Printf("%.10f, %s, finish transaction %s, %x\n",
		// 	now, c.Name(), t.Read.ID, t.InternalAddress)
		return true
	}

	return false
}

func dramAccessReqInfo(req mem.AccessReq) interface{} {
	switch req := req.(type) {
	case *mem.ReadReq:
		return req.Info
	case *mem.WriteReq:
		return req.Info
	default:
		return nil
	}
}
