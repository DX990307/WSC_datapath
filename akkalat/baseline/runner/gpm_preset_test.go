package runner

import (
	"fmt"
	"math"
	"reflect"
	"testing"

	"github.com/sarchlab/akita/v3/mem/dram"
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

// The mi300x-eighth preset must keep the values of commit 756f214, so that
// its results stay reproducible.
func TestMI300XEighthPresetMatchesT13Constants(t *testing.T) {
	want := gpmPreset{
		Name: "mi300x-eighth", NumSA: 19, NumCUPerSA: 2,
		L1VBytes: 32 * mem.KB, L1VWays: 4,
		L1IBytes: 64 * mem.KB, L1IWays: 8,
		L1SBytes:             16 * mem.KB,
		L2MSHRPerBank:        16,
		L2ReqPerCyclePerBank: 2,
		NumMemoryBanks:       16,
		DRAM: dramPreset{
			Freq: 1230 * sim.MHz, BusWidth: 64, DeviceWidth: 64, BurstLength: 8,
			NumCol: 64, NumRow: 16384, NumBank: 4, NumBankGrp: 4,
			TCL: 17, TCWL: 5, TRCDRD: 17, TRCDWR: 17, TRP: 17, TRAS: 42, TREFI: 4797,
			TRRDS: 5, TRRDL: 7, TWTRS: 7, TWTRL: 10, TWR: 20,
			TCCDS: 2, TCCDL: 2, TRTRS: 0, TRTP: 7, TPPD: 5,
		},
	}
	if p := mi300xEighthGPMPreset; p != want {
		t.Fatalf("mi300x-eighth preset changed:\n got %+v\nwant %+v", p, want)
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

// The L2-slice interleave strip must stay off for the historical presets and,
// when a preset turns it on, strip exactly the L2-slice interleaving.
func TestStripSliceInterleaveIsOptIn(t *testing.T) {
	for _, p := range []gpmPreset{hpcaGPMPreset, mi300xEighthGPMPreset} {
		if p.DRAM.StripSliceInterleave {
			t.Errorf("%s: StripSliceInterleave must stay off", p.Name)
		}
	}

	p := mi300xEighthGPMPreset
	gpuBuilder := MakeR9NanoGPUBuilder().
		WithGPMPreset(p).
		WithNumMemoryBank(16).
		WithLog2MemoryBankInterleavingSize(7)
	plain := gpuBuilder.createDramControllerBuilder()

	p.DRAM.StripSliceInterleave = true
	stripGPUBuilder := gpuBuilder.WithGPMPreset(p)
	strip := stripGPUBuilder.createDramControllerBuilder()

	want := plain.WithMappingInterleaveStrip(128, 16)
	if !reflect.DeepEqual(strip, want) {
		t.Fatalf("StripSliceInterleave must add exactly "+
			"WithMappingInterleaveStrip(128, 16):\n got %+v\nwant %+v",
			strip, want)
	}
	if reflect.DeepEqual(plain, want) {
		t.Fatal("the plain builder already strips the slice interleave")
	}
}

// All-bank refresh must stay off for the historical presets and, when a
// preset turns it on, add exactly the refresh calls: tRFC and refresh for
// every controller and one stagger slot per controller.
func TestAllBankRefreshIsOptIn(t *testing.T) {
	for _, p := range []gpmPreset{hpcaGPMPreset, mi300xEighthGPMPreset} {
		if p.DRAM.AllBankRefresh || p.DRAM.TRFC != 0 {
			t.Errorf("%s: AllBankRefresh and TRFC must stay unset", p.Name)
		}
	}

	p := mi300xEighthGPMPreset
	gpuBuilder := MakeR9NanoGPUBuilder().
		WithGPMPreset(p).
		WithNumMemoryBank(16)
	plain := gpuBuilder.createDramControllerBuilder()
	got := gpuBuilder.dramControllerBuilderAt(plain, 3)
	if !reflect.DeepEqual(got, plain) {
		t.Fatal("controllers without refresh must share one builder")
	}

	p.DRAM.TRFC = 423
	p.DRAM.AllBankRefresh = true
	refreshGPUBuilder := gpuBuilder.WithGPMPreset(p)
	refresh := refreshGPUBuilder.createDramControllerBuilder()

	want := plain.WithRFC(423).WithAllBankRefresh(true)
	if !reflect.DeepEqual(refresh, want) {
		t.Fatalf("AllBankRefresh must add exactly "+
			"WithRFC(423).WithAllBankRefresh(true):\n got %+v\nwant %+v",
			refresh, want)
	}

	got = refreshGPUBuilder.dramControllerBuilderAt(refresh, 3)
	if !reflect.DeepEqual(got, want.WithRefreshStagger(3, 16)) {
		t.Fatal("controller 3 must take refresh stagger slot 3 of 16")
	}
}

func TestLookupGPMPresetChecksMemoryBanks(t *testing.T) {
	if lookupGPMPreset("hpca", 4, 4).Name != "hpca" {
		t.Fatal("hpca preset must accept any -num-memory-banks")
	}
	if lookupGPMPreset("hpca", 16, 7).Name != "hpca" {
		t.Fatal("hpca preset must accept any -l2-cache-size-mb")
	}
	if lookupGPMPreset("mi300x-eighth", 16, 4).Name != "mi300x-eighth" {
		t.Fatal("mi300x-eighth with 16 banks must be accepted")
	}
	if lookupGPMPreset("mi200", 8, 2).Name != "mi200" {
		t.Fatal("mi200 with 8 banks and a 2-MB L2 must be accepted")
	}
	for _, c := range []struct {
		name  string
		banks int
		l2MB  int
	}{
		{"mi300x-eighth", 4, 4},
		{"no-such-preset", 4, 4},
		{"mi200", 16, 2},
		{"mi200", 8, 4},
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Fatalf("lookupGPMPreset(%q, %d, %d) did not panic",
						c.name, c.banks, c.l2MB)
				}
			}()
			lookupGPMPreset(c.name, c.banks, c.l2MB)
		}()
	}
}

