package org

import (
	"github.com/sarchlab/akita/v3/mem/dram/internal/signal"
	"github.com/sarchlab/akita/v3/sim"
)

// MultiChannel is a set of independent channels behind one controller, such
// as the two pseudo channels of an HBM2 channel. Commands go to
// Channels[cmd.Channel]; channels share no bank state and no timing.
type MultiChannel struct {
	Channels []*ChannelImpl
}

// GetReadyCommand returns the command that is ready to start in the channel
// of cmd.
func (m *MultiChannel) GetReadyCommand(
	now sim.VTimeInSec,
	cmd *signal.Command,
) *signal.Command {
	return m.Channels[cmd.Channel].GetReadyCommand(now, cmd)
}

// StartCommand starts a command in a bank of the channel of cmd.
func (m *MultiChannel) StartCommand(now sim.VTimeInSec, cmd *signal.Command) {
	m.Channels[cmd.Channel].StartCommand(now, cmd)
}

// UpdateTiming updates the timing-related states of the banks in the channel
// of cmd. The banks of the other channels are not affected.
func (m *MultiChannel) UpdateTiming(now sim.VTimeInSec, cmd *signal.Command) {
	m.Channels[cmd.Channel].UpdateTiming(now, cmd)
}

// Tick updates the internal states of all the channels.
func (m *MultiChannel) Tick(now sim.VTimeInSec) (madeProgress bool) {
	for _, channel := range m.Channels {
		madeProgress = channel.Tick(now) || madeProgress
	}

	return madeProgress
}

// ForEachBank visits the banks channel by channel, each in rank, bank-group,
// bank order.
func (m *MultiChannel) ForEachBank(visit func(bank Bank)) {
	for _, channel := range m.Channels {
		channel.ForEachBank(visit)
	}
}

// ObservationBankStateIn returns the open-row state of a physical bank.
func (m *MultiChannel) ObservationBankStateIn(
	channel, rank, bankGroup, bank uint64,
) (open bool, row uint64) {
	if channel >= uint64(len(m.Channels)) {
		return false, 0
	}

	return m.Channels[channel].ObservationBankState(rank, bankGroup, bank)
}

// ObservationColumnReady checks row-hit readiness in the channel of cmd
// without cloning the command.
func (m *MultiChannel) ObservationColumnReady(cmd *signal.Command) bool {
	if cmd == nil || cmd.Channel >= uint64(len(m.Channels)) {
		return false
	}

	return m.Channels[cmd.Channel].ObservationColumnReady(cmd)
}

// RefreshPending reports whether the given channel waits to refresh.
func (m *MultiChannel) RefreshPending(channel uint64) bool {
	if channel >= uint64(len(m.Channels)) {
		return false
	}

	return m.Channels[channel].RefreshPending(0)
}
