package org

import (
	"github.com/sarchlab/akita/v3/mem/dram/internal/addressmapping"
	"github.com/sarchlab/akita/v3/mem/dram/internal/signal"
	"github.com/sarchlab/akita/v3/sim"
)

// AllBankRefresh refreshes all the banks of one channel, such as one HBM2
// pseudo channel, every Interval cycles (tREFI). A refresh starts once every
// bank is precharged and idle, and then blocks activates in the channel for
// Duration cycles (tRFC). While a refresh is due but cannot start, it is
// pending: the channel refuses new activates, so that the open rows drain.
//
// The channel applies the refresh by itself; the refresh takes no
// command-bus slot. The controller does not tick while it is idle, so the
// channel tells time with its clock: a refresh that fell due while the
// controller slept with all banks closed happened at its due time, and the
// next tick blocks activates only for the part of tRFC that is still running.
type AllBankRefresh struct {
	// Interval (tREFI) and Duration (tRFC) are in channel cycles.
	Interval, Duration int

	// Phase delays the schedule, so that channels refresh at different
	// times. The first refresh is due at cycle Interval+Phase.
	Phase uint64

	Stats RefreshStats

	nextDue      uint64
	lastTick     uint64
	pendingSince uint64
	ticked       bool
	pending      bool
}

// RefreshStats counts the refreshes of one channel.
type RefreshStats struct {
	// Commands counts the refreshes, including the retroactive ones.
	Commands uint64
	// Retroactive counts the refreshes that fell due while the controller
	// was idle and did not tick.
	Retroactive uint64
	// WaitCycles sums, and MaxWaitCycles is the maximum of, the cycles from
	// a refresh falling due to its start, while the open rows drain.
	WaitCycles    uint64
	MaxWaitCycles uint64
	// ForcedPrecharges counts the rows closed by the safety valve: once a
	// refresh has been pending for Duration cycles, the channel precharges
	// the banks that are open but idle. The scheduler closes every row while
	// a refresh is pending, so the count stays 0 in a normal stream.
	ForcedPrecharges uint64
}

// StaggerRefreshPhase returns the refresh phase of channel channel (of
// numChannels) in the controller at slot (of numSlots controllers) that
// spreads the refreshes of all those channels evenly over one interval.
func StaggerRefreshPhase(
	slot, numSlots, channel, numChannels, interval int,
) uint64 {
	index := uint64(slot*numChannels + channel)
	return index * uint64(interval) / uint64(numSlots*numChannels)
}

// RefreshPending reports whether the channel waits to refresh: a refresh is
// due, but some bank is still open or busy. The channel then refuses
// activates, and the scheduler must not keep rows open.
func (cs *ChannelImpl) RefreshPending(_ uint64) bool {
	return cs.Refresh != nil && cs.Refresh.pending
}

// tickRefresh advances the refresh schedule. It runs after the banks have
// ticked and before the controller issues the commands of the cycle.
func (cs *ChannelImpl) tickRefresh(now sim.VTimeInSec) (madeProgress bool) {
	if cs.Freq == 0 {
		panic("all-bank refresh needs the channel frequency")
	}

	r := cs.Refresh
	nowCycle := cs.Freq.Cycle(now)
	if !r.ticked {
		r.nextDue = uint64(r.Interval) + r.Phase
	}
	lastTick := r.lastTick
	slept := !r.ticked || nowCycle > lastTick+1
	r.lastTick, r.ticked = nowCycle, true

	for !r.pending && r.nextDue <= nowCycle {
		if slept && r.nextDue > lastTick && r.nextDue < nowCycle &&
			cs.allBanksClosedAndIdle() {
			// The controller was idle, with all banks closed, when the
			// refresh fell due, so the refresh happened then.
			madeProgress = cs.refreshRetroactively(nowCycle) || madeProgress
			continue
		}

		r.pending = true
		r.pendingSince = r.nextDue
	}

	if !r.pending {
		return madeProgress
	}

	if cs.readyToRefresh() {
		cs.startRefresh(nowCycle)
		return true
	}

	if nowCycle-r.pendingSince >= uint64(r.Duration) {
		cs.forcePrechargeIdleOpenBanks(now)
	}

	// Keep ticking while the refresh waits; the safety valve bounds the
	// wait.
	return true
}

