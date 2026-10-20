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

// columnReadyChannel is a mock channel that also answers the optional
// column-readiness observation.
type columnReadyChannel struct {
	*MockChannel
	ready map[string]bool
}

func (c *columnReadyChannel) ObservationColumnReady(cmd *signal.Command) bool {
	return c.ready[cmd.ID]
}

var _ org.ColumnReadyObserver = (*columnReadyChannel)(nil)

func readyAs(cmd *signal.Command, kind signal.CommandKind) *signal.Command {
	ready := cmd.Clone()
	ready.Kind = kind
	return ready
}

var _ = Describe("CommandQueueImpl queue index", func() {
	It("should give every (channel, rank) its own queue", func() {
		q := CommandQueueImpl{
			Queues:           make([]Queue, 4),
			CapacityPerQueue: 1,
			RanksPerChannel:  2,
		}
		cmds := map[addressmapping.Location]int{
			{Channel: 0, Rank: 0}: 0,
			{Channel: 0, Rank: 1}: 1,
			{Channel: 1, Rank: 0}: 2,
			{Channel: 1, Rank: 1}: 3,
		}

		for location, queueIndex := range cmds {
			cmd := &signal.Command{Location: location}
			Expect(q.getQueueIndex(cmd)).To(Equal(queueIndex))
			Expect(q.CanAccept(cmd)).To(BeTrue())
			q.Accept(cmd)
			Expect(q.Queues[queueIndex]).To(ConsistOf(cmd))
			Expect(q.CanAccept(cmd)).To(BeFalse())
		}
		Expect(q.CanAcceptAll([]*signal.Command{{
			Location: addressmapping.Location{Channel: 1},
		}})).To(BeFalse())
	})

	It("should ignore the channel in the legacy queue index", func() {
		q := CommandQueueImpl{
			Queues:           make([]Queue, 4),
			CapacityPerQueue: 8,
		}
		cmd := &signal.Command{
			Location: addressmapping.Location{Channel: 1, Rank: 1},
		}

		Expect(q.getQueueIndex(cmd)).To(Equal(1))
		q.Accept(cmd)
		Expect(q.Queues[1]).To(ConsistOf(cmd))
	})
})

