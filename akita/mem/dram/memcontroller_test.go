package dram

import (
	"strings"

	"github.com/golang/mock/gomock"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/sarchlab/akita/v3/mem/dram/internal/addressmapping"
	"github.com/sarchlab/akita/v3/mem/dram/internal/cmdq"
	"github.com/sarchlab/akita/v3/mem/dram/internal/org"
	"github.com/sarchlab/akita/v3/mem/dram/internal/signal"
	"github.com/sarchlab/akita/v3/mem/dram/internal/trans"
	"github.com/sarchlab/akita/v3/mem/mem"
	"github.com/sarchlab/akita/v3/sim"
)

var _ = Describe("MemController", func() {
	var (
		mockCtrl *gomock.Controller

		topPort             *MockPort
		addrConverter       *MockAddressConverter
		subTransSplitter    *MockSubTransSplitter
		subTransactionQueue *MockSubTransactionQueue
		cmdQueue            *MockCommandQueue
		channel             *MockChannel
		storage             *mem.Storage

		memCtrl *MemController
	)

	BeforeEach(func() {
		mockCtrl = gomock.NewController(GinkgoT())

		topPort = NewMockPort(mockCtrl)
		subTransactionQueue = NewMockSubTransactionQueue(mockCtrl)
		subTransSplitter = NewMockSubTransSplitter(mockCtrl)
		addrConverter = NewMockAddressConverter(mockCtrl)
		cmdQueue = NewMockCommandQueue(mockCtrl)
		channel = NewMockChannel(mockCtrl)
		storage = mem.NewStorage(4 * mem.GB)

		memCtrl = MakeBuilder().Build("MemCtrl")
		memCtrl.topPort = topPort
		memCtrl.subTransactionQueue = subTransactionQueue
		memCtrl.subTransSplitter = subTransSplitter
		memCtrl.addrConverter = addrConverter
		memCtrl.cmdQueue = cmdQueue
		memCtrl.channel = channel
		memCtrl.storage = storage
	})

	AfterEach(func() {
		mockCtrl.Finish()
	})

	Context("parse top", func() {
		It("should expose paired-read queue admission without mutating state", func() {
			subTransactionQueue.EXPECT().CanPush(2).Return(true)
			Expect(memCtrl.CanAcceptPhysicalAccesses(2)).To(BeTrue())
		})

		It("should count two 32-B units per line in the admission hint", func() {
			memCtrl.physicalAccessBytes = 32
			subTransactionQueue.EXPECT().CanPush(4).Return(false)
			Expect(memCtrl.CanAcceptPhysicalAccesses(2)).To(BeFalse())
		})

		It("should count one 128-B unit per line in the admission hint", func() {
			memCtrl.physicalAccessBytes = 128
			subTransactionQueue.EXPECT().CanPush(2).Return(true)
			Expect(memCtrl.CanAcceptPhysicalAccesses(2)).To(BeTrue())
		})

		It("should do nothing if no message", func() {
			topPort.EXPECT().Peek().Return(nil)

			madeProgress := memCtrl.parseTop(10)

			Expect(madeProgress).To(BeFalse())
		})

		It("should stall if substransaction queue is full", func() {
			read := mem.ReadReqBuilder{}.
				WithAddress(0x1000).
				Build()

			topPort.EXPECT().Peek().Return(read)
			addrConverter.EXPECT().ConvertExternalToInternal(uint64(0x1000))
			subTransSplitter.EXPECT().
				Split(gomock.Any()).
				Do(func(t *signal.Transaction) {
					Expect(t.Read).To(BeIdenticalTo(read))
					t.SubTransactions = make([]*signal.SubTransaction, 3)
				})
			subTransactionQueue.EXPECT().CanPush(3).Return(false)

			madeProgress := memCtrl.parseTop(10)

			Expect(madeProgress).To(BeFalse())
		})

		It("should push sub-transactions to subtrans queue", func() {
			read := mem.ReadReqBuilder{}.
				WithAddress(0x1000).
				Build()

			topPort.EXPECT().Peek().Return(read)
			topPort.EXPECT().Retrieve(gomock.Any()).Return(read)
			addrConverter.EXPECT().ConvertExternalToInternal(uint64(0x1000))
			subTransSplitter.EXPECT().
				Split(gomock.Any()).
				Do(func(t *signal.Transaction) {
					Expect(t.Read).To(BeIdenticalTo(read))
					for i := 0; i < 3; i++ {
						st := &signal.SubTransaction{}
						t.SubTransactions = append(t.SubTransactions, st)
					}
				})
			subTransactionQueue.EXPECT().CanPush(3).Return(true)
			subTransactionQueue.EXPECT().Push(gomock.Any())

			madeProgress := memCtrl.parseTop(10)

			Expect(madeProgress).To(BeTrue())
			Expect(memCtrl.inflightTransactions).To(HaveLen(1))
			stats := memCtrl.GetPhysicalAccessStats()
			Expect(stats.ReadAccesses).To(Equal(uint64(3)))
			Expect(stats.WriteAccesses).To(BeZero())
			Expect(stats.FrontEndReadRequests).To(Equal(uint64(1)))
			Expect(stats.FrontEndReadBytes).To(Equal(read.AccessByteSize))
		})

		It("should keep two 64-byte reads as independent transactions", func() {
			first := mem.ReadReqBuilder{}.
				WithAddress(0x1000).
				WithByteSize(64).
				Build()
			second := mem.ReadReqBuilder{}.
				WithAddress(0x1040).
				WithByteSize(64).
				Build()

			topPort.EXPECT().Peek().Return(first)
			topPort.EXPECT().Retrieve(gomock.Any()).Return(first)
			addrConverter.EXPECT().ConvertExternalToInternal(uint64(0x1000)).
				Return(uint64(0x100))
			subTransSplitter.EXPECT().Split(gomock.Any()).Do(
				func(t *signal.Transaction) {
					t.SubTransactions = []*signal.SubTransaction{{
						ID: "first-read", Transaction: t, Address: 0x1000,
					}}
				},
			)
			subTransactionQueue.EXPECT().CanPush(1).Return(true)
			subTransactionQueue.EXPECT().Push(gomock.Any())
			Expect(memCtrl.parseTop(10)).To(BeTrue())

			topPort.EXPECT().Peek().Return(second)
			topPort.EXPECT().Retrieve(gomock.Any()).Return(second)
			addrConverter.EXPECT().ConvertExternalToInternal(uint64(0x1040)).
				Return(uint64(0x140))
			subTransSplitter.EXPECT().Split(gomock.Any()).Do(
				func(t *signal.Transaction) {
					t.SubTransactions = []*signal.SubTransaction{{
						ID: "second-read", Transaction: t, Address: 0x1000,
					}}
				},
			)
			subTransactionQueue.EXPECT().CanPush(1).Return(true)
			subTransactionQueue.EXPECT().Push(gomock.Any())
			Expect(memCtrl.parseTop(11)).To(BeTrue())

			Expect(memCtrl.inflightTransactions).To(HaveLen(2))
			Expect(memCtrl.GetPhysicalAccessStats().ReadAccesses).
				To(Equal(uint64(2)))
			stats := memCtrl.GetPhysicalAccessStats()
			Expect(stats.FrontEndReadRequests).To(Equal(uint64(2)))
			Expect(stats.FrontEndReadBytes).To(Equal(uint64(128)))
		})

		It("should count one paired descriptor as two independent 64-byte reads", func() {
			demand := mem.ReadReqBuilder{}.
				WithAddress(0x1000).
				WithByteSize(64).
				WithPairedRead("pair-1", mem.PairedReadDemand).
				Build()
			sibling := mem.ReadReqBuilder{}.
				WithAddress(0x1040).
				WithByteSize(64).
				WithPairedRead("pair-1", mem.PairedReadSibling).
				Build()
			descriptor := mem.PairedReadReqBuilder{}.
				WithReads(demand, sibling).
				Build()
			topPort.EXPECT().Peek().Return(descriptor)
			topPort.EXPECT().Retrieve(gomock.Any()).Return(descriptor)
			addrConverter.EXPECT().ConvertExternalToInternal(uint64(0x1000)).
				Return(uint64(0x100))
			subTransSplitter.EXPECT().Split(gomock.Any()).Do(
				func(t *signal.Transaction) {
					t.SubTransactions = []*signal.SubTransaction{{
						ID: "paired-demand", Transaction: t, Address: 0x1000,
					}}
				},
			)
			addrConverter.EXPECT().ConvertExternalToInternal(uint64(0x1040)).
				Return(uint64(0x140))
			subTransSplitter.EXPECT().Split(gomock.Any()).Do(
				func(t *signal.Transaction) {
					t.SubTransactions = []*signal.SubTransaction{{
						ID: "paired-sibling", Transaction: t, Address: 0x1040,
					}}
				},
			)
			subTransactionQueue.EXPECT().CanPush(2).Return(true)
			subTransactionQueue.EXPECT().Push(gomock.Any()).Times(2)
			Expect(memCtrl.parseTop(10)).To(BeTrue())

			stats := memCtrl.GetPhysicalAccessStats()
			Expect(stats.FrontEndReadRequests).To(Equal(uint64(2)))
			Expect(stats.FrontEndReadBytes).To(Equal(uint64(128)))
			Expect(stats.ReadAccesses).To(Equal(uint64(2)))
			Expect(stats.PairedReadDescriptors).To(Equal(uint64(1)))
			Expect(stats.PairedReadMembers).To(Equal(uint64(2)))
			Expect(stats.PairedDemandMembers).To(Equal(uint64(1)))
			Expect(stats.PairedSiblingMembers).To(Equal(uint64(1)))
		})

		It("should strip sub-transaction addresses but not the storage address", func() {
			memCtrl.addrConverter = nil
			memCtrl.mappingConverter = interleaveStrip{granularity: 128, units: 8}
			read := mem.ReadReqBuilder{}.
				WithAddress(0x480).
				WithByteSize(128).
				Build()

			topPort.EXPECT().Peek().Return(read)
			topPort.EXPECT().Retrieve(gomock.Any()).Return(read)
			subTransSplitter.EXPECT().Split(gomock.Any()).Do(
				func(t *signal.Transaction) {
					t.SubTransactions = []*signal.SubTransaction{
						{ID: "low", Transaction: t, Address: 0x480},
						{ID: "high", Transaction: t, Address: 0x4C0},
					}
				},
			)
			subTransactionQueue.EXPECT().CanPush(2).Return(true)
			var pushed *signal.Transaction
			subTransactionQueue.EXPECT().Push(gomock.Any()).Do(
				func(t *signal.Transaction) { pushed = t })

			Expect(memCtrl.parseTop(10)).To(BeTrue())

			Expect(pushed.InternalAddress).To(Equal(uint64(0x480)))
			Expect(pushed.SubTransactions[0].Address).To(Equal(uint64(0x080)))
			Expect(pushed.SubTransactions[1].Address).To(Equal(uint64(0x0C0)))
		})
	})

	Context("issue", func() {
		It("should not issue if nothing is ready", func() {
			cmdQueue.EXPECT().
				GetCommandToIssue(sim.VTimeInSec(10)).
				Return(nil)

			madeProgress := memCtrl.issue(10)

			Expect(madeProgress).To(BeFalse())
		})

		It("should issue", func() {
			cmd := &signal.Command{}
			cmdQueue.EXPECT().
				GetCommandToIssue(sim.VTimeInSec(10)).
				Return(cmd)
			channel.EXPECT().StartCommand(sim.VTimeInSec(10), cmd)
			channel.EXPECT().UpdateTiming(sim.VTimeInSec(10), cmd)

			madeProgress := memCtrl.issue(10)

			Expect(madeProgress).To(BeTrue())
		})

		It("should ask for one command per tick without the dual bus", func() {
			cmd := &signal.Command{Kind: signal.CmdKindActivate}
			cmdQueue.EXPECT().
				GetCommandToIssue(sim.VTimeInSec(10)).
				Return(cmd).
				Times(1)
			channel.EXPECT().StartCommand(sim.VTimeInSec(10), cmd)
			channel.EXPECT().UpdateTiming(sim.VTimeInSec(10), cmd)

			Expect(memCtrl.issue(10)).To(BeTrue())
			Expect(memCtrl.rowBusFreeAt).To(BeZero())
		})

		It("should panic if the dual bus has no bus-aware queue", func() {
			memCtrl.dualCommandBus = true

			Expect(func() { memCtrl.issue(10) }).To(Panic())
		})
	})

	Context("respond", func() {
		It("should do nothing if there is no transaction", func() {
			madeProgress := memCtrl.respond(10)

			Expect(madeProgress).To(BeFalse())
		})

		It("should do nothing if there is no completed transaction",
			func() {
				trans := &signal.Transaction{}
				subTransaction := &signal.SubTransaction{
					Transaction: trans,
					Completed:   false,
				}
				trans.SubTransactions = append(trans.SubTransactions,
					subTransaction)
				memCtrl.inflightTransactions = append(
					memCtrl.inflightTransactions, trans)

				madeProgress := memCtrl.respond(10)

				Expect(madeProgress).To(BeFalse())
			})

		It("should send write done response", func() {
			write := mem.WriteReqBuilder{}.
				WithAddress(0x40).
				WithData([]byte{1, 2, 3, 4}).
				Build()
			trans := &signal.Transaction{
				InternalAddress: 0x40,
				Write:           write,
			}
			subTransaction := &signal.SubTransaction{
				Transaction: trans,
				Completed:   true,
			}
			trans.SubTransactions = append(trans.SubTransactions,
				subTransaction)
			memCtrl.inflightTransactions = append(memCtrl.inflightTransactions,
				trans)

			topPort.EXPECT().Send(gomock.Any()).Return(nil)

			madeProgress := memCtrl.respond(10)

			Expect(madeProgress).To(BeTrue())
			data, _ := storage.Read(0x40, 4)
			Expect(data).To(Equal([]byte{1, 2, 3, 4}))
			Expect(memCtrl.inflightTransactions).NotTo(ContainElement(trans))
		})

		It("should send data ready response", func() {
			storage.Write(0x40, []byte{1, 2, 3, 4})
			read := mem.ReadReqBuilder{}.
				WithAddress(0x40).
				WithByteSize(4).
				Build()
			trans := &signal.Transaction{
				InternalAddress: 0x40,
				Read:            read,
			}
			subTransaction := &signal.SubTransaction{
				Transaction: trans,
				Completed:   true,
			}
			trans.SubTransactions = append(trans.SubTransactions,
				subTransaction)
			memCtrl.inflightTransactions = append(memCtrl.inflightTransactions,
				trans)

			topPort.EXPECT().Send(gomock.Any()).Do(func(dr *mem.DataReadyRsp) {
				Expect(dr.Data).To(Equal([]byte{1, 2, 3, 4}))
			}).Return(nil)

			madeProgress := memCtrl.respond(10)

			Expect(madeProgress).To(BeTrue())
			Expect(memCtrl.inflightTransactions).NotTo(ContainElement(trans))
		})

		It("should return a completed paired demand without waiting for sibling", func() {
			storage.Write(0x40, []byte{1, 2, 3, 4})
			demand := mem.ReadReqBuilder{}.
				WithAddress(0x40).
				WithByteSize(4).
				WithPairedRead("pair", mem.PairedReadDemand).
				Build()
			sibling := mem.ReadReqBuilder{}.
				WithAddress(0x80).
				WithByteSize(4).
				WithPairedRead("pair", mem.PairedReadSibling).
				Build()
			demandTrans := &signal.Transaction{
				InternalAddress: 0x40, Read: demand,
			}
			demandTrans.SubTransactions = []*signal.SubTransaction{{
				Transaction: demandTrans, Completed: true,
			}}
			siblingTrans := &signal.Transaction{
				InternalAddress: 0x80, Read: sibling,
			}
			siblingTrans.SubTransactions = []*signal.SubTransaction{{
				Transaction: siblingTrans, Completed: false,
			}}
			memCtrl.inflightTransactions = []*signal.Transaction{
				demandTrans, siblingTrans,
			}

			topPort.EXPECT().Send(gomock.Any()).Do(func(rsp *mem.DataReadyRsp) {
				Expect(rsp.RespondTo).To(Equal(demand.ID))
			}).Return(nil)

			Expect(memCtrl.respond(10)).To(BeTrue())
			Expect(memCtrl.inflightTransactions).To(ConsistOf(siblingTrans))
		})
	})
})

