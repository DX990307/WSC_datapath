package dram

import (
	"math"
	"strings"

	"github.com/sarchlab/akita/v3/mem/dram/internal/signal"
	"github.com/sarchlab/akita/v3/sim"
)

// PhysicalDRAMEventKind identifies a lifecycle boundary of a physical DRAM
// access.  Physical observers are intentionally independent of Akita's task
// tracing infrastructure: enabling an observer must not change request IDs,
// scheduling decisions, or memory-path attribution.
type PhysicalDRAMEventKind string

const (
	// PhysicalDRAMSubtransactionArrive is emitted after address conversion and
	// splitting, after the subtransaction is accepted by the controller queue.
	PhysicalDRAMSubtransactionArrive PhysicalDRAMEventKind = "subtransaction_arrive"
	// PhysicalDRAMCommandEnqueue is emitted when the column command enters the
	// command queue.
	PhysicalDRAMCommandEnqueue PhysicalDRAMEventKind = "command_enqueue"
	// PhysicalDRAMCommandIssue is emitted for every ACT/PRE/column command that
	// is sent to a bank.
	PhysicalDRAMCommandIssue PhysicalDRAMEventKind = "command_issue"
	// PhysicalDRAMCommandComplete is emitted when a bank completes a command.
	PhysicalDRAMCommandComplete PhysicalDRAMEventKind = "command_complete"
	// PhysicalDRAMTransactionComplete is emitted only after the completed
	// transaction response is accepted by the controller's top port.
	PhysicalDRAMTransactionComplete PhysicalDRAMEventKind = "transaction_complete"
)

// PhysicalDRAMObserver consumes physical DRAM lifecycle events. Implementations
// must not retain or mutate simulator-internal requests; PhysicalDRAMEvent is a
// value-only snapshot for this reason.
type PhysicalDRAMObserver interface {
	ObservePhysicalDRAM(event PhysicalDRAMEvent)
}

// PhysicalDRAMEvent is a value-only snapshot of one physical-access lifecycle
// boundary. Addresses are deliberately kept in all domains:
//
//   - ExternalAddress is the address on the controller's top port.
//   - InternalAddress is the converted storage address of that request.
//   - SubtransactionAddress is the aligned address actually passed to the
//     DRAM address mapper.
//   - InternalSubtransactionAddress is the converted form of that aligned
//     address.
//
// The Location fields are copied directly from the command/address mapper and
// must be preferred over any offline reconstruction of the DRAM geometry.
type PhysicalDRAMEvent struct {
	Kind PhysicalDRAMEventKind

	Owner          string
	ControllerName string
	Controller     int

	TimePS uint64
	Cycle  uint64

	RequestID        string
	SubtransactionID string
	CommandID        string
	QueuedCommandID  string
	CommandKind      string
	IsRead           bool

	ExternalAddress               uint64
	InternalAddress               uint64
	SubtransactionAddress         uint64
	InternalSubtransactionAddress uint64
	RequestBytes                  uint64
	PhysicalBytes                 uint64

	Channel   uint64
	Rank      uint64
	BankGroup uint64
	Bank      uint64
	Row       uint64
	Column    uint64

	QueueDepthBefore         int
	QueueDepthAfter          int
	ControllerQueueDepth     int
	SubtransactionQueueDepth int
	OpenRowValid             bool
	OpenRow                  uint64
	ReadySameOpenRow         int
	ColumnRowHit             bool
}

type physicalDRAMIdentity struct {
	owner      string
	controller int
}

// SetPhysicalDRAMObserver attaches a passive observer to this controller.
// owner and controller are explicit because component names are configuration
// details. Passing an empty owner falls back to the prefix before ".DRAM[".
// Passing nil disables observation.
func (c *MemController) SetPhysicalDRAMObserver(
	observer PhysicalDRAMObserver,
	owner string,
	controller int,
) {
	c.physicalObserver = observer
	if owner == "" {
		owner = inferPhysicalDRAMOwner(c.Name())
	}
	c.physicalIdentity = physicalDRAMIdentity{
		owner:      owner,
		controller: controller,
	}
	if observer == nil {
		c.physicalQueuedCommandIDs = nil
	} else {
		c.physicalQueuedCommandIDs = make(map[string]string)
	}
	c.installPhysicalObserverHooks()
}

func inferPhysicalDRAMOwner(controllerName string) string {
	if i := strings.Index(controllerName, ".DRAM["); i >= 0 {
		return controllerName[:i]
	}
	return controllerName
}

func physicalDRAMTimePS(now sim.VTimeInSec) uint64 {
	return uint64(math.Round(float64(now) * 1e12))
}

func (c *MemController) makePhysicalDRAMEvent(
	kind PhysicalDRAMEventKind,
	now sim.VTimeInSec,
	st *signal.SubTransaction,
) PhysicalDRAMEvent {
	e := PhysicalDRAMEvent{
		Kind:           kind,
		Owner:          c.physicalIdentity.owner,
		ControllerName: c.Name(),
		Controller:     c.physicalIdentity.controller,
		TimePS:         physicalDRAMTimePS(now),
		Cycle:          c.Freq.Cycle(now),
	}
	if st == nil || st.Transaction == nil {
		return e
	}

	t := st.Transaction
	e.RequestID = physicalDRAMRequestID(t)
	e.SubtransactionID = st.ID
	e.IsRead = t.IsRead()
	e.ExternalAddress = t.GlobalAddress()
	e.InternalAddress = t.InternalAddress
	e.SubtransactionAddress = st.Address
	e.InternalSubtransactionAddress = c.internalPhysicalAddress(st.Address)
	e.RequestBytes = t.AccessByteSize()
	e.PhysicalBytes = c.physicalAccessBytes
	location := c.addrMapper.Map(st.Address)
	e.Channel = location.Channel
	e.Rank = location.Rank
	e.BankGroup = location.BankGroup
	e.Bank = location.Bank
	e.Row = location.Row
	e.Column = location.Column
	return e
}

func physicalDRAMRequestID(t *signal.Transaction) string {
	if t == nil {
		return ""
	}
	if t.Read != nil {
		return t.Read.Meta().ID
	}
	if t.Write != nil {
		return t.Write.Meta().ID
	}
	return ""
}

func (c *MemController) internalPhysicalAddress(external uint64) uint64 {
	if c.addrConverter == nil {
		return external
	}
	return c.addrConverter.ConvertExternalToInternal(external)
}

func (c *MemController) emitPhysicalDRAM(event PhysicalDRAMEvent) {
	if c.physicalObserver != nil {
		c.physicalObserver.ObservePhysicalDRAM(event)
	}
}
