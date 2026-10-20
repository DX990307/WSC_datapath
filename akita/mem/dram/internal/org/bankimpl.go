package org

import (
	"github.com/sarchlab/akita/v3/mem/dram/internal/signal"
	"github.com/sarchlab/akita/v3/sim"
	"github.com/sarchlab/akita/v3/tracing"
)

// BankState represents the current state of a bank.
type BankState int

// A list of possible bank states.
const (
	BankStateOpen BankState = iota
	BankStateClosed
	BankStateSRef
	BankStatePD
	BankStateInvalid
)

// BankImpl provides a basic implementation of a bank.
type BankImpl struct {
	sim.HookableBase
	BankName             string
	state                BankState
	currentCmd           *signal.Command
	openRow              uint64
	CmdCycles            [signal.NumCmdKind]int
	cyclesToCmdAvailable [signal.NumCmdKind]int
	commandCompleted     func(now sim.VTimeInSec, cmd *signal.Command)

	// PipelineColumnCommands lets read and write commands overlap in the
	// bank, as their data transfers do in a real device. A column command
	// then does not occupy the bank until its data transfer ends: the next
	// command waits only for the timing constraints (for example, tCCDL
	// between two reads of the open row). Activate and precharge still
	// occupy the bank. False lets a bank execute one command at a time.
	PipelineColumnCommands bool
	inflight               []*signal.Command
}

// NewBankImpl creates a new BankImpl.
func NewBankImpl(name string) *BankImpl {
	b := &BankImpl{
		BankName: name,
		state:    BankStateClosed,
	}

	return b
}

// Name returns the name of the Bank.
func (b *BankImpl) Name() string {
	return b.BankName
}

// Tick updates the internal states of the bank.
func (b *BankImpl) Tick(now sim.VTimeInSec) (madeProgress bool) {
	madeProgress = b.countDownCurrentCmd(now) || madeProgress
	madeProgress = b.countDownInflight(now) || madeProgress
	madeProgress = b.countDownTiming() || madeProgress

	return madeProgress
}

func (b *BankImpl) countDownTiming() (madeProgress bool) {
	for i := signal.CommandKind(0); i < signal.NumCmdKind; i++ {
		if b.cyclesToCmdAvailable[i] > 0 {
			b.cyclesToCmdAvailable[i]--
			madeProgress = true
		}
	}
	return madeProgress
}

func (b *BankImpl) countDownCurrentCmd(now sim.VTimeInSec) (madeProgress bool) {
	if b.currentCmd != nil {
		b.currentCmd.CycleLeft--
		if b.currentCmd.CycleLeft <= 0 {
			b.completeCurrentCmd(now)
		}

		madeProgress = true
	}

	return madeProgress
}

// countDownInflight counts down the pipelined column commands and completes
// those whose data transfer has ended, in issue order.
func (b *BankImpl) countDownInflight(now sim.VTimeInSec) (madeProgress bool) {
	if len(b.inflight) == 0 {
		return false
	}

	remaining := b.inflight[:0]
	for _, cmd := range b.inflight {
		cmd.CycleLeft--
		if cmd.CycleLeft <= 0 {
			b.completeCommand(now, cmd)
			continue
		}

		remaining = append(remaining, cmd)
	}

	for i := len(remaining); i < len(b.inflight); i++ {
		b.inflight[i] = nil
	}
	b.inflight = remaining

	return true
}

func (b *BankImpl) completeCurrentCmd(now sim.VTimeInSec) {
	b.completeCommand(now, b.currentCmd)

	// fmt.Printf("%.10f, %s, cmd completed, %s\n",
	// 	now, b.Name(), b.currentCmd.Kind.String())

	b.currentCmd = nil
}

func (b *BankImpl) completeCommand(now sim.VTimeInSec, cmd *signal.Command) {
	cmd.CycleLeft = 0

	tracing.EndTask(cmd.ID, b)

	if cmd.IsReadOrWrite() {
		cmd.SubTrans.Completed = true

		tracing.EndTask(cmd.SubTrans.ID, b)
	}

	if b.commandCompleted != nil {
		b.commandCompleted(now, cmd)
	}
}

// SetCommandCompletedObserver installs a passive command-completion hook.
func (b *BankImpl) SetCommandCompletedObserver(
	observer func(now sim.VTimeInSec, cmd *signal.Command),
) {
	b.commandCompleted = observer
}

// ObservationState returns the bank state without exposing mutable bank
// internals to the observer.
func (b *BankImpl) ObservationState() (open bool, row uint64) {
	return b.state == BankStateOpen, b.openRow
}

// ObservationColumnReady checks whether cmd is a ready row-hit column command
// without cloning it or changing timing state.
func (b *BankImpl) ObservationColumnReady(cmd *signal.Command) bool {
	if cmd == nil || !cmd.IsReadOrWrite() || b.currentCmd != nil {
		return false
	}
	if b.state != BankStateOpen || b.openRow != cmd.Row {
		return false
	}
	return b.cyclesToCmdAvailable[cmd.Kind] == 0
}

// GetReadyCommand returns the next command is ready to be issued.
func (b *BankImpl) GetReadyCommand(
	now sim.VTimeInSec,
	cmd *signal.Command,
) *signal.Command {
	if b.currentCmd != nil {
		return nil
	}
	requiredKind := b.getRequiredCommandKind(cmd)
	if requiredKind == signal.NumCmdKind {
		panic("never")
	}

	if b.cyclesToCmdAvailable[requiredKind] == 0 {
		readyCmd := cmd.Clone()
		readyCmd.Kind = requiredKind
		return readyCmd
	}

	return nil
}

