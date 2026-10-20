package org

import (
	gomock "github.com/golang/mock/gomock"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/sarchlab/akita/v3/mem/dram/internal/addressmapping"
	"github.com/sarchlab/akita/v3/mem/dram/internal/signal"
	"github.com/sarchlab/akita/v3/sim"
)

var _ = Describe("ChannelImpl", func() {
	var (
		mockCtrl *gomock.Controller
		channel  ChannelImpl
	)

	BeforeEach(func() {
		mockCtrl = gomock.NewController(GinkgoT())
		channel = ChannelImpl{}

		channel.Banks = MakeBanks(2, 2, 2)
		for i := 0; i < 2; i++ {
			for j := 0; j < 2; j++ {
				for k := 0; k < 2; k++ {
					channel.Banks[i][j][k] = NewMockBank(mockCtrl)
				}
			}
		}
	})

	AfterEach(func() {
		mockCtrl.Finish()
	})

	It("should get ready command from the corresponding bank", func() {
		cmd := &signal.Command{
			Kind: signal.CmdKindRead,
			Location: addressmapping.Location{
				Rank:      0,
				BankGroup: 0,
				Bank:      0,
			},
		}
		retCmd := &signal.Command{
			Kind: signal.CmdKindActivate,
		}

		channel.Banks.GetBank(0, 0, 0).(*MockBank).EXPECT().
			GetReadyCommand(sim.VTimeInSec(10), cmd).
			Return(retCmd)

		finalCmd := channel.GetReadyCommand(10, cmd)

		Expect(finalCmd).To(Equal(retCmd))
	})

	It("should update the state of the corresponding bank", func() {
		cmd := &signal.Command{
			Kind: signal.CmdKindRead,
			Location: addressmapping.Location{
				Rank:      0,
				BankGroup: 0,
				Bank:      0,
			},
		}

		channel.Banks.GetBank(0, 0, 0).(*MockBank).EXPECT().
			StartCommand(sim.VTimeInSec(10), cmd)

		channel.StartCommand(10, cmd)
	})

	It("should update timing", func() {
		t := Timing{}

		t.SameBank = MakeTimeTable()
		t.SameBank[signal.CmdKindRead] = []TimeTableEntry{
			{signal.CmdKindRead, 1},
		}

		t.OtherBanksInBankGroup = MakeTimeTable()
		t.OtherBanksInBankGroup[signal.CmdKindRead] = []TimeTableEntry{
			{signal.CmdKindRead, 2},
		}

		t.SameRank = MakeTimeTable()
		t.SameRank[signal.CmdKindRead] = []TimeTableEntry{
			{signal.CmdKindRead, 3},
		}

		t.OtherRanks = MakeTimeTable()
		t.OtherRanks[signal.CmdKindRead] = []TimeTableEntry{
			{signal.CmdKindRead, 4},
		}

		channel.Timing = t

		cmd := &signal.Command{
			Kind: signal.CmdKindRead,
			Location: addressmapping.Location{
				Rank:      0,
				BankGroup: 0,
				Bank:      0,
			},
		}

		channel.Banks.GetBank(0, 0, 0).(*MockBank).EXPECT().
			UpdateTiming(signal.CmdKindRead, 1)
		channel.Banks.GetBank(0, 0, 1).(*MockBank).EXPECT().
			UpdateTiming(signal.CmdKindRead, 2)
		channel.Banks.GetBank(0, 1, 0).(*MockBank).EXPECT().
			UpdateTiming(signal.CmdKindRead, 3)
		channel.Banks.GetBank(0, 1, 1).(*MockBank).EXPECT().
			UpdateTiming(signal.CmdKindRead, 3)
		channel.Banks.GetBank(1, 0, 0).(*MockBank).EXPECT().
			UpdateTiming(signal.CmdKindRead, 4)
		channel.Banks.GetBank(1, 0, 1).(*MockBank).EXPECT().
			UpdateTiming(signal.CmdKindRead, 4)
		channel.Banks.GetBank(1, 1, 0).(*MockBank).EXPECT().
			UpdateTiming(signal.CmdKindRead, 4)
		channel.Banks.GetBank(1, 1, 1).(*MockBank).EXPECT().
			UpdateTiming(signal.CmdKindRead, 4)

		channel.UpdateTiming(10, cmd)
	})
})