var _ = Describe("MemController with mapping interleave strip", func() {
	var (
		mockCtrl *gomock.Controller
		engine   sim.Engine
		srcPort  *MockPort
		storage  *mem.Storage
		memCtrl  *MemController
	)

	BeforeEach(func() {
		mockCtrl = gomock.NewController(GinkgoT())
		engine = sim.NewSerialEngine()
		storage = mem.NewStorage(4 * mem.GB)
		// Controller 1 of 8, interleaved every 128 B.
		memCtrl = MakeBuilder().
			WithEngine(engine).
			WithGlobalStorage(storage).
			WithMappingInterleaveStrip(128, 8).
			Build("MemCtrl")
		srcPort = NewMockPort(mockCtrl)

		conn := sim.NewDirectConnection("Conn", engine, 1*sim.GHz)
		srcPort.EXPECT().SetConnection(conn)
		conn.PlugIn(memCtrl.topPort, 1)
		conn.PlugIn(srcPort, 1)
	})

	AfterEach(func() {
		mockCtrl.Finish()
	})

	It("maps the stripped address but stores at the global address", func() {
		var enqueued []*signal.Command
		queue := memCtrl.subTransactionQueue.(*trans.FCFSSubTransactionQueue)
		queue.CommandEnqueued = func(_ sim.VTimeInSec, cmd *signal.Command) {
			enqueued = append(enqueued, cmd)
		}
		stripped := memCtrl.addrMapper.Map(0x080)
		Expect(memCtrl.addrMapper.Map(0x480)).NotTo(Equal(stripped))

		write := mem.WriteReqBuilder{}.
			WithAddress(0x480).
			WithData([]byte{1, 2, 3, 4}).
			WithSrc(srcPort).
			WithDst(memCtrl.topPort).
			WithSendTime(0).
			Build()
		read := mem.ReadReqBuilder{}.
			WithAddress(0x480).
			WithByteSize(4).
			WithSrc(srcPort).
			WithDst(memCtrl.topPort).
			WithSendTime(0).
			Build()
		memCtrl.topPort.Recv(write)
		memCtrl.topPort.Recv(read)

		writeDone := srcPort.EXPECT().
			Recv(gomock.Any()).
			Do(func(wd *mem.WriteDoneRsp) {
				Expect(wd.RespondTo).To(Equal(write.ID))
			})
		srcPort.EXPECT().
			Recv(gomock.Any()).
			Do(func(dr *mem.DataReadyRsp) {
				Expect(dr.RespondTo).To(Equal(read.ID))
				Expect(dr.Data).To(Equal([]byte{1, 2, 3, 4}))
			}).After(writeDone)

		engine.Run()

		Expect(enqueued).To(HaveLen(2))
		for _, cmd := range enqueued {
			Expect(cmd.Location).To(Equal(stripped))
			Expect(cmd.SubTrans.Address).To(Equal(uint64(0x080)))
			Expect(cmd.SubTrans.Transaction.InternalAddress).
				To(Equal(uint64(0x480)))
		}
		data, _ := storage.Read(0x480, 4)
		Expect(data).To(Equal([]byte{1, 2, 3, 4}))
		data, _ = storage.Read(0x080, 4)
		Expect(data).To(Equal([]byte{0, 0, 0, 0}))
	})

	It("reports the mapper-domain address to the physical observer", func() {
		observer := &collectingPhysicalDRAMObserver{}
		memCtrl.SetPhysicalDRAMObserver(observer, "GPU[0]", 1)
		read := mem.ReadReqBuilder{}.
			WithAddress(0x480).
			WithByteSize(4).
			WithSrc(srcPort).
			WithDst(memCtrl.topPort).
			WithSendTime(0).
			Build()
		memCtrl.topPort.Recv(read)
		srcPort.EXPECT().Recv(gomock.Any())

		engine.Run()

		stripped := memCtrl.addrMapper.Map(0x080)
		Expect(observer.events).NotTo(BeEmpty())
		for _, e := range observer.events {
			Expect(e.ExternalAddress).To(Equal(uint64(0x480)))
			Expect(e.InternalAddress).To(Equal(uint64(0x480)))
			Expect(e.SubtransactionAddress).To(Equal(uint64(0x080)))
			Expect(e.InternalSubtransactionAddress).To(Equal(uint64(0x080)))
			Expect(e.Row).To(Equal(stripped.Row))
			Expect(e.Column).To(Equal(stripped.Column))
			Expect(e.Bank).To(Equal(stripped.Bank))
			Expect(e.Rank).To(Equal(stripped.Rank))
		}
	})
})

