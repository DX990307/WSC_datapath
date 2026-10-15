package org

import (
	"github.com/sarchlab/akita/v3/mem/dram/internal/signal"
	"github.com/sarchlab/akita/v3/sim"
)

// Banks is indexed by rank, bank-group, bank.
type Banks [][][]Bank

// GetSize returns the number of ranks, bank-groups, and banks.
func (b Banks) GetSize() (rank, bankGroup, bank uint64) {
	return uint64(len(b)), uint64(len(b[0])), uint64(len(b[0][0]))
}

// GetBank returns a specific bank identified by the rank index, bank-group
// index, and the bank index.
func (b Banks) GetBank(rank, bankGroup, bank uint64) Bank {
	return b[rank][bankGroup][bank]
}

// MakeBanks create all the banks.
func MakeBanks(numRank, numBankGroup, numBank uint64) Banks {
	b := make(Banks, numRank)

	for i := uint64(0); i < numRank; i++ {
		b[i] = make([][]Bank, numBankGroup)

		for j := uint64(0); j < numBankGroup; j++ {
			b[i][j] = make([]Bank, numBank)

			for k := uint64(0); k < numBank; k++ {
				b[i][j][k] = NewBankImpl("")
			}
		}
	}

	return b
}

// A Channel is a group of ranks.
type Channel interface {
	GetReadyCommand(
		now sim.VTimeInSec,
		cmd *signal.Command,
	) *signal.Command

	StartCommand(
		now sim.VTimeInSec,
		cmd *signal.Command,
	)

	UpdateTiming(
		now sim.VTimeInSec,
		cmd *signal.Command,
	)

	Tick(now sim.VTimeInSec) (madeProgress bool)
}

// ChannelImpl implements a Channel.
type ChannelImpl struct {
	Banks  Banks
	Timing Timing

	// Freq is the channel clock. Refresh uses it to count the cycles that
	// the controller slept through, and the tFAW window to time activates.
	Freq sim.Freq

	// Refresh refreshes all the banks every tREFI. Nil models no refresh.
	Refresh *AllBankRefresh

	// FAW (tFAW, in cycles) limits the channel to fawActivates activates
	// in any FAW-cycle window, counting the activates of all its ranks, as
	// an HBM2 pseudo channel counts those of both stack IDs. 0 models no
	// window.
	FAW int

	// FAWDeniedActivates counts the GetReadyCommand queries that found an
	// activate ready in its bank but refused it because the tFAW window was
	// full. A waiting activate counts once per query.
	FAWDeniedActivates uint64

	// actCycles holds the cycles of the last actCount activates, up to
	// fawActivates. Once it is full, actNext indexes the oldest one, which
	// the next activate replaces.
	actCycles [fawActivates]uint64
	actCount  int
	actNext   int
}

// fawActivates is the number of activates allowed in a tFAW window.
const fawActivates = 4

// ObservationBankState returns the current open-row state of a physical bank.
// It is a read-only helper for DRAM profiling.
func (cs *ChannelImpl) ObservationBankState(
	rank, bankGroup, bank uint64,
) (open bool, row uint64) {
	b, ok := cs.Banks.GetBank(rank, bankGroup, bank).(*BankImpl)
	if !ok {
		return false, 0
	}
	return b.ObservationState()
}

// ObservationColumnReady checks row-hit readiness without invoking
// GetReadyCommand, which would allocate a clone and consume a simulator ID.
func (cs *ChannelImpl) ObservationColumnReady(cmd *signal.Command) bool {
	if cmd == nil {
		return false
	}
	b, ok := cs.Banks.
		GetBank(cmd.Rank, cmd.BankGroup, cmd.Bank).(*BankImpl)
	if !ok {
		return false
	}
	return b.ObservationColumnReady(cmd)
}

// Tick updates the internal states of the channel.
func (cs *ChannelImpl) Tick(now sim.VTimeInSec) (madeProgress bool) {
	for i := 0; i < len(cs.Banks); i++ {
		for j := 0; j < len(cs.Banks[0]); j++ {
			for k := 0; k < len(cs.Banks[0][0]); k++ {
				madeProgress = cs.Banks[i][j][k].Tick(now) || madeProgress
			}
		}
	}

	if cs.Refresh != nil {
		madeProgress = cs.tickRefresh(now) || madeProgress
	}

	if cs.FAW > 0 && !cs.fawAllows(now) {
		// Keep ticking while the window is full, so that an activate it
		// holds back is not left to a sleeping ticker.
		madeProgress = true
	}

	return madeProgress
}

