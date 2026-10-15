package cmdq

import (
	"github.com/sarchlab/akita/v3/mem/dram/internal/org"
	"github.com/sarchlab/akita/v3/mem/dram/internal/signal"
	"github.com/sarchlab/akita/v3/sim"
)

// A Queue is a list of commands that needs to be executed by either a bank or a
// rank.
type Queue []*signal.Command

// A Bus is a DRAM command bus. HBM has separate row and column command
// interfaces, so a row command and a column command can issue in the same
// cycle (JESD235A section 3.2.2).
type Bus int

// A list of command buses.
const (
	// AnyBus does not restrict the command kind.
	AnyBus Bus = iota
	// RowBus carries activate, precharge, and refresh commands.
	RowBus
	// ColumnBus carries read and write commands.
	ColumnBus
)

// BusOf returns the command bus that carries commands of the given kind.
func BusOf(kind signal.CommandKind) Bus {
	switch kind {
	case signal.CmdKindRead, signal.CmdKindReadPrecharge,
		signal.CmdKindWrite, signal.CmdKindWritePrecharge:
		return ColumnBus
	default:
		return RowBus
	}
}

func (b Bus) carries(kind signal.CommandKind) bool {
	return b == AnyBus || BusOf(kind) == b
}

// CommandQueueImpl implements a command queue.
type CommandQueueImpl struct {
	Queues                []Queue
	CapacityPerQueue      int
	nextQueueIndex        int
	Channel               org.Channel
	RowContinuation       bool
	AggregateContinuation bool
	Freq                  sim.Freq
	stats                 RowContinuationStats
	activatedByCmd        map[string]bool
	readyScratch          []readyCandidate
	preferredAggregateID  string

	// RanksPerChannel selects one queue per (channel, rank): a command goes
	// to queue Channel*RanksPerChannel+Rank. Zero keeps one queue per rank,
	// which ignores the channel.
	RanksPerChannel int

	// RowHold keeps a bank open for all the access units of one transaction
	// that use the same row: the first issue without auto-precharge and the
	// last with it. From the activate on, the bank is reserved for that
	// transaction until its last access unit issues; then the row and
	// aggregate continuations may hand the open row to the next queued
	// same-row command. It requires the commands of a transaction to be
	// enqueued together.
	RowHold bool
	held    map[bankKey]*signal.Transaction
}

// bankKey identifies a physical bank.
type bankKey struct {
	channel, rank, bankGroup, bank uint64
}

func bankKeyOf(cmd *signal.Command) bankKey {
	return bankKey{cmd.Channel, cmd.Rank, cmd.BankGroup, cmd.Bank}
}

func transactionOf(cmd *signal.Command) *signal.Transaction {
	if cmd == nil || cmd.SubTrans == nil {
		return nil
	}

	return cmd.SubTrans.Transaction
}

// RowContinuationStats reports immediate same-row continuation behavior.
type RowContinuationStats struct {
	Enabled                     bool
	AggregateEnabled            bool
	CommandsIssued              uint64
	ColumnCommands              uint64
	RowReuseHits                uint64
	AutoPrechargeStops          uint64
	AggregateAutoPrechargeStops uint64
	AggregateImmediateContinues uint64
	ActivateCommands            uint64
	PrechargeCommands           uint64
	MaxQueueAgeCycles           uint64

	// RowHoldEnabled reports whether row hold is on.
	RowHoldEnabled bool
	// RowHoldAutoPrechargeStops counts the column commands issued without
	// auto-precharge because their transaction uses the open row again.
	RowHoldAutoPrechargeStops uint64
}

// ObservationDepth reports the occupancy of the queue that owns cmd and the
// total command-queue occupancy. It is read-only and is used by the physical
// DRAM observer rather than by scheduling policy.
func (q *CommandQueueImpl) ObservationDepth(
	cmd *signal.Command,
) (queueDepth, totalDepth int) {
	if cmd != nil && len(q.Queues) > 0 {
		queueIndex := q.getQueueIndex(cmd)
		if queueIndex >= 0 && queueIndex < len(q.Queues) {
			queueDepth = len(q.Queues[queueIndex])
		}
	}
	for _, queue := range q.Queues {
		totalDepth += len(queue)
	}
	return queueDepth, totalDepth
}