// Builds the whole 48-GPM wafer with each preset and checks what was built.
func TestPresetBuildsExpectedGPM(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the full platform")
	}
	for _, c := range []struct {
		preset      gpmPreset
		banks       int
		l2MB        int
		cus         int
		accessBytes uint64
		dramModel   bool
		built       builtGPM
	}{
		{hpcaGPMPreset, 4, 4, 32, 128, false, hpcaBuiltGPM},
		{mi300xEighthGPMPreset, 16, 4, 38, 64, false, mi300xEighthBuiltGPM},
		{mi200QuarterGPMPreset, 8, 2, 28, 32, true, mi200BuiltGPM},
	} {
		platform := MakeR9NanoBuilder().
			WithGPMPreset(c.preset).
			WithL2CacheSizeMB(c.l2MB).
			WithBandwidth(48).
			WithMagicMemoryCopy().
			Build(c.banks)
		gpu := platform.GPUs[0]
		if len(gpu.CUs) != c.cus {
			t.Errorf("%s: %d CUs, want %d", c.preset.Name, len(gpu.CUs), c.cus)
		}
		checkBuiltGPM(t, c.preset.Name, gpu, c.built)
		if len(gpu.L2Caches) != c.banks || len(gpu.MemControllers) != c.banks {
			t.Errorf("%s: %d L2 partitions and %d memory controllers, want %d",
				c.preset.Name, len(gpu.L2Caches), len(gpu.MemControllers), c.banks)
		}
		for i, component := range gpu.MemControllers {
			ctrl, ok := component.(*dram.MemController)
			if !ok {
				t.Fatalf("%s: memory controller %d is a %T",
					c.preset.Name, i, component)
			}
			if got := ctrl.PhysicalAccessBytes(); got != c.accessBytes {
				t.Errorf("%s: controller %d has %d-B access units, want %d",
					c.preset.Name, i, got, c.accessBytes)
			}
			if got := ctrl.GetDRAMModelStats().Enabled; got != c.dramModel {
				t.Errorf("%s: controller %d reports DRAM-model stats %v, "+
					"want %v", c.preset.Name, i, got, c.dramModel)
			}
		}

		// The DRAM report adds the DRAM-model rows only for mi200, so
		// that the metric files of the other presets stay unchanged.
		r := &Runner{
			platform:         platform,
			Timing:           true,
			metricsCollector: &collector{},
		}
		r.reportDRAMRowContinuationStats()
		rows := legacyDRAMReportRows
		if c.dramModel {
			rows = append(append([]string{}, rows...),
				dramModelReportRows...)
		}
		if got, want := len(r.metricsCollector.metrics),
			len(platform.GPUs)*c.banks*len(rows); got != want {
			t.Fatalf("%s: %d DRAM report rows, want %d",
				c.preset.Name, got, want)
		}
		for i, m := range r.metricsCollector.metrics {
			if want := rows[i%len(rows)]; m.what != want {
				t.Fatalf("%s: DRAM report row %d is %s, want %s",
					c.preset.Name, i, m.what, want)
			}
		}
	}
}

// builtGPM is what one GPM of a preset contains once the platform is built.
type builtGPM struct {
	numSA         int
	l1v, l1i, l1s cacheShape // l1v per CU; l1i and l1s per shader array
	l2            cacheShape // per L2 slice
	l2MSHR        int
	l2ReqPerCycle int
	dram          builtDRAM
}

// cacheShape is the capacity, associativity, and line size of one cache.
type cacheShape struct {
	bytes uint64
	ways  int
	line  int
}

// builtDRAM is the organization and timing of each memory controller.
type builtDRAM struct {
	freq                               sim.Freq
	channels, ranks, bankGroups, banks int
	cmdQueueSize, transQueueSize       int

	// akita DRAM-model features (mi200 only). stripGranularity and
	// stripUnits describe the L2-slice interleave removed before mapping
	// (0: none).
	stripGranularity, stripUnits uint64
	layout, rowHold, pipelined   bool
	actCmdCycles                 int // 0: single command bus
	refreshInterval              int // 0: no refresh
	refreshDuration              int
	faw                          int

	timings    []dramTiming
	bankCycles map[string]int // cycles a bank is busy per command
}

// dramTiming is the minimum number of cycles from command `from` to command
// `to` in one scope of the DRAM timing table (SameBank,
// OtherBanksInBankGroup, SameRank, or OtherRanks); -1 means no constraint.
type dramTiming struct {
	scope, from, to string
	cycles          int
}

var hpcaBuiltGPM = builtGPM{
	numSA:         8,
	l1v:           cacheShape{16 * mem.KB, 4, 64},
	l1i:           cacheShape{32 * mem.KB, 4, 64},
	l1s:           cacheShape{16 * mem.KB, 4, 64},
	l2:            cacheShape{1 * mem.MB, 16, 64},
	l2MSHR:        64,
	l2ReqPerCycle: 16,
	dram: builtDRAM{
		freq:     500 * sim.MHz,
		channels: 1, ranks: 16, bankGroups: 4, banks: 4,
		cmdQueueSize: 8, transQueueSize: 32,
		// Values of commit 756f214 (write data timed from tRL).
		timings: []dramTiming{
			{"SameBank", "Activate", "Activate", 24},
			{"SameBank", "Activate", "Read", 7},
			{"SameBank", "Activate", "Write", 7},
			{"SameBank", "Activate", "Precharge", 17},
			{"SameBank", "Precharge", "Activate", 7},
			{"SameBank", "Read", "Read", 2},
			{"SameBank", "Read", "Write", 7},
			{"SameBank", "Read", "Precharge", 3},
			{"SameBank", "Write", "Read", 13},
			{"SameBank", "Write", "Precharge", 12},
			{"SameBank", "ReadPrecharge", "Activate", 12},
			{"SameBank", "WritePrecharge", "Activate", 19},
			{"OtherBanksInBankGroup", "Activate", "Activate", 3},
			{"SameRank", "Activate", "Activate", 2},
			{"SameRank", "Read", "Read", 2},
			{"SameRank", "Write", "Read", 12},
			{"OtherRanks", "Activate", "Activate", -1},
			{"OtherRanks", "Read", "Write", 2},
			{"OtherRanks", "Write", "Read", 2},
		},
		bankCycles: map[string]int{"Read": 9, "Write": 9,
			"ReadPrecharge": 7, "WritePrecharge": 7, "Activate": 7},
	},
}

