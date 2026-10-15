package dram

import (
	"strings"
	"testing"
)

func TestInterleaveStripRemovesUnitSelectDigits(t *testing.T) {
	for _, c := range []struct {
		granularity uint64
		units       uint64
		external    uint64
		want        uint64
	}{
		{128, 8, 0x000, 0x000},
		{128, 8, 0x040, 0x040},
		{128, 8, 0x080, 0x000}, // unit 1, block 0
		{128, 8, 0x3C0, 0x040}, // unit 7, block 0
		{128, 8, 0x400, 0x080}, // unit 0, block 1
		{128, 8, 0x47F, 0x0FF},
		{128, 8, 0x2_0000_0000, 0x4000_0000}, // 8 GiB GPM base
		{256, 16, 0x0000, 0x000},
		{256, 16, 0x00C0, 0x0C0},
		{256, 16, 0x0100, 0x000}, // unit 1, block 0
		{256, 16, 0x0F40, 0x040}, // unit 15, block 0
		{256, 16, 0x1000, 0x100}, // unit 0, block 1
		{256, 16, 0x1234, 0x134},
		{256, 16, 0x2_0000_0000, 0x2000_0000},
	} {
		s := interleaveStrip{granularity: c.granularity, units: c.units}
		if got := s.ConvertExternalToInternal(c.external); got != c.want {
			t.Errorf("g=%d n=%d: %#x -> %#x, want %#x",
				c.granularity, c.units, c.external, got, c.want)
		}
	}
}

// Every unit's share of the address space becomes one dense range, so the
// mapper inside a controller sees every column, bank group, and bank.
func TestInterleaveStripIsDensePerUnit(t *testing.T) {
	const (
		granularity = 128
		units       = 8
		accessUnit  = 32
		blocks      = 64
	)
	s := interleaveStrip{granularity: granularity, units: units}

	for unit := uint64(0); unit < units; unit++ {
		seen := make(map[uint64]bool)
		for block := uint64(0); block < blocks; block++ {
			base := block*granularity*units + unit*granularity
			for off := uint64(0); off < granularity; off += accessUnit {
				got := s.ConvertExternalToInternal(base + off)
				if got%accessUnit != 0 {
					t.Fatalf("unit %d: %#x -> %#x is not aligned",
						unit, base+off, got)
				}
				if seen[got] {
					t.Fatalf("unit %d: %#x maps to %#x twice",
						unit, base+off, got)
				}
				seen[got] = true
			}
		}

		for addr := uint64(0); addr < blocks*granularity; addr += accessUnit {
			if !seen[addr] {
				t.Fatalf("unit %d never maps to %#x", unit, addr)
			}
		}
	}
}

func TestDefaultBuilderHasNoMappingConverter(t *testing.T) {
	if c := MakeBuilder().Build("DRAM").mappingConverter; c != nil {
		t.Fatalf("default builder installed mapping converter %#v", c)
	}
}

func TestBuilderInstallsMappingInterleaveStrip(t *testing.T) {
	c := MakeBuilder().
		WithBusWidth(64).
		WithBurstLength(4).
		WithMappingInterleaveStrip(256, 16).
		Build("DRAM")

	want := interleaveStrip{granularity: 256, units: 16}
	if c.mappingConverter != want {
		t.Fatalf("mapping converter %#v, want %#v", c.mappingConverter, want)
	}
	if c.addrConverter != nil {
		t.Fatalf("strip must not install a storage converter, got %#v",
			c.addrConverter)
	}
}

func TestMappingInterleaveStripRejectsInvalidUse(t *testing.T) {
	for _, c := range []struct {
		name  string
		build func()
		want  string
	}{
		{
			name: "zero granularity",
			build: func() {
				MakeBuilder().WithMappingInterleaveStrip(0, 8)
			},
			want: "invalid mapping interleave strip",
		},
		{
			name: "no units",
			build: func() {
				MakeBuilder().WithMappingInterleaveStrip(128, 0)
			},
			want: "invalid mapping interleave strip",
		},
		{
			name: "combined with storage conversion",
			build: func() {
				MakeBuilder().
					WithInterleavingAddrConversion(4096, 2, 1, 0, 1<<30).
					WithMappingInterleaveStrip(4096, 2).
					Build("DRAM")
			},
			want: "cannot be combined",
		},
		{
			name: "granularity below the access unit",
			build: func() {
				// Default 64-bit bus with BL8 gives 64-B access units.
				MakeBuilder().WithMappingInterleaveStrip(32, 8).Build("DRAM")
			},
			want: "not a multiple",
		},
	} {
		func() {
			defer func() {
				r := recover()
				if r == nil {
					t.Fatalf("%s: did not panic", c.name)
				}
				if msg, _ := r.(string); !strings.Contains(msg, c.want) {
					t.Fatalf("%s: panic %v, want %q", c.name, r, c.want)
				}
			}()
			c.build()
		}()
	}
}
