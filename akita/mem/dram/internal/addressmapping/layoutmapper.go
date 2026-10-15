package addressmapping

import "fmt"

// FieldBits places Bits bits of one location field in an explicit address
// layout.
type FieldBits struct {
	Item LocationItem
	Bits int
}

// layoutSegment copies width address bits, starting at address bit pos, into
// a location field at bit offset shift within that field.
type layoutSegment struct {
	item  LocationItem
	pos   uint64
	width uint64
	shift uint64
}

// layoutMapper maps addresses with an explicit bit layout in which a field
// can be split into several segments.
type layoutMapper struct {
	segments []layoutSegment
}

// Map returns the location (i.e., channel, rank, bank-group, bank, row, col)
// that can find the given address.
func (m layoutMapper) Map(addr uint64) Location {
	l := Location{}

	for _, s := range m.segments {
		v := ((addr >> s.pos) & (1<<s.width - 1)) << s.shift

		switch s.item {
		case LocationItemChannel:
			l.Channel |= v
		case LocationItemRank:
			l.Rank |= v
		case LocationItemBankGroup:
			l.BankGroup |= v
		case LocationItemBank:
			l.Bank |= v
		case LocationItemRow:
			l.Row |= v
		case LocationItemColumn:
			l.Column |= v
		}
	}

	return l
}

// layoutFields lists the location fields in a fixed order, so that layout
// validation reports problems deterministically.
var layoutFields = []struct {
	item LocationItem
	name string
}{
	{LocationItemChannel, "channel"},
	{LocationItemRank, "rank"},
	{LocationItemBankGroup, "bank group"},
	{LocationItemBank, "bank"},
	{LocationItemRow, "row"},
	{LocationItemColumn, "column"},
}

func (b Builder) buildLayoutMapper() Mapper {
	b.calculateBits()

	want := b.layoutFieldBits()
	placed := make(map[LocationItem]uint64)
	m := layoutMapper{}
	pos := b.accessUnitBit

	for _, f := range b.layoutLowToHigh {
		if _, ok := want[f.Item]; !ok {
			panic(fmt.Sprintf("address layout: invalid location item %d",
				f.Item))
		}

		if f.Bits <= 0 {
			panic(fmt.Sprintf("address layout: %s segment has %d bits",
				layoutFieldName(f.Item), f.Bits))
		}

		m.segments = append(m.segments, layoutSegment{
			item:  f.Item,
			pos:   pos,
			width: uint64(f.Bits),
			shift: placed[f.Item],
		})
		placed[f.Item] += uint64(f.Bits)
		pos += uint64(f.Bits)
	}

	if pos > 64 {
		panic(fmt.Sprintf("address layout: %d address bits exceed 64", pos))
	}

	for _, f := range layoutFields {
		if placed[f.item] != want[f.item] {
			panic(fmt.Sprintf("address layout: %s has %d bits, want %d",
				f.name, placed[f.item], want[f.item]))
		}
	}

	return m
}

// layoutFieldBits returns the number of address bits of each location field.
// Explicit layouts require power-of-two counts, so that every field value is
// reachable and every address maps to exactly one location.
func (b Builder) layoutFieldBits() map[LocationItem]uint64 {
	counts := []struct {
		name  string
		count int
	}{
		{"channel", b.numChannel},
		{"rank", b.numRank},
		{"bank group", b.numBankGroup},
		{"bank", b.numBank},
		{"row", b.numRow},
		{"column", b.numCol},
		{"burst length", b.burstLength},
	}
	for _, c := range counts {
		if _, ok := log2(uint64(c.count)); !ok {
			panic(fmt.Sprintf(
				"address layout: %s count %d is not a power of two",
				c.name, c.count))
		}
	}

	return map[LocationItem]uint64{
		LocationItemChannel:   b.channelBit,
		LocationItemRank:      b.rankBit,
		LocationItemBankGroup: b.bankGroupBit,
		LocationItemBank:      b.bankBit,
		LocationItemRow:       b.rowBit,
		LocationItemColumn:    b.colHiBit,
	}
}

func layoutFieldName(item LocationItem) string {
	for _, f := range layoutFields {
		if f.item == item {
			return f.name
		}
	}

	return fmt.Sprintf("location item %d", item)
}