var mi300xEighthBuiltGPM = builtGPM{
	numSA:         19,
	l1v:           cacheShape{32 * mem.KB, 4, 64},
	l1i:           cacheShape{64 * mem.KB, 8, 64},
	l1s:           cacheShape{16 * mem.KB, 4, 64},
	l2:            cacheShape{256 * mem.KB, 16, 64},
	l2MSHR:        16,
	l2ReqPerCycle: 2,
	dram: builtDRAM{
		freq:     1230 * sim.MHz,
		channels: 1, ranks: 16, bankGroups: 4, banks: 4,
		cmdQueueSize: 8, transQueueSize: 32,
		// Values of commit 756f214 (write data timed from tRL).
		timings: []dramTiming{
			{"SameBank", "Activate", "Activate", 59},
			{"SameBank", "Activate", "Read", 17},
			{"SameBank", "Activate", "Write", 17},
			{"SameBank", "Activate", "Precharge", 42},
			{"SameBank", "Precharge", "Activate", 17},
			{"SameBank", "Read", "Read", 4},
			{"SameBank", "Read", "Write", 16},
			{"SameBank", "Read", "Precharge", 7},
			{"SameBank", "Write", "Read", 31},
			{"SameBank", "Write", "Precharge", 29},
			{"SameBank", "ReadPrecharge", "Activate", 28},
			{"SameBank", "WritePrecharge", "Activate", 46},
			{"OtherBanksInBankGroup", "Activate", "Activate", 7},
			{"SameRank", "Activate", "Activate", 5},
			{"SameRank", "Read", "Read", 4},
			{"SameRank", "Write", "Read", 28},
			{"OtherRanks", "Activate", "Activate", -1},
			{"OtherRanks", "Read", "Write", 4},
			{"OtherRanks", "Write", "Read", 4},
		},
		bankCycles: map[string]int{"Read": 21, "Write": 21,
			"ReadPrecharge": 17, "WritePrecharge": 17, "Activate": 17},
	},
}

// mi200BuiltGPM is 1/4 of an MI250X GCD: 28 CUs, 8 L2 slices of 256 KB, and
// 8 HBM2e channels of 2 pseudo channels (see mi200QuarterGPMPreset).
var mi200BuiltGPM = builtGPM{
	numSA:         14,
	l1v:           cacheShape{16 * mem.KB, 4, 64},
	l1i:           cacheShape{32 * mem.KB, 4, 64},
	l1s:           cacheShape{16 * mem.KB, 4, 64},
	l2:            cacheShape{256 * mem.KB, 16, 64},
	l2MSHR:        32,
	l2ReqPerCycle: 2,
	dram: builtDRAM{
		freq:     941 * sim.MHz,
		channels: 2, ranks: 2, bankGroups: 4, banks: 4,
		cmdQueueSize: 16, transQueueSize: 64,

		stripGranularity: 128, stripUnits: 8,
		layout:          true,
		rowHold:         true,
		pipelined:       true,
		actCmdCycles:    2,
		refreshInterval: 3669, // tREFI 3.9 us
		refreshDuration: 423,  // tRFC 450 ns
		faw:             26,   // tFAW 16 ns at 1.6 GHz

		// tCL 13, tCWL 4, BL/2 2, tRCD 13, tRP 13, tRAS 32, tWR 15,
		// tRTP 6, tCCDS/L 2/5, tRRDS/L 7/10, tWTRS/L 10/13, tRTRS 0.
		timings: []dramTiming{
			{"SameBank", "Activate", "Activate", 45},  // tRC
			{"SameBank", "Activate", "Read", 13},      // tRCDRD
			{"SameBank", "Activate", "Write", 13},     // tRCDWR
			{"SameBank", "Activate", "Precharge", 32}, // tRAS
			{"SameBank", "Precharge", "Activate", 13}, // tRP
			{"SameBank", "Read", "Read", 5},           // tCCDL
			{"SameBank", "Read", "Write", 11},         // tCL + BL/2 - tCWL
			{"SameBank", "Read", "Precharge", 6},      // tRTP
			{"SameBank", "Write", "Read", 19},         // tCWL + BL/2 + tWTRL
			{"SameBank", "Write", "Precharge", 21},    // tCWL + BL/2 + tWR
			{"SameBank", "ReadPrecharge", "Activate", 21},
			{"SameBank", "WritePrecharge", "Activate", 34},
			{"OtherBanksInBankGroup", "Activate", "Activate", 10}, // tRRDL
			{"SameRank", "Activate", "Activate", 7},               // tRRDS
			{"SameRank", "Read", "Read", 2},                       // tCCDS
			{"SameRank", "Write", "Read", 16},                     // tCWL + BL/2 + tWTRS
			{"SameRank", "Refresh", "Activate", 423},              // tRFC
			// The stack IDs of a pseudo channel.
			{"OtherRanks", "Activate", "Activate", 7}, // tRRDS
			{"OtherRanks", "Read", "Write", 11},
			{"OtherRanks", "Write", "Read", 16},
		},
		bankCycles: map[string]int{"Read": 15, "Write": 6,
			"ReadPrecharge": 15, "WritePrecharge": 6, "Activate": 13},
	},
}

// field reads a configuration that a built akita component keeps in
// unexported fields: it follows the named struct fields through pointers and
// interfaces. The result is read-only.
func field(v interface{}, path ...string) reflect.Value {
	r, ok := v.(reflect.Value)
	if !ok {
		r = reflect.ValueOf(v)
	}
	for _, name := range path {
		r = deref(r).FieldByName(name)
		if !r.IsValid() {
			panic("no field " + name)
		}
	}
	return r
}

// inspect is field followed through pointers and interfaces.
func inspect(v interface{}, path ...string) reflect.Value {
	return deref(field(v, path...))
}

func deref(r reflect.Value) reflect.Value {
	for (r.Kind() == reflect.Ptr || r.Kind() == reflect.Interface) &&
		!r.IsNil() {
		r = r.Elem()
	}
	return r
}

func builtCacheShape(cache interface{}) cacheShape {
	dir := inspect(cache, "directory")
	sets := int(dir.FieldByName("NumSets").Int())
	ways := int(dir.FieldByName("NumWays").Int())
	line := int(dir.FieldByName("BlockSize").Int())
	return cacheShape{uint64(sets * ways * line), ways, line}
}

