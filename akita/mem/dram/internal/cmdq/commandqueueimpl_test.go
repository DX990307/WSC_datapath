package cmdq

import (
	"github.com/golang/mock/gomock"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/sarchlab/akita/v3/mem/dram/internal/addressmapping"
	"github.com/sarchlab/akita/v3/mem/dram/internal/signal"
	"github.com/sarchlab/akita/v3/sim"
)

var _ = Describe("CommandQueueImpl", func() {
	var (
		mockCtrl *gomock.Controller
		channel  *MockChannel
		q        CommandQueueImpl
	)

	BeforeEach(func() {
		mockCtrl = gomock.NewController(GinkgoT())
		channel = NewMockChannel(mockCtrl)
		q = CommandQueueImpl{
			Queues:           make([]Queue, 8),
			CapacityPerQueue: 8,
			nextQueueIndex:   0,
			Channel:          channel,
		}
	})

	AfterEach(func() {
		mockCtrl.Finish()
	})

	It("should get the next command to issue", func() {
		cmd1 := &signal.Command{
			ID:   "1",
			Kind: signal.CmdKindRead,
			Location: addressmapping.Location{
				Rank: 0,
				Bank: 0,
			},
		}
		q.Queues[0] = append(q.Queues[0], cmd1)

		cmd2 := &signal.Command{
			ID:   "2",
			Kind: signal.CmdKindRead,
			Location: addressmapping.Location{
				Rank: 0,
				Bank: 0,
			},
		}
		q.Queues[0] = append(q.Queues[0], cmd2)

		cmd3 := &signal.Command{
			ID:   "3",
			Kind: signal.CmdKindRead,
			Location: addressmapping.Location{
				Rank: 0,
				Bank: 1,
			},
		}
		q.Queues[1] = append(q.Queues[1], cmd3)

		channel.EXPECT().
			GetReadyCommand(sim.VTimeInSec(10), cmd1).
			Return(nil)
		channel.EXPECT().
			GetReadyCommand(sim.VTimeInSec(10), cmd2).
			Return(cmd2)

		readyCmd := q.GetCommandToIssue(10)

		Expect(readyCmd).To(BeIdenticalTo(cmd2))
		Expect(q.Queues[0]).NotTo(ContainElement(cmd2))
	})

	It("should accept new commands", func() {
		cmd := &signal.Command{}

		Expect(q.CanAccept(cmd)).To(BeTrue())

		q.Accept(cmd)

		Expect(q.Queues[0]).To(ContainElement(cmd))
	})

	It("should prioritize a ready open-row column command", func() {
		q.Queues = make([]Queue, 1)
		q.RowAware = true
		q.MaxAge = 100e-9
		q.Freq = sim.GHz
		needsActivate := &signal.Command{
			ID:         "activate",
			Kind:       signal.CmdKindRead,
			EnqueuedAt: 1e-9,
		}
		rowHit := &signal.Command{
			ID:         "row-hit",
			Kind:       signal.CmdKindRead,
			EnqueuedAt: 2e-9,
		}
		activate := needsActivate.Clone()
		activate.Kind = signal.CmdKindActivate
		q.Queues[0] = append(q.Queues[0], needsActivate, rowHit)
		channel.EXPECT().
			GetReadyCommand(sim.VTimeInSec(10e-9), needsActivate).
			Return(activate)
		channel.EXPECT().
			GetReadyCommand(sim.VTimeInSec(10e-9), rowHit).
			Return(rowHit)

		ready := q.GetCommandToIssue(10e-9)

		Expect(ready).To(BeIdenticalTo(rowHit))
		Expect(q.Queues[0]).To(ConsistOf(needsActivate))
		Expect(q.GetRowAwareStats().RowReuseHits).To(Equal(uint64(1)))
	})

	It("should prioritize an aged command to prevent starvation", func() {
		q.Queues = make([]Queue, 1)
		q.RowAware = true
		q.MaxAge = 5e-9
		q.Freq = sim.GHz
		aged := &signal.Command{
			ID:         "aged",
			Kind:       signal.CmdKindRead,
			EnqueuedAt: 1e-9,
		}
		rowHit := &signal.Command{
			ID:         "row-hit",
			Kind:       signal.CmdKindRead,
			EnqueuedAt: 9e-9,
		}
		activate := aged.Clone()
		activate.Kind = signal.CmdKindActivate
		q.Queues[0] = append(q.Queues[0], aged, rowHit)
		channel.EXPECT().
			GetReadyCommand(sim.VTimeInSec(10e-9), aged).
			Return(activate)
		channel.EXPECT().
			GetReadyCommand(sim.VTimeInSec(10e-9), rowHit).
			Return(rowHit)

		ready := q.GetCommandToIssue(10e-9)

		Expect(ready.Kind).To(Equal(signal.CmdKindActivate))
		Expect(q.Queues[0]).To(ConsistOf(aged, rowHit))
		stats := q.GetRowAwareStats()
		Expect(stats.AgedPriorityIssues).To(Equal(uint64(1)))
		Expect(stats.ActivateCommands).To(Equal(uint64(1)))
	})
})