// ObservationReadySameOpenRow counts queued column commands that can issue
// without changing the currently open row in the selected physical bank.
// The method has no scheduling side effects and does not generate command IDs.
func (q *CommandQueueImpl) ObservationReadySameOpenRow(
	channelID, rank, bankGroup, bank, openRow uint64,
) int {
	channel, ok := q.Channel.(org.ColumnReadyObserver)
	if !ok {
		return 0
	}

	ready := 0
	for _, queue := range q.Queues {
		for _, candidate := range queue {
			if candidate == nil || !candidate.IsReadOrWrite() ||
				candidate.Channel != channelID ||
				candidate.Rank != rank ||
				candidate.BankGroup != bankGroup ||
				candidate.Bank != bank ||
				candidate.Row != openRow {
				continue
			}
			if channel.ObservationColumnReady(candidate) {
				ready++
			}
		}
	}
	return ready
}

// GetCommandToIssue returns the next command ready to issue. It returns nil
// if there if no command ready.
func (q *CommandQueueImpl) GetCommandToIssue(
	now sim.VTimeInSec,
) *signal.Command {
	if q.RowContinuation || q.AggregateContinuation || q.RowHold {
		return q.getRowContinuationCommandToIssue(now, AnyBus)
	}

	for i := 0; i < len(q.Queues); i++ {
		queueIndex, _ := q.getNextQueue()
		readyCmd := q.getFirstReadyInQueue(now, queueIndex)

		if readyCmd != nil {
			return readyCmd
		}
	}

	return nil
}

// GetCommandToIssueOn is GetCommandToIssue restricted to the commands of one
// command bus. With AnyBus it is GetCommandToIssue. Otherwise it always uses
// the scan path, which selects the first ready command of that bus in the
// same round-robin order as the first-ready loop; ready commands of the other
// bus are skipped but still count as same-bank peers for the continuations.
func (q *CommandQueueImpl) GetCommandToIssueOn(
	now sim.VTimeInSec,
	bus Bus,
) *signal.Command {
	if bus == AnyBus {
		return q.GetCommandToIssue(now)
	}

	return q.getRowContinuationCommandToIssue(now, bus)
}

type readyCandidate struct {
	queueIndex int
	cmdIndex   int
	cmd        *signal.Command
	ready      *signal.Command
	age        sim.VTimeInSec
	rowReady   bool
}