func checkBuiltGPM(t *testing.T, name string, gpu *GPU, want builtGPM) {
	t.Helper()

	for _, c := range []struct {
		what   string
		caches []TraceableComponent
		count  int
		shape  cacheShape
	}{
		{"L1V", gpu.L1VCaches, len(gpu.CUs), want.l1v},
		{"L1I", gpu.L1ICaches, want.numSA, want.l1i},
		{"L1S", gpu.L1SCaches, want.numSA, want.l1s},
		{"L2", gpu.L2Caches, len(gpu.MemControllers), want.l2},
	} {
		if len(c.caches) != c.count {
			t.Errorf("%s: %d %s caches, want %d",
				name, len(c.caches), c.what, c.count)
		}
		for i, cache := range c.caches {
			if got := builtCacheShape(cache); got != c.shape {
				t.Errorf("%s: %s cache %d is %+v, want %+v",
					name, c.what, i, got, c.shape)
			}
		}
	}
	for i, l2 := range gpu.L2Caches {
		mshr := int(inspect(l2, "mshrCapacity").Int())
		reqs := int(inspect(l2, "numReqPerCycle").Int())
		if mshr != want.l2MSHR || reqs != want.l2ReqPerCycle {
			t.Errorf("%s: L2 slice %d has %d MSHRs and %d requests/cycle, "+
				"want %d and %d", name, i, mshr, reqs,
				want.l2MSHR, want.l2ReqPerCycle)
		}
	}

	var refreshPhases []uint64
	for i, component := range gpu.MemControllers {
		ctrl, ok := component.(*dram.MemController)
		if !ok {
			t.Fatalf("%s: memory controller %d is a %T", name, i, component)
		}
		where := fmt.Sprintf("%s: controller %d", name, i)
		refreshPhases = append(refreshPhases,
			checkBuiltDRAM(t, where, ctrl, want.dram)...)
	}

	// Refresh is staggered evenly over one interval across the channels
	// of all the controllers of a GPM.
	for k, phase := range refreshPhases {
		n := len(refreshPhases)
		if w := uint64(k * want.dram.refreshInterval / n); phase != w {
			t.Errorf("%s: channel %d of %d refreshes at phase %d, want %d",
				name, k, n, phase, w)
		}
	}
}

// checkBuiltDRAM checks one memory controller and returns the refresh phases
// of its channels.
func checkBuiltDRAM(
	t *testing.T,
	where string,
	ctrl *dram.MemController,
	want builtDRAM,
) (refreshPhases []uint64) {
	t.Helper()

	if ctrl.Freq != want.freq {
		t.Errorf("%s: DRAM clock %.0f MHz, want %.0f MHz",
			where, float64(ctrl.Freq)/1e6, float64(want.freq)/1e6)
	}

	channel := inspect(ctrl, "channel")
	channels := []reflect.Value{channel}
	if channel.Type().Name() == "MultiChannel" {
		channels = nil
		pcs := channel.FieldByName("Channels")
		for c := 0; c < pcs.Len(); c++ {
			channels = append(channels, deref(pcs.Index(c)))
		}
	}
	if len(channels) != want.channels {
		t.Fatalf("%s: %d channels (%s), want %d",
			where, len(channels), channel.Type(), want.channels)
	}

	for c, pc := range channels {
		at := fmt.Sprintf("%s channel %d", where, c)
		refreshPhases = append(refreshPhases,
			checkBuiltDRAMChannel(t, at, pc, want)...)
	}

	cmdQueue := inspect(ctrl, "cmdQueue")
	transQueue := inspect(ctrl, "subTransactionQueue")
	numQueues := cmdQueue.FieldByName("Queues").Len()
	if numQueues != want.channels*want.ranks ||
		int(cmdQueue.FieldByName("CapacityPerQueue").Int()) != want.cmdQueueSize ||
		int(transQueue.FieldByName("Capacity").Int()) != want.transQueueSize {
		t.Errorf("%s: %d command queues of %d and a transaction queue of "+
			"%d, want %d of %d and %d", where, numQueues,
			cmdQueue.FieldByName("CapacityPerQueue").Int(),
			transQueue.FieldByName("Capacity").Int(),
			want.channels*want.ranks, want.cmdQueueSize, want.transQueueSize)
	}
	if cmdQueue.FieldByName("RowHold").Bool() != want.rowHold ||
		transQueue.FieldByName("BatchByTransaction").Bool() != want.rowHold {
		t.Errorf("%s: row hold %v, want %v", where,
			cmdQueue.FieldByName("RowHold").Bool(), want.rowHold)
	}

	actCycles := 0
	if field(ctrl, "dualCommandBus").Bool() {
		actCycles = int(field(ctrl, "activateCommandCycles").Int())
	}
	if actCycles != want.actCmdCycles {
		t.Errorf("%s: dual command bus with %d-cycle activates, want %d",
			where, actCycles, want.actCmdCycles)
	}

	var granularity, units uint64
	if !field(ctrl, "mappingConverter").IsNil() {
		granularity = inspect(ctrl, "mappingConverter", "granularity").Uint()
		units = inspect(ctrl, "mappingConverter", "units").Uint()
	}
	if granularity != want.stripGranularity || units != want.stripUnits {
		t.Errorf("%s: strips a %d-B interleave over %d controllers, "+
			"want %d B over %d", where, granularity, units,
			want.stripGranularity, want.stripUnits)
	}

	layout := inspect(ctrl, "addrMapper").Type().Name() != "DefaultMapper"
	if layout != want.layout {
		t.Errorf("%s: address mapper %s, want explicit layout %v",
			where, inspect(ctrl, "addrMapper").Type(), want.layout)
	}

	return refreshPhases
}

