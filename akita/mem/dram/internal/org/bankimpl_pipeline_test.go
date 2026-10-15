package org

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/sarchlab/akita/v3/mem/dram/internal/signal"
	"github.com/sarchlab/akita/v3/sim"
	"github.com/sarchlab/akita/v3/tracing"
)

// recordingTracer records the IDs of the started and ended tasks.
type recordingTracer struct {
	starts, ends []string
}

func (t *recordingTracer) StartTask(task tracing.Task) {
	t.starts = append(t.starts, task.ID)
}

func (t *recordingTracer) StepTask(task tracing.Task) {}

func (t *recordingTracer) EndTask(task tracing.Task) {
	t.ends = append(t.ends, task.ID)
}

var _ = Describe("Bank with pipelined column commands", func() {
	var (
		b *BankImpl
	)

	column := func(id string, kind signal.CommandKind) *signal.Command {
		cmd := &signal.Command{
			ID:       id,
			Kind:     kind,
			SubTrans: &signal.SubTransaction{ID: id + ".st"},
		}
		cmd.Row = 6
		return cmd
	}

	// tick ticks the bank n times.
	tick := func(n int) {
		for i := 0; i < n; i++ {
			b.Tick(10)
		}
	}

	BeforeEach(func() {
		b = NewBankImpl("Bank")
		b.PipelineColumnCommands = true
		b.CmdCycles = [signal.NumCmdKind]int{
			signal.CmdKindRead:           5,
			signal.CmdKindReadPrecharge:  5,
			signal.CmdKindWrite:          3,
			signal.CmdKindWritePrecharge: 3,
			signal.CmdKindActivate:       4,
			signal.CmdKindPrecharge:      2,
		}
		b.state = BankStateOpen
		b.openRow = 6
	})

	It("should issue a second read of the open row while the first is "+
		"in flight", func() {
		first := column("rd", signal.CmdKindRead)
		second := column("rda", signal.CmdKindReadPrecharge)

		b.StartCommand(10, first)
		// tCCDL = 2, as the channel would apply it.
		b.UpdateTiming(signal.CmdKindReadPrecharge, 2)

		Expect(b.currentCmd).To(BeNil())
		Expect(b.GetReadyCommand(10, second)).To(BeNil())
		tick(1)
		Expect(b.GetReadyCommand(10, second)).To(BeNil())
		tick(1)
		ready := b.GetReadyCommand(10, second)
		Expect(ready).NotTo(BeNil())
		Expect(ready.Kind).To(Equal(signal.CmdKindReadPrecharge))
		Expect(b.ObservationColumnReady(second)).To(BeTrue())

		b.StartCommand(10, ready)
		open, _ := b.ObservationState()
		Expect(open).To(BeFalse())
		Expect(b.inflight).To(HaveLen(2))

		tick(2)
		Expect(first.SubTrans.Completed).To(BeFalse())
		tick(1)
		Expect(first.SubTrans.Completed).To(BeTrue())
		Expect(ready.SubTrans.Completed).To(BeFalse())
		tick(1)
		Expect(ready.SubTrans.Completed).To(BeFalse())
		tick(1)
		Expect(ready.SubTrans.Completed).To(BeTrue())
		Expect(b.inflight).To(BeEmpty())
		Expect(b.Tick(10)).To(BeFalse())
	})

	It("should still let an activate occupy the bank", func() {
		b.state = BankStateClosed
		act := column("act", signal.CmdKindActivate)

		b.StartCommand(10, act)

		Expect(b.currentCmd).To(BeIdenticalTo(act))
		Expect(b.GetReadyCommand(10, column("rd", signal.CmdKindRead))).
			To(BeNil())
		tick(4)
		Expect(b.currentCmd).To(BeNil())
		Expect(b.GetReadyCommand(10, column("rd", signal.CmdKindRead))).
			NotTo(BeNil())
	})

	It("should complete in-flight commands when their transfers end",
		func() {
			read := column("rd", signal.CmdKindRead)
			write := column("wr", signal.CmdKindWrite)
			var completed []string
			b.SetCommandCompletedObserver(
				func(_ sim.VTimeInSec, cmd *signal.Command) {
					completed = append(completed, cmd.ID)
				})

			b.StartCommand(10, read)
			tick(1)
			b.StartCommand(10, write)
			tick(3)

			Expect(completed).To(Equal([]string{"wr"}))
			tick(1)
			Expect(completed).To(Equal([]string{"wr", "rd"}))
		})

	It("should trace and observe every in-flight command once", func() {
		tracer := &recordingTracer{}
		tracing.CollectTrace(b, tracer)
		var completed []string
		b.SetCommandCompletedObserver(
			func(_ sim.VTimeInSec, cmd *signal.Command) {
				completed = append(completed, cmd.ID)
			})

		b.StartCommand(10, column("rd", signal.CmdKindRead))
		tick(2)
		b.StartCommand(10, column("rda", signal.CmdKindReadPrecharge))
		tick(10)

		Expect(tracer.starts).To(Equal([]string{"rd", "rda"}))
		Expect(tracer.ends).To(Equal([]string{
			"rd", "rd.st", "rda", "rda.st",
		}))
		Expect(completed).To(Equal([]string{"rd", "rda"}))
	})

	It("should keep one command at a time without pipelining", func() {
		b.PipelineColumnCommands = false
		read := column("rd", signal.CmdKindRead)

		b.StartCommand(10, read)

		Expect(b.currentCmd).To(BeIdenticalTo(read))
		Expect(b.inflight).To(BeEmpty())
		Expect(b.GetReadyCommand(10, column("rd2", signal.CmdKindRead))).
			To(BeNil())
	})
})