func (b *BankImpl) getRequiredCommandKind(cmd *signal.Command) signal.CommandKind {
	key := cmdKindTableKey{b.state, cmd.Kind}

	kindFunc, found := requiredCmdKindTable[key]
	if !found {
		return signal.NumCmdKind
	}

	return kindFunc(b, cmd)
}

// StartCommand starts a new command in the Bank.
func (b *BankImpl) StartCommand(now sim.VTimeInSec, cmd *signal.Command) {
	if b.currentCmd != nil {
		panic("previous cmd is not completed")
	}
	if b.PipelineColumnCommands && cmd.IsReadOrWrite() {
		b.startColumnCommand(cmd)
		return
	}
	b.currentCmd = cmd
	b.currentCmd.CycleLeft = b.CmdCycles[cmd.Kind]

	key := cmdKindTableKey{b.state, cmd.Kind}

	updateFunc, found := stateUpdateTable[key]
	if !found {
		panic("never")
	}

	updateFunc(b, cmd)

	tracing.StartTask(
		cmd.ID,
		cmd.SubTrans.ID,
		b,
		"cmd",
		cmd.Kind.String(),
		nil,
	)

	// fmt.Printf("%.10f, %s, cmd started, %s\n",
	// 	now, b.Name(), b.currentCmd.Kind.String())
}

// startColumnCommand starts a pipelined read or write. The state update
// happens at once (an auto-precharge closes the row for the next command),
// while the command stays in flight until its data transfer ends.
func (b *BankImpl) startColumnCommand(cmd *signal.Command) {
	cmd.CycleLeft = b.CmdCycles[cmd.Kind]

	updateFunc, found := stateUpdateTable[cmdKindTableKey{b.state, cmd.Kind}]
	if !found {
		panic("never")
	}

	updateFunc(b, cmd)

	tracing.StartTask(
		cmd.ID,
		cmd.SubTrans.ID,
		b,
		"cmd",
		cmd.Kind.String(),
		nil,
	)

	b.inflight = append(b.inflight, cmd)
}

// UpdateTiming updates timing related states of the bank.
func (b *BankImpl) UpdateTiming(cmdKind signal.CommandKind, cycleNeeded int) {
	t := b.cyclesToCmdAvailable[cmdKind]

	if t < cycleNeeded {
		b.cyclesToCmdAvailable[cmdKind] = cycleNeeded
	}

	//fmt.Printf("%s, cmd timing updated, %s, %d, %d\n",
	//	b.Name(), cmdKind.String(),
	//	cycleNeeded, b.cyclesToCmdAvailable[cmdKind])
}

type cmdKindTableKey struct {
	bankState BankState
	cmdKind   signal.CommandKind
}

type requiredCmdKindFunc func(b *BankImpl, cmd *signal.Command) signal.CommandKind
type updateStateFunc func(b *BankImpl, cmd *signal.Command)

var requiredCmdKindTable map[cmdKindTableKey]requiredCmdKindFunc
var stateUpdateTable map[cmdKindTableKey]updateStateFunc

func returnCmdKindActive(b *BankImpl, cmd *signal.Command) signal.CommandKind {
	return signal.CmdKindActivate
}

func actionOnOpenRowOrPrecharge(
	b *BankImpl,
	cmd *signal.Command,
) signal.CommandKind {
	if b.openRow == cmd.Row {
		return cmd.Kind
	}
	return signal.CmdKindPrecharge
}

func openRow(b *BankImpl, cmd *signal.Command) {
	b.openRow = cmd.Row
	b.state = BankStateOpen
}

func closeRow(b *BankImpl, cmd *signal.Command) {
	b.state = BankStateClosed
}

func doNothing(b *BankImpl, cmd *signal.Command) {
	// Do nothing
}

func init() {
	requiredCmdKindTable = map[cmdKindTableKey]requiredCmdKindFunc{
		{BankStateClosed, signal.CmdKindRead}:           returnCmdKindActive,
		{BankStateClosed, signal.CmdKindReadPrecharge}:  returnCmdKindActive,
		{BankStateClosed, signal.CmdKindWrite}:          returnCmdKindActive,
		{BankStateClosed, signal.CmdKindWritePrecharge}: returnCmdKindActive,
		{BankStateOpen, signal.CmdKindRead}:             actionOnOpenRowOrPrecharge,
		{BankStateOpen, signal.CmdKindReadPrecharge}:    actionOnOpenRowOrPrecharge,
		{BankStateOpen, signal.CmdKindWrite}:            actionOnOpenRowOrPrecharge,
		{BankStateOpen, signal.CmdKindWritePrecharge}:   actionOnOpenRowOrPrecharge,
	}

	stateUpdateTable = map[cmdKindTableKey]updateStateFunc{
		{BankStateClosed, signal.CmdKindActivate}:     openRow,
		{BankStateOpen, signal.CmdKindPrecharge}:      closeRow,
		{BankStateOpen, signal.CmdKindReadPrecharge}:  closeRow,
		{BankStateOpen, signal.CmdKindWritePrecharge}: closeRow,
		{BankStateOpen, signal.CmdKindRead}:           doNothing,
		{BankStateOpen, signal.CmdKindWrite}:          doNothing,
	}
}