func (q *CommandQueueImpl) getRowContinuationCommandToIssue(
	now sim.VTimeInSec,
	bus Bus,
) *signal.Command {
	q.resetReadyScratch()
	firstReadyIndex := -1
	for offset := 0; offset < len(q.Queues); offset++ {
		queueIndex := (q.nextQueueIndex + offset) % len(q.Queues)
		for cmdIndex, cmd := range q.Queues[queueIndex] {
			// Only the holder may use a held bank. Skipping before
			// GetReadyCommand also saves the clone.
			if q.RowHold && q.heldByOther(cmd) {
				continue
			}
			ready := q.Channel.GetReadyCommand(now, cmd)
			if ready == nil {
				continue
			}
			// A held bank is open on the holder's row; a precharge for
			// another row of the same transaction waits for the release.
			if q.RowHold && ready.Kind == signal.CmdKindPrecharge &&
				q.holds(cmd) {
				continue
			}

			age := now - cmd.EnqueuedAt
			candidate := readyCandidate{
				queueIndex: queueIndex,
				cmdIndex:   cmdIndex,
				cmd:        cmd,
				ready:      ready,
				age:        age,
				rowReady:   ready.Kind == cmd.Kind && cmd.IsReadOrWrite(),
			}
			q.readyScratch = append(q.readyScratch, candidate)
			if firstReadyIndex < 0 && bus.carries(ready.Kind) {
				firstReadyIndex = len(q.readyScratch) - 1
			}
		}
	}

	if firstReadyIndex < 0 {
		q.resetReadyScratch()
		return nil
	}

	firstReady := &q.readyScratch[firstReadyIndex]
	selected := firstReady
	continuedAggregate := false
	if holder := q.readyHolder(bus); holder != nil {
		selected = holder
	} else if q.preferredAggregateID != "" {
		for i := range q.readyScratch {
			candidate := &q.readyScratch[i]
			if pairedReadAggregateID(candidate.cmd) ==
				q.preferredAggregateID &&
				candidate.ready.Kind == candidate.cmd.Kind &&
				candidate.cmd.IsReadOrWrite() &&
				bus.carries(candidate.ready.Kind) {
				selected = candidate
				continuedAggregate = true
				break
			}
		}
	}
	autoPrechargeStopped, aggregateStop := false, false
	if q.RowHold {
		// The hold replaces the peer test, which can never see another
		// transaction's command to a held bank. A hand-off makes the peer
		// the holder instead of the preferred aggregate.
		autoPrechargeStopped, aggregateStop = q.applyRowHold(selected)
	} else if (q.RowContinuation || q.AggregateContinuation) &&
		!q.refreshPending(selected.cmd.Channel) {
		// A bus-restricted scan also runs without continuations; it must
		// then keep the first-ready policy. A channel that waits to
		// refresh must be able to close its rows.
		autoPrechargeStopped, aggregateStop =
			q.stopAutoPrechargeForReadyPeer(selected)
	}
	if aggregateStop && !q.RowHold {
		q.preferredAggregateID = pairedReadAggregateID(selected.cmd)
	} else if continuedAggregate {
		q.preferredAggregateID = ""
	}

	q.nextQueueIndex = (selected.queueIndex + 1) % len(q.Queues)
	q.recordIssue(selected, autoPrechargeStopped, aggregateStop)
	if continuedAggregate {
		q.stats.AggregateImmediateContinues++
	}
	if selected.cmd.Kind == selected.ready.Kind {
		queue := q.Queues[selected.queueIndex]
		q.Queues[selected.queueIndex] = append(
			queue[:selected.cmdIndex], queue[selected.cmdIndex+1:]...)
		delete(q.activatedByCmd, selected.cmd.ID)
	}
	ready := selected.ready
	q.resetReadyScratch()
	return ready
}

func (q *CommandQueueImpl) stopAutoPrechargeForReadyPeer(
	selected *readyCandidate,
) (stopped bool, aggregate bool) {
	openKind := signal.NumCmdKind
	switch selected.ready.Kind {
	case signal.CmdKindReadPrecharge:
		openKind = signal.CmdKindRead
	case signal.CmdKindWritePrecharge:
		openKind = signal.CmdKindWrite
	default:
		return false, false
	}

	for i := range q.readyScratch {
		peer := &q.readyScratch[i]
		if peer.cmd == selected.cmd || !peer.cmd.IsReadOrWrite() ||
			!samePhysicalBank(peer.cmd, selected.cmd) {
			continue
		}
		// Only the next ready command for this physical bank can justify
		// keeping the row open. A farther same-row peer behind a different-row
		// command is not an immediate reuse opportunity.
		sameAggregate := samePairedReadAggregate(peer.cmd, selected.cmd)
		if peer.cmd.Row != selected.cmd.Row ||
			peer.ready.Kind != peer.cmd.Kind ||
			(!q.RowContinuation && !sameAggregate) {
			return false, false
		}
		selected.cmd.Kind = openKind
		selected.ready.Kind = openKind
		return true, sameAggregate
	}
	return false, false
}

// heldByOther reports whether the bank of cmd is held by another
// transaction.
func (q *CommandQueueImpl) heldByOther(cmd *signal.Command) bool {
	holder, found := q.held[bankKeyOf(cmd)]
	return found && holder != transactionOf(cmd)
}

