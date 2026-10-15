package org

import (
	"fmt"

	gomock "github.com/golang/mock/gomock"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/sarchlab/akita/v3/mem/dram/internal/addressmapping"
	"github.com/sarchlab/akita/v3/mem/dram/internal/signal"
	"github.com/sarchlab/akita/v3/sim"
)

func makeTestBanks(channel int, numRank, numBankGroup, numBank int) Banks {
	banks := make(Banks, numRank)
	for i := range banks {
		banks[i] = make([][]Bank, numBankGroup)
		for j := range banks[i] {
			banks[i][j] = make([]Bank, numBank)
			for k := range banks[i][j] {
				bank := NewBankImpl(
					fmt.Sprintf("Channel[%d].Bank[%d][%d][%d]",
						channel, i, j, k))
				bank.CmdCycles[signal.CmdKindActivate] = 2
				bank.CmdCycles[signal.CmdKindRead] = 2
				banks[i][j][k] = bank
			}
		}
	}

	return banks
}

func makeTestTiming() Timing {
	t := Timing{
		SameBank:              MakeTimeTable(),
		OtherBanksInBankGroup: MakeTimeTable(),
		SameRank:              MakeTimeTable(),
		OtherRanks:            MakeTimeTable(),
	}
	t.SameBank[signal.CmdKindActivate] = []TimeTableEntry{
		{signal.CmdKindRead, 3},
	}
	t.OtherBanksInBankGroup[signal.CmdKindActivate] = []TimeTableEntry{
		{signal.CmdKindActivate, 2},
	}
	t.SameRank[signal.CmdKindActivate] = []TimeTableEntry{
		{signal.CmdKindActivate, 1},
	}
	t.OtherRanks[signal.CmdKindActivate] = []TimeTableEntry{
		{signal.CmdKindActivate, 1},
	}
	t.SameBank[signal.CmdKindRead] = []TimeTableEntry{
		{signal.CmdKindRead, 4},
	}
	t.OtherRanks[signal.CmdKindRead] = []TimeTableEntry{
		{signal.CmdKindRead, 1},
	}

	return t
}

func bankAt(m *MultiChannel, ch, rank, bankGroup, bank uint64) *BankImpl {
	return m.Channels[ch].Banks.GetBank(rank, bankGroup, bank).(*BankImpl)
}

