package runner

import (
	"fmt"
	"sort"
	"strings"

	"github.com/sarchlab/akita/v3/mem/dram"
	"github.com/sarchlab/akita/v3/mem/mem"
	"github.com/sarchlab/akita/v3/sim"
)

// gpmPreset collects the GPM-internal parameters that differ between the
// HPCA submission configuration and the ISCA'27 1/8-MI300X and 1/4-MI250X
// configurations (task T13). Everything else (wafer network, RDMA, MMU, L1
// MSHRs, address interleaving, clock) is shared and stays on its existing
// flags.
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

	// L2CacheSizeMB, when non-zero, is the -l2-cache-size-mb value the preset
	// requires (total L2 capacity per GPM).
	L2CacheSizeMB int

	// L2CacheSizeKB, when non-zero, is the -l2-cache-size-kb value the
	// preset requires (capacities that are not whole MiB).
	L2CacheSizeKB int

	DRAM dramPreset

	// In-GPM translation (scale-model presets). 0 keeps the historical
	// values: L2 TLB 32 sets x 16 ways, 32 MSHRs, 32 requests per cycle;
	// GMMU 16 page walks in flight.
	L2TLBSets        int
	L2TLBMSHR        int
	L2TLBReqPerCycle int
	GMMUInFlight     int

	// CapScaleDen, when non-zero, marks a scale-model preset with GPM
	// shared resources scaled by 1/CapScaleDen. The per-GPM capacity-like
	// tables of the mechanisms (-pm-adapt-track-cap,
	// -pm-adapt-replica-budget-mb, -pm-l2-window) are divided by it, so the
	// command lines of the full-size GPM keep their meaning (see
	// scaledPerGPM).
	CapScaleDen int
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

	// Explicit organization: channels per controller (the pseudo channels of
	// an HBM2 channel) and ranks per channel (HBM stack IDs). 0 keeps the
	// historical derivation: one channel, and the rank count from the
	// capacity formula in createDramControllerBuilder.
	NumChannel int
	NumRank    int

	// Commands per command queue (one queue per channel and rank) and access
	// units in the transaction queue; 0 keeps 8 and 32.
	CmdQueueSize   int
	TransQueueSize int

	TCL, TCWL, TRCDRD, TRCDWR, TRP, TRAS, TREFI int
	TRRDS, TRRDL, TWTRS, TWTRL, TWR             int
	TCCDS, TCCDL, TRTRS, TRTP, TPPD             int

	// TRFC is the refresh cycle time; 0 keeps the akita default, which only
	// AllBankRefresh uses.
	TRFC int
	// TFAW is the four-activate window of each channel; 0 models no window.
	TFAW int
	// ActCmdCycles is the number of row-bus cycles an activate occupies,
	// which needs DualCommandBus; 0 keeps one cycle.
	ActCmdCycles int

	// akita DRAM-model features; all off for hpca and mi300x-eighth, which
	// keeps their results bit-identical.
	//
	// AddrLayout is the explicit bit layout of the DRAM address; the zero
	// layout keeps the default mapping (column, bank group, bank, rank,
	// channel, row from low to high).
	AddrLayout dram.AddressLayout
	// StripSliceInterleave removes the L2-slice interleave bits before DRAM
	// address mapping, so each controller uses all of its columns, bank
	// groups, and banks. Storage keeps the global address.
	StripSliceInterleave bool
	// DualCommandBus issues one row and one column command per cycle, as
	// HBM's separate row and column command interfaces allow.
	DualCommandBus bool
	// RowHold keeps the row open for all the access units of one request
	// (RD, then RDA), and PipelinedColumns lets the column commands of a
	// bank overlap, so that these access units are tCCDL apart.
	RowHold          bool
	PipelinedColumns bool
	// AllBankRefresh refreshes all the banks of each channel every TREFI,
	// blocking activates for TRFC. The channels of a GPM's controllers
	// refresh at evenly staggered times.
	AllBankRefresh bool
	// CrossRankTRRD keeps activates to different ranks (stack IDs) of a
	// channel TRRDS apart.
	CrossRankTRRD bool
	// HBM2Turnaround times write data from tWL and applies the read/write
	// turnarounds across the stack IDs of a pseudo channel.
	HBM2Turnaround bool
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