// holds reports whether the transaction of cmd holds the bank of cmd.
func (q *CommandQueueImpl) holds(cmd *signal.Command) bool {
	t := transactionOf(cmd)
	return t != nil && q.held[bankKeyOf(cmd)] == t
}

// readyHolder returns the first ready row-hit column command on the bus whose
// transaction holds its bank, or nil.
func (q *CommandQueueImpl) readyHolder(bus Bus) *readyCandidate {
	if !q.RowHold || len(q.held) == 0 {
		return nil
	}

	for i := range q.readyScratch {
		candidate := &q.readyScratch[i]
		if candidate.rowReady && bus.carries(candidate.ready.Kind) &&
			q.holds(candidate.cmd) {
			return candidate
		}
	}

	return nil
}

// applyRowHold keeps a bank open for the remaining sub-accesses of the
// selected command's transaction and, when that transaction is done with
// the bank, lets the opportunistic continuations hand the open row to the
// next queued same-row command. It never calls GetReadyCommand.
func (q *CommandQueueImpl) applyRowHold(
	selected *readyCandidate,
) (stopped bool, aggregate bool) {
	t := transactionOf(selected.cmd)
	if t == nil {
		return false, false
	}

	key := bankKeyOf(selected.cmd)
	if selected.ready.Kind == signal.CmdKindActivate {
		// The activate opens the row for t; another transaction's row hit
		// must not close it before t's first column command.
		if selected.cmd.IsReadOrWrite() {
			q.hold(key, t)
		}
		return false, false
	}

	if !selected.rowReady {
		return false, false
	}

	if q.hasQueuedSameRowCommand(selected, t) {
		if openKind := openKindOf(selected.ready.Kind); openKind !=
			signal.NumCmdKind {
			selected.cmd.Kind = openKind
			selected.ready.Kind = openKind
			q.stats.RowHoldAutoPrechargeStops++
		}
		q.hold(key, t)
		return false, false
	}

	// This is t's last access to the row.
	if q.held[key] == t {
		delete(q.held, key)
	}

	return q.handOffRow(selected, key)
}

// handOffRow keeps the row open for the first queued command of another
// transaction to the bank if it is a ready same-row column command that the
// row or aggregate continuation accepts, and makes its transaction the
// holder.
func (q *CommandQueueImpl) handOffRow(
	selected *readyCandidate,
	key bankKey,
) (stopped bool, aggregate bool) {
	openKind := openKindOf(selected.ready.Kind)
	if openKind == signal.NumCmdKind ||
		(!q.RowContinuation && !q.AggregateContinuation) ||
		q.refreshPending(selected.cmd.Channel) {
		return false, false
	}

	peer := q.firstOtherTransactionCommand(selected)
	if peer == nil || peer.Row != selected.cmd.Row ||
		transactionOf(peer) == nil {
		return false, false
	}

	sameAggregate := samePairedReadAggregate(peer, selected.cmd)
	if !q.RowContinuation && !sameAggregate {
		return false, false
	}

	// The non-cloning probe draws no ID. Like the legacy peer test, it
	// sees the bank before the selected command starts.
	channel, ok := q.Channel.(org.ColumnReadyObserver)
	if !ok || !channel.ObservationColumnReady(peer) {
		return false, false
	}

	selected.cmd.Kind = openKind
	selected.ready.Kind = openKind
	q.hold(key, transactionOf(peer))
	return true, sameAggregate
}

// hasQueuedSameRowCommand reports whether another column command of t to the
// bank and row of the selected command is queued. A bank belongs to one
// queue, so only the selected command's queue is searched.
func (q *CommandQueueImpl) hasQueuedSameRowCommand(
	selected *readyCandidate,
	t *signal.Transaction,
) bool {
	for _, cmd := range q.Queues[selected.queueIndex] {
		if cmd == selected.cmd || transactionOf(cmd) != t ||
			!cmd.IsReadOrWrite() || cmd.Row != selected.cmd.Row ||
			!samePhysicalBank(cmd, selected.cmd) {
			continue
		}
		return true
	}

	return false
}