var _ = Describe("MultiChannel", func() {
	var (
		timing  Timing
		channel *MultiChannel
	)

	BeforeEach(func() {
		timing = makeTestTiming()
		channel = &MultiChannel{}
		for c := 0; c < 2; c++ {
			channel.Channels = append(channel.Channels, &ChannelImpl{
				Banks:  makeTestBanks(c, 2, 2, 2),
				Timing: timing,
			})
		}
	})

	It("should route commands by the command's channel", func() {
		mockCtrl := gomock.NewController(GinkgoT())
		defer mockCtrl.Finish()
		for _, c := range channel.Channels {
			c.Banks = MakeBanks(2, 2, 2)
			for i := 0; i < 2; i++ {
				for j := 0; j < 2; j++ {
					for k := 0; k < 2; k++ {
						c.Banks[i][j][k] = NewMockBank(mockCtrl)
					}
				}
			}
		}
		cmd := &signal.Command{
			Kind: signal.CmdKindRead,
			Location: addressmapping.Location{
				Channel: 1, Rank: 1, BankGroup: 0, Bank: 1,
			},
		}
		ready := &signal.Command{Kind: signal.CmdKindActivate}
		target := channel.Channels[1].Banks.GetBank(1, 0, 1).(*MockBank)
		target.EXPECT().GetReadyCommand(sim.VTimeInSec(10), cmd).
			Return(ready)
		target.EXPECT().StartCommand(sim.VTimeInSec(10), cmd)

		Expect(channel.GetReadyCommand(10, cmd)).To(BeIdenticalTo(ready))
		channel.StartCommand(10, cmd)

		// Only the banks of channel 1 see the timing update.
		channel.Channels[1].ForEachBank(func(b Bank) {
			b.(*MockBank).EXPECT().UpdateTiming(gomock.Any(), gomock.Any()).
				AnyTimes()
		})
		channel.UpdateTiming(10, cmd)

		// Tick reaches every bank of every channel.
		channel.ForEachBank(func(b Bank) {
			b.(*MockBank).EXPECT().Tick(sim.VTimeInSec(11)).Return(false)
		})
		Expect(channel.Tick(11)).To(BeFalse())
	})

	It("should keep the bank state and timing of the channels apart", func() {
		act := &signal.Command{
			ID:       "act",
			Kind:     signal.CmdKindActivate,
			SubTrans: &signal.SubTransaction{ID: "st"},
			Location: addressmapping.Location{Row: 7},
		}
		channel.StartCommand(0, act)
		channel.UpdateTiming(0, act)
		for i := 0; i < 3; i++ {
			channel.Tick(sim.VTimeInSec(i+1) * 1e-9)
		}
		read := &signal.Command{
			ID:       "read",
			Kind:     signal.CmdKindRead,
			SubTrans: &signal.SubTransaction{ID: "st"},
			Location: addressmapping.Location{Row: 7},
		}
		Expect(channel.GetReadyCommand(3e-9, read)).NotTo(BeNil())
		channel.StartCommand(3e-9, read)
		channel.UpdateTiming(3e-9, read)

		open, row := channel.ObservationBankStateIn(0, 0, 0, 0)
		Expect(open).To(BeTrue())
		Expect(row).To(Equal(uint64(7)))
		open, _ = channel.ObservationBankStateIn(1, 0, 0, 0)
		Expect(open).To(BeFalse())
		Expect(bankAt(channel, 0, 0, 0, 0).
			cyclesToCmdAvailable[signal.CmdKindRead]).To(Equal(4))
		Expect(bankAt(channel, 0, 1, 0, 0).
			cyclesToCmdAvailable[signal.CmdKindRead]).To(Equal(1))
		channel.Channels[1].ForEachBank(func(b Bank) {
			Expect(b.(*BankImpl).cyclesToCmdAvailable).
				To(Equal([signal.NumCmdKind]int{}), b.Name())
		})

		// The same location in channel 1 still needs its own activate.
		other := &signal.Command{
			ID:       "other",
			Kind:     signal.CmdKindRead,
			SubTrans: &signal.SubTransaction{ID: "st2"},
			Location: addressmapping.Location{Channel: 1, Row: 7},
		}
		ready := channel.GetReadyCommand(3e-9, other)
		Expect(ready.Kind).To(Equal(signal.CmdKindActivate))
		Expect(channel.ObservationColumnReady(other)).To(BeFalse())
	})

	It("should observe the bank state of the requested channel", func() {
		act := &signal.Command{
			ID:       "act",
			Kind:     signal.CmdKindActivate,
			SubTrans: &signal.SubTransaction{ID: "st"},
			Location: addressmapping.Location{
				Channel: 1, Rank: 0, BankGroup: 1, Bank: 1, Row: 9,
			},
		}
		channel.StartCommand(0, act)
		channel.UpdateTiming(0, act)
		for i := 0; i < 3; i++ {
			channel.Tick(sim.VTimeInSec(i+1) * 1e-9)
		}

		open, row := channel.ObservationBankStateIn(1, 0, 1, 1)
		Expect(open).To(BeTrue())
		Expect(row).To(Equal(uint64(9)))
		open, _ = channel.ObservationBankStateIn(0, 0, 1, 1)
		Expect(open).To(BeFalse())
		open, _ = channel.ObservationBankStateIn(2, 0, 1, 1)
		Expect(open).To(BeFalse())

		hit := &signal.Command{
			Kind:     signal.CmdKindRead,
			Location: act.Location,
		}
		Expect(channel.ObservationColumnReady(hit)).To(BeTrue())
		hit.Channel = 0
		Expect(channel.ObservationColumnReady(hit)).To(BeFalse())
		hit.Channel = 2
		Expect(channel.ObservationColumnReady(hit)).To(BeFalse())
		Expect(channel.ObservationColumnReady(nil)).To(BeFalse())
	})

	It("should visit the banks of every channel in order", func() {
		var names []string
		channel.ForEachBank(func(b Bank) { names = append(names, b.Name()) })

		Expect(names).To(HaveLen(16))
		Expect(names[0]).To(Equal("Channel[0].Bank[0][0][0]"))
		Expect(names[1]).To(Equal("Channel[0].Bank[0][0][1]"))
		Expect(names[7]).To(Equal("Channel[0].Bank[1][1][1]"))
		Expect(names[8]).To(Equal("Channel[1].Bank[0][0][0]"))
		Expect(names[15]).To(Equal("Channel[1].Bank[1][1][1]"))
	})

	It("should report no pending refresh", func() {
		Expect(channel.RefreshPending(0)).To(BeFalse())
		Expect(channel.RefreshPending(1)).To(BeFalse())
		Expect(channel.RefreshPending(2)).To(BeFalse())
	})
})

var _ = Describe("ChannelImpl observation", func() {
	It("should visit the banks in rank, bank-group, bank order", func() {
		channel := &ChannelImpl{Banks: makeTestBanks(0, 2, 2, 2)}
		var names []string
		channel.ForEachBank(func(b Bank) { names = append(names, b.Name()) })

		expected := []string{}
		for i := 0; i < 2; i++ {
			for j := 0; j < 2; j++ {
				for k := 0; k < 2; k++ {
					expected = append(expected, fmt.Sprintf(
						"Channel[0].Bank[%d][%d][%d]", i, j, k))
				}
			}
		}
		Expect(names).To(Equal(expected))
	})

	It("should ignore the channel index of the bank state query", func() {
		channel := &ChannelImpl{
			Banks:  makeTestBanks(0, 1, 1, 2),
			Timing: makeTestTiming(),
		}
		act := &signal.Command{
			ID:       "act",
			Kind:     signal.CmdKindActivate,
			SubTrans: &signal.SubTransaction{ID: "st"},
			Location: addressmapping.Location{Bank: 1, Row: 3},
		}
		channel.StartCommand(0, act)

		for _, ch := range []uint64{0, 5} {
			open, row := channel.ObservationBankStateIn(ch, 0, 0, 1)
			Expect(open).To(BeTrue())
			Expect(row).To(Equal(uint64(3)))
		}
		Expect(channel.RefreshPending(0)).To(BeFalse())
	})
})