// mi200QuarterGPMPreset models one GPM as 1/4 of an AMD MI250X GCD (CDNA2),
// at 1 GHz with bandwidths converted to per-cycle equivalents of the 1.7-GHz
// chip (user decisions of 2026-10-14; consistent with public specs, not
// calibrated):
//   - 28 CUs as 14 shader arrays of 2 CUs (110 CUs per GCD / 4, at most 30
//     CUs per tile); L1V 16 KB 4-way per CU, L1I 32 KB 4-way and L1S 16 KB
//     per 2 CUs;
//   - 8 L2 slices of 256 KB (2 MB, 16-way, 64-B lines instead of CDNA2's
//     128 B), each with 2 requests/cycle (128 B/clk) and 32 MSHRs;
//   - one 8-high HBM2e stack (16 GB, 3.2 Gbps, 409.6 GB/s) behind 8 memory
//     controllers, one per L2 slice. A controller is one HBM2e channel of 2
//     pseudo channels (64-bit, BL4, 32-B access), each with 2 stack IDs
//     (ranks) of 4 bank groups x 4 banks and 1-KB rows. The DRAM clock is
//     1.6 GHz / 1.7 = 941 MHz, so a pseudo channel peaks at 32 B per 2
//     cycles = 15.06 B/ns and a GPM at 241 B/cycle (409.6 GB/s / 1.7).
//
// DRAM timing rule: access latencies (tCL, tCWL, tRCD, tRP, tRAS, tWR, tRTP,
// tPPD, tRFC, tREFI) keep their nanosecond values at 941 MHz, rounded to
// nearest (tREFI, a maximum, rounded down). Spacings that bound bandwidth
// (BL/2, tCCD, tRRD, tWTR, tFAW, the activate command cycles, tRTRS) keep
// the clock count of the real 1.6-GHz part, max(nCK floor, ceil(ns /
// 0.625 ns)), so that activate-bound traffic reaches the same fraction of
// peak as on the chip (4 / max(tRRDS, tFAW/4) = 4/7 for random 64-B lines).
// HBM2e timings are not public; the values below follow JEDEC HBM2 as
// transcribed by the cited simulators, with the HPCA preset's ns values
// (which equal DRAMsim3's HBM2) where those disagree or are silent.
//
// Sources:
//
//	[S1] AMD, "Introducing AMD CDNA 2 Architecture" whitepaper: 110 CUs and
//	     32 L2 slices (8 MB, 16-way, 128 B/clk/slice, one slice per memory
//	     controller) per GCD; 1.7-GHz engine clock; HBM2e 3.2 Gbps with a
//	     1.6-GHz memory clock.
//	[S2] AMD ROCm "Accelerator and GPU hardware specifications", MI250X:
//	     L1V 16 KiB per CU, L1I 32 KiB and L1S 16 KiB per 2 CUs (ways
//	     are not published; 4-way as in the hpca preset).
//	[S3] Samsung Flashbolt HBM2E (2020-02-04): 16 GB from eight 16-Gb dies,
//	     3.2 Gbps, 410 GB/s per stack.
//	[S4] JEDEC JESD235A sections 3.2.1-3.2.2: pseudo channels (64-bit, BL4,
//	     independent array timing) and dual row/column command interfaces.
//	[S5] Ramulator 2 python/ramulator/dram/hbm2.py: HBM2_8Gb organization
//	     (2 SIDs, 4x4 banks, 32768 rows), tREFI 3.9 us and tRFC by channel
//	     density per JESD235D Table 68, nCCDS 2, nCCDL max(4 nCK, 2.8 ns),
//	     nRRD max(4 nCK, 4 ns), nFAW max(8 nCK, 15 ns), 2-cycle ACT.
//	[S6] gem5 hbm.py HBM_2000_4H_1x64: tRRD 4 ns, tRRD_L 6 ns, tXAW 16 ns
//	     with 4 activates per pseudo channel, 1-KB rows per pseudo channel,
//	     two-cycle activate.
//	[S7] DRAMsim3 configs/HBM2_8Gb_x128.ini (tCK 1 ns): CL 14, tRCD 14,
//	     tRP 14, tRAS 34, tWR 16, tRTP 6, tWTR_S 6, tWTR_L 8, tREFI 3900.
var mi200QuarterGPMPreset = gpmPreset{
	Name:       "mi200",
	NumSA:      14,
	NumCUPerSA: 2,

	L1VBytes: 16 * mem.KB, // [S2]
	L1VWays:  4,
	L1IBytes: 32 * mem.KB, // [S2]
	L1IWays:  4,
	L1SBytes: 16 * mem.KB, // [S2]

	L2MSHRPerBank:        32, // 256 per GPM
	L2ReqPerCyclePerBank: 2,  // 128 B/clk/slice [S1]

	NumMemoryBanks: 8,
	L2CacheSizeMB:  2, // 8 MB per GCD / 4 [S1]

	DRAM: dramPreset{
		Freq:        941 * sim.MHz, // 1600 MHz [S1] / 1.7
		BusWidth:    64,            // pseudo channel [S4]
		DeviceWidth: 64,
		BurstLength: 4,     // pseudo-channel mode [S4]; 32-B access
		NumCol:      128,   // 8-B columns, 1-KB row [S5][S6]
		NumRow:      32768, // [S5]
		NumBank:     4,     // [S5]
		NumBankGrp:  4,     // [S5]
		NumChannel:  2,     // pseudo channels [S4]
		NumRank:     2,     // stack IDs of an 8-high stack [S5]

		// One queue per pseudo channel and stack ID. The 64 access units
		// hold the 32 lines of the slice's MSHRs (structural, not
		// published).
		CmdQueueSize:   16,
		TransQueueSize: 64,

		// Access latencies in ns at 941 MHz.
		TCL:    13,   // 14 ns [S7]
		TCWL:   4,    // 4 ns [S7]
		TRCDRD: 13,   // 14 ns [S7]
		TRCDWR: 13,   // 14 ns [S7]
		TRP:    13,   // 14 ns [S7]
		TRAS:   32,   // 34 ns [S7]
		TWR:    15,   // 16 ns [S7]
		TRTP:   6,    // 6 ns [S7]
		TPPD:   4,    // 4 ns (HPCA preset)
		TREFI:  3669, // 3.9 us [S5][S7], rounded down
		TRFC:   423,  // 450 ns, 16 Gb per channel [S5]

		// Spacings in nCK of the 1.6-GHz part.
		TCCDS:        2,  // 2 nCK [S5]
		TCCDL:        5,  // max(4 nCK, 2.8 ns) [S5]
		TRRDS:        7,  // max(4 nCK, 4 ns) [S5][S6]
		TRRDL:        10, // 6 ns [S6][S7]
		TWTRS:        10, // 6 ns [S7]
		TWTRL:        13, // 8 ns [S7]
		TFAW:         26, // 16 ns per pseudo channel [S6]
		ActCmdCycles: 2,  // [S5][S6]
		TRTRS:        0,  // stack-ID switch = BL/2 [S5]

		AddrLayout: dram.AddressLayout{
			// Above the 32-B offset: the two halves of a 64-B line share
			// a bank and row, consecutive lines alternate between the
			// pseudo channels, and consecutive 128-B blocks rotate through
			// the bank groups, banks, and stack IDs.
			{Field: dram.AddressFieldColumn, Bits: 1},
			{Field: dram.AddressFieldChannel, Bits: 1},
			{Field: dram.AddressFieldBankGroup, Bits: 2},
			{Field: dram.AddressFieldBank, Bits: 2},
			{Field: dram.AddressFieldRank, Bits: 1},
			{Field: dram.AddressFieldColumn, Bits: 4},
			{Field: dram.AddressFieldRow, Bits: 15},
		},
		StripSliceInterleave: true,
		DualCommandBus:       true,
		RowHold:              true,
		PipelinedColumns:     true,
		AllBankRefresh:       true,
		CrossRankTRRD:        true,
		HBM2Turnaround:       true,
	},
}

