package runner

// ISCA'27 page-migration baseline: runtime page migration between GPMs
// (on-fault or access-counter triggered, 4-KB or 2-MB units). Off by
// default; with -page-migration=none nothing is built and nothing is hooked,
// so the simulator is unchanged. See mgpusim/timing/pagemigration.

import (
	"flag"
	"fmt"
	"math/bits"
	"os"
	"strconv"
	"strings"

	"github.com/sarchlab/akita/v3/mem/cache/writearound"
	"github.com/sarchlab/akita/v3/mem/cache/writeback"
	"github.com/sarchlab/akita/v3/mem/mem"
	"github.com/sarchlab/akita/v3/mem/vm"
	"github.com/sarchlab/akita/v3/mem/vm/addresstranslator"
	"github.com/sarchlab/akita/v3/sim"
	"github.com/sarchlab/mgpusim/v3/timing/pagemigration"
	"github.com/sarchlab/mgpusim/v3/timing/rdma"
)

var pageMigrationFlag = flag.String("page-migration", "none",
	"Page migration between GPMs: none (off), observe (hooks and statistics "+
		"only, never migrates; timing must equal none), fault (migrate a page "+
		"to a GPM when that GPM's access translates to it remotely; the access "+
		"waits), counter (NVIDIA-style access counters: migrate a unit to the "+
		"GPM with the most remote accesses once one GPM reaches "+
		"-pm-threshold; asynchronous).")
var pmUnitKBFlag = flag.Int("pm-unit-kb", 4,
	"Page migration: migration and counting unit in KB (4 or 2048). Units "+
		"larger than the page move as a group of 4-KB pages; the page size and "+
		"the TLBs are unchanged.")
var pmSoftwareLatencyFlag = flag.Int("pm-sw-latency-ns", 5000,
	"Page migration: service time of one fault / notification batch (driver "+
		"work), per destination GPM.")
var pmBatchFlag = flag.Int("pm-batch", 256,
	"Page migration: most units serviced per batch (UVM fault batch).")
var pmShootdownFlag = flag.Int("pm-shootdown-ns", 1000,
	"Page migration: TLB shootdown latency (accesses to the unit are held "+
		"from its start).")
var pmThresholdFlag = flag.Int("pm-threshold", 256,
	"Page migration (counter): remote accesses of one GPM to one unit that "+
		"trigger a migration (NVIDIA uvm_perf_access_counter_threshold default).")
var pmCooldownFlag = flag.Int("pm-cooldown-ns", 5000,
	"Page migration: a unit does not migrate again within this time.")
var pmStallWatchFlag = flag.Int("pm-stall-watch-ns", 0,
	"Page migration diagnostic: print the units migrating for longer than this "+
		"(0 = off); see -pm-stall-reports.")
var pmStallReportsFlag = flag.Int("pm-stall-reports", 3,
	"Page migration diagnostic: panic after this many -pm-stall-watch-ns reports (0 = never).")
var pmTracePagesFlag = flag.String("pm-trace-pages", "",
	"Page migration diagnostic: comma-separated physical page addresses whose copy chunks are traced.")
var pmMaxMigrationsFlag = flag.Int("pm-max-migrations", 3,
	"Page migration: a unit that migrated this many times is pinned "+
		"(0 = unlimited; NVIDIA uvm_perf_thrashing_threshold default 3).")
var pmChunkBytesFlag = flag.Int("pm-chunk-bytes", 4096,
	"Page migration: bytes per pull of the copy engine over the network.")
var pmPullWindowFlag = flag.Int("pm-pull-window", 16,
	"Page migration: pulls in flight per destination copy engine.")
var pmL2WindowFlag = flag.Int("pm-l2-window", 128,
	"Page migration: line accesses in flight from a copy engine to its L2.")
var pmIdealFlag = flag.Bool("pm-ideal", false,
	"Page migration upper bound: zero software latency and zero shootdown "+
		"latency (TLB invalidation, drain, held accesses and the data copy over "+
		"the network are still modeled).")

var pmCheckFlag = flag.Bool("pm-check", false,
	"Page migration: expensive invariant checks (every L2 of every GPM is "+
		"searched for lines of each new frame at allocation and at remap; "+
		"a hit panics). The cheap checks (no access to a frozen or retired "+
		"frame, new frames never accessed) are always on.")

var pmPageListFlag = flag.String("pm-page-list", "",
	"P19 Q6: only migrate 4 KiB pages listed in this file (decimal physical "+
		"page numbers of the original placement, one per line; e.g. the pages "+
		"pgcls classifies as private-misplaced). Empty: every page.")