// refreshRetroactively accounts for the refresh that fell due while the
// controller slept and blocks activates for the rest of its tRFC.
func (cs *ChannelImpl) refreshRetroactively(nowCycle uint64) (blocked bool) {
	r := cs.Refresh
	end := r.nextDue + uint64(r.Duration)
	if end > nowCycle {
		cs.blockAllBanks(int(end - nowCycle))
		blocked = true
	}

	r.Stats.Commands++
	r.Stats.Retroactive++
	r.nextDue += uint64(r.Interval)

	return blocked
}

// startRefresh starts the pending refresh. The schedule keeps its due
// times, so a wait does not shift the following refreshes.
func (cs *ChannelImpl) startRefresh(nowCycle uint64) {
	r := cs.Refresh
	cs.blockAllBanks(r.Duration)

	wait := nowCycle - r.pendingSince
	r.Stats.Commands++
	r.Stats.WaitCycles += wait
	if wait > r.Stats.MaxWaitCycles {
		r.Stats.MaxWaitCycles = wait
	}

	r.pending = false
	r.nextDue += uint64(r.Interval)
}

// blockAllBanks keeps every bank from activating or refreshing for the
// given number of cycles. The banks are closed, so a column command needs an
// activate first.
func (cs *ChannelImpl) blockAllBanks(cycles int) {
	cs.ForEachBank(func(b Bank) {
		b.UpdateTiming(signal.CmdKindActivate, cycles)
		b.UpdateTiming(signal.CmdKindRefresh, cycles)
		b.UpdateTiming(signal.CmdKindRefreshBank, cycles)
	})
}

func (cs *ChannelImpl) allBanksClosedAndIdle() bool {
	for _, rank := range cs.Banks {
		for _, bankGroup := range rank {
			for _, bank := range bankGroup {
				b := refreshedBank(bank)
				if b.state != BankStateClosed || !b.idle() {
					return false
				}
			}
		}
	}

	return true
}

func (cs *ChannelImpl) readyToRefresh() bool {
	for _, rank := range cs.Banks {
		for _, bankGroup := range rank {
			for _, bank := range bankGroup {
				if !refreshedBank(bank).readyToRefresh() {
					return false
				}
			}
		}
	}

	return true
}

// forcePrechargeIdleOpenBanks is the safety valve of a refresh that has been
// pending for tRFC cycles. It closes every open bank that executes no
// command and whose row may close (tRAS, tRTP, and tWR have passed), with the
// timing of a precharge command.
func (cs *ChannelImpl) forcePrechargeIdleOpenBanks(now sim.VTimeInSec) {
	for i, rank := range cs.Banks {
		for j, bankGroup := range rank {
			for k, bank := range bankGroup {
				if !refreshedBank(bank).forcePrecharge() {
					continue
				}

				cs.Refresh.Stats.ForcedPrecharges++
				cs.updateAllBankTiming(now, &signal.Command{
					Kind: signal.CmdKindPrecharge,
					Location: addressmapping.Location{
						Rank:      uint64(i),
						BankGroup: uint64(j),
						Bank:      uint64(k),
					},
				})
			}
		}
	}
}

// refreshedBank returns the bank state that refresh needs.
func refreshedBank(bank Bank) *BankImpl {
	b, ok := bank.(*BankImpl)
	if !ok {
		panic("all-bank refresh needs BankImpl banks")
	}

	return b
}

// idle reports whether the bank executes no command.
func (b *BankImpl) idle() bool {
	return b.currentCmd == nil && len(b.inflight) == 0
}

// readyToRefresh reports whether the bank is closed and idle and could
// activate, so that its precharge time (tRP) and the row cycle time of its
// last activate (tRC) have passed.
func (b *BankImpl) readyToRefresh() bool {
	return b.state == BankStateClosed && b.idle() &&
		b.cyclesToCmdAvailable[signal.CmdKindActivate] == 0 &&
		b.cyclesToCmdAvailable[signal.CmdKindRefresh] == 0
}

// forcePrecharge closes the row of an open, idle bank that may precharge. It
// reports whether it closed the row.
func (b *BankImpl) forcePrecharge() bool {
	if b.state != BankStateOpen || !b.idle() ||
		b.cyclesToCmdAvailable[signal.CmdKindPrecharge] > 0 {
		return false
	}

	b.state = BankStateClosed

	return true
}
