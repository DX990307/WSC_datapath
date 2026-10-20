package shocsort

import "testing"

func TestScanLevels(t *testing.T) {
	// 4M pairs: counters 16 * 8192 = 131072 -> 128 blocks -> 1
	got := ScanLevels(16 * (4194304 / 512))
	if len(got) != 2 || got[0] != 128 || got[1] != 1 {
		t.Fatalf("levels %v", got)
	}
	if l := ScanLevels(1000); len(l) != 1 || l[0] != 1 {
		t.Fatalf("levels %v", l)
	}
	r, f, o, s, sl := KernelWGs(4194304)
	if r != 8192 || f != 8192 || o != 8192 || s != 257 || sl != 3 {
		t.Fatalf("wgs %d %d %d %d %d", r, f, o, s, sl)
	}
}

// TestPipelineModel runs a host model of the SHOC pipeline (block-local
// stable digit sort, per-block counts/offsets in [digit][block] order,
// exclusive scan, scatter) and checks it against the stable sort.
func TestPipelineModel(t *testing.T) {
	p := Params{N: 4096, Seed: 7}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	keys, vals := Generate(p)
	k := append([]uint32(nil), keys...)
	v := append([]uint32(nil), vals...)
	nb := p.N / 512
	for bit := uint(0); bit < SortBits; bit += RadixBits {
		// radixSortBlocks: stable sort of each 512-block by the digit
		tk := make([]uint32, p.N)
		tv := make([]uint32, p.N)
		for blk := 0; blk < nb; blk++ {
			pos := blk * 512
			for d := uint32(0); d < 16; d++ {
				for i := blk * 512; i < (blk+1)*512; i++ {
					if (k[i]>>bit)&0xF == d {
						tk[pos], tv[pos] = k[i], v[i]
						pos++
					}
				}
			}
		}
		counts := make([]int, 16*nb) // [digit][block]
		for blk := 0; blk < nb; blk++ {
			for i := blk * 512; i < (blk+1)*512; i++ {
				counts[int((tk[i]>>bit)&0xF)*nb+blk]++
			}
		}
		offs := make([]int, len(counts))
		s := 0
		for i, c := range counts {
			offs[i] = s
			s += c
		}
		for blk := 0; blk < nb; blk++ {
			seen := make([]int, 16)
			for i := blk * 512; i < (blk+1)*512; i++ {
				d := int((tk[i] >> bit) & 0xF)
				g := offs[d*nb+blk] + seen[d]
				seen[d]++
				k[g], v[g] = tk[i], tv[i]
			}
		}
	}
	rk, rv := Reference(keys, vals)
	for i := range rk {
		if rk[i] != k[i] || rv[i] != v[i] {
			t.Fatalf("mismatch at %d", i)
		}
	}
	for i := 1; i < len(rk); i++ {
		if rk[i-1] > rk[i] {
			t.Fatal("reference not sorted")
		}
	}
}