var _ = Describe("ChannelImpl tFAW window", func() {
	var (
		channel *ChannelImpl
		next    map[*ChannelImpl]uint64
	)

	// readOf returns a read of bank (rank, bankGroup, bank), which needs an
	// activate while the bank is closed.
	readOf := func(rank, bankGroup, bank uint64) *signal.Command {
		return &signal.Command{
			ID:       "read",
			Kind:     signal.CmdKindRead,
			SubTrans: &signal.SubTransaction{ID: "st"},
			Location: addressmapping.Location{
				Rank: rank, BankGroup: bankGroup, Bank: bank, Row: 7,
			},
		}
	}

	// tickTo ticks c every cycle that it has not ticked yet, through cycle,
	// and reports whether the last tick made progress.
	tickTo := func(c *ChannelImpl, cycle uint64) (madeProgress bool) {
		for ; next[c] <= cycle; next[c]++ {
			madeProgress = c.Tick(atCycle(next[c]))
		}
		return madeProgress
	}

	// activateAt ticks c through cycle and returns the command that read
	// needs then, which it issues if it is an activate.
	activateAt := func(
		c *ChannelImpl, cycle uint64, read *signal.Command,
	) *signal.Command {
		tickTo(c, cycle)
		ready := c.GetReadyCommand(atCycle(cycle), read)
		if ready != nil && ready.Kind == signal.CmdKindActivate {
			c.StartCommand(atCycle(cycle), ready)
			c.UpdateTiming(atCycle(cycle), ready)
		}
		return ready
	}

	BeforeEach(func() {
		next = map[*ChannelImpl]uint64{}
		channel = &ChannelImpl{
			Banks:  makeTestBanks(0, 2, 2, 2),
			Timing: makeTestTiming(),
			Freq:   1 * sim.GHz,
			FAW:    15,
		}
	})

	It("should allow four activates per window", func() {
		reads := []*signal.Command{
			readOf(0, 0, 0), readOf(0, 0, 1), readOf(0, 1, 0),
			readOf(0, 1, 1), readOf(1, 0, 0), readOf(1, 0, 1),
		}
		for i, cycle := range []uint64{0, 4, 8, 12} {
			Expect(activateAt(channel, cycle, reads[i]).Kind).
				To(Equal(signal.CmdKindActivate), "cycle %d", cycle)
		}

		// The window holds 0, 4, 8, and 12, so the fifth activate waits
		// for cycle 0 + 15.
		Expect(activateAt(channel, 14, reads[4])).To(BeNil())
		Expect(channel.FAWDeniedActivates).To(Equal(uint64(1)))
		Expect(activateAt(channel, 15, reads[4]).Kind).
			To(Equal(signal.CmdKindActivate))

		// The window now holds 4, 8, 12, and 15.
		Expect(activateAt(channel, 18, reads[5])).To(BeNil())
		Expect(activateAt(channel, 19, reads[5]).Kind).
			To(Equal(signal.CmdKindActivate))
		Expect(channel.FAWDeniedActivates).To(Equal(uint64(2)))
	})

	It("should leave column commands to open rows ready", func() {
		reads := []*signal.Command{
			readOf(0, 0, 0), readOf(0, 1, 0), readOf(1, 0, 0),
			readOf(1, 1, 0), readOf(0, 0, 1),
		}
		for i, cycle := range []uint64{0, 1, 2, 3} {
			Expect(activateAt(channel, cycle, reads[i]).Kind).
				To(Equal(signal.CmdKindActivate), "cycle %d", cycle)
		}

		Expect(activateAt(channel, 6, reads[4])).To(BeNil())
		Expect(channel.GetReadyCommand(atCycle(6), reads[0]).Kind).
			To(Equal(signal.CmdKindRead))
		Expect(channel.FAWDeniedActivates).To(Equal(uint64(1)))
	})

	It("should keep ticking while the window is full", func() {
		channel.Timing = Timing{
			SameBank:              MakeTimeTable(),
			OtherBanksInBankGroup: MakeTimeTable(),
			SameRank:              MakeTimeTable(),
			OtherRanks:            MakeTimeTable(),
		}
		reads := []*signal.Command{
			readOf(0, 0, 0), readOf(0, 0, 1), readOf(0, 1, 0),
			readOf(0, 1, 1),
		}
		for i, cycle := range []uint64{0, 1, 2} {
			activateAt(channel, cycle, reads[i])
		}
		// The last activate ends at cycle 4; the window has room.
		Expect(tickTo(channel, 4)).To(BeTrue())
		Expect(tickTo(channel, 5)).To(BeFalse())

		activateAt(channel, 6, reads[3])
		for cycle := uint64(7); cycle < 15; cycle++ {
			Expect(tickTo(channel, cycle)).To(BeTrue(), "cycle %d", cycle)
		}
		// The window opens at cycle 0 + 15.
		Expect(tickTo(channel, 15)).To(BeFalse())
	})

	It("should keep the windows of pseudo channels apart", func() {
		other := &ChannelImpl{
			Banks:  makeTestBanks(1, 2, 2, 2),
			Timing: makeTestTiming(),
			Freq:   1 * sim.GHz,
			FAW:    15,
		}
		multi := &MultiChannel{Channels: []*ChannelImpl{channel, other}}
		acts := map[uint64]*signal.Command{
			0: readOf(0, 0, 0), 4: readOf(0, 0, 1),
			8: readOf(0, 1, 0), 12: readOf(1, 1, 1),
		}
		for cycle := uint64(0); cycle <= 14; cycle++ {
			multi.Tick(atCycle(cycle))
			read, ok := acts[cycle]
			if !ok {
				continue
			}
			ready := multi.GetReadyCommand(atCycle(cycle), read)
			Expect(ready.Kind).To(Equal(signal.CmdKindActivate))
			multi.StartCommand(atCycle(cycle), ready)
			multi.UpdateTiming(atCycle(cycle), ready)
		}
		Expect(multi.GetReadyCommand(atCycle(14), readOf(1, 0, 0))).
			To(BeNil())

		// Channel 1 has seen no activate.
		toOther := readOf(1, 0, 0)
		toOther.Channel = 1
		ready := multi.GetReadyCommand(atCycle(14), toOther)
		Expect(ready.Kind).To(Equal(signal.CmdKindActivate))
		multi.StartCommand(atCycle(14), ready)
		Expect(other.actCount).To(Equal(1))
		Expect(other.FAWDeniedActivates).To(BeZero())
		Expect(channel.actCount).To(Equal(4))
		Expect(channel.FAWDeniedActivates).To(Equal(uint64(1)))
	})

	It("should never hold back an activate without a window", func() {
		channel.FAW = 0
		channel.Freq = 0
		cycle := uint64(0)
		for rank := uint64(0); rank < 2; rank++ {
			for bankGroup := uint64(0); bankGroup < 2; bankGroup++ {
				for bank := uint64(0); bank < 2; bank++ {
					ready := activateAt(channel, cycle,
						readOf(rank, bankGroup, bank))
					Expect(ready.Kind).To(Equal(signal.CmdKindActivate),
						"cycle %d", cycle)
					cycle += 2
				}
			}
		}

		Expect(channel.actCount).To(BeZero())
		Expect(channel.FAWDeniedActivates).To(BeZero())
	})

	It("should panic without a clock", func() {
		channel.Freq = 0
		ready := channel.GetReadyCommand(0, readOf(0, 0, 0))
		Expect(func() { channel.StartCommand(0, ready) }).
			To(PanicWith(ContainSubstring("frequency")))
	})
})
