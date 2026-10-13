package runner

import (
	"fmt"
	"sort"
	"strings"

	"github.com/sarchlab/akita/v3/mem/mem"
	"github.com/sarchlab/akita/v3/sim"
)

// gpmPreset collects the GPM-internal parameters that differ between the
// HPCA submission configuration and the ISCA'27 1/8-MI300X configuration
// (task T13). Everything else (wafer network, RDMA, MMU, L1 MSHRs, address
// interleaving, clock) is shared and stays on its existing flags.
//
// The "hpca" preset is the default and must keep exactly the values that
// used to be hard-coded in timingplatform.go, r9nanobuilder.go, and
// shaderarray.go, so that HPCA results stay reproducible.
type gpmPreset struct {
	Name string

	// Shader arrays. One L1I and one L1S cache are shared by the CUs of a
	// shader array.
	NumSA      int
	NumCUPerSA int

	L1VBytes uint64
	L1VWays  int
	L1IBytes uint64
	L1IWays  int
	L1SBytes uint64

	// Per L2 partition (one partition per memory controller).
	L2MSHRPerBank        int
	L2ReqPerCyclePerBank int

	// NumMemoryBanks, when non-zero, is the -num-memory-banks value the preset
	// requires (number of L2 partitions = number of memory controllers).
	NumMemoryBanks int

	DRAM dramPreset
}

// dramPreset describes one memory controller (one HBM channel).
type dramPreset struct {
	Freq        sim.Freq
	BusWidth    int // bits
	DeviceWidth int // bits
	BurstLength int
	NumCol      int
	NumRow      int
	NumBank     int
	NumBankGrp  int

	TCL, TCWL, TRCDRD, TRCDWR, TRP, TRAS, TREFI int
	TRRDS, TRRDL, TWTRS, TWTRL, TWR             int
	TCCDS, TCCDL, TRTRS, TRTP, TPPD             int
}

// hpcaGPMPreset is the configuration of the HPCA submission (commit 95b4f46).
var hpcaGPMPreset = gpmPreset{
	Name:       "hpca",
	NumSA:      8,
	NumCUPerSA: 4,

	L1VBytes: 16 * mem.KB,
	L1VWays:  4,
	L1IBytes: 32 * mem.KB,
	L1IWays:  4,
	L1SBytes: 16 * mem.KB,

	L2MSHRPerBank:        64,
	L2ReqPerCyclePerBank: 16,

	NumMemoryBanks: 0, // taken from -num-memory-banks (the paper uses 4)

	DRAM: dramPreset{
		Freq:        500 * sim.MHz,
		BusWidth:    256,
		DeviceWidth: 256,
		BurstLength: 4,
		NumCol:      64,
		NumRow:      16384,
		NumBank:     4,
		NumBankGrp:  4,
		TCL:         7, TCWL: 2, TRCDRD: 7, TRCDWR: 7, TRP: 7, TRAS: 17, TREFI: 1950,
		TRRDS: 2, TRRDL: 3, TWTRS: 3, TWTRL: 4, TWR: 8,
		TCCDS: 1, TCCDL: 1, TRTRS: 0, TRTP: 3, TPPD: 2,
	},
}

// mi300xEighthGPMPreset models one GPM as 1/8 of an MI300X (one XCD plus one
// HBM3 stack), at 1 GHz with bandwidths converted to per-cycle equivalents of
// the 2.1-GHz chip (task T13, user decisions of 2026-10-13):
//   - 38 CUs as 19 shader arrays of 2 CUs (L1I shared by 2 CUs);
//   - L1V 32 KB/CU, L1I 64 KB 8-way per 2 CUs, L1S 16 KB per 2 CUs;
//   - 16 L2 channels of 256 KB with 2 requests/cycle each (2 KB/cycle) and
//     16 MSHRs each (256 per GPM, as before);
//   - 16 HBM3-like channels: 64-bit bus, BL8 (64-B access = one cache line),
//     1.23 GHz, so each channel peaks at 64 B / (4 / 1.23 GHz) = 19.7 B/ns and
//     a GPM at 315 B/cycle (5.3 TB/s / 8 / 2.1 GHz).
//
// DRAM timing parameters keep the nanosecond values of the HPCA preset
// (cycles scaled by 1.23 GHz / 500 MHz and rounded); latencies are NOT
// scaled to per-cycle equivalents, only bandwidth is.
var mi300xEighthGPMPreset = gpmPreset{
	Name:       "mi300x-eighth",
	NumSA:      19,
	NumCUPerSA: 2,

	L1VBytes: 32 * mem.KB,
	L1VWays:  4,
	L1IBytes: 64 * mem.KB,
	L1IWays:  8,
	L1SBytes: 16 * mem.KB,

	L2MSHRPerBank:        16,
	L2ReqPerCyclePerBank: 2,

	NumMemoryBanks: 16,

	DRAM: dramPreset{
		Freq:        1230 * sim.MHz,
		BusWidth:    64,
		DeviceWidth: 64,
		BurstLength: 8,
		NumCol:      64,
		NumRow:      16384,
		NumBank:     4,
		NumBankGrp:  4,
		TCL:         17, TCWL: 5, TRCDRD: 17, TRCDWR: 17, TRP: 17, TRAS: 42, TREFI: 4797,
		TRRDS: 5, TRRDL: 7, TWTRS: 7, TWTRL: 10, TWR: 20,
		TCCDS: 2, TCCDL: 2, TRTRS: 0, TRTP: 7, TPPD: 5,
	},
}

var gpmPresets = map[string]gpmPreset{
	hpcaGPMPreset.Name:         hpcaGPMPreset,
	mi300xEighthGPMPreset.Name: mi300xEighthGPMPreset,
}

func gpmPresetNames() string {
	names := make([]string, 0, len(gpmPresets))
	for name := range gpmPresets {
		names = append(names, name)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// lookupGPMPreset returns the named preset and checks it against the
// -num-memory-banks value in use.
func lookupGPMPreset(name string, numMemoryBanks int) gpmPreset {
	p, ok := gpmPresets[name]
	if !ok {
		panic(fmt.Sprintf("unknown -gpm-preset %q (choose from %s)", name, gpmPresetNames()))
	}
	if p.NumMemoryBanks != 0 && p.NumMemoryBanks != numMemoryBanks {
		panic(fmt.Sprintf("-gpm-preset=%s requires -num-memory-banks=%d, got %d",
			name, p.NumMemoryBanks, numMemoryBanks))
	}
	return p
}

// NumCU returns the number of CUs per GPM.
func (p gpmPreset) NumCU() int {
	return p.NumSA * p.NumCUPerSA
}

// CUsPerGPM returns the number of CUs per GPM of the selected -gpm-preset
// (after -config files are applied). Benchmarks use it to size launches.
func CUsPerGPM() int {
	applyConfigFiles()
	p, ok := gpmPresets[*gpmPresetFlag]
	if !ok {
		panic(fmt.Sprintf("unknown -gpm-preset %q (choose from %s)", *gpmPresetFlag, gpmPresetNames()))
	}
	return p.NumCU()
}