var _ = Describe("MemController admission hint", func() {
	It("should count 32-B units against the sub-transaction queue room",
		func() {
			narrow := pseudoChannelBuilder(nil).
				WithTransactionQueueSize(8).
				Build("Narrow")
			wide := MakeBuilder().WithTransactionQueueSize(8).Build("Wide")
			for _, m := range []*MemController{narrow, wide} {
				queue := m.subTransactionQueue.(*trans.FCFSSubTransactionQueue)
				queue.Queue = make([]*signal.SubTransaction, 5)
			}

			Expect(narrow.CanAcceptPhysicalAccesses(2)).To(BeFalse())
			Expect(narrow.CanAcceptPhysicalAccesses(1)).To(BeTrue())
			Expect(wide.CanAcceptPhysicalAccesses(3)).To(BeTrue())
			Expect(wide.CanAcceptPhysicalAccesses(4)).To(BeFalse())
		})
})

var _ = Describe("MemController with pseudo channels", func() {
	var (
		mockCtrl *gomock.Controller
		engine   sim.Engine
		srcPort  *MockPort
		tracer   *countingTracer
		memCtrl  *MemController
	)

	BeforeEach(func() {
		mockCtrl = gomock.NewController(GinkgoT())
		engine = sim.NewSerialEngine()
		tracer = &countingTracer{}
		memCtrl = pseudoChannelBuilder(engine).
			WithGlobalStorage(mem.NewStorage(1 * mem.GB)).
			WithAdditionalTracer(tracer).
			Build("MemCtrl")
		srcPort = NewMockPort(mockCtrl)

		conn := sim.NewDirectConnection("Conn", engine, 1*sim.GHz)
		srcPort.EXPECT().SetConnection(conn)
		conn.PlugIn(memCtrl.topPort, 4)
		conn.PlugIn(srcPort, 4)
	})

	AfterEach(func() {
		mockCtrl.Finish()
	})

	It("observer and tracer cover both pseudo-channels", func() {
		observer := &collectingPhysicalDRAMObserver{}
		memCtrl.SetPhysicalDRAMObserver(observer, "GPU[0]", 0)
		// Line 0x00 maps to pseudo channel 0 and line 0x40 to pseudo
		// channel 1; each line is two 32-B accesses.
		for _, addr := range []uint64{0x00, 0x40} {
			read := mem.ReadReqBuilder{}.
				WithAddress(addr).
				WithByteSize(64).
				WithSrc(srcPort).
				WithDst(memCtrl.topPort).
				WithSendTime(0).
				Build()
			memCtrl.topPort.Recv(read)
		}
		srcPort.EXPECT().Recv(gomock.Any()).Times(2)

		engine.Run()

		completed := map[uint64]map[string]int{0: {}, 1: {}}
		columnIssues := 0
		readySameOpenRow := map[uint64]int{}
		for _, e := range observer.events {
			switch e.Kind {
			case PhysicalDRAMCommandComplete:
				completed[e.Channel][e.CommandKind]++
			case PhysicalDRAMCommandIssue:
				if e.CommandKind != "ReadPrecharge" {
					continue
				}
				columnIssues++
				Expect(e.OpenRowValid).To(BeTrue())
				Expect(e.ColumnRowHit).To(BeTrue())
				readySameOpenRow[e.Channel] += e.ReadySameOpenRow
			}
		}
		Expect(columnIssues).To(Equal(4))
		// When the first half of a line issues, the second half waits in
		// the queue as a ready row hit of the same pseudo channel; the
		// line of the other pseudo channel, at the same bank and row
		// indices, does not count.
		Expect(readySameOpenRow).To(Equal(map[uint64]int{0: 1, 1: 1}))
		for ch := uint64(0); ch < 2; ch++ {
			Expect(completed[ch]).To(Equal(map[string]int{
				"Activate":      2,
				"ReadPrecharge": 2,
			}), "channel %d", ch)
		}

		started := map[string]string{}
		cmdTasks := map[string]int{}
		for _, task := range tracer.starts {
			if task.Kind != "cmd" {
				continue
			}
			started[task.ID] = task.Where
			for ch := 0; ch < 2; ch++ {
				prefix := []string{
					"MemCtrl.Channel[0].Bank[", "MemCtrl.Channel[1].Bank[",
				}[ch]
				if strings.HasPrefix(task.Where, prefix) {
					cmdTasks[prefix]++
				}
			}
		}
		Expect(cmdTasks).To(Equal(map[string]int{
			"MemCtrl.Channel[0].Bank[": 4,
			"MemCtrl.Channel[1].Bank[": 4,
		}))
		ended := 0
		for _, task := range tracer.ends {
			if _, ok := started[task.ID]; ok {
				ended++
			}
		}
		Expect(ended).To(Equal(8))
	})
})

