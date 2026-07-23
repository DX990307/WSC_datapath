package addressmapping

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Default Mapper", func() {
	var (
		mapper Mapper
		table  map[uint64]Location
	)

	BeforeEach(func() {
		mapper = MakeBuilder().Build()
		table = map[uint64]Location{
			0x0000_0000_0000_0000: {0, 0, 0, 0, 0, 0},
			0x0000_0000_0000_0040: {0, 0, 0, 0, 0, 1},
			0x0000_0000_0002_0040: {0, 0, 0, 0, 1, 1},
			0x0000_0000_0002_4040: {0, 0, 0, 1, 1, 1},
		}
	})

	It("should map", func() {
		for addr, location := range table {
			loc := mapper.Map(addr)
			Expect(loc).To(Equal(location))
		}
	})

	It("keeps both 64-byte reads of an aligned pair in one bank and row", func() {
		pairedMapper := MakeBuilder().
			WithBusWidth(128).
			WithBurstLength(4).
			WithNumChannel(1).
			WithNumRank(32).
			WithNumBankGroup(4).
			WithNumBank(4).
			WithNumCol(64).
			WithNumRow(16384).
			Build()

		for base := uint64(0); base < 4096; base += 128 {
			lower := pairedMapper.Map(base)
			upper := pairedMapper.Map(base + 64)
			Expect(upper.Channel).To(Equal(lower.Channel))
			Expect(upper.Rank).To(Equal(lower.Rank))
			Expect(upper.BankGroup).To(Equal(lower.BankGroup))
			Expect(upper.Bank).To(Equal(lower.Bank))
			Expect(upper.Row).To(Equal(lower.Row))
			Expect(upper.Column).To(Equal(lower.Column + 1))
		}
	})

})
