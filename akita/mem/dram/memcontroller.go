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
	subTransSplitter    trans.SubTransSplitter
	addrMapper          addressmapping.Mapper
	subTransactionQueue trans.SubTransactionQueue
	cmdQueue            cmdq.CommandQueue
	channel             org.Channel

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
func (c *MemController) CanAcceptPhysicalAccesses(count int) bool {
	return count > 0 && c.subTransactionQueue.CanPush(count)
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

func (c *MemController) issue(now sim.VTimeInSec) (madeProgress bool) {
	cmd := c.cmdQueue.GetCommandToIssue(now)
	if cmd == nil {
		return false
	}

	c.observePhysicalCommandIssue(now, cmd)
	c.channel.StartCommand(now, cmd)
	c.channel.UpdateTiming(now, cmd)

	return true
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
