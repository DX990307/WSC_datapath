package dram

import (
	"github.com/golang/mock/gomock"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/sarchlab/akita/v3/mem/dram/internal/cmdq"
	"github.com/sarchlab/akita/v3/mem/dram/internal/org"
	"github.com/sarchlab/akita/v3/mem/dram/internal/signal"
	"github.com/sarchlab/akita/v3/mem/dram/internal/trans"
	"github.com/sarchlab/akita/v3/mem/mem"
	"github.com/sarchlab/akita/v3/sim"
)

// forEachBankImpl visits every bank of a controller.
func forEachBankImpl(m *MemController, visit func(b *org.BankImpl)) {
	m.channel.(org.BankVisitor).ForEachBank(func(b org.Bank) {
		visit(b.(*org.BankImpl))
	})
}

var _ = Describe("Builder with row hold and pipelined column commands",
	func() {
		It("should keep both off by default", func() {
			m := MakeBuilder().Build("X")

			Expect(m.cmdQueue.(*cmdq.CommandQueueImpl).RowHold).To(BeFalse())
			Expect(m.subTransactionQueue.(*trans.FCFSSubTransactionQueue).
				BatchByTransaction).To(BeFalse())
			forEachBankImpl(m, func(b *org.BankImpl) {
				Expect(b.PipelineColumnCommands).To(BeFalse())
				Expect(b.CmdCycles[signal.CmdKindReadPrecharge]).To(Equal(11))
				Expect(b.CmdCycles[signal.CmdKindWritePrecharge]).
					To(Equal(11))
			})
		})

		It("should hold rows and batch the transactions", func() {
			m := MakeBuilder().WithTransactionRowHold(true).Build("X")

			Expect(m.cmdQueue.(*cmdq.CommandQueueImpl).RowHold).To(BeTrue())
			Expect(m.subTransactionQueue.(*trans.FCFSSubTransactionQueue).
				BatchByTransaction).To(BeTrue())
		})

		It("should complete pipelined auto-precharge commands with their "+
			"data", func() {
			m := MakeBuilder().WithPipelinedColumnCommands(true).Build("X")

			// DDR3 defaults: tCL 11, BL8 (4 cycles).
			forEachBankImpl(m, func(b *org.BankImpl) {
				Expect(b.PipelineColumnCommands).To(BeTrue())
				Expect(b.CmdCycles[signal.CmdKindRead]).To(Equal(15))
				Expect(b.CmdCycles[signal.CmdKindReadPrecharge]).To(Equal(15))
				Expect(b.CmdCycles[signal.CmdKindWritePrecharge]).
					To(Equal(15))
				Expect(b.CmdCycles[signal.CmdKindPrecharge]).To(Equal(11))
			})
		})
	})