// firstOtherTransactionCommand returns, in queue order, the first queued
// command to the bank of the selected command that belongs to another
// transaction, or nil. Only this command can justify keeping the row open.
func (q *CommandQueueImpl) firstOtherTransactionCommand(
	selected *readyCandidate,
) *signal.Command {
	t := transactionOf(selected.cmd)
	for _, cmd := range q.Queues[selected.queueIndex] {
		if cmd == selected.cmd || transactionOf(cmd) == t ||
			!samePhysicalBank(cmd, selected.cmd) {
			continue
		}
		if !cmd.IsReadOrWrite() {
			return nil
		}
		return cmd
	}

	return nil
}

func (q *CommandQueueImpl) hold(key bankKey, t *signal.Transaction) {
	if q.held == nil {
		q.held = make(map[bankKey]*signal.Transaction)
	}
	q.held[key] = t
}

// refreshPending reports whether the channel waits to refresh. Such a channel
// opens no row until its open rows have closed, so the continuations must
// not keep a row open in it. A channel that cannot tell never waits.
func (q *CommandQueueImpl) refreshPending(channel uint64) bool {
	querier, ok := q.Channel.(org.RefreshPendingQuerier)
	return ok && querier.RefreshPending(channel)
}

// openKindOf returns the kind without auto-precharge of an auto-precharge
// column command kind, or NumCmdKind.
func openKindOf(kind signal.CommandKind) signal.CommandKind {
	switch kind {
	case signal.CmdKindReadPrecharge:
		return signal.CmdKindRead
	case signal.CmdKindWritePrecharge:
		return signal.CmdKindWrite
	default:
		return signal.NumCmdKind
	}
}

// samePairedReadAggregate recognizes only the two independent 64-B reads
// explicitly linked by the L2. Empty IDs, same-part duplicates, non-adjacent
// columns, writes, and unrelated transactions never receive M1 priority.
func samePairedReadAggregate(a, b *signal.Command) bool {
	if a == nil || b == nil || a.SubTrans == nil || b.SubTrans == nil ||
		a.SubTrans.Transaction == nil || b.SubTrans.Transaction == nil {
		return false
	}
	if !signal.ArePairedReadPeers(
		a.SubTrans.Transaction, b.SubTrans.Transaction) {
		return false
	}
	if a.Column > b.Column {
		return a.Column-b.Column == 1
	}
	return b.Column-a.Column == 1
}

func pairedReadAggregateID(cmd *signal.Command) string {
	if cmd == nil || cmd.SubTrans == nil ||
		cmd.SubTrans.Transaction == nil ||
		cmd.SubTrans.Transaction.Read == nil {
		return ""
	}
	return cmd.SubTrans.Transaction.Read.PairedReadID
}

// CanAcceptAll checks batch admission without mutating command-queue state.
func (q *CommandQueueImpl) CanAcceptAll(commands []*signal.Command) bool {
	required := make(map[int]int)
	for _, cmd := range commands {
		if cmd == nil {
			return false
		}
		required[q.getQueueIndex(cmd)]++
	}
	for index, count := range required {
		if len(q.Queues[index])+count > q.CapacityPerQueue {
			return false
		}
	}
	return true
}

// AcceptAll atomically appends a previously admitted command batch.
func (q *CommandQueueImpl) AcceptAll(commands []*signal.Command) {
	if !q.CanAcceptAll(commands) {
		panic("command queue batch overflow")
	}
	for _, cmd := range commands {
		index := q.getQueueIndex(cmd)
		q.Queues[index] = append(q.Queues[index], cmd)
	}
}

func (q *CommandQueueImpl) resetReadyScratch() {
	for i := range q.readyScratch {
		q.readyScratch[i] = readyCandidate{}
	}
	q.readyScratch = q.readyScratch[:0]
}

func samePhysicalBank(a, b *signal.Command) bool {
	return a.Channel == b.Channel &&
		a.Rank == b.Rank &&
		a.BankGroup == b.BankGroup &&
		a.Bank == b.Bank
}

