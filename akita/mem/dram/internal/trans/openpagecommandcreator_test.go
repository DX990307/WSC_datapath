package trans

import (
	"github.com/golang/mock/gomock"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/sarchlab/akita/v3/mem/dram/internal/addressmapping"
	"github.com/sarchlab/akita/v3/mem/dram/internal/signal"
	"github.com/sarchlab/akita/v3/mem/mem"
)

var _ = Describe("OpenPageCommandCreator", func() {
	It("creates a read command without auto-precharge", func() {
		mockCtrl := gomock.NewController(GinkgoT())
		mapper := NewMockMapper(mockCtrl)
		creator := &OpenPageCommandCreator{AddrMapper: mapper}
		read := mem.ReadReqBuilder{}.Build()
		transaction := &signal.Transaction{Read: read}
		subTransaction := &signal.SubTransaction{
			Transaction: transaction,
			Address:     0x80,
		}
		location := addressmapping.Location{
			Rank: 1,
			Bank: 2,
			Row:  3,
		}
		mapper.EXPECT().Map(uint64(0x80)).Return(location)

		cmd := creator.Create(subTransaction)

		Expect(cmd.Kind).To(Equal(signal.CmdKindRead))
		Expect(cmd.Location).To(Equal(location))
		Expect(cmd.SubTrans).To(BeIdenticalTo(subTransaction))
	})
})
