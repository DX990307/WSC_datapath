package trans

import (
	"github.com/sarchlab/akita/v3/mem/dram/internal/cmdq"
	"github.com/sarchlab/akita/v3/mem/dram/internal/signal"
	"github.com/sarchlab/akita/v3/sim"
)

// A FCFSSubTransactionQueue returns sub-transactions in a
// first-come-first-serve way.
type FCFSSubTransactionQueue struct {
	Capacity   int
	Queue      []*signal.SubTransaction
	CmdCreator CommandCreator
	CmdQueue   cmdq.CommandQueue

	// CommandEnqueued is an optional passive observation hook. It is invoked
	// after a command has been accepted and must not mutate the command.
	CommandEnqueued func(now sim.VTimeInSec, cmd *signal.Command)

	// BatchByTransaction moves all the sub-transactions of a transaction to
	// the command queue in one step, so that the scheduler always sees every
	// access unit of a request together. False moves one sub-transaction
	// (or one paired-read pair) at a time.
	BatchByTransaction bool
}

// batchCommandQueue is a command queue that can admit several commands
// atomically.
type batchCommandQueue interface {
	CanAcceptAll([]*signal.Command) bool
	AcceptAll([]*signal.Command)
}

// CanPush returns true if there are enough slots to hold n subtransactions.
func (q *FCFSSubTransactionQueue) CanPush(n int) bool {
	if n >= q.Capacity {
		panic("queue size not large enough to handle a single transaction")
	}

	if len(q.Queue)+n > q.Capacity {
		return false
	}
	return true
}

// Push adds new transaction to the transaction queue.
func (q *FCFSSubTransactionQueue) Push(t *signal.Transaction) {
	if len(q.Queue)+len(t.SubTransactions) > q.Capacity {
		panic("pushing too many subtransactions into queue.")
	}

	q.Queue = append(q.Queue, t.SubTransactions...)
}

// Tick breaks down transactions to commands and dispatches the command to the
// command queues.
func (q *FCFSSubTransactionQueue) Tick(now sim.VTimeInSec) bool {
	if q.BatchByTransaction {
		return q.tickBatches(now)
	}

	for i, subTrans := range q.Queue {
		cmd := q.CmdCreator.Create(subTrans)
		cmd.EnqueuedAt = now
		if i+1 < len(q.Queue) &&
			signal.ArePairedReadPeers(
				subTrans.Transaction, q.Queue[i+1].Transaction) {
			peerCmd := q.CmdCreator.Create(q.Queue[i+1])
			peerCmd.EnqueuedAt = now
			if batch, ok := q.CmdQueue.(interface {
				CanAcceptAll([]*signal.Command) bool
				AcceptAll([]*signal.Command)
			}); ok && batch.CanAcceptAll([]*signal.Command{cmd, peerCmd}) {
				batch.AcceptAll([]*signal.Command{cmd, peerCmd})
				if q.CommandEnqueued != nil {
					q.CommandEnqueued(now, cmd)
					q.CommandEnqueued(now, peerCmd)
				}
				q.Queue = append(q.Queue[:i], q.Queue[i+2:]...)
				return true
			}
		}

		if q.CmdQueue.CanAccept(cmd) {
			q.CmdQueue.Accept(cmd)
			if q.CommandEnqueued != nil {
				q.CommandEnqueued(now, cmd)
			}
			q.Queue = append(q.Queue[:i], q.Queue[i+1:]...)

			// fmt.Printf("Command Pushed: %#v\n", cmd)

			return true
		}
	}

	return false
}

// tickBatches moves the sub-transactions of the first transaction that fits
// to the command queue. It mirrors Tick at the granularity of transactions: a
// transaction and its paired-read peer go together if they fit, otherwise
// the transaction goes alone, otherwise the next transaction is tried.
func (q *FCFSSubTransactionQueue) tickBatches(now sim.VTimeInSec) bool {
	_, canBatch := q.CmdQueue.(batchCommandQueue)
	for start := 0; start < len(q.Queue); {
		end := q.transactionRunEnd(start)
		cmds := q.createCommands(now, start, end)

		if canBatch && end < len(q.Queue) && signal.ArePairedReadPeers(
			q.Queue[start].Transaction, q.Queue[end].Transaction) {
			peerEnd := q.transactionRunEnd(end)
			pair := append(cmds[:len(cmds):len(cmds)],
				q.createCommands(now, end, peerEnd)...)
			if q.acceptBatch(now, pair) {
				q.Queue = append(q.Queue[:start], q.Queue[peerEnd:]...)
				return true
			}
		}

		if q.acceptBatch(now, cmds) {
			q.Queue = append(q.Queue[:start], q.Queue[end:]...)
			return true
		}

		start = end
	}

	return false
}

// transactionRunEnd returns the end of the run of queue entries, starting at
// start, that belong to the same transaction.
func (q *FCFSSubTransactionQueue) transactionRunEnd(start int) int {
	end := start + 1
	for end < len(q.Queue) &&
		q.Queue[end].Transaction == q.Queue[start].Transaction {
		end++
	}

	return end
}

func (q *FCFSSubTransactionQueue) createCommands(
	now sim.VTimeInSec,
	start, end int,
) []*signal.Command {
	cmds := make([]*signal.Command, 0, end-start)
	for _, subTrans := range q.Queue[start:end] {
		cmd := q.CmdCreator.Create(subTrans)
		cmd.EnqueuedAt = now
		cmds = append(cmds, cmd)
	}

	return cmds
}

// acceptBatch admits all the commands or none of them.
func (q *FCFSSubTransactionQueue) acceptBatch(
	now sim.VTimeInSec,
	cmds []*signal.Command,
) bool {
	if batch, ok := q.CmdQueue.(batchCommandQueue); ok {
		if !batch.CanAcceptAll(cmds) {
			return false
		}
		batch.AcceptAll(cmds)
	} else {
		if len(cmds) != 1 {
			panic("batching by transaction needs a command queue " +
				"that accepts command batches")
		}
		if !q.CmdQueue.CanAccept(cmds[0]) {
			return false
		}
		q.CmdQueue.Accept(cmds[0])
	}

	if q.CommandEnqueued != nil {
		for _, cmd := range cmds {
			q.CommandEnqueued(now, cmd)
		}
	}

	return true
}
