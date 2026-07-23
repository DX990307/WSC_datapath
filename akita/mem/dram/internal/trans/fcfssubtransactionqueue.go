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
