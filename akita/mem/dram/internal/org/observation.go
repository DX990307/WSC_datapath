package org

import "github.com/sarchlab/akita/v3/mem/dram/internal/signal"

// The interfaces in this file are optional capabilities of a Channel. The
// controller and the command queue discover them with type assertions and
// skip the work when an assertion fails, so a mismatch would silently drop
// observer events instead of failing. The assertions at the end of the file
// therefore pin every implementation at compile time.

// BankVisitor can visit every bank of a channel.
type BankVisitor interface {
	ForEachBank(visit func(bank Bank))
}

// BankStateObserver reports the open-row state of a physical bank. It is a
// read-only helper for DRAM profiling.
type BankStateObserver interface {
	ObservationBankStateIn(
		channel, rank, bankGroup, bank uint64,
	) (open bool, row uint64)
}

// ColumnReadyObserver checks row-hit readiness of a column command without
// invoking GetReadyCommand, which would allocate a clone and consume a
// simulator ID.
type ColumnReadyObserver interface {
	ObservationColumnReady(cmd *signal.Command) bool
}

// RefreshPendingQuerier reports whether a channel waits to refresh, so that
// the scheduler can stop keeping rows open in it.
type RefreshPendingQuerier interface {
	RefreshPending(channel uint64) bool
}

var (
	_ BankVisitor           = (*ChannelImpl)(nil)
	_ BankStateObserver     = (*ChannelImpl)(nil)
	_ ColumnReadyObserver   = (*ChannelImpl)(nil)
	_ RefreshPendingQuerier = (*ChannelImpl)(nil)
	_ Channel               = (*ChannelImpl)(nil)
	_ BankVisitor           = (*MultiChannel)(nil)
	_ BankStateObserver     = (*MultiChannel)(nil)
	_ ColumnReadyObserver   = (*MultiChannel)(nil)
	_ RefreshPendingQuerier = (*MultiChannel)(nil)
	_ Channel               = (*MultiChannel)(nil)
)

// ForEachBank visits the banks in rank, bank-group, bank order.
func (cs *ChannelImpl) ForEachBank(visit func(bank Bank)) {
	for _, rank := range cs.Banks {
		for _, bankGroup := range rank {
			for _, bank := range bankGroup {
				visit(bank)
			}
		}
	}
}

// ObservationBankStateIn returns the open-row state of a physical bank. A
// ChannelImpl is a single channel, so the channel index is ignored.
func (cs *ChannelImpl) ObservationBankStateIn(
	_, rank, bankGroup, bank uint64,
) (open bool, row uint64) {
	return cs.ObservationBankState(rank, bankGroup, bank)
}