var _ = Describe("MemController with the dual command bus", func() {
	var (
		memCtrl *MemController
		queue   *cmdq.CommandQueueImpl
	)

	build := func(activateCycles int) {
		memCtrl = MakeBuilder().
			WithEngine(sim.NewSerialEngine()).
			WithDualCommandBus(true).
			WithActivateCommandCycles(activateCycles).
			Build("MemCtrl")
		queue = memCtrl.cmdQueue.(*cmdq.CommandQueueImpl)
	}

	at := func(cycle int) sim.VTimeInSec {
		return sim.VTimeInSec(cycle) * memCtrl.Freq.Period()
	}

	read := func(id string, rank, bank, row uint64) *signal.Command {
		t := &signal.Transaction{
			Read: mem.ReadReqBuilder{}.WithByteSize(64).Build(),
		}
		st := &signal.SubTransaction{ID: id + ".st", Transaction: t}
		t.SubTransactions = []*signal.SubTransaction{st}
		return &signal.Command{
			ID:       id,
			Kind:     signal.CmdKindReadPrecharge,
			SubTrans: st,
			Location: addressmapping.Location{
				Rank: rank, Bank: bank, Row: row,
			},
		}
	}

	bankState := func(rank, bank uint64) (bool, uint64) {
		return memCtrl.channel.(org.BankStateObserver).
			ObservationBankStateIn(0, rank, 0, bank)
	}

	// openRowAndIssue opens bank 1 of rank 0 at row 5, then queues a read
	// that needs an activate in bank 0 ahead of a row hit in bank 1, and
	// ticks the controller at cycle 30.
	openRowAndIssue := func() (needsActivate, rowHit *signal.Command) {
		needsActivate = read("A", 0, 0, 3)
		rowHit = read("B", 0, 1, 5)
		act := rowHit.Clone()
		act.Kind = signal.CmdKindActivate
		memCtrl.channel.StartCommand(0, act)
		memCtrl.channel.UpdateTiming(0, act)
		for cycle := 1; cycle < 30; cycle++ {
			memCtrl.Tick(at(cycle))
		}
		queue.Accept(needsActivate)
		queue.Accept(rowHit)

		Expect(memCtrl.Tick(at(30))).To(BeTrue())
		return needsActivate, rowHit
	}

	It("should issue an activate and a read in the same tick", func() {
		build(2)

		needsActivate, _ := openRowAndIssue()

		open, row := bankState(0, 0)
		Expect(open).To(BeTrue())
		Expect(row).To(Equal(uint64(3)))
		open, _ = bankState(0, 1)
		Expect(open).To(BeFalse())
		Expect(queue.Queues[0]).To(ConsistOf(needsActivate))
		stats := queue.GetRowContinuationStats()
		Expect(stats.ActivateCommands).To(Equal(uint64(1)))
		Expect(stats.ColumnCommands).To(Equal(uint64(1)))
		Expect(memCtrl.rowBusFreeAt).To(Equal(uint64(32)))
	})

	It("should keep the row bus busy for a two-cycle activate", func() {
		build(2)
		openRowAndIssue()
		queue.Accept(read("C", 1, 0, 2))

		Expect(memCtrl.Tick(at(31))).To(BeTrue())
		open, _ := bankState(1, 0)
		Expect(open).To(BeFalse())

		memCtrl.Tick(at(32))
		open, row := bankState(1, 0)
		Expect(open).To(BeTrue())
		Expect(row).To(Equal(uint64(2)))
		Expect(memCtrl.rowBusFreeAt).To(Equal(uint64(34)))
	})

	It("should free the row bus after one cycle by default", func() {
		build(0)
		openRowAndIssue()
		queue.Accept(read("C", 1, 0, 2))

		memCtrl.Tick(at(31))
		open, row := bankState(1, 0)
		Expect(open).To(BeTrue())
		Expect(row).To(Equal(uint64(2)))
	})
})