func checkBuiltDRAMChannel(
	t *testing.T,
	where string,
	pc reflect.Value,
	want builtDRAM,
) (refreshPhases []uint64) {
	t.Helper()

	banks := pc.FieldByName("Banks")
	if banks.Len() != want.ranks ||
		banks.Index(0).Len() != want.bankGroups ||
		banks.Index(0).Index(0).Len() != want.banks {
		t.Errorf("%s: %d ranks x %d bank groups x %d banks, "+
			"want %d x %d x %d", where, banks.Len(), banks.Index(0).Len(),
			banks.Index(0).Index(0).Len(),
			want.ranks, want.bankGroups, want.banks)
	}

	timing := pc.FieldByName("Timing")
	kinds := commandKinds(timing.FieldByName("SameBank"))
	kind := func(name string) int {
		i, ok := kinds[name]
		if !ok {
			t.Fatalf("no DRAM command kind %s in %v", name, kinds)
		}
		return i
	}
	// Report only the first bank that differs.
banks:
	for r := 0; r < banks.Len(); r++ {
		for g := 0; g < banks.Index(r).Len(); g++ {
			for b := 0; b < banks.Index(r).Index(g).Len(); b++ {
				bank := deref(banks.Index(r).Index(g).Index(b))
				pipelined := bank.FieldByName("PipelineColumnCommands").Bool()
				if pipelined != want.pipelined {
					t.Errorf("%s: bank [%d][%d][%d] pipelined %v, want %v",
						where, r, g, b, pipelined, want.pipelined)
					break banks
				}
				cycles := bank.FieldByName("CmdCycles")
				for cmd, n := range want.bankCycles {
					if got := int(cycles.Index(kind(cmd)).Int()); got != n {
						t.Errorf("%s: a %s keeps bank [%d][%d][%d] busy "+
							"%d cycles, want %d", where, cmd, r, g, b, got, n)
						break banks
					}
				}
			}
		}
	}

	for _, w := range want.timings {
		got := -1
		row := timing.FieldByName(w.scope).Index(kind(w.from))
		for e := 0; e < row.Len(); e++ {
			entry := row.Index(e)
			if int(entry.FieldByName("NextCmdKind").Int()) == kind(w.to) {
				got = int(entry.FieldByName("MinCycleInBetween").Int())
			}
		}
		if got != w.cycles {
			t.Errorf("%s: %s %s -> %s takes %d cycles, want %d",
				where, w.scope, w.from, w.to, got, w.cycles)
		}
	}

	if faw := int(pc.FieldByName("FAW").Int()); faw != want.faw {
		t.Errorf("%s: tFAW %d, want %d", where, faw, want.faw)
	}

	refresh := pc.FieldByName("Refresh")
	if want.refreshInterval == 0 {
		if !refresh.IsNil() {
			t.Errorf("%s: refreshes, want no refresh", where)
		}
		return nil
	}
	if refresh.IsNil() {
		t.Fatalf("%s: no refresh", where)
	}
	refresh = deref(refresh)
	interval := int(refresh.FieldByName("Interval").Int())
	duration := int(refresh.FieldByName("Duration").Int())
	if interval != want.refreshInterval || duration != want.refreshDuration {
		t.Errorf("%s: refresh every %d cycles for %d, want %d and %d",
			where, interval, duration,
			want.refreshInterval, want.refreshDuration)
	}

	return []uint64{refresh.FieldByName("Phase").Uint()}
}

// commandKinds maps the names of akita's DRAM command kinds, an internal
// type, to their indices in a timing table and in a bank's command cycles.
func commandKinds(timeTable reflect.Value) map[string]int {
	next, _ := timeTable.Type().Elem().Elem().FieldByName("NextCmdKind")
	kinds := map[string]int{}
	for i := 0; i < timeTable.Len(); i++ {
		kind := reflect.New(next.Type).Elem()
		kind.SetInt(int64(i))
		kinds[fmt.Sprint(kind.Interface())] = i
	}
	return kinds
}

// legacyDRAMReportRows lists, in order, the rows that
// reportDRAMRowContinuationStats reports for each memory controller at
// commit 756f214.
var legacyDRAMReportRows = []string{
	"dram_physical_access_bytes",
	"dram_physical_read_accesses",
	"dram_physical_write_accesses",
	"dram_frontend_read_requests",
	"dram_frontend_write_requests",
	"dram_frontend_read_bytes",
	"dram_frontend_write_bytes",
	"dram_paired_read_descriptors",
	"dram_paired_read_members",
	"dram_paired_read_demand_members",
	"dram_paired_read_sibling_members",
	"dram_row_continuation_enabled",
	"dram_row_commands_issued",
	"dram_row_column_commands",
	"dram_row_reuse_hits",
	"dram_row_auto_precharge_stops",
	"dram_aggregate_continuation_enabled",
	"dram_aggregate_auto_precharge_stops",
	"dram_aggregate_immediate_continuations",
	"dram_row_activate_commands",
	"dram_row_precharge_commands",
	"dram_row_max_queue_age_cycles",
}

// dramModelReportRows lists, in order, the DRAM-model rows that follow the
// legacy rows of a controller with DRAM-model features.
var dramModelReportRows = []string{
	"dram_row_hold_auto_precharge_stops",
	"dram_dual_issue_ticks",
	"dram_row_bus_busy_ticks",
	"dram_refresh_commands",
	"dram_refresh_retroactive",
	"dram_refresh_wait_cycles",
	"dram_refresh_max_wait_cycles",
	"dram_refresh_forced_precharges",
	"dram_faw_denied_activates",
}

func TestReportDRAMModelStats(t *testing.T) {
	r := &Runner{metricsCollector: &collector{}}
	r.reportDRAMModelStats("GPU[1].DRAM[0]", dram.DRAMModelStats{
		RowHoldAutoPrechargeStops: 1,
		RefreshCommands:           2,
	})
	if len(r.metricsCollector.metrics) != 0 {
		t.Fatalf("reported %d rows without DRAM-model features",
			len(r.metricsCollector.metrics))
	}

	r.reportDRAMModelStats("GPU[1].DRAM[0]", dram.DRAMModelStats{
		Enabled:                   true,
		RowHoldAutoPrechargeStops: 1,
		DualIssueTicks:            2,
		RowBusBusyTicks:           3,
		RefreshCommands:           4,
		RefreshRetroactive:        5,
		RefreshWaitCycles:         6,
		RefreshMaxWaitCycles:      7,
		RefreshForcedPrecharges:   8,
		FAWDeniedActivates:        9,
	})
	if len(r.metricsCollector.metrics) != len(dramModelReportRows) {
		t.Fatalf("reported %d rows, want %d",
			len(r.metricsCollector.metrics), len(dramModelReportRows))
	}
	for i, m := range r.metricsCollector.metrics {
		want := metric{
			where: "GPU[1].DRAM[0]",
			what:  dramModelReportRows[i],
			value: float64(i + 1),
		}
		if m != want {
			t.Errorf("row %d is %+v, want %+v", i, m, want)
		}
	}
}

