package dram

import (
	"github.com/sarchlab/akita/v3/mem/dram/internal/cmdq"
	"github.com/sarchlab/akita/v3/mem/dram/internal/org"
	"github.com/sarchlab/akita/v3/mem/dram/internal/signal"
	"github.com/sarchlab/akita/v3/mem/dram/internal/trans"
	"github.com/sarchlab/akita/v3/sim"
)

func (c *MemController) installPhysicalObserverHooks() {
	if queue, ok := c.subTransactionQueue.(*trans.FCFSSubTransactionQueue); ok {
		if c.physicalObserver == nil {
			queue.CommandEnqueued = nil
		} else {
			queue.CommandEnqueued = c.observePhysicalCommandEnqueue
		}
	}

	channel, ok := c.channel.(*org.ChannelImpl)
	if !ok {
		return
	}
	ranks, bankGroups, banks := channel.Banks.GetSize()
	for rank := uint64(0); rank < ranks; rank++ {
		for bankGroup := uint64(0); bankGroup < bankGroups; bankGroup++ {
			for bank := uint64(0); bank < banks; bank++ {
				impl, ok := channel.Banks.
					GetBank(rank, bankGroup, bank).(*org.BankImpl)
				if !ok {
					continue
				}
				if c.physicalObserver == nil {
					impl.SetCommandCompletedObserver(nil)
				} else {
					impl.SetCommandCompletedObserver(
						c.observePhysicalCommandComplete)
				}
			}
		}
	}
}

func (c *MemController) observePhysicalCommandEnqueue(
	now sim.VTimeInSec,
	cmd *signal.Command,
) {
	if c.physicalObserver == nil || cmd == nil || cmd.SubTrans == nil {
		return
	}
	e := c.makePhysicalDRAMEvent(
		PhysicalDRAMCommandEnqueue, now, cmd.SubTrans)
	c.physicalQueuedCommandIDs[cmd.SubTrans.ID] = cmd.ID
	c.copyPhysicalCommand(&e, cmd)
	c.fillPhysicalOpenRowObservation(&e, cmd)
	if queue, ok := c.cmdQueue.(*cmdq.CommandQueueImpl); ok {
		queueDepth, totalDepth := queue.ObservationDepth(cmd)
		e.QueueDepthAfter = queueDepth
		e.QueueDepthBefore = queueDepth - 1
		e.ControllerQueueDepth = totalDepth
	}
	c.emitPhysicalDRAM(e)
}

func (c *MemController) observePhysicalCommandIssue(
	now sim.VTimeInSec,
	cmd *signal.Command,
) {
	if c.physicalObserver == nil || cmd == nil || cmd.SubTrans == nil {
		return
	}
	e := c.makePhysicalDRAMEvent(
		PhysicalDRAMCommandIssue, now, cmd.SubTrans)
	c.copyPhysicalCommand(&e, cmd)

	if queue, ok := c.cmdQueue.(*cmdq.CommandQueueImpl); ok {
		queueDepth, totalDepth := queue.ObservationDepth(cmd)
		e.QueueDepthAfter = queueDepth
		e.QueueDepthBefore = queueDepth
		e.ControllerQueueDepth = totalDepth
		// A column command is removed by GetCommandToIssue; ACT/PRE clones
		// leave the original column command queued.
		if cmd.IsReadOrWrite() {
			e.QueueDepthBefore++
		}
	}

	c.fillPhysicalOpenRowObservation(&e, cmd)
	c.emitPhysicalDRAM(e)
}

func (c *MemController) observePhysicalCommandComplete(
	now sim.VTimeInSec,
	cmd *signal.Command,
) {
	if c.physicalObserver == nil || cmd == nil || cmd.SubTrans == nil {
		return
	}
	e := c.makePhysicalDRAMEvent(
		PhysicalDRAMCommandComplete, now, cmd.SubTrans)
	c.copyPhysicalCommand(&e, cmd)
	c.emitPhysicalDRAM(e)
}

func (c *MemController) observePhysicalTransactionComplete(
	now sim.VTimeInSec,
	t *signal.Transaction,
) {
	if c.physicalObserver == nil || t == nil {
		return
	}
	for _, st := range t.SubTransactions {
		c.emitPhysicalDRAM(c.makePhysicalDRAMEvent(
			PhysicalDRAMTransactionComplete, now, st))
		delete(c.physicalQueuedCommandIDs, st.ID)
	}
}

func (c *MemController) copyPhysicalCommand(
	e *PhysicalDRAMEvent,
	cmd *signal.Command,
) {
	e.CommandID = cmd.ID
	e.QueuedCommandID = c.physicalQueuedCommandIDs[cmd.SubTrans.ID]
	e.CommandKind = cmd.Kind.String()
	e.Channel = cmd.Channel
	e.Rank = cmd.Rank
	e.BankGroup = cmd.BankGroup
	e.Bank = cmd.Bank
	e.Row = cmd.Row
	e.Column = cmd.Column
}

func (c *MemController) fillPhysicalOpenRowObservation(
	e *PhysicalDRAMEvent,
	cmd *signal.Command,
) {
	channel, ok := c.channel.(*org.ChannelImpl)
	if !ok {
		return
	}
	e.OpenRowValid, e.OpenRow = channel.ObservationBankState(
		cmd.Rank, cmd.BankGroup, cmd.Bank)
	if !e.OpenRowValid {
		return
	}
	e.ColumnRowHit = cmd.IsReadOrWrite() && e.OpenRow == cmd.Row
	if queue, ok := c.cmdQueue.(*cmdq.CommandQueueImpl); ok {
		e.ReadySameOpenRow = queue.ObservationReadySameOpenRow(
			cmd.Channel, cmd.Rank, cmd.BankGroup, cmd.Bank, e.OpenRow)
	}
}

func (c *MemController) physicalSubtransactionQueueDepth() int {
	if queue, ok := c.subTransactionQueue.(*trans.FCFSSubTransactionQueue); ok {
		return len(queue.Queue)
	}
	return 0
}
