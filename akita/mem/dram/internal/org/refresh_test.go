package org

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/sarchlab/akita/v3/mem/dram/internal/addressmapping"
	"github.com/sarchlab/akita/v3/mem/dram/internal/signal"
	"github.com/sarchlab/akita/v3/sim"
)

// makeRefreshTestTiming returns the timing of the refresh tests: tRCD 3,
// tRAS 5 (35 with a long row), tRC 8, RDA to activate or refresh 6, tRP 2,
// and tRRD 1.
func makeRefreshTestTiming(tRAS int) Timing {
	t := Timing{
		SameBank:              MakeTimeTable(),
		OtherBanksInBankGroup: MakeTimeTable(),
		SameRank:              MakeTimeTable(),
		OtherRanks:            MakeTimeTable(),
	}
	t.SameBank[signal.CmdKindActivate] = []TimeTableEntry{
		{signal.CmdKindRead, 3},
		{signal.CmdKindReadPrecharge, 3},
		{signal.CmdKindPrecharge, tRAS},
		{signal.CmdKindActivate, 8},
	}
	t.SameRank[signal.CmdKindActivate] = []TimeTableEntry{
		{signal.CmdKindActivate, 1},
	}
	t.SameBank[signal.CmdKindReadPrecharge] = []TimeTableEntry{
		{signal.CmdKindActivate, 6},
		{signal.CmdKindRefresh, 6},
	}
	t.SameBank[signal.CmdKindPrecharge] = []TimeTableEntry{
		{signal.CmdKindActivate, 2},
		{signal.CmdKindRefresh, 2},
	}

	return t
}

func makeRefreshTestBanks(channel int) Banks {
	banks := makeTestBanks(channel, 1, 2, 2)
	for _, rank := range banks {
		for _, bankGroup := range rank {
			for _, bank := range bankGroup {
				bank.(*BankImpl).CmdCycles[signal.CmdKindReadPrecharge] = 3
			}
		}
	}

	return banks
}

// atCycle returns the time of a cycle of the 1-GHz test clock.
func atCycle(cycle uint64) sim.VTimeInSec {
	return sim.VTimeInSec(float64(cycle) * 1e-9)
}