// GetReadyCommand returns the command that is ready to start in the channel.
// While a refresh is pending, no activate is ready, so that the open rows
// drain; column commands to open rows stay ready. No activate is ready
// either while the tFAW window is full.
func (cs *ChannelImpl) GetReadyCommand(
	now sim.VTimeInSec,
	cmd *signal.Command,
) *signal.Command {
	readyCmd := cs.Banks.
		GetBank(cmd.Rank, cmd.BankGroup, cmd.Bank).
		GetReadyCommand(now, cmd)

	if readyCmd == nil || readyCmd.Kind != signal.CmdKindActivate {
		return readyCmd
	}

	if cs.RefreshPending(0) {
		return nil
	}

	if cs.FAW > 0 && !cs.fawAllows(now) {
		cs.FAWDeniedActivates++
		return nil
	}

	return readyCmd
}

// StartCommand starts a command in a bank.
func (cs *ChannelImpl) StartCommand(now sim.VTimeInSec, cmd *signal.Command) {
	cs.Banks.
		GetBank(cmd.Rank, cmd.BankGroup, cmd.Bank).
		StartCommand(now, cmd)

	if cs.FAW > 0 && cmd.Kind == signal.CmdKindActivate {
		cs.recordActivate(now)
	}
}

// fawAllows reports whether the tFAW window admits an activate now: fewer
// than fawActivates activates have started, or the oldest of the last
// fawActivates started at least FAW cycles ago.
func (cs *ChannelImpl) fawAllows(now sim.VTimeInSec) bool {
	if cs.actCount < fawActivates {
		return true
	}

	oldest := cs.actCycles[cs.actNext]

	return cs.fawCycle(now) >= oldest+uint64(cs.FAW)
}

// recordActivate adds an activate that starts now to the tFAW window.
func (cs *ChannelImpl) recordActivate(now sim.VTimeInSec) {
	cs.actCycles[cs.actNext] = cs.fawCycle(now)
	cs.actNext = (cs.actNext + 1) % fawActivates
	if cs.actCount < fawActivates {
		cs.actCount++
	}
}

// fawCycle returns the channel cycle of now. The window counts in absolute
// cycles, because the controller does not tick while it is idle.
func (cs *ChannelImpl) fawCycle(now sim.VTimeInSec) uint64 {
	if cs.Freq == 0 {
		panic("the tFAW window needs the channel frequency")
	}

	return cs.Freq.Cycle(now)
}

// UpdateTiming updates the timing-related states of the banks.
func (cs *ChannelImpl) UpdateTiming(now sim.VTimeInSec, cmd *signal.Command) {
	switch cmd.Kind {
	case signal.CmdKindActivate:
		fallthrough
	case signal.CmdKindRead, signal.CmdKindReadPrecharge,
		signal.CmdKindWrite, signal.CmdKindWritePrecharge,
		signal.CmdKindPrecharge, signal.CmdKindRefreshBank:
		cs.updateAllBankTiming(now, cmd)
	}
}

func (cs *ChannelImpl) updateAllBankTiming(
	now sim.VTimeInSec,
	cmd *signal.Command,
) {
	rank, bankGroup, bank := cs.Banks.GetSize()
	for i := uint64(0); i < rank; i++ {
		for j := uint64(0); j < bankGroup; j++ {
			for k := uint64(0); k < bank; k++ {
				cs.updateBankTiming(now, cmd, i, j, k)
			}
		}
	}
}

func (cs *ChannelImpl) updateBankTiming(
	now sim.VTimeInSec,
	cmd *signal.Command,
	rank, bankGroup, bank uint64,
) {
	timingTable := cs.Timing.OtherRanks
	if cmd.Rank == rank {
		timingTable = cs.Timing.SameRank

		if cmd.BankGroup == bankGroup {
			timingTable = cs.Timing.OtherBanksInBankGroup

			if cmd.Bank == bank {
				timingTable = cs.Timing.SameBank
			}
		}
	}

	for _, entry := range timingTable[cmd.Kind] {
		cs.Banks.GetBank(rank, bankGroup, bank).
			UpdateTiming(entry.NextCmdKind, entry.MinCycleInBetween)
	}
}