// P20 target selection (mgpusim/timing/pagemigration/target.go).
var pmTargetFlag = flag.String("pm-target", "argmax",
	"Access-counter migration target: argmax (largest count, original), "+
		"median (weighted Manhattan median tile) or class (dominant reader / "+
		"median / broadcast spread or stay).")
var pmDomShareFlag = flag.Float64("pm-dom-share", 0.6,
	"-pm-target class: a GPM with at least this share of the count gets the page.")
var pmMedianMaxReadersFlag = flag.Int("pm-median-max-readers", 31,
	"-pm-target class: pages with more readers than this use the broadcast rule.")
var pmBroadcastKFlag = flag.Int("pm-broadcast-k", 4,
	"-pm-target class: broadcast pages go page by page to the k tiles closest "+
		"to the median (0 = broadcast pages do not migrate).")
var pmMinSavingFlag = flag.Float64("pm-min-saving", 0.1,
	"-pm-target median/class: do not migrate if the weighted hops drop by "+
		"less than this fraction.")
var pmReaderNoiseFlag = flag.Float64("pm-reader-noise", 0.01,
	"-pm-target class: a GPM counts as a reader with at least this share.")
var pmCountLocalFlag = flag.Bool("pm-count-local", false,
	"Access counter: also count the owner's own L1V misses (never triggers).")
var pmCountNetworkFlag = flag.Bool("pm-count-network", false,
	"Access counter: do not count remote accesses served by an M3 replica.")

var pmFirstTouchMigrateFlag = flag.Bool("pm-first-touch-migrate", false,
	"P21 real-cost first-touch baseline (with -page-migration=fault): a page "+
		"faults only on its first vector access, from a GPM other than its home, "+
		"and migrates to it (software batch, shootdown, drain, copy; the access "+
		"waits); later accesses never fault.")

func pageMigrationConfigFromFlags() pagemigration.Config {
	policy, err := pagemigration.ParsePolicy(*pageMigrationFlag)
	if err != nil {
		panic(err)
	}
	c := pagemigration.DefaultConfig()
	c.Policy = policy
	kb := *pmUnitKBFlag
	if kb <= 0 || kb&(kb-1) != 0 {
		panic(fmt.Sprintf("-pm-unit-kb must be a power of two, got %d", kb))
	}
	c.Log2Unit = uint64(bits.TrailingZeros(uint(kb))) + 10
	c.SoftwareLatencyNS = uint64(*pmSoftwareLatencyFlag)
	c.BatchSize = *pmBatchFlag
	c.ShootdownLatencyNS = uint64(*pmShootdownFlag)
	c.Threshold = uint32(*pmThresholdFlag)
	c.CooldownNS = uint64(*pmCooldownFlag)
	c.MaxMigrations = *pmMaxMigrationsFlag
	c.StallWatchNS = uint64(*pmStallWatchFlag)
	c.StallReports = *pmStallReportsFlag
	if *pmTracePagesFlag != "" {
		c.TracePages = map[uint64]bool{}
		for _, f := range strings.Split(*pmTracePagesFlag, ",") {
			v, err := strconv.ParseUint(strings.TrimSpace(f), 0, 64)
			if err != nil {
				panic(err)
			}
			c.TracePages[v>>12<<12] = true
		}
	}
	c.ChunkBytes = uint64(*pmChunkBytesFlag)
	c.PullWindow = *pmPullWindowFlag
	c.L2Window = *pmL2WindowFlag
	c.Check = *pmCheckFlag
	t, ok := pagemigration.ParseTarget(*pmTargetFlag)
	if !ok {
		panic(fmt.Sprintf("unknown -pm-target %q", *pmTargetFlag))
	}
	c.Target = t
	c.DomShare = *pmDomShareFlag
	c.MedianMaxReaders = *pmMedianMaxReadersFlag
	c.BroadcastK = *pmBroadcastKFlag
	c.MinSaving = *pmMinSavingFlag
	c.ReaderNoise = *pmReaderNoiseFlag
	c.CountLocal = *pmCountLocalFlag
	c.CountNetwork = *pmCountNetworkFlag
	if *pmPageListFlag != "" {
		data, err := os.ReadFile(*pmPageListFlag)
		if err != nil {
			panic(err)
		}
		c.AllowPages = map[uint64]bool{}
		for _, l := range strings.Fields(string(data)) {
			n, err := strconv.ParseUint(l, 10, 64)
			if err != nil {
				panic(fmt.Sprintf("-pm-page-list: bad page %q", l))
			}
			c.AllowPages[n] = true
		}
		fmt.Printf("[Config] page-migration page list: %d pages\n", len(c.AllowPages))
	}
	if *pmIdealFlag {
		c.SoftwareLatencyNS = 0
		c.ShootdownLatencyNS = 0
	}
	c.FirstTouchMigrate = *pmFirstTouchMigrateFlag
	c.Adapt = adaptConfigFromFlags()
	return c
}