// mi200ScaledGPMPreset (mesh9, user 10-22) is the mi200 GPM scaled by about
// 0.6 = 48/80, so that 80 GPMs on a 9x9 mesh have about the resources of 48
// mi200 GPMs on 7x7: 16 CUs (8 SA x 2; x0.571, total 1280 = 95% of 1344),
// 5 L2 slices of 256 KiB (1.25 MiB, x0.625; one slice per memory controller
// as in mi200) and 5 HBM2e channels (5/8 of the stack bandwidth, x0.625).
// Everything per CU, per slice and per channel is the mi200 value. The GPM
// still maps 8 GB (5 controllers x 2 pseudo channels x 2 stack IDs hold
// 10 GB); slice and channel interleaving is modulo 5.
var mi200ScaledGPMPreset = func() gpmPreset {
	p := mi200QuarterGPMPreset
	p.Name = "mi200s"
	p.NumSA = 8
	p.NumMemoryBanks = 5
	p.L2CacheSizeMB = 0
	p.L2CacheSizeKB = 5 * 256
	return p
}()

// Scale-model presets (user 10-26; SeyyedAghaei et al., HPCA'24): the mi200
// GPM with its shared resources scaled by s = 1/4 or 1/8, while every
// per-CU resource, every latency and the wafer network (7x7 mesh, links,
// switches, RDMA windows, central IOMMU) stay as in mi200. Inputs are scaled
// with the machine (weak scaling); see
// isca27/results_index/scalemodel/README.md.
//   - CUs: 2-CU shader arrays as in mi200 (per-SA L1I / L1S unchanged), so
//     28 x s is rounded up to an even count: 8 (4 SA) for 1/4 and 4 (2 SA)
//     for 1/8, i.e. 8/7 x s;
//   - L2: 2 slices (512 KiB) / 1 slice (256 KiB) of the mi200 slice (256 KiB,
//     16-way, 32 MSHRs, 2 requests/cycle); one HBM2e channel per slice as in
//     mi200, so HBM bandwidth is exactly s (DRAM timing unchanged; the GPM
//     still maps 8 GB, controllers keep their 2-GB organization and the
//     high address bits alias, which has no timing effect with close-page
//     row handling);
//   - L2 TLB sets (16 ways kept), MSHRs and requests/cycle, and GMMU walks in
//     flight: x s;
//   - mechanism tables sized per GPM: x s through CapScaleDen.
var mi200QuarterScaleGPMPreset = func() gpmPreset {
	p := mi200QuarterGPMPreset
	p.Name = "mi200-quarter"
	p.NumSA = 4
	p.NumMemoryBanks = 2
	p.L2CacheSizeMB = 0
	p.L2CacheSizeKB = 2 * 256
	p.L2TLBSets = 8
	p.L2TLBMSHR = 8
	p.L2TLBReqPerCycle = 8
	p.GMMUInFlight = 4
	p.CapScaleDen = 4
	return p
}()

