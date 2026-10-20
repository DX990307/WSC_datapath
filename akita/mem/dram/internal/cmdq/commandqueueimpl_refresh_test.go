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

// refreshChannel is a mock channel whose channels wait to refresh
// independently. It records the channels it is asked about.
type refreshChannel struct {
	*MockChannel
	pending map[uint64]bool
	queries []uint64
}

func (c *refreshChannel) RefreshPending(channel uint64) bool {
	c.queries = append(c.queries, channel)
	return c.pending[channel]
}

var _ org.RefreshPendingQuerier = (*refreshChannel)(nil)

var _ = Describe("CommandQueueImpl with a pending refresh", func() {
	var (
		mockCtrl *gomock.Controller
		mock     *MockChannel
		channel  *refreshChannel
		q        CommandQueueImpl
	)

	// read makes an auto-precharge read of channel ch, bank 2, row 7.
	read := func(
		id string,
		ch, column uint64,
		req *mem.ReadReq,
	) *signal.Command {
		return &signal.Command{
			ID:   id,
			Kind: signal.CmdKindReadPrecharge,
			SubTrans: &signal.SubTransaction{
				Transaction: &signal.Transaction{Read: req},
			},
			Location: addressmapping.Location{
				Channel: ch, Bank: 2, Row: 7, Column: column,
			},
		}
	}

	plainRead := func() *mem.ReadReq {
		return mem.ReadReqBuilder{}.WithByteSize(64).Build()
	}

	readyAt := func(now sim.VTimeInSec, cmds ...*signal.Command) {
		for _, cmd := range cmds {
			mock.EXPECT().GetReadyCommand(now, cmd).
				Return(readyAs(cmd, cmd.Kind))
		}
	}

	BeforeEach(func() {
		mockCtrl = gomock.NewController(GinkgoT())
		mock = NewMockChannel(mockCtrl)
		channel = &refreshChannel{
			MockChannel: mock,
			pending:     map[uint64]bool{},
		}
		q = CommandQueueImpl{
			Queues:           make([]Queue, 4),
			CapacityPerQueue: 8,
			Channel:          channel,
			RanksPerChannel:  2,
		}
	})

	AfterEach(func() {
		mockCtrl.Finish()
	})

	It("should keep rows open only in channels that do not wait to refresh",
		func() {
			q.RowContinuation = true
			channel.pending[1] = true
			first1 := read("first1", 1, 0, plainRead())
			peer1 := read("peer1", 1, 1, plainRead())
			q.Queues[2] = Queue{first1, peer1}
			readyAt(10, first1, peer1)

			issued := q.GetCommandToIssue(10)

			Expect(issued.SubTrans).To(BeIdenticalTo(first1.SubTrans))
			Expect(issued.Kind).To(Equal(signal.CmdKindReadPrecharge))
			Expect(q.GetRowContinuationStats().AutoPrechargeStops).
				To(BeZero())
			Expect(channel.queries).To(Equal([]uint64{1}))

			first0 := read("first0", 0, 0, plainRead())
			peer0 := read("peer0", 0, 1, plainRead())
			q.Queues[0] = Queue{first0, peer0}
			readyAt(11, first0, peer0, peer1)

			issued = q.GetCommandToIssue(11)

			Expect(issued.Kind).To(Equal(signal.CmdKindRead))
			Expect(issued.SubTrans).To(BeIdenticalTo(first0.SubTrans))
			Expect(q.GetRowContinuationStats().AutoPrechargeStops).
				To(Equal(uint64(1)))
			Expect(channel.queries).To(Equal([]uint64{1, 0}))
		})

	It("should not continue an aggregate while the channel waits to refresh",
		func() {
			q.AggregateContinuation = true
			channel.pending[0] = true
			demand := read("demand", 0, 0, mem.ReadReqBuilder{}.
				WithByteSize(64).
				WithPairedRead("pair", mem.PairedReadDemand).
				Build())
			sibling := read("sibling", 0, 1, mem.ReadReqBuilder{}.
				WithByteSize(64).
				WithPairedRead("pair", mem.PairedReadSibling).
				Build())
			q.Queues[0] = Queue{demand, sibling}
			readyAt(10, demand, sibling)

			Expect(q.GetCommandToIssue(10).Kind).
				To(Equal(signal.CmdKindReadPrecharge))
			Expect(q.preferredAggregateID).To(BeEmpty())

			channel.pending[0] = false
			readyAt(11, sibling)

			Expect(q.GetCommandToIssue(11).Kind).
				To(Equal(signal.CmdKindReadPrecharge))
			stats := q.GetRowContinuationStats()
			Expect(stats.AutoPrechargeStops).To(BeZero())
			Expect(stats.AggregateAutoPrechargeStops).To(BeZero())
			Expect(stats.AggregateImmediateContinues).To(BeZero())
		})
})
