package dram

import (
	"github.com/golang/mock/gomock"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/sarchlab/akita/v3/mem/dram/internal/signal"
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