// historicalDramControllerBuilder is createDramControllerBuilder as of commit
// 756f214, before the explicit organization and the DRAM-model features.
func historicalDramControllerBuilder(b R9NanoGPUBuilder) dram.Builder {
	memBankSize := 8 * mem.GB / uint64(b.numMemoryBank)
	d := b.gpmPreset.DRAM
	dramBankSize := d.NumCol * d.NumRow * d.DeviceWidth
	dramRankSize := dramBankSize * (d.BusWidth / d.DeviceWidth) * d.NumBank
	dramRank := int(memBankSize * 8 / uint64(dramRankSize))

	return dram.MakeBuilder().
		WithEngine(b.engine).
		WithFreq(d.Freq).
		WithProtocol(dram.HBM).
		WithBurstLength(d.BurstLength).
		WithDeviceWidth(d.DeviceWidth).
		WithBusWidth(d.BusWidth).
		WithNumChannel(1).
		WithNumRank(dramRank).
		WithNumBankGroup(d.NumBankGrp).
		WithNumBank(d.NumBank).
		WithNumCol(d.NumCol).
		WithNumRow(d.NumRow).
		WithCommandQueueSize(8).
		WithTransactionQueueSize(32).
		WithAggregateContinuation(b.l2GranularityAdaptation).
		WithRowContinuation(b.dramRowContinuation).
		WithTCL(d.TCL).
		WithTCWL(d.TCWL).
		WithTRCDRD(d.TRCDRD).
		WithTRCDWR(d.TRCDWR).
		WithTRP(d.TRP).
		WithTRAS(d.TRAS).
		WithTREFI(d.TREFI).
		WithTRRDS(d.TRRDS).
		WithTRRDL(d.TRRDL).
		WithTWTRS(d.TWTRS).
		WithTWTRL(d.TWTRL).
		WithTWR(d.TWR).
		WithTCCDS(d.TCCDS).
		WithTCCDL(d.TCCDL).
		WithTRTRS(d.TRTRS).
		WithTRTP(d.TRTP).
		WithTPPD(d.TPPD)
}

// hpca and mi300x-eighth must leave every new preset field unset and build
// exactly the memory controllers of commit 756f214, so that their results
// stay bit-identical.
func TestHistoricalPresetsKeepDRAMModelOff(t *testing.T) {
	for _, c := range []struct {
		preset gpmPreset
		banks  int
	}{{hpcaGPMPreset, 4}, {mi300xEighthGPMPreset, 16}} {
		p, d := c.preset, c.preset.DRAM
		if p.L2CacheSizeMB != 0 {
			t.Errorf("%s: L2CacheSizeMB = %d, want 0", p.Name, p.L2CacheSizeMB)
		}
		for _, f := range []struct {
			name  string
			value int
		}{
			{"NumChannel", d.NumChannel},
			{"NumRank", d.NumRank},
			{"CmdQueueSize", d.CmdQueueSize},
			{"TransQueueSize", d.TransQueueSize},
			{"TRFC", d.TRFC},
			{"TFAW", d.TFAW},
			{"ActCmdCycles", d.ActCmdCycles},
		} {
			if f.value != 0 {
				t.Errorf("%s: %s = %d, want 0", p.Name, f.name, f.value)
			}
		}
		for _, f := range []struct {
			name string
			on   bool
		}{
			{"StripSliceInterleave", d.StripSliceInterleave},
			{"DualCommandBus", d.DualCommandBus},
			{"RowHold", d.RowHold},
			{"PipelinedColumns", d.PipelinedColumns},
			{"AllBankRefresh", d.AllBankRefresh},
			{"CrossRankTRRD", d.CrossRankTRRD},
			{"HBM2Turnaround", d.HBM2Turnaround},
		} {
			if f.on {
				t.Errorf("%s: %s must stay off", p.Name, f.name)
			}
		}
		if d.AddrLayout != (dram.AddressLayout{}) {
			t.Errorf("%s: AddrLayout = %v, want the default layout",
				p.Name, d.AddrLayout)
		}
		if features := d.modelFeatures(); len(features) != 0 {
			t.Errorf("%s: DRAM model features %v, want none", p.Name, features)
		}

		for _, mode := range []struct{ aggregate, rowContinuation bool }{
			{false, false}, {true, false}, {false, true},
		} {
			gpuBuilder := MakeR9NanoGPUBuilder().
				WithEngine(sim.NewSerialEngine()).
				WithGPMPreset(p).
				WithNumMemoryBank(c.banks).
				WithLog2MemoryBankInterleavingSize(7).
				WithL2GranularityAdaptation(mode.aggregate, false, false, false).
				WithDRAMRowContinuation(mode.rowContinuation)
			want := historicalDramControllerBuilder(gpuBuilder)
			got := gpuBuilder.createDramControllerBuilder()
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("%s %+v: controller builder changed:\n got %+v\nwant %+v",
					p.Name, mode, got, want)
			}
			if !reflect.DeepEqual(gpuBuilder.dramControllerBuilderAt(got, 1), want) {
				t.Fatalf("%s: controller 1 builder differs from the shared one",
					p.Name)
			}
		}
	}
}

// mi200 timing rules (see mi200QuarterGPMPreset).
const (
	mi200ModelMHz = 941.0 // 1600 MHz / 1.7
	hbm2eTCKNS    = 0.625 // tCK of the 1.6-GHz part
)

type mi200TimingRule int

const (
	// ruleNS keeps the ns value at the 941-MHz model clock, rounded to
	// nearest; ruleNSFloor rounds down (tREFI is a maximum).
	ruleNS mi200TimingRule = iota
	ruleNSFloor
	// ruleNCK keeps the clock count of the real part: max(floor,
	// ceil(ns / tCK)).
	ruleNCK
)

// hbm2eNCK is the clock count a controller for the 1.6-GHz part programs.
func hbm2eNCK(ns float64, floor int) int {
	return max(floor, int(math.Ceil(ns/hbm2eTCKNS-1e-9)))
}

