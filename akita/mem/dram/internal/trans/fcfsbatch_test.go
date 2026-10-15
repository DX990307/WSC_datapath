package trans

import (
	gomock "github.com/golang/mock/gomock"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/sarchlab/akita/v3/mem/dram/internal/addressmapping"
	"github.com/sarchlab/akita/v3/mem/dram/internal/signal"
	"github.com/sarchlab/akita/v3/mem/mem"
	"github.com/sarchlab/akita/v3/sim"
)

// batchQueue is a command queue that admits command batches. It accepts a
// batch while the batch, together with the accepted commands, fits the
// capacity of the queue of each rank.
type batchQueue struct {
	*MockCommandQueue
	capacity int
	queues   map[uint64][]*signal.Command
	checked  [][]*signal.Command
}

func (q *batchQueue) CanAcceptAll(cmds []*signal.Command) bool {
	q.checked = append(q.checked, cmds)
	need := map[uint64]int{}
	for _, cmd := range cmds {
		need[cmd.Rank]++
	}
	for rank, n := range need {
		if len(q.queues[rank])+n > q.capacity {
			return false
		}
	}
	return true
}

func (q *batchQueue) AcceptAll(cmds []*signal.Command) {
	for _, cmd := range cmds {
		q.queues[cmd.Rank] = append(q.queues[cmd.Rank], cmd)
	}
}

var _ = Describe("FCFSSubTransactionQueue batching by transaction", func() {
	var (
		mockCtrl *gomock.Controller
		batch    *batchQueue
		queue    *FCFSSubTransactionQueue
		enqueued []string
	)

	// transaction makes a read transaction with n sub-transactions in the
	// given rank.
	transaction := func(
		id string,
		n int,
		rank uint64,
		read *mem.ReadReq,
	) *signal.Transaction {
		if read == nil {
			read = mem.ReadReqBuilder{}.WithByteSize(64).Build()
		}
		t := &signal.Transaction{Read: read}
		for i := 0; i < n; i++ {
			t.SubTransactions = append(t.SubTransactions,
				&signal.SubTransaction{
					ID:          id + "." + string(rune('0'+i)),
					Transaction: t,
					Address:     rank,
				})
		}
		return t
	}

	push := func(ts ...*signal.Transaction) {
		for _, t := range ts {
			queue.Push(t)
		}
	}

	BeforeEach(func() {
		mockCtrl = gomock.NewController(GinkgoT())
		batch = &batchQueue{
			MockCommandQueue: NewMockCommandQueue(mockCtrl),
			capacity:         4,
			queues:           map[uint64][]*signal.Command{},
		}
		mapper := NewMockMapper(mockCtrl)
		mapper.EXPECT().Map(gomock.Any()).
			DoAndReturn(func(addr uint64) addressmapping.Location {
				return addressmapping.Location{Rank: addr}
			}).AnyTimes()
		enqueued = nil
		queue = &FCFSSubTransactionQueue{
			Capacity:           16,
			CmdQueue:           batch,
			CmdCreator:         &ClosePageCommandCreator{AddrMapper: mapper},
			BatchByTransaction: true,
			CommandEnqueued: func(_ sim.VTimeInSec, cmd *signal.Command) {
				enqueued = append(enqueued, cmd.SubTrans.ID)
			},
		}
	})

	AfterEach(func() {
		mockCtrl.Finish()
	})

	It("should enqueue all sub-transactions of a transaction together",
		func() {
			a := transaction("A", 2, 0, nil)
			b := transaction("B", 2, 0, nil)
			push(a, b)

			Expect(queue.Tick(10)).To(BeTrue())

			Expect(batch.queues[0]).To(HaveLen(2))
			Expect(batch.queues[0][0].SubTrans).
				To(BeIdenticalTo(a.SubTransactions[0]))
			Expect(batch.queues[0][1].SubTrans).
				To(BeIdenticalTo(a.SubTransactions[1]))
			Expect(batch.queues[0][1].EnqueuedAt).
				To(Equal(sim.VTimeInSec(10)))
			Expect(enqueued).To(Equal([]string{"A.0", "A.1"}))
			Expect(queue.Queue).To(Equal(b.SubTransactions))
		})

	It("should skip a transaction that does not fit as a whole and admit "+
		"a later one", func() {
		batch.queues[0] = make([]*signal.Command, 3)
		a := transaction("A", 2, 0, nil)
		b := transaction("B", 2, 1, nil)
		push(a, b)

		Expect(queue.Tick(10)).To(BeTrue())

		Expect(batch.queues[0]).To(HaveLen(3))
		Expect(batch.queues[1]).To(HaveLen(2))
		Expect(enqueued).To(Equal([]string{"B.0", "B.1"}))
		Expect(queue.Queue).To(Equal(a.SubTransactions))

		Expect(queue.Tick(11)).To(BeFalse())
		Expect(queue.Queue).To(Equal(a.SubTransactions))
	})

	It("should admit a paired read of 2x2 sub-transactions atomically",
		func() {
			demand := transaction("D", 2, 0, mem.ReadReqBuilder{}.
				WithByteSize(64).
				WithPairedRead("pair", mem.PairedReadDemand).
				Build())
			sibling := transaction("S", 2, 1, mem.ReadReqBuilder{}.
				WithByteSize(64).
				WithPairedRead("pair", mem.PairedReadSibling).
				Build())
			push(demand, sibling)

			Expect(queue.Tick(10)).To(BeTrue())

			Expect(batch.checked).To(HaveLen(1))
			Expect(batch.checked[0]).To(HaveLen(4))
			Expect(batch.queues[0]).To(HaveLen(2))
			Expect(batch.queues[1]).To(HaveLen(2))
			Expect(enqueued).To(Equal([]string{"D.0", "D.1", "S.0", "S.1"}))
			Expect(queue.Queue).To(BeEmpty())
		})

	It("should admit the demand alone when the pair does not fit", func() {
		batch.queues[1] = make([]*signal.Command, 3)
		demand := transaction("D", 2, 0, mem.ReadReqBuilder{}.
			WithByteSize(64).
			WithPairedRead("pair", mem.PairedReadDemand).
			Build())
		sibling := transaction("S", 2, 1, mem.ReadReqBuilder{}.
			WithByteSize(64).
			WithPairedRead("pair", mem.PairedReadSibling).
			Build())
		push(demand, sibling)

		Expect(queue.Tick(10)).To(BeTrue())

		Expect(batch.checked).To(HaveLen(2))
		Expect(batch.checked[0]).To(HaveLen(4))
		Expect(batch.checked[1]).To(HaveLen(2))
		Expect(enqueued).To(Equal([]string{"D.0", "D.1"}))
		Expect(queue.Queue).To(Equal(sibling.SubTransactions))
	})

	It("should use CanAccept for single commands of a plain queue", func() {
		plain := NewMockCommandQueue(mockCtrl)
		queue.CmdQueue = plain
		a := transaction("A", 1, 0, nil)
		push(a)
		plain.EXPECT().CanAccept(gomock.Any()).Return(true)
		plain.EXPECT().Accept(gomock.Any())

		Expect(queue.Tick(10)).To(BeTrue())
		Expect(queue.Queue).To(BeEmpty())

		push(transaction("B", 2, 0, nil))
		Expect(func() { queue.Tick(11) }).To(Panic())
	})
})