var _ = Describe("CommandQueueImpl command bus", func() {
	var (
		mockCtrl *gomock.Controller
		channel  *MockChannel
		q        CommandQueueImpl
		read     *signal.Command
		activate *signal.Command
	)

	BeforeEach(func() {
		mockCtrl = gomock.NewController(GinkgoT())
		channel = NewMockChannel(mockCtrl)
		q = CommandQueueImpl{
			Queues:           make([]Queue, 2),
			CapacityPerQueue: 8,
			Channel:          channel,
		}
		read = &signal.Command{
			ID:   "row-hit",
			Kind: signal.CmdKindReadPrecharge,
			Location: addressmapping.Location{
				Rank: 0, Bank: 0, Row: 1,
			},
		}
		activate = &signal.Command{
			ID:   "needs-activate",
			Kind: signal.CmdKindReadPrecharge,
			Location: addressmapping.Location{
				Rank: 1, Bank: 1, Row: 2,
			},
		}
		q.Queues[0] = append(q.Queues[0], read)
		q.Queues[1] = append(q.Queues[1], activate)
	})

	AfterEach(func() {
		mockCtrl.Finish()
	})

	It("should classify the commands by bus", func() {
		Expect(BusOf(signal.CmdKindRead)).To(Equal(ColumnBus))
		Expect(BusOf(signal.CmdKindReadPrecharge)).To(Equal(ColumnBus))
		Expect(BusOf(signal.CmdKindWrite)).To(Equal(ColumnBus))
		Expect(BusOf(signal.CmdKindWritePrecharge)).To(Equal(ColumnBus))
		Expect(BusOf(signal.CmdKindActivate)).To(Equal(RowBus))
		Expect(BusOf(signal.CmdKindPrecharge)).To(Equal(RowBus))
		Expect(BusOf(signal.CmdKindRefresh)).To(Equal(RowBus))
		Expect(BusOf(signal.CmdKindRefreshBank)).To(Equal(RowBus))
	})

	It("should skip a ready read on the row bus", func() {
		channel.EXPECT().GetReadyCommand(sim.VTimeInSec(10), read).
			Return(read.Clone())
		channel.EXPECT().GetReadyCommand(sim.VTimeInSec(10), activate).
			Return(readyAs(activate, signal.CmdKindActivate))

		issued := q.GetCommandToIssueOn(10, RowBus)

		Expect(issued.Kind).To(Equal(signal.CmdKindActivate))
		Expect(issued.Rank).To(Equal(uint64(1)))
		Expect(q.Queues[0]).To(ConsistOf(read))
		Expect(q.Queues[1]).To(ConsistOf(activate))
		Expect(q.nextQueueIndex).To(Equal(0))
		Expect(q.GetRowContinuationStats().ActivateCommands).
			To(Equal(uint64(1)))
	})

	It("should skip a ready activate on the column bus", func() {
		q.nextQueueIndex = 1
		channel.EXPECT().GetReadyCommand(sim.VTimeInSec(10), activate).
			Return(readyAs(activate, signal.CmdKindActivate))
		channel.EXPECT().GetReadyCommand(sim.VTimeInSec(10), read).
			Return(read.Clone())

		issued := q.GetCommandToIssueOn(10, ColumnBus)

		Expect(issued.Kind).To(Equal(signal.CmdKindReadPrecharge))
		Expect(issued.Rank).To(Equal(uint64(0)))
		Expect(q.Queues[0]).To(BeEmpty())
		Expect(q.Queues[1]).To(ConsistOf(activate))
		Expect(q.nextQueueIndex).To(Equal(1))
		Expect(q.GetRowContinuationStats().ColumnCommands).
			To(Equal(uint64(1)))
	})

	It("should keep round robin when only the other bus is ready", func() {
		q.nextQueueIndex = 1
		channel.EXPECT().GetReadyCommand(sim.VTimeInSec(10), activate).
			Return(nil)
		channel.EXPECT().GetReadyCommand(sim.VTimeInSec(10), read).
			Return(read.Clone())

		Expect(q.GetCommandToIssueOn(10, RowBus)).To(BeNil())
		Expect(q.nextQueueIndex).To(Equal(1))
		Expect(q.readyScratch).To(BeEmpty())
		Expect(q.Queues[0]).To(ConsistOf(read))
		Expect(q.GetRowContinuationStats().CommandsIssued).To(BeZero())
	})

	It("should use the first-ready loop for any bus", func() {
		// The loop stops at the first ready command, so the activate is
		// never queried.
		channel.EXPECT().GetReadyCommand(sim.VTimeInSec(10), read).
			Return(read)

		Expect(q.GetCommandToIssueOn(10, AnyBus)).To(BeIdenticalTo(read))
		Expect(q.nextQueueIndex).To(Equal(1))
	})

	It("should not keep a row open without continuations", func() {
		q.Queues = make([]Queue, 1)
		demand := mem.ReadReqBuilder{}.
			WithByteSize(64).
			WithPairedRead("pair-bus", mem.PairedReadDemand).
			Build()
		sibling := mem.ReadReqBuilder{}.
			WithByteSize(64).
			WithPairedRead("pair-bus", mem.PairedReadSibling).
			Build()
		first := &signal.Command{
			ID: "pair-demand", Kind: signal.CmdKindReadPrecharge,
			SubTrans: &signal.SubTransaction{Transaction: &signal.Transaction{
				Read: demand,
			}},
			Location: addressmapping.Location{Bank: 2, Row: 7, Column: 10},
		}
		peer := &signal.Command{
			ID: "pair-sibling", Kind: signal.CmdKindReadPrecharge,
			SubTrans: &signal.SubTransaction{Transaction: &signal.Transaction{
				Read: sibling,
			}},
			Location: addressmapping.Location{Bank: 2, Row: 7, Column: 11},
		}
		q.Queues[0] = append(q.Queues[0], first, peer)
		channel.EXPECT().GetReadyCommand(sim.VTimeInSec(10), first).
			Return(first.Clone())
		channel.EXPECT().GetReadyCommand(sim.VTimeInSec(10), peer).
			Return(peer.Clone())

		issued := q.GetCommandToIssueOn(10, ColumnBus)

		Expect(issued.Kind).To(Equal(signal.CmdKindReadPrecharge))
		Expect(q.GetRowContinuationStats().AggregateAutoPrechargeStops).
			To(BeZero())
	})

	It("should keep a row open for a ready same-row peer", func() {
		q.Queues = make([]Queue, 1)
		q.RowContinuation = true
		first := &signal.Command{
			ID: "first", Kind: signal.CmdKindReadPrecharge,
			Location: addressmapping.Location{Bank: 2, Row: 7},
		}
		peer := &signal.Command{
			ID: "peer", Kind: signal.CmdKindReadPrecharge,
			Location: addressmapping.Location{Bank: 2, Row: 7},
		}
		q.Queues[0] = append(q.Queues[0], first, peer)
		channel.EXPECT().GetReadyCommand(sim.VTimeInSec(10), first).
			Return(first.Clone())
		channel.EXPECT().GetReadyCommand(sim.VTimeInSec(10), peer).
			Return(peer.Clone())

		issued := q.GetCommandToIssueOn(10, ColumnBus)

		Expect(issued.Kind).To(Equal(signal.CmdKindRead))
		Expect(q.GetRowContinuationStats().AutoPrechargeStops).
			To(Equal(uint64(1)))
	})

	It("should count a ready row command of the other bus as a peer",
		func() {
			q.Queues = make([]Queue, 1)
			q.RowContinuation = true
			first := &signal.Command{
				ID: "first", Kind: signal.CmdKindReadPrecharge,
				Location: addressmapping.Location{Bank: 2, Row: 7},
			}
			conflict := &signal.Command{
				ID: "conflict", Kind: signal.CmdKindReadPrecharge,
				Location: addressmapping.Location{Bank: 2, Row: 8},
			}
			farPeer := &signal.Command{
				ID: "far-peer", Kind: signal.CmdKindReadPrecharge,
				Location: addressmapping.Location{Bank: 2, Row: 7},
			}
			q.Queues[0] = append(q.Queues[0], first, conflict, farPeer)
			channel.EXPECT().GetReadyCommand(sim.VTimeInSec(10), first).
				Return(first.Clone())
			channel.EXPECT().GetReadyCommand(sim.VTimeInSec(10), conflict).
				Return(readyAs(conflict, signal.CmdKindPrecharge))
			channel.EXPECT().GetReadyCommand(sim.VTimeInSec(10), farPeer).
				Return(farPeer.Clone())

			issued := q.GetCommandToIssueOn(10, ColumnBus)

			Expect(issued.Kind).To(Equal(signal.CmdKindReadPrecharge))
			Expect(issued.Row).To(Equal(uint64(7)))
			Expect(q.Queues[0]).To(ConsistOf(conflict, farPeer))
			Expect(q.GetRowContinuationStats().AutoPrechargeStops).
				To(BeZero())
		})

	It("should observe column readiness through the optional interface",
		func() {
			q.Channel = &columnReadyChannel{
				MockChannel: channel,
				ready:       map[string]bool{"row-hit": true},
			}

			Expect(q.ObservationReadySameOpenRow(0, 0, 0, 0, 1)).To(Equal(1))
			Expect(q.ObservationReadySameOpenRow(1, 0, 0, 0, 1)).To(BeZero())
			Expect(q.ObservationReadySameOpenRow(0, 0, 0, 0, 2)).To(BeZero())

			q.Channel = channel
			Expect(q.ObservationReadySameOpenRow(0, 0, 0, 0, 1)).To(BeZero())
		})
})