func printPageMigrationConfig(c pagemigration.Config) {
	if !c.Enabled() {
		return
	}
	fmt.Printf("[Config] page-migration policy=%s unit=%dKB sw=%dns batch=%d "+
		"shootdown=%dns threshold=%d cooldown=%dns max-migrations=%d "+
		"chunk=%dB pull-window=%d l2-window=%d ideal=%v check=%v\n",
		c.Policy, (uint64(1)<<c.Log2Unit)>>10, c.SoftwareLatencyNS, c.BatchSize,
		c.ShootdownLatencyNS, c.Threshold, c.CooldownNS, c.MaxMigrations,
		c.ChunkBytes, c.PullWindow, c.L2Window, *pmIdealFlag, c.Check)
	if c.FirstTouchMigrate {
		fmt.Printf("[Config] page-migration first-touch-migrate=true\n")
	}
	printAdaptConfig(c)
	if c.Target != pagemigration.TargetArgmax || c.CountLocal || c.CountNetwork {
		fmt.Printf("[Config] page-migration target=%s dom-share=%g "+
			"median-max-readers=%d broadcast-k=%d min-saving=%g reader-noise=%g "+
			"count-local=%v count-network=%v\n", c.Target, c.DomShare,
			c.MedianMaxReaders, c.BroadcastK, c.MinSaving, c.ReaderNoise,
			c.CountLocal, c.CountNetwork)
	}
}

// WithPageMigration configures page migration.
func (b R9NanoPlatformBuilder) WithPageMigration(
	c pagemigration.Config,
) R9NanoPlatformBuilder {
	b.pageMigration = c
	return b
}

// WithPageMigration configures page migration.
func (b R9NanoGPUBuilder) WithPageMigration(
	c pagemigration.Config,
) R9NanoGPUBuilder {
	b.pageMigration = c
	return b
}

type gpmPageMigrationParts struct {
	memBase uint64
	vATs    []*addresstranslator.AddressTranslator
	otherAT []*addresstranslator.AddressTranslator
	tlbs    []pagemigration.PageInvalidator
	rdma    *rdma.Comp
	engine  *pagemigration.CopyEngine
	// l2Of returns the L2 slice that holds (or would hold) a physical line,
	// local or remote (M3 replica), with the GPM's slice interleaving.
	l2Of func(pAddr uint64) *writeback.Cache
	l2s  []*writeback.Cache
	// lease: the GPM's page re-use table (nil = off or class re-use)
	lease *writeback.LeaseTable
}

// buildPageMigrationEngine creates the GPM's copy engine. Only a migrating
// policy gets one: the observe policy must leave the hardware unchanged (an
// extra network port or connection port shifts the round-robin arbitration
// of the mesh endpoint and of the direct connections, which changes timing).
func (b *R9NanoGPUBuilder) buildPageMigrationEngine() {
	b.pmEngine = nil
	if !b.pageMigration.Migrates() {
		return
	}
	b.pmEngine = pagemigration.NewCopyEngine(
		fmt.Sprintf("%s.PageMigrationDMA", b.gpuName), b.engine, b.pageMigration)
	if b.monitor != nil {
		b.monitor.RegisterComponent(b.pmEngine)
	}
}

// recordPageMigrationParts keeps what the manager needs from this GPM (its
// translators, TLBs, RDMA engine, copy engine and L2 slices).
func (b *R9NanoGPUBuilder) recordPageMigrationParts() {
	if !b.pageMigration.Enabled() {
		return
	}
	p := &gpmPageMigrationParts{
		memBase: b.memAddrOffset,
		rdma:    b.rdmaEngine,
		engine:  b.pmEngine,
	}
	p.vATs = append(p.vATs, b.l1vAddrTrans...)
	p.otherAT = append(p.otherAT, b.l1sAddrTrans...)
	p.otherAT = append(p.otherAT, b.l1iAddrTrans...)
	for _, t := range b.l1vTLBs {
		p.tlbs = append(p.tlbs, t)
	}
	for _, t := range b.l1sTLBs {
		p.tlbs = append(p.tlbs, t)
	}
	for _, t := range b.l1iTLBs {
		p.tlbs = append(p.tlbs, t)
	}
	p.tlbs = append(p.tlbs, b.l2TLB)
	l2s := append([]*writeback.Cache(nil), b.l2Caches...)
	p.l2s = l2s
	interleave := uint64(1) << b.log2MemoryBankInterleavingSize
	p.l2Of = func(a uint64) *writeback.Cache {
		return l2s[a/interleave%uint64(len(l2s))]
	}
	b.gpu.pm = p
}