func (q *CommandQueueImpl) recordIssue(
	candidate *readyCandidate,
	autoPrechargeStopped bool,
	aggregateStop bool,
) {
	if q.activatedByCmd == nil {
		q.activatedByCmd = make(map[string]bool)
	}
	q.stats.Enabled = true
	q.stats.CommandsIssued++
	if autoPrechargeStopped {
		q.stats.AutoPrechargeStops++
	}
	if aggregateStop {
		q.stats.AggregateAutoPrechargeStops++
	}
	if q.Freq > 0 {
		ageCycles := q.Freq.Cycle(candidate.age)
		if ageCycles > q.stats.MaxQueueAgeCycles {
			q.stats.MaxQueueAgeCycles = ageCycles
		}
	}

	switch candidate.ready.Kind {
	case signal.CmdKindActivate:
		q.stats.ActivateCommands++
		q.activatedByCmd[candidate.cmd.ID] = true
	case signal.CmdKindPrecharge:
		q.stats.PrechargeCommands++
	case signal.CmdKindRead, signal.CmdKindReadPrecharge,
		signal.CmdKindWrite, signal.CmdKindWritePrecharge:
		q.stats.ColumnCommands++
		if !q.activatedByCmd[candidate.cmd.ID] {
			q.stats.RowReuseHits++
		}
	}
}

// GetRowContinuationStats returns row-continuation counters.
func (q *CommandQueueImpl) GetRowContinuationStats() RowContinuationStats {
	stats := q.stats
	stats.Enabled = q.RowContinuation
	stats.AggregateEnabled = q.AggregateContinuation
	stats.RowHoldEnabled = q.RowHold
	return stats
}

func (q *CommandQueueImpl) getNextQueue() (queueIndex int, queue Queue) {
	queueIndex = q.nextQueueIndex
	retQueue := q.Queues[q.nextQueueIndex]
	q.nextQueueIndex = (q.nextQueueIndex + 1) % len(q.Queues)
	return queueIndex, retQueue
}

func (q *CommandQueueImpl) getFirstReadyInQueue(
	now sim.VTimeInSec,
	queueIndex int,
) *signal.Command {
	for i, cmd := range q.Queues[queueIndex] {
		readyCmd := q.Channel.GetReadyCommand(now, cmd)

		if readyCmd != nil {
			// Command counters describe the physical DRAM work, not the
			// row-continuation policy.  Record the ordinary scheduler as well
			// so Baseline and M1 expose comparable ACT/PRE/column counts.
			q.recordIssue(&readyCandidate{
				queueIndex: queueIndex,
				cmdIndex:   i,
				cmd:        cmd,
				ready:      readyCmd,
				age:        now - cmd.EnqueuedAt,
			}, false, false)
			if cmd.Kind == readyCmd.Kind {
				q.Queues[queueIndex] = append(
					q.Queues[queueIndex][:i], q.Queues[queueIndex][i+1:]...)
				delete(q.activatedByCmd, cmd.ID)
			}
			return readyCmd
		}
	}

	return nil
}

// CanAccept returns true is there is empty space in the command queue.
func (q *CommandQueueImpl) CanAccept(cmd *signal.Command) bool {
	queueIndex := q.getQueueIndex(cmd)
	queue := q.Queues[queueIndex]

	return len(queue) < q.CapacityPerQueue
}

// Accept adds a new command in the command queue.
func (q *CommandQueueImpl) Accept(cmd *signal.Command) {
	queueIndex := q.getQueueIndex(cmd)
	queue := q.Queues[queueIndex]

	if len(queue) >= q.CapacityPerQueue {
		panic("command queue overflow")
	}

	q.Queues[queueIndex] = append(queue, cmd)
}

func (q *CommandQueueImpl) getQueueIndex(cmd *signal.Command) int {
	if q.RanksPerChannel == 0 {
		return int(cmd.Rank)
	}

	return int(cmd.Channel)*q.RanksPerChannel + int(cmd.Rank)
}