var _ = Describe("MemController with row hold", func() {
	var (
		mockCtrl *gomock.Controller
		engine   sim.Engine
		srcPort  *MockPort
		observer *collectingPhysicalDRAMObserver
		memCtrl  *MemController
	)

	type issue struct {
		kind  string
		cycle uint64
	}

	// build builds one HBM2 channel in pseudo-channel mode with row hold
	// and pipelined column commands: tRCDRD 24, tCL = tCWL = 11, BL4 (2
	// cycles), tCCDL 4.
	build := func(rowContinuation bool) {
		engine = sim.NewSerialEngine()
		observer = &collectingPhysicalDRAMObserver{}
		memCtrl = pseudoChannelBuilder(engine).
			WithTCWL(11).
			WithHBM2Turnaround(true).
			WithTransactionRowHold(true).
			WithPipelinedColumnCommands(true).
			WithRowContinuation(rowContinuation).
			WithGlobalStorage(mem.NewStorage(1 * mem.GB)).
			Build("MemCtrl")
		memCtrl.SetPhysicalDRAMObserver(observer, "GPU[0]", 0)

		conn := sim.NewDirectConnection("Conn", engine, 1*sim.GHz)
		srcPort.EXPECT().SetConnection(conn)
		conn.PlugIn(memCtrl.topPort, 4)
		conn.PlugIn(srcPort, 4)
	}

	send := func(msg sim.Msg) {
		msg.Meta().Src = srcPort
		msg.Meta().Dst = memCtrl.topPort
		memCtrl.topPort.Recv(msg)
	}

	events := func(kind PhysicalDRAMEventKind, channel uint64) []issue {
		var out []issue
		for _, e := range observer.events {
			if e.Kind == kind && e.Channel == channel {
				out = append(out, issue{e.CommandKind, e.Cycle})
			}
		}
		return out
	}

	kinds := func(issues []issue) []string {
		var out []string
		for _, i := range issues {
			out = append(out, i.kind)
		}
		return out
	}

	BeforeEach(func() {
		mockCtrl = gomock.NewController(GinkgoT())
		srcPort = NewMockPort(mockCtrl)
	})

	AfterEach(func() {
		mockCtrl.Finish()
	})

	It("should serve each 64-B line with one activate", func() {
		build(false)
		send(mem.ReadReqBuilder{}.WithAddress(0x00).WithByteSize(64).Build())
		data := make([]byte, 64)
		for i := range data {
			data[i] = byte(i + 1)
		}
		send(mem.WriteReqBuilder{}.WithAddress(0x40).WithData(data).Build())
		srcPort.EXPECT().Recv(gomock.Any()).Times(2)

		engine.Run()

		reads := events(PhysicalDRAMCommandIssue, 0)
		Expect(kinds(reads)).To(Equal([]string{
			"Activate", "Read", "ReadPrecharge",
		}))
		act := reads[0].cycle
		Expect(reads[1].cycle).To(Equal(act + 24))
		Expect(reads[2].cycle).To(Equal(act + 28))
		Expect(events(PhysicalDRAMCommandComplete, 0)).To(Equal([]issue{
			{"Activate", act + 24},
			{"Read", act + 24 + 13},
			{"ReadPrecharge", act + 28 + 13},
		}))

		Expect(kinds(events(PhysicalDRAMCommandIssue, 1))).To(Equal(
			[]string{"Activate", "Write", "WritePrecharge"}))
		stored, err := memCtrl.storage.Read(0x40, 64)
		Expect(err).NotTo(HaveOccurred())
		Expect(stored).To(Equal(data))

		stats := memCtrl.GetRowContinuationStats()
		Expect(stats.RowHoldEnabled).To(BeTrue())
		Expect(stats.RowHoldAutoPrechargeStops).To(Equal(uint64(2)))
		Expect(stats.ActivateCommands).To(Equal(uint64(2)))
	})

	DescribeTable("should hand the open row to the next line of the row",
		func(rowContinuation bool, want []string, stops uint64) {
			build(rowContinuation)
			// Both lines map to pseudo channel 0, bank 0, row 0.
			for _, addr := range []uint64{0x0000, 0x1000} {
				send(mem.ReadReqBuilder{}.
					WithAddress(addr).
					WithByteSize(64).
					Build())
			}
			srcPort.EXPECT().Recv(gomock.Any()).Times(2)

			engine.Run()

			Expect(kinds(events(PhysicalDRAMCommandIssue, 0))).
				To(Equal(want))
			stats := memCtrl.GetRowContinuationStats()
			Expect(stats.AutoPrechargeStops).To(Equal(stops))
			Expect(stats.RowHoldAutoPrechargeStops).To(Equal(uint64(2)))
		},
		Entry("with row continuation", true,
			[]string{"Activate", "Read", "Read", "Read", "ReadPrecharge"},
			uint64(1)),
		Entry("without row continuation", false,
			[]string{
				"Activate", "Read", "ReadPrecharge",
				"Activate", "Read", "ReadPrecharge",
			},
			uint64(0)),
	)
})