func (b *R9NanoPlatformBuilder) buildPageMigrationManager(
	pageTable vm.PageTable,
) *pagemigration.Manager {
	if !b.pageMigration.Enabled() {
		return nil
	}
	if b.useParallelEngine {
		panic("page migration needs the serial engine")
	}
	gpms := make([]*pagemigration.GPM, len(b.gpus)+1)
	for _, gpu := range b.gpus {
		p := gpu.pm
		if p.memBase != gpu.GPUID*8*mem.GB {
			panic("page migration: unexpected GPM physical layout")
		}
		l2Of := p.l2Of
		gpms[gpu.GPUID] = &pagemigration.GPM{
			DeviceID: gpu.GPUID,
			MemBase:  p.memBase,
			Tile:     pgclsTile(p.rdma.Name()),
			TLBs:     p.tlbs,
			Engine:   p.engine,
			HoldsLine: func(a uint64) bool {
				l2 := l2Of(a)
				return l2.AuthoritativeResidentLookup(0, a) ||
					l2.AuthoritativeMSHRLookup(0, a)
			},
			InstallRemote: func(now sim.VTimeInSec, pid vm.PID, a uint64, data []byte) bool {
				return l2Of(a).InstallRemoteLine(now, pid, a, data)
			},
			PullInstall: func(now sim.VTimeInSec, pid vm.PID, a uint64, data []byte,
				req sim.VTimeInSec) bool {
				return l2Of(a).PullInstallLine(now, pid, a, data, req)
			},
		}
		l2s := p.l2s
		gpms[gpu.GPUID].Dump = func() string {
			out := ""
			for _, l2 := range l2s {
				out += l2.DebugDump()
			}
			return out
		}
		gpms[gpu.GPUID].PullUse = func() (inst, used uint64) {
			for _, l2 := range l2s {
				i, u := l2.PullUse()
				inst += i
				used += u
			}
			return
		}
		gpms[gpu.GPUID].VictimScore = func() float64 {
			sum, n := 0.0, uint64(0)
			for _, l2 := range l2s {
				s, k := l2.VictimScore()
				sum += s
				n += k
			}
			if n == 0 {
				return 0
			}
			return sum / float64(n)
		}
	}
	m := pagemigration.NewManager("PageMigrationManager", b.engine,
		b.pageMigration, pageTable, b.log2PageSize, gpms,
		[]pagemigration.PageInvalidator{b.IOMMUTLB})
	if b.monitor != nil {
		b.monitor.RegisterComponent(m)
	}
	for _, gpu := range b.gpus {
		p := gpu.pm
		for _, at := range p.vATs {
			at.SetMigrationHook(m.ATHook(gpu.GPUID, true))
		}
		for _, at := range p.otherAT {
			at.SetMigrationHook(m.ATHook(gpu.GPUID, false))
		}
		p.rdma.RemoteAccessHook = m.RDMAHook(gpu.GPUID)
		if b.pageMigration.CountNetwork {
			dev := gpu.GPUID
			p.rdma.RemoteReplicaHitHook = func(now sim.VTimeInSec, addr uint64) {
				m.ReplicaHit(dev, addr)
			}
		}
	}
	b.wireAdaptive(m)
	writearound.LocalBottomHook = nil
	if b.pageMigration.CountLocal {
		devOf := map[int]uint64{}
		for _, gpu := range b.gpus {
			devOf[pgclsTile(gpu.pm.rdma.Name())] = gpu.GPUID
		}
		writearound.LocalBottomHook = func(name string, addr uint64, now sim.VTimeInSec) {
			if dev, ok := devOf[pgclsTile(name)]; ok {
				m.LocalAccess(dev, addr)
			}
		}
	}
	return m
}