var _ = Describe("AllBankRefresh", func() {
	var (
		channel *ChannelImpl
	)

	read := func(bank uint64) *signal.Command {
		return &signal.Command{
			ID:       "read",
			Kind:     signal.CmdKindReadPrecharge,
			SubTrans: &signal.SubTransaction{ID: "st"},
			Location: addressmapping.Location{Bank: bank, Row: 7},
		}
	}

	tick := func(cycle uint64) bool {
		return channel.Tick(atCycle(cycle))
	}

	// tickTo ticks every cycle from, through to.
	tickTo := func(from, to uint64) {
		for c := from; c <= to; c++ {
			tick(c)
		}
	}

	// readyKind returns the kind of the command that a read of bank needs
	// at cycle, or NumCmdKind if nothing is ready.
	readyKind := func(cycle uint64, bank uint64) signal.CommandKind {
		ready := channel.GetReadyCommand(atCycle(cycle), read(bank))
		if ready == nil {
			return signal.NumCmdKind
		}
		return ready.Kind
	}

	issue := func(cycle uint64, cmd *signal.Command) {
		channel.StartCommand(atCycle(cycle), cmd)
		channel.UpdateTiming(atCycle(cycle), cmd)
	}

	activate := func(cycle uint64, bank uint64) {
		cmd := read(bank)
		cmd.Kind = signal.CmdKindActivate
		issue(cycle, cmd)
	}

	// expectActivateBlockedUntil ticks every cycle from from and expects no
	// activate before cycle until and an activate at cycle until.
	expectActivateBlockedUntil := func(from, until uint64) {
		for c := from; c < until; c++ {
			if c > from {
				tick(c)
			}
			Expect(readyKind(c, 1)).To(Equal(signal.NumCmdKind),
				"cycle %d", c)
		}
		tick(until)
		Expect(readyKind(until, 1)).To(Equal(signal.CmdKindActivate))
	}

	BeforeEach(func() {
		channel = &ChannelImpl{
			Banks:  makeRefreshTestBanks(0),
			Timing: makeRefreshTestTiming(5),
			Freq:   1 * sim.GHz,
			Refresh: &AllBankRefresh{
				Interval: 100,
				Duration: 20,
			},
		}
	})

	It("should refresh at the interval and block activates for tRFC",
		func() {
			for c := uint64(0); c < 100; c++ {
				Expect(tick(c)).To(BeFalse())
			}
			Expect(channel.Refresh.Stats.Commands).To(BeZero())
			Expect(readyKind(99, 1)).To(Equal(signal.CmdKindActivate))

			Expect(tick(100)).To(BeTrue())

			Expect(channel.Refresh.Stats.Commands).To(Equal(uint64(1)))
			Expect(channel.RefreshPending(0)).To(BeFalse())
			expectActivateBlockedUntil(100, 120)
			Expect(channel.Refresh.Stats.WaitCycles).To(BeZero())
			Expect(channel.Refresh.Stats.Retroactive).To(BeZero())
		})

	It("should drain the open rows before it refreshes", func() {
		tickTo(0, 90)
		activate(90, 0)
		tickTo(91, 99)

		Expect(tick(100)).To(BeTrue())

		Expect(channel.RefreshPending(0)).To(BeTrue())
		Expect(channel.Refresh.Stats.Commands).To(BeZero())
		// No row opens, but the open row still serves its read.
		Expect(readyKind(100, 1)).To(Equal(signal.NumCmdKind))
		Expect(readyKind(100, 0)).To(Equal(signal.CmdKindReadPrecharge))
		issue(100, read(0))

		// The read ends at 103; the refresh waits for RDA to refresh (6).
		for c := uint64(101); c < 106; c++ {
			Expect(tick(c)).To(BeTrue())
			Expect(channel.RefreshPending(0)).To(BeTrue(), "cycle %d", c)
		}
		tick(106)

		Expect(channel.RefreshPending(0)).To(BeFalse())
		stats := channel.Refresh.Stats
		Expect(stats.Commands).To(Equal(uint64(1)))
		Expect(stats.WaitCycles).To(Equal(uint64(6)))
		Expect(stats.MaxWaitCycles).To(Equal(uint64(6)))
		Expect(stats.ForcedPrecharges).To(BeZero())
		expectActivateBlockedUntil(106, 126)

		// The schedule keeps its due times.
		tickTo(127, 199)
		Expect(tick(200)).To(BeTrue())
		Expect(channel.Refresh.Stats.Commands).To(Equal(uint64(2)))
	})

	It("should account for the refreshes due while the controller slept",
		func() {
			Expect(tick(50)).To(BeFalse())

			// Due at 100, it ends at 120.
			Expect(tick(112)).To(BeTrue())

			Expect(channel.Refresh.Stats.Commands).To(Equal(uint64(1)))
			Expect(channel.Refresh.Stats.Retroactive).To(Equal(uint64(1)))
			expectActivateBlockedUntil(112, 120)
			Expect(tick(121)).To(BeFalse())

			// Due at 200 and 300 and over; due at 400 and starting now.
			Expect(tick(400)).To(BeTrue())

			stats := channel.Refresh.Stats
			Expect(stats.Commands).To(Equal(uint64(4)))
			Expect(stats.Retroactive).To(Equal(uint64(3)))
			Expect(stats.WaitCycles).To(BeZero())
			expectActivateBlockedUntil(400, 420)
		})

	It("should not refresh retroactively over an open row", func() {
		Expect(tick(50)).To(BeFalse())
		activate(50, 0)
		tickTo(51, 60)

		Expect(tick(112)).To(BeTrue())

		Expect(channel.RefreshPending(0)).To(BeTrue())
		Expect(channel.Refresh.Stats.Retroactive).To(BeZero())
	})

	It("should force idle open rows closed after tRFC", func() {
		// The row may not close before 125 (tRAS 35).
		channel.Timing = makeRefreshTestTiming(35)
		tickTo(0, 90)
		activate(90, 0)
		tickTo(91, 119)

		Expect(channel.RefreshPending(0)).To(BeTrue())
		Expect(channel.Refresh.Stats.ForcedPrecharges).To(BeZero())
		open, _ := channel.ObservationBankState(0, 0, 0)
		Expect(open).To(BeTrue())

		// Pending for 20 cycles at 120; the precharge may start at 125
		// and takes tRP (2).
		tickTo(120, 124)
		Expect(channel.Refresh.Stats.ForcedPrecharges).To(BeZero())
		tick(125)
		Expect(channel.Refresh.Stats.ForcedPrecharges).To(Equal(uint64(1)))
		open, _ = channel.ObservationBankState(0, 0, 0)
		Expect(open).To(BeFalse())
		Expect(channel.RefreshPending(0)).To(BeTrue())

		tickTo(126, 127)

		Expect(channel.RefreshPending(0)).To(BeFalse())
		Expect(channel.Refresh.Stats.Commands).To(Equal(uint64(1)))
		Expect(channel.Refresh.Stats.WaitCycles).To(Equal(uint64(27)))
	})

	It("should not force a busy bank closed", func() {
		// The activate occupies the bank until 125.
		channel.Banks.GetBank(0, 0, 0).(*BankImpl).
			CmdCycles[signal.CmdKindActivate] = 30
		tickTo(0, 95)
		activate(95, 0)
		tickTo(96, 124)

		Expect(channel.RefreshPending(0)).To(BeTrue())
		Expect(channel.Refresh.Stats.ForcedPrecharges).To(BeZero())

		tickTo(125, 127)

		Expect(channel.Refresh.Stats.ForcedPrecharges).To(Equal(uint64(1)))
		Expect(channel.Refresh.Stats.Commands).To(Equal(uint64(1)))
		Expect(channel.Refresh.Stats.WaitCycles).To(Equal(uint64(27)))
	})

	It("should stagger the channels of the controllers", func() {
		Expect(StaggerRefreshPhase(3, 8, 0, 2, 100)).To(Equal(uint64(37)))
		Expect(StaggerRefreshPhase(3, 8, 1, 2, 100)).To(Equal(uint64(43)))
		Expect(StaggerRefreshPhase(0, 1, 1, 2, 3669)).
			To(Equal(uint64(1834)))

		multi := &MultiChannel{}
		for c := 0; c < 2; c++ {
			multi.Channels = append(multi.Channels, &ChannelImpl{
				Banks:  makeRefreshTestBanks(c),
				Timing: makeRefreshTestTiming(5),
				Freq:   1 * sim.GHz,
				Refresh: &AllBankRefresh{
					Interval: 100,
					Duration: 20,
					Phase:    StaggerRefreshPhase(3, 8, c, 2, 100),
				},
			})
		}
		commands := func() []uint64 {
			return []uint64{
				multi.Channels[0].Refresh.Stats.Commands,
				multi.Channels[1].Refresh.Stats.Commands,
			}
		}

		for c := uint64(0); c < 137; c++ {
			multi.Tick(atCycle(c))
		}
		Expect(commands()).To(Equal([]uint64{0, 0}))
		multi.Tick(atCycle(137))
		Expect(commands()).To(Equal([]uint64{1, 0}))
		for c := uint64(138); c < 143; c++ {
			multi.Tick(atCycle(c))
		}
		Expect(commands()).To(Equal([]uint64{1, 0}))
		multi.Tick(atCycle(143))
		Expect(commands()).To(Equal([]uint64{1, 1}))
	})

	It("should report a pending refresh per channel", func() {
		multi := &MultiChannel{Channels: []*ChannelImpl{
			{
				Banks:  makeRefreshTestBanks(0),
				Timing: makeRefreshTestTiming(5),
			},
			channel,
		}}
		tickTo(0, 90)
		activate(90, 0)
		tickTo(91, 100)

		Expect(multi.RefreshPending(0)).To(BeFalse())
		Expect(multi.RefreshPending(1)).To(BeTrue())
	})

	It("should do nothing without refresh", func() {
		channel.Refresh = nil
		channel.Freq = 0

		for c := uint64(0); c < 300; c++ {
			Expect(tick(c)).To(BeFalse())
		}
		Expect(channel.RefreshPending(0)).To(BeFalse())
		Expect(readyKind(300, 1)).To(Equal(signal.CmdKindActivate))
	})
})
