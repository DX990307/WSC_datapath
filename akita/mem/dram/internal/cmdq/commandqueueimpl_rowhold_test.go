package cmdq

import (
	"github.com/golang/mock/gomock"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/sarchlab/akita/v3/mem/dram/internal/addressmapping"
	"github.com/sarchlab/akita/v3/mem/dram/internal/org"
	"github.com/sarchlab/akita/v3/mem/dram/internal/signal"
	"github.com/sarchlab/akita/v3/mem/mem"
	"github.com/sarchlab/akita/v3/sim"
)

// rowHoldChannel is a mock channel that also answers the non-cloning
// column-readiness probe, counting the probes, and the refresh query.
type rowHoldChannel struct {
	*MockChannel
	ready   map[string]bool
	probes  map[string]int
	refresh bool
}

func (c *rowHoldChannel) ObservationColumnReady(cmd *signal.Command) bool {
	c.probes[cmd.ID]++
	return c.ready[cmd.ID]
}

func (c *rowHoldChannel) RefreshPending(uint64) bool {
	return c.refresh
}

var (
	_ org.ColumnReadyObserver   = (*rowHoldChannel)(nil)
	_ org.RefreshPendingQuerier = (*rowHoldChannel)(nil)
)

var _ = Describe("CommandQueueImpl with row hold", func() {
	var (
		mockCtrl *gomock.Controller
		mock     *MockChannel
		channel  *rowHoldChannel
		q        CommandQueueImpl
	)

	read := func() *signal.Transaction {
		return &signal.Transaction{
			Read: mem.ReadReqBuilder{}.WithByteSize(64).Build(),
		}
	}

	pairedRead := func(part mem.PairedReadPart) *signal.Transaction {
		return &signal.Transaction{
			Read: mem.ReadReqBuilder{}.
				WithByteSize(64).
				WithPairedRead("pair", part).
				Build(),
		}
	}

	// unit makes the auto-precharge read command of one access unit of t.
	unit := func(
		t *signal.Transaction,
		id string,
		rank, bank, row, column uint64,
	) *signal.Command {
		st := &signal.SubTransaction{ID: id + ".st", Transaction: t}
		t.SubTransactions = append(t.SubTransactions, st)
		return &signal.Command{
			ID:       id,
			Kind:     signal.CmdKindReadPrecharge,
			SubTrans: st,
			Location: addressmapping.Location{
				Rank: rank, Bank: bank, Row: row, Column: column,
			},
		}
	}

	readyAt := func(
		now sim.VTimeInSec,
		cmd *signal.Command,
		kind signal.CommandKind,
	) {
		mock.EXPECT().GetReadyCommand(now, cmd).Return(readyAs(cmd, kind))
	}

	notReadyAt := func(now sim.VTimeInSec, cmd *signal.Command) {
		mock.EXPECT().GetReadyCommand(now, cmd).Return(nil)
	}

	bank2 := bankKey{bank: 2}

	BeforeEach(func() {
		mockCtrl = gomock.NewController(GinkgoT())
		mock = NewMockChannel(mockCtrl)
		channel = &rowHoldChannel{
			MockChannel: mock,
			ready:       map[string]bool{},
			probes:      map[string]int{},
		}
		q = CommandQueueImpl{
			Queues:           make([]Queue, 1),
			CapacityPerQueue: 8,
			Channel:          channel,
			RowHold:          true,
		}
	})

	AfterEach(func() {
		mockCtrl.Finish()
	})

	It("should issue the first of two same-transaction commands as RD and "+
		"the second as RDA", func() {
		a := read()
		a0 := unit(a, "A0", 0, 2, 7, 0)
		a1 := unit(a, "A1", 0, 2, 7, 1)
		q.Queues[0] = Queue{a0, a1}
		readyAt(10, a0, signal.CmdKindReadPrecharge)
		readyAt(10, a1, signal.CmdKindReadPrecharge)

		issued := q.GetCommandToIssue(10)

		Expect(issued.Kind).To(Equal(signal.CmdKindRead))
		Expect(issued.SubTrans).To(BeIdenticalTo(a0.SubTrans))
		Expect(q.Queues[0]).To(ConsistOf(a1))
		Expect(q.held).To(HaveKeyWithValue(bank2, a))

		readyAt(11, a1, signal.CmdKindReadPrecharge)
		issued = q.GetCommandToIssue(11)

		Expect(issued.Kind).To(Equal(signal.CmdKindReadPrecharge))
		Expect(issued.SubTrans).To(BeIdenticalTo(a1.SubTrans))
		Expect(q.Queues[0]).To(BeEmpty())
		Expect(q.held).To(BeEmpty())
		stats := q.GetRowContinuationStats()
		Expect(stats.Enabled).To(BeFalse())
		Expect(stats.RowHoldEnabled).To(BeTrue())
		Expect(stats.RowHoldAutoPrechargeStops).To(Equal(uint64(1)))
		Expect(stats.AutoPrechargeStops).To(BeZero())
		Expect(stats.ColumnCommands).To(Equal(uint64(2)))
	})

	It("should issue a single sub-transaction as RDA directly", func() {
		a0 := unit(read(), "A0", 0, 2, 7, 0)
		q.Queues[0] = Queue{a0}
		readyAt(10, a0, signal.CmdKindReadPrecharge)

		issued := q.GetCommandToIssue(10)

		Expect(issued.Kind).To(Equal(signal.CmdKindReadPrecharge))
		Expect(q.held).To(BeEmpty())
		Expect(q.GetRowContinuationStats().RowHoldAutoPrechargeStops).
			To(BeZero())
	})

	It("should reserve the bank from the activate on", func() {
		a, b := read(), read()
		a0 := unit(a, "A0", 0, 2, 7, 0)
		a1 := unit(a, "A1", 0, 2, 7, 1)
		b0 := unit(b, "B0", 0, 2, 7, 2)
		q.Queues[0] = Queue{a0, a1, b0}
		readyAt(10, a0, signal.CmdKindActivate)
		readyAt(10, a1, signal.CmdKindActivate)
		readyAt(10, b0, signal.CmdKindActivate)

		issued := q.GetCommandToIssue(10)

		Expect(issued.Kind).To(Equal(signal.CmdKindActivate))
		Expect(q.Queues[0]).To(ConsistOf(a0, a1, b0))
		Expect(q.held).To(HaveKeyWithValue(bank2, a))

		// B's row hit is not scanned while A waits for tRCD.
		notReadyAt(11, a0)
		notReadyAt(11, a1)
		Expect(q.GetCommandToIssue(11)).To(BeNil())

		readyAt(12, a0, signal.CmdKindReadPrecharge)
		readyAt(12, a1, signal.CmdKindReadPrecharge)
		Expect(q.GetCommandToIssue(12).Kind).To(Equal(signal.CmdKindRead))

		readyAt(13, a1, signal.CmdKindReadPrecharge)
		Expect(q.GetCommandToIssue(13).Kind).
			To(Equal(signal.CmdKindReadPrecharge))
		Expect(q.held).To(BeEmpty())

		readyAt(14, b0, signal.CmdKindActivate)
		Expect(q.GetCommandToIssue(14).SubTrans).To(BeIdenticalTo(b0.SubTrans))
	})

	It("should reject another transaction's PRE until the holder's RDA "+
		"issues", func() {
		a := read()
		a0 := unit(a, "A0", 0, 2, 7, 0)
		a1 := unit(a, "A1", 0, 2, 7, 1)
		c0 := unit(read(), "C0", 0, 2, 8, 0)
		q.Queues[0] = Queue{a0, a1, c0}
		readyAt(10, a0, signal.CmdKindReadPrecharge)
		readyAt(10, a1, signal.CmdKindReadPrecharge)
		readyAt(10, c0, signal.CmdKindPrecharge)
		Expect(q.GetCommandToIssue(10).Kind).To(Equal(signal.CmdKindRead))

		// The mock fails on any GetReadyCommand for C0 at 11 and 12.
		notReadyAt(11, a1)
		Expect(q.GetCommandToIssue(11)).To(BeNil())

		readyAt(12, a1, signal.CmdKindReadPrecharge)
		Expect(q.GetCommandToIssue(12).Kind).
			To(Equal(signal.CmdKindReadPrecharge))

		readyAt(13, c0, signal.CmdKindActivate)
		issued := q.GetCommandToIssue(13)
		Expect(issued.Kind).To(Equal(signal.CmdKindActivate))
		Expect(issued.SubTrans).To(BeIdenticalTo(c0.SubTrans))
	})

	It("should prefer the holder's ready column command over an older "+
		"ready command", func() {
		q.Queues = make([]Queue, 2)
		q.nextQueueIndex = 1
		d0 := unit(read(), "D0", 0, 1, 3, 0)
		a := read()
		a0 := unit(a, "A0", 1, 2, 7, 0)
		a1 := unit(a, "A1", 1, 2, 7, 1)
		q.Queues[0] = Queue{d0}
		q.Queues[1] = Queue{a0, a1}
		readyAt(10, a0, signal.CmdKindReadPrecharge)
		readyAt(10, a1, signal.CmdKindReadPrecharge)
		readyAt(10, d0, signal.CmdKindActivate)
		Expect(q.GetCommandToIssue(10).SubTrans).To(BeIdenticalTo(a0.SubTrans))
		Expect(q.nextQueueIndex).To(Equal(0))

		readyAt(11, d0, signal.CmdKindActivate)
		readyAt(11, a1, signal.CmdKindReadPrecharge)
		issued := q.GetCommandToIssue(11)

		Expect(issued.SubTrans).To(BeIdenticalTo(a1.SubTrans))
		Expect(issued.Kind).To(Equal(signal.CmdKindReadPrecharge))
		Expect(q.Queues[0]).To(ConsistOf(d0))
		Expect(q.nextQueueIndex).To(Equal(0))
	})

	It("should not prefer the holder's column command on the row bus",
		func() {
			q.Queues = make([]Queue, 2)
			d0 := unit(read(), "D0", 1, 1, 3, 0)
			a := read()
			a0 := unit(a, "A0", 0, 2, 7, 0)
			a1 := unit(a, "A1", 0, 2, 7, 1)
			q.Queues[0] = Queue{a0, a1}
			q.Queues[1] = Queue{d0}
			readyAt(10, a0, signal.CmdKindReadPrecharge)
			readyAt(10, a1, signal.CmdKindReadPrecharge)
			readyAt(10, d0, signal.CmdKindActivate)
			Expect(q.GetCommandToIssueOn(10, ColumnBus).Kind).
				To(Equal(signal.CmdKindRead))

			readyAt(11, d0, signal.CmdKindActivate)
			readyAt(11, a1, signal.CmdKindReadPrecharge)
			issued := q.GetCommandToIssueOn(11, RowBus)

			Expect(issued.Kind).To(Equal(signal.CmdKindActivate))
			Expect(issued.SubTrans).To(BeIdenticalTo(d0.SubTrans))
			Expect(q.held).To(HaveLen(2))
		})

	It("should hand the row over with row continuation", func() {
		q.Queues = make([]Queue, 2)
		q.RowContinuation = true
		a, b := read(), read()
		a0 := unit(a, "A0", 0, 2, 7, 0)
		a1 := unit(a, "A1", 0, 2, 7, 1)
		b0 := unit(b, "B0", 0, 2, 7, 4)
		b1 := unit(b, "B1", 0, 2, 7, 5)
		c0 := unit(read(), "C0", 0, 2, 8, 0)
		d0 := unit(read(), "D0", 1, 1, 3, 0)
		q.Queues[0] = Queue{a0, a1, b0, b1, c0}
		q.Queues[1] = Queue{d0}
		channel.ready["B0"] = true

		readyAt(10, a0, signal.CmdKindReadPrecharge)
		readyAt(10, a1, signal.CmdKindReadPrecharge)
		readyAt(10, b0, signal.CmdKindReadPrecharge)
		readyAt(10, b1, signal.CmdKindReadPrecharge)
		readyAt(10, c0, signal.CmdKindPrecharge)
		readyAt(10, d0, signal.CmdKindActivate)
		Expect(q.GetCommandToIssue(10).Kind).To(Equal(signal.CmdKindRead))

		// A's last access hands the row to B. B is probed but never
		// cloned: the mock fails on GetReadyCommand for B0, B1 or C0.
		readyAt(11, d0, signal.CmdKindActivate)
		readyAt(11, a1, signal.CmdKindReadPrecharge)
		issued := q.GetCommandToIssue(11)

		Expect(issued.SubTrans).To(BeIdenticalTo(a1.SubTrans))
		Expect(issued.Kind).To(Equal(signal.CmdKindRead))
		Expect(q.held).To(HaveKeyWithValue(bank2, b))
		Expect(channel.probes).To(Equal(map[string]int{"B0": 1}))
		stats := q.GetRowContinuationStats()
		Expect(stats.AutoPrechargeStops).To(Equal(uint64(1)))
		Expect(stats.AggregateAutoPrechargeStops).To(BeZero())
		Expect(q.preferredAggregateID).To(BeEmpty())

		// B goes ahead of the older ready activate; C is not scanned.
		readyAt(12, d0, signal.CmdKindActivate)
		readyAt(12, b0, signal.CmdKindReadPrecharge)
		readyAt(12, b1, signal.CmdKindReadPrecharge)
		issued = q.GetCommandToIssue(12)
		Expect(issued.SubTrans).To(BeIdenticalTo(b0.SubTrans))
		Expect(issued.Kind).To(Equal(signal.CmdKindRead))

		readyAt(13, d0, signal.CmdKindActivate)
		readyAt(13, b1, signal.CmdKindReadPrecharge)
		issued = q.GetCommandToIssue(13)
		Expect(issued.SubTrans).To(BeIdenticalTo(b1.SubTrans))
		Expect(issued.Kind).To(Equal(signal.CmdKindReadPrecharge))
		Expect(q.held).To(BeEmpty())

		// The bank is released, so C's precharge is eligible again.
		readyAt(14, d0, signal.CmdKindActivate)
		readyAt(14, c0, signal.CmdKindPrecharge)
		Expect(q.GetCommandToIssue(14).SubTrans).
			To(BeIdenticalTo(d0.SubTrans))

		stats = q.GetRowContinuationStats()
		Expect(stats.AutoPrechargeStops).To(Equal(uint64(1)))
		Expect(stats.RowHoldAutoPrechargeStops).To(Equal(uint64(2)))
		Expect(channel.probes).To(Equal(map[string]int{"B0": 1}))
	})

	It("should not hand the row over when the first other-transaction "+
		"entry targets another row", func() {
		q.RowContinuation = true
		a := read()
		a0 := unit(a, "A0", 0, 2, 7, 0)
		a1 := unit(a, "A1", 0, 2, 7, 1)
		c0 := unit(read(), "C0", 0, 2, 8, 0)
		b0 := unit(read(), "B0", 0, 2, 7, 4)
		q.Queues[0] = Queue{a0, a1, c0, b0}
		channel.ready["B0"] = true
		readyAt(10, a0, signal.CmdKindReadPrecharge)
		readyAt(10, a1, signal.CmdKindReadPrecharge)
		readyAt(10, c0, signal.CmdKindPrecharge)
		readyAt(10, b0, signal.CmdKindReadPrecharge)
		q.GetCommandToIssue(10)

		readyAt(11, a1, signal.CmdKindReadPrecharge)
		issued := q.GetCommandToIssue(11)

		Expect(issued.Kind).To(Equal(signal.CmdKindReadPrecharge))
		Expect(q.held).To(BeEmpty())
		Expect(channel.probes).To(BeEmpty())
		Expect(q.GetRowContinuationStats().AutoPrechargeStops).To(BeZero())
	})

	It("should not hand the row over to a peer that is not ready", func() {
		q.RowContinuation = true
		a := read()
		a0 := unit(a, "A0", 0, 2, 7, 0)
		a1 := unit(a, "A1", 0, 2, 7, 1)
		b0 := unit(read(), "B0", 0, 2, 7, 4)
		q.Queues[0] = Queue{a0, a1, b0}
		readyAt(10, a0, signal.CmdKindReadPrecharge)
		readyAt(10, a1, signal.CmdKindReadPrecharge)
		readyAt(10, b0, signal.CmdKindReadPrecharge)
		q.GetCommandToIssue(10)

		readyAt(11, a1, signal.CmdKindReadPrecharge)
		issued := q.GetCommandToIssue(11)

		Expect(issued.Kind).To(Equal(signal.CmdKindReadPrecharge))
		Expect(q.held).To(BeEmpty())
		Expect(channel.probes).To(Equal(map[string]int{"B0": 1}))
	})

	DescribeTable("should not hand the row over",
		func(rowContinuation, refresh bool) {
			q.RowContinuation = rowContinuation
			channel.refresh = refresh
			a := read()
			a0 := unit(a, "A0", 0, 2, 7, 0)
			a1 := unit(a, "A1", 0, 2, 7, 1)
			b0 := unit(read(), "B0", 0, 2, 7, 4)
			q.Queues[0] = Queue{a0, a1, b0}
			channel.ready["B0"] = true
			readyAt(10, a0, signal.CmdKindReadPrecharge)
			readyAt(10, a1, signal.CmdKindReadPrecharge)
			readyAt(10, b0, signal.CmdKindReadPrecharge)

			// Row hold still keeps the row for A's second access.
			Expect(q.GetCommandToIssue(10).Kind).
				To(Equal(signal.CmdKindRead))

			readyAt(11, a1, signal.CmdKindReadPrecharge)
			issued := q.GetCommandToIssue(11)

			Expect(issued.Kind).To(Equal(signal.CmdKindReadPrecharge))
			Expect(q.held).To(BeEmpty())
			Expect(channel.probes).To(BeEmpty())
			stats := q.GetRowContinuationStats()
			Expect(stats.AutoPrechargeStops).To(BeZero())
			Expect(stats.RowHoldAutoPrechargeStops).To(Equal(uint64(1)))
		},
		Entry("when row and aggregate continuation are off", false, false),
		Entry("while the channel waits to refresh", true, true),
	)

	It("should hand the row over to the same aggregate with aggregate "+
		"continuation", func() {
		q.AggregateContinuation = true
		demand := pairedRead(mem.PairedReadDemand)
		sibling := pairedRead(mem.PairedReadSibling)
		a0 := unit(demand, "A0", 0, 2, 7, 0)
		a1 := unit(demand, "A1", 0, 2, 7, 1)
		b0 := unit(sibling, "B0", 0, 2, 7, 2)
		q.Queues[0] = Queue{a0, a1, b0}
		channel.ready["B0"] = true
		readyAt(10, a0, signal.CmdKindReadPrecharge)
		readyAt(10, a1, signal.CmdKindReadPrecharge)
		readyAt(10, b0, signal.CmdKindReadPrecharge)
		q.GetCommandToIssue(10)

		readyAt(11, a1, signal.CmdKindReadPrecharge)
		issued := q.GetCommandToIssue(11)

		Expect(issued.Kind).To(Equal(signal.CmdKindRead))
		Expect(q.held).To(HaveKeyWithValue(bank2, sibling))
		Expect(q.preferredAggregateID).To(BeEmpty())
		stats := q.GetRowContinuationStats()
		Expect(stats.AutoPrechargeStops).To(Equal(uint64(1)))
		Expect(stats.AggregateAutoPrechargeStops).To(Equal(uint64(1)))
	})

	It("should not hand the row over to an unrelated transaction with "+
		"aggregate continuation only", func() {
		q.AggregateContinuation = true
		a := read()
		a0 := unit(a, "A0", 0, 2, 7, 0)
		a1 := unit(a, "A1", 0, 2, 7, 1)
		b0 := unit(read(), "B0", 0, 2, 7, 2)
		q.Queues[0] = Queue{a0, a1, b0}
		channel.ready["B0"] = true
		readyAt(10, a0, signal.CmdKindReadPrecharge)
		readyAt(10, a1, signal.CmdKindReadPrecharge)
		readyAt(10, b0, signal.CmdKindReadPrecharge)
		q.GetCommandToIssue(10)

		readyAt(11, a1, signal.CmdKindReadPrecharge)

		Expect(q.GetCommandToIssue(11).Kind).
			To(Equal(signal.CmdKindReadPrecharge))
		Expect(q.held).To(BeEmpty())
	})

	It("should keep the holder's precharge for another row until the "+
		"release", func() {
		a := read()
		a0 := unit(a, "A0", 0, 2, 7, 0)
		a2 := unit(a, "A2", 0, 2, 8, 0)
		a1 := unit(a, "A1", 0, 2, 7, 1)
		q.Queues[0] = Queue{a0, a2, a1}
		readyAt(10, a0, signal.CmdKindReadPrecharge)
		readyAt(10, a2, signal.CmdKindPrecharge)
		readyAt(10, a1, signal.CmdKindReadPrecharge)
		Expect(q.GetCommandToIssue(10).Kind).To(Equal(signal.CmdKindRead))

		readyAt(11, a2, signal.CmdKindPrecharge)
		notReadyAt(11, a1)
		Expect(q.GetCommandToIssue(11)).To(BeNil())

		readyAt(12, a2, signal.CmdKindPrecharge)
		readyAt(12, a1, signal.CmdKindReadPrecharge)
		issued := q.GetCommandToIssue(12)
		Expect(issued.SubTrans).To(BeIdenticalTo(a1.SubTrans))
		Expect(issued.Kind).To(Equal(signal.CmdKindReadPrecharge))
		Expect(q.held).To(BeEmpty())

		readyAt(13, a2, signal.CmdKindActivate)
		issued = q.GetCommandToIssue(13)
		Expect(issued.Kind).To(Equal(signal.CmdKindActivate))
		Expect(issued.SubTrans).To(BeIdenticalTo(a2.SubTrans))
	})

	It("should skip the legacy peer test while the channel waits to "+
		"refresh", func() {
		q.RowHold = false
		q.RowContinuation = true
		channel.refresh = true
		first := unit(read(), "first", 0, 2, 7, 0)
		peer := unit(read(), "peer", 0, 2, 7, 1)
		q.Queues[0] = Queue{first, peer}
		readyAt(10, first, signal.CmdKindReadPrecharge)
		readyAt(10, peer, signal.CmdKindReadPrecharge)

		Expect(q.GetCommandToIssue(10).Kind).
			To(Equal(signal.CmdKindReadPrecharge))
		Expect(q.GetRowContinuationStats().AutoPrechargeStops).To(BeZero())

		channel.refresh = false
		readyAt(11, peer, signal.CmdKindReadPrecharge)
		Expect(q.GetCommandToIssue(11).Kind).
			To(Equal(signal.CmdKindReadPrecharge))
		Expect(q.held).To(BeEmpty())
	})
})