func (r *Runner) reportPageMigrationStats() {
	m := r.platform.PageMigration
	if m == nil {
		return
	}
	c := m.Config()
	s := m.Stats
	col := func(what string, v float64) {
		r.metricsCollector.Collect("PageMigration", what, v)
	}
	col("pm_policy_code", float64(c.Policy))
	col("pm_unit_bytes", float64(uint64(1)<<c.Log2Unit))
	col("pm_sw_latency_ns", float64(c.SoftwareLatencyNS))
	col("pm_shootdown_ns", float64(c.ShootdownLatencyNS))
	col("pm_threshold", float64(c.Threshold))
	col("pm_cooldown_ns", float64(c.CooldownNS))
	col("pm_max_migrations", float64(c.MaxMigrations))
	col("pm_faults", float64(s.Faults))
	if fmax, ftot, fret := m.FrameUsage(); ftot > 0 {
		col("pm_frames_max_gpm_bytes", float64(fmax))
		col("pm_frames_total_bytes", float64(ftot))
		col("pm_frames_retired_bytes", float64(fret))
	}
	if c.FirstTouchMigrate {
		col("pm_first_touches", float64(s.FirstTouches))
	}
	col("pm_counter_triggers", float64(s.CounterTriggers))
	col("pm_class_filtered_accesses", float64(s.ClassFilteredAccesses))
	col("pm_batches", float64(s.Batches))
	col("pm_migrations", float64(s.Migrations))
	col("pm_noop_migrations", float64(s.NoopMigrations))
	col("pm_pages_migrated", float64(s.PagesMigrated))
	col("pm_bytes_migrated", float64(s.BytesMigrated))
	col("pm_pingpong", float64(s.PingPong))
	col("pm_pinned_units", float64(s.Pinned))
	col("pm_shootdowns", float64(s.ShootdownEvents))
	col("pm_tlb_entries_invalidated", float64(s.TLBEntriesDropped))
	col("pm_remote_accesses", float64(s.RemoteAccesses))
	col("pm_remote_reads", float64(s.RemoteReads))
	col("pm_units_touched", float64(m.UnitsTouched()))
	if s.Migrations > 0 {
		n := float64(s.Migrations)
		col("pm_avg_migration_time", s.MigrationTimeSum/n)
		col("pm_avg_blocked_time", s.BlockedTimeSum/n)
		col("pm_avg_drain_wait", s.DrainWaitSum/n)
		col("pm_avg_copy_time", s.CopyTimeSum/n)
	}
	share, owner := m.HotOwnerShare()
	col("pm_hot_owner_read_share", share)
	col("pm_hot_owner_device", float64(owner))
	for _, k := range pagemigration.SortedKeys(m.MigrationsPerUnitHistogram()) {
		col(fmt.Sprintf("pm_units_migrated_%dx", k),
			float64(m.MigrationsPerUnitHistogram()[k]))
	}
	var held, pulled, served uint64
	for _, gpu := range r.platform.GPUs {
		if gpu.pm == nil {
			continue
		}
		for _, at := range gpu.pm.vATs {
			held += at.HeldAccesses
		}
		for _, at := range gpu.pm.otherAT {
			held += at.HeldAccesses
		}
		if gpu.pm.engine != nil {
			pulled += gpu.pm.engine.PulledBytes
			served += gpu.pm.engine.ServedBytes
		}
	}
	if c.Target != pagemigration.TargetArgmax || c.CountLocal || c.CountNetwork {
		col("pm_target_code", float64(c.Target))
		col("pm_target_dominant", float64(s.TargetDominant))
		col("pm_target_median", float64(s.TargetMedian))
		col("pm_target_broadcast", float64(s.TargetBroadcast))
		col("pm_declined_same_place", float64(s.DeclinedSamePlace))
		col("pm_declined_small_saving", float64(s.DeclinedSmallSaving))
		col("pm_declined_broadcast", float64(s.DeclinedBroadcast))
		col("pm_local_counted", float64(s.LocalCounted))
		col("pm_replica_uncounted", float64(s.ReplicaUncounted))
	}
	r.reportAdaptStats(m)
	col("pm_held_accesses", float64(held))
	col("pm_dma_pulled_bytes", float64(pulled))
	col("pm_dma_served_bytes", float64(served))
	col("pm_check_enabled", boolMetric(c.Check))
	col("pm_checked_frames", float64(s.CheckedFrames))
	for dev, n := range s.RemoteReadsByOwner {
		if n > 0 {
			r.metricsCollector.Collect(fmt.Sprintf("GPU%d", dev),
				"pm_remote_reads_served", float64(n))
		}
	}
}