var mi200EighthScaleGPMPreset = func() gpmPreset {
	p := mi200QuarterGPMPreset
	p.Name = "mi200-eighth"
	p.NumSA = 2
	p.NumMemoryBanks = 1
	p.L2CacheSizeMB = 0
	p.L2CacheSizeKB = 256
	p.L2TLBSets = 4
	p.L2TLBMSHR = 4
	p.L2TLBReqPerCycle = 4
	p.GMMUInFlight = 2
	p.CapScaleDen = 8
	return p
}()

var gpmPresets = map[string]gpmPreset{
	mi200QuarterScaleGPMPreset.Name: mi200QuarterScaleGPMPreset,
	mi200EighthScaleGPMPreset.Name:  mi200EighthScaleGPMPreset,
	hpcaGPMPreset.Name:         hpcaGPMPreset,
	mi300xEighthGPMPreset.Name: mi300xEighthGPMPreset,
	mi200QuarterGPMPreset.Name: mi200QuarterGPMPreset,
	mi200ScaledGPMPreset.Name:  mi200ScaledGPMPreset,
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
// -num-memory-banks and -l2-cache-size-mb values in use.
func lookupGPMPreset(name string, numMemoryBanks, l2CacheSizeMB int) gpmPreset {
	return lookupGPMPresetKB(name, numMemoryBanks, l2CacheSizeMB, 0)
}

// lookupGPMPresetKB is lookupGPMPreset with the -l2-cache-size-kb value.
func lookupGPMPresetKB(name string, numMemoryBanks, l2CacheSizeMB, l2CacheSizeKB int) gpmPreset {
	p, ok := gpmPresets[name]
	if !ok {
		panic(fmt.Sprintf("unknown -gpm-preset %q (choose from %s)", name, gpmPresetNames()))
	}
	if p.NumMemoryBanks != 0 && p.NumMemoryBanks != numMemoryBanks {
		panic(fmt.Sprintf("-gpm-preset=%s requires -num-memory-banks=%d, got %d",
			name, p.NumMemoryBanks, numMemoryBanks))
	}
	if *l2SizeSensitivityFlag && l2CacheSizeKB > 0 {
		return p // L2 capacity sensitivity: -l2-cache-size-kb overrides the preset's size
	}
	if p.L2CacheSizeKB != 0 && p.L2CacheSizeKB != l2CacheSizeKB {
		panic(fmt.Sprintf("-gpm-preset=%s requires -l2-cache-size-kb=%d, got %d",
			name, p.L2CacheSizeKB, l2CacheSizeKB))
	}
	if p.L2CacheSizeKB == 0 && l2CacheSizeKB != 0 && p.L2CacheSizeMB != 0 {
		panic(fmt.Sprintf("-gpm-preset=%s requires -l2-cache-size-mb=%d and no -l2-cache-size-kb",
			name, p.L2CacheSizeMB))
	}
	if p.L2CacheSizeMB != 0 && p.L2CacheSizeMB != l2CacheSizeMB {
		panic(fmt.Sprintf("-gpm-preset=%s requires -l2-cache-size-mb=%d, got %d",
			name, p.L2CacheSizeMB, l2CacheSizeMB))
	}
	return p
}

// modelFeatures lists the explicit DRAM organization and the akita
// DRAM-model features that the preset turns on, for the [Config] log. It is
// empty for hpca and mi300x-eighth.
func (d dramPreset) modelFeatures() []string {
	var features []string
	for _, v := range []struct {
		name  string
		value int
	}{
		{"channels", d.NumChannel},
		{"ranks", d.NumRank},
		{"cmd-queue", d.CmdQueueSize},
		{"trans-queue", d.TransQueueSize},
		{"tRFC", d.TRFC},
		{"tFAW", d.TFAW},
		{"act-cycles", d.ActCmdCycles},
	} {
		if v.value != 0 {
			features = append(features, fmt.Sprintf("%s=%d", v.name, v.value))
		}
	}
	if d.AddrLayout != (dram.AddressLayout{}) {
		features = append(features, "address-layout")
	}
	for _, f := range []struct {
		name string
		on   bool
	}{
		{"strip-slice-interleave", d.StripSliceInterleave},
		{"dual-command-bus", d.DualCommandBus},
		{"row-hold", d.RowHold},
		{"pipelined-columns", d.PipelinedColumns},
		{"all-bank-refresh", d.AllBankRefresh},
		{"cross-rank-tRRD", d.CrossRankTRRD},
		{"hbm2-turnaround", d.HBM2Turnaround},
	} {
		if f.on {
			features = append(features, f.name)
		}
	}
	return features
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

// orDefault returns v, or def when v is 0.
func orDefault(v, def int) int {
	if v == 0 {
		return def
	}
	return v
}

// GPMCapacityScaleDen returns 1/s of the selected -gpm-preset: 1 for the
// full-size presets, 4 for mi200-quarter, 8 for mi200-eighth. Mechanisms
// whose tables are sized per GPM divide their capacities by it.
func GPMCapacityScaleDen() int {
	applyConfigFiles()
	p, ok := gpmPresets[*gpmPresetFlag]
	if !ok || p.CapScaleDen == 0 {
		return 1
	}
	return p.CapScaleDen
}

// scaledPerGPM divides a per-GPM capacity by GPMCapacityScaleDen, keeping 0
// (unlimited) and at least 1.
func scaledPerGPM(v int) int {
	d := GPMCapacityScaleDen()
	if v <= 0 || d == 1 {
		return v
	}
	if v/d < 1 {
		return 1
	}
	return v / d
}
