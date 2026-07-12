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
	Queues           []Queue
	CapacityPerQueue int
	nextQueueIndex   int
	Channel          org.Channel
	RowAware         bool
	MaxAge           sim.VTimeInSec
	Freq             sim.Freq
	stats            RowAwareStats
	activatedByCmd   map[string]bool
}

// RowAwareStats reports open-row scheduling behavior.
type RowAwareStats struct {
	Enabled            bool
	CommandsIssued     uint64
	ColumnCommands     uint64
	RowReuseHits       uint64
	ActivateCommands   uint64
	PrechargeCommands  uint64
	AgedPriorityIssues uint64
	MaxQueueAgeCycles  uint64
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
	if q.RowAware {
		return q.getRowAwareCommandToIssue(now)
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
	aged       bool
}

func (q *CommandQueueImpl) getRowAwareCommandToIssue(
	now sim.VTimeInSec,
) *signal.Command {
	var firstReady, oldestRowReady, oldestAged *readyCandidate
	for offset := 0; offset < len(q.Queues); offset++ {
		queueIndex := (q.nextQueueIndex + offset) % len(q.Queues)
		for cmdIndex, cmd := range q.Queues[queueIndex] {
			ready := q.Channel.GetReadyCommand(now, cmd)
			if ready == nil {
				continue
			}

			age := now - cmd.EnqueuedAt
			candidate := &readyCandidate{
				queueIndex: queueIndex,
				cmdIndex:   cmdIndex,
				cmd:        cmd,
				ready:      ready,
				age:        age,
				rowReady:   ready.Kind == cmd.Kind && cmd.IsReadOrWrite(),
				aged:       q.MaxAge > 0 && age >= q.MaxAge,
			}
			if firstReady == nil {
				firstReady = candidate
			}
			if candidate.rowReady && older(candidate, oldestRowReady) {
				oldestRowReady = candidate
			}
			if candidate.aged && older(candidate, oldestAged) {
				oldestAged = candidate
			}
		}
	}

	selected := oldestAged
	agedPriority := selected != nil
	if selected == nil {
		selected = oldestRowReady
	}
	if selected == nil {
		selected = firstReady
	}
	if selected == nil {
		return nil
	}

	q.nextQueueIndex = (selected.queueIndex + 1) % len(q.Queues)
	q.recordIssue(selected, agedPriority)
	if selected.cmd.Kind == selected.ready.Kind {
		queue := q.Queues[selected.queueIndex]
		q.Queues[selected.queueIndex] = append(
			queue[:selected.cmdIndex], queue[selected.cmdIndex+1:]...)
		delete(q.activatedByCmd, selected.cmd.ID)
	}
	return selected.ready
}

func older(candidate, current *readyCandidate) bool {
	return current == nil || candidate.cmd.EnqueuedAt < current.cmd.EnqueuedAt
}

func (q *CommandQueueImpl) recordIssue(
	candidate *readyCandidate,
	agedPriority bool,
) {
	if q.activatedByCmd == nil {
		q.activatedByCmd = make(map[string]bool)
	}
	q.stats.Enabled = true
	q.stats.CommandsIssued++
	if agedPriority {
		q.stats.AgedPriorityIssues++
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
	case signal.CmdKindRead, signal.CmdKindWrite:
		q.stats.ColumnCommands++
		if !q.activatedByCmd[candidate.cmd.ID] {
			q.stats.RowReuseHits++
		}
	}
}

// GetRowAwareStats returns a copy of the row-aware scheduler counters.
func (q *CommandQueueImpl) GetRowAwareStats() RowAwareStats {
	stats := q.stats
	stats.Enabled = q.RowAware
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
			if cmd.Kind == readyCmd.Kind {
				q.Queues[queueIndex] = append(
					q.Queues[queueIndex][:i], q.Queues[queueIndex][i+1:]...)
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
