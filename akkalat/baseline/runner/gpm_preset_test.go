package runner

import (
	"testing"

	"github.com/sarchlab/akita/v3/mem/mem"
	"github.com/sarchlab/akita/v3/sim"
)

// The hpca preset must keep exactly the values that were hard-coded at the
// HPCA submission commit (95b4f46), so that old results stay reproducible.
func TestHPCAPresetMatchesSubmissionConstants(t *testing.T) {
	p := hpcaGPMPreset
	want := gpmPreset{
		Name: "hpca", NumSA: 8, NumCUPerSA: 4,
		L1VBytes: 16 * mem.KB, L1VWays: 4,
		L1IBytes: 32 * mem.KB, L1IWays: 4,
		L1SBytes:             16 * mem.KB,
		L2MSHRPerBank:        64,
		L2ReqPerCyclePerBank: 16,
		NumMemoryBanks:       0,
		DRAM: dramPreset{
			Freq: 500 * sim.MHz, BusWidth: 256, DeviceWidth: 256, BurstLength: 4,
			NumCol: 64, NumRow: 16384, NumBank: 4, NumBankGrp: 4,
			TCL: 7, TCWL: 2, TRCDRD: 7, TRCDWR: 7, TRP: 7, TRAS: 17, TREFI: 1950,
			TRRDS: 2, TRRDL: 3, TWTRS: 3, TWTRL: 4, TWR: 8,
			TCCDS: 1, TCCDL: 1, TRTRS: 0, TRTP: 3, TPPD: 2,
		},
	}
	if p != want {
		t.Fatalf("hpca preset changed:\n got %+v\nwant %+v", p, want)
	}
	if p.NumCU() != 32 {
		t.Fatalf("hpca preset has %d CUs, want 32", p.NumCU())
	}
}

func TestMI300XEighthPresetTargets(t *testing.T) {
	p := mi300xEighthGPMPreset
	if p.NumCU() != 38 || p.NumCUPerSA != 2 {
		t.Fatalf("want 19 x 2 = 38 CUs, got %d x %d", p.NumSA, p.NumCUPerSA)
	}
	if p.NumMemoryBanks != 16 {
		t.Fatalf("want 16 L2/HBM channels, got %d", p.NumMemoryBanks)
	}
	if got := p.L2MSHRPerBank * p.NumMemoryBanks; got != 256 {
		t.Fatalf("L2 MSHRs per GPM = %d, want 256 (unchanged)", got)
	}
	if got := p.L2ReqPerCyclePerBank * p.NumMemoryBanks * 64; got != 2048 {
		t.Fatalf("L2 throughput = %d B/cycle, want 2048", got)
	}

	d := p.DRAM
	accessBytes := d.BusWidth / 8 * d.BurstLength
	if accessBytes != 64 {
		t.Fatalf("DRAM access unit = %d B, want 64 (one cache line)", accessBytes)
	}
	// HBM burst occupies BL/2 DRAM cycles (akita dram, HBM protocol).
	perChannel := float64(accessBytes) * float64(d.Freq) / float64(d.BurstLength/2) / 1e9
	perGPM := perChannel * float64(p.NumMemoryBanks)
	if perGPM < 310 || perGPM > 320 {
		t.Fatalf("theoretical HBM peak = %.1f B/ns per GPM, want about 315", perGPM)
	}
}

func TestLookupGPMPresetChecksMemoryBanks(t *testing.T) {
	if lookupGPMPreset("hpca", 4).Name != "hpca" {
		t.Fatal("hpca preset must accept any -num-memory-banks")
	}
	if lookupGPMPreset("mi300x-eighth", 16).Name != "mi300x-eighth" {
		t.Fatal("mi300x-eighth with 16 banks must be accepted")
	}
	for _, c := range []struct {
		name  string
		banks int
	}{{"mi300x-eighth", 4}, {"no-such-preset", 4}} {
		func() {
			defer func() {
				if recover() == nil {
					t.Fatalf("lookupGPMPreset(%q, %d) did not panic", c.name, c.banks)
				}
			}()
			lookupGPMPreset(c.name, c.banks)
		}()
	}
}

// Builds the whole 48-GPM wafer with each preset and checks what was built.
func TestPresetBuildsExpectedGPM(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the full platform")
	}
	for _, c := range []struct {
		preset gpmPreset
		banks  int
		cus    int
	}{{hpcaGPMPreset, 4, 32}, {mi300xEighthGPMPreset, 16, 38}} {
		platform := MakeR9NanoBuilder().
			WithGPMPreset(c.preset).
			WithBandwidth(48).
			WithMagicMemoryCopy().
			Build(c.banks)
		gpu := platform.GPUs[0]
		if len(gpu.CUs) != c.cus {
			t.Errorf("%s: %d CUs, want %d", c.preset.Name, len(gpu.CUs), c.cus)
		}
		if len(gpu.L2Caches) != c.banks || len(gpu.MemControllers) != c.banks {
			t.Errorf("%s: %d L2 partitions and %d memory controllers, want %d",
				c.preset.Name, len(gpu.L2Caches), len(gpu.MemControllers), c.banks)
		}
	}
}