func TestMI200PresetTargets(t *testing.T) {
	p := mi200QuarterGPMPreset
	if p.NumSA != 14 || p.NumCUPerSA != 2 || p.NumCU() != 28 {
		t.Fatalf("want 14 x 2 = 28 CUs, got %d x %d", p.NumSA, p.NumCUPerSA)
	}
	if p.L1VBytes != 16*mem.KB || p.L1VWays != 4 ||
		p.L1IBytes != 32*mem.KB || p.L1IWays != 4 || p.L1SBytes != 16*mem.KB {
		t.Fatalf("want L1V 16 KB 4-way, L1I 32 KB 4-way, L1S 16 KB; got %+v", p)
	}
	if p.NumMemoryBanks != 8 {
		t.Fatalf("want 8 L2 slices/HBM2e channels, got %d", p.NumMemoryBanks)
	}
	if got := uint64(p.L2CacheSizeMB) * mem.MB / uint64(p.NumMemoryBanks); got != 256*mem.KB {
		t.Fatalf("L2 slice = %d B, want 256 KB", got)
	}
	if got := p.L2ReqPerCyclePerBank * p.NumMemoryBanks * 64; got != 1024 {
		t.Fatalf("L2 throughput = %d B/cycle, want 1024", got)
	}
	if got := p.L2MSHRPerBank * p.NumMemoryBanks; got != 256 {
		t.Fatalf("L2 MSHRs per GPM = %d, want 256", got)
	}

	d := p.DRAM
	if math.Abs(float64(d.Freq)/1e6-1600/1.7) > 0.5 {
		t.Fatalf("DRAM clock = %.0f MHz, want 1600 MHz / 1.7", float64(d.Freq)/1e6)
	}
	accessBytes := d.BusWidth / 8 * d.BurstLength
	if accessBytes != 32 || d.BusWidth != 64 || d.BurstLength != 4 {
		t.Fatalf("DRAM access unit = %d B, want 32 (64-bit pseudo channel, BL4)",
			accessBytes)
	}
	if d.NumChannel != 2 || d.NumRank != 2 || d.NumBankGrp != 4 || d.NumBank != 4 {
		t.Fatalf("want 2 pseudo channels x 2 stack IDs x 4 x 4 banks, got %d x %d x %d x %d",
			d.NumChannel, d.NumRank, d.NumBankGrp, d.NumBank)
	}
	// HBM burst occupies BL/2 DRAM cycles (akita dram, HBM protocol).
	perPC := float64(accessBytes) * float64(d.Freq) / float64(d.BurstLength/2) / 1e9
	perGPM := perPC * float64(d.NumChannel*p.NumMemoryBanks)
	if perGPM < 240 || perGPM > 242 {
		t.Fatalf("theoretical HBM peak = %.1f B/ns per GPM, want about 241 (409.6 / 1.7)",
			perGPM)
	}
	rowBytes := d.NumCol * d.BusWidth / 8
	controllerBytes := uint64(d.NumChannel*d.NumRank*d.NumBankGrp*d.NumBank*d.NumRow) *
		uint64(rowBytes)
	if rowBytes != 1024 || controllerBytes*uint64(p.NumMemoryBanks) != 16*mem.GB {
		t.Fatalf("%d-B rows and %d B per GPM, want 1-KB rows and one 16-GB stack",
			rowBytes, controllerBytes*uint64(p.NumMemoryBanks))
	}

	// The layout covers each field exactly once and the controller capacity.
	log2 := func(n int) int { return int(math.Log2(float64(n))) }
	wantBits := map[dram.AddressField]int{
		dram.AddressFieldColumn:    log2(d.NumCol) - log2(d.BurstLength),
		dram.AddressFieldChannel:   log2(d.NumChannel),
		dram.AddressFieldRank:      log2(d.NumRank),
		dram.AddressFieldBankGroup: log2(d.NumBankGrp),
		dram.AddressFieldBank:      log2(d.NumBank),
		dram.AddressFieldRow:       log2(d.NumRow),
	}
	gotBits := map[dram.AddressField]int{}
	totalBits := log2(accessBytes)
	for _, f := range d.AddrLayout {
		if f.Bits > 0 {
			gotBits[f.Field] += f.Bits
			totalBits += f.Bits
		}
	}
	if !reflect.DeepEqual(gotBits, wantBits) || uint64(1)<<totalBits != controllerBytes {
		t.Fatalf("layout bits %v (%d in all), want %v covering %d B",
			gotBits, totalBits, wantBits, controllerBytes)
	}
	if f := d.AddrLayout[1]; f.Field != dram.AddressFieldChannel ||
		log2(accessBytes)+d.AddrLayout[0].Bits != 6 {
		t.Fatal("the pseudo channel must be the bit right above the 64-B line")
	}

	// The command queues and the transaction queue hold the slice's MSHR
	// lines.
	if d.TransQueueSize != p.L2MSHRPerBank*64/accessBytes ||
		d.CmdQueueSize*d.NumChannel*d.NumRank != d.TransQueueSize {
		t.Fatalf("queues %d/%d, want %d access units over %d queues",
			d.CmdQueueSize, d.TransQueueSize,
			p.L2MSHRPerBank*64/accessBytes, d.NumChannel*d.NumRank)
	}

	for _, f := range []struct {
		name string
		on   bool
	}{
		{"StripSliceInterleave", d.StripSliceInterleave},
		{"DualCommandBus", d.DualCommandBus},
		{"RowHold", d.RowHold},
		{"PipelinedColumns", d.PipelinedColumns},
		{"AllBankRefresh", d.AllBankRefresh},
		{"CrossRankTRRD", d.CrossRankTRRD},
		// Pipelined column commands on HBM need it (akita dram Build).
		{"HBM2Turnaround", d.HBM2Turnaround},
	} {
		if !f.on {
			t.Errorf("%s must be on", f.name)
		}
	}

	// One row per DRAM timing: the rule, the source ns value, and the nCK
	// floor of the real part.
	tCK := 1e3 / mi200ModelMHz
	for _, r := range []struct {
		name  string
		got   int
		rule  mi200TimingRule
		ns    float64
		floor int
	}{
		{"tCL", d.TCL, ruleNS, 14, 0},
		{"tCWL", d.TCWL, ruleNS, 4, 0},
		{"tRCDRD", d.TRCDRD, ruleNS, 14, 0},
		{"tRCDWR", d.TRCDWR, ruleNS, 14, 0},
		{"tRP", d.TRP, ruleNS, 14, 0},
		{"tRAS", d.TRAS, ruleNS, 34, 0},
		{"tWR", d.TWR, ruleNS, 16, 0},
		{"tRTP", d.TRTP, ruleNS, 6, 0},
		{"tPPD", d.TPPD, ruleNS, 4, 0},
		{"tRFC", d.TRFC, ruleNS, 450, 0},
		{"tREFI", d.TREFI, ruleNSFloor, 3900, 0},
		{"BL/2", d.BurstLength / 2, ruleNCK, 0, 2},
		{"tCCDS", d.TCCDS, ruleNCK, 0, 2},
		{"tCCDL", d.TCCDL, ruleNCK, 2.8, 4},
		{"tRRDS", d.TRRDS, ruleNCK, 4, 4},
		{"tRRDL", d.TRRDL, ruleNCK, 6, 4},
		{"tWTRS", d.TWTRS, ruleNCK, 6, 0},
		{"tWTRL", d.TWTRL, ruleNCK, 8, 0},
		{"tFAW", d.TFAW, ruleNCK, 16, 8},
		{"ACT cycles", d.ActCmdCycles, ruleNCK, 0, 2},
		{"tRTRS", d.TRTRS, ruleNCK, 0, 0},
	} {
		gotNS := float64(r.got) * tCK
		switch r.rule {
		case ruleNS:
			if math.Abs(gotNS-r.ns) > 0.5*tCK {
				t.Errorf("%s = %d cycles = %.2f ns, want %.1f ns at %.0f MHz",
					r.name, r.got, gotNS, r.ns, mi200ModelMHz)
			}
		case ruleNSFloor:
			if gotNS > r.ns || float64(r.got+1)*tCK <= r.ns {
				t.Errorf("%s = %d cycles = %.1f ns, want the most cycles within %.0f ns",
					r.name, r.got, gotNS, r.ns)
			}
		case ruleNCK:
			if want := hbm2eNCK(r.ns, r.floor); r.got != want {
				t.Errorf("%s = %d cycles, want %d nCK of the 1.6-GHz part "+
					"(max(%d, ceil(%.1f ns / %.3f ns)))",
					r.name, r.got, want, r.floor, r.ns, hbm2eTCKNS)
			}
		}
	}

	// Random 64-B lines with close page need one activate per line in a
	// pseudo channel, so the activate rate caps the data bus at
	// (data cycles per line) / max(tRRDS, tFAW/4) of its peak, which must be
	// the fraction of the real part (4 / max(7, 26/4) = 4/7).
	actFraction := func(dataCycles, trrds, tfaw int) float64 {
		return float64(dataCycles) / math.Max(float64(trrds), float64(tfaw)/4)
	}
	lineCycles := 64 / accessBytes * d.BurstLength / 2
	model := actFraction(lineCycles, d.TRRDS, d.TFAW)
	chip := actFraction(4, hbm2eNCK(4, 4), hbm2eNCK(16, 8))
	if math.Abs(model-chip) > 1e-9 || math.Abs(model-4.0/7) > 1e-9 {
		t.Fatalf("activate-bound fraction of peak = %.3f, want %.3f (4/7)",
			model, chip)
	}
}

