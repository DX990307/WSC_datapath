package cmdq

import (
	"github.com/golang/mock/gomock"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/sarchlab/akita/v3/mem/dram/internal/addressmapping"
	"github.com/sarchlab/akita/v3/mem/dram/internal/signal"
	"github.com/sarchlab/akita/v3/mem/mem"
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
		stats := q.GetRowContinuationStats()
		Expect(stats.Enabled).To(BeFalse())
		Expect(stats.CommandsIssued).To(Equal(uint64(1)))
		Expect(stats.ColumnCommands).To(Equal(uint64(1)))
	})

	It("should accept new commands", func() {
		cmd := &signal.Command{}

		Expect(q.CanAccept(cmd)).To(BeTrue())

		q.Accept(cmd)

		Expect(q.Queues[0]).To(ContainElement(cmd))
	})

	It("should preserve command order instead of promoting a row hit", func() {
		q.Queues = make([]Queue, 1)
		q.RowContinuation = true
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

		Expect(ready.Kind).To(Equal(signal.CmdKindActivate))
		Expect(q.Queues[0]).To(ConsistOf(needsActivate, rowHit))
		stats := q.GetRowContinuationStats()
		Expect(stats.RowReuseHits).To(BeZero())
	})

	It("should not promote a row hit from another bank", func() {
		q.Queues = make([]Queue, 1)
		q.RowContinuation = true
		q.Freq = sim.GHz
		needsActivate := &signal.Command{
			ID:         "activate-bank-0",
			Kind:       signal.CmdKindRead,
			EnqueuedAt: 1e-9,
			Location: addressmapping.Location{
				Bank: 0,
			},
		}
		otherBankHit := &signal.Command{
			ID:         "row-hit-bank-1",
			Kind:       signal.CmdKindRead,
			EnqueuedAt: 2e-9,
			Location: addressmapping.Location{
				Bank: 1,
			},
		}
		activate := needsActivate.Clone()
		activate.Kind = signal.CmdKindActivate
		q.Queues[0] = append(q.Queues[0], needsActivate, otherBankHit)
		channel.EXPECT().
			GetReadyCommand(sim.VTimeInSec(10e-9), needsActivate).
			Return(activate)
		channel.EXPECT().
			GetReadyCommand(sim.VTimeInSec(10e-9), otherBankHit).
			Return(otherBankHit)

		ready := q.GetCommandToIssue(10e-9)

		Expect(ready.Kind).To(Equal(signal.CmdKindActivate))
		Expect(ready.Bank).To(Equal(uint64(0)))
		Expect(q.Queues[0]).To(ConsistOf(needsActivate, otherBankHit))
	})

	It("should keep a row open only for a ready same-row peer", func() {
		q.Queues = make([]Queue, 1)
		q.RowContinuation = true
		first := &signal.Command{
			ID:         "first",
			Kind:       signal.CmdKindReadPrecharge,
			EnqueuedAt: 1e-9,
			Location: addressmapping.Location{
				Bank: 2,
				Row:  7,
			},
		}
		peer := &signal.Command{
			ID:         "peer",
			Kind:       signal.CmdKindReadPrecharge,
			EnqueuedAt: 2e-9,
			Location: addressmapping.Location{
				Bank: 2,
				Row:  7,
			},
		}
		readyFirst := first.Clone()
		readyPeer := peer.Clone()
		q.Queues[0] = append(q.Queues[0], first, peer)
		channel.EXPECT().
			GetReadyCommand(sim.VTimeInSec(10e-9), first).
			Return(readyFirst)
		channel.EXPECT().
			GetReadyCommand(sim.VTimeInSec(10e-9), peer).
			Return(readyPeer)

		ready := q.GetCommandToIssue(10e-9)

		Expect(ready.Kind).To(Equal(signal.CmdKindRead))
		Expect(q.Queues[0]).To(ConsistOf(peer))
		stats := q.GetRowContinuationStats()
		Expect(stats.AutoPrechargeStops).To(Equal(uint64(1)))
	})

	It("should keep a row open for two units of the same aggregate", func() {
		q.Queues = make([]Queue, 1)
		q.AggregateContinuation = true
		demand := mem.ReadReqBuilder{}.
			WithByteSize(64).
			WithPairedRead("pair-1", mem.PairedReadDemand).
			Build()
		sibling := mem.ReadReqBuilder{}.
			WithByteSize(64).
			WithPairedRead("pair-1", mem.PairedReadSibling).
			Build()
		firstSub := &signal.SubTransaction{Transaction: &signal.Transaction{
			Read: demand,
		}}
		peerSub := &signal.SubTransaction{Transaction: &signal.Transaction{
			Read: sibling,
		}}
		first := &signal.Command{
			ID: "aggregate-first", Kind: signal.CmdKindReadPrecharge,
			SubTrans: firstSub,
			Location: addressmapping.Location{Bank: 2, Row: 7, Column: 10},
		}
		peer := &signal.Command{
			ID: "aggregate-peer", Kind: signal.CmdKindReadPrecharge,
			SubTrans: peerSub,
			Location: addressmapping.Location{Bank: 2, Row: 7, Column: 11},
		}
		q.Queues[0] = append(q.Queues[0], first, peer)
		channel.EXPECT().GetReadyCommand(sim.VTimeInSec(10e-9), first).
			Return(first.Clone())
		channel.EXPECT().GetReadyCommand(sim.VTimeInSec(10e-9), peer).
			Return(peer.Clone())

		ready := q.GetCommandToIssue(10e-9)

		Expect(ready.Kind).To(Equal(signal.CmdKindRead))
		stats := q.GetRowContinuationStats()
		Expect(stats.Enabled).To(BeFalse())
		Expect(stats.AggregateEnabled).To(BeTrue())
		Expect(stats.AggregateAutoPrechargeStops).To(Equal(uint64(1)))
	})

	It("should continue a ready aggregate peer before round robin moves on", func() {
		q.Queues = make([]Queue, 2)
		q.AggregateContinuation = true
		demand := mem.ReadReqBuilder{}.
			WithByteSize(64).
			WithPairedRead("pair-immediate", mem.PairedReadDemand).
			Build()
		sibling := mem.ReadReqBuilder{}.
			WithByteSize(64).
			WithPairedRead("pair-immediate", mem.PairedReadSibling).
			Build()
		first := &signal.Command{
			ID: "pair-demand", Kind: signal.CmdKindReadPrecharge,
			SubTrans: &signal.SubTransaction{Transaction: &signal.Transaction{
				Read: demand,
			}},
			Location: addressmapping.Location{
				Rank: 0, Bank: 2, Row: 7, Column: 10,
			},
		}
		peer := &signal.Command{
			ID: "pair-sibling", Kind: signal.CmdKindReadPrecharge,
			SubTrans: &signal.SubTransaction{Transaction: &signal.Transaction{
				Read: sibling,
			}},
			Location: addressmapping.Location{
				Rank: 0, Bank: 2, Row: 7, Column: 11,
			},
		}
		unrelated := &signal.Command{
			ID: "round-robin", Kind: signal.CmdKindReadPrecharge,
			Location: addressmapping.Location{
				Rank: 1, Bank: 1, Row: 3, Column: 4,
			},
		}
		q.Queues[0] = append(q.Queues[0], first, peer)
		q.Queues[1] = append(q.Queues[1], unrelated)
		channel.EXPECT().GetReadyCommand(sim.VTimeInSec(10e-9), first).
			Return(first.Clone())
		channel.EXPECT().GetReadyCommand(sim.VTimeInSec(10e-9), peer).
			Return(peer.Clone())
		channel.EXPECT().GetReadyCommand(sim.VTimeInSec(10e-9), unrelated).
			Return(unrelated.Clone())

		ready := q.GetCommandToIssue(10e-9)
		Expect(ready.SubTrans).To(BeIdenticalTo(first.SubTrans))

		channel.EXPECT().GetReadyCommand(sim.VTimeInSec(11e-9), unrelated).
			Return(unrelated.Clone())
		channel.EXPECT().GetReadyCommand(sim.VTimeInSec(11e-9), peer).
			Return(peer.Clone())

		ready = q.GetCommandToIssue(11e-9)
		Expect(ready.SubTrans).To(BeIdenticalTo(peer.SubTrans))
		Expect(q.Queues[1]).To(ConsistOf(unrelated))
		stats := q.GetRowContinuationStats()
		Expect(stats.AggregateAutoPrechargeStops).To(Equal(uint64(1)))
		Expect(stats.AggregateImmediateContinues).To(Equal(uint64(1)))
	})

	It("should reject an empty pair ID and non-adjacent columns", func() {
		emptyDemand := mem.ReadReqBuilder{}.
			WithByteSize(64).
			Build()
		emptySibling := mem.ReadReqBuilder{}.
			WithByteSize(64).
			Build()
		first := &signal.Command{
			SubTrans: &signal.SubTransaction{Transaction: &signal.Transaction{
				Read: emptyDemand,
			}},
			Location: addressmapping.Location{Column: 10},
		}
		peer := &signal.Command{
			SubTrans: &signal.SubTransaction{Transaction: &signal.Transaction{
				Read: emptySibling,
			}},
			Location: addressmapping.Location{Column: 11},
		}
		Expect(samePairedReadAggregate(first, peer)).To(BeFalse())

		first.SubTrans.Transaction.Read = mem.ReadReqBuilder{}.
			WithByteSize(64).
			WithPairedRead("pair-2", mem.PairedReadDemand).
			Build()
		peer.SubTrans.Transaction.Read = mem.ReadReqBuilder{}.
			WithByteSize(64).
			WithPairedRead("pair-2", mem.PairedReadSibling).
			Build()
		peer.Column = 12
		Expect(samePairedReadAggregate(first, peer)).To(BeFalse())
	})

	It("should not keep a row open for an unrelated aggregate", func() {
		q.Queues = make([]Queue, 1)
		q.AggregateContinuation = true
		first := &signal.Command{
			ID: "first-parent", Kind: signal.CmdKindReadPrecharge,
			SubTrans: &signal.SubTransaction{Transaction: &signal.Transaction{}},
			Location: addressmapping.Location{Bank: 2, Row: 7},
		}
		peer := &signal.Command{
			ID: "second-parent", Kind: signal.CmdKindReadPrecharge,
			SubTrans: &signal.SubTransaction{Transaction: &signal.Transaction{}},
			Location: addressmapping.Location{Bank: 2, Row: 7},
		}
		q.Queues[0] = append(q.Queues[0], first, peer)
		channel.EXPECT().GetReadyCommand(sim.VTimeInSec(10e-9), first).
			Return(first.Clone())
		channel.EXPECT().GetReadyCommand(sim.VTimeInSec(10e-9), peer).
			Return(peer.Clone())

		ready := q.GetCommandToIssue(10e-9)

		Expect(ready.Kind).To(Equal(signal.CmdKindReadPrecharge))
		Expect(q.GetRowContinuationStats().AggregateAutoPrechargeStops).
			To(BeZero())
	})

	It("should auto-precharge when a queued same-row peer is not ready", func() {
		q.Queues = make([]Queue, 1)
		q.RowContinuation = true
		first := &signal.Command{
			ID:         "first",
			Kind:       signal.CmdKindReadPrecharge,
			EnqueuedAt: 1e-9,
			Location: addressmapping.Location{
				Bank: 2,
				Row:  7,
			},
		}
		peer := &signal.Command{
			ID:         "blocked-peer",
			Kind:       signal.CmdKindReadPrecharge,
			EnqueuedAt: 2e-9,
			Location: addressmapping.Location{
				Bank: 2,
				Row:  7,
			},
		}
		readyFirst := first.Clone()
		q.Queues[0] = append(q.Queues[0], first, peer)
		channel.EXPECT().
			GetReadyCommand(sim.VTimeInSec(10e-9), first).
			Return(readyFirst)
		channel.EXPECT().
			GetReadyCommand(sim.VTimeInSec(10e-9), peer).
			Return(nil)

		ready := q.GetCommandToIssue(10e-9)

		Expect(ready.Kind).To(Equal(signal.CmdKindReadPrecharge))
		Expect(q.Queues[0]).To(ConsistOf(peer))
		stats := q.GetRowContinuationStats()
		Expect(stats.AutoPrechargeStops).To(BeZero())
	})

	It("should not keep a row for a peer behind a different-row command", func() {
		q.Queues = make([]Queue, 1)
		q.RowContinuation = true
		first := &signal.Command{
			ID:         "first",
			Kind:       signal.CmdKindReadPrecharge,
			EnqueuedAt: 1e-9,
			Location: addressmapping.Location{
				Bank: 2,
				Row:  7,
			},
		}
		conflict := &signal.Command{
			ID:         "conflict",
			Kind:       signal.CmdKindReadPrecharge,
			EnqueuedAt: 2e-9,
			Location: addressmapping.Location{
				Bank: 2,
				Row:  8,
			},
		}
		farPeer := &signal.Command{
			ID:         "far-peer",
			Kind:       signal.CmdKindReadPrecharge,
			EnqueuedAt: 3e-9,
			Location: addressmapping.Location{
				Bank: 2,
				Row:  7,
			},
		}
		q.Queues[0] = append(q.Queues[0], first, conflict, farPeer)
		channel.EXPECT().
			GetReadyCommand(sim.VTimeInSec(10e-9), first).
			Return(first.Clone())
		channel.EXPECT().
			GetReadyCommand(sim.VTimeInSec(10e-9), conflict).
			Return(conflict.Clone())
		channel.EXPECT().
			GetReadyCommand(sim.VTimeInSec(10e-9), farPeer).
			Return(farPeer.Clone())

		ready := q.GetCommandToIssue(10e-9)

		Expect(ready.Kind).To(Equal(signal.CmdKindReadPrecharge))
		Expect(q.GetRowContinuationStats().AutoPrechargeStops).To(BeZero())
	})

	It("should count an ordinary auto-precharge column command", func() {
		q.Queues = make([]Queue, 1)
		q.RowContinuation = true
		cmd := &signal.Command{
			ID:         "singleton",
			Kind:       signal.CmdKindReadPrecharge,
			EnqueuedAt: 1e-9,
		}
		ready := cmd.Clone()
		q.Queues[0] = append(q.Queues[0], cmd)
		channel.EXPECT().
			GetReadyCommand(sim.VTimeInSec(10e-9), cmd).
			Return(ready)

		issued := q.GetCommandToIssue(10e-9)

		Expect(issued.Kind).To(Equal(signal.CmdKindReadPrecharge))
		stats := q.GetRowContinuationStats()
		Expect(stats.ColumnCommands).To(Equal(uint64(1)))
		Expect(stats.AutoPrechargeStops).To(BeZero())
	})

})
