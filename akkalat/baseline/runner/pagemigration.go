package runner

// ISCA'27 page-migration baseline: runtime page migration between GPMs
// (on-fault or access-counter triggered, 4-KB or 2-MB units). Off by
// default; with -page-migration=none nothing is built and nothing is hooked,
// so the simulator is unchanged. See mgpusim/timing/pagemigration.

import (
	"flag"
	"fmt"
	"math/bits"

	"github.com/sarchlab/akita/v3/mem/mem"
	"github.com/sarchlab/akita/v3/mem/vm"
	"github.com/sarchlab/akita/v3/mem/vm/addresstranslator"
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
	c.ChunkBytes = uint64(*pmChunkBytesFlag)
	c.PullWindow = *pmPullWindowFlag
	c.L2Window = *pmL2WindowFlag
	if *pmIdealFlag {
		c.SoftwareLatencyNS = 0
		c.ShootdownLatencyNS = 0
	}
	return c
}

func printPageMigrationConfig(c pagemigration.Config) {
	if !c.Enabled() {
		return
	}
	fmt.Printf("[Config] page-migration policy=%s unit=%dKB sw=%dns batch=%d "+
		"shootdown=%dns threshold=%d cooldown=%dns max-migrations=%d "+
		"chunk=%dB pull-window=%d l2-window=%d ideal=%v\n",
		c.Policy, (uint64(1)<<c.Log2Unit)>>10, c.SoftwareLatencyNS, c.BatchSize,
		c.ShootdownLatencyNS, c.Threshold, c.CooldownNS, c.MaxMigrations,
		c.ChunkBytes, c.PullWindow, c.L2Window, *pmIdealFlag)
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
}

func (b *R9NanoGPUBuilder) buildPageMigrationEngine() {
	b.pmEngine = nil
	if !b.pageMigration.Enabled() {
		return
	}
	b.pmEngine = pagemigration.NewCopyEngine(
		fmt.Sprintf("%s.PageMigrationDMA", b.gpuName), b.engine, b.pageMigration)
	if b.monitor != nil {
		b.monitor.RegisterComponent(b.pmEngine)
	}
}

func (b *R9NanoGPUBuilder) recordPageMigrationParts() {
	if b.pmEngine == nil {
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
		gpms[gpu.GPUID] = &pagemigration.GPM{
			DeviceID: gpu.GPUID,
			MemBase:  p.memBase,
			TLBs:     p.tlbs,
			Engine:   p.engine,
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
	col("pm_counter_triggers", float64(s.CounterTriggers))
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
		pulled += gpu.pm.engine.PulledBytes
		served += gpu.pm.engine.ServedBytes
	}
	col("pm_held_accesses", float64(held))
	col("pm_dma_pulled_bytes", float64(pulled))
	col("pm_dma_served_bytes", float64(served))
	for dev, n := range s.RemoteReadsByOwner {
		if n > 0 {
			r.metricsCollector.Collect(fmt.Sprintf("GPU%d", dev),
				"pm_remote_reads_served", float64(n))
		}
	}
}
