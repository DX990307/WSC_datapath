package addressmapping

import (
	"math/rand"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// mi200Builder is one HBM2e channel in pseudo-channel mode: 2 pseudo
// channels, 2 stack IDs, 4x4 banks, 32-B access units (64-bit bus, BL4).
func mi200Builder() Builder {
	return MakeBuilder().
		WithBusWidth(64).
		WithBurstLength(4).
		WithNumChannel(2).
		WithNumRank(2).
		WithNumBankGroup(4).
		WithNumBank(4).
		WithNumCol(128).
		WithNumRow(32768)
}

var mi200Layout = []FieldBits{
	{LocationItemColumn, 1},
	{LocationItemChannel, 1},
	{LocationItemBankGroup, 2},
	{LocationItemBank, 2},
	{LocationItemRank, 1},
	{LocationItemColumn, 4},
	{LocationItemRow, 15},
}

var _ = Describe("Layout Mapper", func() {
	It("should map with the mi200 layout", func() {
		mapper := mi200Builder().WithLayoutLowToHigh(mi200Layout).Build()
		table := map[uint64]Location{
			0x0000_0000: {},
			0x0000_001F: {},
			0x0000_0020: {Column: 1},
			0x0000_0040: {Channel: 1},
			0x0000_0080: {BankGroup: 1},
			0x0000_0200: {Bank: 1},
			0x0000_0800: {Rank: 1},
			0x0000_1000: {Column: 2},
			0x0001_0000: {Row: 1},
			0x4000_0000: {Row: 1 << 14},
			0x8000_0000: {},
			0x0005_FDE0: {
				Channel: 1, Rank: 1, BankGroup: 3, Bank: 2,
				Row: 5, Column: 31,
			},
		}

		for addr, location := range table {
			Expect(mapper.Map(addr)).To(Equal(location),
				"address 0x%x", addr)
		}
	})

	It("should keep both 32-B halves of a 64-B line in one bank and row",
		func() {
			mapper := mi200Builder().WithLayoutLowToHigh(mi200Layout).Build()

			for base := uint64(0); base < 64*1024; base += 64 {
				lower := mapper.Map(base)
				upper := mapper.Map(base + 32)
				Expect(upper.Channel).To(Equal(lower.Channel))
				Expect(upper.Rank).To(Equal(lower.Rank))
				Expect(upper.BankGroup).To(Equal(lower.BankGroup))
				Expect(upper.Bank).To(Equal(lower.Bank))
				Expect(upper.Row).To(Equal(lower.Row))
				Expect(upper.Column).To(Equal(lower.Column + 1))
			}
		})

	It("should put adjacent 64-B lines in different pseudo channels", func() {
		mapper := mi200Builder().WithLayoutLowToHigh(mi200Layout).Build()

		for base := uint64(0); base < 64*1024; base += 128 {
			Expect(mapper.Map(base).Channel).To(Equal(uint64(0)))
			Expect(mapper.Map(base + 64).Channel).To(Equal(uint64(1)))
		}
	})

	It("should match the default mapper for the default order", func() {
		builder := MakeBuilder().
			WithBusWidth(256).
			WithBurstLength(4).
			WithNumChannel(2).
			WithNumRank(16).
			WithNumBankGroup(4).
			WithNumBank(4).
			WithNumCol(64).
			WithNumRow(16384)
		defaultMapper := builder.Build()
		layoutMapper := builder.WithLayoutLowToHigh([]FieldBits{
			{LocationItemColumn, 4},
			{LocationItemBankGroup, 2},
			{LocationItemBank, 2},
			{LocationItemRank, 4},
			{LocationItemChannel, 1},
			{LocationItemRow, 14},
		}).Build()

		rng := rand.New(rand.NewSource(1))
		for i := 0; i < 10000; i++ {
			addr := rng.Uint64() & (1<<35 - 1)
			Expect(layoutMapper.Map(addr)).To(Equal(defaultMapper.Map(addr)),
				"address 0x%x", addr)
		}
	})

	It("should panic if the widths of a field do not add up", func() {
		layout := append([]FieldBits(nil), mi200Layout...)
		layout[3] = FieldBits{LocationItemBank, 1}

		Expect(func() {
			mi200Builder().WithLayoutLowToHigh(layout).Build()
		}).To(PanicWith(ContainSubstring("bank has 1 bits, want 2")))
	})

	It("should panic if a field is missing", func() {
		layout := mi200Layout[:len(mi200Layout)-1]

		Expect(func() {
			mi200Builder().WithLayoutLowToHigh(layout).Build()
		}).To(PanicWith(ContainSubstring("row has 0 bits, want 15")))
	})

	It("should panic on an empty segment or an invalid field", func() {
		empty := append([]FieldBits{{LocationItemRank, 0}}, mi200Layout...)
		invalid := append([]FieldBits{{LocationItemInvalid, 1}},
			mi200Layout...)

		Expect(func() {
			mi200Builder().WithLayoutLowToHigh(empty).Build()
		}).To(PanicWith(ContainSubstring("rank segment has 0 bits")))
		Expect(func() {
			mi200Builder().WithLayoutLowToHigh(invalid).Build()
		}).To(PanicWith(ContainSubstring("invalid location item")))
	})

	It("should panic on a count that is not a power of two", func() {
		Expect(func() {
			mi200Builder().
				WithNumRank(3).
				WithLayoutLowToHigh(mi200Layout).
				Build()
		}).To(PanicWith(ContainSubstring("rank count 3")))
	})

	It("should build the default mapper without a layout", func() {
		Expect(MakeBuilder().Build()).
			To(BeAssignableToTypeOf(DefaultMapper{}))
		Expect(MakeBuilder().WithLayoutLowToHigh(nil).Build()).
			To(BeAssignableToTypeOf(DefaultMapper{}))
		Expect(mi200Builder().Build()).
			To(BeAssignableToTypeOf(DefaultMapper{}))
	})
})
