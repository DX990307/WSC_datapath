package cmdq

import (
	"github.com/sarchlab/akita/v3/mem/dram/internal/org"
	"github.com/sarchlab/akita/v3/mem/dram/internal/signal"
	"github.com/sarchlab/akita/v3/sim"
)

// A Queue is a list of commands that needs to be executed by either a bank or a
// rank.
type Queue []*signal.Command

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
	channel, ok := q.Channel.(*org.ChannelImpl)
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
	if q.RowContinuation || q.AggregateContinuation {
		return q.getRowContinuationCommandToIssue(now)
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
) *signal.Command {
	q.resetReadyScratch()
	firstReadyIndex := -1
	for offset := 0; offset < len(q.Queues); offset++ {
		queueIndex := (q.nextQueueIndex + offset) % len(q.Queues)
		for cmdIndex, cmd := range q.Queues[queueIndex] {
			ready := q.Channel.GetReadyCommand(now, cmd)
			if ready == nil {
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
			if firstReadyIndex < 0 {
				firstReadyIndex = len(q.readyScratch) - 1
			}
		}
	}

	if firstReadyIndex < 0 {
		return nil
	}

	firstReady := &q.readyScratch[firstReadyIndex]
	selected := firstReady
	continuedAggregate := false
	if q.preferredAggregateID != "" {
		for i := range q.readyScratch {
			candidate := &q.readyScratch[i]
			if pairedReadAggregateID(candidate.cmd) ==
				q.preferredAggregateID &&
				candidate.ready.Kind == candidate.cmd.Kind &&
				candidate.cmd.IsReadOrWrite() {
				selected = candidate
				continuedAggregate = true
				break
			}
		}
	}
	autoPrechargeStopped, aggregateStop :=
		q.stopAutoPrechargeForReadyPeer(selected)
	if aggregateStop {
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
	return int(cmd.Rank)
}