// The mi200 controllers get exactly the preset's organization, queue sizes
// and DRAM-model features, and one refresh stagger slot each.
func TestMI200DRAMControllerBuilder(t *testing.T) {
	p := mi200QuarterGPMPreset
	d := p.DRAM
	engine := sim.NewSerialEngine()
	gpuBuilder := MakeR9NanoGPUBuilder().
		WithEngine(engine).
		WithGPMPreset(p).
		WithNumMemoryBank(8).
		WithLog2MemoryBankInterleavingSize(7)

	want := dram.MakeBuilder().
		WithEngine(engine).
		WithFreq(d.Freq).
		WithProtocol(dram.HBM).
		WithBurstLength(4).
		WithDeviceWidth(64).
		WithBusWidth(64).
		WithNumChannel(2).
		WithNumRank(2).
		WithNumBankGroup(4).
		WithNumBank(4).
		WithNumCol(128).
		WithNumRow(32768).
		WithCommandQueueSize(16).
		WithTransactionQueueSize(64).
		WithAggregateContinuation(false).
		WithRowContinuation(false).
		WithTCL(d.TCL).
		WithTCWL(d.TCWL).
		WithTRCDRD(d.TRCDRD).
		WithTRCDWR(d.TRCDWR).
		WithTRP(d.TRP).
		WithTRAS(d.TRAS).
		WithTREFI(d.TREFI).
		WithTRRDS(d.TRRDS).
		WithTRRDL(d.TRRDL).
		WithTWTRS(d.TWTRS).
		WithTWTRL(d.TWTRL).
		WithTWR(d.TWR).
		WithTCCDS(d.TCCDS).
		WithTCCDL(d.TCCDL).
		WithTRTRS(d.TRTRS).
		WithTRTP(d.TRTP).
		WithTPPD(d.TPPD).
		WithMappingInterleaveStrip(128, 8).
		WithAddressLayout(d.AddrLayout).
		WithDualCommandBus(true).
		WithActivateCommandCycles(2).
		WithHBM2Turnaround(true).
		WithTransactionRowHold(true).
		WithPipelinedColumnCommands(true).
		WithRFC(d.TRFC).
		WithAllBankRefresh(true).
		WithTFAW(d.TFAW).
		WithCrossRankTRRD(true)

	got := gpuBuilder.createDramControllerBuilder()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("mi200 controller builder:\n got %+v\nwant %+v", got, want)
	}
	for i := 0; i < 8; i++ {
		ctrl := gpuBuilder.dramControllerBuilderAt(got, i)
		if !reflect.DeepEqual(ctrl, want.WithRefreshStagger(i, 8)) {
			t.Fatalf("controller %d must take refresh stagger slot %d of 8", i, i)
		}
	}

	// The controllers build, also with the row and aggregate continuations,
	// which hand rows over between the requests of a held bank.
	for _, mode := range []struct{ aggregate, rowContinuation bool }{
		{false, false}, {true, false}, {false, true},
	} {
		b := gpuBuilder.
			WithL2GranularityAdaptation(mode.aggregate, false, false, false).
			WithDRAMRowContinuation(mode.rowContinuation)
		m := b.dramControllerBuilderAt(b.createDramControllerBuilder(), 7).
			Build("GPU.DRAM[7]")
		if got := m.PhysicalAccessBytes(); got != 32 {
			t.Fatalf("%+v: %d-B access units, want 32", mode, got)
		}
	}
}

func TestScaleModelPresets(t *testing.T) {
	for _, c := range []struct {
		name          string
		banks, kb, cu int
		den           int
	}{{"mi200-quarter", 2, 512, 8, 4}, {"mi200-eighth", 1, 256, 4, 8}} {
		p := lookupGPMPresetKB(c.name, c.banks, 2, c.kb)
		if p.NumCU() != c.cu || p.CapScaleDen != c.den || p.NumCUPerSA != 2 {
			t.Errorf("%s: CUs %d den %d", c.name, p.NumCU(), p.CapScaleDen)
		}
		if p.L2MSHRPerBank != mi200QuarterGPMPreset.L2MSHRPerBank ||
			p.L1VBytes != mi200QuarterGPMPreset.L1VBytes {
			t.Errorf("%s: per-CU / per-slice resources changed", c.name)
		}
	}
	if mi200QuarterGPMPreset.CapScaleDen != 0 || mi200QuarterGPMPreset.L2TLBSets != 0 {
		t.Error("mi200 must keep the historical translation sizes")
	}
}

func TestNetScaledOffIsIdentity(t *testing.T) {
	if *scaleNetworkFlag {
		t.Skip("flag set")
	}
	for _, v := range []int{0, 1, 3, 24, 128} {
		if netScaled(v) != v {
			t.Errorf("netScaled(%d) = %d without -scale-network", v, netScaled(v))
		}
	}
}
